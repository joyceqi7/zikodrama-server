package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	domain "drama-server/internal/biz/consumer"
)

const proxiedMediaUploadTimeout = 30 * time.Minute

// ReserveEpisodeMediaUpload 接收入口已校验的文件信息，在直传前检查短剧可编辑状态并登记上传预留任务。
// 返回的短期授权携带上传任务标识，供上传确认和提前到达的 VOD 回调定位同一个任务。
func (s *DramaService) ReserveEpisodeMediaUpload(
	ctx context.Context,
	episodeID string,
	input EpisodeMediaUploadReservationInput,
) (*EpisodeMediaUploadReservation, error) {
	if s == nil || s.repositories.Content.Catalog == nil || s.repositories.Media.Jobs == nil {
		slog.WarnContext(ctx, "reserve episode media upload rejected", "error", ErrMediaNotConfigured)
		return nil, ErrMediaNotConfigured
	}
	episode, err := s.repositories.Content.Catalog.FindEpisode(ctx, episodeID)
	if err != nil {
		slog.ErrorContext(ctx, "find episode failed", "error", err)
		return nil, err
	}
	drama, err := s.repositories.Content.Catalog.FindDrama(ctx, episode.DramaID)
	if err != nil {
		slog.ErrorContext(ctx, "find drama failed", "error", err)
		return nil, err
	}
	mutable, err := s.episodeContentMutable(ctx, *drama, *episode)
	if err != nil {
		slog.ErrorContext(ctx, "check reserved upload boundary failed", "error", err)
		return nil, err
	}
	if !mutable {
		slog.WarnContext(ctx, "reserve episode media upload rejected", "error", ErrConflict)
		return nil, ErrConflict
	}
	authorization, err := s.MediaUploadAuthorization(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "media upload authorization failed", "error", err)
		return nil, err
	}
	if input.Size > authorization.MaxFileBytes {
		slog.WarnContext(ctx, "reserve episode media upload rejected", "error", ErrInvalidInput)
		return nil, ErrInvalidInput
	}
	now := time.Now().UTC()
	job := MediaJob{Kind: MediaJobUpload,
		DramaID: drama.ID, EpisodeID: episodeID, ExpectedVID: episode.VID,
		ResourceClass: MediaResourceVideo, SpaceName: authorization.SpaceName,
		FileName: input.FileName, Size: input.Size, Status: MediaJobReserved,
		CreatedAt: now, UpdatedAt: now, NextAttemptAt: now}
	if err := s.repositories.Media.Jobs.CreateMediaJob(ctx, &job); err != nil {
		slog.ErrorContext(ctx, "reserve episode upload task failed", "error", err)
		return nil, err
	}
	callback, err := json.Marshal(map[string]string{"uploadId": job.ID, "dramaId": drama.ID, "episodeId": episode.ID})
	if err != nil {
		slog.ErrorContext(ctx, "encode episode upload callback failed", "error", err)
		return nil, err
	}
	authorization.CallbackArgs = string(callback)
	return &EpisodeMediaUploadReservation{UploadID: job.ID, Authorization: *authorization}, nil
}

