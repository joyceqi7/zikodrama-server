package mysqlstore

import (
	"context"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/sharding"
	"drama-server/internal/pkg/requesttime"
	"gorm.io/gorm"
)

// TestDashboardRevisionRefreshAcrossUTCDaysIntegration 验证跨日跳过未变化事件表，保留账本、MAU、迟到完成和显式重建口径。
func TestDashboardRevisionRefreshAcrossUTCDaysIntegration(t *testing.T) {
	f := newIAAFixture(t)
	ctx := sharding.WithBucketID(application.WithContext(context.Background(), "101"), 0)
	db := f.store.data.DB()
	today := time.Now().UTC().Truncate(24 * time.Hour)
	beforeMidnight, afterMidnight := today.Add(-5*time.Minute), today.Add(5*time.Minute)
	startDay, controlDay := today.AddDate(0, 0, -10), today.AddDate(0, 0, -9)
	initial := []domain.MetricEvent{
		{ID: "revision-old-play", Kind: "play_start", OccurredAt: startDay},
		{ID: "revision-old-ad", Kind: "rewarded_show", OccurredAt: startDay},
		{ID: "revision-control", Kind: "active", OccurredAt: controlDay},
		{ID: "revision-yesterday-launch", Kind: "launch", OccurredAt: beforeMidnight},
	}
	initialCtx := requesttime.WithReceivedAt(ctx, beforeMidnight)
	if err := f.store.RecordMetrics(initialCtx, "70101", initial); err != nil {
		t.Fatal(err)
	}
	if err := f.store.RecordMetrics(initialCtx, "70102", []domain.MetricEvent{
		{ID: "revision-expiring-active", Kind: "active", OccurredAt: today.AddDate(0, 0, -30)},
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.refreshDashboard(ctx, beforeMidnight); err != nil {
		t.Fatal(err)
	}
	readDay := func(at time.Time) dashboardSnapshotModel {
		t.Helper()
		var row dashboardSnapshotModel
		if err := db.Table(metricMonthTable(at)).Where("application_id=? AND date=?", "101", at.Format(time.DateOnly)).Take(&row).Error; err != nil {
			t.Fatal(err)
		}
		return row
	}
	if row := readDay(beforeMidnight); row.MonthlyActiveUsers == nil || *row.MonthlyActiveUsers != 2 {
		t.Fatalf("跨日前 MAU 应包含即将离开三十日窗口的用户: %+v", row)
	}
	// 独立账本补录不产生事件修订；跨日仍须校正历史新增用户数。
	if err := db.Table("users_00").Create(&consumerUserModel{ID: "70103", ApplicationID: "101", BucketID: 0, CreatedAt: controlDay}).Error; err != nil {
		t.Fatal(err)
	}
	var rawReads, controlReads atomic.Int64
	observe := func(tx *gorm.DB) {
		query := tx.Statement.SQL.String()
		if strings.Contains(query, "`metric_events_") {
			rawReads.Add(1)
		}
		if strings.Contains(query, "`"+metricDayTable(controlDay)+"`") {
			controlReads.Add(1)
		}
	}
	const callback = "test:dashboard_revision_raw_reads"
	if err := db.Callback().Query().After("gorm:query").Register(callback, observe); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Callback().Query().Remove(callback); err != nil {
			t.Errorf("移除事件查询观测回调失败: %v", err)
		}
	})
	if err := db.Callback().Row().After("gorm:row").Register(callback, observe); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Callback().Row().Remove(callback); err != nil {
			t.Errorf("移除事件聚合观测回调失败: %v", err)
		}
	})
	if err := f.store.refreshDashboard(ctx, afterMidnight); err != nil {
		t.Fatal(err)
	}
	if rawReads.Load() != 0 {
		t.Fatalf("跨日未变化日期仍读取原始事件 %d 次", rawReads.Load())
	}
	if row := readDay(controlDay); row.NewUsers != 1 || row.ActiveUsers != 1 {
		t.Fatalf("跨日没有校正账本或覆盖了既有行为: %+v", row)
	}
	if row := readDay(today); row.MonthlyActiveUsers == nil || *row.MonthlyActiveUsers != 1 {
		t.Fatalf("跨日没有移动 MAU 窗口: %+v", row)
	}
	late := []domain.MetricEvent{
		{ID: initial[0].ID, Kind: "play_complete", OccurredAt: afterMidnight},
		{ID: initial[1].ID, Kind: "rewarded_complete", OccurredAt: afterMidnight},
	}
	lateCtx := requesttime.WithReceivedAt(ctx, afterMidnight)
	if err := f.store.RecordMetrics(lateCtx, "70101", late); err != nil {
		t.Fatal(err)
	}
	newLaunch := []domain.MetricEvent{{ID: "revision-new-launch", Kind: "launch", OccurredAt: afterMidnight}}
	if err := f.store.RecordMetrics(lateCtx, "70103", newLaunch); err != nil {
		t.Fatal(err)
	}
	rawReads.Store(0)
	controlReads.Store(0)
	if err := f.store.refreshDashboard(ctx, afterMidnight.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if rawReads.Load() == 0 || controlReads.Load() != 0 {
		t.Fatalf("变化日期未重算或无变化历史日被扫描: raw=%d control=%d", rawReads.Load(), controlReads.Load())
	}
	if row := readDay(startDay); row.PlayStarts != 1 || row.PlayCompletions != 1 || row.RewardedShows != 1 || row.AdCompletions != 1 {
		t.Fatalf("迟到播放与广告完成没有归属开始日: %+v", row)
	}
	if row := readDay(today); row.ActiveUsers != 2 || row.Launches != 1 || row.MonthlyActiveUsers == nil || *row.MonthlyActiveUsers != 2 {
		t.Fatalf("新增事实没有维护精确日活与 MAU: %+v", row)
	}
	// 重复事实不改变修订，刚初始化的今日也不能在下一轮重新执行 DISTINCT。
	if err := f.store.RecordMetrics(lateCtx, "70101", late); err != nil {
		t.Fatal(err)
	}
	if err := f.store.RecordMetrics(lateCtx, "70103", newLaunch); err != nil {
		t.Fatal(err)
	}
	rawReads.Store(0)
	if err := f.store.refreshDashboard(ctx, afterMidnight.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if rawReads.Load() != 0 {
		t.Fatalf("今日初始化后或重复事实仍触发原始事件扫描 %d 次", rawReads.Load())
	}
	from, to := metricCutoff(afterMidnight), today.AddDate(0, 0, 1)
	warm, err := f.store.readDashboardSnapshots(ctx, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&metricSummaryState{}).Where("application_id=?", "101").Update("refreshed_at", nil).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.store.refreshDashboard(ctx, afterMidnight.Add(6*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if rawReads.Load() == 0 {
		t.Fatal("显式重建没有回源原始事件")
	}
	cold, err := f.store.readDashboardSnapshots(ctx, from, to)
	if err != nil || len(cold) != len(warm) {
		t.Fatalf("重建快照读取失败: %v", err)
	}
	for i := range cold {
		if !reflect.DeepEqual(cold[i].DashboardCounters, warm[i].DashboardCounters) || !reflect.DeepEqual(cold[i].MonthlyActiveUsers, warm[i].MonthlyActiveUsers) {
			t.Fatalf("增量与显式重建口径不一致: warm=%+v cold=%+v", warm[i], cold[i])
		}
	}
}

// TestDashboardExpiredCompletionRefreshIntegration 验证完成先到、开始后到时，保留窗口移动仍定向校正完成计数。
func TestDashboardExpiredCompletionRefreshIntegration(t *testing.T) {
	f := newIAAFixture(t)
	ctx := sharding.WithBucketID(application.WithContext(context.Background(), "101"), 0)
	today := time.Now().UTC().Truncate(24 * time.Hour)
	beforeMidnight, afterMidnight := today.Add(-5*time.Minute), today.Add(5*time.Minute)
	completionDay := metricCutoff(beforeMidnight)
	startDay := completionDay.AddDate(0, 0, 1)
	if err := f.store.RecordMetrics(requesttime.WithReceivedAt(ctx, beforeMidnight), "70101", []domain.MetricEvent{
		{ID: "expiry-reversed-play", Kind: "play_complete", OccurredAt: completionDay},
		{ID: "expiry-reversed-play", Kind: "play_start", OccurredAt: startDay},
		{ID: "expiry-reversed-ad", Kind: "rewarded_complete", OccurredAt: completionDay},
		{ID: "expiry-reversed-ad", Kind: "rewarded_show", OccurredAt: startDay},
	}); err != nil {
		t.Fatal(err)
	}
	readStartDay := func() dashboardSnapshotModel {
		t.Helper()
		var row dashboardSnapshotModel
		if err := f.store.data.DB().Table(metricMonthTable(startDay)).Where("application_id=? AND date=?", "101", startDay.Format(time.DateOnly)).Take(&row).Error; err != nil {
			t.Fatal(err)
		}
		return row
	}
	if err := f.store.refreshDashboard(ctx, beforeMidnight); err != nil {
		t.Fatal(err)
	}
	if row := readStartDay(); row.PlayCompletions != 1 || row.AdCompletions != 1 {
		t.Fatalf("完成先到时没有匹配开始事实: %+v", row)
	}
	if err := f.store.refreshDashboard(ctx, afterMidnight); err != nil {
		t.Fatal(err)
	}
	if row := readStartDay(); row.PlayStarts != 1 || row.PlayCompletions != 0 || row.RewardedShows != 1 || row.AdCompletions != 0 {
		t.Fatalf("完成事实离开保留窗口后没有校正仍在窗口内的开始日: %+v", row)
	}
}
