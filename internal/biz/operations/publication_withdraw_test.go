package operations_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"drama-server/internal/biz/operations"
)

// TestFailedWithdrawReconcilesOfficialTerminalState 验证旧撤回失败任务重试时同步平台终态，保留编辑及人工上线门禁。
func TestFailedWithdrawReconcilesOfficialTerminalState(t *testing.T) {
	for _, test := range []struct {
		name, status, stage, publishStatus string
		reviewStatus                       int
		appealState                        int
	}{
		{"审核被拒", operations.PublicationStatusRejected, operations.PublicationStagePollReview, "rejected", 3, 0},
		{"整版被拒且单项申诉被拒", operations.PublicationStatusRejected, operations.PublicationStagePollReview, "rejected", 3, 7},
		{"整版被拒且单项申诉通过", operations.PublicationStatusRejected, operations.PublicationStagePollReview, "rejected", 3, 6},
		{"审核通过", operations.PublicationStatusApproved, operations.PublicationStageAwaitPublish, "publishing", 2, 0},
		{"申诉通过", operations.PublicationStatusApproved, operations.PublicationStageAwaitPublish, "publishing", 6, 0},
		{"申诉被拒", operations.PublicationStatusRejected, operations.PublicationStagePollReview, "rejected", 7, 0},
		{"平台已撤回", operations.PublicationStatusRejected, operations.PublicationStageDone, "draft", 4, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			provider := &reviewProviderStub{publicationProviderStub: publicationProviderStub{reviewStatus: 1}}
			service, store, id := publicationFixture(t, provider)
			if _, err := submitPreparedPublication(t, service, ctx, id, operations.TikTokPublicationInput{}); err != nil {
				t.Fatal(err)
			}
			waitForPublicationStatus(t, service, id, operations.PublicationStatusUnderReview)
			provider.mu.Lock()
			provider.reviewStatus = test.reviewStatus
			provider.itemState = test.appealState
			provider.mu.Unlock()
			p, err := store.FindTikTokPublicationByDramaID(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if test.appealState != 0 {
				p.Appeal = &operations.TikTokReviewAppealInput{ItemType: 1, ItemID: "7637420375425239060", Reason: "测试申诉"}
			}
			p.Status, p.Stage, p.ErrorCode = operations.PublicationStatusFailed, operations.PublicationStageWithdrawReview, "review_action_conflict"
			if err := store.UpdateTikTokPublication(ctx, p, nil); err != nil {
				t.Fatal(err)
			}
			if _, err := service.RetryTikTokPublication(ctx, id); err != nil {
				t.Fatal(err)
			}
			waitForPublicationStatus(t, service, id, test.status)
			result, err := service.FindTikTokPublication(ctx, id)
			if err != nil || result.Stage != test.stage || result.ReviewStatus != test.reviewStatus || result.ExternalWriteStarted || result.PublishRequested {
				t.Fatalf("撤回任务没有同步平台终态: %+v %v", result, err)
			}
			drama, err := service.FindDrama(ctx, id)
			if err != nil || drama.PublishStatus != "ready" || drama.Published != nil {
				t.Fatalf("编辑或上线门禁不符合官方状态: %+v %v", drama, err)
			}
			provider.mu.Lock()
			defer provider.mu.Unlock()
			if provider.withdrawCalls != 0 {
				t.Fatalf("已结束的审核仍调用撤回: %d", provider.withdrawCalls)
			}
		})
	}
}

