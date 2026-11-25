package operations

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
)

// registerVideos 逐集登记并查询官方媒资事实，每次外部调用后立即保存进度。
func (runner *publicationJobRunner) registerVideos(ctx context.Context, publication TikTokPublication, owner application.Application, drama domain.Drama, provider TikTokMediaProvider) {
	if len(publication.Videos) != len(drama.Episodes) || len(publication.Videos) == 0 {
		runner.fail(ctx, publication, &PlatformCallError{Code: "invalid_video_plan"})
		return
	}
	for index := range publication.Videos {
		video := &publication.Videos[index]
		if video.Status == "ready" {
			continue
		}
		if video.VID != drama.Episodes[index].VID || video.EpisodeID != drama.Episodes[index].ID {
			runner.fail(ctx, publication, &PlatformCallError{Code: "video_identity_mismatch"})
			return
		}
		if video.Status == "pending" {
			result, err := provider.RegisterVideo(ctx, TikTokVideoInput{VID: video.VID, Title: drama.Episodes[index].Title,
				Region:    publication.OwnerScope.Region,
				SpaceName: owner.BytePlusSpaceName, BytePlusAccountID: owner.BytePlusAccountID})
			if err != nil {
				slog.ErrorContext(ctx, "register video failed", "error", err)
				var platformErr *PlatformCallError
				if errors.As(err, &platformErr) && platformErr.OutcomeUnknown {
					// 已知 VID 可通过官方查询核实；将检查点转为查询，自动及人工重试均不重发登记。
					video.JobID, video.Status = "", "processing"
				}
				runner.fail(ctx, publication, err)
				return
			}
			video.JobID, video.Status = result.JobID, "processing"
			runner.requeueMediaStage(ctx, publication, 0, "video_registered")
			return
		}
		result, err := provider.QueryVideo(ctx, video.JobID, video.VID, owner.BytePlusAccountID)
		if err != nil {
			slog.ErrorContext(ctx, "query video failed", "error", err)
			runner.fail(ctx, publication, err)
			return
		}
		switch result.Status {
		case 1:
			runner.requeueMediaStage(ctx, publication, runner.mediaPollDelay(), "video_registration_waiting")
		case 2:
			if result.VID != video.VID {
				runner.fail(ctx, publication, &PlatformCallError{Code: "video_identity_mismatch"})
				return
			}
			video.Status = "ready"
			runner.requeueMediaStage(ctx, publication, 0, "video_confirmed")
		default:
			runner.fail(ctx, publication, &PlatformCallError{Code: "video_registration_rejected"})
		}
		return
	}
	runner.advance(ctx, publication, nextPublicationMediaStage(publication), nil)
}

