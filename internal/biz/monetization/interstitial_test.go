package monetization

import (
	"encoding/json"
	"testing"
)

// TestInterstitialPolicyPreservesLegacyDefault 验证旧 JSON 默认开启、显式关闭可持久化，且不改变激励规则。
func TestInterstitialPolicyPreservesLegacyDefault(t *testing.T) {
	for _, test := range []struct {
		name, field           string
		strategyEnabled, want bool
	}{
		{"legacy", "", true, true},
		{"enabled", `,"interstitialEnabled":true`, true, true},
		{"disabled", `,"interstitialEnabled":false`, true, false},
		{"strategy disabled", `,"interstitialEnabled":true`, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := `{"schemaVersion":1,"enabled":true,"freeEpisodeCount":3,"rules":[{"code":"episode","type":"rewarded_ad","enabled":true,"config":{"requiredAdCount":1},"grants":[{"type":"episode_access","config":{"selection":"next_locked_episodes","count":2,"validity":"unlimited"}}]}]` + test.field + `}`
			var doc StrategyDocument
			if err := json.Unmarshal([]byte(raw), &doc); err != nil {
				t.Fatal(err)
			}
			doc.Enabled = test.strategyEnabled
			saved, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			var restored StrategyDocument
			if err := json.Unmarshal(saved, &restored); err != nil {
				t.Fatal(err)
			}
			if err := DefaultStrategies().Validate(restored); err != nil {
				t.Fatal(err)
			}
			p := (ApplicationMonetizationConfig{Document: &restored, Bindings: map[string]RuleBinding{"episode": {AdUnitID: "reward"}}}).Policy()
			if p.AllowsInterstitial() != test.want {
				t.Fatalf("插屏门禁不符: %s", saved)
			}
			if p.FreeEpisodeCount != 3 || p.EpisodeReward.UnlockEpisodeCount != 2 || p.EpisodeAdUnitCode != "reward" {
				t.Fatalf("已有激励规则被插屏开关改变: %+v", p)
			}
		})
	}
	if !(ApplicationMonetizationConfig{Status: MonetizationEnabled}).Policy().AllowsInterstitial() {
		t.Fatal("无文档的旧配置不应默认关闭插屏")
	}
}
