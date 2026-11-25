package mysql

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/plugin/opentelemetry/tracing"
)

func TestMySQLSQLLogRedactsLiteralsAndComments(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		want string
	}{
		{"绑定参数", "SELECT * FROM users WHERE email = ?", "SELECT * FROM users WHERE email = ?"},
		{"Dashboard 日期格式", "SELECT DATE_FORMAT(created_at, '%Y-%m-%d') AS day FROM users WHERE id = 123", "SELECT DATE_FORMAT(created_at, ?) AS day FROM users WHERE id = ?"},
		{"字符串转义与注释", "SELECT 'a\\'secret', \"other\", `user1` FROM users /* secret */ WHERE id=0x12 -- secret\n AND n=2", "SELECT ?, ?, `user1` FROM users   WHERE id=?  \n AND n=?"},
		{"不完整字符串", "SELECT 'secret", ""},
		{"不完整注释", "SELECT 1 /* secret", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := redactMySQLSQL(test.sql); got != test.want {
				t.Fatalf("redactMySQLSQL() = %q，期望 %q", got, test.want)
			}
		})
	}
}

func TestMySQLSQLLogGORMScanAndOtherOperations(t *testing.T) {
	var output bytes.Buffer
	sqlDB := sql.OpenDB(sqlLogTestConnector{})
	t.Cleanup(func() { _ = sqlDB.Close() })
	db, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{
		Logger:                 newMySQLLogger(time.Nanosecond, gormlogger.Info, slog.New(slog.NewTextHandler(&output, nil))),
		DisableAutomaticPing:   true,
		SkipDefaultTransaction: true,
	})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}
	if err := db.Use(tracing.NewPlugin(tracing.WithoutMetrics(), tracing.WithoutQueryVariables())); err != nil {
		t.Fatalf("注册 OTel 回调失败：%v", err)
	}
	if err := registerMySQLSQLCallbacks(db); err != nil {
		t.Fatalf("注册 SQL 日志回调失败：%v", err)
	}

	var dashboard struct{ Day string }
	if err := db.Raw("SELECT DATE_FORMAT(created_at, '%Y-%m-%d') AS day FROM users WHERE email = ?", "secret@example.com").Scan(&dashboard).Error; err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if dashboard.Day != "2026-09-23" {
		t.Fatalf("Scan() day = %q", dashboard.Day)
	}
	assertSQLLog(t, output.String(), "SELECT DATE_FORMAT(created_at, ?) AS day FROM users WHERE email = ?")
	output.Reset()

	var users []struct{ Day string }
	if err := db.Table("users").Where("email = ?", "secret@example.com").Find(&users).Error; err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	assertSQLLog(t, output.String(), "SELECT * FROM `users` WHERE email = ?")
	output.Reset()

	if err := db.Exec("UPDATE users SET email = ? WHERE id = ?", "secret@example.com", 123).Error; err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	assertSQLLog(t, output.String(), "UPDATE users SET email = ? WHERE id = ?")
}

func assertSQLLog(t *testing.T, output, sqlTemplate string) {
	t.Helper()
	if !strings.Contains(output, "mysql slow query") || !strings.Contains(output, "sql=") || !strings.Contains(output, sqlTemplate) {
		t.Fatalf("慢查询日志缺少 SQL 模板 %q：%s", sqlTemplate, output)
	}
	if strings.Contains(output, "secret@example.com") || strings.Contains(output, "%Y-%m-%d") || strings.Contains(output, "id = 123") {
		t.Fatalf("慢查询日志泄露了 SQL 参数或字面量：%s", output)
	}
}

type sqlLogTestConnector struct{}

func (sqlLogTestConnector) Connect(context.Context) (driver.Conn, error) {
	return sqlLogTestConn{}, nil
}
func (sqlLogTestConnector) Driver() driver.Driver { return sqlLogTestDriver{} }

type sqlLogTestDriver struct{}

func (sqlLogTestDriver) Open(string) (driver.Conn, error) { return sqlLogTestConn{}, nil }

type sqlLogTestConn struct{}

func (sqlLogTestConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("不支持 Prepare")
}
func (sqlLogTestConn) Close() error              { return nil }
func (sqlLogTestConn) Begin() (driver.Tx, error) { return nil, errors.New("不支持事务") }
func (sqlLogTestConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return &sqlLogTestRows{}, nil
}
func (sqlLogTestConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return driver.RowsAffected(1), nil
}

type sqlLogTestRows struct{ emitted bool }

func (*sqlLogTestRows) Columns() []string { return []string{"day"} }
func (*sqlLogTestRows) Close() error      { return nil }
func (rows *sqlLogTestRows) Next(values []driver.Value) error {
	if rows.emitted {
		return io.EOF
	}
	rows.emitted = true
	values[0] = "2026-09-23"
	return nil
}
