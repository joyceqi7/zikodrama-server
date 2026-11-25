// Package monetization 定义应用消费设置、访问 Offer、VIP 进度和奖励结算领域。
// 广告位编码归 Application 聚合所有；本包只在消费设置中保存并校验引用。
package monetization

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ErrInvalidRewardClaim 表示 Offer、广告完成信息或当前业务状态不满足领取要求。
var ErrInvalidRewardClaim = errors.New("invalid reward claim")

const (
	// MonetizationEnabled 表示当前应用允许执行变现奖励策略。
	MonetizationEnabled = "enabled"
	// MonetizationDisabled 表示当前应用关闭变现奖励策略。
	MonetizationDisabled = "disabled"

	// MonetizationStrategyEpisodeUnlock 是当前公开合同支持的逐集解锁策略编码。
	MonetizationStrategyEpisodeUnlock = "episode_unlock"

	// AccessOfferRewardedEpisode 标识观看激励广告后授予分集权益的 Offer。
	AccessOfferRewardedEpisode = "rewarded_episode"
	// AccessOfferRewardedVIP 标识观看激励广告后累计 VIP 进度的 Offer。
	AccessOfferRewardedVIP = "rewarded_vip"
	// AccessOfferRewardedEntry 标识累计当天入剧资格的激励广告 Offer。
	AccessOfferRewardedEntry = "rewarded_entry"

	// EntitlementScopeEpisode 表示权益限定于指定内部剧集。
	EntitlementScopeEpisode = "episode"
	// EntitlementScopeCatalog 表示权益覆盖当前应用可见目录。
	EntitlementScopeCatalog = "catalog"
)

// MonetizationPolicy 是应用配置在一次消费者请求中的不可变执行快照。
// 它只包含播放授权和奖励结算所需字段，不复刻运营存储模型，也不承担版本发布职责。
type MonetizationPolicy struct {
	// InterstitialEnabled 仅控制普通插屏，历史未配置时沿用开启行为。
	InterstitialEnabled *bool
	// Revision 保留现有 Offer 编码格式；新领取不比较修订，始终使用当前规则。
	Revision int
	// ProgressNamespace 由已确认奖励快照提供，避免恢复时依赖当前规则。
	ProgressNamespace string
	// StrategyID 标识当前应用绑定的策略；进度按策略 ID 和规则编码隔离，编辑修订不清零次数。
	StrategyID string
	// Rules 保存本次请求的规则快照；空列表仅用于旧广告合同的适配。
	Rules []UnlockRule
	// Bindings 保存当前应用专属广告位和商品映射。
	Bindings map[string]RuleBinding
	// Rule 指向本次 Offer 选择的规则；全局展示投影不设置该值。
	Rule *UnlockRule
	// ApplicationID 是消费设置归属的官方 TikTok AppID，领取时必须与请求上下文一致。
	ApplicationID string
	// Status 控制当前消费设置是否启用。
	Status string
	// FreeEpisodeCount 表示每部短剧按集号从首集开始可免费访问的集数。
	FreeEpisodeCount int
	// EntryAdDelaySeconds 控制首次广告的时机，以及锁定首集可先播放的秒数。
	EntryAdDelaySeconds int
	// EpisodeReward 配置一次广告的解锁集数与每日领取限制。
	EpisodeReward EpisodeRewardPolicy
	// EpisodeAdUnitCode 是逐集解锁使用的应用激励广告位编码。
	EpisodeAdUnitCode string
	// VIPAdUnitCode 是 VIP 进度使用的应用激励广告位编码。
	VIPAdUnitCode string
	// VIP 配置应用自然日 VIP 的广告阈值及提示文案。
	VIP ApplicationVIPPolicy
}

// EpisodeRewardPolicy 规定逐集奖励一次授予多少集、一天最多领取多少次。
type EpisodeRewardPolicy struct {
	// UnlockEpisodeCount 表示从目标集起按集号顺序授予的最大集数，不超过现有目录尾部。
	UnlockEpisodeCount int `json:"unlockEpisodeCount"`
	// DailyRewardLimit 是每访客在应用自然日内的逐集奖励领取上限；零值表示不限制。
	DailyRewardLimit int `json:"dailyRewardLimit"`
}

