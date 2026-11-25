package mysqlstore

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/sharding"
)

// TestServerTimeAdShowRetryIntegration 验证真实 MySQL 并发跨日重试保留首次时间、旧记录及不可变内容。
func TestServerTimeAdShowRetryIntegration(t *testing.T) {
	f := newIAAFixtureWithSchema(t, string(readDeploymentSchema(t)), false)
	if err := f.store.data.DB().Exec("INSERT INTO users_07 (bucket_id,id,application_id) VALUES (7,107,101)").Error; err != nil {
		t.Fatal(err)
	}
	ctx := sharding.WithBucketID(application.WithTimeZone(context.Background(), "101", "Asia/Shanghai"), 7)
	before := time.Date(2026, 10, 23, 15, 59, 59, 0, time.UTC)
	show := monetization.AdShow{EventID: "server-time-original", Scene: "home_entry", AdUnitID: "original-ad", OccurredAt: before}
	if err := f.store.RecordAdShow(ctx, "107", show); err != nil {
		t.Fatal(err)
	}
	var pending sync.WaitGroup
	errorsFound := make(chan error, 12)
	for index := range 12 {
		pending.Add(1)
		go func() {
			defer pending.Done()
			retry := show
			retry.OccurredAt = before.Add(time.Duration(index+1) * 24 * time.Hour)
			errorsFound <- f.store.RecordAdShow(ctx, "107", retry)
		}()
	}
	pending.Wait()
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Fatal(err)
		}
	}
	saved, found, err := f.store.FindAdShow(ctx, "107", show.EventID)
	if err != nil || !found || !saved.OccurredAt.Equal(before) {
		t.Fatalf("并发重试覆盖原记录时间: %+v %v", saved, err)
	}
	for _, change := range []func(*monetization.AdShow){
		func(in *monetization.AdShow) { in.AdUnitID = "replacement" },
		func(in *monetization.AdShow) { in.Scene = "entry_reward" },
		func(in *monetization.AdShow) { in.DramaID = "201" },
		func(in *monetization.AdShow) { in.DramaID, in.EpisodeID = "201", "301" },
	} {
		conflict := show
		change(&conflict)
		if err := f.store.RecordAdShow(ctx, "107", conflict); !errors.Is(err, monetization.ErrInvalidAdShow) {
			t.Fatalf("同键替换内容未拒绝: %v", err)
		}
	}
	period := monetization.RewardPeriodFor(before, application.LocationFromContext(ctx))
	count, err := f.store.CountAdShows(ctx, "107", monetization.AdShowQuery{Scene: "home_entry", From: period.StartsAt, Until: period.ExpiresAt})
	if err != nil || count != 1 {
		t.Fatalf("原日展示重复计数: %d %v", count, err)
	}
	count, err = f.store.CountAdShows(ctx, "107", monetization.AdShowQuery{Scene: "home_entry", From: period.ExpiresAt, Until: period.ExpiresAt.AddDate(0, 0, 15)})
	if err != nil || count != 0 {
		t.Fatalf("重试错误占用后续日期: %d %v", count, err)
	}
}
