package monetization

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"regexp"
	"time"
)

// ErrInvalidStrategy 表示规则类型、结构版本或配置不能被当前处理器解释。
var ErrInvalidStrategy = errors.New("invalid unlock strategy")

// UnlockStrategy 是单个应用的解锁规则快照，平台广告位和商品映射留在应用配置中。
type UnlockStrategy struct {
	// ID 是策略快照的内部标识。
	ID string `json:"id"`
	// Revision 用于乐观锁；规则结构版本另由 Document.SchemaVersion 表达。
	Revision int `json:"revision"`
	// Document 保存全部玩法参数和发放模板。
	Document StrategyDocument `json:"document"`
	// CreatedAt 保留策略创建时间。
	CreatedAt time.Time `json:"createdAt"`
	// UpdatedAt 是最近一次修改时间。
	UpdatedAt time.Time `json:"updatedAt"`
}

// StrategyDocument 是策略 JSON 的稳定外壳，业务参数由类型处理器校验。
type StrategyDocument struct {
	// SchemaVersion 决定 JSON 结构的解释方式。
	SchemaVersion int `json:"schemaVersion"`
	// Enabled 决定是否向消费者提供新的获取入口，不撤销已有权益。
	Enabled bool `json:"enabled"`
	// InterstitialEnabled 控制普通插屏；历史 JSON 省略时沿用开启行为，不影响激励广告。
	InterstitialEnabled *bool `json:"interstitialEnabled,omitempty"`
	// FreeEpisodeCount 是每部短剧的免费预览集数。
	FreeEpisodeCount int `json:"freeEpisodeCount"`
	// EntryAdDelaySeconds 是首集开始播放后再执行首次广告策略的秒数；零表示直接执行策略。
	EntryAdDelaySeconds int `json:"entryAdDelaySeconds,omitempty"`
	// Rules 的顺序就是消费者获取入口的优先顺序。
	Rules []UnlockRule `json:"rules"`
}

// UnlockRule 描述一种权益获取方式；VIP 可由广告、单次购买和订阅分别提供。
type UnlockRule struct {
	// Code 在策略内唯一，绑定平台资源、进度及 Offer 时保持稳定。
	Code string `json:"code"`
	// Type 选择注册的规则处理器。
	Type string `json:"type"`
	// Enabled 允许单独开关一种玩法。
	Enabled bool `json:"enabled"`
	// Config 保存获取条件，由规则处理器解释。
	Config json.RawMessage `json:"config"`
	// Grants 允许一次完成发放多种权益。
	Grants []GrantTemplate `json:"grants"`
}

// GrantTemplate 是尚未绑定实际内容与时间的权益模板。
type GrantTemplate struct {
	// Type 选择权益生成和访问判断处理器。
	Type string `json:"type"`
	// Config 描述内容选择、待遇和有效期。
	Config json.RawMessage `json:"config"`
}

// RuleBinding 将共享规则引用映射到当前应用的平台资源。
type RuleBinding struct {
	// AdUnitID 仅为运行时从应用配置解析的激励广告位；绑定接口兼容接收但不保存该字段。
	AdUnitID string `json:"adUnitId,omitempty"`
	// AdUnitIDs 是从应用配置读取的激励候选集合，只在服务端选位和领奖校验中使用，不改变对外合同。
	AdUnitIDs []string `json:"-"`
	// ProductID 是当前应用的平台商品标识，尚未接入支付时仅保存。
	ProductID string `json:"productId,omitempty"`
}

// StrategyBinding 保存应用与策略的一对一绑定以及应用专属资源映射。
type StrategyBinding struct {
	// ApplicationID 来自可信应用上下文。
	ApplicationID string `json:"applicationId"`
	// StrategyID 为空表示未绑定策略。
	StrategyID string `json:"strategyId"`
	// Rules 按稳定规则编码保存商品映射，广告位无需手动绑定。
	Rules map[string]RuleBinding `json:"rules"`
}

