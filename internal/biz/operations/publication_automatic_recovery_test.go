package operations_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"drama-server/internal/biz/operations"
)

// automaticRecoveryProvider 模拟外部已保存、后续查询暂不可用，计数区分查询与更新。
type automaticRecoveryProvider struct {
	publicationProviderStub
	queries               atomic.Int32
	failuresBeforeSuccess int32
}

// UpdateAlbum 模拟原更新已发出，但确认查询遇到临时故障。
func (p *automaticRecoveryProvider) UpdateAlbum(context.Context, operations.AlbumDraft) (operations.UpdateAlbumResult, error) {
	p.record("update-unknown")
	return operations.UpdateAlbumResult{}, &operations.PlatformCallError{Code: "album_update_unconfirmed", OutcomeUnknown: true, Retryable: true}
}

// ResolveAlbumUpdate 按测试配置返回若干次临时错误，之后核实原版本；不会再调用更新接口。
func (p *automaticRecoveryProvider) ResolveAlbumUpdate(_ context.Context, d operations.AlbumDraft) (operations.UpdateAlbumResult, error) {
	failures := p.failuresBeforeSuccess
	if failures == 0 {
		failures = 2
	}
	if p.queries.Add(1) <= failures {
		return operations.UpdateAlbumResult{}, &operations.PlatformCallError{Code: "album_update_unconfirmed", OutcomeUnknown: true, Retryable: true}
	}
	return operations.UpdateAlbumResult{Version: d.Version + 1, EpisodeIDsBySequence: map[int]string{1: "7685603384053909501"}}, nil
}

// TestManualAlbumRecoveryNeverRewrites 验证临时查询故障由 Worker 自动恢复且只更新一次。
func TestManualAlbumRecoveryNeverRewrites(t *testing.T) {
	p := &automaticRecoveryProvider{}
	s, _, id := publicationFixture(t, p)
	if _, err := submitPreparedPublication(t, s, context.Background(), id, operations.TikTokPublicationInput{}); err != nil {
		t.Fatal(err)
	}
	waitForPublicationStatus(t, s, id, operations.PublicationStatusFailed)
	if p.queries.Load() != 0 {
		t.Fatal("同步失败被 Worker 自动恢复")
	}
	attempts := int(p.failuresBeforeSuccess)
	if attempts == 0 {
		attempts = 2
	}
	for i := 0; i <= attempts; i++ {
		result, err := s.RetryTikTokPublication(context.Background(), id)
		if i < attempts && err == nil {
			t.Fatal("核对失败未返回错误")
		}
		if i == attempts && (err != nil || result.Status != operations.PublicationStatusSynced) {
			t.Fatal("最终核对未完成", result, err)
		}
	}
	waitForPublicationStatus(t, s, id, operations.PublicationStatusUnderReview)
	result, err := s.FindTikTokPublication(context.Background(), id)
	if err != nil || result.ExternalWriteStarted || result.PlatformVersion != 1 || result.Attempts != 0 || p.queries.Load() != 3 {
		t.Fatalf("自动核对没有恢复：%+v %v", result, err)
	}
	writes, reviews := 0, 0
	for _, call := range p.callSnapshot() {
		if call == "update-unknown" {
			writes++
		}
		if call == "review" {
			reviews++
		}
		if call == "list" {
			t.Fatal("未经确认上线")
		}
	}
	if writes != 1 || reviews != 1 {
		t.Fatalf("发生重复外部写入：%v", p.callSnapshot())
	}
}

// TestManualAlbumRecoveryAllowsRepeatedReadOnlyChecks 验证只读核对不会被通用失败次数提前终止。
func TestManualAlbumRecoveryAllowsRepeatedReadOnlyChecks(t *testing.T) {
	p := &automaticRecoveryProvider{failuresBeforeSuccess: 6}
	s, _, id := publicationFixture(t, p)
	if _, err := submitPreparedPublication(t, s, context.Background(), id, operations.TikTokPublicationInput{}); err != nil {
		t.Fatal(err)
	}
	waitForPublicationStatus(t, s, id, operations.PublicationStatusFailed)
	if p.queries.Load() != 0 {
		t.Fatal("同步失败被 Worker 自动恢复")
	}
	attempts := int(p.failuresBeforeSuccess)
	if attempts == 0 {
		attempts = 2
	}
	for i := 0; i <= attempts; i++ {
		result, err := s.RetryTikTokPublication(context.Background(), id)
		if i < attempts && err == nil {
			t.Fatal("核对失败未返回错误")
		}
		if i == attempts && (err != nil || result.Status != operations.PublicationStatusSynced) {
			t.Fatal("最终核对未完成", result, err)
		}
	}
	waitForPublicationStatus(t, s, id, operations.PublicationStatusUnderReview)
	result, err := s.FindTikTokPublication(context.Background(), id)
	if err != nil || result.Attempts != 0 || result.ExternalWriteStarted || result.ErrorCode != "" || p.queries.Load() != 7 {
		t.Fatalf("通用重试上限错误终止了安全核对：%+v %v，查询=%d", result, err, p.queries.Load())
	}
}

