package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"sort"
	"strings"
	"time"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/campaign"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/pkg/requesttime"
)

// RewardOffer 使用认证后的业务 visitorID 对当前应用激励候选集合取模，返回单个字符串 AdUnitID。
// policy 是当前应用策略快照，kind 定位规则编码或既有入口类型，vipProgress 是可选的进度展示值。
// 候选集合不变时同一 UID 在所有实例上选位一致；规则不可用返回 ErrInvalidRewardClaim，依赖故障保留错误。
// 领取按当前允许集合校验，保持旧 Mini 合同。
func (s *Service) RewardOffer(
	ctx context.Context,
	visitorID string,
	policy monetization.MonetizationPolicy,
	kind string,
	vipProgress *monetization.VIPProgress,
) (*monetization.AccessOffer, error) {
	offer, err := s.rewardOffer(ctx, visitorID, policy, kind, vipProgress, "")
	if err != nil {
		level := slog.LevelError
		if errors.Is(err, monetization.ErrInvalidRewardClaim) {
			level = slog.LevelWarn
		}
		slog.Log(ctx, level, "build reward offer failed", "error", err)
	}
	return offer, err
}

// rewardOffer 下发时按 UID 选位，领奖时只校验实际完播编码，不修改共享规则快照。
// completedAdUnitID 非空时不强求它等于当前 UID 的选位，允许旧 Mini 在候选集合变更后提交仍有效的广告。
func (s *Service) rewardOffer(ctx context.Context, visitorID string, policy monetization.MonetizationPolicy, kind string, vipProgress *monetization.VIPProgress, completedAdUnitID string) (*monetization.AccessOffer, error) {
	if s == nil || s.store == nil || !policy.Enabled() {
		slog.WarnContext(ctx, "reward offer rejected", "error", monetization.ErrInvalidRewardClaim)
		return nil, monetization.ErrInvalidRewardClaim
	}
	selected, ok := policy.ForOffer(kind)
	if !ok {
		return nil, monetization.ErrInvalidRewardClaim
	}
	policy = selected
	if policy.Rule != nil {
		binding := policy.Bindings[policy.Rule.Code]
		candidates := binding.AdUnitIDs
		if len(candidates) == 0 && binding.AdUnitID != "" {
			candidates = []string{binding.AdUnitID}
		}
		if len(candidates) == 0 || (completedAdUnitID != "" && !slices.Contains(candidates, completedAdUnitID)) {
			slog.WarnContext(ctx, "reward ad unit rejected by rule", "error", monetization.ErrInvalidRewardClaim)
			return nil, monetization.ErrInvalidRewardClaim
		}
		binding.AdUnitID = completedAdUnitID
		if completedAdUnitID == "" {
			var err error
			binding.AdUnitID, err = rewardedAdUnitForUID(visitorID, candidates)
			if err != nil {
				slog.ErrorContext(ctx, "select reward ad unit by uid failed", "error", err)
				return nil, err
			}
		}
		// 每个请求只改自己的绑定，避免并发下发污染缓存或其他规则的广告位。
		policy.Bindings = maps.Clone(policy.Bindings)
		policy.Bindings[policy.Rule.Code] = binding
		offer, err := monetization.DefaultStrategies().Offer(ctx, policy, s.store)
		if err != nil {
			level := slog.LevelError
			if errors.Is(err, monetization.ErrInvalidRewardClaim) {
				level = slog.LevelWarn
			}
			slog.Log(ctx, level, "build access offer strategy failed", "error", err)
			return nil, rewardDependencyError(err)
		}
		offer.VIPProgress = vipProgress
		return offer, nil
	}
	adUnitCode := strings.TrimSpace(policy.AdUnitForOffer(kind))
	if adUnitCode == "" || (completedAdUnitID != "" && completedAdUnitID != adUnitCode) {
		slog.WarnContext(ctx, "reward offer rejected", "error", monetization.ErrInvalidRewardClaim)
		return nil, monetization.ErrInvalidRewardClaim
	}
	configured, err := s.store.HasRewardedAdUnit(ctx, adUnitCode)
	if err != nil {
		slog.ErrorContext(ctx, "has rewarded ad unit failed", "error", err)
		return nil, rewardDependencyError(err)
	}
	if !configured {
		slog.WarnContext(ctx, "reward offer rejected", "error", monetization.ErrInvalidRewardClaim)
		return nil, monetization.ErrInvalidRewardClaim
	}
	offer := &monetization.AccessOffer{
		ID: policy.OfferIDForKind(kind), Kind: kind, AdUnitID: adUnitCode,
		ApplicationID: policy.ApplicationID, VIPProgress: vipProgress,
	}
	offer.Method = "rewarded_ad"
	if policy.VIP.Enabled {
		vip := monetization.ConsumerMonetizationFromPolicy(policy).VIP
		offer.VIP = &vip
	}
	if policy.Rule != nil {
		offer.RuleCode = policy.Rule.Code
		for _, grant := range policy.Rule.Grants {
			offer.Benefits = append(offer.Benefits, grant.Type)
		}
	}
	return offer, nil
}