// ApplicationMonetizationConfig 是共享 JSON 策略与应用绑定的请求快照。
// 固定广告字段仅供原广告合同投影；数据库权威规则保存在独立策略的 Document 中。
type ApplicationMonetizationConfig struct {
	// Revision 是当前配置修订，只保留在兼容的 Offer 编码中，不参与领取拒绝条件。
	Revision int `json:"-"`
	// StrategyID 指向独立解锁策略；以下固定字段仅是广告合同的运行时投影。
	StrategyID string `json:"strategyId,omitempty"`
	// Document 保存权威 JSON 规则，固定字段不再作为数据库规则字段。
	Document *StrategyDocument `json:"document,omitempty"`
	// Bindings 是应用专属平台资源映射。
	Bindings map[string]RuleBinding `json:"bindings,omitempty"`
	// ApplicationID 是消费设置归属的官方 TikTok AppID，领取时必须与请求上下文一致。
	ApplicationID string `json:"applicationId"`
	// Status 控制当前消费设置是否启用。
	Status string `json:"status"`
	// Strategy 是公开消费策略编码，当前使用 episode_unlock。
	Strategy string `json:"strategy"`
	// EpisodeRewardAdUnitCode 是逐集解锁使用的应用激励广告位编码。
	EpisodeRewardAdUnitCode string `json:"episodeRewardAdUnitCode,omitempty"`
	// VIPAdUnitCode 是 VIP 进度使用的应用激励广告位编码。
	VIPAdUnitCode string `json:"vipAdUnitCode,omitempty"`
	// FreeEpisodeCount 表示每部短剧按集号从首集开始可免费访问的集数。
	FreeEpisodeCount int `json:"freeEpisodeCount,omitempty"`
	// EpisodeReward 配置一次广告的解锁集数与每日领取限制。
	EpisodeReward EpisodeRewardPolicy `json:"episodeReward,omitempty"`
	// VIP 配置应用自然日 VIP 的广告阈值及提示文案。
	VIP ApplicationVIPPolicy `json:"vip,omitempty"`

	// CreatedAt 记录该应用消费设置首次创建时间。
	CreatedAt time.Time `json:"createdAt"`
	// UpdatedAt 记录消费设置最近保存时间。
	UpdatedAt time.Time `json:"updatedAt"`
}

// ApplicationVIPPolicy 是应用级 VIP 的权威领域表示。
// StrongWatchAdCount 是兼容旧配置的停用字段；RequiredAdCount 决定用户何时可以手动领取当日 VIP。
// 服务端持久化每次完整 VIP 广告的当日累计次数，达标后仍须显式领取。
type ApplicationVIPPolicy struct {
	// RequireBeforePlayback 只作为 Mini 入剧交互配置下发，服务端播放授权仍按原权益规则处理。
	RequireBeforePlayback bool `json:"requireBeforePlayback,omitempty"`
	// EntryAdCountdownSeconds 仅配置 Mini 的 VIP 入剧强弹倒计时，省略或零保持立即拉起。
	EntryAdCountdownSeconds *int `json:"entryAdCountdownSeconds,omitempty"`
	// DailyRewardLimit 是当前 VIP 广告规则的每日领取上限，零表示不限。
	DailyRewardLimit int `json:"dailyRewardLimit,omitempty"`
	// Enabled 控制 VIP 累计进度与目录权益能力；关闭时不提供 VIP 奖励入口。
	Enabled bool `json:"enabled"`
	// StrongWatchAdCount 对应旧合同中的 forceWatchCount，不再触发自动提示。
	StrongWatchAdCount int `json:"strongWatchAdCount"`
	// RequiredAdCount 是应用时区自然日内允许手动领取 VIP 所需的累计奖励数。
	RequiredAdCount int `json:"requiredAdCount"`
	// EntryDelaySeconds 是旧投放提示的兼容字段，不再用于弹窗调度。
	EntryDelaySeconds int `json:"entryDelaySeconds"`
	// Title 是 VIP 广告提示的默认语言标题。
	Title string `json:"title"`
	// Content 是 VIP 广告提示的默认语言正文。
	Content string `json:"content"`
	// FailureMessage 是在奖励流程失败时展示的运营文案，不代替服务端错误日志。
	FailureMessage string `json:"failureMessage"`
}

