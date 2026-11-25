package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"drama-server/internal/biz/sharding"
	"testing"
	"time"
)

// TestReportAdShowsIntegration 验证展示回调幂等、应用隔离、日期边界、无匹配搜索以及原始事件过期提示。
func TestReportAdShowsIntegration(t *testing.T) {
	f := newIAAFixture(t)
	today := time.Now().UTC().Truncate(24 * time.Hour)
	ctx := sharding.WithBucketID(application.WithContext(application.WithSubject(context.Background(), "1"), "101"), 0)
	events := []domain.MetricEvent{
		{ID: "report-ad-show-00001", Kind: "rewarded_show", OccurredAt: today},
		{ID: "report-ad-show-00001", Kind: "rewarded_complete", OccurredAt: today},
		{ID: "report-ad-show-00002", Kind: "interstitial_show", OccurredAt: today.Add(time.Hour)},
		{ID: "report-ad-show-00003", Kind: "tt_ad_show_success", OccurredAt: today},
		{ID: "report-ad-show-00004", Kind: "rewarded_show", OccurredAt: today.Add(-time.Second)},
	}
	for i := 0; i < 2; i++ {
		if err := f.store.RecordMetrics(ctx, "777", events); err != nil {
			t.Fatal(err)
		}
	}
	other := sharding.WithBucketID(application.WithContext(application.WithSubject(context.Background(), "1"), "102"), 0)
	if err := f.store.RecordMetrics(other, "777", events[:1]); err != nil {
		t.Fatal(err)
	}
	all, err := f.store.reportAdShows(application.WithSubject(context.Background(), "1"), today, today.AddDate(0, 0, 1))
	if err != nil || all.Total != 3 || all.Rewarded != 2 || all.Interstitial != 1 || !all.Complete {
		t.Fatalf("全部展示统计错误: %+v %v", all, err)
	}
	single, err := f.store.reportAdShows(ctx, today, today.AddDate(0, 0, 1))
	if err != nil || single.Total != 2 || single.Rewarded != 1 || single.Interstitial != 1 {
		t.Fatalf("应用隔离错误: %+v %v", single, err)
	}
	for _, search := range []string{"", "no-match"} {
		d, err := f.store.DramaReport(ctx, operations.DramaReportQuery{From: today, To: today.AddDate(0, 0, 1), Query: search, Page: 1, PageSize: 1})
		if err != nil || d.AdShows != single {
			t.Fatalf("剧目搜索影响展示汇总: %+v %v", d, err)
		}
		c, err := f.store.CampaignReport(ctx, operations.CampaignReportQuery{From: today, To: today.AddDate(0, 0, 1), Query: search, Page: 2, PageSize: 1})
		if err != nil || c.AdShows != single {
			t.Fatalf("链接搜索或分页影响展示汇总: %+v %v", c, err)
		}
	}
	empty, err := f.store.reportAdShows(application.WithContext(application.WithSubject(context.Background(), "1"), "999"), today, today.AddDate(0, 0, 1))
	if err != nil || empty.Total != 0 {
		t.Fatalf("空应用展示错误: %+v %v", empty, err)
	}
	expired, err := f.store.reportAdShows(ctx, today.AddDate(0, 0, -90), today.AddDate(0, 0, 1))
	if err != nil || expired.Complete || expired.AvailableFrom != metricCutoffNow().Format(time.DateOnly) {
		t.Fatalf("过期范围提示错误: %+v %v", expired, err)
	}
}
