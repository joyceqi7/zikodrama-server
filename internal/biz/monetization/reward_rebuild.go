package monetization

import "encoding/json"

// RebuildRewardEntitlements 使用事务内有效权益过滤候选集，再按原规则构造实际授予集合。
// 调用方必须持有 MySQL 用户行锁；无构造上下文的历史或直接仓储调用保留已授予事实。
func RebuildRewardEntitlements(grant *RewardGrant, owned map[string]bool) error {
	if grant.BuildContext == nil || grant.BuildRule == nil {
		return nil
	}
	build := *grant.BuildContext
	build.EpisodeIDs = nil
	for _, id := range grant.BuildContext.EpisodeIDs {
		if !owned[id] {
			build.EpisodeIDs = append(build.EpisodeIDs, id)
		}
	}
	items, err := DefaultStrategies().BuildEntitlements(grant.BuildRule.Grants, build)
	if err != nil {
		return err
	}
	grant.Entitlements = items
	grant.UnlockEpisodeIDs = nil
	seen := map[string]bool{}
	for _, item := range items {
		var data AccessData
		if err := json.Unmarshal(item.Data, &data); err != nil {
			return err
		}
		for _, id := range data.EpisodeIDs {
			if !seen[id] {
				grant.UnlockEpisodeIDs = append(grant.UnlockEpisodeIDs, id)
				seen[id] = true
			}
		}
	}
	return nil
}
