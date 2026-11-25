package memory

import (
	"context"
	"sort"

	"drama-server/internal/biz/application"
	domaincampaign "drama-server/internal/biz/campaign"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
)

// 查询并返回符合条件的投放活动列表。
func (s *Store) ListCampaigns(ctx context.Context) ([]domaincampaign.Campaign, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	applicationID := recordApplicationID(application.IDFromContext(ctx))
	items := make([]domaincampaign.Campaign, 0)
	for _, item := range s.campaigns {
		if item.ApplicationID == applicationID {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Code < items[j].Code })
	return items, nil
}

// 结合当前上下文解析投放活动。
func (s *Store) ResolveCampaign(_ context.Context, applicationID, code string) (domaincampaign.Attribution, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	app, appExists := s.applications[applicationID]
	for _, item := range s.campaigns {
		if item.ApplicationID == applicationID && item.Code == code && item.Status == domaincampaign.StatusActive {
			if !appExists || app.Status != "active" {
				return domaincampaign.Attribution{}, false, nil
			}
			dramaID, episodeID := item.DramaID, item.EpisodeID
			if dramaID != "" {
				drama, exists := s.dramas[dramaID]
				published, visible := drama.PublishedForApplication(applicationID)
				validEpisode := episodeID == ""
				if visible {
					for _, episode := range published.Episodes {
						if episode.ID == episodeID {
							validEpisode = true
							break
						}
					}
				}
				if !exists || !visible || !validEpisode {
					return domaincampaign.Attribution{}, false, nil
				}
			}
			return domaincampaign.Attribution{
				ID: item.ID, Code: item.Code, DramaID: dramaID,
				EpisodeID: episodeID, ExperimentKey: item.ExperimentKey,
			}, true, nil
		}
	}
	return domaincampaign.Attribution{}, false, nil
}

// 查询并返回指定的投放活动。
func (s *Store) FindCampaign(ctx context.Context, id string) (domaincampaign.Campaign, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	campaign, exists := s.campaigns[id]
	if !exists || campaign.ApplicationID != recordApplicationID(application.IDFromContext(ctx)) {
		return domaincampaign.Campaign{}, domain.ErrNotFound
	}
	return campaign, nil
}

// 校验并保存投放活动。
func (s *Store) SaveCampaign(ctx context.Context, campaign *domaincampaign.Campaign) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if campaign.ID == "" {
		campaign.ID = s.allocateIDLocked()
	}
	applicationID := recordApplicationID(application.IDFromContext(ctx))
	if campaign.ApplicationID != applicationID {
		return domain.ErrNotFound
	}
	for id, existing := range s.campaigns {
		if id != campaign.ID && existing.ApplicationID == applicationID && existing.Code == campaign.Code {
			return operations.ErrConflict
		}
	}
	if existing, exists := s.campaigns[campaign.ID]; exists && existing.ApplicationID != applicationID {
		return domain.ErrNotFound
	}
	s.campaigns[campaign.ID] = *campaign
	return nil
}
