package mysqlstore

import (
	"context"
	"reflect"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/sharding"
)

// TestDashboardIncrementalRefreshIntegration 验证同日增量保留历史账本、回补旧开始日完播，并与完整重建一致。
func TestDashboardIncrementalRefreshIntegration(t *testing.T) {
	f := newIAAFixture(t)
	ctx := sharding.WithBucketID(application.WithContext(context.Background(), "101"), 0)
	now := time.Now().UTC()
	old := now.Truncate(24*time.Hour).AddDate(0, 0, -10)
	control := old.AddDate(0, 0, 1)
	if err := f.store.data.DB().Table("users_00").Create(&consumerUserModel{ID: "70101", ApplicationID: "101", BucketID: 0, CreatedAt: old}).Error; err != nil {
		t.Fatal(err)
	}
	initial := []domain.MetricEvent{
		{ID: "old-play-event-123456", Kind: "play_start", OccurredAt: old.Add(time.Hour)},
		{ID: "old-ad-event-12345678", Kind: "rewarded_show", OccurredAt: old.Add(time.Hour)},
		{ID: "unchanged-event-123456", Kind: "active", OccurredAt: control.Add(time.Hour)},
		{ID: "backdated-play-123456", Kind: "play_start", OccurredAt: old.Add(2 * time.Hour)},
	}
	if err := f.store.RecordMetrics(ctx, "70101", initial); err != nil {
		t.Fatal(err)
	}
	if err := f.store.refreshDashboard(ctx, now); err != nil {
		t.Fatal(err)
	}
	var before dashboardSnapshotModel
	if err := f.store.data.DB().Table(metricMonthTable(control)).Where("application_id=? AND date=?", "101", control.Format(time.DateOnly)).First(&before).Error; err != nil {
		t.Fatal(err)
	}
	late := []domain.MetricEvent{
		{ID: initial[0].ID, Kind: "play_complete", OccurredAt: now},
		{ID: initial[1].ID, Kind: "rewarded_complete", OccurredAt: now},
		{ID: "yesterday-active-12345", Kind: "active", OccurredAt: now.Add(-24 * time.Hour)},
		// 完成事实本身也可以补报到历史日，仍须回算更早的开始日。
		{ID: initial[3].ID, Kind: "play_complete", OccurredAt: old.AddDate(0, 0, 2)},
	}
	if err := f.store.RecordMetrics(ctx, "70101", late); err != nil {
		t.Fatal(err)
	}
	if err := f.store.refreshDashboard(ctx, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	warm, err := f.store.readDashboardSnapshots(ctx, metricCutoff(now), metricTomorrow())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range warm {
		if row.Date == old.Format(time.DateOnly) {
			found = true
			if row.NewUsers != 1 || row.PlayCompletions != 2 || row.AdCompletions != 1 {
				t.Fatalf("历史完播或账本回补错误: %+v", row)
			}
		}
		if row.Date == control.Format(time.DateOnly) && !row.UpdatedAt.Equal(before.UpdatedAt) {
			t.Fatal("无变化历史日期被重复重算")
		}
	}
	if !found {
		t.Fatal("缺少历史快照")
	}
	if err := f.store.data.DB().Model(&metricSummaryState{}).Where("application_id=?", "101").Update("refreshed_at", nil).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.store.refreshDashboard(ctx, now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	cold, err := f.store.readDashboardSnapshots(ctx, metricCutoff(now), metricTomorrow())
	if err != nil || len(cold) != len(warm) {
		t.Fatalf("完整快照失败: %v", err)
	}
	for i := range cold {
		if !reflect.DeepEqual(cold[i].DashboardCounters, warm[i].DashboardCounters) || !reflect.DeepEqual(cold[i].MonthlyActiveUsers, warm[i].MonthlyActiveUsers) {
			t.Fatalf("增量与完整汇总不一致: warm=%+v cold=%+v", warm[i], cold[i])
		}
	}
}

// TestDashboardRefreshWindow 验证账本与 MAU 仍按二十四小时补报窗口及每日全窗校正，不与行为选日耦合。
func TestDashboardRefreshWindow(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	cutoff := metricCutoff(now)
	recent := now.Add(-2 * time.Minute)
	if got := dashboardRefreshFrom(now, &recent, cutoff); !got.Equal(now.Truncate(24*time.Hour).AddDate(0, 0, -1)) {
		t.Fatalf("增量范围=%v", got)
	}
	yesterday := now.AddDate(0, 0, -1)
	for _, last := range []*time.Time{nil, &yesterday} {
		if got := dashboardRefreshFrom(now, last, cutoff); !got.Equal(cutoff) {
			t.Fatalf("应进行全窗校正: %v", got)
		}
	}
}