// ConsumerMonetization 是当前应用的公开规则集。
// 它有意省略服务端配置版本和标识符：客户端只接收不透明 Offer，
// 无法影响奖励结算。
type ConsumerMonetization struct {
	// Status 控制当前消费设置是否启用。
	Status string `json:"status"`
	// Strategy 是公开消费策略编码，当前使用 episode_unlock。
	Strategy string `json:"strategy"`
	// FreeEpisodeCount 表示每部短剧按集号从首集开始可免费访问的集数。
	FreeEpisodeCount int `json:"freeEpisodeCount,omitempty"`
	// EntryAdDelaySeconds 是首集开始播放后执行首次广告策略前的秒数。
	EntryAdDelaySeconds int `json:"entryAdDelaySeconds,omitempty"`
	// EpisodeReward 配置一次广告的解锁集数与每日领取限制。
	EpisodeReward EpisodeRewardPolicy `json:"episodeReward,omitempty"`
	// VIP 配置应用自然日 VIP 的广告阈值及提示文案。
	VIP ConsumerVIPPolicy `json:"vip"`
}

// ConsumerVIPPolicy 是消费者展示 VIP 进度与弹窗所需的公开合同。
// ForceWatchCount 和 DelaySeconds 为旧 Mini 合同兼容保留，不再触发投放广告。
// 广告位、应用 ID、内部修订和奖励结算字段均不会
// 通过该结构下发。
type ConsumerVIPPolicy struct {
	// RequireBeforePlayback 为新版 Mini 开启 VIP 入剧门禁；缺省关闭，旧 Mini 忽略此字段。
	RequireBeforePlayback bool `json:"requireBeforePlayback,omitempty"`
	// EntryAdCountdownSeconds 是每次自动拉起 VIP 入剧广告前的前台等待秒数；旧 Mini 忽略，缺省或零立即拉起。
	EntryAdCountdownSeconds *int `json:"entryAdCountdownSeconds,omitempty"`
	// Enabled 表示当前应用是否启用 VIP 进度与目录权益。
	Enabled bool `json:"enabled"`
	// ForceWatchCount 是已停用的旧投放自动提示次数。
	ForceWatchCount int `json:"forceWatchCount"`
	// RequiredAdCount 是当前应用自然日内允许手动领取 VIP 所需的累计成功奖励数。
	RequiredAdCount int `json:"requiredAdCount"`
	// DelaySeconds 是已停用的旧投放自动提示等待秒数。
	DelaySeconds int `json:"delaySeconds"`
	// Title 是 VIP 广告提示的默认语言标题。
	Title string `json:"title"`
	// Content 是 VIP 广告提示的默认语言正文。
	Content string `json:"content"`
	// FailureMessage 是在奖励流程失败时展示的运营文案，不代替服务端错误日志。
	FailureMessage string `json:"failureMessage,omitempty"`
}