// RefreshEpisodeMediaUploadAuthorization 校验原预留任务仍可上传，并为同一任务续发短期授权。
// episodeID 和 uploadID 必须同时匹配，已进入处理或终态的任务不能重新取得上传权限。
func (s *DramaService) RefreshEpisodeMediaUploadAuthorization(ctx context.Context, episodeID, uploadID string) (*MediaUploadAuthorization, error) {
	if s == nil || s.repositories.Content.Catalog == nil || s.repositories.Media.Jobs == nil {
		slog.WarnContext(ctx, "refresh episode media upload authorization rejected", "error", ErrMediaNotConfigured)
		return nil, ErrMediaNotConfigured
	}
	episode, err := s.repositories.Content.Catalog.FindEpisode(ctx, strings.TrimSpace(episodeID))
	if err != nil {
		slog.ErrorContext(ctx, "find episode for upload authorization refresh failed", "error", err)
		return nil, err
	}
	drama, err := s.repositories.Content.Catalog.FindDrama(ctx, episode.DramaID)
	if err != nil {
		slog.ErrorContext(ctx, "find drama for upload authorization refresh failed", "error", err)
		return nil, err
	}
	mutable, err := s.episodeContentMutable(ctx, *drama, *episode)
	if err != nil {
		slog.ErrorContext(ctx, "check refreshed upload boundary failed", "error", err)
		return nil, err
	}
	if !mutable {
		slog.WarnContext(ctx, "refresh episode media upload authorization rejected", "error", ErrConflict)
		return nil, ErrConflict
	}
	job, err := s.repositories.Media.Jobs.FindMediaJob(ctx, strings.TrimSpace(uploadID))
	if err != nil {
		slog.ErrorContext(ctx, "find upload reservation for authorization refresh failed", "error", err)
		return nil, err
	}
	if job.Kind != MediaJobUpload || job.Status != MediaJobReserved || job.DramaID != drama.ID || job.EpisodeID != episode.ID {
		slog.WarnContext(ctx, "refresh episode media upload authorization rejected", "error", ErrConflict)
		return nil, ErrConflict
	}
	authorization, err := s.MediaUploadAuthorization(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "refresh media upload authorization failed", "error", err)
		return nil, err
	}
	callback, err := json.Marshal(map[string]string{"uploadId": job.ID, "dramaId": drama.ID, "episodeId": episode.ID})
	if err != nil {
		slog.ErrorContext(ctx, "encode refreshed episode upload callback failed", "error", err)
		return nil, err
	}
	authorization.CallbackArgs = string(callback)
	return authorization, nil
}

// MediaUploadAuthorization 返回前端直传 BytePlus VOD 所需的短期授权。
func (s *DramaService) MediaUploadAuthorization(ctx context.Context) (*MediaUploadAuthorization, error) {
	if s == nil || s.media == nil {
		slog.WarnContext(ctx, "media upload authorization rejected", "error", ErrMediaNotConfigured)
		return nil, ErrMediaNotConfigured
	}
	direct, ok := s.media.(DirectMediaProvider)
	if !ok {
		slog.WarnContext(ctx, "media upload authorization rejected", "error", ErrMediaNotConfigured)
		return nil, ErrMediaNotConfigured
	}
	authorization, err := direct.UploadAuthorization(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "upload authorization failed", "error", err)
		return nil, err
	}
	// 这里检查的是 Provider 返回的运行配置完整性，不能由 HTTP 字段校验替代。
	if authorization.Mode != MediaUploadModeDirect || strings.TrimSpace(authorization.AppID) == "" ||
		strings.TrimSpace(authorization.SpaceName) == "" || strings.TrimSpace(authorization.Region) == "" ||
		authorization.MaxFileBytes <= 0 || strings.TrimSpace(authorization.Token.AccessKeyID) == "" ||
		strings.TrimSpace(authorization.Token.SecretAccessKey) == "" || strings.TrimSpace(authorization.Token.SessionToken) == "" ||
		strings.TrimSpace(authorization.Token.CurrentTime) == "" || strings.TrimSpace(authorization.Token.ExpiredTime) == "" {
		slog.WarnContext(ctx, "media upload authorization rejected", "error", ErrMediaNotConfigured)
		return nil, ErrMediaNotConfigured
	}
	return &authorization, nil
}