// progressLogBuffer 支持 Worker 写入与测试读取同时发生，避免日志断言引入数据竞争。
type progressLogBuffer struct {
	mu   sync.Mutex
	data bytes.Buffer
}

// Write 接收结构化日志并保留原始错误详情。
func (b *progressLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.Write(p)
}

// snapshot 返回已经记录的日志副本。
func (b *progressLogBuffer) snapshot() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.String()
}

// stalledTranscodeProvider 让同一官方任务长时间无进展，手动检查时才返回成功。
type stalledTranscodeProvider struct {
	publicationProviderStub
	ready atomic.Bool
}

// QueryTranscode 保留原任务身份及 VID，只切换官方处理状态。
func (p *stalledTranscodeProvider) QueryTranscode(ctx context.Context, taskID, accountID string) (operations.TikTokTranscodeObservation, error) {
	p.record("transcode-query")
	result, err := p.Capabilities.QueryTranscode(ctx, taskID, accountID)
	if !p.ready.Load() {
		result.Status = "PROCESSING"
		result.PendingVIDs = result.SuccessVIDs
		result.SuccessVIDs = nil
	}
	return result, err
}

// TestStalledTranscodeResumesOriginalTaskWithoutNewWrite 验证超时后的人工检查继续使用同一任务和原分集。
func TestStalledTranscodeResumesOriginalTaskWithoutNewWrite(t *testing.T) {
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	var output progressLogBuffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	p := &stalledTranscodeProvider{}
	s, _, id := publicationFixture(t, p, 15*time.Millisecond)
	first, err := submitPreparedPublication(t, s, context.Background(), id, operations.TikTokPublicationInput{})
	if err != nil {
		t.Fatal(err)
	}
	waitForPublicationStatus(t, s, id, operations.PublicationStatusFailed)
	stalled, err := s.FindTikTokPublication(context.Background(), id)
	if err != nil || stalled.ErrorCode != "transcode_stalled" || len(stalled.Transcodes) != 1 {
		t.Fatalf("无进展未暂停：%+v %v", stalled, err)
	}
	batch := stalled.Transcodes[0]
	if batch.StartedAt.IsZero() || !batch.LastProgressAt.IsZero() || !batch.LastCheckedAt.After(batch.StartedAt) || batch.Ready {
		t.Fatal("等待时间与进展没有区分")
	}
	logs := output.snapshot()
	if !strings.Contains(logs, `"msg":"TikTok transcode progress stalled"`) || !strings.Contains(logs, `"level":"WARN"`) || !strings.Contains(logs, `"error":"TikTok platform call failed: transcode_stalled"`) {
		t.Fatalf("停滞日志缺少操作、级别或实际原因：%s", logs)
	}
	for _, sensitive := range []string{"https://cdn.example.test/cover.png", "TEST_ONLY_SECRET", "vid-1"} {
		if strings.Contains(logs, sensitive) {
			t.Fatal("停滞日志泄漏媒资或凭据")
		}
	}
	if strings.Contains(logs, `"level":"ERROR"`) {
		t.Fatalf("正常等待被误报为系统故障：%s", logs)
	}
	p.ready.Store(true)
	if _, err := s.RetryTikTokPublication(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	waitForPublicationStatus(t, s, id, operations.PublicationStatusUnderReview)
	done, err := s.FindTikTokPublication(context.Background(), id)
	if err != nil || done.ID != first.ID || done.Transcodes[0].TaskID != batch.TaskID || len(done.Transcodes[0].CompletedVIDs) != 1 || !done.Transcodes[0].StartedAt.Equal(batch.StartedAt) {
		t.Fatalf("没有复用原转码任务：%+v %v", done, err)
	}
	starts := 0
	for _, call := range p.callSnapshot() {
		if call == "transcode-start" {
			starts++
		}
	}
	if starts != 1 {
		t.Fatalf("重复转码：%v", p.callSnapshot())
	}
}