// RewardedAdConfig 保存广告获取条件，与实际发放权益类型解耦。
type RewardedAdConfig struct {
	// RequireBeforePlayback 仅向新版 Mini 声明 VIP 入剧门禁；旧规则缺省为关闭，服务端不据此拦截播放。
	RequireBeforePlayback bool `json:"requireBeforePlayback,omitempty"`
	// EntryAdCountdownSeconds 是 VIP 入剧强弹每次自动拉起前的前台倒计时；省略或零表示立即拉起，范围为 0–300 秒。
	EntryAdCountdownSeconds *int `json:"entryAdCountdownSeconds,omitempty"`
	// AutoUnlock 配置逐集广告领奖后的连续解锁提示；缺省关闭，不适用于 VIP。
	AutoUnlock *AutoUnlockConfig `json:"autoUnlock,omitempty"`
	// PromptOnIncomplete 控制逐集广告未完整观看时是否展示续看弹窗；缺省关闭。
	PromptOnIncomplete bool `json:"promptOnIncomplete,omitempty"`
	// IncompleteAdCountdownSeconds 是未看完广告后自动重开的等待秒数；未配置时客户端默认等待三秒。
	IncompleteAdCountdownSeconds *int `json:"incompleteAdCountdownSeconds,omitempty"`
	// IncompleteAdDisplayCountdownSeconds 是弹窗显示的倒计时起点；客户端在实际等待时间内走到零，缺省与等待秒数相同。
	IncompleteAdDisplayCountdownSeconds *int `json:"incompleteAdDisplayCountdownSeconds,omitempty"`
	// RequiredAdCount 是自然日内达成权益所需次数；逐集广告固定一次。
	RequiredAdCount int `json:"requiredAdCount"`
	// DailyRewardLimit 是每个用户的每日领取次数上限，零表示不限。
	DailyRewardLimit int `json:"dailyRewardLimit"`
	// ForceWatchCount 是投放启动自动提示次数，不是已完成广告次数。
	ForceWatchCount int `json:"forceWatchCount"`
	// DelaySeconds 是自动广告提示延迟。
	DelaySeconds int `json:"delaySeconds"`
	// Title 是广告 VIP 入口标题。
	Title string `json:"title"`
	// Content 是广告 VIP 入口说明。
	Content string `json:"content"`
	// FailureMessage 是广告失败时的业务文案。
	FailureMessage string `json:"failureMessage"`
}

// EntryRewardedAdConfig 下发 Mini 的入剧计数条件，不发放分集或 VIP 权益。
type EntryRewardedAdConfig struct {
	// Scope 选择 user 或 user_drama；缺省为每人每天共享计数。
	Scope string `json:"scope,omitempty"`
	// CountBy 选择 shown 或 completed；缺省为完整观看计数。
	CountBy string `json:"countBy,omitempty"`
	// RequiredAdCount 是 Mini 在应用时区自然日内判断达标的次数。
	RequiredAdCount int `json:"requiredAdCount"`
}

// EffectiveScope 返回配置的计数范围，不把剧目次数共享给其他用户。
func (c EntryRewardedAdConfig) EffectiveScope() string {
	if c.Scope == "user_drama" {
		return "user_drama"
	}
	return "user"
}

// EffectiveCountBy 返回配置的达标方式，只有展示模式计入提前关闭。
func (c EntryRewardedAdConfig) EffectiveCountBy() string {
	if c.CountBy == "shown" {
		return "shown"
	}
	return "completed"
}

// AutoUnlockConfig 控制领奖后的可取消倒计时，只影响交互，不授予任何权益。
type AutoUnlockConfig struct {
	// Enabled 开启后，倒计时结束继续为后续锁定集观看广告。
	Enabled bool `json:"enabled"`
	// CountdownSeconds 是前台显示的等待秒数，配置范围为 1 至 300，后台暂停。
	CountdownSeconds int `json:"countdownSeconds"`
	// Title 是弹窗标题，留空使用客户端默认文案。
	Title string `json:"title"`
	// Content 是弹窗说明，留空使用客户端默认文案。
	Content string `json:"content"`
}