// TestWithdrawRecoveryLogsStateAndQueryFailure 验证恢复日志记录实际审核状态及查询故障，且不携带凭据或媒体地址。
func TestWithdrawRecoveryLogsStateAndQueryFailure(t *testing.T) {
	for _, queryFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "查询恢复成功", true: "恢复查询也失败"}[queryFails], func(t *testing.T) {
			original := slog.Default()
			var logs progressLogBuffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(original) })
			provider := &reviewProviderStub{publicationProviderStub: publicationProviderStub{reviewStatus: 1}, afterWithdrawError: 2,
				withdrawError: &operations.PlatformCallError{Code: "api_rejected"}}
			if queryFails {
				provider.queryErrorAfterWithdraw = errors.New("review query temporarily unavailable")
			}
			ctx := context.Background()
			service, _, id := publicationFixture(t, provider)
			if _, err := submitPreparedPublication(t, service, ctx, id, operations.TikTokPublicationInput{}); err != nil {
				t.Fatal(err)
			}
			waitForPublicationStatus(t, service, id, operations.PublicationStatusUnderReview)
			if _, err := service.WithdrawTikTokReview(ctx, id); (err != nil) != queryFails {
				t.Fatal("同步撤回错误传播不符", err)
			}
			status, marker := operations.PublicationStatusApproved, `"outcome":"withdraw_skipped_review_completed"`
			if queryFails {
				status, marker = operations.PublicationStatusFailed, `"outcome":"failed"`
			}
			waitForPublicationStatus(t, service, id, status)
			deadline := time.Now().Add(time.Second)
			for !strings.Contains(logs.snapshot(), marker) && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			output := logs.snapshot()
			if !strings.Contains(output, marker) || !strings.Contains(output, `"platform_version":1`) {
				t.Fatalf("撤回恢复日志缺少终态或版本: %s", output)
			}
			if queryFails {
				if !strings.Contains(output, "query withdraw review recovery failed") || !strings.Contains(output, "review query temporarily unavailable") {
					t.Fatalf("恢复查询的实际故障详情丢失: %s", output)
				}
			} else if !strings.Contains(output, `"review_status":2`) {
				t.Fatalf("日志未记录最新官方审核状态: %s", output)
			}
			for _, secret := range []string{"TEST_ONLY_SECRET", "https://cdn.example.test/cover.png", "vid-1"} {
				if strings.Contains(output, secret) {
					t.Fatalf("恢复日志包含敏感信息: %s", secret)
				}
			}
		})
	}
}

// TestWithdrawWaitsForReviewVisibility 验证查询暂时返回未送审时等待可见状态，随后只撤回一次。
func TestWithdrawWaitsForReviewVisibility(t *testing.T) {
	ctx := context.Background()
	provider := &reviewProviderStub{publicationProviderStub: publicationProviderStub{reviewStatus: 1}, queryStatuses: []int{0, 1}}
	service, id := startReviewFixture(t, provider)
	waitForPublicationStatus(t, service, id, operations.PublicationStatusUnderReview)
	if _, err := service.WithdrawTikTokReview(ctx, id); err == nil {
		t.Fatal("未就绪的撤回未返回错误")
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusFailed)
	if _, err := service.RetryTikTokPublication(ctx, id); err != nil {
		t.Fatal(err)
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusRejected)
	p, err := service.FindTikTokPublication(ctx, id)
	if err != nil || p.ReviewStatus != 4 || p.ErrorCode != "review_withdrawn" {
		t.Fatalf("等待平台可见后没有完成撤回: %+v %v", p, err)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.withdrawCalls != 1 {
		t.Fatalf("撤回调用次数不正确: %d", provider.withdrawCalls)
	}
}

// TestWithdrawErrorReconcilesWithoutReplaying 验证请求期间完成审核或丢失响应时的查询恢复及未知写入保护。
func TestWithdrawErrorReconcilesWithoutReplaying(t *testing.T) {
	for _, test := range []struct {
		name, status, code string
		after              int
		unknown            bool
	}{
		{"已撤回但响应丢失", operations.PublicationStatusRejected, "review_withdrawn", 4, true},
		{"明确拒绝且审核被拒", operations.PublicationStatusRejected, "review_rejected", 3, false},
		{"明确拒绝且审核已通过", operations.PublicationStatusApproved, "", 2, false},
		{"明确拒绝且申诉被拒", operations.PublicationStatusRejected, "review_rejected", 7, false},
		{"结果未知且尚未确认撤回", operations.PublicationStatusFailed, "external_write_uncertain", 2, true},
		{"明确拒绝且仍在审核", operations.PublicationStatusFailed, "api_rejected", 1, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			code := "api_rejected"
			if test.unknown {
				code = "network_error"
			}
			provider := &reviewProviderStub{publicationProviderStub: publicationProviderStub{reviewStatus: 1}, afterWithdrawError: test.after,
				withdrawError: &operations.PlatformCallError{Code: code, OutcomeUnknown: test.unknown, Retryable: test.unknown}}
			service, id := startReviewFixture(t, provider)
			waitForPublicationStatus(t, service, id, operations.PublicationStatusUnderReview)
			if _, err := service.WithdrawTikTokReview(ctx, id); (err != nil) != (test.status == operations.PublicationStatusFailed) {
				t.Fatal("同步撤回错误传播不符", err)
			}
			waitForPublicationStatus(t, service, id, test.status)
			p, err := service.FindTikTokPublication(ctx, id)
			if err != nil || p.ErrorCode != test.code || p.ExternalWriteStarted != (test.code == "external_write_uncertain") || p.PublishRequested {
				t.Fatalf("撤回错误的恢复结果不正确: %+v %v", p, err)
			}
			provider.mu.Lock()
			defer provider.mu.Unlock()
			if provider.withdrawCalls != 1 {
				t.Fatalf("恢复查询时重复撤回: %d", provider.withdrawCalls)
			}
		})
	}
}

