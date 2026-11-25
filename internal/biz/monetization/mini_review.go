package monetization

// EpisodeOnlyDocument 返回仅保留逐集激励规则的副本，不修改运营策略和规则顺序。
func EpisodeOnlyDocument(doc StrategyDocument) StrategyDocument {
	closed := false
	doc.InterstitialEnabled = &closed
	rules := make([]UnlockRule, 0, len(doc.Rules))
	for _, rule := range doc.Rules {
		if rule.Type != "rewarded_ad" || len(rule.Grants) == 0 {
			continue
		}
		episodeOnly := true
		for _, grant := range rule.Grants {
			if grant.Type != "episode_access" {
				episodeOnly = false
			}
		}
		if episodeOnly {
			rules = append(rules, rule)
		}
	}
	doc.Rules = rules
	return doc
}

// HasEnabledEpisodeRule 判断策略是否可以在审核期间提供逐集激励解锁。
func HasEnabledEpisodeRule(doc StrategyDocument) bool {
	if !doc.Enabled {
		return false
	}
	for _, rule := range EpisodeOnlyDocument(doc).Rules {
		if rule.Enabled {
			return true
		}
	}
	return false
}
