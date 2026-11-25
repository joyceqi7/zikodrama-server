// Package mongodb 提供纳入 Runtime 生命周期的 MongoDB 客户端工厂。
package mongodb

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"go.opentelemetry.io/contrib/instrumentation/go.mongodb.org/mongo-driver/v2/mongo/otelmongo"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
	"gitee.com/linpbin/styx/internal/lifecycle"
	styxruntime "gitee.com/linpbin/styx/runtime"
)

// Default configuration values
const (
	DefaultMinPoolSize     = 5
	DefaultMaxPoolSize     = 100
	DefaultMaxConnIdleTime = 10 * time.Minute
	DefaultConnectTimeout  = 10 * time.Second
	DefaultServerTimeout   = 30 * time.Second
)

// NewMongoDB 创建带 OTel 插桩的 MongoDB 连接，并把指标注销和断开连接交给 Runtime。
func NewMongoDB(ctx context.Context, name string, c *configv1.MongoConfig, runtime *styxruntime.Runtime) (*mongo.Database, error) {
	if ctx == nil {
		return nil, fmt.Errorf("mongodb: context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("mongodb: initialization context: %w", err)
	}
	if runtime == nil {
		return nil, fmt.Errorf("mongodb: runtime is required")
	}
	dependencies := runtime.Dependencies()
	if err := dependencies.Validate(); err != nil {
		return nil, fmt.Errorf("mongodb: runtime dependencies: %w", err)
	}
	if c == nil {
		return nil, fmt.Errorf("mongodb: config is nil")
	}
	if c.Uri == "" {
		return nil, fmt.Errorf("mongodb: uri is required")
	}
	if c.Database == "" {
		return nil, fmt.Errorf("mongodb: database is required")
	}
	if c.MinPoolSize < 0 || c.MaxPoolSize < 0 {
		return nil, fmt.Errorf("mongodb: pool limits must not be negative")
	}

	// Apply defaults
	minPoolSize := uint64(c.MinPoolSize)
	if minPoolSize <= 0 {
		minPoolSize = DefaultMinPoolSize
	}

	maxPoolSize := uint64(c.MaxPoolSize)
	if maxPoolSize <= 0 {
		maxPoolSize = DefaultMaxPoolSize
	}
	if minPoolSize > maxPoolSize {
		return nil, fmt.Errorf("mongodb: min pool size must not exceed max pool size")
	}

	maxConnIdleTime := DefaultMaxConnIdleTime
	if c.MaxConnIdleTime != nil {
		maxConnIdleTime = c.MaxConnIdleTime.AsDuration()
		if maxConnIdleTime <= 0 {
			return nil, fmt.Errorf("mongodb: max connection idle time must be positive")
		}
	}

	connectTimeout := DefaultConnectTimeout
	if c.ConnectTimeout != nil {
		connectTimeout = c.ConnectTimeout.AsDuration()
		if connectTimeout <= 0 {
			return nil, fmt.Errorf("mongodb: connect timeout must be positive")
		}
	}

	serverTimeout := DefaultServerTimeout
	if c.ServerTimeout != nil {
		serverTimeout = c.ServerTimeout.AsDuration()
		if serverTimeout <= 0 {
			return nil, fmt.Errorf("mongodb: server timeout must be positive")
		}
	}

	// Parse read preference
	rp, err := parseReadPreference(c.ReadPreference)
	if err != nil {
		return nil, fmt.Errorf("mongodb: invalid read_preference: %w", err)
	}

	// Create client options
	opts := options.Client().
		ApplyURI(c.Uri).
		SetMinPoolSize(minPoolSize).
		SetMaxPoolSize(maxPoolSize).
		SetMaxConnIdleTime(maxConnIdleTime).
		SetConnectTimeout(connectTimeout).
		SetServerSelectionTimeout(serverTimeout).
		SetReadPreference(rp)

	// OTel tracing
	if !c.DisableTracing {
		opts.SetMonitor(otelmongo.NewMonitor(
			otelmongo.WithTracerProvider(dependencies.TracerProvider()),
			otelmongo.WithMeterProvider(dependencies.MeterProvider()),
		))
	}

	// Pool metrics collector
	var metricsCollector *PoolMetricsCollector
	if !c.DisableMetrics {
		metricsCollector = NewPoolMetricsCollector(name, dependencies)
		if err := metricsCollector.Register(); err != nil {
			dependencies.Logger().Warn("mongodb: failed to register pool metrics", "error", err)
			metricsCollector.Close()
			metricsCollector = nil
		} else {
			opts.SetPoolMonitor(metricsCollector.PoolMonitor())
		}
	}

	// Connect with timeout
	connectContext, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()

	client, err := mongo.Connect(opts)
	if err != nil {
		if metricsCollector != nil {
			metricsCollector.Close()
		}
		return nil, fmt.Errorf("mongodb: connect failed: %w", err)
	}

	// Ping to verify connection
	if err := client.Ping(connectContext, rp); err != nil {
		disconnectClient(client)
		if metricsCollector != nil {
			metricsCollector.Close()
		}
		return nil, fmt.Errorf("mongodb: ping failed: %w", err)
	}

	db := client.Database(c.Database)

	dependencies.Logger().Info("mongodb: connected", "name", name, "database", c.Database)

	shutdown := func(ctx context.Context) error {
		if metricsCollector != nil {
			metricsCollector.Close()
		}
		return client.Disconnect(ctx)
	}
	if err := runtime.Own("mongodb "+name, shutdown); err != nil {
		return nil, lifecycle.Rollback("mongodb "+name, shutdown, err)
	}
	return db, nil
}

func disconnectClient(client *mongo.Client) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultConnectTimeout)
	defer cancel()
	_ = client.Disconnect(ctx)
}

// parseReadPreference converts read preference string to readpref.ReadPref.
// Supported values: primary, primaryPreferred, secondary, secondaryPreferred, nearest
func parseReadPreference(pref string) (*readpref.ReadPref, error) {
	switch strings.ToLower(strings.TrimSpace(pref)) {
	case "", "primary":
		return readpref.Primary(), nil
	case "primarypreferred":
		return readpref.PrimaryPreferred(), nil
	case "secondary":
		return readpref.Secondary(), nil
	case "secondarypreferred":
		return readpref.SecondaryPreferred(), nil
	case "nearest":
		return readpref.Nearest(), nil
	default:
		return nil, fmt.Errorf("unknown read preference: %s", pref)
	}
}
