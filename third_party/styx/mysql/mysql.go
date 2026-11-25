// Package mysql 提供纳入 Runtime 生命周期的 GORM MySQL 工厂。
package mysql

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/XSAM/otelsql"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/plugin/dbresolver"
	"gorm.io/plugin/opentelemetry/tracing"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
	"gitee.com/linpbin/styx/internal/lifecycle"
	"gitee.com/linpbin/styx/internal/logutil"
	styxruntime "gitee.com/linpbin/styx/runtime"
)

// Default values for MySQL configuration
const (
	defaultMaxOpenConns    = 100
	defaultMaxIdleConns    = 10
	defaultConnMaxLifetime = time.Hour
	defaultConnMaxIdleTime = 10 * time.Minute
	defaultSlowThreshold   = 200 * time.Millisecond
)

var errMySQLDependency = errors.New("mysql dependency failure")

// NewMySQL 创建带 OTel 插桩的 GORM 连接，并把指标注销和数据库关闭交给 Runtime。
func NewMySQL(ctx context.Context, name string, c *configv1.MySQLConfig, runtime *styxruntime.Runtime) (*gorm.DB, error) {
	// 1. Validate config
	if ctx == nil {
		return nil, fmt.Errorf("mysql: context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("mysql: initialization context: %w", err)
	}
	if runtime == nil {
		return nil, fmt.Errorf("mysql: runtime is required")
	}
	dependencies := runtime.Dependencies()
	if err := dependencies.Validate(); err != nil {
		return nil, fmt.Errorf("mysql: runtime dependencies: %w", err)
	}
	if c == nil {
		return nil, fmt.Errorf("mysql: config is nil")
	}
	if c.GetMaster() == "" {
		return nil, fmt.Errorf("mysql: master DSN is required")
	}
	if c.GetMaxOpenConns() < 0 || c.GetMaxIdleConns() < 0 {
		return nil, fmt.Errorf("mysql: connection pool limits must not be negative")
	}
	if c.GetMaxOpenConns() > 0 && c.GetMaxIdleConns() > c.GetMaxOpenConns() {
		return nil, fmt.Errorf("mysql: max idle connections must not exceed max open connections")
	}
	if !validLogLevel(c.GetLogLevel()) {
		return nil, fmt.Errorf("mysql: unsupported log level %q", c.GetLogLevel())
	}

	// 2. Create gorm logger with slow query threshold and log level
	slowThreshold := defaultSlowThreshold
	if c.GetSlowThreshold() != nil {
		slowThreshold = c.GetSlowThreshold().AsDuration()
		if slowThreshold <= 0 {
			return nil, fmt.Errorf("mysql: slow threshold must be positive")
		}
	}
	logLevel := parseLogLevel(c.GetLogLevel())
	logger := newMySQLLogger(slowThreshold, logLevel, dependencies.Logger())

	// 3. Create connection with gorm
	db, err := gorm.Open(mysql.Open(c.GetMaster()), &gorm.Config{
		Logger:               logger,
		DisableAutomaticPing: true,
	})
	if err != nil {
		return nil, fmt.Errorf("mysql: failed to open connection: %w", err)
	}

	// 4. Configure connection pool
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("mysql: failed to get sql.DB: %w", err)
	}

	maxOpenConns := int(c.GetMaxOpenConns())
	if maxOpenConns <= 0 {
		maxOpenConns = defaultMaxOpenConns
	}
	sqlDB.SetMaxOpenConns(maxOpenConns)

	maxIdleConns := int(c.GetMaxIdleConns())
	if maxIdleConns <= 0 {
		maxIdleConns = defaultMaxIdleConns
	}
	sqlDB.SetMaxIdleConns(maxIdleConns)

	connMaxLifetime := defaultConnMaxLifetime
	if c.GetConnMaxLifetime() != nil {
		connMaxLifetime = c.GetConnMaxLifetime().AsDuration()
		if connMaxLifetime <= 0 {
			sqlDB.Close()
			return nil, fmt.Errorf("mysql: connection max lifetime must be positive")
		}
	}
	sqlDB.SetConnMaxLifetime(connMaxLifetime)

	connMaxIdleTime := defaultConnMaxIdleTime
	if c.GetConnMaxIdleTime() != nil {
		connMaxIdleTime = c.GetConnMaxIdleTime().AsDuration()
		if connMaxIdleTime <= 0 {
			sqlDB.Close()
			return nil, fmt.Errorf("mysql: connection max idle time must be positive")
		}
	}
	sqlDB.SetConnMaxIdleTime(connMaxIdleTime)

	// 5. Inject OTel tracing
	if err := db.Use(tracing.NewPlugin(
		tracing.WithoutMetrics(),
		tracing.WithoutQueryVariables(),
		tracing.WithTracerProvider(mysqlSafeTracerProvider{TracerProvider: dependencies.TracerProvider()}),
	)); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("mysql: failed to register otel tracing plugin: %w", err)
	}
	if err := registerMySQLSQLCallbacks(db); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("mysql: failed to register SQL log callbacks: %w", err)
	}

	// 6. Register connection pool metrics
	metricsAttrs := []attribute.KeyValue{semconv.DBSystemMySQL}
	if name != "" {
		metricsAttrs = append(metricsAttrs, attribute.String("db.instance", name))
	}
	registration, err := otelsql.RegisterDBStatsMetrics(
		sqlDB,
		otelsql.WithMeterProvider(dependencies.MeterProvider()),
		otelsql.WithAttributes(metricsAttrs...),
	)
	if err != nil {
		dependencies.Logger().Warn("mysql: failed to register connection pool metrics", "error", err)
	}

	// 7. Setup read replicas if configured
	if len(c.GetSlaves()) > 0 {
		var replicas []gorm.Dialector
		for _, slave := range c.GetSlaves() {
			replicas = append(replicas, mysql.Open(slave))
		}

		err = db.Use(dbresolver.Register(dbresolver.Config{
			Replicas: replicas,
			Policy:   dbresolver.RandomPolicy{},
		}).SetMaxIdleConns(maxIdleConns).
			SetMaxOpenConns(maxOpenConns).
			SetConnMaxLifetime(connMaxLifetime).
			SetConnMaxIdleTime(connMaxIdleTime))
		if err != nil {
			if registration != nil {
				_ = registration.Unregister()
			}
			sqlDB.Close()
			return nil, fmt.Errorf("mysql: failed to configure dbresolver: %w", err)
		}
	}

	// 8. Ping to verify connection
	if err := sqlDB.PingContext(ctx); err != nil {
		if registration != nil {
			_ = registration.Unregister()
		}
		sqlDB.Close()
		return nil, fmt.Errorf("mysql: failed to ping: %w", err)
	}

	shutdown := styxruntime.BoundedClose(func() error {
		var shutdownErrors []error
		if registration != nil {
			shutdownErrors = append(shutdownErrors, registration.Unregister())
		}
		shutdownErrors = append(shutdownErrors, sqlDB.Close())
		return errors.Join(shutdownErrors...)
	})
	if err := runtime.Own("mysql "+name, shutdown); err != nil {
		return nil, lifecycle.Rollback("mysql "+name, shutdown, err)
	}
	return db, nil
}

