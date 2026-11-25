package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/operations"
	"time"
)

// reportAdShows 复用安全日表发现和事件去重事实，仅按应用、日期汇总展示成功回调。
func (s *Store) reportAdShows(ctx context.Context, from, to time.Time) (operations.ReportAdShows, error) {
	cutoff := metricCutoffNow()
	result := operations.ReportAdShows{AvailableFrom: cutoff.Format(time.DateOnly), Complete: !from.Before(cutoff)}
	if from.Before(cutoff) {
		from = cutoff
	}
	tables, err := s.metricTables(ctx, false)
	if err != nil {
		return result, err
	}
	q := s.data.DB().WithContext(ctx).Table("("+metricEventUnion(tables, from, to)+") m").
		Where("m.occurred_at >= ? AND m.occurred_at < ?", from.UTC(), to.UTC()).
		Where("m.application_id IN (?)", s.data.DB().Table("applications").Select("id").Where("subject_id=?", application.SubjectIDFromContext(ctx)))
	if app := application.IDFromContext(ctx); app != "" {
		q = q.Where("m.application_id = ?", app)
	}
	var counts struct {
		Rewarded     int64
		Interstitial int64
	}
	err = q.Select("COALESCE(SUM(m.kind='rewarded_show'),0) AS rewarded,COALESCE(SUM(m.kind='interstitial_show'),0) AS interstitial").Scan(&counts).Error
	result.Rewarded = counts.Rewarded
	result.Interstitial = counts.Interstitial
	result.Total = result.Rewarded + result.Interstitial
	return result, err
}