// ClaimReward 处理 TikTok SDK 已确认完整结束的激励广告，并返回幂等结算结果。
// 本方法不创建“广告展示尝试”。ClaimID 只承担网络重试幂等；真正的业务事实是与权益
// 发放处于同一数据库事务的 RewardGrant。服务端仍无法独立证明客户端 SDK 的 isEnded，
// 因此额外保存一份客户端可见的短期领取凭据不会提高真实性。
func (s *Service) ClaimReward(
	ctx context.Context,
	visitorID string,
	input monetization.ClaimRewardInput,
) (result *RewardClaimResult, resultErr error) {
	ctx = requesttime.Ensure(ctx, s.now)
	defer func() {
		if resultErr == nil && result != nil {
			// 补充状态失败不推翻已经提交的奖励；旧字段仍确认结算，新客户端可走兼容恢复。
			if result.RewardType == monetization.RewardTypeEntryProgress {
				state, err := s.EntryAdStatus(ctx, visitorID, result.DramaID)
				if err != nil {
					slog.ErrorContext(ctx, "read settled entry ad status failed", "error", err)
				} else {
					result.EntryAdStatus = &state
				}
			}
			if result.RewardType == monetization.RewardTypeEpisodeUnlock && result.Drama == nil {
				state, err := s.rewardDramaState(ctx, visitorID, result.DramaID)
				if err != nil {
					slog.ErrorContext(ctx, "read settled episode access failed", "error", err)
				} else {
					result.Drama = state
				}
			}
			slog.InfoContext(ctx, "consumer reward claim finished", "claim_id", input.ClaimID, "status", result.Status)
		}
	}()
	visitorID = strings.TrimSpace(visitorID)
	if visitorID == "" {
		slog.WarnContext(ctx, "claim reward rejected", "error", monetization.ErrInvalidRewardClaim)
		return nil, monetization.ErrInvalidRewardClaim
	}

	// 网络重试必须优先恢复既有结算。若先按当前策略重新校验，配置修改或已经发放的
	// 权益会把一次成功请求误判为无效，破坏幂等语义。

	existing, found, err := s.store.FindRewardGrant(ctx, visitorID, input.ClaimID)
	if err != nil {
		slog.ErrorContext(ctx, "find reward grant failed", "error", err)
		return nil, rewardDependencyError(err)
	}
	if found {
		return s.recoverRewardClaim(ctx, visitorID, existing, input)
	}
	return s.claimNewReward(ctx, visitorID, input)
}