type mysqlSafeTracerProvider struct {
	trace.TracerProvider
}

func (provider mysqlSafeTracerProvider) Tracer(name string, options ...trace.TracerOption) trace.Tracer {
	return mysqlSafeTracer{Tracer: provider.TracerProvider.Tracer(name, options...)}
}

type mysqlSafeTracer struct {
	trace.Tracer
}

func (tracer mysqlSafeTracer) Start(
	ctx context.Context,
	name string,
	options ...trace.SpanStartOption,
) (context.Context, trace.Span) {
	ctx, span := tracer.Tracer.Start(ctx, name, options...)
	return ctx, mysqlSafeSpan{Span: span}
}

type mysqlSafeSpan struct {
	trace.Span
}

func (span mysqlSafeSpan) RecordError(error, ...trace.EventOption) {
	span.Span.RecordError(errMySQLDependency)
}

func (span mysqlSafeSpan) SetStatus(code codes.Code, _ string) {
	span.Span.SetStatus(code, errMySQLDependency.Error())
}

// mysqlLogger implements gorm.io/gorm/logger.Interface.
// Uses slog for logging with OTel tracing support.
type mysqlLogger struct {
	slowThreshold             time.Duration
	ignoreRecordNotFoundError bool
	logLevel                  gormlogger.LogLevel
	logger                    *slog.Logger
}

// newMySQLLogger creates a new gorm logger with slow query threshold and log level.
func newMySQLLogger(slowThreshold time.Duration, logLevel gormlogger.LogLevel, logger *slog.Logger) *mysqlLogger {
	return &mysqlLogger{
		slowThreshold:             slowThreshold,
		ignoreRecordNotFoundError: true,
		logLevel:                  logLevel,
		logger:                    logger,
	}
}

// parseLogLevel parses log level string to gorm logger level.
// Supported values: silent, error, warn, info (default: warn)
func parseLogLevel(level string) gormlogger.LogLevel {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "silent":
		return gormlogger.Silent
	case "error":
		return gormlogger.Error
	case "warn", "":
		return gormlogger.Warn
	case "info":
		return gormlogger.Info
	default:
		return gormlogger.Warn
	}
}

