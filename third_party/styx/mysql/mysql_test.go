package mysql

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
	"gitee.com/linpbin/styx/observe"
	styxruntime "gitee.com/linpbin/styx/runtime"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	gormlogger "gorm.io/gorm/logger"
)

func TestMySQLSafeTracerHidesDriverErrorDetails(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	tracer := mysqlSafeTracerProvider{TracerProvider: provider}.Tracer("test")
	_, span := tracer.Start(context.Background(), "gorm.Create")
	span.RecordError(errors.New("duplicate entry secret@example.com"))
	span.SetStatus(codes.Error, "duplicate entry secret@example.com")
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].Status.Code != codes.Error {
		t.Fatalf("MySQL 失败 Span 状态错误：%+v", spans)
	}
	serialized := fmt.Sprintf("%+v", spans[0])
	if strings.Contains(serialized, "secret@example.com") || !strings.Contains(serialized, errMySQLDependency.Error()) {
		t.Fatalf("MySQL 失败 Span 未正确脱敏：%s", serialized)
	}
}

func TestMySQLLoggerDoesNotRecordStatementOrDriverError(t *testing.T) {
	var output bytes.Buffer
	logger := newMySQLLogger(
		time.Millisecond,
		gormlogger.Info,
		slog.New(slog.NewTextHandler(&output, nil)),
	)
	logger.Trace(
		context.Background(),
		time.Now().Add(-time.Second),
		func() (string, int64) { return "SELECT * FROM users WHERE email='secret@example.com'", 0 },
		errors.New("duplicate secret@example.com"),
	)
	if strings.Contains(output.String(), "secret@example.com") {
		t.Fatalf("MySQL 日志泄露了 SQL 或驱动错误：%s", output.String())
	}
}

func TestMySQLConfigRejectsInvalidPoolAndLogLevel(t *testing.T) {
	for _, config := range []*configv1.MySQLConfig{
		{Master: "user:pass@tcp(127.0.0.1:3306)/db", MaxOpenConns: 1, MaxIdleConns: 2},
		{Master: "user:pass@tcp(127.0.0.1:3306)/db", LogLevel: "verbose"},
	} {
		if _, err := NewMySQL(context.Background(), "audit", config, newMySQLRuntime(t)); err == nil {
			t.Fatal("无效 MySQL 配置必须在连接前失败")
		}
	}
}

func newMySQLRuntime(t *testing.T) *styxruntime.Runtime {
	t.Helper()
	runtime, err := styxruntime.New(styxruntime.Config{
		Identity:     styxruntime.Identity{Name: "mysql-test", Version: "test"},
		Dependencies: observe.NoopDependencies(),
	})
	if err != nil {
		t.Fatalf("runtime.New() error = %v", err)
	}
	return runtime
}
