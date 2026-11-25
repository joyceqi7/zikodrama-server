package mysqlstore

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// metricTableState 仅抑制本实例重复执行幂等 DDL；状态丢失后可由 MySQL 重建。
type metricTableState struct {
	mu     sync.Mutex
	tables map[string]struct{}
}

// ensureMetricTable 只允许内部生成的合法日期表名，模板必须由部署阶段安装。
func (s *Store) ensureMetricTable(ctx context.Context, table, template string) error {
	valid := false
	if template == "metric_events_template" && metricDayPattern.MatchString(table) {
		_, err := time.Parse("20060102", table[len("metric_events_"):])
		valid = err == nil
	}
	if template == "dashboard_daily_template" && metricMonthPattern.MatchString(table) {
		_, err := time.Parse("200601", table[len("dashboard_daily_"):])
		valid = err == nil
	}
	if !valid {
		return fmt.Errorf("invalid metric partition name")
	}
	// 每个进程对每张合法分表只执行一次幂等 DDL，省去高频 information_schema
	// COUNT 查询；多实例并发仍由 MySQL 的 IF NOT EXISTS 保证一致结果。
	s.metricTableState.mu.Lock()
	defer s.metricTableState.mu.Unlock()
	if _, ok := s.metricTableState.tables[table]; ok {
		return nil
	}
	if err := s.data.DB().WithContext(ctx).Exec("CREATE TABLE IF NOT EXISTS `" + table + "` LIKE `" + template + "`").Error; err != nil {
		return err
	}
	if s.metricTableState.tables == nil {
		s.metricTableState.tables = make(map[string]struct{})
	}
	s.metricTableState.tables[table] = struct{}{}
	return nil
}
