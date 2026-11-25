package monetization

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"drama-server/internal/biz/application"
)

// TestApplicationRewardedPoolProjection 验证应用候选集合只投影到服务端运行时绑定。
func TestApplicationRewardedPoolProjection(t *testing.T) {
	doc := StrategyDocument{Rules: []UnlockRule{{Code: "episode", Type: "rewarded_ad"}, {Code: "entry", Type: "entry_rewarded_ad"}}}
	units := application.AdUnits{RewardedVideo: []string{"ad-b", "9007199254740993"}, Interstitial: []string{"interstitial"}}
	if !units.Complete() {
		t.Fatal("完整多激励配置应允许启用")
	}
	binding := (StrategyBinding{ApplicationID: "app"}).WithApplicationAdUnits(doc, units)
	for _, rule := range doc.Rules {
		if !reflect.DeepEqual(binding.Rules[rule.Code].AdUnitIDs, []string{"9007199254740993", "ad-b"}) {
			t.Fatal("各广告规则必须取得当前应用的候选集合")
		}
	}
	raw, err := json.Marshal(binding)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "adUnitIds") || strings.Contains(string(raw), "AdUnitIDs") {
		t.Fatalf("候选集合不得进入消费者绑定合同: %s", raw)
	}
	units.RewardedVideo[0] = "changed"
	if binding.Rules["episode"].AdUnitIDs[1] != "ad-b" {
		t.Fatal("候选集合必须复制，不能与输入共享可变切片")
	}
	for _, units := range []application.AdUnits{{}, {RewardedVideo: []string{"reward"}}, {RewardedVideo: []string{"reward"}, Interstitial: []string{"a", "b"}}} {
		if units.Complete() {
			t.Fatal("缺少广告位或多个插屏必须拒绝启用")
		}
		if len((StrategyBinding{}).WithApplicationAdUnits(doc, units).Rules) != 0 {
			t.Fatal("不完整应用不能产生广告入口")
		}
	}
}