// recoverRewardClaim 核对同键不可变内容后，使用已落库目标恢复播放和提示；重试不改写事实时间。
func (s *Service) recoverRewardClaim(ctx context.Context, visitorID string, existing monetization.RewardGrant, input monetization.ClaimRewardInput) (*RewardClaimResult, error) {
	if !rewardClaimMatchesGrant(existing, input) {
		slog.WarnContext(ctx, "reward retry immutable content rejected", "error", monetization.ErrInvalidRewardClaim)
		return nil, monetization.ErrInvalidRewardClaim
	}
	claim := claimResultFromGrant(existing, monetization.ClaimStatusAlreadyGranted)
	if existing.RewardType == monetization.RewardTypeVIPProgress {
		state, stateErr := s.rewardVIPProgress(ctx, visitorID, existing)
		if stateErr != nil {
			slog.ErrorContext(ctx, "vip progress failed", "error", stateErr)
			return nil, stateErr
		}
		claim.VIPProgress = &state
	}
	result := &RewardClaimResult{ClaimResult: claim}
	if claim.Unlocked && claim.RewardType == monetization.RewardTypeEpisodeUnlock {
		next, nextErr := s.nextRewardAd(ctx, visitorID, existing)
		if nextErr != nil {
			slog.ErrorContext(ctx, "resolve next reward ad for recovered claim failed", "error", nextErr)
		} else {
			result.NextAd = next
		}
		playback, playbackErr := s.ResolveOfficialPlay(ctx, visitorID, claim.DramaID, claim.EpisodeID)
		if playbackErr != nil {
			slog.ErrorContext(ctx, "resolve reward playback for recovered claim failed", "error", playbackErr)
		} else if playback.Status == PlayStatusPlayable {
			result.Playback = playback
		}
	}
	return result, nil
}

