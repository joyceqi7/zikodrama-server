package memory

import (
	"context"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/operations"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// DashboardDays 从测试仓库的真实记录聚合日统计，保留应用和日期隔离。
func (s *Store) DashboardDays(ctx context.Context, from, to time.Time) ([]operations.DashboardDay, error) {
	result := []operations.DashboardDay{}
	for day := from; day.Before(to); day = day.AddDate(0, 0, 1) {
		next := day.AddDate(0, 0, 1)
		row := operations.DashboardDay{Date: day.Format("2006-01-02")}
		s.mu.RLock()
		applicationID := recordApplicationID(application.IDFromContext(ctx))
		for _, grant := range s.rewardGrants {
			if recordApplicationID(grant.ApplicationID) != applicationID {
				continue
			}
			if !grant.GrantedAt.Before(day) && grant.GrantedAt.Before(next) {
				row.RewardCompletions++
			}
		}
		for _, item := range s.benefitGrants {
			if item.ApplicationID != applicationID || item.CreatedAt.Before(day) || !item.CreatedAt.Before(next) {
				continue
			}
			if item.Type == "vip" {
				row.VIPActivations++
			}
			if item.Type == "episode_access" {
				var data monetization.AccessData
				if err := json.Unmarshal(item.Data, &data); err != nil {
					s.mu.RUnlock()
					return nil, err
				}
				row.EpisodeUnlocks += int64(len(data.EpisodeIDs))
			}
		}

		for key, created := range s.visitors {
			prefix := scopedVisitorKey(application.IDFromContext(ctx), "")
			if ((prefix == "" && !strings.Contains(key, ":")) || (prefix != "" && strings.HasPrefix(key, prefix))) && !created.Before(day) && created.Before(next) {
				row.NewUsers++
			}
		}
		s.mu.RUnlock()
		result = append(result, row)
	}
	return result, nil
}

// DashboardAudience 测试内存仓储尚未注入客户端信号时返回空采集状态。
func (s *Store) DashboardAudience(context.Context, time.Time) (operations.DashboardAudience, error) {
	return operations.DashboardAudience{}, nil
}

// CampaignRewardRanking 从不可变奖励账本生成当前应用的投放前 50 名。
func (s *Store) CampaignRewardRanking(ctx context.Context, from, to time.Time) ([]operations.CampaignRewardRank, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	app := recordApplicationID(application.IDFromContext(ctx))
	counts := map[string]int64{}
	users := map[string]map[string]bool{}
	for _, grant := range s.rewardGrants {
		if recordApplicationID(grant.ApplicationID) != app || grant.CampaignID == "" || grant.GrantedAt.Before(from) || !grant.GrantedAt.Before(to) {
			continue
		}
		counts[grant.CampaignID]++
		if users[grant.CampaignID] == nil {
			users[grant.CampaignID] = map[string]bool{}
		}
		users[grant.CampaignID][grant.VisitorID] = true
	}
	result := []operations.CampaignRewardRank{}
	for _, item := range s.campaigns {
		if recordApplicationID(item.ApplicationID) != app || counts[item.ID] == 0 {
			continue
		}
		result = append(result, operations.CampaignRewardRank{CampaignID: item.ID, Name: item.Name, Code: item.Code, LandingURL: item.LandingURL, RewardCompletions: counts[item.ID], RewardUsers: int64(len(users[item.ID]))})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].RewardCompletions == result[j].RewardCompletions {
			return len(result[i].CampaignID) < len(result[j].CampaignID) || (len(result[i].CampaignID) == len(result[j].CampaignID) && result[i].CampaignID < result[j].CampaignID)
		}
		return result[i].RewardCompletions > result[j].RewardCompletions
	})
	if len(result) > 50 {
		result = result[:50]
	}
	return result, nil
}
