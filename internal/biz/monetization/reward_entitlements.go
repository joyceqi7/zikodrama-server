package monetization

import "encoding/json"

// RewardEntitlements 保留内部旧调用方的已解析内容，所有落库仍统一走类型 JSON 账本。
func RewardEntitlements(g RewardGrant) ([]Entitlement, error) {
	if g.RewardType == RewardTypeEntryProgress {
		return nil, nil
	}
	if len(g.Entitlements) > 0 {
		return g.Entitlements, nil
	}
	var templates []GrantTemplate
	if g.RewardType == RewardTypeVIPProgress {
		templates = []GrantTemplate{{Type: "vip", Config: json.RawMessage(`{"scope":"application","adFree":true,"validity":"day"}`)}}
	} else {
		raw, _ := json.Marshal(map[string]any{"selection": "next_locked_episodes", "count": max(1, len(g.UnlockEpisodeIDs)), "validity": "unlimited"})
		templates = []GrantTemplate{{Type: "episode_access", Config: raw}}
	}
	ids := g.UnlockEpisodeIDs
	if len(ids) == 0 && g.EpisodeID != "" {
		ids = []string{g.EpisodeID}
	}
	return DefaultStrategies().BuildEntitlements(templates, GrantContext{ApplicationID: g.ApplicationID, UserID: g.VisitorID, SourceType: "reward_grant", SourceID: g.ClaimID, DramaID: g.DramaID, EpisodeIDs: ids, Now: g.GrantedAt, PeriodEnd: g.PeriodExpiresAt})
}
