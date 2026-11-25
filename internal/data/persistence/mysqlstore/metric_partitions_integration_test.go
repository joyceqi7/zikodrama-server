package mysqlstore

import (
	"context"
	"encoding/base64"
	"maps"
	"strings"
	"sync"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"drama-server/internal/biz/sharding"
)

// TestMetricDatePartitionsIntegration 验证跨日幂等、跨月关联、真实月表读取及过期整表清理。
func TestMetricDatePartitionsIntegration(t *testing.T) {
	f := newIAAFixture(t)
	now := time.Now().UTC()
	today := now.Truncate(24 * time.Hour)
	month := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
	before := month.Add(-time.Hour)
	ctx := sharding.WithBucketID(application.WithContext(context.Background(), "101"), 0)
	events := []domain.MetricEvent{
		{ID: "cross-month-event-1234", Kind: "play_start", OccurredAt: before},
		{ID: "cross-month-event-1234", Kind: "play_complete", OccurredAt: month},
		{ID: "retry-date-event-12345", Kind: "launch", OccurredAt: today.Add(-time.Hour)},
	}
	if err := f.store.RecordMetrics(ctx, "777", events); err != nil {
		t.Fatal(err)
	}
	// 同一键改变日期重发仍不能制造新的事实。
	if err := f.store.RecordMetrics(ctx, "777", []domain.MetricEvent{{ID: "retry-date-event-12345", Kind: "launch", OccurredAt: now}}); err != nil {
		t.Fatal(err)
	}

	// 并发重发只接受一条事实；改变日期也不能绕过去重。
	var retries sync.WaitGroup
	errors := make(chan error, 4)
	for i := 0; i < 4; i++ {
		retries.Add(1)
		go func() {
			defer retries.Done()
			errors <- f.store.RecordMetrics(ctx, "777", []domain.MetricEvent{{ID: "retry-date-event-12345", Kind: "launch", OccurredAt: now}})
		}()
	}
	retries.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	// 批量后半段落库失败时，前半段事件和去重键也必须回滚。
	err := f.store.RecordMetrics(ctx, "777", []domain.MetricEvent{
		{ID: "atomic-a-valid-event-1234", Kind: "launch", OccurredAt: now},
		{ID: "atomic-z-invalid-event-1234", Kind: strings.Repeat("x", 40), OccurredAt: now},
	})
	if err == nil {
		t.Fatal("畸形内部事件应触发落库错误")
	}
	var rolledBack int64
	if err := f.store.data.DB().Model(&metricEventKey{}).Where("event_id LIKE ?", "atomic-%").Count(&rolledBack).Error; err != nil || rolledBack != 0 {
		t.Fatalf("事务未整体回滚: %d %v", rolledBack, err)
	}
	if err := f.store.RecordMetrics(ctx, "777", []domain.MetricEvent{{ID: "too-old-metric-123456", Kind: "launch", OccurredAt: metricCutoff(now).Add(-time.Second)}}); err == nil {
		t.Fatal("不能重建已过期的日表")
	}
	if err := f.store.MaintainMetrics(ctx, now); err != nil {
		t.Fatal(err)
	}
	rows, err := f.store.readDashboardSnapshots(ctx, month.AddDate(0, 0, -1), today.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	starts, complete, launches := int64(0), int64(0), int64(0)
	for _, row := range rows {
		starts += row.PlayStarts
		complete += row.PlayCompletions
		launches += row.Launches
		if row.PlayCompletions > 0 && row.Date != before.Format(time.DateOnly) {
			t.Fatal("完播未归属开始月")
		}
	}
	if starts != 1 || complete != 1 || launches != 1 {
		t.Fatalf("跨日跨月统计错误: starts=%d complete=%d launches=%d", starts, complete, launches)
	}
	audience, err := f.store.DashboardAudience(ctx, today.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	if audience.MonthlyActiveUsers == nil || *audience.MonthlyActiveUsers != 1 {
		t.Fatalf("跨日同一用户未去重: %+v", audience)
	}
	// 未来维护清除明细，但早先生成的月表和MAU快照仍保留。
	future := today.AddDate(0, 0, 91)
	if err := f.store.MaintainMetrics(ctx, future); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := f.store.data.DB().Raw("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name=?", metricDayTable(before)).Scan(&count).Error; err != nil || count != 0 {
		t.Fatalf("旧日表未删除: %d %v", count, err)
	}
	if err := f.store.data.DB().Model(&metricEventKey{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("旧去重键未删除: %d %v", count, err)
	}
	persisted, err := f.store.DashboardAudience(ctx, today.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	if persisted.MonthlyActiveUsers == nil || *persisted.MonthlyActiveUsers != 1 || persisted.FirstReceivedAt == nil {
		t.Fatalf("清理破坏历史汇总: %+v", persisted)
	}
}

// TestTikTokPostbackLogsIntegration 验证同一事实的发起与终态合并为一行，状态筛选针对合并结果。
func TestTikTokPostbackLogsIntegration(t *testing.T) {
	f := newIAAFixture(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	ctx := sharding.WithBucketID(application.WithContext(context.Background(), "101"), 0)
	events := []domain.MetricEvent{
		{ID: "postback-success-1234", Kind: "tt_ad_show_attempt", OccurredAt: now.Add(-3 * time.Minute)},
		{ID: "postback-success-1234", Kind: "tt_ad_show_success", OccurredAt: now.Add(-2 * time.Minute)},
		{ID: "postback-pending-1234", Kind: "tt_ad_show_attempt", OccurredAt: now.Add(-time.Minute)},
		{ID: "postback-failure-1234", Kind: "tt_ad_show_attempt", OccurredAt: now.Add(-time.Minute)},
		{ID: "postback-failure-1234", Kind: "tt_ad_show_failure", OccurredAt: now},
	}
	if err := f.store.RecordMetrics(ctx, "777", events); err != nil {
		t.Fatal(err)
	}

	page, err := f.store.ListTikTokPostbackLogs(ctx, operations.TikTokPostbackLogQuery{PageSize: 10, Event: "minis_ad_show"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 {
		t.Fatalf("同一回传事实应合并为一行，实际 %+v", page.Items)
	}
	outcomes := map[string]string{}
	for _, item := range page.Items {
		outcomes[item.EventID] = item.Outcome
	}
	want := map[string]string{
		"postback-success-1234": "success",
		"postback-pending-1234": "attempt",
		"postback-failure-1234": "failure",
	}
	if !maps.Equal(outcomes, want) {
		t.Fatalf("回传事实终态错误：got=%v want=%v", outcomes, want)
	}

	for outcome, eventID := range map[string]string{
		"success": "postback-success-1234",
		"attempt": "postback-pending-1234",
		"failure": "postback-failure-1234",
	} {
		filtered, filterErr := f.store.ListTikTokPostbackLogs(ctx, operations.TikTokPostbackLogQuery{PageSize: 10, Event: "minis_ad_show", Outcome: outcome})
		if filterErr != nil {
			t.Fatal(filterErr)
		}
		if len(filtered.Items) != 1 || filtered.Items[0].EventID != eventID || filtered.Items[0].Outcome != outcome {
			t.Fatalf("状态 %s 应只返回合并后的事实 %s，实际 %+v", outcome, eventID, filtered.Items)
		}
	}

	seen := map[string]bool{}
	pageQuery := operations.TikTokPostbackLogQuery{PageSize: 1, Event: "minis_ad_show"}
	for {
		paged, pageErr := f.store.ListTikTokPostbackLogs(ctx, pageQuery)
		if pageErr != nil {
			t.Fatal(pageErr)
		}
		if len(paged.Items) != 1 || seen[paged.Items[0].EventID] {
			t.Fatalf("游标分页不应遗漏或重复合并事实：%+v", paged.Items)
		}
		seen[paged.Items[0].EventID] = true
		if !paged.HasMore {
			break
		}
		decoded, decodeErr := base64.RawURLEncoding.DecodeString(paged.NextCursor)
		parts := strings.Split(string(decoded), "|")
		if decodeErr != nil || len(parts) != 7 {
			t.Fatalf("无效回传日志游标：%q %v", paged.NextCursor, decodeErr)
		}
		boundary, parseErr := time.Parse(time.RFC3339Nano, parts[3])
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		pageQuery.BeforeReceivedAt = boundary
		pageQuery.BeforeUserID = parts[4]
		pageQuery.BeforeEventID = parts[5]
		pageQuery.BeforeKind = parts[6]
	}
	if len(seen) != 3 {
		t.Fatalf("游标分页应遍历三个合并事实，实际 %v", seen)
	}
}