// UploadEpisodeMedia 在浏览器无法建立 VOD multipart 会话时，由服务端使用官方 SDK 流式上传已预留的视频并入队处理。
func (s *DramaService) UploadEpisodeMedia(ctx context.Context, episodeID, uploadID string, upload Upload) (*MediaJob, error) {
	if s == nil || s.repositories.Content.Catalog == nil || s.repositories.Media.Jobs == nil {
		slog.WarnContext(ctx, "proxy episode media upload rejected", "error", ErrMediaNotConfigured)
		return nil, ErrMediaNotConfigured
	}
	provider, ok := s.media.(ProxiedMediaUploadProvider)
	if !ok {
		slog.WarnContext(ctx, "proxy episode media upload rejected", "error", ErrMediaNotConfigured)
		return nil, ErrMediaNotConfigured
	}
	// multipart 已完整落入服务端临时文件后，即使浏览器或反向代理结束原请求，也必须完成远端上传确认和任务入库，避免产生孤儿 VID。
	operationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), proxiedMediaUploadTimeout)
	defer cancel()
	episode, err := s.repositories.Content.Catalog.FindEpisode(operationCtx, strings.TrimSpace(episodeID))
	if err != nil {
		slog.ErrorContext(ctx, "find episode for proxy upload failed", "error", err)
		return nil, err
	}
	drama, err := s.repositories.Content.Catalog.FindDrama(operationCtx, episode.DramaID)
	if err != nil {
		slog.ErrorContext(ctx, "find drama for proxy upload failed", "error", err)
		return nil, err
	}
	mutable, err := s.episodeContentMutable(operationCtx, *drama, *episode)
	if err != nil {
		slog.ErrorContext(ctx, "check proxy upload boundary failed", "error", err)
		return nil, err
	}
	if !mutable {
		slog.WarnContext(ctx, "proxy episode media upload rejected", "error", ErrConflict)
		return nil, ErrConflict
	}
	reserved, err := s.repositories.Media.Jobs.FindMediaJob(operationCtx, strings.TrimSpace(uploadID))
	if err != nil {
		slog.ErrorContext(ctx, "find proxy upload reservation failed", "error", err)
		return nil, err
	}
	if reserved.Kind != MediaJobUpload || (reserved.Status != MediaJobReserved && reserved.Status != MediaJobUploading) || reserved.DramaID != drama.ID || reserved.EpisodeID != episode.ID || reserved.FileName != upload.FileName || reserved.Size != upload.Size {
		slog.WarnContext(ctx, "proxy episode media upload rejected", "error", ErrConflict)
		return nil, ErrConflict
	}
	callback, err := json.Marshal(map[string]string{"uploadId": reserved.ID, "dramaId": drama.ID, "episodeId": episode.ID})
	if err != nil {
		slog.ErrorContext(ctx, "encode proxy episode upload callback failed", "error", err)
		return nil, err
	}
	upload.CallbackArgs = string(callback)
	asset, err := provider.UploadEpisodeMedia(operationCtx, upload)
	if err != nil {
		slog.ErrorContext(ctx, "proxy episode media upload failed", "error", err)
		return nil, err
	}
	job, err := s.QueueDirectEpisodeMedia(operationCtx, episode.ID, "proxy-upload-"+reserved.ID, DirectMediaUploadInput{
		UploadID: reserved.ID, VID: asset.VID, UploadRequestID: asset.UploadRequestID, FileName: upload.FileName, Size: upload.Size,
	})
	if err != nil {
		slog.ErrorContext(ctx, "queue proxied episode media failed", "error", err)
		return nil, err
	}
	return job, nil
}

// BeginProxiedEpisodeMediaUpload 将服务端代传任务标记为上传中并返回可轮询的任务；文件上传由调用方在后台执行。
func (s *DramaService) BeginProxiedEpisodeMediaUpload(ctx context.Context, episodeID, uploadID string) (*MediaJob, error) {
	if s == nil || s.repositories.Media.Jobs == nil {
		slog.WarnContext(ctx, "begin proxy episode media upload rejected", "error", ErrMediaNotConfigured)
		return nil, ErrMediaNotConfigured
	}
	job, err := s.repositories.Media.Jobs.FindMediaJob(ctx, strings.TrimSpace(uploadID))
	if err != nil {
		slog.ErrorContext(ctx, "find proxy upload task failed", "error", err)
		return nil, err
	}
	if job.Kind != MediaJobUpload || job.EpisodeID != strings.TrimSpace(episodeID) || job.Status != MediaJobReserved {
		slog.WarnContext(ctx, "begin proxy episode media upload rejected", "error", ErrConflict)
		return nil, ErrConflict
	}
	job.Status = MediaJobUploading
	job.UpdatedAt = time.Now().UTC()
	if err := s.repositories.Media.Jobs.UpdateMediaJob(ctx, job); err != nil {
		slog.ErrorContext(ctx, "mark proxy upload task uploading failed", "error", err)
		return nil, err
	}
	return &job, nil
}

