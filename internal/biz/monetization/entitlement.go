package monetization

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

// Entitlement 是按来源独立保存的权益快照；多来源有效权益取并集。
type Entitlement struct {
	// ID 是落账后数据库分配的自增主键，以十进制字符串返回。
	ID string `json:"id"`
	// ApplicationID 隔离不同小程序的权益。
	ApplicationID string `json:"-"`
	// UserID 是稳定消费者身份，与后台 RBAC 用户独立。
	UserID string `json:"-"`
	// Type 决定 Data 的解释策略。
	Type string `json:"type"`
	// Data 保存实际内容和待遇，不随当前解锁策略改变。
	Data json.RawMessage `json:"data"`
	// SourceType 描述广告、订单、订阅周期等授予来源。
	SourceType string `json:"sourceType"`
	// SourceID 是已经确认的业务事件标识；订阅必须使用具体付费周期事件。
	SourceID string `json:"sourceId"`
	// SourceItem 是同一事件中的稳定发放项编码。
	SourceItem string `json:"sourceItem"`
	// ValidFrom 是包含的生效时刻，支持预先记录未来周期。
	ValidFrom time.Time `json:"validFrom"`
	// ValidUntil 是不包含的截止时刻，空值表示无计划过期时间。
	ValidUntil *time.Time `json:"validUntil"`
	// RevokedAt 只撤销本条来源记录，不影响其他来源。
	RevokedAt *time.Time `json:"revokedAt"`
	// CreatedAt 是该发放项首次落账时间。
	CreatedAt time.Time `json:"createdAt"`
}

// AccessTarget 是权益判断所需的资源上下文，不包含媒体地址。
type AccessTarget struct {
	// DramaID 是本次访问的短剧。
	DramaID string
	// EpisodeID 是本次访问的剧集；为空时仅判断目录权益。
	EpisodeID string
}

// GrantContext 由已验证的广告或支付业务提供，禁止直接采用客户端提交的权益参数。
type GrantContext struct {
	// ApplicationID 是当前业务事件的归属应用。
	ApplicationID string
	// UserID 是经过身份校验的权益获得者。
	UserID string
	// SourceType 是确认事件的业务类别。
	SourceType string
	// SourceID 是幂等业务事件标识。
	SourceID string
	// DramaID 是已解析的目标短剧。
	DramaID string
	// EpisodeIDs 是按顺序排列、从目标集开始的可授予剧集快照。
	EpisodeIDs []string
	// AllEpisodeIDs 保存整剧购买时已经包含的剧集。
	AllEpisodeIDs []string
	// Now 是业务确认时刻。
	Now time.Time
	// PeriodEnd 是应用自然日结束时刻。
	PeriodEnd time.Time
	// BillingStart 是支付系统确认的付费周期开始时刻。
	BillingStart time.Time
	// BillingEnd 是支付系统确认的付费周期结束时刻。
	BillingEnd time.Time
}

// EntitlementStrategy 把权益的校验、生成和判断集中在对应类型处理器中。
type EntitlementStrategy interface {
	// ValidateData 验证实际权益快照，损坏或未知版本数据不能进入账本。
	ValidateData(json.RawMessage) error
	// ValidateTemplate 校验运营保存的授予规则。
	ValidateTemplate(json.RawMessage) error
	// Build 将模板解析为具体内容快照以及有效区间。
	Build(json.RawMessage, GrantContext) (json.RawMessage, time.Time, *time.Time, error)
	// Allows 根据已保存快照判断内容访问，不能读取当前策略重新解释待遇。
	Allows(json.RawMessage, AccessTarget) bool
}

// BuildEntitlements 使用统一发放算法构造全部权益，任何一项失败都不返回部分结果。
func (r *StrategyRegistry) BuildEntitlements(templates []GrantTemplate, c GrantContext) ([]Entitlement, error) {
	if c.ApplicationID == "" || c.UserID == "" || c.SourceType == "" || c.SourceID == "" || c.Now.IsZero() || len(templates) == 0 {
		return nil, ErrInvalidStrategy
	}
	items := make([]Entitlement, 0, len(templates))
	for i, template := range templates {
		h := r.benefits[template.Type]
		if h == nil {
			return nil, ErrInvalidStrategy
		}
		if err := h.ValidateTemplate(template.Config); err != nil {
			return nil, err
		}
		data, start, end, err := h.Build(template.Config, c)
		if err != nil {
			return nil, err
		}
		item := fmt.Sprint(i)
		items = append(items, Entitlement{ApplicationID: c.ApplicationID, UserID: c.UserID, Type: template.Type, Data: data, SourceType: c.SourceType, SourceID: c.SourceID, SourceItem: item, ValidFrom: start, ValidUntil: end, CreatedAt: c.Now.UTC()})
	}
	return items, nil
}

