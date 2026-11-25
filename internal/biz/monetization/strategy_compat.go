package monetization

import "encoding/json"

// StrategyFromConfig 将旧广告配置入口转换为独立规则，数据库仅保存转换后的 JSON。
// 此适配供既有调用方平滑切换；新后台直接管理 StrategyDocument。
func StrategyFromConfig(c ApplicationMonetizationConfig) (StrategyDocument, map[string]RuleBinding) {
	if c.Document != nil {
		return *c.Document, c.Bindings
	}
	marshal := func(v any) json.RawMessage { raw, _ := json.Marshal(v); return raw }
	count := max(1, c.EpisodeReward.UnlockEpisodeCount)
	d := StrategyDocument{SchemaVersion: 1, Enabled: c.Enabled(), FreeEpisodeCount: c.FreeEpisodeCount, Rules: []UnlockRule{
		{Code: AccessOfferRewardedEpisode, Type: "rewarded_ad", Enabled: true,
			Config: marshal(RewardedAdConfig{RequiredAdCount: 1, DailyRewardLimit: c.EpisodeReward.DailyRewardLimit}),
			Grants: []GrantTemplate{{Type: "episode_access", Config: marshal(accessTemplate{Selection: "next_locked_episodes", Count: count, Validity: "unlimited"})}}},
	}}
	bindings := map[string]RuleBinding{AccessOfferRewardedEpisode: {AdUnitID: c.EpisodeAdUnit()}}
	if c.VIP.Enabled {
		d.Rules = append(d.Rules, UnlockRule{Code: AccessOfferRewardedVIP, Type: "rewarded_ad", Enabled: true,
			Config: marshal(RewardedAdConfig{RequiredAdCount: c.VIP.RequiredAdCount, DailyRewardLimit: c.VIP.DailyRewardLimit, RequireBeforePlayback: c.VIP.RequireBeforePlayback, EntryAdCountdownSeconds: c.VIP.EntryAdCountdownSeconds, ForceWatchCount: c.VIP.StrongWatchAdCount, DelaySeconds: c.VIP.EntryDelaySeconds, Title: c.VIP.Title, Content: c.VIP.Content, FailureMessage: c.VIP.FailureMessage}),
			Grants: []GrantTemplate{{Type: "vip", Config: marshal(accessTemplate{Scope: "application", AdFree: true, Validity: "day"})}}})
		bindings[AccessOfferRewardedVIP] = RuleBinding{AdUnitID: c.VIPAdUnit()}
	}
	return d, bindings
}

// ConfigFromStrategy 为既有广告界面和 Bootstrap 提供只读投影，不产生第二份权威配置。
func ConfigFromStrategy(s UnlockStrategy, b StrategyBinding) ApplicationMonetizationConfig {
	c := ApplicationMonetizationConfig{ApplicationID: b.ApplicationID, StrategyID: s.ID, Revision: s.Revision, Document: &s.Document, Bindings: b.Rules, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt, Strategy: MonetizationStrategyEpisodeUnlock}
	p := c.Policy()
	c.Status, c.FreeEpisodeCount, c.EpisodeReward = p.Status, p.FreeEpisodeCount, p.EpisodeReward
	c.EpisodeRewardAdUnitCode, c.VIPAdUnitCode, c.VIP = p.EpisodeAdUnitCode, p.VIPAdUnitCode, p.VIP
	return c
}