// FailProxiedEpisodeMediaUpload 将后台代传失败持久化为可见的终态，避免任务因进程或远端错误永久停留在上传中。
func (s *DramaService) FailProxiedEpisodeMediaUpload(ctx context.Context, uploadID string, uploadErr error) error {
	if s == nil || s.repositories.Media.Jobs == nil {
		return ErrMediaNotConfigured
	}
	job, err := s.repositories.Media.Jobs.FindMediaJob(ctx, strings.TrimSpace(uploadID))
	if err != nil {
		slog.ErrorContext(ctx, "find failed proxy upload task failed", "error", err)
		return err
	}
	if job.Status != MediaJobUploading {
		return nil
	}
	job.Status = MediaJobFailed
	job.Error = truncateMediaError(uploadErr.Error())
	job.UpdatedAt = time.Now().UTC()
	if err := s.repositories.Media.Jobs.UpdateMediaJob(ctx, job); err != nil {
		slog.ErrorContext(ctx, "mark proxy upload task failed", "error", err)
		return err
	}
	return nil
}

// UploadCover 接收入口已检查类型、大小和内容的封面流；先建上传任务，再上传并回填官方 MID 与空间信息。
// 上传失败保留失败任务，后续内容保存通过 UploadID 消费上传任务并保存封面到剧。
func (s *DramaService) UploadCover(ctx context.Context, upload Upload) (*CoverAsset, error) {
	if s == nil || s.media == nil || s.repositories.Media.Jobs == nil {
		slog.WarnContext(ctx, "upload cover rejected", "error", ErrMediaNotConfigured)
		return nil, ErrMediaNotConfigured
	}
	provider, ok := s.media.(CoverProvider)
	if !ok {
		slog.WarnContext(ctx, "upload cover rejected", "error", ErrMediaNotConfigured)
		return nil, ErrMediaNotConfigured
	}
	normalized := upload
	now := time.Now().UTC()
	job := MediaJob{Kind: MediaJobCover,
		ResourceClass: MediaResourceMaterial, FileName: upload.FileName, Size: upload.Size,
		Status: MediaJobReserved, CreatedAt: now, UpdatedAt: now, NextAttemptAt: now}
	if err := s.repositories.Media.Jobs.CreateMediaJob(ctx, &job); err != nil {
		slog.ErrorContext(ctx, "reserve cover upload task failed", "error", err)
		return nil, err
	}
	callback, err := json.Marshal(map[string]string{"uploadId": job.ID})
	if err != nil {
		slog.ErrorContext(ctx, "encode cover upload callback failed", "error", err)
		return nil, err
	}
	normalized.CallbackArgs = string(callback)
	asset, err := provider.UploadCover(ctx, normalized)
	if err != nil {
		slog.ErrorContext(ctx, "upload cover failed", "error", err)
		job.Status = MediaJobFailed
		job.Error = truncateMediaError(err.Error())
		job.UpdatedAt = time.Now().UTC()
		if updateErr := s.repositories.Media.Jobs.UpdateMediaJob(context.WithoutCancel(ctx), job); updateErr != nil {
			slog.ErrorContext(ctx, "save failed cover upload task failed", "error", updateErr)
		}
		return nil, err
	}
	asset.MID = strings.TrimSpace(defaultString(asset.MID, asset.ID))
	asset.ID = asset.MID
	asset.URL = strings.TrimSpace(asset.URL)
	asset.SpaceName = strings.TrimSpace(asset.SpaceName)
	asset.ProviderFileName = strings.TrimSpace(asset.ProviderFileName)
	if asset.MID == "" || asset.URL == "" || asset.SpaceName == "" || asset.ProviderFileName == "" {
		slog.WarnContext(ctx, "upload cover rejected", "error", ErrInvalidInput)
		return nil, ErrInvalidInput
	}
	job.SpaceName = asset.SpaceName
	job.RemoteVID = asset.MID
	job.ProviderFileName = asset.ProviderFileName
	job.PublicURL = asset.URL
	job.Status = MediaJobUploaded
	job.UpdatedAt = time.Now().UTC()
	if err := s.repositories.Media.Jobs.UpdateMediaJob(ctx, job); err != nil {
		slog.ErrorContext(ctx, "save cover upload result failed", "error", err)
		return nil, err
	}
	asset.UploadID = job.ID
	return &asset, nil
}

