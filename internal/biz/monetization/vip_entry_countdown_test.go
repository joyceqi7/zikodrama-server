package monetization

import (
	"encoding/json"
	"testing"
)

// TestVIPEntryCountdownValidationAndProjection 验证可选秒数、严格输入及旧规则缺省合同。
func TestVIPEntryCountdownValidationAndProjection(t *testing.T) {
	for _, tc := range []struct {
		name, config string
		valid        bool
		seconds      *int
	}{
		{name: "旧规则", config: `{"requiredAdCount":2,"delaySeconds":5}`, valid: true},
		{name: "强弹缺省", config: `{"requiredAdCount":2,"requireBeforePlayback":true}`, valid: true},
		{name: "立即拉起", config: `{"requiredAdCount":2,"requireBeforePlayback":true,"entryAdCountdownSeconds":0}`, valid: true, seconds: intPointer(0)},
		{name: "最短倒计时", config: `{"requiredAdCount":2,"requireBeforePlayback":true,"entryAdCountdownSeconds":1}`, valid: true, seconds: intPointer(1)},
		{name: "最大倒计时", config: `{"requiredAdCount":2,"requireBeforePlayback":true,"entryAdCountdownSeconds":300}`, valid: true, seconds: intPointer(300)},
		{name: "负数", config: `{"requiredAdCount":2,"requireBeforePlayback":true,"entryAdCountdownSeconds":-1}`},
		{name: "超过上限", config: `{"requiredAdCount":2,"requireBeforePlayback":true,"entryAdCountdownSeconds":301}`},
		{name: "小数", config: `{"requiredAdCount":2,"requireBeforePlayback":true,"entryAdCountdownSeconds":1.5}`},
		{name: "字符串", config: `{"requiredAdCount":2,"requireBeforePlayback":true,"entryAdCountdownSeconds":"5"}`},
		{name: "空值", config: `{"requiredAdCount":2,"requireBeforePlayback":true,"entryAdCountdownSeconds":null}`},
		{name: "未开启强弹", config: `{"requiredAdCount":2,"entryAdCountdownSeconds":0}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rule := UnlockRule{Code: "vip", Type: "rewarded_ad", Enabled: true, Config: json.RawMessage(tc.config), Grants: []GrantTemplate{{Type: "vip", Config: json.RawMessage(`{"scope":"application","adFree":true,"validity":"day"}`)}}}
			doc := StrategyDocument{SchemaVersion: 1, Enabled: true, Rules: []UnlockRule{rule}}
			err := DefaultStrategies().Validate(doc)
			if (err == nil) != tc.valid {
				t.Fatalf("倒计时校验结果错误: %v", err)
			}
			if !tc.valid {
				return
			}
			policy := ApplicationMonetizationConfig{ApplicationID: "app", Document: &doc, Bindings: map[string]RuleBinding{"vip": {AdUnitID: "vip-ad"}}}.Policy()
			vip := ConsumerMonetizationFromPolicy(policy).VIP
			if (vip.EntryAdCountdownSeconds == nil) != (tc.seconds == nil) || tc.seconds != nil && *vip.EntryAdCountdownSeconds != *tc.seconds {
				t.Fatal("消费者倒计时投影丢失或改变")
			}
			raw, err := json.Marshal(vip)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatal(err)
			}
			if _, present := fields["entryAdCountdownSeconds"]; present != (tc.seconds != nil) {
				t.Fatal("旧规则必须省略新字段，显式零必须保留")
			}
			rule.Grants = []GrantTemplate{{Type: "episode_access", Config: json.RawMessage(`{"selection":"next_locked_episodes","count":1,"validity":"unlimited"}`)}}
			if tc.seconds != nil && DefaultStrategies().Validate(StrategyDocument{SchemaVersion: 1, Rules: []UnlockRule{rule}}) == nil {
				t.Fatal("逐集广告不能使用 VIP 入剧倒计时")
			}
		})
	}
}

// intPointer 保留显式零与未配置之间的差异。
func intPointer(value int) *int { return &value }

// TestVIPEntryCountdownConfigBridge 验证旧配置模型桥接规则时不丢失新字段或改用旧延迟。
func TestVIPEntryCountdownConfigBridge(t *testing.T) {
	config := ApplicationMonetizationConfig{VIP: ApplicationVIPPolicy{Enabled: true, RequiredAdCount: 2, RequireBeforePlayback: true, EntryAdCountdownSeconds: intPointer(7), EntryDelaySeconds: 5}}
	doc, _ := StrategyFromConfig(config)
	var vip RewardedAdConfig
	if err := json.Unmarshal(doc.Rules[1].Config, &vip); err != nil {
		t.Fatal(err)
	}
	if vip.EntryAdCountdownSeconds == nil || *vip.EntryAdCountdownSeconds != 7 || vip.DelaySeconds != 5 {
		t.Fatal("VIP 桥接没有分别保留新倒计时和旧延迟字段")
	}
}