// Allows 先校验用户、应用、撤销和时间边界，再调用类型处理器；未知类型默认拒绝。
func (r *StrategyRegistry) Allows(e Entitlement, appID, userID string, target AccessTarget, now time.Time) bool {
	if e.ApplicationID != appID || e.UserID != userID || e.RevokedAt != nil || e.ValidFrom.After(now) || (e.ValidUntil != nil && !e.ValidUntil.After(now)) {
		return false
	}
	h := r.benefits[e.Type]
	return h != nil && h.ValidateData(e.Data) == nil && h.Allows(e.Data, target)
}

type accessTemplate struct {
	// Selection 指定从当前短剧选取内容的方式。
	Selection string `json:"selection,omitempty"`
	// Count 限定一次授予的后续未解锁集数。
	Count int `json:"count,omitempty"`
	// Scope 限定 VIP 覆盖的应用目录范围。
	Scope string `json:"scope,omitempty"`
	// AdFree 描述该权益承诺的免广告待遇。
	AdFree bool `json:"adFree,omitempty"`
	// IncludeFutureEpisodes 指定整剧权益是否包含未来上架集数。
	IncludeFutureEpisodes bool `json:"includeFutureEpisodes,omitempty"`
	// Validity 指定无限期、自然日、时长或已确认付费周期。
	Validity string `json:"validity"`
	// DurationSeconds 仅在按明确时长授予时生效。
	DurationSeconds int `json:"durationSeconds,omitempty"`
}

// AccessData 保存内容权益的实际快照，结构版本与策略修订无关。
type AccessData struct {
	// SchemaVersion 决定权益内容结构的解释方式。
	SchemaVersion int `json:"schemaVersion"`
	// DramaID 是剧集或整剧权益所属短剧。
	DramaID string `json:"dramaId,omitempty"`
	// EpisodeIDs 保存实际覆盖剧集，整剧不含后续更新时同样保存快照。
	EpisodeIDs []string `json:"episodeIds,omitempty"`
	// IncludeFutureEpisodes 为真时整剧权益也覆盖后续更新。
	IncludeFutureEpisodes bool `json:"includeFutureEpisodes,omitempty"`
	// Scope 为 application 时 VIP 覆盖当前应用可见目录。
	Scope string `json:"scope,omitempty"`
	// AdFree 表示该来源承诺的免广告待遇。
	AdFree bool `json:"adFree,omitempty"`
}

func validateAccessTemplate(data json.RawMessage) (accessTemplate, error) {
	var t accessTemplate
	if err := strictJSON(data, &t); err != nil {
		return t, err
	}
	validities := map[string]bool{"unlimited": true, "day": true, "duration": true, "billing_period": true}
	if !validities[t.Validity] || (t.Validity == "duration" && (t.DurationSeconds < 1 || t.DurationSeconds > 315360000)) || (t.Validity != "duration" && t.DurationSeconds != 0) {
		return t, ErrInvalidStrategy
	}
	return t, nil
}

func buildAccessData(t accessTemplate, data AccessData, c GrantContext) (json.RawMessage, time.Time, *time.Time, error) {
	start := c.Now.UTC()
	var end *time.Time
	// 时间算法通过配置选择，与具体权益类型无关。
	endResolvers := map[string]func() (time.Time, time.Time){
		"unlimited":      func() (time.Time, time.Time) { return start, time.Time{} },
		"day":            func() (time.Time, time.Time) { return start, c.PeriodEnd.UTC() },
		"duration":       func() (time.Time, time.Time) { return start, start.Add(time.Duration(t.DurationSeconds) * time.Second) },
		"billing_period": func() (time.Time, time.Time) { return c.BillingStart.UTC(), c.BillingEnd.UTC() },
	}
	resolve := endResolvers[t.Validity]
	if resolve == nil {
		return nil, start, nil, ErrInvalidStrategy
	}
	start, until := resolve()
	if start.IsZero() || (t.Validity != "unlimited" && (until.IsZero() || !until.After(start))) {
		return nil, start, nil, ErrInvalidStrategy
	}
	if !until.IsZero() {
		end = &until
	}
	data.SchemaVersion = 1
	raw, err := json.Marshal(data)
	return raw, start, end, err
}

type episodeAccessStrategy struct{}

func (episodeAccessStrategy) ValidateTemplate(raw json.RawMessage) error {
	t, err := validateAccessTemplate(raw)
	if err != nil || t.Selection != "next_locked_episodes" || t.Count < 1 || t.Count > 10000 || t.Scope != "" || t.IncludeFutureEpisodes || t.AdFree {
		return ErrInvalidStrategy
	}
	return nil
}
func (episodeAccessStrategy) Build(raw json.RawMessage, c GrantContext) (json.RawMessage, time.Time, *time.Time, error) {
	t, err := validateAccessTemplate(raw)
	if err != nil || c.DramaID == "" || len(c.EpisodeIDs) == 0 {
		return nil, c.Now, nil, ErrInvalidStrategy
	}
	return buildAccessData(t, AccessData{DramaID: c.DramaID, EpisodeIDs: slices.Clone(c.EpisodeIDs[:min(t.Count, len(c.EpisodeIDs))])}, c)
}
func (episodeAccessStrategy) Allows(raw json.RawMessage, target AccessTarget) bool {
	var data AccessData
	return strictJSON(raw, &data) == nil && data.SchemaVersion == 1 && target.DramaID != "" && target.EpisodeID != "" && data.DramaID == target.DramaID && slices.Contains(data.EpisodeIDs, target.EpisodeID)
}

