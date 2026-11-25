package operations

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/operations"
)

// TestApplicationAdUnitTypeAllowsRewardedPool 验证广告位在 HTTP 入口先规范化再按类型限制数量。
func TestApplicationAdUnitTypeAllowsRewardedPool(t *testing.T) {
	tooMany := make([]string, application.MaxRewardedAdUnits+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("ad-%d", i)
	}
	for _, test := range []struct {
		name    string
		units   application.AdUnits
		invalid bool
	}{
		{"空集合可暂存", application.AdUnits{}, false},
		{"单个广告位", application.AdUnits{RewardedVideo: []string{" reward "}, Interstitial: []string{"interstitial"}}, false},
		{"多个激励广告位", application.AdUnits{RewardedVideo: []string{"one", "two"}}, false},
		{"多个插屏仍拒绝", application.AdUnits{Interstitial: []string{"one", "two"}}, true},
		{"重复激励先去重", application.AdUnits{RewardedVideo: []string{"one", "one"}}, false},
		{"候选集合过多", application.AdUnits{RewardedVideo: tooMany}, true},
		{"单项过长", application.AdUnits{RewardedVideo: []string{strings.Repeat("a", 256)}}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := normalizeCreateApplicationInput(operations.ApplicationInput{SubjectID: "1", MonetizationMode: "IAA", Name: "Test", TikTokAppID: "123", ClientKey: "test-client", ClientSecret: "test-secret", AdUnits: test.units})
			if (err != nil) != test.invalid {
				t.Fatalf("广告位入口校验结果不符: %v", err)
			}
			if !test.invalid && !reflect.DeepEqual(result.AdUnits, test.units.Normalized()) {
				t.Fatal("广告位未规范化或去重")
			}
		})
	}
}
