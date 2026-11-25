package monetization

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// TestEntryAdDelayValidationAndProjection 验证首集广告延后的合法范围与公开策略投影。
func TestEntryAdDelayValidationAndProjection(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		free, delay, projected int
		enabled, valid         bool
	}{
		{name: "关闭延后", valid: true, enabled: true},
		{name: "不足四秒", free: 1, delay: 3, enabled: true},
		{name: "允许四秒", free: 1, delay: 4, projected: 4, enabled: true, valid: true},
		{name: "允许三百秒", free: 1, delay: 300, projected: 300, enabled: true, valid: true},
		{name: "超过上限", free: 1, delay: 301, enabled: true},
		{name: "首集锁定也可延后", delay: 5, projected: 5, enabled: true, valid: true},
		{name: "停用策略不下发", free: 1, delay: 5, valid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := StrategyDocument{SchemaVersion: 1, Enabled: tc.enabled, FreeEpisodeCount: tc.free, EntryAdDelaySeconds: tc.delay}
			if err := DefaultStrategies().Validate(doc); (err == nil) != tc.valid {
				t.Fatalf("策略校验结果错误: %v", err)
			}
			if tc.valid {
				policy := (ApplicationMonetizationConfig{Document: &doc}).Policy()
				if got := ConsumerMonetizationFromPolicy(policy).EntryAdDelaySeconds; got != tc.projected {
					t.Fatalf("消费者策略秒数 = %d, 期望 %d", got, tc.projected)
				}
			}
		})
	}
}

func TestVIPProgressPreservesUnlimitedAndTimedValidity(t *testing.T) {
	for _, end := range []time.Time{{}, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)} {
		raw, err := json.Marshal(VIPProgress{Active: true, ValidUntil: end})
		if err != nil {
			t.Fatal(err)
		}
		var value map[string]any
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		if value["active"] != true {
			t.Fatal("VIP 激活状态丢失")
		}
		actual, exists := value["validUntil"]
		if end.IsZero() && exists {
			t.Fatal("无限期权益不能向客户端返回零值截止时间")
		}
		if !end.IsZero() && actual != end.Format(time.RFC3339) {
			t.Fatal("有限期权益必须保留实际截止时间")
		}
	}
}

func TestJSONRulesSeparateAcquisitionFromBenefits(t *testing.T) {
	r := DefaultStrategies()
	doc := StrategyDocument{SchemaVersion: 1, Enabled: true, Rules: []UnlockRule{
		{Code: "ad", Type: "rewarded_ad", Enabled: true, Config: json.RawMessage(`{"requiredAdCount":1}`), Grants: []GrantTemplate{{Type: "episode_access", Config: json.RawMessage(`{"selection":"next_locked_episodes","count":2,"validity":"unlimited"}`)}}},
		{Code: "purchase", Type: "one_time_purchase", Enabled: true, Config: json.RawMessage(`{"productCode":"bundle"}`), Grants: []GrantTemplate{{Type: "drama_access", Config: json.RawMessage(`{"selection":"current_drama","includeFutureEpisodes":true,"validity":"unlimited"}`)}, {Type: "vip", Config: json.RawMessage(`{"scope":"application","adFree":true,"validity":"duration","durationSeconds":86400}`)}}},
		{Code: "subscription", Type: "subscription", Enabled: true, Config: json.RawMessage(`{"productCode":"monthly"}`), Grants: []GrantTemplate{{Type: "vip", Config: json.RawMessage(`{"scope":"application","adFree":true,"validity":"billing_period"}`)}}},
	}}
	if err := r.Validate(doc); err != nil {
		t.Fatal(err)
	}
	if !r.Available(doc.Rules[0]) || r.Available(doc.Rules[1]) || r.Available(doc.Rules[2]) {
		t.Fatal("未接通支付能力不能生成购买入口")
	}
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	context := GrantContext{ApplicationID: "a", UserID: "u", SourceType: "order", SourceID: "paid-order-1", DramaID: "d", AllEpisodeIDs: []string{"e1", "e2"}, Now: now}
	items, err := r.BuildEntitlements(doc.Rules[1].Grants, context)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].SourceItem == items[1].SourceItem {
		t.Fatal("组合商品的发放项不能互相覆盖")
	}
	if !r.Allows(items[0], "a", "u", AccessTarget{"d", "new-episode"}, now) {
		t.Fatal("整剧权益没有按快照允许后续更新")
	}
	// 修改现行策略不会改变已发放的内容与待遇。
	doc.Rules[1].Grants[0].Config = json.RawMessage(`{"selection":"current_drama","includeFutureEpisodes":false,"validity":"day"}`)
	if !r.Allows(items[0], "a", "u", AccessTarget{"d", "new-episode"}, now.Add(48*time.Hour)) {
		t.Fatal("旧权益被新策略改变")
	}
	if r.Allows(items[1], "a", "u", AccessTarget{}, now.Add(24*time.Hour)) {
		t.Fatal("有效区间结束边界必须不包含")
	}
}

