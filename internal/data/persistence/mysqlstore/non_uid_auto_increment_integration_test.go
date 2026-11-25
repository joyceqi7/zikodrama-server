package mysqlstore

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/operations"
	"drama-server/internal/biz/sharding"
	"drama-server/internal/biz/visitor"
)

// nonUIDAutoIncrementUser 保存由现有 UID 流程创建的测试用户及其可信路由。
type nonUIDAutoIncrementUser struct {
	id     string
	bucket uint16
	ctx    context.Context
}

// nonUIDAutoIncrementUsers 先使用未修改的身份流程创建不同物理分片用户，再禁用号段器检测非 UID 写入的隐藏依赖。
func nonUIDAutoIncrementUsers(t *testing.T, f *iaaFixture, shards ...uint16) []nonUIDAutoIncrementUser {
	t.Helper()
	requireUIDIntegration(t)
	users := make([]nonUIDAutoIncrementUser, 0, len(shards))
	for _, shard := range shards {
		var hash string
		var bucket uint16
		for attempt := 0; attempt < 10000; attempt++ {
			hash = visitor.TikTokOpenIDHash("101", fmt.Sprintf("non-uid-auto-increment-%d-%d", shard, attempt))
			bucket = logicalBucket("101", hash)
			if bucket%sharding.TableShardCount == shard {
				break
			}
		}
		if bucket%sharding.TableShardCount != shard {
			t.Fatalf("未找到物理分片 %d 的身份样本", shard)
		}
		id, err := f.store.ResolveTikTokIdentity(context.Background(), visitor.TikTokIdentityRecord{ApplicationID: "101", OpenIDHash: hash})
		if err != nil {
			t.Fatalf("创建测试用户失败: %v", err)
		}
		ctx := sharding.WithBucketID(application.WithContext(context.Background(), "101"), bucket)
		users = append(users, nonUIDAutoIncrementUser{id: id, bucket: bucket, ctx: ctx})
		for _, base := range []string{"entitlement_grants", "user_entitlement_state", "entitlement_revocations", "reward_grants", "application_vip_progress"} {
			if err := f.store.data.DB().Exec("ALTER TABLE `" + userTable(base, bucket) + "` AUTO_INCREMENT=9007199254740993").Error; err != nil {
				t.Fatalf("设置 %s 分表大整数起点失败: %v", base, err)
			}
		}
	}
	// 后续写入如仍调用 nextID 会立即失败，不能依赖号段缓存掩盖分配器耦合。
	f.store.ids = nil
	return users
}

// nonUIDEpisodeEntitlements 使用实际策略生成有效权益，避免测试手写与业务规则脱节的 JSON。
func nonUIDEpisodeEntitlements(t *testing.T, user nonUIDAutoIncrementUser, source string, now time.Time) []monetization.Entitlement {
	t.Helper()
	items, err := monetization.DefaultStrategies().BuildEntitlements(
		[]monetization.GrantTemplate{{Type: "episode_access", Config: []byte(`{"selection":"next_locked_episodes","count":1,"validity":"unlimited"}`)}},
		monetization.GrantContext{ApplicationID: "101", UserID: user.id, SourceType: "order", SourceID: source, DramaID: "201", EpisodeIDs: []string{"302"}, Now: now},
	)
	if err != nil || len(items) != 1 {
		t.Fatalf("生成测试权益失败: count=%d err=%v", len(items), err)
	}
	return items
}

