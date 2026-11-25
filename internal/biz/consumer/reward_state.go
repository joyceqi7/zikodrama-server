package consumer

import (
	"context"
	"log/slog"

	"drama-server/internal/biz/monetization"
	"drama-server/internal/pkg/requesttime"
)

// rewardDramaFromSnapshot 只将服务端确认的有效权益映射为展示状态，不由客户端推算解锁范围。
func rewardDramaFromSnapshot(drama Drama, freeCount int, owned map[string]bool) *RewardDramaState {
	state := &RewardDramaState{DramaSummary: drama.Summary(), Episodes: make([]EpisodeSummary, 0, len(drama.Episodes))}
	for _, episode := range drama.Episodes {
		summary := episode.Summary()
		summary.AccessMode = monetization.EpisodeAccessRewarded
		if episode.Number <= freeCount {
			summary.AccessMode = monetization.EpisodeAccessFree
		} else if owned[episode.ID] {
			summary.AccessMode = monetization.EpisodeAccessUnlocked
		}
		state.Episodes = append(state.Episodes, summary)
	}
	return state
}

// rewardDramaState 幂等恢复只复用原结算事实，展示状态仍按当前有效权益读取，不能复活已撤销权益。
func (s *Service) rewardDramaState(ctx context.Context, visitorID, dramaID string) (*RewardDramaState, error) {
	drama, err := s.FindDrama(ctx, dramaID)
	if err != nil {
		slog.ErrorContext(ctx, "find settled drama failed", "error", err)
		return nil, err
	}
	policy, err := s.ResolveMonetizationPolicy(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "read settled drama policy failed", "error", err)
		return nil, err
	}
	ids := make([]string, 0, len(drama.Episodes))
	for _, episode := range drama.Episodes {
		ids = append(ids, episode.ID)
	}
	owned, err := s.store.FindOwnedEpisodeIDs(ctx, visitorID, drama.ID, ids, requesttime.At(ctx, s.now))
	if err != nil {
		slog.ErrorContext(ctx, "read settled drama ownership failed", "error", err)
		return nil, err
	}
	return rewardDramaFromSnapshot(*drama, policy.FreeEpisodeCount, owned), nil
}