func TestEntitlementsAreIsolatedAndSubscriptionPeriodsAreExplicit(t *testing.T) {
	r := DefaultStrategies()
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	template := []GrantTemplate{{Type: "vip", Config: json.RawMessage(`{"scope":"application","validity":"billing_period","adFree":true}`)}}
	c := GrantContext{ApplicationID: "a", UserID: "u", SourceType: "subscription", SourceID: "renewal-1", Now: now, BillingStart: now.Add(time.Hour), BillingEnd: now.Add(2 * time.Hour)}
	items, err := r.BuildEntitlements(template, c)
	if err != nil {
		t.Fatal(err)
	}
	e := items[0]
	if r.Allows(e, "a", "u", AccessTarget{}, now) || r.Allows(e, "b", "u", AccessTarget{}, c.BillingStart) || r.Allows(e, "a", "other", AccessTarget{}, c.BillingStart) {
		t.Fatal("未来周期或其他主体错误获得权益")
	}
	if !r.Allows(e, "a", "u", AccessTarget{}, c.BillingStart) || r.Allows(e, "a", "u", AccessTarget{}, c.BillingEnd) {
		t.Fatal("订阅周期边界错误")
	}
	c.SourceType, c.SourceID = "reward", "ad-1"
	other, err := r.BuildEntitlements(template, c)
	if err != nil {
		t.Fatal(err)
	}
	e.RevokedAt = &now
	if r.Allows(e, "a", "u", AccessTarget{}, c.BillingStart) || !r.Allows(other[0], "a", "u", AccessTarget{}, c.BillingStart) {
		t.Fatal("撤销一个来源影响了其他来源")
	}
	c.BillingEnd = time.Time{}
	if _, err := r.BuildEntitlements(template, c); err == nil {
		t.Fatal("未确认付费周期不允许授予订阅权益")
	}
}

func TestRulesRejectUnknownTypesVersionsAndInvalidParameters(t *testing.T) {
	r := DefaultStrategies()
	base := StrategyDocument{SchemaVersion: 1, Rules: []UnlockRule{{Code: "ad", Type: "rewarded_ad", Config: json.RawMessage(`{"requiredAdCount":1}`), Grants: []GrantTemplate{{Type: "episode_access", Config: json.RawMessage(`{"selection":"next_locked_episodes","count":2,"validity":"unlimited"}`)}}}}}
	for _, change := range []func(*StrategyDocument){
		func(d *StrategyDocument) { d.SchemaVersion = 2 },
		func(d *StrategyDocument) { d.Rules[0].Type = "unknown" },
		func(d *StrategyDocument) { d.Rules[0].Config = json.RawMessage(`{"requiredAdCount":1,"typo":true}`) },
		func(d *StrategyDocument) { d.Rules[0].Grants[0].Type = "unknown" },
		func(d *StrategyDocument) {
			d.Rules[0].Grants[0].Config = json.RawMessage(`{"selection":"next_locked_episodes","count":0,"validity":"unlimited"}`)
		},
		func(d *StrategyDocument) { d.Rules = append(d.Rules, d.Rules[0]) },
	} {
		raw, _ := json.Marshal(base)
		var doc StrategyDocument
		_ = json.Unmarshal(raw, &doc)
		change(&doc)
		if err := r.Validate(doc); err == nil {
			t.Fatalf("错误接受无效规则: %+v", doc)
		}
	}
}

