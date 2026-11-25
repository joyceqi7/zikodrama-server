package consumer

import (
	domain "drama-server/internal/biz/consumer"
	"encoding/json"
	"strings"
	"testing"
)

// TestPostbackValidation 覆盖旧格式、白名单、标识脱敏、精度与错误消息处理。
func TestPostbackValidation(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"legacy", `{"kind":"tt_ad_show_success"}`, true},
		{"diagnostic", `{"kind":"tt_ad_show_unknown","postback":{"scene":"entry_reward","clientVersion":"abc123","params":{"open_id":"[REDACTED]","minis_drama_id":"9007199254740993","if_last_ep":null,"if_first_time":0},"warnings":["missing_previous_episode"],"elapsedMs":10000,"reason":"callback_timeout"}}`, true},
		{"identity", `{"kind":"tt_ad_show_success","postback":{"scene":"entry_reward","clientVersion":"abc","params":{"open_id":"private"}}}`, false},
		{"extra", `{"kind":"tt_ad_show_success","postback":{"scene":"entry_reward","clientVersion":"abc","params":{"token":"private"}}}`, false},
		{"flag", `{"kind":"tt_ad_show_success","postback":{"scene":"entry_reward","clientVersion":"abc","params":{"if_last_ep":"1"}}}`, false},
		{"flag_range", `{"kind":"tt_ad_show_success","postback":{"scene":"entry_reward","clientVersion":"abc","params":{"if_last_ep":2}}}`, false},
		{"event_params", `{"kind":"tt_video_request_success","postback":{"scene":"playback","clientVersion":"abc","params":{"ad_type":"RewardedVideoAd"}}}`, false},
		{"other_metric", `{"kind":"launch","postback":{"scene":"playback","clientVersion":"abc","params":{}}}`, false},
		{"negative_time", `{"kind":"tt_ad_show_failure","postback":{"scene":"vip_reward","clientVersion":"abc","params":{},"elapsedMs":-1}}`, false},
		{"code", `{"kind":"tt_ad_show_failure","postback":{"scene":"vip_reward","clientVersion":"abc","params":{},"errorCode":"token=private"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var event domain.MetricEvent
			if err := json.Unmarshal([]byte(tc.body), &event); err != nil {
				t.Fatal(err)
			}
			if got := validatePostback(&event); got != tc.valid {
				t.Fatalf("valid=%v want %v", got, tc.valid)
			}
		})
	}
	event := domain.MetricEvent{Kind: "tt_ad_show_failure", Postback: &domain.PostbackDetails{Scene: "vip_reward", ClientVersion: "abc", Params: map[string]any{}, ErrorMessage: "token=private https://private.test/x open_id=private " + strings.Repeat("中", 600)}}
	if !validatePostback(&event) || strings.Contains(event.Postback.ErrorMessage, "private") || len([]rune(event.Postback.ErrorMessage)) != 512 {
		t.Fatalf("脱敏或长度错误：%d", len(event.Postback.ErrorMessage))
	}
}

// TestPostbackAuthorizationRedaction 验证共享脱敏规则同时覆盖回传消息，保留实际错误描述。
func TestPostbackAuthorizationRedaction(t *testing.T) {
	event := domain.MetricEvent{Kind: "tt_ad_show_failure", Postback: &domain.PostbackDetails{
		Scene: "entry_reward", ClientVersion: "build", Params: map[string]any{},
		ErrorMessage: "SDK rejected; Authorization: Bearer private-value; retryable",
	}}
	if !validatePostback(&event) || event.Postback.ErrorMessage != "SDK rejected; Authorization: [REDACTED]; retryable" {
		t.Fatalf("回传凭据脱敏错误: %s", event.Postback.ErrorMessage)
	}
}

// TestPostbackCookieRedaction 验证共享脱敏规则覆盖多个 Cookie，保留后续行的实际故障。
func TestPostbackCookieRedaction(t *testing.T) {
	event := domain.MetricEvent{Kind: "tt_ad_show_failure", Postback: &domain.PostbackDetails{
		Scene: "entry_reward", ClientVersion: "build", Params: map[string]any{},
		ErrorMessage: "SDK rejected\nCookie: a=private-first; session=private-second\nbridge unavailable",
	}}
	if !validatePostback(&event) || event.Postback.ErrorMessage != "SDK rejected\nCookie: [REDACTED]\nbridge unavailable" {
		t.Fatal("共享 Cookie 脱敏错误或故障说明丢失")
	}
}
