package monetization

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

type autoUnlockResources struct{}

func (autoUnlockResources) HasRewardedAdUnit(context.Context, string) (bool, error) { return true, nil }

type failingAutoUnlockResources struct{}

func (failingAutoUnlockResources) HasRewardedAdUnit(context.Context, string) (bool, error) {
	return false, errors.New("advertisement lookup unavailable")
}

func TestAutoUnlockValidationAndOffer(t *testing.T) {
	rule := UnlockRule{Code: "episode", Type: "rewarded_ad", Enabled: true, Grants: []GrantTemplate{{Type: "episode_access", Config: json.RawMessage(`{"selection":"next_locked_episodes","count":2,"validity":"unlimited"}`)}}}
	for _, tc := range []struct {
		name   string
		config string
		valid  bool
	}{
		{"旧配置默认关闭", `{"requiredAdCount":1}`, true},
		{"开启未完成广告提示", `{"requiredAdCount":1,"promptOnIncomplete":true}`, true},
		{"配置未完成广告五秒重开", `{"requiredAdCount":1,"promptOnIncomplete":true,"incompleteAdCountdownSeconds":5}`, true},
		{"显示五秒但三秒重开", `{"requiredAdCount":1,"promptOnIncomplete":true,"incompleteAdCountdownSeconds":3,"incompleteAdDisplayCountdownSeconds":5}`, true},
		{"拒绝未完成广告零秒重开", `{"requiredAdCount":1,"promptOnIncomplete":true,"incompleteAdCountdownSeconds":0}`, false},
		{"拒绝未完成广告超限重开", `{"requiredAdCount":1,"promptOnIncomplete":true,"incompleteAdCountdownSeconds":301}`, false},
		{"拒绝未完成广告小数重开", `{"requiredAdCount":1,"promptOnIncomplete":true,"incompleteAdCountdownSeconds":1.5}`, false},
		{"拒绝关闭强弹时设置重开秒数", `{"requiredAdCount":1,"incompleteAdCountdownSeconds":5}`, false},
		{"拒绝零秒显示倒计时", `{"requiredAdCount":1,"promptOnIncomplete":true,"incompleteAdDisplayCountdownSeconds":0}`, false},
		{"拒绝超限显示倒计时", `{"requiredAdCount":1,"promptOnIncomplete":true,"incompleteAdDisplayCountdownSeconds":301}`, false},
		{"拒绝小数显示倒计时", `{"requiredAdCount":1,"promptOnIncomplete":true,"incompleteAdDisplayCountdownSeconds":1.5}`, false},
		{"拒绝关闭强弹时设置显示秒数", `{"requiredAdCount":1,"incompleteAdDisplayCountdownSeconds":5}`, false},
		{"关闭未完成广告提示", `{"requiredAdCount":1,"promptOnIncomplete":false}`, true},
		{"拒绝非布尔提示开关", `{"requiredAdCount":1,"promptOnIncomplete":"true"}`, false},
		{"启用五秒提示", `{"requiredAdCount":1,"autoUnlock":{"enabled":true,"countdownSeconds":5,"title":"继续解锁","content":"可取消并播放"}}`, true},
		{"禁用后保留文案", `{"requiredAdCount":1,"autoUnlock":{"enabled":false,"countdownSeconds":5,"title":"保留"}}`, true},
		{"拒绝零秒", `{"requiredAdCount":1,"autoUnlock":{"enabled":true,"countdownSeconds":0}}`, false},
		{"拒绝小数", `{"requiredAdCount":1,"autoUnlock":{"enabled":true,"countdownSeconds":1.5}}`, false},
		{"拒绝超限", `{"requiredAdCount":1,"autoUnlock":{"enabled":true,"countdownSeconds":301}}`, false},
		{"拒绝开关字符串", `{"requiredAdCount":1,"autoUnlock":{"enabled":"true","countdownSeconds":5}}`, false},
		{"拒绝超长文案", `{"requiredAdCount":1,"autoUnlock":{"enabled":true,"countdownSeconds":5,"title":"` + strings.Repeat("字", 256) + `"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rule.Config = json.RawMessage(tc.config)
			err := DefaultStrategies().Validate(StrategyDocument{SchemaVersion: 1, Enabled: true, Rules: []UnlockRule{rule}})
			if (err == nil) != tc.valid {
				t.Fatalf("校验结果不符: %v", err)
			}
			if !tc.valid {
				return
			}
			p := MonetizationPolicy{ApplicationID: "1", Status: MonetizationEnabled, Rules: []UnlockRule{rule}, Bindings: map[string]RuleBinding{"episode": {AdUnitID: "ad"}}}
			p = DefaultStrategies().ProjectRule(p, rule)
			offer, err := DefaultStrategies().Offer(context.Background(), p, autoUnlockResources{})
			if err != nil {
				t.Fatal(err)
			}
			var config RewardedAdConfig
			if err := json.Unmarshal(rule.Config, &config); err != nil {
				t.Fatal(err)
			}
			if config.AutoUnlock == nil {
				if offer.AutoUnlock != nil {
					t.Fatal("旧规则不应开启自动解锁")
				}
			} else if offer.AutoUnlock == nil || *offer.AutoUnlock != *config.AutoUnlock {
				t.Fatal("当前规则配置未随 Offer 下发")
			}
			if offer.PromptOnIncomplete != config.PromptOnIncomplete {
				t.Fatal("未完成广告提示开关未随 Offer 下发")
			}
			if (offer.IncompleteAdCountdownSeconds == nil) != (config.IncompleteAdCountdownSeconds == nil) ||
				(offer.IncompleteAdCountdownSeconds != nil && *offer.IncompleteAdCountdownSeconds != *config.IncompleteAdCountdownSeconds) {
				t.Fatal("未完成广告重开秒数未随 Offer 下发")
			}
			if (offer.IncompleteAdDisplayCountdownSeconds == nil) != (config.IncompleteAdDisplayCountdownSeconds == nil) ||
				(offer.IncompleteAdDisplayCountdownSeconds != nil && *offer.IncompleteAdDisplayCountdownSeconds != *config.IncompleteAdDisplayCountdownSeconds) {
				t.Fatal("未完成广告显示秒数未随 Offer 下发")
			}
			if config.IncompleteAdDisplayCountdownSeconds != nil {
				// 旧 Mini 只识别原有等待字段；新字段不能改写它的数值。
				raw, err := json.Marshal(offer)
				if err != nil {
					t.Fatal(err)
				}
				var legacy struct {
					// PromptOnIncomplete 是旧 Mini 已识别的强弹开关。
					PromptOnIncomplete bool `json:"promptOnIncomplete"`
					// IncompleteAdCountdownSeconds 是旧 Mini 已识别的实际等待时间。
					IncompleteAdCountdownSeconds *int `json:"incompleteAdCountdownSeconds"`
				}
				if err := json.Unmarshal(raw, &legacy); err != nil {
					t.Fatal(err)
				}
				actualSeconds := 3
				if config.IncompleteAdCountdownSeconds != nil {
					actualSeconds = *config.IncompleteAdCountdownSeconds
				}
				legacySeconds := 3
				if legacy.IncompleteAdCountdownSeconds != nil {
					legacySeconds = *legacy.IncompleteAdCountdownSeconds
				}
				if !legacy.PromptOnIncomplete || legacySeconds != actualSeconds {
					t.Fatal("新 Offer 改变了旧 Mini 的实际重看时间")
				}
			}
		})
	}
	rule.Config = json.RawMessage(`{"requiredAdCount":1,"autoUnlock":{"enabled":true,"countdownSeconds":5}}`)
	rule.Grants = []GrantTemplate{{Type: "vip", Config: json.RawMessage(`{"scope":"application","adFree":true,"validity":"day"}`)}}
	if DefaultStrategies().Validate(StrategyDocument{SchemaVersion: 1, Rules: []UnlockRule{rule}}) == nil {
		t.Fatal("VIP 不应允许逐集自动解锁")
	}
	rule.Config = json.RawMessage(`{"requiredAdCount":1,"promptOnIncomplete":true}`)
	if DefaultStrategies().Validate(StrategyDocument{SchemaVersion: 1, Rules: []UnlockRule{rule}}) == nil {
		t.Fatal("VIP 不应允许逐集广告未完成弹窗")
	}
}

func TestPromptOfferResourceFailureLogsDetail(t *testing.T) {
	rule := UnlockRule{Code: "episode", Type: "rewarded_ad", Enabled: true,
		Config: json.RawMessage(`{"requiredAdCount":1,"promptOnIncomplete":true}`),
		Grants: []GrantTemplate{{Type: "episode_access", Config: json.RawMessage(`{"selection":"next_locked_episodes","count":2,"validity":"unlimited"}`)}},
	}
	p := MonetizationPolicy{ApplicationID: "1", Status: MonetizationEnabled, Rules: []UnlockRule{rule}, Bindings: map[string]RuleBinding{"episode": {AdUnitID: "ad"}}}
	p = DefaultStrategies().ProjectRule(p, rule)
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	_, err := DefaultStrategies().Offer(context.Background(), p, failingAutoUnlockResources{})
	if err == nil || !strings.Contains(err.Error(), "advertisement lookup unavailable") {
		t.Fatalf("资源错误未传播: %v", err)
	}
	if !strings.Contains(logs.String(), "check rewarded ad unit for offer failed") || !strings.Contains(logs.String(), "advertisement lookup unavailable") {
		t.Fatalf("未记录操作及具体错误: %s", logs.String())
	}
}