// ConsumerMonetizationFromPolicy 把执行快照投影为 Bootstrap 的公开变现合同。具体
// AdUnitID 只随对应 Offer 下发，避免客户端把全局变现配置和某次奖励领取混在一起。
func ConsumerMonetizationFromPolicy(policy MonetizationPolicy) ConsumerMonetization {
	vip := policy.VIP
	strategy := MonetizationStrategyEpisodeUnlock
	if policy.Rules != nil {
		strategy = "rules"
	}
	return ConsumerMonetization{
		Status:              policy.Status,
		Strategy:            strategy,
		FreeEpisodeCount:    policy.FreeEpisodeCount,
		EntryAdDelaySeconds: policy.EntryAdDelaySeconds,
		EpisodeReward:       policy.EpisodeReward,
		VIP: ConsumerVIPPolicy{
			Enabled:                 vip.Enabled,
			RequireBeforePlayback:   vip.RequireBeforePlayback,
			EntryAdCountdownSeconds: vip.EntryAdCountdownSeconds,
			ForceWatchCount:         vip.StrongWatchAdCount,
			RequiredAdCount:         vip.RequiredAdCount,
			DelaySeconds:            vip.EntryDelaySeconds,
			Title:                   vip.Title,
			Content:                 vip.Content,
			FailureMessage:          vip.FailureMessage,
		},
	}
}

// Enabled 仅判断配置状态是否 enabled，不检查广告位存在性或访客权益。
func (config ApplicationMonetizationConfig) Enabled() bool {
	return config.Status == MonetizationEnabled
}

// Policy 将持久化配置转换为消费者领域使用的最小策略快照。
func (config ApplicationMonetizationConfig) Policy() MonetizationPolicy {
	policy := MonetizationPolicy{
		Revision:          config.Revision,
		ApplicationID:     config.ApplicationID,
		Status:            config.Status,
		FreeEpisodeCount:  config.FreeEpisodeCount,
		EpisodeReward:     config.EpisodeReward,
		EpisodeAdUnitCode: config.EpisodeAdUnit(),
		VIPAdUnitCode:     config.VIPAdUnit(),
		VIP:               config.VIP,
		StrategyID:        config.StrategyID, Bindings: config.Bindings,
	}
	if config.Document != nil {
		policy.InterstitialEnabled = config.Document.InterstitialEnabled
		policy.Rules = config.Document.Rules
		policy.FreeEpisodeCount = config.Document.FreeEpisodeCount
		policy.Status = MonetizationDisabled
		if config.Document.Enabled {
			policy.Status = MonetizationEnabled
			policy.EntryAdDelaySeconds = config.Document.EntryAdDelaySeconds
		}
		policy.EpisodeAdUnitCode, policy.VIPAdUnitCode = "", ""
		policy.VIP = ApplicationVIPPolicy{}
		for _, candidate := range policy.RulePolicies() {
			if policy.EpisodeAdUnitCode == "" && candidate.EpisodeAdUnitCode != "" {
				policy.EpisodeAdUnitCode = candidate.EpisodeAdUnitCode
				policy.EpisodeReward = candidate.EpisodeReward
			}
			if policy.VIPAdUnitCode == "" && candidate.VIPAdUnitCode != "" {
				policy.VIPAdUnitCode = candidate.VIPAdUnitCode
				policy.VIP = candidate.VIP
			}
		}
	}
	return policy
}

// EpisodeAdUnit 返回逐集解锁使用的应用广告位编码。
func (config ApplicationMonetizationConfig) EpisodeAdUnit() string {
	return strings.TrimSpace(config.EpisodeRewardAdUnitCode)
}

// VIPAdUnit 返回会员奖励场景使用的应用广告位编码。
func (config ApplicationMonetizationConfig) VIPAdUnit() string {
	return strings.TrimSpace(config.VIPAdUnitCode)
}