// attachEpisodeMedia 将官方已确认的媒资绑定到分集；expectedVID 是任务创建时的旧 VID，用于识别并发替换。
// 分集被删除、内容冻结或绑定冲突时安排新媒资清理；成功保存后再唤醒状态校准。
func (s *DramaService) attachEpisodeMedia(ctx context.Context, episodeID, expectedVID, uploadID string, asset MediaAsset) (*domain.Episode, error) {
	if s == nil || s.repositories.Content.Catalog == nil || s.repositories.Media.Jobs == nil || strings.TrimSpace(episodeID) == "" || strings.TrimSpace(asset.VID) == "" {
		slog.WarnContext(ctx, "attach episode media rejected", "error", ErrInvalidInput)
		return nil, ErrInvalidInput
	}
	episode, err := s.repositories.Content.Catalog.FindEpisode(ctx, strings.TrimSpace(episodeID))
	if err != nil {
		slog.ErrorContext(ctx, "find episode failed", "error", err)
		cleanupErr := s.cleanupUnattachedMedia(ctx, uploadID, asset.VID, err)
		if cleanupErr != nil {
			logMediaJobError(ctx, "attach episode media failed after cleanup attempt", uploadID, cleanupErr)
		}
		return nil, cleanupErr
	}
	drama, err := s.repositories.Content.Catalog.FindDrama(ctx, episode.DramaID)
	if err != nil {
		slog.ErrorContext(ctx, "find drama failed", "error", err)
		cleanupErr := s.cleanupUnattachedMedia(ctx, uploadID, asset.VID, err)
		if cleanupErr != nil {
			logMediaJobError(ctx, "attach episode media failed after cleanup attempt", uploadID, cleanupErr)
		}
		return nil, cleanupErr
	}
	mutable, err := s.episodeContentMutable(ctx, *drama, *episode)
	if err != nil {
		slog.ErrorContext(ctx, "check media binding boundary failed", "error", err)
		return nil, err
	}
	if !mutable || episode.VID != expectedVID {
		cleanupErr := s.cleanupUnattachedMedia(ctx, uploadID, asset.VID, ErrConflict)
		if cleanupErr != nil {
			logMediaJobError(ctx, "attach episode media failed after cleanup attempt", uploadID, cleanupErr)
		}
		return nil, cleanupErr
	}
	job, err := s.repositories.Media.Jobs.FindMediaJob(ctx, uploadID)
	if err != nil {
		slog.ErrorContext(ctx, "find upload task for episode binding failed", "error", err)
		return nil, err
	}
	episode.MediaSpaceName = job.SpaceName
	episode.VID = strings.TrimSpace(asset.VID)
	episode.MediaStatus = defaultString(asset.Status, "ready")
	if strings.TrimSpace(asset.PosterURL) != "" {
		episode.PosterURL = strings.TrimSpace(asset.PosterURL)
	}
	if asset.DurationSeconds > 0 {
		episode.DurationSeconds = asset.DurationSeconds
	}
	// 仓储在保存时再次比较 expectedVID，关闭读取分集与提交绑定之间的并发窗口。
	if err := s.repositories.Content.Catalog.SaveEpisodeMedia(ctx, *episode, *drama, expectedVID, uploadID); err != nil {
		slog.ErrorContext(ctx, "save episode media failed", "error", err)
		cleanupErr := s.cleanupUnattachedMedia(ctx, uploadID, asset.VID, err)
		if cleanupErr != nil {
			logMediaJobError(ctx, "attach episode media failed after cleanup attempt", uploadID, cleanupErr)
		}
		return nil, cleanupErr
	}
	s.notifyMediaWorker()
	s.notifyMediaReconciliation()
	return episode, nil
}

