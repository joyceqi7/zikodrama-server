package monetization

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func TestEntryAdRuleIsIndependentOfContentGrants(t *testing.T) {
	rule := UnlockRule{Code: "entry_ad", Type: "entry_rewarded_ad", Enabled: true, Config: json.RawMessage(`{"requiredAdCount":3}`), Grants: []GrantTemplate{}}
	doc := StrategyDocument{SchemaVersion: 1, Enabled: true, Rules: []UnlockRule{rule}}
	if err := DefaultStrategies().Validate(doc); err != nil {
		t.Fatalf("入剧广告无需内容权益: %v", err)
	}
	for _, config := range []string{`{"requiredAdCount":0}`, `{"requiredAdCount":101}`, `{"requiredAdCount":3,"forceWatchCount":1}`, `{"requiredAdCount":3,"scope":"drama"}`, `{"requiredAdCount":3,"countBy":"attempt"}`, `{"requiredAdCount":3,"scope":null}`, `{"requiredAdCount":3,"countBy":null}`, `{"requiredAdCount":3,"scope":""}`, `{"requiredAdCount":3,"countBy":""}`} {
		doc.Rules[0].Config = json.RawMessage(config)
		if !errors.Is(DefaultStrategies().Validate(doc), ErrInvalidStrategy) {
			t.Fatalf("无效入剧次数或旧强看字段应被拒绝: %s", config)
		}
	}
	doc.Rules[0] = rule
	doc.Rules[0].Grants = []GrantTemplate{{Type: "episode_access", Config: json.RawMessage(`{"selection":"next_locked_episodes","count":1,"validity":"unlimited"}`)}}
	if !errors.Is(DefaultStrategies().Validate(doc), ErrInvalidStrategy) {
		t.Fatal("入剧广告不能同时发放剧集权益")
	}
}

// TestEntryAdFourModesAndDefaults 验证四种组合共享同一规则类型，省略模式时使用默认配置。
func TestEntryAdFourModesAndDefaults(t *testing.T) {
	for _, count := range []int{1, 10, 100} {
		doc := StrategyDocument{SchemaVersion: 1, Enabled: true, Rules: []UnlockRule{{Code: "entry", Type: "entry_rewarded_ad", Enabled: true, Config: json.RawMessage([]byte(`{"requiredAdCount":` + fmt.Sprint(count) + `}`))}}}
		if err := DefaultStrategies().Validate(doc); err != nil {
			t.Fatalf("合法次数边界被拒绝: %d %v", count, err)
		}
	}
	for _, scope := range []string{"user", "user_drama"} {
		for _, countBy := range []string{"shown", "completed"} {
			raw, _ := json.Marshal(EntryRewardedAdConfig{Scope: scope, CountBy: countBy, RequiredAdCount: 2})
			doc := StrategyDocument{SchemaVersion: 1, Enabled: true, Rules: []UnlockRule{{Code: "entry", Type: "entry_rewarded_ad", Enabled: true, Config: raw}}}
			if err := DefaultStrategies().Validate(doc); err != nil {
				t.Fatalf("四种入剧模式应可保存: %s %s %v", scope, countBy, err)
			}
		}
	}
	defaults := EntryRewardedAdConfig{RequiredAdCount: 3}
	if defaults.EffectiveScope() != "user" || defaults.EffectiveCountBy() != "completed" {
		t.Fatal("默认范围和计数方式不正确")
	}
}