// AccessOffer 描述当前奖励入口；领取通过规则身份重新读取最新配置。
type AccessOffer struct {
	// AutoUnlock 是当前逐集入口的连续广告提示，不能用作解锁凭据。
	AutoUnlock *AutoUnlockConfig `json:"autoUnlock,omitempty"`
	// PromptOnIncomplete 仅控制未看完逐集广告后的续看弹窗，不改变领奖条件。
	PromptOnIncomplete bool `json:"promptOnIncomplete,omitempty"`
	// IncompleteAdCountdownSeconds 是未看完广告弹窗自动重开的等待秒数，缺省由客户端使用三秒。
	IncompleteAdCountdownSeconds *int `json:"incompleteAdCountdownSeconds,omitempty"`
	// IncompleteAdDisplayCountdownSeconds 是弹窗展示的倒计时起点，实际重开仍由等待秒数控制。
	IncompleteAdDisplayCountdownSeconds *int `json:"incompleteAdDisplayCountdownSeconds,omitempty"`
	// VIP 保存当前入口专属的广告提示配置，避免多个 VIP 规则互相借用阈值。
	VIP *ConsumerVIPPolicy `json:"vip,omitempty"`
	// Method 选择客户端已注册的获取方式处理器。
	Method string `json:"method,omitempty"`
	// RuleCode 是当前策略中稳定的入口编码。
	RuleCode string `json:"ruleCode,omitempty"`
	// Benefits 告知客户端此入口可获得哪些待遇，不作为发放凭据。
	Benefits []string `json:"benefits,omitempty"`
	// ID 兼容编码应用、修订及规则身份；修订不锁定待遇，也不是广告完成证明。
	ID string `json:"offerId"`
	// Kind 区分逐集解锁和 VIP 进度两种奖励类型。
	Kind string `json:"kind"`
	// AdUnitID 是客户端交给 TikTok 激励广告 SDK 的公开广告单元标识。
	AdUnitID string `json:"adUnitId"`
	// ApplicationID 是消费设置归属的官方 TikTok AppID，领取时必须与请求上下文一致。
	ApplicationID string `json:"-"`
	// VIPProgress 保存构造 Offer 时的 VIP 状态，仅供服务端编排，不随此字段下发。
	VIPProgress *VIPProgress `json:"vipState,omitempty"`
}

// RewardPeriod 表示应用时区的一个自然日，边界以 UTC 时刻提供给持久层。
type RewardPeriod struct {
	// Key 是应用本地日期 YYYY-MM-DD，用于归并当天奖励进度。
	Key string
	// StartsAt 是自然日包含的开始时刻。
	StartsAt time.Time
	// ExpiresAt 是下一个自然日开始时刻，作为本周期不包含的结束边界。
	ExpiresAt time.Time
}

// Enabled 仅判断配置状态是否 enabled，不检查广告位存在性或访客权益。
func (policy MonetizationPolicy) Enabled() bool {
	return policy.Status == MonetizationEnabled
}

// AllowsInterstitial 决定是否下发插屏广告位；策略停用或明确关闭时客户端无可展示的插屏编码。
func (policy MonetizationPolicy) AllowsInterstitial() bool {
	return policy.Enabled() && (policy.InterstitialEnabled == nil || *policy.InterstitialEnabled)
}

// OfferKind 返回当前固定的逐集奖励类型。
func (policy MonetizationPolicy) OfferKind() string {
	return AccessOfferRewardedEpisode
}

// VIPOfferKind 返回统一 VIP 奖励类型；布尔参数目前不影响结果，不区分强看与主动领取。
func (policy MonetizationPolicy) VIPOfferKind(bool) string {
	return AccessOfferRewardedVIP
}

// AdUnitForOffer 返回指定奖励类型使用的应用广告位编码。
func (policy MonetizationPolicy) AdUnitForOffer(kind string) string {
	switch kind {
	case AccessOfferRewardedEpisode:
		return policy.EpisodeAdUnitCode
	case AccessOfferRewardedVIP:
		return policy.VIPAdUnitCode
	}
	return ""
}

// OfferID 编码当前策略的默认逐集奖励入口标识。
func (version MonetizationPolicy) OfferID() string {
	return version.OfferIDForKind(version.OfferKind())
}

// OfferIDForKind 保留应用、修订和规则身份的既有编码，领取只按应用和精确规则身份读取最新配置。
func (version MonetizationPolicy) OfferIDForKind(kind string) string {
	if version.Rule != nil {
		kind = version.Rule.Code
	}
	payload := strings.Join([]string{version.ApplicationID, strconv.Itoa(version.Revision), kind}, "\x1f")
	return base64.RawURLEncoding.EncodeToString([]byte(payload))
}

