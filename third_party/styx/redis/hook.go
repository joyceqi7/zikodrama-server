// Package redis 提供纳入 Runtime 生命周期的 Redis 客户端与命令诊断。
package redis

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"gitee.com/linpbin/styx/internal/logutil"
	"gitee.com/linpbin/styx/observe"
)

var errRedisDependency = errors.New("redis dependency failure")

// LoggingHook 记录 Redis 慢命令与真实失败，不采集命令参数或值。
type LoggingHook struct {
	// SlowThreshold 指定慢命令告警阈值，零值不启用慢命令告警。
	SlowThreshold time.Duration
	// InstanceName 区分连接池的诊断记录，不包含连接地址或凭据。
	InstanceName string
	dependencies observe.Dependencies
}

// NewLoggingHook 使用运行依赖记录指定实例的命令诊断；非正阈值采用默认慢命令阈值。
func NewLoggingHook(instanceName string, slowThreshold time.Duration, dependencies observe.Dependencies) *LoggingHook {
	if slowThreshold <= 0 {
		slowThreshold = DefaultSlowThreshold
	}
	return &LoggingHook{
		SlowThreshold: slowThreshold,
		InstanceName:  instanceName,
		dependencies:  dependencies,
	}
}

// DialHook 保留原连接建立流程及其返回结果，不额外记录连接地址或认证信息。
func (h *LoggingHook) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		return next(ctx, network, addr)
	}
}

// ProcessHook 记录单条命令耗时及真实错误，原样返回错误以保留客户端重试语义。
func (h *LoggingHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		start := time.Now()
		err := next(ctx, cmd)
		duration := time.Since(start)
		h.logCommand(ctx, cmd, duration, err)
		return err
	}
}

// ProcessPipelineHook 记录流水线整体耗时及错误，保留各命令结果和原错误传播。
func (h *LoggingHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		start := time.Now()
		err := next(ctx, cmds)
		duration := time.Since(start)
		h.logPipeline(ctx, cmds, duration, err)
		return err
	}
}

// logCommand 只记录命令名称、耗时及错误，不采集脚本、键名或参数值。
func (h *LoggingHook) logCommand(ctx context.Context, cmd redis.Cmder, duration time.Duration, err error) {
	cmdName := cmd.Name()
	attrs := []any{
		"db.system", "redis",
		"db.operation", cmdName,
		"duration_ms", duration.Milliseconds(),
	}

	if h.InstanceName != "" {
		attrs = append(attrs, "db.instance", h.InstanceName)
	}

	// 复用当前 Span 关联连接池与操作，不将命令参数写入链路。
	setSpanAttributes(ctx,
		attribute.String("db.system", "redis"),
		attribute.String("db.operation", cmdName),
	)

	switch {
	case err != nil && !errors.Is(err, redis.Nil):
		// Script.Run 会在脚本缓存未命中时改用 EVAL；该中间结果不应误报为业务故障。
		// EVAL、权限和网络等真实失败仍记录具体原因，并原样交给调用方。
		if isInternalCmd(cmdName) || isScriptCacheMiss(cmdName, err) {
			break
		}
		attrs = append(attrs, "error", err, "error_type", fmt.Sprintf("%T", err))
		recordSpanError(ctx, err)
		logutil.Error(h.dependencies.Logger(), ctx, "redis command error", attrs...)

	case duration >= h.SlowThreshold && h.SlowThreshold != 0:
		attrs = append(attrs, "slow", true, "threshold_ms", h.SlowThreshold.Milliseconds())
		logutil.Warn(h.dependencies.Logger(), ctx, "redis slow command", attrs...)
	}
}

// logPipeline 记录流水线整体错误及命令名称，不采集各命令的键名和参数。
func (h *LoggingHook) logPipeline(ctx context.Context, cmds []redis.Cmder, duration time.Duration, err error) {
	cmdCount := len(cmds)
	cmdNames := make([]string, 0, cmdCount)
	for _, cmd := range cmds {
		cmdNames = append(cmdNames, cmd.Name())
	}

	attrs := []any{
		"db.system", "redis",
		"db.operation", "pipeline",
		"db.operations", strings.Join(cmdNames, ","),
		"duration_ms", duration.Milliseconds(),
		"cmd_count", cmdCount,
	}

	if h.InstanceName != "" {
		attrs = append(attrs, "db.instance", h.InstanceName)
	}

	// 流水线沿用当前 Span；命令名称只用于定位失败操作。
	setSpanAttributes(ctx,
		attribute.String("db.system", "redis"),
		attribute.String("db.operation", "pipeline"),
	)

	switch {
	case err != nil && !errors.Is(err, redis.Nil):
		// 流水线不会在此自动执行脚本回退，NOSCRIPT 也必须保留为真实失败。
		attrs = append(attrs, "error", err, "error_type", fmt.Sprintf("%T", err))
		recordSpanError(ctx, err)
		logutil.Error(h.dependencies.Logger(), ctx, "redis pipeline error", attrs...)

	case duration >= h.SlowThreshold && h.SlowThreshold != 0:
		attrs = append(attrs, "slow", true, "threshold_ms", h.SlowThreshold.Milliseconds())
		logutil.Warn(h.dependencies.Logger(), ctx, "redis slow pipeline", attrs...)
	}
}

// isScriptCacheMiss 识别 Script.Run 与 RunRO 可自动回退的脚本缓存未命中。
// 只接受 Redis 服务端错误，其他命令或仅含相同文本的普通错误不能被忽略。
func isScriptCacheMiss(cmdName string, err error) bool {
	return (cmdName == "evalsha" || cmdName == "evalsha_ro") && redis.HasErrorPrefix(err, "NOSCRIPT")
}

// isInternalCmd 识别由 go-redis 在连接握手阶段自行处理的内部命令。
func isInternalCmd(cmdName string) bool {
	switch cmdName {
	case "client", "hello", "auth":
		return true
	}
	return false
}

// setSpanAttributes 只为正在记录的当前 Span 补充命令元数据。
func setSpanAttributes(ctx context.Context, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	if span.IsRecording() {
		span.SetAttributes(attrs...)
	}
}

// recordSpanError 保留现有链路故障分类，具体错误原因由命令日志记录。
func recordSpanError(ctx context.Context, _ error) {
	span := trace.SpanFromContext(ctx)
	if span.IsRecording() {
		span.RecordError(errRedisDependency)
	}
}
