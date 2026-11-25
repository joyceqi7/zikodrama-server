package mysqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
	styxobserve "gitee.com/linpbin/styx/observe"
	styxruntime "gitee.com/linpbin/styx/runtime"
	mysqldriver "github.com/go-sql-driver/mysql"
)

// TestReportingPoolIsolationIntegration 使用隔离 MySQL 验证两个真实连接池互不占用配额、不共享关闭状态。
// 自建临时空库并在结束后删除，不访问现有业务库的表或记录。
func TestReportingPoolIsolationIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("需配置隔离 TEST_MYSQL_DSN")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	databaseConfig, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		t.Fatal("隔离 MySQL DSN 格式错误")
	}
	databaseConfig.DBName = ""
	admin, err := sql.Open("mysql", databaseConfig.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	schema := fmt.Sprintf("drama_reporting_pool_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE `"+schema+"`"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if _, err := admin.ExecContext(cleanupCtx, "DROP DATABASE `"+schema+"`"); err != nil {
			t.Errorf("清理隔离统计连接池测试库失败：%v", err)
		}
	})
	databaseConfig.DBName = schema
	runtime, err := styxruntime.New(styxruntime.Config{Identity: styxruntime.Identity{Name: "reporting-pool-isolation-test"}, Dependencies: styxobserve.NoopDependencies()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Shutdown(context.Background()) })
	config := &configv1.MySQLConfig{Master: databaseConfig.FormatDSN(), MaxOpenConns: 1, MaxIdleConns: 1}
	primary, err := OpenData(ctx, config, runtime)
	if err != nil {
		t.Fatal(err)
	}
	reporting, err := OpenReportingData(ctx, config, runtime)
	if err != nil {
		t.Fatal(err)
	}
	if primary.pool == reporting.pool || primary.pool.Stats().MaxOpenConnections != 1 || reporting.pool.Stats().MaxOpenConnections != 1 {
		t.Fatal("统计与业务没有使用独立底层连接池")
	}
	// 业务池只有一个连接且被占用，统计池仍可独立读取同一数据库。
	held, err := primary.pool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	var primaryDatabase, reportingDatabase string
	if err := held.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&primaryDatabase); err != nil {
		t.Fatal(err)
	}
	if err := reporting.DB().WithContext(ctx).Raw("SELECT DATABASE()").Scan(&reportingDatabase).Error; err != nil {
		t.Fatal(err)
	}
	if reportingDatabase != primaryDatabase {
		t.Fatal("统计与业务连接未访问同一逻辑库")
	}
	waitCtx, cancelWait := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancelWait()
	if err := primary.Ping(waitCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("业务连接池未处于预期的配额占满状态：%v", err)
	}
	if err := reporting.Ping(ctx); err != nil {
		t.Fatal("业务池耗尽阻塞了统计池", err)
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	if err := reporting.pool.Close(); err != nil {
		t.Fatal(err)
	}
	if err := primary.Ping(ctx); err != nil {
		t.Fatal("关闭统计池影响了业务连接", err)
	}
}
