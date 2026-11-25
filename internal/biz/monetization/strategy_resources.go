package monetization

import "drama-server/internal/biz/application"

// ProductBindings 返回可持久化的商品映射；旧客户端传入的广告位不再作为配置来源。
func ProductBindings(rules map[string]RuleBinding) map[string]RuleBinding {
	result := make(map[string]RuleBinding)
	for code, binding := range rules {
		if binding.ProductID != "" {
			result[code] = RuleBinding{ProductID: binding.ProductID}
		}
	}
	return result
}

// WithApplicationAdUnits 在读取配置时为所有激励规则解析本小程序的候选广告位，不修改持久化绑定。
// 未配齐两种广告位时不提供广告资源，历史不完整应用也不会产生可执行的广告入口。
func (b StrategyBinding) WithApplicationAdUnits(doc StrategyDocument, units application.AdUnits) StrategyBinding {
	b.Rules = ProductBindings(b.Rules)
	if !units.Complete() {
		return b
	}
	units = units.Normalized()
	for _, rule := range doc.Rules {
		if rule.Type == "rewarded_ad" || rule.Type == "entry_rewarded_ad" {
			b.Rules[rule.Code] = RuleBinding{AdUnitID: units.RewardedVideo[0], AdUnitIDs: append([]string(nil), units.RewardedVideo...)}
		}
	}
	return b
}
