// Package mongodb provides MongoDB client factory functions for Kratos/Wire integration.
package mongodb

import (
	"context"
	"sync"
	"sync/atomic"

	"go.mongodb.org/mongo-driver/v2/event"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"gitee.com/linpbin/styx/observe"
)

// PoolMetricsCollector collects MongoDB connection pool metrics.
type PoolMetricsCollector struct {
	instanceName string
	meter        metric.Meter
	attrs        []attribute.KeyValue
	dependencies observe.Dependencies

	// Counters
	checkedOutCounter  metric.Int64Counter
	checkedInCounter   metric.Int64Counter
	openedCounter      metric.Int64Counter
	closedCounter      metric.Int64Counter
	poolClearedCounter metric.Int64Counter

	// Active connections gauge
	mu           sync.RWMutex
	activeConns  atomic.Int64
	closed       bool
	activeGauge  metric.Int64ObservableGauge
	registration metric.Registration
}

// NewPoolMetricsCollector creates a new MongoDB connection pool metrics collector.
func NewPoolMetricsCollector(instanceName string, dependencies observe.Dependencies) *PoolMetricsCollector {
	return &PoolMetricsCollector{
		instanceName: instanceName,
		meter:        dependencies.MeterProvider().Meter("gitee.com/linpbin/styx/mongodb"),
		dependencies: dependencies,
		attrs: []attribute.KeyValue{
			attribute.String("db.system", "mongodb"),
			attribute.String("db.instance", instanceName),
		},
	}
}

// PoolMonitor returns the event.PoolMonitor for MongoDB driver.
func (c *PoolMetricsCollector) PoolMonitor() *event.PoolMonitor {
	return &event.PoolMonitor{
		Event: c.handlePoolEvent,
	}
}

func (c *PoolMetricsCollector) handlePoolEvent(evt *event.PoolEvent) {
	ctx := context.Background()

	switch evt.Type {
	case event.ConnectionCheckedOut:
		if c.checkedOutCounter != nil {
			c.checkedOutCounter.Add(ctx, 1, metric.WithAttributes(c.attrs...))
		}
		c.activeConns.Add(1)
	case event.ConnectionCheckedIn:
		if c.checkedInCounter != nil {
			c.checkedInCounter.Add(ctx, 1, metric.WithAttributes(c.attrs...))
		}
		c.activeConns.Add(-1)
	case event.ConnectionCreated:
		if c.openedCounter != nil {
			c.openedCounter.Add(ctx, 1, metric.WithAttributes(c.attrs...))
		}
	case event.ConnectionClosed:
		if c.closedCounter != nil {
			c.closedCounter.Add(ctx, 1, metric.WithAttributes(c.attrs...))
		}
	case event.ConnectionPoolCleared:
		if c.poolClearedCounter != nil {
			c.poolClearedCounter.Add(ctx, 1, metric.WithAttributes(c.attrs...))
		}
	}
}

// Register registers all connection pool metrics.
func (c *PoolMetricsCollector) Register() error {
	var err error

	c.checkedOutCounter, err = c.meter.Int64Counter(
		"mongodb_pool_checked_out_total",
		metric.WithDescription("Total connections checked out from pool"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return err
	}

	c.checkedInCounter, err = c.meter.Int64Counter(
		"mongodb_pool_checked_in_total",
		metric.WithDescription("Total connections checked in to pool"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return err
	}

	c.openedCounter, err = c.meter.Int64Counter(
		"mongodb_pool_opened_total",
		metric.WithDescription("Total connections opened"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return err
	}

	c.closedCounter, err = c.meter.Int64Counter(
		"mongodb_pool_closed_total",
		metric.WithDescription("Total connections closed"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return err
	}

	c.poolClearedCounter, err = c.meter.Int64Counter(
		"mongodb_pool_cleared_total",
		metric.WithDescription("Total pool cleared events"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return err
	}

	c.activeGauge, err = c.meter.Int64ObservableGauge(
		"mongodb_pool_active_conns",
		metric.WithDescription("Current active connections"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return err
	}
	c.registration, err = c.meter.RegisterCallback(c.collectActiveConns, c.activeGauge)
	return err
}

func (c *PoolMetricsCollector) collectActiveConns(_ context.Context, observer metric.Observer) error {
	c.mu.RLock()
	isClosed := c.closed
	c.mu.RUnlock()

	if isClosed {
		return nil
	}

	active := c.activeConns.Load()
	if active < 0 {
		active = 0
	}
	observer.ObserveInt64(c.activeGauge, active, metric.WithAttributes(c.attrs...))
	return nil
}

// Close cleans up the metrics collector.
func (c *PoolMetricsCollector) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	registration := c.registration
	c.mu.Unlock()
	if registration != nil {
		if err := registration.Unregister(); err != nil {
			c.dependencies.Logger().Warn("unregister mongodb metrics failed", "error", err)
		}
	}
	c.dependencies.Logger().Debug("mongodb pool metrics collector closed", "instance", c.instanceName)
}
