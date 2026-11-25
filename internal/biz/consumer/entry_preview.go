package consumer

import "context"

type entryPreviewContextKey struct{}

// WithEntryPreview 标记可以处理锁定首集片段的新版客户端请求。
func WithEntryPreview(ctx context.Context) context.Context {
	return context.WithValue(ctx, entryPreviewContextKey{}, true)
}

func supportsEntryPreview(ctx context.Context) bool {
	return ctx.Value(entryPreviewContextKey{}) == true
}

// EntryPreviewRepository 读写与当前主体、线上片源和秒数绑定的独立直连片段任务。
type EntryPreviewRepository interface {
	// FindOrQueueEntryPreview 返回已生成的片段 VID；等待生成时返回空字符串，失败时返回错误。
	// episode 必须来自当前应用已发布快照，仓储只能返回为该片源和秒数生成的产物。
	FindOrQueueEntryPreview(ctx context.Context, episode Episode, seconds int) (string, error)
}

// entryPlaybackPreview 保留锁定状态，直连签名只使用独立片段的 VID。
func (s *Service) entryPlaybackPreview(ctx context.Context, drama Drama, episode Episode, seconds int, official *PlaybackIdentity) (*EntryPlaybackPreview, error) {
	preview := &EntryPlaybackPreview{Seconds: seconds, Playback: official}
	if official != nil {
		return preview, nil
	}
	repository, ok := s.store.(EntryPreviewRepository)
	if !ok {
		return nil, ErrServiceUnavailable
	}
	vid, err := repository.FindOrQueueEntryPreview(ctx, episode, seconds)
	if err != nil {
		return nil, err
	}
	if vid == "" {
		return preview, nil
	}
	if vid == episode.VID {
		return nil, ErrServiceUnavailable
	}
	episode.VID = vid
	episode.DurationSeconds = seconds
	preview.Playback, err = s.playbackIdentity(ctx, drama, episode)
	if err != nil {
		return nil, err
	}
	return preview, nil
}