// TestWithdrawUnknownReviewStatusRemainsFrozen 验证未识别的审核状态保留冻结，并记录实际官方数值。
func TestWithdrawUnknownReviewStatusRemainsFrozen(t *testing.T) {
	ctx := context.Background()
	provider := &reviewProviderStub{publicationProviderStub: publicationProviderStub{reviewStatus: 8}}
	service, id := startReviewFixture(t, provider)
	waitForPublicationStatus(t, service, id, operations.PublicationStatusUnderReview)
	if _, err := service.WithdrawTikTokReview(ctx, id); err == nil {
		t.Fatal("未知审核状态未返回错误")
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusFailed)
	p, err := service.FindTikTokPublication(ctx, id)
	if err != nil || p.ReviewStatus != 8 || p.ErrorCode != "unknown_review_status" || p.ExternalWriteStarted {
		t.Fatalf("未保留未知官方状态: %+v %v", p, err)
	}
	drama, err := service.FindDrama(ctx, id)
	if err != nil || drama.PublishStatus != "ready" {
		t.Fatalf("未知状态提前解除编辑锁: %+v %v", drama, err)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.withdrawCalls != 0 {
		t.Fatalf("未知状态仍写入平台: %d", provider.withdrawCalls)
	}
}

// TestWithdrawPendingAppealWithOldRejectedStatus 验证整版旧驳回不会阻止仍在处理的单项申诉撤回。
func TestWithdrawPendingAppealWithOldRejectedStatus(t *testing.T) {
	ctx := context.Background()
	provider := &reviewProviderStub{publicationProviderStub: publicationProviderStub{reviewStatus: 1}}
	service, store, id := publicationFixture(t, provider)
	if _, err := submitPreparedPublication(t, service, ctx, id, operations.TikTokPublicationInput{}); err != nil {
		t.Fatal(err)
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusUnderReview)
	p, err := store.FindTikTokPublicationByDramaID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	p.Stage = operations.PublicationStagePollAppeal
	p.Appeal = &operations.TikTokReviewAppealInput{ItemType: 1, ItemID: "7637420375425239060", Reason: "测试申诉"}
	if err := store.UpdateTikTokPublication(ctx, p, nil); err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	provider.reviewStatus, provider.itemState = 3, 5
	provider.mu.Unlock()
	if _, err := service.WithdrawTikTokReview(ctx, id); err != nil {
		t.Fatal(err)
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusRejected)
	result, err := service.FindTikTokPublication(ctx, id)
	if err != nil || result.ReviewStatus != 4 || result.ErrorCode != "review_withdrawn" {
		t.Fatalf("申诉未结束时没有完成撤回: %+v %v", result, err)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.withdrawCalls != 1 {
		t.Fatalf("申诉撤回调用次数不正确: %d", provider.withdrawCalls)
	}
}
