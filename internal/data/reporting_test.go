package data

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/operations"
	"drama-server/internal/data/persistence/mysqlstore"
)

// newReportingMockStore 为业务池与统计池分别建立 SQL 连接，错误路由会直接触发未预期 SQL。
func newReportingMockStore(t *testing.T) (*mysqlstore.Store, sqlmock.Sqlmock) {
	t.Helper()
	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	orm, err := gorm.Open(mysql.New(mysql.Config{Conn: database, SkipInitializeWithVersion: true}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	data, err := mysqlstore.NewData(orm)
	if err != nil {
		t.Fatal(err)
	}
	return mysqlstore.NewStore(data), mock
}

// TestReportingQueriesUseIndependentPool 覆盖五个页面及其排行、趋势、分集明细，统计故障不能回退到业务池。
func TestReportingQueriesUseIndependentPool(t *testing.T) {
	primary, primarySQL := newReportingMockStore(t)
	reports, reportSQL := newReportingMockStore(t)
	ports := NewOperationsRepositories(&Resources{store: primary, reportingStore: reports})
	service := operations.NewReportingService(ports.Reporting.Metrics, ports.Reporting.Audits)
	ctx := application.WithContext(context.Background(), "101")
	from := time.Now().UTC().Truncate(24 * time.Hour)
	to := from.AddDate(0, 0, 1)
	failure := errors.New("isolated reporting pool unavailable")
	for _, tc := range []struct {
		name        string
		transaction bool
		call        func() error
	}{
		{"dashboard", false, func() error { _, err := service.Dashboard(ctx, from, to); return err }},
		{"dashboard-audience", false, func() error { _, err := ports.Reporting.Metrics.DashboardAudience(ctx, to); return err }},
		{"dashboard-ranking", false, func() error { _, err := ports.Reporting.Metrics.CampaignRewardRanking(ctx, from, to); return err }},
		{"ad-analytics", true, func() error {
			_, err := ports.Reporting.Ads.ReadAdAnalytics(ctx, operations.AdAnalyticsQuery{})
			return err
		}},
		{"postback-logs", false, func() error {
			_, err := ports.Reporting.Postbacks.ListTikTokPostbackLogs(ctx, operations.TikTokPostbackLogQuery{PageSize: 20})
			return err
		}},
		{"drama-report", true, func() error {
			_, err := service.DramaReport(ctx, operations.DramaReportQuery{From: from, To: to})
			return err
		}},
		{"campaign-report", true, func() error {
			_, err := service.CampaignReport(ctx, operations.CampaignReportQuery{From: from, To: to})
			return err
		}},
		{"reward-analysis", true, func() error { _, err := service.RewardAnalysis(ctx, from, to); return err }},
		{"episode-detail", false, func() error { _, err := service.RewardEpisodes(ctx, "201", from, to); return err }},
		{"reward-trend", true, func() error { _, err := service.RewardTrend(ctx, "campaign", "301", from, to); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.transaction {
				reportSQL.ExpectBegin().WillReturnError(failure)
			} else {
				reportSQL.ExpectQuery("SELECT").WillReturnError(failure)
			}
			if err := tc.call(); !errors.Is(err, failure) {
				t.Fatalf("查询未使用独立统计池或发生了业务池降级：%v", err)
			}
			if err := reportSQL.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
	// 统计池不可用之后业务查询仍能成功；不会因为统计错误关闭或替换原连接池。
	primarySQL.ExpectBegin()
	primarySQL.ExpectQuery("SELECT .* FROM `applications`").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	primarySQL.ExpectCommit()
	if apps, err := ports.Application.Registry.ListApplications(ctx); err != nil || len(apps) != 0 {
		t.Fatal("统计故障影响了原连接上的业务查询", err)
	}
	if err := primarySQL.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestReportingMaintenanceAndAuditKeepPrimaryPool 验证读端口切换不会迁移原 Worker 清理及管理员审计写入。
func TestReportingMaintenanceAndAuditKeepPrimaryPool(t *testing.T) {
	primary, primarySQL := newReportingMockStore(t)
	reports, reportSQL := newReportingMockStore(t)
	ports := NewOperationsRepositories(&Resources{store: primary, reportingStore: reports})
	service := operations.NewReportingService(ports.Reporting.Metrics, ports.Reporting.Audits)
	failure := errors.New("primary-only write failure")
	primarySQL.ExpectBegin()
	primarySQL.ExpectExec("DELETE FROM `metric_postback_details`").WillReturnError(failure)
	primarySQL.ExpectRollback()
	if err := service.MaintainMetrics(context.Background(), time.Now().UTC()); !errors.Is(err, failure) {
		t.Fatalf("后台维护错误地切换了数据库连接：%v", err)
	}
	primarySQL.ExpectBegin().WillReturnError(failure)
	if err := service.RecordAudit(context.Background(), operations.AuditRecord{RequestID: "fixture", ActorID: "1", Action: "POST /dramas"}); !errors.Is(err, failure) {
		t.Fatalf("审计写入错误地切换了数据库连接：%v", err)
	}
	if err := primarySQL.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	if err := reportSQL.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