// claimNewReward 校验当前规则并将首次授予交给用户锁内的存储事务。
func (s *Service) claimNewReward(ctx context.Context, visitorID string, input monetization.ClaimRewardInput) (*RewardClaimResult, error) {
	policy, err := s.ResolveMonetizationPolicy(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "resolve monetization policy failed", "error", err)
		// 配置确实不存在属于业务不可领取；数据库不可用必须保留可重试错误。
		if errors.Is(err, ErrNotFound) {
			slog.WarnContext(ctx, "claim reward rejected", "error", monetization.ErrInvalidRewardClaim)
			return nil, monetization.ErrInvalidRewardClaim
		}
		return nil, err
	}
	if !policy.Enabled() {
		slog.WarnContext(ctx, "claim reward rejected", "error", monetization.ErrInvalidRewardClaim)
		return nil, monetization.ErrInvalidRewardClaim
	}
	applicationID, _, offerKind, err := monetization.ParseOfferID(input.OfferID)
	if err != nil {
		slog.ErrorContext(ctx, "parse offer id failed", "error", err)
	}
	if err != nil || applicationID != policy.ApplicationID {
		slog.WarnContext(ctx, "claim reward rejected", "error", monetization.ErrInvalidRewardClaim)
		return nil, monetization.ErrInvalidRewardClaim
	}

	selected, ok := policy.ForClaimOffer(offerKind)
	if !ok {
		slog.WarnContext(ctx, "claim reward rule unavailable", "error", monetization.ErrInvalidRewardClaim)
		return nil, monetization.ErrInvalidRewardClaim
	}
	policy = selected
	if policy.Rule != nil {
		offerKind = policy.SelectedKind()
	}
	offer, err := s.rewardOffer(ctx, visitorID, policy, offerKind, nil, input.CompletedAdUnitID)
	if err != nil {
		level := slog.LevelError
		if errors.Is(err, monetization.ErrInvalidRewardClaim) {
			level = slog.LevelWarn
		}
		slog.Log(ctx, level, "reward offer failed", "error", err)
		return nil, err
	}
	if offer == nil || offer.AdUnitID != input.CompletedAdUnitID {
		slog.WarnContext(ctx, "claim reward rejected", "error", monetization.ErrInvalidRewardClaim)
		return nil, monetization.ErrInvalidRewardClaim
	}

	now := requesttime.At(ctx, s.now).UTC()
	period := monetization.RewardPeriodFor(now, application.LocationFromContext(ctx))
	grant := monetization.RewardGrant{
		ClaimID: input.ClaimID, ApplicationID: application.IDFromContext(ctx), VisitorID: visitorID,
		CampaignID: campaign.AttributionFromContext(ctx).ID,
		AdUnitID:   input.CompletedAdUnitID,
		PeriodKey:  period.Key, PeriodStartsAt: period.StartsAt, PeriodExpiresAt: period.ExpiresAt,
		GrantedAt: now,
	}

	handler := rewardClaimStrategies[offerKind]
	if handler == nil {
		return nil, monetization.ErrInvalidRewardClaim
	}
	rewardSnapshot, err := handler.Prepare(ctx, s, visitorID, input, policy, period, now, &grant)
	if err != nil {
		if errors.Is(err, monetization.ErrInvalidRewardClaim) || errors.Is(err, ErrRateLimited) {
			slog.WarnContext(ctx, "prepare reward strategy rejected", "error", err)
		} else {
			slog.ErrorContext(ctx, "prepare reward strategy failed", "error", err)
		}
		// 首次幂等查询与业务校验之间可能已有相同领取提交，优先恢复该事实。
		settled, found, findErr := s.store.FindRewardGrant(ctx, visitorID, input.ClaimID)
		if findErr != nil {
			slog.ErrorContext(ctx, "recover reward after prepare rejection failed", "error", findErr)
			return nil, rewardDependencyError(findErr)
		}
		if found {
			return s.recoverRewardClaim(ctx, visitorID, settled, input)
		}
		return nil, err
	}
	grant.ProgressKey = policy.ProgressKey()
	if grant.RewardType != monetization.RewardTypeEntryProgress {
		if err := s.buildRewardEntitlements(ctx, policy, offer, &grant, rewardSnapshot); err != nil {
			slog.ErrorContext(ctx, "build reward entitlements failed", "error", err)
			return nil, err
		}
	}

	// 上面的读取用于及时拒绝无效领取；并发安全仍由仓储事务再次检查幂等键、日限额与权益保证。
	granted, err := s.store.GrantReward(ctx, grant, now)
	if err != nil {
		slog.ErrorContext(ctx, "grant reward failed", "error", err)
		return nil, rewardDependencyError(err)
	}
	if granted.Status == monetization.ClaimStatusAlreadyGranted {
		// 并发幂等命中不能拿本次请求的目录快照签发另一集播放许可。
		if granted.SettledGrant == nil {
			slog.ErrorContext(ctx, "recover concurrent reward missing settled grant", "error", ErrServiceUnavailable)
			return nil, ErrServiceUnavailable
		}
		return s.recoverRewardClaim(ctx, visitorID, *granted.SettledGrant, input)
	}
	// VIP 进度已由结算事务返回，不在提交后重复查询同一进度。
	result := &RewardClaimResult{ClaimResult: granted}
	if granted.Unlocked && granted.RewardType == monetization.RewardTypeEpisodeUnlock && rewardSnapshot != nil {
		rewardSnapshot.ownedEpisodeIDs = granted.OwnedEpisodeIDs
		// 目录复用本次已验证快照，权益来自事务内批量读取和实际授予，避免前后端重复查询。
		owned := maps.Clone(granted.OwnedEpisodeIDs)
		if owned == nil {
			owned = map[string]bool{}
		}
		if granted.SettledGrant != nil {
			for _, id := range granted.SettledGrant.UnlockEpisodeIDs {
				owned[id] = true
			}
		}
		result.Drama = rewardDramaFromSnapshot(rewardSnapshot.drama, policy.FreeEpisodeCount, owned)
		if application.IsDirect(ctx) {
			identity, signErr := s.playbackIdentity(ctx, rewardSnapshot.drama, rewardSnapshot.target)
			// 已提交奖励不能因为签名依赖失败而要求重新观看；客户端可以单独重试播放解析。
			if signErr == nil {
				result.Playback = rewardSnapshot.playback()
				result.Playback.Playback = identity
			} else {
				slog.ErrorContext(ctx, "resolve committed reward playback failed", "error", signErr)
			}
		} else {
			result.Playback = rewardSnapshot.playback()
		}
		if granted.SettledGrant != nil {
			result.NextAd = nextRewardAdFromSnapshot(policy, offer, *granted.SettledGrant, granted.DailyLimitReached, rewardSnapshot)
			if next := result.NextAd; next != nil {
				candidates := policy.Bindings[next.Offer.RuleCode].AdUnitIDs
				if len(candidates) > 0 {
					adUnitID, err := rewardedAdUnitForUID(visitorID, candidates)
					if err != nil {
						// 奖励已经提交；后续入口分配失败只停止连续提示，不能要求用户重新观看。
						slog.ErrorContext(ctx, "select next reward ad unit by uid failed", "error", err)
						result.NextAd = nil
					} else {
						next.Offer.AdUnitID = adUnitID
					}
				}
			}
		}
	}
	return result, nil
}

