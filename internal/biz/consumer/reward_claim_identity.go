package consumer

import (
	"strings"

	"drama-server/internal/biz/monetization"
)

// rewardClaimMatchesGrant 只核对已落库事实的不可变内容，修订和请求日期变化不影响同键恢复。
// 不读取当前配置，避免规则停用或跨日后把合法旧领奖重试判成一次新领取。
func rewardClaimMatchesGrant(grant monetization.RewardGrant, input monetization.ClaimRewardInput) bool {
	if grant.AdUnitID != input.CompletedAdUnitID || grant.DramaID != input.DramaID || grant.EpisodeID != input.EpisodeID {
		return false
	}
	applicationID, _, kind, err := monetization.ParseOfferID(input.OfferID)
	if err != nil || applicationID != grant.ApplicationID {
		return false
	}
	if index := strings.LastIndex(grant.ProgressKey, ":"); index >= 0 {
		return kind == grant.ProgressKey[index+1:]
	}
	want := grantOfferKind(grant)
	if grant.RewardType == monetization.RewardTypeEntryProgress {
		want = monetization.AccessOfferRewardedEntry
	}
	return kind == want
}
