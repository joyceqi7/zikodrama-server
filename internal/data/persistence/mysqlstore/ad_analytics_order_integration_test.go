package mysqlstore

import (
	"context"
	"reflect"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"drama-server/internal/biz/sharding"
)

// TestAdAnalyticsEventOrderIntegration 复现同批明细按名称乱序，并核对补报、缺失耗时及统计不变。
func TestAdAnalyticsEventOrderIntegration(t *testing.T) {
	f := newIAAFixture(t)
	ctx := sharding.WithBucketID(application.WithContext(context.Background(), "101"), 0)
	now := time.Now().UTC().Truncate(time.Microsecond)
	sequence := []string{"ad_trigger", "ad_create", "ad_created", "ad_request", "ad_show_unknown", "ad_shown", "ad_close_unknown", "ad_closed", "ad_cancelled"}
	elapsed := map[string]float64{"ad_trigger": 0, "ad_skipped": 0, "ad_create": 0, "ad_created": 1, "ad_request": 1,
		"ad_show_unknown": 200, "ad_shown": 572, "ad_close_unknown": 3000, "ad_closed": 3607, "ad_complete": 3607, "ad_cancelled": 3607, "ad_failure": 0}
	stages := map[string]string{"ad_trigger": "gate", "ad_skipped": "capability", "ad_create": "create", "ad_created": "create", "ad_request": "show",
		"ad_show_unknown": "show", "ad_shown": "show", "ad_close_unknown": "close", "ad_closed": "close", "ad_complete": "close", "ad_cancelled": "close", "ad_failure": "create"}
	completed := append([]string(nil), sequence...)
	completed[len(completed)-1] = "ad_complete"
	cases := []struct {
		name     string
		kinds    []string
		timing   string
		reversed bool
	}{
		{name: "batched", kinds: sequence, timing: "complete"},
		{name: "replayed", kinds: completed, timing: "complete", reversed: true},
		{name: "legacy", kinds: sequence, timing: "missing"},
		{name: "partial", kinds: sequence, timing: "partial"},
		{name: "create-failed", kinds: []string{"ad_trigger", "ad_create", "ad_failure"}, timing: "complete"},
		{name: "skipped", kinds: []string{"ad_trigger", "ad_skipped"}, timing: "complete"},
	}
	want := map[string][]string{}
	received := map[string]map[string]time.Time{}
	for _, test := range cases {
		id := "ordered-ad-call-" + test.name
		want[id], received[id] = test.kinds, map[string]time.Time{}
		var batch []domain.MetricEvent
		// 特意反向提交；存储层本身还会按 kind 排序，不能依赖写入顺序碰巧正确。
		for i := len(test.kinds) - 1; i >= 0; i-- {
			kind := test.kinds[i]
			detail := &domain.AdMetricDetails{AdType: "rewarded", AdUnitID: "slot-1", Scene: "entry_reward", ClientVersion: "build-order",
				OS: "ios", TikTokVersion: "46.1.0", Stage: stages[kind]}
			if test.timing != "missing" && !(test.timing == "partial" && kind == "ad_created") {
				ms := elapsed[kind]
				detail.ElapsedMS = &ms
			}
			if kind == "ad_shown" {
				ms := 571.0
				detail.ShowElapsedMS, detail.Evidence = &ms, "show_promise"
			}
			batch = append(batch, domain.MetricEvent{ID: id, Kind: kind, OccurredAt: now, Ad: detail})
		}
		if err := f.store.RecordMetrics(ctx, "9007199254740993", batch); err != nil {
			t.Fatal(err)
		}
		for i, kind := range test.kinds {
			at := now
			if test.reversed {
				at = at.Add(time.Duration(len(test.kinds)-i) * time.Millisecond)
			}
			received[id][kind] = at
			if err := f.store.data.DB().Model(&metricAdEventModel{}).
				Where("application_id=? AND user_id=? AND event_id=? AND kind=?", "101", "9007199254740993", id, kind).Update("received_at", at).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	query := operations.AdAnalyticsQuery{From: now.Format(time.DateOnly), To: now.Format(time.DateOnly), PageSize: 20}
	result, err := f.store.ReadAdAnalytics(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != len(cases) {
		t.Fatalf("阶段排序改变了调用分组: %+v", result.Items)
	}
	for _, item := range result.Items {
		var kinds []string
		for _, event := range item.Events {
			kinds = append(kinds, event.Kind)
			if !event.ReceivedAt.Equal(received[item.EventID][event.Kind]) {
				t.Fatal("排序改写了原始接收时间")
			}
		}
		if !reflect.DeepEqual(kinds, want[item.EventID]) {
			t.Fatalf("%s 阶段顺序错误: got=%v want=%v", item.EventID, kinds, want[item.EventID])
		}
	}
	summary := result.Summary
	if summary.Calls != 6 || summary.Requests != 4 || summary.Shown != 4 || summary.MatchedShown != 4 || summary.Completed != 1 || summary.Cancelled != 3 ||
		summary.CreateFailures != 1 || summary.Skipped != 1 || summary.Unknown != 0 || summary.CloseUnknown != 0 || summary.TimingSamples != 4 ||
		summary.P50MS == nil || *summary.P50MS != 571 || summary.P95MS == nil || *summary.P95MS != 571 {
		t.Fatalf("阶段排序改变了漏斗或耗时统计: %+v", summary)
	}
}
