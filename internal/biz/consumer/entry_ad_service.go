package consumer

import (
	"context"
	"drama-server/internal/pkg/requesttime"
	"encoding/json"
	"log/slog"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/monetization"
)

// EntryAdStatus 是小程序起播前读取的服务端广告进度，不是播放授权。
type EntryAdStatus struct {
	// Scope 声明当前用户全局或按剧目的计数范围。
	Scope string `json:"scope,omitempty"`
	// CountBy 由 Mini 据此选择展示或完整观看计数，服务端不据此拦截播放。
	CountBy string `json:"countBy,omitempty"`
	// ShownCount 是对应范围内的成功展示次数；加载失败不计入。
	ShownCount int `json:"shownCount,omitempty"`
	// Enabled 表示当前应用已启用入剧广告规则。
	Enabled bool `json:"enabled"`
	// CompletedCount 是当前计数范围内已成功结算的入剧广告次数。
	CompletedCount int `json:"completedCount"`
	// RequiredCount 是当天进入短剧播放前必须完成的次数。
	RequiredCount int `json:"requiredCount"`
	// ProgressDate 是应用时区下的自然日日期。
	ProgressDate string `json:"progressDate,omitempty"`
	// ValidUntil 是下一个应用当地午夜的 UTC 时刻，客户端须在此前重新读取进度。
	ValidUntil string `json:"validUntil,omitempty"`
	// Offer 提供事实采集入口，由 Mini 判断是否仍需展示。
	Offer *monetization.AccessOffer `json:"offer,omitempty"`
}

// EntryAdStatus 读取当前剧目的规则及自然日事实，仅查询所配置的计数账本。
func (s *Service) EntryAdStatus(ctx context.Context, visitorID, dramaID string) (EntryAdStatus, error) {
	ctx = requesttime.Ensure(ctx, s.now)
	policy, err := s.ResolveMonetizationPolicy(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "resolve entry ad policy failed", "error", err)
		return EntryAdStatus{}, err
	}
	status, err := s.entryAdStatusForPolicy(ctx, visitorID, dramaID, policy, false)
	if err != nil {
		slog.ErrorContext(ctx, "resolve entry ad status failed", "error", err)
	}
	return status, err
}

// 已用权威线上目录确认目标的起播请求复用同一剧目和策略快照，旧入口仍执行原有可见性验证。
func (s *Service) entryAdStatusForPolicy(ctx context.Context, visitorID, dramaID string, policy monetization.MonetizationPolicy, verifiedDrama bool) (EntryAdStatus, error) {
	if !policy.Enabled() {
		return EntryAdStatus{}, nil
	}
	var selected *monetization.MonetizationPolicy
	for _, candidate := range policy.RulePolicies() {
		if candidate.Rule != nil && candidate.Rule.Type == "entry_rewarded_ad" {
			selected = &candidate
			break
		}
	}
	if selected == nil {
		return EntryAdStatus{}, nil
	}
	var config monetization.EntryRewardedAdConfig
	if err := json.Unmarshal(selected.Rule.Config, &config); err != nil {
		slog.ErrorContext(ctx, "decode entry ad rule failed", "error", err)
		return EntryAdStatus{}, err
	}
	period := monetization.RewardPeriodFor(requesttime.At(ctx, s.now), application.LocationFromContext(ctx))
	if config.EffectiveScope() == "user_drama" && dramaID == "" {
		slog.WarnContext(ctx, "entry ad target required by rule", "error", monetization.ErrInvalidAdShow)
		return EntryAdStatus{}, monetization.ErrInvalidAdShow
	}
	if dramaID != "" && !verifiedDrama {
		drama, err := s.FindDrama(ctx, dramaID)
		if err != nil {
			slog.ErrorContext(ctx, "check entry ad drama failed", "error", err)
			return EntryAdStatus{}, err
		}
		dramaID = drama.ID
	}
	countDramaID := ""
	if dramaID != "" && config.EffectiveScope() == "user_drama" {
		countDramaID = dramaID
	}
	status := EntryAdStatus{Enabled: true, RequiredCount: config.RequiredAdCount, ProgressDate: period.Key, ValidUntil: period.ExpiresAt.Format("2006-01-02T15:04:05Z07:00")}
	status.Scope, status.CountBy = config.EffectiveScope(), config.EffectiveCountBy()
	var err error
	if status.CountBy == "shown" {
		status.ShownCount, err = s.store.CountAdShows(ctx, visitorID, monetization.AdShowQuery{Scene: monetization.AdSceneEntryReward, DramaID: countDramaID, From: period.StartsAt, Until: period.ExpiresAt})
	} else {
		// 完整观看只读取奖励账本，不依赖展示表或展示上报确认。
		status.CompletedCount, err = s.store.CountEntryAdCompletions(ctx, visitorID, selected.ProgressKey(), countDramaID, period.StartsAt, period.ExpiresAt)
	}
	if err != nil {
		slog.ErrorContext(ctx, "count entry ad facts failed", "error", err)
		return EntryAdStatus{}, err
	}
	status.Offer, err = s.RewardOffer(ctx, visitorID, *selected, monetization.AccessOfferRewardedEntry, nil)
	if err != nil {
		slog.ErrorContext(ctx, "build entry ad offer failed", "error", err)
		return EntryAdStatus{}, err
	}
	return status, nil
}
