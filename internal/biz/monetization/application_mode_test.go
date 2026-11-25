package monetization

import (
	"drama-server/internal/biz/application"
	"errors"
	"testing"
)

// TestApplicationModeRules 覆盖模式与获取方式矩阵，包括停用规则和未知方式。
func TestApplicationModeRules(t *testing.T) {
	for _, mode := range []application.MonetizationMode{"", "IAA", "IAP", "IAA+IAP"} {
		for _, typ := range []string{"rewarded_ad", "one_time_purchase", "subscription", "unknown"} {
			allowed := (typ == "rewarded_ad" && (mode == "IAA" || mode == "IAA+IAP")) || ((typ == "one_time_purchase" || typ == "subscription") && (mode == "IAP" || mode == "IAA+IAP"))
			for _, enabled := range []bool{true, false} {
				doc := StrategyDocument{Enabled: true, Rules: []UnlockRule{{Type: typ, Enabled: enabled}}}
				err := ValidateApplicationMode(mode, doc)
				if (err == nil) != allowed || (!allowed && !errors.Is(err, ErrApplicationModeConflict)) {
					t.Fatalf("模式 %s 未正确限制 %s，启用=%v", mode, typ, enabled)
				}
				if RequiresAdUnits(doc) != (typ == "rewarded_ad" && enabled) {
					t.Fatal("广告资源条件错误")
				}
			}
		}
	}
	if RequiresAdUnits(StrategyDocument{Enabled: true}) {
		t.Fatal("空规则不应要求广告位")
	}
}