// PurchaseConfig 保存商品的业务编码，平台 SKU 通过应用绑定关联。
type PurchaseConfig struct {
	// ProductCode 是内部商品编码，订单接入时写入交易条款快照。
	ProductCode string `json:"productCode"`
}

// RuleStrategy 是获取方式的策略接口；支付方式接入后无需修改广告或播放主流程。
type RuleStrategy interface {
	// Offer 通过对应获取方式校验平台资源并生成入口，播放用例不承担广告或支付分支。
	Offer(context.Context, MonetizationPolicy, UnlockRule, RuleBinding, RuleResources) (*AccessOffer, error)
	// Validate 校验当前类型的参数以及能发放的权益组合。
	Validate(UnlockRule) error
	// Available 表示当前版本是否已接通该获取方式，未接通的支付规则绝不产生购买入口。
	Available() bool
	// Project 把规则投影为广告执行合同；非广告类型保留自己的处理器实现。
	Project(UnlockRule, RuleBinding, *MonetizationPolicy)
}

// StrategyRegistry 按类型选择规则和权益算法，构建后不修改，供并发请求共享。
type StrategyRegistry struct {
	rules    map[string]RuleStrategy
	benefits map[string]EntitlementStrategy
}

// NewStrategyRegistry 复制处理器映射，调用方可在装配时增加玩法而不改主流程。
func NewStrategyRegistry(rules map[string]RuleStrategy, benefits map[string]EntitlementStrategy) *StrategyRegistry {
	r := &StrategyRegistry{rules: map[string]RuleStrategy{}, benefits: map[string]EntitlementStrategy{}}
	for k, v := range rules {
		r.rules[k] = v
	}
	for k, v := range benefits {
		r.benefits[k] = v
	}
	return r
}

// DefaultStrategies 装配当前广告执行器、待接入支付规则和三种内容权益处理器。
func DefaultStrategies() *StrategyRegistry {
	return NewStrategyRegistry(map[string]RuleStrategy{
		"rewarded_ad":       rewardedAdStrategy{},
		"entry_rewarded_ad": entryRewardedAdStrategy{},
		"one_time_purchase": purchaseStrategy{},
		"subscription":      purchaseStrategy{subscription: true},
	}, map[string]EntitlementStrategy{
		"episode_access": episodeAccessStrategy{},
		"drama_access":   dramaAccessStrategy{},
		"vip":            vipAccessStrategy{},
	})
}

var strategyCodePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// Validate 对已知版本执行严格校验，未知类型不能因使用 JSON 而绕过业务约束。
func (r *StrategyRegistry) Validate(d StrategyDocument) error {
	if d.SchemaVersion != 1 || d.FreeEpisodeCount < 0 || d.FreeEpisodeCount > 10000 || len(d.Rules) > 5 {
		return ErrInvalidStrategy
	}
	if d.EntryAdDelaySeconds != 0 && (d.EntryAdDelaySeconds < 4 || d.EntryAdDelaySeconds > 300) {
		return ErrInvalidStrategy
	}
	seenCodes := map[string]bool{}
	seenWays := map[string]bool{}
	for _, rule := range d.Rules {
		strategy := r.rules[rule.Type]
		if !strategyCodePattern.MatchString(rule.Code) || seenCodes[rule.Code] || strategy == nil || len(rule.Grants) > 16 || (rule.Type != "entry_rewarded_ad" && len(rule.Grants) < 1) {
			return ErrInvalidStrategy
		}
		seenCodes[rule.Code] = true
		for _, way := range unlockWayKeys(rule) {
			if seenWays[way] {
				return ErrInvalidStrategy
			}
			seenWays[way] = true
		}
		if err := strategy.Validate(rule); err != nil {
			return err
		}
		for _, grant := range rule.Grants {
			benefit := r.benefits[grant.Type]
			if benefit == nil {
				return ErrInvalidStrategy
			}
			if err := benefit.ValidateTemplate(grant.Config); err != nil {
				return err
			}
		}
	}
	return nil
}