// ParseOfferID 解码应用、策略修订和奖励类型；缺少修订的旧入口不再兼容。
func ParseOfferID(value string) (applicationID string, revision int, kind string, err error) {
	decoded, decodeErr := base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
	if decodeErr != nil {
		return "", 0, "", ErrInvalidRewardClaim
	}
	parts := strings.Split(string(decoded), "\x1f")
	if len(parts) != 3 || strings.TrimSpace(parts[0]) == "" {
		return "", 0, "", ErrInvalidRewardClaim
	}
	revision, revisionErr := strconv.Atoi(parts[1])
	if revisionErr != nil || revision < 0 || !strategyCodePattern.MatchString(parts[2]) {
		return "", 0, "", ErrInvalidRewardClaim
	}
	return parts[0], revision, parts[2], nil
}

// RewardPeriodFor 按应用时区计算当地午夜到次日午夜并转为 UTC；使用日历加一天以适配夏令时，未指定时区时用 UTC。
func RewardPeriodFor(now time.Time, location *time.Location) RewardPeriod {
	if location == nil {
		location = time.UTC
	}
	local := now.In(location)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
	expires := start.AddDate(0, 0, 1)
	return RewardPeriod{
		Key:       fmt.Sprintf("%04d-%02d-%02d", local.Year(), local.Month(), local.Day()),
		StartsAt:  start.UTC(),
		ExpiresAt: expires.UTC(),
	}
}

// RulePolicies 通过规则策略注册表产生可执行的入口快照，未接入支付规则不会出现在结果中。
func (policy MonetizationPolicy) RulePolicies() []MonetizationPolicy {
	registry := DefaultStrategies()
	items := []MonetizationPolicy{}
	for _, rule := range policy.Rules {
		if registry.Available(rule) {
			items = append(items, registry.ProjectRule(policy, rule))
		}
	}
	return items
}

// ForOffer 按编码解析具体规则；固定广告类型仅用于旧合同的展示入口选择。
func (policy MonetizationPolicy) ForOffer(code string) (MonetizationPolicy, bool) {
	if policy.Rule != nil {
		return policy, code == policy.Rule.Code || code == policy.SelectedKind()
	}
	for _, candidate := range policy.RulePolicies() {
		if candidate.Rule.Code == code {
			return candidate, true
		}
	}
	for _, candidate := range policy.RulePolicies() {
		if candidate.SelectedKind() == code {
			return candidate, true
		}
	}
	if policy.Rules == nil && (code == AccessOfferRewardedEpisode || code == AccessOfferRewardedVIP) {
		return policy, true
	}
	return policy, false
}

// ForClaimOffer 精确解析广告已选择的规则；规则删除或停用时不退回另一同类入口。
// 无规则文档的历史配置仅接受原有固定广告类型，不将它们作为新规则文档的别名。
func (policy MonetizationPolicy) ForClaimOffer(code string) (MonetizationPolicy, bool) {
	if policy.Rules == nil {
		return policy.ForOffer(code)
	}
	for _, candidate := range policy.RulePolicies() {
		if candidate.Rule.Code == code {
			return candidate, true
		}
	}
	return MonetizationPolicy{}, false
}

// SelectedKind 保持现有广告展示语义，具体发放内容由规则 Grants 和权益处理器决定。
func (policy MonetizationPolicy) SelectedKind() string {
	if policy.Rule != nil && policy.Rule.Type == "entry_rewarded_ad" {
		return AccessOfferRewardedEntry
	}
	if policy.VIPAdUnitCode != "" {
		return AccessOfferRewardedVIP
	}
	return AccessOfferRewardedEpisode
}

// ProgressKey 隔离同一应用内不同策略快照、不同规则的累计广告进度。
func (policy MonetizationPolicy) ProgressKey() string {
	if policy.ProgressNamespace != "" {
		return policy.ProgressNamespace
	}
	if policy.Rule != nil {
		return policy.StrategyID + ":" + policy.Rule.Code
	}
	for _, candidate := range policy.RulePolicies() {
		if candidate.VIP.Enabled {
			return candidate.ProgressKey()
		}
	}
	return ""
}
