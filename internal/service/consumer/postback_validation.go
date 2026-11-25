package consumer

import (
	domain "drama-server/internal/biz/consumer"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"
)

var postbackVersionPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,64}$`)
var postbackCodePattern = regexp.MustCompile(`^-?[0-9]{1,10}$`)
var postbackURLPattern = regexp.MustCompile(`(?i)https?://[^\s"'<>]+`)

// Cookie 的分号连接多个凭据；未加引号的字段隐藏到行末，后续行的故障说明保留。
var postbackCookiePattern = regexp.MustCompile(`(?i)(["']?(?:cookie|set-cookie)["']?[ \t]*[=:][ \t]*)(?:"(?:\\.|[^"\\\r\n])*"|'(?:\\.|[^'\\\r\n])*'|[^\r\n]*)`)

// Authorization 的值可能带 Bearer 方案，必须连同后面的凭据一起消费，不能只替换方案名称。
var postbackCredentialPattern = regexp.MustCompile(`(?i)(bearer\s+|["']?(?:[a-z_-]*(?:token|secret|password|signature|cookie)|authorization|code|open_?id)["']?\s*[=:]\s*)(?:bearer\s+)?(?:"[^"]*"|'[^']*'|[^\s,;]+)`)

// validatePostback 在 HTTP 边界拒绝额外字段及真实平台身份，旧版纯计数仍然有效。
func validatePostback(event *domain.MetricEvent) bool {
	p := event.Postback
	if p == nil {
		return true
	}
	if !strings.HasPrefix(event.Kind, "tt_") || !postbackVersionPattern.MatchString(p.ClientVersion) {
		return false
	}
	switch p.Scene {
	case "playback", "entry_reward", "episode_unlock", "vip_reward", "home_interstitial", "pause_interstitial":
	default:
		return false
	}
	switch p.Reason {
	case "", "sdk_rejected", "exception", "unsupported", "identity_changed", "identity_missing", "callback_timeout", "invalid_params":
	default:
		return false
	}
	if p.ElapsedMS != nil && (math.IsNaN(*p.ElapsedMS) || math.IsInf(*p.ElapsedMS, 0) || *p.ElapsedMS < 0 || *p.ElapsedMS > 86400000) {
		return false
	}
	if p.ErrorCode != "" && !postbackCodePattern.MatchString(p.ErrorCode) || p.SubErrorCode != "" && !postbackCodePattern.MatchString(p.SubErrorCode) {
		return false
	}
	if len(p.Warnings) > 3 || p.Params == nil || len(p.Params) > 9 {
		return false
	}
	if p.Warnings == nil {
		p.Warnings = []string{}
	}
	for _, warning := range p.Warnings {
		switch warning {
		case "missing_drama_context", "missing_previous_episode", "unknown_last_episode":
		default:
			return false
		}
	}
	ad := strings.HasPrefix(event.Kind, "tt_ad_show_")
	for key, value := range p.Params {
		switch key {
		case "if_last_ep", "if_first_time":
			if key == "if_first_time" && !ad {
				return false
			}
			if value != nil {
				n, ok := value.(float64)
				if !ok || n != 0 && n != 1 {
					return false
				}
			}
		case "minis_drama_name", "minis_drama_id", "episode_number", "minis_name", "minis_id", "open_id", "ad_type":
			text, ok := value.(string)
			if !ok || !utf8.ValidString(text) || utf8.RuneCountInString(text) > 256 {
				return false
			}
			if !ad && (key == "minis_name" || key == "minis_id" || key == "open_id" || key == "ad_type") {
				return false
			}
			if key == "open_id" && text != "" && text != "[REDACTED]" {
				return false
			}
			if key == "ad_type" && text != "RewardedVideoAd" && text != "InterstitialAd" {
				return false
			}
		default:
			return false
		}
	}
	p.ErrorMessage = postbackURLPattern.ReplaceAllString(p.ErrorMessage, "[URL]")
	p.ErrorMessage = postbackCookiePattern.ReplaceAllString(p.ErrorMessage, "${1}[REDACTED]")
	p.ErrorMessage = postbackCredentialPattern.ReplaceAllString(p.ErrorMessage, "${1}[REDACTED]")
	runes := []rune(p.ErrorMessage)
	if len(runes) > 512 {
		p.ErrorMessage = string(runes[:512])
	}
	return true
}
