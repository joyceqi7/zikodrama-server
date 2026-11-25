package monetization

import "drama-server/internal/biz/application"

// ApplicationStrategy 是当前应用的策略快照，规则和商品映射共享应用修订。
type ApplicationStrategy struct {
	// ApplicationID 来自可信应用上下文，不接受客户端选择归属。
	ApplicationID string `json:"applicationId"`
	// MonetizationMode 与策略处于同一快照，用于限定当前编辑器的规则类型。
	MonetizationMode application.MonetizationMode `json:"monetizationMode"`
	// Strategy 为空表示当前应用尚未配置策略。
	Strategy *UnlockStrategy `json:"strategy"`
	// Rules 保存当前应用的商品映射，广告位从应用配置自动读取。
	Rules map[string]RuleBinding `json:"rules"`
}

// ApplicationStrategyInput 整体替换当前应用的规则和商品映射，不含可变应用归属。
type ApplicationStrategyInput struct {
	// Document 保存规则和权益模板，支付规则保持平台尚未接入的执行门禁。
	Document StrategyDocument `json:"document"`
	// Rules 按规则编码保存平台商品标识，空映射表示清除商品配置。
	Rules map[string]RuleBinding `json:"rules"`
}