// TestNonUIDEntitlementAutoIncrementIntegration 验证物理分表独立发号、大整数关联、幂等与回滚，撤销同 ID 时不得串分片。
func TestNonUIDEntitlementAutoIncrementIntegration(t *testing.T) {
	f := newIAAFixture(t)
	users := nonUIDAutoIncrementUsers(t, f, 0, 1)
	now := time.Date(2026, time.September, 23, 8, 0, 0, 0, time.UTC)
	for _, user := range users {
		items := nonUIDEpisodeEntitlements(t, user, "same-order", now)
		// 调用方预填的主键不能覆盖当前物理表的数据库发号。
		items[0].ID = "7"
		if err := f.store.GrantEntitlements(user.ctx, user.id, items); err != nil {
			t.Fatalf("授予分片权益失败: %v", err)
		}
		if items[0].ID != "9007199254740993" {
			t.Fatalf("物理表未独立回填大整数权益主键: %q", items[0].ID)
		}
		var state entitlementStateModel
		if err := f.store.data.DB().Table(userTable("user_entitlement_state", user.bucket)).First(&state, "visitor_id = ?", user.id).Error; err != nil {
			t.Fatalf("读取鉴权投影失败: %v", err)
		}
		if state.ID != "9007199254740993" || state.GrantID != items[0].ID || state.BucketID != user.bucket {
			t.Fatalf("投影主键或 grant_id 回填错误: id=%q grant_id=%q bucket=%d", state.ID, state.GrantID, state.BucketID)
		}
		items[0].ID = ""
		if err := f.store.GrantEntitlements(user.ctx, user.id, items); err != nil || items[0].ID != state.GrantID {
			t.Fatalf("来源重放没有恢复原权益 ID: id=%q err=%v", items[0].ID, err)
		}
		if f.count(t, userTable("entitlement_grants", user.bucket), "visitor_id = ?", user.id) != 1 ||
			f.count(t, userTable("user_entitlement_state", user.bucket), "visitor_id = ?", user.id) != 1 {
			t.Fatal("来源重放产生重复流水或投影")
		}
	}

	first := users[0]
	rollback := nonUIDEpisodeEntitlements(t, first, "rolled-back-order", now)
	invalid := rollback[0]
	invalid.UserID = "0"
	rollback = append(rollback, invalid)
	if err := f.store.GrantEntitlements(first.ctx, first.id, rollback); !errors.Is(err, operations.ErrInvalidInput) || rollback[0].ID != "" {
		t.Fatalf("失败事务泄漏权益主键或错误语义改变: id=%q err=%v", rollback[0].ID, err)
	}
	if f.count(t, userTable("entitlement_grants", first.bucket), "source_id = ?", "rolled-back-order") != 0 ||
		f.count(t, userTable("user_entitlement_state", first.bucket), "visitor_id = ?", first.id) != 1 {
		t.Fatal("失败事务留下权益或投影")
	}

	for range 2 {
		if err := f.store.RevokeEntitlementSource(first.ctx, first.id, "order", "same-order", now.Add(time.Minute)); err != nil {
			t.Fatalf("撤销权益失败: %v", err)
		}
	}
	var revoked entitlementRevocationModel
	if err := f.store.data.DB().Table(userTable("entitlement_revocations", first.bucket)).First(&revoked, "visitor_id = ?", first.id).Error; err != nil {
		t.Fatalf("读取撤销流水失败: %v", err)
	}
	if revoked.ID != "9007199254740993" || f.count(t, userTable("entitlement_revocations", first.bucket), "visitor_id = ?", first.id) != 1 {
		t.Fatal("撤销流水未使用物理表自增主键或重复撤销未幂等")
	}
	if f.count(t, userTable("entitlement_grants", first.bucket), "visitor_id = ?", first.id) != 1 ||
		f.count(t, userTable("user_entitlement_state", first.bucket), "visitor_id = ?", first.id) != 0 {
		t.Fatal("撤销必须保留授予流水并删除当前投影")
	}
	replay := nonUIDEpisodeEntitlements(t, first, "same-order", now)
	if err := f.store.GrantEntitlements(first.ctx, first.id, replay); err != nil || replay[0].ID != "9007199254740993" {
		t.Fatalf("撤销后重放未恢复原流水: id=%q err=%v", replay[0].ID, err)
	}
	for index, user := range users {
		allowed, err := f.store.HasEntitlement(user.ctx, user.id, "201", "302", now.Add(2*time.Minute))
		if err != nil || allowed != (index == 1) {
			t.Fatalf("撤销后重放复活权益或串分片: shard=%d allowed=%t err=%v", index, allowed, err)
		}
	}

	// 新来源即使携带已经占用的主键，也必须独立落账，不能覆盖或误命中旧来源。
	collision := nonUIDEpisodeEntitlements(t, first, "new-order-with-existing-id", now)
	collision[0].ID = "9007199254740993"
	if err := f.store.GrantEntitlements(first.ctx, first.id, collision); err != nil {
		t.Fatalf("携带已占用主键的新来源落账失败: %v", err)
	}
	if collision[0].ID == "" || collision[0].ID == "9007199254740993" {
		t.Fatalf("新来源沿用了调用方预填的旧主键: %q", collision[0].ID)
	}
	var original entitlementGrantModel
	if err := f.store.data.DB().Table(userTable("entitlement_grants", first.bucket)).First(&original, "id = ?", "9007199254740993").Error; err != nil {
		t.Fatalf("读取原来源流水失败: %v", err)
	}
	if original.SourceID != "same-order" || f.count(t, userTable("entitlement_grants", first.bucket), "visitor_id = ?", first.id) != 2 {
		t.Fatal("调用方预填主键覆盖了原来源或新来源没有独立保存")
	}
}

