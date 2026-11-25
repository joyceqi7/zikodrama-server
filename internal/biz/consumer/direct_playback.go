package consumer

import (
	"context"
	"drama-server/internal/biz/application"
	"log/slog"
	"time"
)

// DirectPlaybackProvider 只在服务器验权后签发临时凭据，并读取公共 SDK 配置。
type DirectPlaybackProvider interface {
	// Configuration 返回服务端装配的通道及基础版能力，不下发 License、SDK AppID 或长期凭据。
	Configuration(context.Context) (PlaybackSDKConfig, error)
	// Sign 仅对当前已验权剧集按配置文件有效期签发短时凭据，并返回对应到期时间；失败不返回凭据。
	Sign(context.Context, Episode) (string, time.Time, error)
}

// ConfigureDirectPlayback 在组合根注入直连签名能力，官方通道不使用此依赖。
func (s *Service) ConfigureDirectPlayback(p DirectPlaybackProvider) { s.directPlayback = p }

// PlaybackConfiguration 按可信应用通道返回公开 SDK 配置，直连使用已装配的基础版能力。
func (s *Service) PlaybackConfiguration(ctx context.Context) (PlaybackSDKConfig, error) {
	if !application.IsDirect(ctx) {
		return PlaybackSDKConfig{Provider: PlaybackProviderTikTokMinis, Channel: PlaybackChannelBytePlus}, nil
	}
	if s.directPlayback == nil {
		slog.WarnContext(ctx, "direct consumer playback configuration unavailable", "error", ErrServiceUnavailable)
		return PlaybackSDKConfig{}, ErrServiceUnavailable
	}
	config, err := s.directPlayback.Configuration(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "read direct consumer playback configuration failed", "error", err)
	}
	return config, err
}

// playbackIdentity 为已授权的正片或独立直连片段生成播放参数。
func (s *Service) playbackIdentity(ctx context.Context, d Drama, e Episode) (*PlaybackIdentity, error) {
	if !application.IsDirect(ctx) {
		return officialPlaybackIdentity(d, e), nil
	}
	if s.directPlayback == nil {
		slog.WarnContext(ctx, "direct consumer playback signing unavailable", "error", ErrServiceUnavailable)
		return nil, ErrServiceUnavailable
	}
	token, expires, err := s.directPlayback.Sign(ctx, e)
	if err != nil || token == "" || !expires.After(time.Now().Add(5*time.Second)) {
		if err == nil {
			err = ErrServiceUnavailable
		}
		slog.ErrorContext(ctx, "sign direct playback failed", "drama_id", d.ID, "episode_id", e.ID, "error", err)
		return nil, ErrServiceUnavailable
	}
	return &PlaybackIdentity{Channel: PlaybackChannelBytePlus, VID: e.VID, PlayAuthToken: token, ExpiresAt: expires.UTC().Format(time.RFC3339), PosterURL: e.PosterURL, Title: e.Title, EpisodeNumber: e.Number, DurationSecond: e.DurationSeconds}, nil
}