func validLogLevel(level string) bool {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "", "silent", "error", "warn", "info":
		return true
	default:
		return false
	}
}

// LogMode implements gorm/logger.Interface.
func (l *mysqlLogger) LogMode(level gormlogger.LogLevel) gormlogger.Interface {
	newLogger := *l
	newLogger.logLevel = level
	return &newLogger
}

// ParamsFilter 禁止 GORM 为常规查询日志插入绑定参数；Scan 的 Recorder 路径另由 SQL 回调处理。
func (l *mysqlLogger) ParamsFilter(_ context.Context, sql string, _ ...interface{}) (string, []interface{}) {
	return sql, nil
}

// Info implements gorm/logger.Interface.
func (l *mysqlLogger) Info(ctx context.Context, msg string, data ...interface{}) {
	if l.logLevel >= gormlogger.Info {
		logutil.Info(l.logger, ctx, "mysql info", "db.system", "mysql", "event", msg)
	}
}

// Warn implements gorm/logger.Interface.
func (l *mysqlLogger) Warn(ctx context.Context, msg string, data ...interface{}) {
	if l.logLevel >= gormlogger.Warn {
		logutil.Warn(l.logger, ctx, "mysql warning", "db.system", "mysql", "event", msg)
	}
}

// Error implements gorm/logger.Interface.
func (l *mysqlLogger) Error(ctx context.Context, msg string, data ...interface{}) {
	if l.logLevel >= gormlogger.Error {
		logutil.Error(l.logger, ctx, "mysql error", "db.system", "mysql", "event", msg)
	}
}

// Trace implements gorm/logger.Interface.
// SQL 日志只使用回调捕获并脱敏的模板，不使用 fc 返回的已插值语句。
func (l *mysqlLogger) Trace(ctx context.Context, begin time.Time, fc func() (sql string, rowsAffected int64), err error) {
	if l.logLevel <= gormlogger.Silent {
		return
	}

	duration := time.Since(begin)
	sql, rows := fc()

	// Build log attributes
	operation := parseSQLOperation(sql)
	attrs := []any{
		"db.system", "mysql",
		"db.operation", operation,
		"duration_ms", duration.Milliseconds(),
		"rows", rows,
	}
	if safeSQL, ok := ctx.Value(mysqlSQLContextKey{}).(string); ok && safeSQL != "" {
		attrs = append(attrs, "sql", safeSQL)
	}

	// Set span attributes for tracing
	setMySQLSpanAttributes(ctx,
		attribute.String("db.system", "mysql"),
		attribute.String("db.operation", operation),
	)

	// Handle different log scenarios
	switch {
	case err != nil && l.logLevel >= gormlogger.Error:
		// Skip ErrRecordNotFound if configured
		if l.ignoreRecordNotFoundError && errors.Is(err, gorm.ErrRecordNotFound) {
			return
		}
		attrs = append(attrs, "error_type", fmt.Sprintf("%T", err))
		recordMySQLSpanError(ctx, err)
		logutil.Error(l.logger, ctx, "mysql query failed", attrs...)

	case duration >= l.slowThreshold && l.slowThreshold != 0 && l.logLevel >= gormlogger.Warn:
		attrs = append(attrs, "slow", true, "threshold_ms", l.slowThreshold.Milliseconds())
		logutil.Warn(l.logger, ctx, "mysql slow query", attrs...)

	case l.logLevel >= gormlogger.Info:
		logutil.Info(l.logger, ctx, "mysql query", attrs...)
	}
}

// parseSQLOperation extracts the operation type from SQL statement.
func parseSQLOperation(sql string) string {
	sql = strings.TrimLeft(sql, " \t\n")
	if len(sql) == 0 {
		return "unknown"
	}

	end := strings.IndexAny(sql, " \t\n")
	if end == -1 {
		end = len(sql)
	}

	switch strings.ToUpper(sql[:end]) {
	case "SELECT":
		return "select"
	case "INSERT", "REPLACE":
		return "insert"
	case "UPDATE":
		return "update"
	case "DELETE":
		return "delete"
	default:
		return "exec"
	}
}

// setMySQLSpanAttributes sets attributes on the current span.
func setMySQLSpanAttributes(ctx context.Context, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	if span.IsRecording() {
		span.SetAttributes(attrs...)
	}
}

// recordMySQLSpanError records an error on the current span.
func recordMySQLSpanError(ctx context.Context, _ error) {
	span := trace.SpanFromContext(ctx)
	if span.IsRecording() {
		span.RecordError(errMySQLDependency)
	}
}
