package monetization

import (
	"encoding/base64"
	"errors"
	"testing"
)

// TestOfferIDPreservesCompatibleEncoding 保留旧字段格式，但修订不再是领取门禁。
func TestOfferIDPreservesCompatibleEncoding(t *testing.T) {
	policy := MonetizationPolicy{ApplicationID: "101", Revision: 7}
	offerID := policy.OfferIDForKind(AccessOfferRewardedEpisode)

	applicationID, revision, kind, err := ParseOfferID(offerID)
	if err != nil {
		t.Fatal(err)
	}
	if applicationID != "101" || revision != 7 || kind != AccessOfferRewardedEpisode {
		t.Fatalf("Offer 解析结果错误: application=%q revision=%d kind=%q", applicationID, revision, kind)
	}

	legacy := base64.RawURLEncoding.EncodeToString([]byte("101\x1f" + AccessOfferRewardedEpisode))
	if _, _, _, err := ParseOfferID(legacy); !errors.Is(err, ErrInvalidRewardClaim) {
		t.Fatalf("缺少策略修订的旧 Offer 必须失效，实际错误: %v", err)
	}
}

// TestClaimOfferRequiresExactEnabledRule 验证领取不能通过固定类型借用其他规则，展示兼容分支不影响领取身份。
func TestClaimOfferRequiresExactEnabledRule(t *testing.T) {
	policy := MonetizationPolicy{ApplicationID: "101", Status: MonetizationEnabled,
		Rules: []UnlockRule{{Code: "episode-a", Type: "rewarded_ad", Enabled: true}, {Code: "episode-disabled", Type: "rewarded_ad", Enabled: false}},
	}
	for _, code := range []string{AccessOfferRewardedEpisode, "deleted", "episode-disabled"} {
		if _, ok := policy.ForClaimOffer(code); ok {
			t.Fatalf("不可用规则或类型别名不能领取: %s", code)
		}
	}
	if selected, ok := policy.ForClaimOffer("episode-a"); !ok || selected.Rule.Code != "episode-a" {
		t.Fatal("应精确匹配仍启用的规则")
	}
}
