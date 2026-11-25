package monetization

import (
	"errors"

	"drama-server/internal/biz/application"
)

// ErrApplicationModeConflict 表示规则获取方式不属于当前应用模式，停用规则同样需要匹配。
var ErrApplicationModeConflict = errors.New("unlock rule is incompatible with application monetization mode")

// RuleAllowedForMode 决定应用可配置的获取方式；空模式及未知模式拒绝所有规则。
func RuleAllowedForMode(mode application.MonetizationMode, ruleType string) bool {
	switch mode {
	case application.MonetizationModeIAA:
		return ruleType == "rewarded_ad" || ruleType == "entry_rewarded_ad"
	case application.MonetizationModeIAP:
		return ruleType == "one_time_purchase" || ruleType == "subscription"
	case application.MonetizationModeHybrid:
		return ruleType == "rewarded_ad" || ruleType == "entry_rewarded_ad" || ruleType == "one_time_purchase" || ruleType == "subscription"
	default:
		return false
	}
}

// ValidateApplicationMode 校验完整策略与权威应用模式，禁止用停用状态绕过配置范围。
func ValidateApplicationMode(mode application.MonetizationMode, doc StrategyDocument) error {
	for _, rule := range doc.Rules {
		if !RuleAllowedForMode(mode, rule.Type) {
			return ErrApplicationModeConflict
		}
	}
	return nil
}

// RequiresAdUnits 表示当前策略含已启用广告入口，纯内购或空规则不要求广告资源。
func RequiresAdUnits(doc StrategyDocument) bool {
	if !doc.Enabled {
		return false
	}
	for _, rule := range doc.Rules {
		if rule.Enabled && (rule.Type == "rewarded_ad" || rule.Type == "entry_rewarded_ad") {
			return true
		}
	}
	return false
}