// TestStrategyAllowsEachUnlockWayOnce 验证广告逐集与广告 VIP 可并存，但同一方式不能重复配置。
func TestStrategyAllowsEachUnlockWayOnce(t *testing.T) {
	episode := UnlockRule{Code: "episode", Type: "rewarded_ad", Config: json.RawMessage(`{"requiredAdCount":1}`), Grants: []GrantTemplate{{Type: "episode_access", Config: json.RawMessage(`{"selection":"next_locked_episodes","count":1,"validity":"unlimited"}`)}}}
	vip := UnlockRule{Code: "vip", Type: "rewarded_ad", Config: json.RawMessage(`{"requiredAdCount":2}`), Grants: []GrantTemplate{{Type: "vip", Config: json.RawMessage(`{"scope":"application","validity":"day"}`)}}}
	doc := StrategyDocument{SchemaVersion: 1, Rules: []UnlockRule{episode, vip}}
	if err := DefaultStrategies().Validate(doc); err != nil {
		t.Fatal("不同广告解锁方式应可并存", err)
	}
	duplicate := episode
	duplicate.Code = "episode_again"
	doc.Rules = append(doc.Rules, duplicate)
	if err := DefaultStrategies().Validate(doc); err == nil {
		t.Fatal("重复逐集解锁方式未被拒绝")
	}
}

// TestVIPEntrySwitchKeepsOldRulesCompatible 验证新开关只投影给 Mini，旧规则缺省关闭且不改变服务端授权。
func TestVIPEntrySwitchKeepsOldRulesCompatible(t *testing.T) {
	for _, tc := range []struct {
		config   string
		required bool
	}{
		{config: `{"requiredAdCount":2}`, required: false},
		{config: `{"requiredAdCount":2,"requireBeforePlayback":true}`, required: true},
	} {
		rule := UnlockRule{Code: "vip", Type: "rewarded_ad", Enabled: true, Config: json.RawMessage(tc.config), Grants: []GrantTemplate{{Type: "vip", Config: json.RawMessage(`{"scope":"application","adFree":true,"validity":"day"}`)}}}
		doc := StrategyDocument{SchemaVersion: 1, Enabled: true, Rules: []UnlockRule{rule}}
		if err := DefaultStrategies().Validate(doc); err != nil {
			t.Fatal(err)
		}
		config := ApplicationMonetizationConfig{ApplicationID: "app", Document: &doc, Bindings: map[string]RuleBinding{"vip": {AdUnitID: "vip-ad"}}}
		policy := config.Policy()
		if actual := ConsumerMonetizationFromPolicy(policy).VIP.RequireBeforePlayback; actual != tc.required {
			t.Fatalf("VIP 入剧开关投影错误: got %t, want %t", actual, tc.required)
		}
		raw, err := json.Marshal(ConsumerMonetizationFromPolicy(policy).VIP)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		if _, exists := fields["requireBeforePlayback"]; exists != tc.required {
			t.Fatal("关闭时必须省略新字段，保持旧消费者合同")
		}
	}
}

// testTaskStrategy 演示新增任务获取方式只需实现并注册策略接口。
type testTaskStrategy struct{}

func (testTaskStrategy) Validate(UnlockRule) error                            { return nil }
func (testTaskStrategy) Available() bool                                      { return true }
func (testTaskStrategy) Project(UnlockRule, RuleBinding, *MonetizationPolicy) {}
func TestAdditionalRuleAndBenefitUseTheSameRegistryFlow(t *testing.T) {
	registry := NewStrategyRegistry(map[string]RuleStrategy{"task": testTaskStrategy{}}, map[string]EntitlementStrategy{"festival_pass": vipAccessStrategy{}})
	rule := UnlockRule{Code: "task", Type: "task", Enabled: true, Config: json.RawMessage(`{}`), Grants: []GrantTemplate{{Type: "festival_pass", Config: json.RawMessage(`{"scope":"application","validity":"duration","durationSeconds":3600}`)}}}
	if err := registry.Validate(StrategyDocument{SchemaVersion: 1, Rules: []UnlockRule{rule}}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	items, err := registry.BuildEntitlements(rule.Grants, GrantContext{ApplicationID: "a", UserID: "u", SourceType: "task", SourceID: "done-1", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if !registry.Available(rule) || !registry.Allows(items[0], "a", "u", AccessTarget{"d", "e"}, now) {
		t.Fatal("新增类型需要改动核心判断流程")
	}
}

func (testTaskStrategy) Offer(context.Context, MonetizationPolicy, UnlockRule, RuleBinding, RuleResources) (*AccessOffer, error) {
	return &AccessOffer{Method: "task"}, nil
}