type dramaAccessStrategy struct{}

func (dramaAccessStrategy) ValidateTemplate(raw json.RawMessage) error {
	t, err := validateAccessTemplate(raw)
	if err != nil || t.Selection != "current_drama" || t.Count != 0 || t.Scope != "" || t.AdFree {
		return ErrInvalidStrategy
	}
	return nil
}
func (dramaAccessStrategy) Build(raw json.RawMessage, c GrantContext) (json.RawMessage, time.Time, *time.Time, error) {
	t, err := validateAccessTemplate(raw)
	if err != nil || c.DramaID == "" || (!t.IncludeFutureEpisodes && len(c.AllEpisodeIDs) == 0) {
		return nil, c.Now, nil, ErrInvalidStrategy
	}
	return buildAccessData(t, AccessData{DramaID: c.DramaID, EpisodeIDs: slices.Clone(c.AllEpisodeIDs), IncludeFutureEpisodes: t.IncludeFutureEpisodes}, c)
}
func (dramaAccessStrategy) Allows(raw json.RawMessage, target AccessTarget) bool {
	var data AccessData
	return strictJSON(raw, &data) == nil && data.SchemaVersion == 1 && target.DramaID != "" && target.EpisodeID != "" && data.DramaID == target.DramaID && (data.IncludeFutureEpisodes || slices.Contains(data.EpisodeIDs, target.EpisodeID))
}

type vipAccessStrategy struct{}

func (vipAccessStrategy) ValidateTemplate(raw json.RawMessage) error {
	t, err := validateAccessTemplate(raw)
	if err != nil || t.Scope != "application" || t.Selection != "" || t.Count != 0 || t.IncludeFutureEpisodes {
		return ErrInvalidStrategy
	}
	return nil
}
func (vipAccessStrategy) Build(raw json.RawMessage, c GrantContext) (json.RawMessage, time.Time, *time.Time, error) {
	t, err := validateAccessTemplate(raw)
	if err != nil {
		return nil, c.Now, nil, err
	}
	return buildAccessData(t, AccessData{Scope: t.Scope, AdFree: t.AdFree}, c)
}
func (vipAccessStrategy) Allows(raw json.RawMessage, _ AccessTarget) bool {
	var data AccessData
	return strictJSON(raw, &data) == nil && data.SchemaVersion == 1 && data.Scope == "application"
}

// ValidateEntitlement 校验通用来源、有效区间和类型快照，供所有可信履约入口共用。
func (r *StrategyRegistry) ValidateEntitlement(e Entitlement) error {
	if e.ApplicationID == "" || e.UserID == "" || e.SourceType == "" || e.SourceID == "" || e.SourceItem == "" || e.ValidFrom.IsZero() || e.CreatedAt.IsZero() || (e.ValidUntil != nil && !e.ValidUntil.After(e.ValidFrom)) {
		return ErrInvalidStrategy
	}
	h := r.benefits[e.Type]
	if h == nil {
		return ErrInvalidStrategy
	}
	return h.ValidateData(e.Data)
}

func (episodeAccessStrategy) ValidateData(raw json.RawMessage) error {
	var d AccessData
	if strictJSON(raw, &d) != nil || d.SchemaVersion != 1 || d.DramaID == "" || len(d.EpisodeIDs) == 0 || d.Scope != "" || d.IncludeFutureEpisodes || d.AdFree {
		return ErrInvalidStrategy
	}
	for _, id := range d.EpisodeIDs {
		if id == "" {
			return ErrInvalidStrategy
		}
	}
	return nil
}
func (dramaAccessStrategy) ValidateData(raw json.RawMessage) error {
	var d AccessData
	if strictJSON(raw, &d) != nil || d.SchemaVersion != 1 || d.DramaID == "" || (!d.IncludeFutureEpisodes && len(d.EpisodeIDs) == 0) || d.Scope != "" || d.AdFree {
		return ErrInvalidStrategy
	}
	for _, id := range d.EpisodeIDs {
		if id == "" {
			return ErrInvalidStrategy
		}
	}
	return nil
}
func (vipAccessStrategy) ValidateData(raw json.RawMessage) error {
	var d AccessData
	if strictJSON(raw, &d) != nil || d.SchemaVersion != 1 || d.Scope != "application" || d.DramaID != "" || len(d.EpisodeIDs) != 0 || d.IncludeFutureEpisodes {
		return ErrInvalidStrategy
	}
	return nil
}
