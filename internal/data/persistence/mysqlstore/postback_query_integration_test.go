package mysqlstore

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/operations"
)

// TestPostbackBoundedQueryIntegration 验证先选事实再按主键回查完整阶段，不丢跨日成功或改变最早日期与分页。
func TestPostbackBoundedQueryIntegration(t *testing.T) {
	f := newIAAFixture(t)
	ctx := application.WithContext(context.Background(), "101")
	day := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -1)
	add := func(app, user, id, kind string, at, received time.Time) {
		t.Helper()
		table := metricDayTable(at)
		if err := f.store.ensureMetricTable(ctx, table, "metric_events_template"); err != nil {
			t.Fatal(err)
		}
		if err := f.store.data.DB().Create(&metricEventKey{ApplicationID: app, UserID: user, EventID: id, Kind: kind, EventDate: at.Format(time.DateOnly)}).Error; err != nil {
			t.Fatal(err)
		}
		if err := f.store.data.DB().Table(table).Create(&metricReceiptModel{ApplicationID: app, UserID: user, EventID: id, Kind: kind, OccurredAt: at, ReceivedAt: received}).Error; err != nil {
			t.Fatal(err)
		}
	}
	add("101", "777", "cross-day-success-1234", "tt_ad_show_attempt", day.Add(time.Hour), day.Add(time.Hour))
	add("101", "777", "cross-day-success-1234", "tt_ad_show_success", day.AddDate(0, 0, 1).Add(time.Minute), day.AddDate(0, 0, 1).Add(time.Minute))
	add("101", "777", "started-before-range-1", "tt_ad_show_attempt", day.AddDate(0, 0, -1), day.AddDate(0, 0, -1))
	add("101", "777", "started-before-range-1", "tt_ad_show_success", day.Add(time.Hour), day.Add(time.Hour))
	add("101", "778", "standalone-success-12", "tt_ad_show_success", day.Add(2*time.Hour), day.Add(2*time.Hour))
	add("102", "777", "other-app-success-123", "tt_ad_show_success", day.Add(2*time.Hour), day.Add(2*time.Hour))
	query := operations.TikTokPostbackLogQuery{PageSize: 1, Event: "minis_ad_show", Outcome: "success", From: day.Format(time.DateOnly), Through: day.Format(time.DateOnly)}
	seen := map[string]bool{}
	for {
		page, err := f.store.ListTikTokPostbackLogs(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 1 || seen[page.Items[0].EventID] {
			t.Fatalf("丢失或重复事实: %+v", page)
		}
		item := page.Items[0]
		if item.EventID == "started-before-range-1" || item.EventID == "other-app-success-123" {
			t.Fatalf("日期或应用筛选错误: %+v", item)
		}
		seen[item.EventID] = true
		if !page.HasMore {
			break
		}
		query.BeforeReceivedAt, query.BeforeUserID, query.BeforeEventID, query.BeforeKind = item.ReceivedAt, item.UserID, item.EventID, "tt_ad_show_"+item.Outcome
	}
	if len(seen) != 2 || !seen["cross-day-success-1234"] {
		t.Fatalf("跨日成功未合并: %v", seen)
	}
	tables, err := f.store.metricTables(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	source, args := postbackEventSource(tables, "101", query)
	var plan string
	if err := f.store.data.DB().Raw("EXPLAIN FORMAT=JSON "+source, args...).Scan(&plan).Error; err != nil {
		t.Fatal(err)
	}
	var tree any
	if err := json.Unmarshal([]byte(plan), &tree); err != nil {
		t.Fatal(err)
	}
	lookups := 0
	var inspect func(any)
	inspect = func(value any) {
		switch node := value.(type) {
		case map[string]any:
			if node["table_name"] == "e" {
				lookups++
				if node["key"] != "PRIMARY" || node["access_type"] == "ALL" {
					t.Fatalf("完整阶段回查未走主键: %+v", node)
				}
			}
			for _, child := range node {
				inspect(child)
			}
		case []any:
			for _, child := range node {
				inspect(child)
			}
		}
	}
	inspect(tree)
	if lookups == 0 || !strings.Contains(source, "postback_phase_keys") {
		t.Fatal("缺少完整阶段主键回查")
	}
}

// TestPostbackBoundedQueryScaleIntegration 在多日多应用事实中对照原整窗查询，记录实际查询耗时但不写死机器性能阈值。
func TestPostbackBoundedQueryScaleIntegration(t *testing.T) {
	f := newIAAFixture(t)
	ctx := application.WithContext(context.Background(), "101")
	today := time.Now().UTC().Truncate(24 * time.Hour)
	for offset := 0; offset < 45; offset++ {
		day := today.AddDate(0, 0, -offset)
		table := metricDayTable(day)
		if err := f.store.ensureMetricTable(ctx, table, "metric_events_template"); err != nil {
			t.Fatal(err)
		}
		var receipts []metricReceiptModel
		var keys []metricEventKey
		for _, app := range []string{"101", "102"} {
			for user := 1001; user <= 4000; user++ {
				for _, kind := range []string{"tt_ad_show_attempt", "tt_ad_show_success"} {
					id := fmt.Sprintf("postback-scale-day-%02d", offset)
					at := day.Add(time.Minute)
					receipts = append(receipts, metricReceiptModel{ApplicationID: app, UserID: fmt.Sprint(user), EventID: id, Kind: kind, OccurredAt: at, ReceivedAt: at})
					keys = append(keys, metricEventKey{ApplicationID: app, UserID: fmt.Sprint(user), EventID: id, Kind: kind, EventDate: day.Format(time.DateOnly)})
				}
			}
		}
		if err := f.store.data.DB().Table(table).CreateInBatches(receipts, 300).Error; err != nil {
			t.Fatal(err)
		}
		if err := f.store.data.DB().CreateInBatches(keys, 300).Error; err != nil {
			t.Fatal(err)
		}
	}
	tables, err := f.store.metricTables(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	query := operations.TikTokPostbackLogQuery{From: today.AddDate(0, 0, -6).Format(time.DateOnly), Through: today.Format(time.DateOnly), Event: "minis_ad_show", PageSize: 20}
	newSource, args := postbackEventSource(tables, "101", query)
	oldSource := metricEventUnion(tables, metricCutoffNow(), metricTomorrow())
	type row struct {
		// UserID 保留内部用户标识精度。
		UserID string
		// EventID 关联一次回传的全部阶段。
		EventID string
		// Outcome 验证合并后的终态一致。
		Outcome string
		// OccurredAt 保留最早发生时间和日期筛选口径。
		OccurredAt time.Time
		// ReceivedAt 保留最早接收时间和分页排序口径。
		ReceivedAt time.Time
	}
	read := func(source string, args []any) ([]row, time.Duration) {
		t.Helper()
		start := time.Now()
		grouped := f.store.data.DB().Table("("+source+") events", args...).Where("application_id=? AND kind IN ?", "101", postbackKinds(query.Event)).
			Select("user_id,event_id,CASE WHEN MAX(kind LIKE '%_success')=1 THEN 'success' ELSE 'attempt' END AS outcome,MIN(occurred_at) AS occurred_at,MIN(received_at) AS received_at").Group("user_id,event_id")
		var rows []row
		if err := f.store.data.DB().Table("(?) facts", grouped).Where("occurred_at>=? AND occurred_at<DATE_ADD(?,INTERVAL 1 DAY)", query.From, query.Through).
			Order("received_at DESC,user_id DESC,event_id DESC").Limit(21).Scan(&rows).Error; err != nil {
			t.Fatal(err)
		}
		return rows, time.Since(start)
	}
	var oldElapsed, newElapsed time.Duration
	for run := 0; run < 3; run++ {
		oldRows, before := read(oldSource, nil)
		newRows, after := read(newSource, args)
		if len(newRows) != 21 || !reflect.DeepEqual(oldRows, newRows) {
			t.Fatal("限日期查询改变原整窗合并或排序结果")
		}
		oldElapsed += before
		newElapsed += after
	}
	t.Logf("45 日日表 / 540000 阶段 / 查询最近7日，三次平均：原整窗=%s 按日期读取并补齐跨日阶段=%s", oldElapsed/3, newElapsed/3)
	// 同一批明细对照仪表盘首次全窗与同日增量，避免只验证列表查询而漏掉汇总开销。
	refreshAt := today.Add(12 * time.Hour)
	started := time.Now()
	if err := f.store.refreshDashboard(ctx, refreshAt); err != nil {
		t.Fatal(err)
	}
	fullElapsed := time.Since(started)
	full, err := f.store.readDashboardSnapshots(ctx, metricCutoff(refreshAt), metricTomorrow())
	if err != nil {
		t.Fatal(err)
	}
	started = time.Now()
	if err := f.store.refreshDashboard(ctx, refreshAt.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	warmElapsed := time.Since(started)
	warm, err := f.store.readDashboardSnapshots(ctx, metricCutoff(refreshAt), metricTomorrow())
	if err != nil || len(warm) != len(full) {
		t.Fatalf("增量汇总快照读取失败: %v", err)
	}
	for i := range full {
		if !reflect.DeepEqual(full[i].DashboardCounters, warm[i].DashboardCounters) || !reflect.DeepEqual(full[i].MonthlyActiveUsers, warm[i].MonthlyActiveUsers) {
			t.Fatalf("增量重算改变原有汇总: full=%+v warm=%+v", full[i], warm[i])
		}
	}
	t.Logf("同一批事实的仪表盘刷新：首次完整=%s 同日增量=%s", fullElapsed, warmElapsed)
}