// finalizeDirectEpisodeMedia 在任务截止时间内查询官方直传结果，再将确认就绪的视频绑定到原分集。
// 先检查当前 VID 仍等于任务快照，并核对官方返回 VID，避免旧任务覆盖新上传。
func (s *DramaService) finalizeDirectEpisodeMedia(ctx context.Context, job MediaJob) (*domain.Episode, error) {
	if s == nil || s.repositories.Content.Catalog == nil || s.repositories.Media.Jobs == nil || strings.TrimSpace(job.RemoteVID) == "" {
		slog.WarnContext(ctx, "finalize direct episode media rejected", "error", ErrMediaNotConfigured)
		return nil, ErrMediaNotConfigured
	}
	direct, ok := s.media.(DirectMediaProvider)
	if !ok {
		slog.WarnContext(ctx, "finalize direct episode media rejected", "error", ErrMediaNotConfigured)
		return nil, ErrMediaNotConfigured
	}
	episode, err := s.repositories.Content.Catalog.FindEpisode(ctx, job.EpisodeID)
	if err != nil {
		logMediaJobError(ctx, "find media job episode failed", job.ID, err)
		return nil, err
	}
	drama, err := s.repositories.Content.Catalog.FindDrama(ctx, episode.DramaID)
	if err != nil {
		logMediaJobError(ctx, "find media job drama failed", job.ID, err)
		return nil, err
	}
	if episode.VID == job.RemoteVID {
		return episode, nil
	}
	mutable, err := s.episodeContentMutable(ctx, *drama, *episode)
	if err != nil {
		logMediaJobError(ctx, "check media finalization boundary failed", job.ID, err)
		return nil, err
	}
	if !mutable || episode.VID != job.ExpectedVID {
		slog.WarnContext(ctx, "finalize direct episode media rejected", "error", ErrConflict)
		return nil, ErrConflict
	}
	asset, err := direct.FinalizeUpload(ctx, job.RemoteVID, job.UploadRequestID)
	if err != nil {
		logMediaJobError(ctx, "finalize upload failed", job.ID, err)
		return nil, err
	}
	if strings.TrimSpace(asset.VID) == "" || strings.TrimSpace(asset.VID) != strings.TrimSpace(job.RemoteVID) {
		slog.WarnContext(ctx, "finalize direct episode media rejected", "error", ErrInvalidInput)
		return nil, ErrInvalidInput
	}

	result1, resultErr := s.attachEpisodeMedia(ctx, job.EpisodeID, job.ExpectedVID, job.ID, asset)
	if resultErr != nil {
		logMediaJobError(ctx, "attach episode media failed", job.ID, resultErr)
	}
	return result1, resultErr
}

// cleanupUnattachedMedia 为未能绑定的远端产物安排持久化删除任务，返回原业务错误以及可能发生的清理错误。
// 收尾使用脱离请求取消的五秒上下文，只在成功提交删除任务后记录入队，实际删除由 Worker 再次检查引用。
func (s *DramaService) cleanupUnattachedMedia(ctx context.Context, uploadID, providerID string, cause error) error {
	persistCtx, cancel := mediaJobPersistenceContext(ctx)
	defer cancel()
	job, err := s.repositories.Media.Jobs.FindMediaJob(persistCtx, uploadID)
	if err != nil {
		slog.ErrorContext(ctx, "find abandoned upload task failed", "job_id", uploadID, "error", err)
		return errors.Join(cause, err)
	}
	job.RemoteVID = providerID
	if err := s.repositories.Media.Jobs.QueueMediaDeletion(persistCtx, job); err != nil {
		slog.ErrorContext(ctx, "queue abandoned video deletion failed", "job_id", uploadID, "error", err)
		return errors.Join(cause, err)
	}
	s.notifyMediaWorker()
	slog.InfoContext(ctx, "unattached media cleanup queued", "job_id", uploadID)
	return cause
}

// wrapMediaCleanupError 为清理失败附加操作和资产 ID，保留错误链；无错误时返回 nil，便于合并多个收尾结果。
func wrapMediaCleanupError(action, uploadID string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s %s: %w", action, uploadID, err)
}

// notifyMediaWorker 尝试唤醒已配置的本进程队列；任务事实已先写入数据库，未运行 Worker 时不影响入队结果。
func (s *DramaService) notifyMediaWorker() {
	if s != nil && s.mediaJobs != nil {
		s.mediaJobs.notify()
	}
}

// notifyMediaReconciliation 提醒本进程扫描已到期的媒资观察项，数据库中的 ReconcileAfter 决定实际处理范围。
func (s *DramaService) notifyMediaReconciliation() {
	if s != nil && s.mediaJobs != nil {
		s.mediaJobs.notifyReconciliation()
	}
}

// defaultString 返回去空白的非空值，否则沿用调用方指定的回退值。
func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}