// transcodeVideos 由官方转码模板保证播放器兼容性，不再自建 HLS-only 就绪规则。
func (runner *publicationJobRunner) transcodeVideos(ctx context.Context, publication TikTokPublication, owner application.Application, provider TikTokMediaProvider) {
	if publication.SkipTranscode {
		// 新同步明确使用源视频，恢复处理也不能自动新增转码。
		runner.advance(ctx, publication, nextPublicationMediaStage(publication), nil)
		return
	}
	for _, video := range publication.Videos {
		if video.Status != "ready" {
			runner.fail(ctx, publication, &PlatformCallError{Code: "video_not_registered"})
			return
		}
	}
	// 先保存稳定批次，再调用官方转码；重启后不能因重新分组改变幂等键的含义。
	if len(publication.Transcodes) == 0 {
		vids := []string{}
		seen := map[string]bool{}
		for _, video := range publication.Videos {
			if !seen[video.VID] {
				vids = append(vids, video.VID)
				seen[video.VID] = true
			}
		}
		if len(vids) == 0 {
			runner.fail(ctx, publication, &PlatformCallError{Code: "invalid_video_plan"})
			return
		}
		for start := 0; start < len(vids); start += 1000 {
			publication.Transcodes = append(publication.Transcodes, PublicationTranscode{VIDs: append([]string(nil), vids[start:min(start+1000, len(vids))]...)})
		}
		runner.requeueMediaStage(ctx, publication, 0, "transcode_plan_created")
		return
	}
	for index := range publication.Transcodes {
		batch := &publication.Transcodes[index]
		if batch.Ready {
			continue
		}
		if batch.TaskID == "" {
			// 新任务使用持久化随机前缀，避免不同环境的自增 ID 共用平台幂等键。
			// 历史任务继续使用原算法，响应丢失后不能擅自换键重发。
			batch.IdempotencyKey = fmt.Sprintf("%s-transcode-%d", publication.ID, index)
			if publication.TranscodeKeySeed != "" {
				batch.IdempotencyKey = fmt.Sprintf("%s-%d", publication.TranscodeKeySeed, index)
			}
			taskID, err := provider.StartTranscode(ctx, TikTokTranscodeInput{IdempotencyKey: batch.IdempotencyKey, VIDs: batch.VIDs,
				SpaceName: owner.BytePlusSpaceName, BytePlusAccountID: owner.BytePlusAccountID})
			if err != nil {
				slog.ErrorContext(ctx, "start transcode failed", "error", err)
				runner.fail(ctx, publication, err)
				return
			}
			if strings.TrimSpace(taskID) == "" {
				runner.fail(ctx, publication, &PlatformCallError{Code: "invalid_transcode_response"})
				return
			}
			batch.TaskID = taskID
			now := time.Now().UTC()
			batch.StartedAt, batch.MonitoringStartedAt = now, now
			runner.requeueMediaStage(ctx, publication, runner.mediaPollDelay(), "transcode_started")
			return
		}
		result, err := provider.QueryTranscode(ctx, batch.TaskID, owner.BytePlusAccountID)
		if err != nil {
			slog.ErrorContext(ctx, "query transcode failed", "error", err)
			runner.fail(ctx, publication, err)
			return
		}
		if result.TaskID != batch.TaskID || result.TotalCount != len(batch.VIDs) {
			runner.fail(ctx, publication, &PlatformCallError{Code: "invalid_transcode_response"})
			return
		}
		if len(result.FailedVIDs) > 0 || result.Status == "FAILED" {
			runner.fail(ctx, publication, &PlatformCallError{Code: "transcode_failed"})
			return
		}
		if result.Status == "PROCESSING" {
			now := time.Now().UTC()
			if !recordTranscodeProgress(batch, result.SuccessVIDs, now) {
				runner.fail(ctx, publication, &PlatformCallError{Code: "invalid_transcode_response"})
				return
			}
			waitingSince := batch.LastProgressAt
			if batch.MonitoringStartedAt.After(waitingSince) {
				waitingSince = batch.MonitoringStartedAt
			}
			if now.Sub(waitingSince) >= runner.mediaStallTimeout {
				runner.fail(ctx, publication, &PlatformCallError{Code: "transcode_stalled"})
				return
			}
			runner.requeueMediaStage(ctx, publication, runner.mediaPollDelay(), "transcode_waiting")
			return
		}
		// SUCCESS 总状态不足以证明全部视频完成，必须逐个核对 VID 集合和未完成项。
		if result.Status != "SUCCESS" || len(result.PendingVIDs) > 0 || !sameVideoSet(batch.VIDs, result.SuccessVIDs) {
			runner.fail(ctx, publication, &PlatformCallError{Code: "invalid_transcode_response"})
			return
		}
		if !recordTranscodeProgress(batch, result.SuccessVIDs, time.Now().UTC()) {
			runner.fail(ctx, publication, &PlatformCallError{Code: "invalid_transcode_response"})
			return
		}
		batch.Ready = true
		runner.requeueMediaStage(ctx, publication, 0, "transcode_confirmed")
		return
	}
	runner.advance(ctx, publication, nextPublicationMediaStage(publication), nil)
}

// recordTranscodeProgress 只接受属于原批次且不倒退的成功集合；轮询成功不等于分集取得进展。
func recordTranscodeProgress(batch *PublicationTranscode, completed []string, now time.Time) bool {
	allowed := make(map[string]bool, len(batch.VIDs))
	for _, vid := range batch.VIDs {
		allowed[vid] = true
	}
	seen := make(map[string]bool, len(completed))
	for _, vid := range completed {
		if !allowed[vid] || seen[vid] {
			return false
		}
		seen[vid] = true
	}
	for _, vid := range batch.CompletedVIDs {
		if !seen[vid] {
			return false
		}
	}
	if batch.StartedAt.IsZero() {
		batch.StartedAt = now
	}
	if batch.MonitoringStartedAt.IsZero() {
		batch.MonitoringStartedAt = now
	}
	if len(completed) > len(batch.CompletedVIDs) {
		batch.LastProgressAt = now
	}
	batch.CompletedVIDs = append([]string(nil), completed...)
	batch.LastCheckedAt = now
	return true
}

// sameVideoSet 比较完整集合，拒绝重复、缺失或替换 VID 的成功响应。
func sameVideoSet(expected, actual []string) bool {
	if len(expected) == 0 || len(expected) != len(actual) {
		return false
	}
	seen := map[string]bool{}
	for _, vid := range expected {
		seen[vid] = true
	}
	for _, vid := range actual {
		if !seen[vid] {
			return false
		}
		delete(seen, vid)
	}
	return len(seen) == 0
}

// requeueMediaStage 保留当前阶段，以正常等待状态轮询异步官方任务；成功交互清除连续技术失败次数。
func (runner *publicationJobRunner) requeueMediaStage(ctx context.Context, publication TikTokPublication, delay time.Duration, outcome string) {
	publication.Status, publication.ErrorCode, publication.ErrorMessage = PublicationStatusQueued, "", ""
	// 已确认的分集不能消耗后续分集的重试额度，也不能让其初次故障继承指数退避。
	publication.Attempts = 0
	publication.NextAttemptAt = time.Now().UTC().Add(delay)
	runner.persist(ctx, publication, nil, outcome)
	runner.service.notifyPublicationWorker()
}