// claimResultFromGrant 从既有奖励事实恢复领取响应，VIP 实时进度由调用方另外读取。
func claimResultFromGrant(grant monetization.RewardGrant, status string) monetization.ClaimResult {
	return monetization.ClaimResult{
		ClaimID: grant.ClaimID, Status: status, Unlocked: grant.RewardType == monetization.RewardTypeEpisodeUnlock,
		DramaID: grant.DramaID, EpisodeID: grant.EpisodeID, RewardType: grant.RewardType,
	}
}

// ResolveMonetizationPolicy 读取当前应用的权威变现配置并投影为消费者执行快照。
func (s *Service) ResolveMonetizationPolicy(ctx context.Context) (monetization.MonetizationPolicy, error) {
	if s == nil || s.store == nil {
		slog.WarnContext(ctx, "resolve monetization policy rejected", "error", monetization.ErrInvalidRewardClaim)
		return monetization.MonetizationPolicy{}, monetization.ErrInvalidRewardClaim
	}
	config, err := s.store.GetApplicationMonetization(ctx)
	if errors.Is(err, ErrNotFound) {
		// 未绑定策略时停止提供新入口，仍允许通过历史权益播放。
		return monetization.MonetizationPolicy{ApplicationID: application.IDFromContext(ctx), Status: monetization.MonetizationDisabled, Rules: []monetization.UnlockRule{}}, nil
	}
	if err != nil {
		slog.ErrorContext(ctx, "get application monetization failed", "error", err)
		return monetization.MonetizationPolicy{}, rewardDependencyError(err)
	}
	if application.MiniReviewFromContext(ctx).EpisodeOnly {
		doc, _ := monetization.StrategyFromConfig(config)
		doc = monetization.EpisodeOnlyDocument(doc)
		config.Document = &doc
	}
	return config.Policy(), nil
}

// VIPProgress 只读 Visitor 在当前应用自然日内已结算的 VIP 广告进度和已领取权益。
func (s *Service) VIPProgress(ctx context.Context, visitorID string) (monetization.VIPProgress, error) {
	ctx = requesttime.Ensure(ctx, s.now)
	policy, err := s.ResolveMonetizationPolicy(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "resolve monetization policy failed", "error", err)
		return monetization.VIPProgress{}, err
	}
	period := monetization.RewardPeriodFor(requesttime.At(ctx, s.now), application.LocationFromContext(ctx))
	state, err := s.store.FindVIPProgress(ctx, strings.TrimSpace(visitorID), policy, period, requesttime.At(ctx, s.now))
	if err != nil {
		slog.ErrorContext(ctx, "find vip progress failed", "error", err)
	}
	return state, rewardDependencyError(err)
}

// ClaimVIP 在用户主动领取时核对当前规则和自然日进度，达标后幂等发放 VIP 权益。
// 未达标或规则不可用时拒绝；广告结算和只读进度查询均不会代替这次领取。
func (s *Service) ClaimVIP(ctx context.Context, visitorID string) (monetization.VIPProgress, error) {
	ctx = requesttime.Ensure(ctx, s.now)
	visitorID = strings.TrimSpace(visitorID)
	if visitorID == "" {
		slog.WarnContext(ctx, "claim vip rejected", "error", monetization.ErrInvalidRewardClaim)
		return monetization.VIPProgress{}, monetization.ErrInvalidRewardClaim
	}
	policy, err := s.ResolveMonetizationPolicy(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "resolve monetization policy for vip claim failed", "error", err)
		if errors.Is(err, ErrNotFound) {
			return monetization.VIPProgress{}, monetization.ErrInvalidRewardClaim
		}
		return monetization.VIPProgress{}, err
	}
	selected, ok := policy.ForOffer(monetization.AccessOfferRewardedVIP)
	if !policy.Enabled() || !ok || !selected.VIP.Enabled {
		slog.WarnContext(ctx, "claim vip rejected", "error", monetization.ErrInvalidRewardClaim)
		return monetization.VIPProgress{}, monetization.ErrInvalidRewardClaim
	}
	period := monetization.RewardPeriodFor(requesttime.At(ctx, s.now), application.LocationFromContext(ctx))
	state, err := s.store.FindVIPProgress(ctx, visitorID, selected, period, requesttime.At(ctx, s.now))
	if err != nil {
		slog.ErrorContext(ctx, "find vip progress for claim failed", "error", err)
		return monetization.VIPProgress{}, rewardDependencyError(err)
	}
	if state.Active {
		return state, nil
	}
	if state.RequiredCount < 1 || state.AdCount < state.RequiredCount {
		slog.WarnContext(ctx, "claim vip before threshold rejected", "error", monetization.ErrInvalidRewardClaim)
		return monetization.VIPProgress{}, monetization.ErrInvalidRewardClaim
	}
	state, err = s.store.ReconcileVIPProgress(ctx, visitorID, selected, period, requesttime.At(ctx, s.now))
	if err != nil {
		slog.ErrorContext(ctx, "grant manually claimed vip failed", "error", err)
		return monetization.VIPProgress{}, rewardDependencyError(err)
	}
	return state, nil
}