// unlockWayKeys 将底层规则投影为运营可见的解锁方式，入剧、逐集和 VIP 广告分别计数。
func unlockWayKeys(rule UnlockRule) []string {
	if rule.Type != "rewarded_ad" {
		return []string{rule.Type}
	}
	keys := make([]string, 0, len(rule.Grants))
	seen := map[string]bool{}
	for _, grant := range rule.Grants {
		key := rule.Type + ":" + grant.Type
		if !seen[key] {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	return keys
}

// Available 只对启用且已经接通执行能力的规则返回真。
func (r *StrategyRegistry) Available(rule UnlockRule) bool {
	s := r.rules[rule.Type]
	return rule.Enabled && s != nil && s.Available()
}

// ProjectRule 为一次 Offer 生成专属快照，避免不同规则互相使用阈值和广告位。
func (r *StrategyRegistry) ProjectRule(base MonetizationPolicy, rule UnlockRule) MonetizationPolicy {
	base.Rule = &rule
	base.EpisodeAdUnitCode, base.VIPAdUnitCode = "", ""
	base.VIP = ApplicationVIPPolicy{}
	if s := r.rules[rule.Type]; s != nil {
		s.Project(rule, base.Bindings[rule.Code], &base)
	}
	return base
}

type rewardedAdStrategy struct{}

type entryRewardedAdStrategy struct{}

func (entryRewardedAdStrategy) Available() bool { return true }
func (entryRewardedAdStrategy) Validate(rule UnlockRule) error {
	var config EntryRewardedAdConfig
	if strictJSON(rule.Config, &config) != nil || config.RequiredAdCount < 1 || config.RequiredAdCount > 100 || len(rule.Grants) != 0 ||
		(config.Scope != "" && config.Scope != "user" && config.Scope != "user_drama") ||
		(config.CountBy != "" && config.CountBy != "shown" && config.CountBy != "completed") {
		return ErrInvalidStrategy
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(rule.Config, &fields); err != nil {
		return ErrInvalidStrategy
	}
	// 省略字段时使用默认值；显式空字符串或 null 不能作为有效枚举保存。
	if _, present := fields["scope"]; present && config.Scope == "" {
		return ErrInvalidStrategy
	}
	if _, present := fields["countBy"]; present && config.CountBy == "" {
		return ErrInvalidStrategy
	}
	return nil
}
func (entryRewardedAdStrategy) Project(UnlockRule, RuleBinding, *MonetizationPolicy) {}

func (entryRewardedAdStrategy) Offer(ctx context.Context, policy MonetizationPolicy, rule UnlockRule, binding RuleBinding, resources RuleResources) (*AccessOffer, error) {
	if binding.AdUnitID == "" {
		return nil, ErrInvalidRewardClaim
	}
	configured, err := resources.HasRewardedAdUnit(ctx, binding.AdUnitID)
	if err != nil {
		slog.ErrorContext(ctx, "check entry rewarded ad unit failed", "error", err)
		return nil, err
	}
	if !configured {
		return nil, ErrInvalidRewardClaim
	}
	return &AccessOffer{ID: policy.OfferIDForKind(AccessOfferRewardedEntry), Method: "rewarded_ad", RuleCode: rule.Code, Kind: AccessOfferRewardedEntry, AdUnitID: binding.AdUnitID, ApplicationID: policy.ApplicationID}, nil
}

func (rewardedAdStrategy) Available() bool { return true }
func (rewardedAdStrategy) Validate(rule UnlockRule) error {
	var c RewardedAdConfig
	if strictJSON(rule.Config, &c) != nil || c.RequiredAdCount < 1 || c.RequiredAdCount > 10 || c.DailyRewardLimit < 0 || c.DailyRewardLimit > 10000 || c.ForceWatchCount < 0 || c.ForceWatchCount > c.RequiredAdCount || c.DelaySeconds < 0 || c.DelaySeconds > 300 || len([]rune(c.Title)) > 255 || len([]rune(c.Content)) > 500 || len([]rune(c.FailureMessage)) > 500 {
		return ErrInvalidStrategy
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(rule.Config, &fields); err != nil {
		return ErrInvalidStrategy
	}
	if _, present := fields["entryAdCountdownSeconds"]; present {
		// 倒计时只编排 Mini 的 VIP 入剧强弹，不能用于普通入口或逐集广告；显式 null 也应在入口拒绝。
		if c.EntryAdCountdownSeconds == nil || !c.RequireBeforePlayback || !ruleHasBenefit(rule, "vip") || *c.EntryAdCountdownSeconds < 0 || *c.EntryAdCountdownSeconds > 300 {
			return ErrInvalidStrategy
		}
	}
	if a := c.AutoUnlock; a != nil {
		if a.CountdownSeconds < 1 || a.CountdownSeconds > 300 || len([]rune(a.Title)) > 255 || len([]rune(a.Content)) > 500 ||
			(a.Enabled && (ruleHasBenefit(rule, "vip") || !ruleHasBenefit(rule, "episode_access"))) {
			return ErrInvalidStrategy
		}
	}
	if c.PromptOnIncomplete && ruleHasBenefit(rule, "vip") {
		return ErrInvalidStrategy
	}
	if c.IncompleteAdCountdownSeconds != nil && (!c.PromptOnIncomplete || *c.IncompleteAdCountdownSeconds < 1 || *c.IncompleteAdCountdownSeconds > 300) {
		return ErrInvalidStrategy
	}
	if c.IncompleteAdDisplayCountdownSeconds != nil && (!c.PromptOnIncomplete || *c.IncompleteAdDisplayCountdownSeconds < 1 || *c.IncompleteAdDisplayCountdownSeconds > 300) {
		return ErrInvalidStrategy
	}
	// 当前累计广告玩法发放自然日 VIP；其他获取方式可使用时长、整剧和组合权益。
	for _, grant := range rule.Grants {
		var t accessTemplate
		if strictJSON(grant.Config, &t) != nil || t.Validity == "billing_period" {
			return ErrInvalidStrategy
		}
	}
	if ruleHasBenefit(rule, "vip") {
		if c.DailyRewardLimit > 0 && c.DailyRewardLimit < c.RequiredAdCount {
			return ErrInvalidStrategy
		}
		for _, grant := range rule.Grants {
			if grant.Type != "vip" {
				return ErrInvalidStrategy
			}
			var benefit accessTemplate
			if strictJSON(grant.Config, &benefit) != nil || benefit.Validity != "day" {
				return ErrInvalidStrategy
			}
		}
	} else if c.RequiredAdCount != 1 || c.ForceWatchCount != 0 || c.RequireBeforePlayback {
		return ErrInvalidStrategy
	}
	return nil
}
func (rewardedAdStrategy) Project(rule UnlockRule, binding RuleBinding, p *MonetizationPolicy) {
	var c RewardedAdConfig
	_ = json.Unmarshal(rule.Config, &c) // 文档已在写入与读取边界完成校验。
	if ruleHasBenefit(rule, "vip") {
		p.VIPAdUnitCode = binding.AdUnitID
		p.VIP = ApplicationVIPPolicy{DailyRewardLimit: c.DailyRewardLimit, Enabled: rule.Enabled, RequireBeforePlayback: c.RequireBeforePlayback, EntryAdCountdownSeconds: c.EntryAdCountdownSeconds, StrongWatchAdCount: c.ForceWatchCount, RequiredAdCount: c.RequiredAdCount, EntryDelaySeconds: c.DelaySeconds, Title: c.Title, Content: c.Content, FailureMessage: c.FailureMessage}
	} else {
		p.EpisodeAdUnitCode = binding.AdUnitID
		p.EpisodeReward = EpisodeRewardPolicy{UnlockEpisodeCount: 1, DailyRewardLimit: c.DailyRewardLimit}
		for _, grant := range rule.Grants {
			if grant.Type == "episode_access" {
				var t accessTemplate
				_ = json.Unmarshal(grant.Config, &t)
				p.EpisodeReward.UnlockEpisodeCount = t.Count
				break
			}
		}
	}
}

type purchaseStrategy struct{ subscription bool }

func (purchaseStrategy) Available() bool { return false }
func (s purchaseStrategy) Validate(rule UnlockRule) error {
	var c PurchaseConfig
	if strictJSON(rule.Config, &c) != nil || !strategyCodePattern.MatchString(c.ProductCode) {
		return ErrInvalidStrategy
	}
	for _, grant := range rule.Grants {
		var t accessTemplate
		if strictJSON(grant.Config, &t) != nil || (!s.subscription && t.Validity == "billing_period") {
			return ErrInvalidStrategy
		}
	}
	return nil
}
func (purchaseStrategy) Project(UnlockRule, RuleBinding, *MonetizationPolicy) {}

func ruleHasBenefit(rule UnlockRule, kind string) bool {
	for _, grant := range rule.Grants {
		if grant.Type == kind {
			return true
		}
	}
	return false
}

func strictJSON(data []byte, dst any) error {
	if len(data) == 0 || len(data) > 2*1024*1024 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return ErrInvalidStrategy
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return ErrInvalidStrategy
	}
	if decoder.Decode(new(any)) != io.EOF {
		return ErrInvalidStrategy
	}
	return nil
}

// RuleResources 提供规则策略需要的资源校验端口；平台长期凭据不会进入策略 JSON。
type RuleResources interface {
	// HasRewardedAdUnit 确认广告编码属于可信上下文应用。
	HasRewardedAdUnit(context.Context, string) (bool, error)
}

// Offer 把入口生成交给规则处理器，未接通能力保持不可执行。
func (r *StrategyRegistry) Offer(ctx context.Context, p MonetizationPolicy, resources RuleResources) (*AccessOffer, error) {
	if p.Rule == nil || !p.Enabled() || !r.Available(*p.Rule) {
		return nil, ErrInvalidRewardClaim
	}
	return r.rules[p.Rule.Type].Offer(ctx, p, *p.Rule, p.Bindings[p.Rule.Code], resources)
}

func (rewardedAdStrategy) Offer(ctx context.Context, p MonetizationPolicy, rule UnlockRule, binding RuleBinding, resources RuleResources) (*AccessOffer, error) {
	if binding.AdUnitID == "" {
		return nil, ErrInvalidRewardClaim
	}
	configured, err := resources.HasRewardedAdUnit(ctx, binding.AdUnitID)
	if err != nil {
		slog.ErrorContext(ctx, "check rewarded ad unit for offer failed", "error", err)
		return nil, err
	}
	if !configured {
		return nil, ErrInvalidRewardClaim
	}
	offer := &AccessOffer{ID: p.OfferIDForKind(p.SelectedKind()), Method: "rewarded_ad", RuleCode: rule.Code, Kind: p.SelectedKind(), AdUnitID: binding.AdUnitID, ApplicationID: p.ApplicationID}
	for _, grant := range rule.Grants {
		offer.Benefits = append(offer.Benefits, grant.Type)
	}
	if p.VIP.Enabled {
		vip := ConsumerMonetizationFromPolicy(p).VIP
		offer.VIP = &vip
	} else {
		var config RewardedAdConfig
		_ = json.Unmarshal(rule.Config, &config) // 已由策略写入和读取边界校验。
		offer.AutoUnlock = config.AutoUnlock
		offer.PromptOnIncomplete = config.PromptOnIncomplete
		offer.IncompleteAdCountdownSeconds = config.IncompleteAdCountdownSeconds
		offer.IncompleteAdDisplayCountdownSeconds = config.IncompleteAdDisplayCountdownSeconds
	}
	return offer, nil
}

func (purchaseStrategy) Offer(context.Context, MonetizationPolicy, UnlockRule, RuleBinding, RuleResources) (*AccessOffer, error) {
	// 支付接入时在此生成商品入口，并独立完成订单创建、查询和回调履约。
	return nil, ErrInvalidRewardClaim
}
