package consumer

import (
	domain "drama-server/internal/biz/consumer"
	"encoding/json"
	"testing"
)

// TestAdMetricValidation 覆盖旧客户端、敏感字段、枚举、耗时和证据约束。
func TestAdMetricValidation(t *testing.T) {
	valid := `{"kind":"ad_shown","ad":{"adType":"rewarded","adUnitId":"slot-1","scene":"entry_reward","clientVersion":"abc","os":"ios","tiktokVersion":"46.1.0","stage":"show","showElapsedMs":100,"evidence":"show_promise"}}`
	for _, tc := range []struct {
		name, body string
		want       bool
	}{{"valid", valid, true}, {"legacy", `{"kind":"rewarded_show"}`, true}, {"missing", `{"kind":"ad_request"}`, false}, {"credentials", `{"kind":"ad_request","ad":{"adType":"rewarded","adUnitId":"https://secret.test","scene":"entry_reward","clientVersion":"abc","os":"ios","tiktokVersion":"46.1.0","stage":"show"}}`, false}} {
		t.Run(tc.name, func(t *testing.T) {
			var event domain.MetricEvent
			if err := json.Unmarshal([]byte(tc.body), &event); err != nil {
				t.Fatal(err)
			}
			if validateAdMetric(&event) != tc.want {
				t.Fatal("unexpected validation")
			}
		})
	}
	for _, mutate := range []func(*domain.AdMetricDetails){func(p *domain.AdMetricDetails) { p.Evidence = "" }, func(p *domain.AdMetricDetails) { x := -1.0; p.ElapsedMS = &x }, func(p *domain.AdMetricDetails) { p.ErrorCode = "secret" }, func(p *domain.AdMetricDetails) { p.Stage = "close" }, func(p *domain.AdMetricDetails) { p.Scene = "pause_interstitial" }} {
		var event domain.MetricEvent
		_ = json.Unmarshal([]byte(valid), &event)
		mutate(event.Ad)
		if validateAdMetric(&event) {
			t.Fatal("invalid detail accepted")
		}
	}
}

// TestAdMetricRemovedLoadStage 验证通用失败与未知事件也不能携带已移除的加载阶段。
func TestAdMetricRemovedLoadStage(t *testing.T) {
	for _, kind := range []string{"ad_failure", "ad_show_unknown", "ad_close_unknown"} {
		t.Run(kind, func(t *testing.T) {
			event := domain.MetricEvent{Kind: kind, Ad: &domain.AdMetricDetails{
				AdType: "rewarded", AdUnitID: "slot-1", Scene: "entry_reward", ClientVersion: "build-direct",
				OS: "ios", TikTokVersion: "46.1.0", Stage: "load",
			}}
			if validateAdMetric(&event) {
				t.Fatal("已移除的加载阶段不应通过采集校验")
			}
			event.Ad.Stage = "show"
			if !validateAdMetric(&event) {
				t.Fatal("直接展示流程的原有故障事件应继续接受")
			}
		})
	}
}

// TestAdContainerClassification 验证内部码独立保留，通用主码不能单独解释为 50008。
func TestAdContainerClassification(t *testing.T) {
	event := domain.MetricEvent{Kind: "ad_failure", Ad: &domain.AdMetricDetails{AdType: "rewarded", AdUnitID: "slot", Scene: "entry_reward", ClientVersion: "build", OS: "ios", TikTokVersion: "46.1", Stage: "show", Reason: "container_unavailable", ErrorCode: "101150000", SubErrorCode: "50000", InnerErrorCode: "50008"}}
	if !validateAdMetric(&event) {
		t.Fatal("独立内部码被拒绝")
	}
	event.Ad.InnerErrorCode = ""
	if validateAdMetric(&event) {
		t.Fatal("通用主码不能推断 50008")
	}
	event.Ad.SubErrorCode = "50008"
	if !validateAdMetric(&event) {
		t.Fatal("明确子码被拒绝")
	}
	for _, reason := range []string{"not_visible", "stale_intent", "container_suspended"} {
		event.Kind, event.Ad.Stage, event.Ad.Reason = "ad_skipped", "gate", reason
		if !validateAdMetric(&event) {
			t.Fatal("本地门禁被拒绝")
		}
	}
}