func (s *Service) playbackVIPProgress(ctx context.Context, visitorID string, policy monetization.MonetizationPolicy) (monetization.VIPProgress, error) {
	now := requesttime.At(ctx, s.now)
	period := monetization.RewardPeriodFor(now, application.LocationFromContext(ctx))
	state, err := s.store.FindVIPProgress(ctx, visitorID, policy, period, now)
	if err != nil {
		slog.ErrorContext(ctx, "read playback vip progress failed", "error", err)
	}
	return state, err
}

// 同一播放决策只读取一次策略和 VIP 状态；授权分集仍由既有权益账本核对。
func (s *Service) episodeAccessForPolicy(ctx context.Context, visitorID string, drama Drama, episode Episode, policy monetization.MonetizationPolicy, vipState monetization.VIPProgress) (bool, error) {
	if episode.Number <= policy.FreeEpisodeCount || vipState.Active {
		return true, nil
	}
	unlocked, err := s.store.HasEntitlement(ctx, visitorID, drama.ID, episode.ID, requesttime.At(ctx, s.now))
	if err != nil {
		slog.ErrorContext(ctx, "has entitlement failed", "error", err)
	}
	return unlocked, err
}

// rewardClaimStrategy 将领取领域约束与幂等主流程分离，新增入口只注册对应处理器。
type rewardClaimStrategy interface {
	// Prepare 校验领域状态并填充业务事实，内容奖励返回可复用目录；VIP 返回 nil，存储事务仍负责并发安全。
	Prepare(context.Context, *Service, string, monetization.ClaimRewardInput, monetization.MonetizationPolicy, monetization.RewardPeriod, time.Time, *monetization.RewardGrant) (*rewardEpisodeSnapshot, error)
}

var rewardClaimStrategies = map[string]rewardClaimStrategy{
	monetization.AccessOfferRewardedEpisode: episodeRewardStrategy{},
	monetization.AccessOfferRewardedVIP:     vipRewardStrategy{},
	monetization.AccessOfferRewardedEntry:   entryRewardStrategy{},
}

