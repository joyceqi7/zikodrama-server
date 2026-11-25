package consumer

import (
	domain "drama-server/internal/biz/consumer"
	"math"
	"regexp"
	"strings"
)

var adPlacementPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)
var adVersionPattern = regexp.MustCompile(`^(unknown|[0-9]+(?:\.[0-9]+){0,4})$`)

// validateAdMetric 在入口限定广告诊断字段，不能把原始 SDK 参数写入统计。
func validateAdMetric(event *domain.MetricEvent) bool {
	p := event.Ad
	if !strings.HasPrefix(event.Kind, "ad_") {
		return p == nil
	}
	if p == nil || event.Postback != nil || !adPlacementPattern.MatchString(p.AdUnitID) || !postbackVersionPattern.MatchString(p.ClientVersion) || len(p.TikTokVersion) > 32 || !adVersionPattern.MatchString(p.TikTokVersion) {
		return false
	}
	switch p.AdType {
	case "rewarded", "interstitial":
	default:
		return false
	}
	switch p.Scene {
	case "entry_reward", "episode_unlock", "vip_reward", "home_interstitial", "pause_interstitial":
	default:
		return false
	}
	if p.AdType == "rewarded" && (p.Scene == "home_interstitial" || p.Scene == "pause_interstitial") || p.AdType == "interstitial" && p.Scene != "home_interstitial" && p.Scene != "pause_interstitial" {
		return false
	}
	switch p.OS {
	case "ios", "android", "unknown":
	default:
		return false
	}
	switch p.Stage {
	case "capability", "gate", "create", "setup", "show", "callback", "close", "destroy":
	default:
		return false
	}
	switch p.Reason {
	case "", "unsupported", "busy", "not_visible", "stale_intent", "container_unavailable", "container_suspended", "cooldown", "exception", "sdk_error", "ad_active_conflict", "user_cancel", "not_completed", "callback_timeout", "container_destroyed":
	default:
		return false
	}
	switch p.Evidence {
	case "", "show_promise", "completed_close":
	default:
		return false
	}
	for _, v := range []*float64{p.ElapsedMS, p.ShowElapsedMS} {
		if v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0 || *v > 86400000) {
			return false
		}
	}
	if p.ErrorCode != "" && !postbackCodePattern.MatchString(p.ErrorCode) || p.SubErrorCode != "" && !postbackCodePattern.MatchString(p.SubErrorCode) || p.InnerErrorCode != "" && !postbackCodePattern.MatchString(p.InnerErrorCode) {
		return false
	}
	if p.Reason == "ad_active_conflict" && p.ErrorCode != "11006" && p.ErrorCode != "104111006" && p.SubErrorCode != "11006" {
		return false
	}
	if p.Reason == "container_unavailable" && (event.Kind != "ad_failure" || p.SubErrorCode != "50008" && p.InnerErrorCode != "50008") {
		return false
	}
	if (p.Reason == "container_suspended" || p.Reason == "stale_intent" || p.Reason == "not_visible") && (event.Kind != "ad_skipped" || p.Stage != "gate") {
		return false
	}
	// 每个阶段有固定来源，只有明确展示或完播才能携带对应事实。
	expected := map[string]string{"ad_trigger": "gate", "ad_create": "create", "ad_created": "create", "ad_request": "show", "ad_shown": "show", "ad_complete": "close", "ad_closed": "close"}
	if stage, ok := expected[event.Kind]; ok && p.Stage != stage {
		return false
	}
	if event.Kind == "ad_shown" && (p.Evidence == "" || p.ShowElapsedMS == nil) {
		return false
	}
	if event.Kind == "ad_complete" && p.AdType != "rewarded" {
		return false
	}
	return true
}