// TestNonUIDRewardAndVIPAutoIncrementIntegration 验证奖励与 VIP 进度自增、重放及手动领取后的权益投影。
func TestNonUIDRewardAndVIPAutoIncrementIntegration(t *testing.T) {
	f := newIAAFixture(t)
	user := nonUIDAutoIncrementUsers(t, f, 7)[0]
	now := time.Date(2026, time.September, 23, 8, 0, 0, 0, time.UTC)
	period := monetization.RewardPeriodFor(now, time.UTC)
	episode := monetization.RewardGrant{
		ClaimID: "non-uid-episode", ApplicationID: "101", VisitorID: user.id,
		DramaID: "201", EpisodeID: "302", UnlockEpisodeIDs: []string{"302"}, AdUnitID: "fixture-reward",
		Scene: monetization.AdSceneEpisodeUnlock, RewardType: monetization.RewardTypeEpisodeUnlock,
		PeriodKey: period.Key, PeriodStartsAt: period.StartsAt, PeriodExpiresAt: period.ExpiresAt,
		EntitlementScope: monetization.EntitlementScopeEpisode, GrantedAt: now, ProgressKey: "episode-rule",
	}
	for index := range 2 {
		result, err := f.store.GrantReward(user.ctx, episode, now)
		want := monetization.ClaimStatusGranted
		if index == 1 {
			want = monetization.ClaimStatusAlreadyGranted
		}
		if err != nil || result.Status != want {
			t.Fatalf("逐集奖励结算或重放失败: status=%q err=%v", result.Status, err)
		}
	}
	var reward rewardGrantModel
	if err := f.store.data.DB().Table(userTable("reward_grants", user.bucket)).First(&reward, "claim_id = ?", episode.ClaimID).Error; err != nil {
		t.Fatalf("读取逐集奖励流水失败: %v", err)
	}
	if reward.ID != "9007199254740993" || f.count(t, userTable("reward_grants", user.bucket), "visitor_id = ?", user.id) != 1 {
		t.Fatal("逐集奖励没有使用自增大整数主键或重复领取产生新流水")
	}
	saved, found, err := f.store.FindRewardGrant(user.ctx, user.id, episode.ClaimID)
	if err != nil || !found || len(saved.Entitlements) != 1 || saved.Entitlements[0].ID != "9007199254740993" {
		t.Fatalf("奖励快照丢失已落账权益字符串主键: found=%t count=%d err=%v", found, len(saved.Entitlements), err)
	}

	vip := monetization.RewardGrant{
		ClaimID: "non-uid-vip-first", ApplicationID: "101", VisitorID: user.id, AdUnitID: "fixture-reward",
		Scene: monetization.AdSceneVIPReward, RewardType: monetization.RewardTypeVIPProgress,
		PeriodKey: period.Key, PeriodStartsAt: period.StartsAt, PeriodExpiresAt: period.ExpiresAt,
		RequiredCount: 2, EntitlementScope: monetization.EntitlementScopeCatalog,
		GrantedAt: now.Add(time.Minute), ProgressKey: "vip-rule",
	}
	result, err := f.store.GrantReward(user.ctx, vip, vip.GrantedAt)
	if err != nil || result.VIPProgress == nil || result.VIPProgress.AdCount != 1 || result.VIPProgress.Active {
		t.Fatalf("首次 VIP 广告没有仅累计一次进度: err=%v", err)
	}
	var progress vipProgressModel
	if err := f.store.data.DB().Table(userTable("application_vip_progress", user.bucket)).First(&progress, "visitor_id = ?", user.id).Error; err != nil {
		t.Fatalf("读取 VIP 进度失败: %v", err)
	}
	if progress.ID != "9007199254740993" || progress.Completed != 1 {
		t.Fatalf("VIP 进度未自增或计数异常: id=%q completed=%d", progress.ID, progress.Completed)
	}
	vip.ClaimID, vip.GrantedAt = "non-uid-vip-second", now.Add(2*time.Minute)
	result, err = f.store.GrantReward(user.ctx, vip, vip.GrantedAt)
	if err != nil || result.VIPProgress == nil || result.VIPProgress.AdCount != 2 || result.VIPProgress.Active {
		t.Fatalf("第二次 VIP 广告应等待手动领取: err=%v", err)
	}
	result, err = f.store.GrantReward(user.ctx, vip, vip.GrantedAt)
	if err != nil || result.Status != monetization.ClaimStatusAlreadyGranted {
		t.Fatalf("VIP 奖励重放未恢复成功结果: status=%q err=%v", result.Status, err)
	}
	if err := f.store.data.DB().Table(userTable("application_vip_progress", user.bucket)).First(&progress, "visitor_id = ?", user.id).Error; err != nil {
		t.Fatalf("重读 VIP 进度失败: %v", err)
	}
	if progress.ID != "9007199254740993" || progress.Completed != 2 ||
		f.count(t, userTable("application_vip_progress", user.bucket), "visitor_id = ?", user.id) != 1 ||
		f.count(t, userTable("reward_grants", user.bucket), "visitor_id = ?", user.id) != 3 {
		t.Fatal("VIP 更新改变已有主键、重复累计或产生重复流水")
	}
	saved, found, err = f.store.FindRewardGrant(user.ctx, user.id, vip.ClaimID)
	if err != nil || !found || len(saved.Entitlements) != 0 {
		t.Fatalf("VIP 广告流水不应提前携带权益快照: found=%t count=%d err=%v", found, len(saved.Entitlements), err)
	}
	policy := monetization.MonetizationPolicy{ApplicationID: "101", Status: monetization.MonetizationEnabled,
		VIP: monetization.ApplicationVIPPolicy{Enabled: true, RequiredAdCount: 2}, ProgressNamespace: "vip-rule"}
	claimed, err := f.store.ReconcileVIPProgress(user.ctx, user.id, policy, period, vip.GrantedAt)
	if err != nil || !claimed.Active {
		t.Fatalf("达标后手动领取 VIP 失败: state=%+v err=%v", claimed, err)
	}
	var state entitlementStateModel
	if err := f.store.data.DB().Table(userTable("user_entitlement_state", user.bucket)).First(&state, "visitor_id = ? AND target_key = ?", user.id, "application").Error; err != nil {
		t.Fatalf("读取 VIP 鉴权投影失败: %v", err)
	}
	if state.ID != "9007199254740994" || state.GrantID != "9007199254740994" {
		t.Fatalf("VIP 投影没有关联实际权益主键: id=%q grant_id=%q", state.ID, state.GrantID)
	}
}
