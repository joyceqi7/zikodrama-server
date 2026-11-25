package monetization

import "testing"

func TestMiniReviewRetainsConfiguredEpisodeRulesOnly(t *testing.T) {
	doc := StrategyDocument{Enabled: true, FreeEpisodeCount: 3, Rules: []UnlockRule{
		{Code: "entry", Type: "entry_rewarded_ad", Enabled: true},
		{Code: "first", Type: "rewarded_ad", Enabled: true, Grants: []GrantTemplate{{Type: "episode_access"}}},
		{Code: "vip", Type: "rewarded_ad", Enabled: true, Grants: []GrantTemplate{{Type: "vip"}}},
		{Code: "mixed", Type: "rewarded_ad", Enabled: true, Grants: []GrantTemplate{{Type: "episode_access"}, {Type: "vip"}}},
		{Code: "disabled", Type: "rewarded_ad", Enabled: false, Grants: []GrantTemplate{{Type: "episode_access"}}},
		{Code: "second", Type: "rewarded_ad", Enabled: true, Grants: []GrantTemplate{{Type: "episode_access"}}},
	}}
	filtered := EpisodeOnlyDocument(doc)
	if len(filtered.Rules) != 3 || filtered.Rules[0].Code != "first" || filtered.Rules[1].Code != "disabled" || filtered.Rules[2].Code != "second" {
		t.Fatalf("逐集规则顺序变化: %+v", filtered.Rules)
	}
	if filtered.FreeEpisodeCount != 3 || filtered.InterstitialEnabled == nil || *filtered.InterstitialEnabled {
		t.Fatal("免费集数或插屏覆盖错误")
	}
	if filtered.Rules[1].Enabled {
		t.Fatal("审核不能启用原已停用的逐集规则")
	}
	if len(doc.Rules) != 6 || doc.InterstitialEnabled != nil {
		t.Fatal("审核覆盖污染运营配置")
	}
	if !HasEnabledEpisodeRule(doc) {
		t.Fatal("未识别可执行逐集规则")
	}
	doc.Enabled = false
	if HasEnabledEpisodeRule(doc) {
		t.Fatal("停用策略仍可开启审核")
	}
}