// buildRewardEntitlements 按当前规则全部授予项的需求并集复用已验证目录，不再查询目录或用户权益。
// published_snapshot 仍由首次目录读取完整解码；这里缩小的是构造及事务内权益查询范围。
func (s *Service) buildRewardEntitlements(ctx context.Context, policy monetization.MonetizationPolicy, offer *monetization.AccessOffer, grant *monetization.RewardGrant, snapshot *rewardEpisodeSnapshot) error {
	rule := policy.Rule
	if rule == nil {
		config := monetization.ApplicationMonetizationConfig{Status: policy.Status, EpisodeReward: policy.EpisodeReward, VIP: policy.VIP}
		doc, _ := monetization.StrategyFromConfig(config)
		for i := range doc.Rules {
			if doc.Rules[i].Code == grantOfferKind(*grant) {
				rule = &doc.Rules[i]
				break
			}
		}
	}
	if rule == nil {
		return monetization.ErrInvalidRewardClaim
	}
	grant.BuildRule = rule
	build := monetization.GrantContext{ApplicationID: grant.ApplicationID, UserID: grant.VisitorID, SourceType: "reward_grant", SourceID: grant.ClaimID, DramaID: grant.DramaID, Now: grant.GrantedAt, PeriodEnd: grant.PeriodExpiresAt}
	episodeCount, wholeDrama := 0, false
	for _, template := range rule.Grants {
		if template.Type == "drama_access" {
			// 包含未来更新的整剧权益也保留当前分集事实，不能省略已有授予集合。
			wholeDrama = true
		}
		if template.Type == "episode_access" {
			var config struct {
				// Count 决定该授予项从共同未拥有候选中取得的数量。
				Count int `json:"count"`
			}
			if err := json.Unmarshal(template.Config, &config); err != nil {
				slog.ErrorContext(ctx, "read reward episode count failed", "error", err)
				return err
			}
			// 多个逐集授予项共享同一未拥有候选前缀，需求取最大值而非第一项或总和。
			episodeCount = max(episodeCount, config.Count)
		}
	}
	if snapshot != nil {
		episodes := append([]Episode(nil), snapshot.drama.Episodes...)
		sort.SliceStable(episodes, func(i, j int) bool { return episodes[i].Number < episodes[j].Number })
		snapshot.episodes = episodes
		for _, episode := range episodes {
			// 结算回包需要完整选集状态：一次批量读取本剧投影，也供连续提示和授予构造复用。
			grant.OwnershipEpisodeIDs = append(grant.OwnershipEpisodeIDs, episode.ID)
			if ready, _ := officialPlaybackReady(snapshot.drama, episode); !ready {
				continue
			}
			if wholeDrama {
				build.AllEpisodeIDs = append(build.AllEpisodeIDs, episode.ID)
			}
			if episodeCount == 0 || episode.Number < snapshot.target.Number || episode.Number <= policy.FreeEpisodeCount {
				continue
			}
			if episodeCount == 1 && episode.ID != snapshot.target.ID {
				continue
			}
			build.EpisodeIDs = append(build.EpisodeIDs, episode.ID)
		}
	}
	grant.BuildContext = &build
	if err := monetization.RebuildRewardEntitlements(grant, nil); err != nil {
		slog.ErrorContext(ctx, "resolve entitlement templates failed", "error", err)
		return err
	}
	return nil
}

func grantOfferKind(grant monetization.RewardGrant) string {
	if grant.RewardType == monetization.RewardTypeVIPProgress {
		return monetization.AccessOfferRewardedVIP
	}
	return monetization.AccessOfferRewardedEpisode
}

// rewardVIPProgress 恢复已结算规则的当前进度与最新阈值，不重复累计或借用另一规则的次数。
// 阈值降低时只更新可领取状态，不重写原广告结算事实。
// 原规则已移除时阈值为零，既有已发权益仍按实际有效期展示。
func (s *Service) rewardVIPProgress(ctx context.Context, visitorID string, grant monetization.RewardGrant) (monetization.VIPProgress, error) {
	if grant.ProgressKey == "" {
		return s.VIPProgress(ctx, visitorID)
	}
	latest, err := s.ResolveMonetizationPolicy(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "read current vip reward policy failed", "error", err)
		return monetization.VIPProgress{}, err
	}
	policy := monetization.MonetizationPolicy{ApplicationID: grant.ApplicationID, ProgressNamespace: grant.ProgressKey}
	for _, candidate := range latest.RulePolicies() {
		if candidate.ProgressKey() == grant.ProgressKey && candidate.SelectedKind() == monetization.AccessOfferRewardedVIP {
			policy = candidate
			break
		}
	}
	period := monetization.RewardPeriodFor(requesttime.At(ctx, s.now), application.LocationFromContext(ctx))
	state, err := s.store.FindVIPProgress(ctx, visitorID, policy, period, requesttime.At(ctx, s.now))
	if err != nil {
		slog.ErrorContext(ctx, "find settled rule vip progress failed", "error", err)
	}
	return state, err
}
