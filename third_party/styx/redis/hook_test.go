package redis

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"gitee.com/linpbin/styx/observe"
)

type captureHandler struct {
	records []slog.Record
}

// Enabled 捕获所有级别，避免错误降级后被测试日志级别过滤而漏检。
func (h *captureHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

// Handle 保存实际日志记录，供级别、错误详情和敏感参数断言使用。
func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r)
	return nil
}

// WithAttrs 满足测试 Handler 接口，测试钩子不依赖预绑定字段。
func (h *captureHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }

// WithGroup 满足测试 Handler 接口，测试钩子不使用日志分组。
func (h *captureHandler) WithGroup(_ string) slog.Handler { return h }

func testDependencies(t *testing.T, logger *slog.Logger) observe.Dependencies {
	t.Helper()
	dependencies, err := observe.NewDependencies(observe.Config{
		Logger:         logger,
		TracerProvider: tracenoop.NewTracerProvider(),
		MeterProvider:  metricnoop.NewMeterProvider(),
		Propagator:     propagation.TraceContext{},
	})
	if err != nil {
		t.Fatalf("创建测试运行依赖：%v", err)
	}
	return dependencies
}

// TestLoggingHookLogPipeline_IgnoresRedisNil 验证正常未命中不产生流水线错误日志。
func TestLoggingHookLogPipeline_IgnoresRedisNil(t *testing.T) {
	handler := &captureHandler{}
	orig := slog.Default()
	slog.SetDefault(slog.New(handler))
	defer slog.SetDefault(orig)

	hook := NewLoggingHook("default", time.Second, testDependencies(t, slog.New(handler)))
	cmd := goredis.NewStringCmd(context.Background(), "get", "missing-key")

	hook.logPipeline(context.Background(), []goredis.Cmder{cmd}, time.Millisecond, goredis.Nil)

	if len(handler.records) != 0 {
		t.Fatalf("redis.Nil 不应产生日志，实际 %d 条", len(handler.records))
	}
}

// testRedisServerError 保留 go-redis 对服务端错误的类型识别，避免只按文本误判网络故障。
type testRedisServerError string

// Error 返回测试服务端的具体错误内容。
func (e testRedisServerError) Error() string { return string(e) }

// RedisError 标识该错误来自 Redis 协议响应。
func (testRedisServerError) RedisError() {}

// hookedScriptClient 让真实 Script.Run 经过生产日志钩子，只替换底层命令执行结果。
type hookedScriptClient struct {
	// Scripter 占位未使用的脚本管理方法；测试只允许调用下方显式实现的执行方法。
	goredis.Scripter
	hook      *LoggingHook
	shaError  error
	evalError error
	value     int64
	commands  []string
}

// EvalSha 保留脚本摘要、键及参数，模拟服务端执行缓存脚本。
func (c *hookedScriptClient) EvalSha(ctx context.Context, sha string, keys []string, args ...interface{}) *goredis.Cmd {
	return c.execute(ctx, "evalsha", sha, keys, c.shaError, args...)
}

// Eval 使用同一组参数模拟 Script.Run 的完整脚本回退。
func (c *hookedScriptClient) Eval(ctx context.Context, script string, keys []string, args ...interface{}) *goredis.Cmd {
	return c.execute(ctx, "eval", script, keys, c.evalError, args...)
}

// EvalShaRO 模拟只读脚本缓存执行，错误仍交给真实 RunRO 判断。
func (c *hookedScriptClient) EvalShaRO(ctx context.Context, sha string, keys []string, args ...interface{}) *goredis.Cmd {
	return c.execute(ctx, "evalsha_ro", sha, keys, c.shaError, args...)
}

// EvalRO 模拟只读脚本回退，不改变调用方的错误和结果。
func (c *hookedScriptClient) EvalRO(ctx context.Context, script string, keys []string, args ...interface{}) *goredis.Cmd {
	return c.execute(ctx, "eval_ro", script, keys, c.evalError, args...)
}

func (c *hookedScriptClient) execute(ctx context.Context, operation, script string, keys []string, resultErr error, args ...interface{}) *goredis.Cmd {
	c.commands = append(c.commands, operation)
	commandArgs := []interface{}{operation, script, len(keys)}
	for _, key := range keys {
		commandArgs = append(commandArgs, key)
	}
	commandArgs = append(commandArgs, args...)
	cmd := goredis.NewCmd(ctx, commandArgs...)
	err := c.hook.ProcessHook(func(context.Context, goredis.Cmder) error { return resultErr })(ctx, cmd)
	cmd.SetErr(err)
	if err == nil {
		cmd.SetVal(c.value)
	}
	return cmd
}

// TestLoggingHookScriptFallback 验证真实脚本回退、锁竞争、错误传播和敏感参数保护。
func TestLoggingHookScriptFallback(t *testing.T) {
	cacheMiss := testRedisServerError("NOSCRIPT No matching script. Please use EVAL.")
	permissionDenied := testRedisServerError("NOPERM this user has no permissions to run the command")
	timeout := errors.New("dial tcp: i/o timeout")
	ordinaryError := errors.New("NOSCRIPT text in an ordinary client error")
	cases := []struct {
		name      string
		shaError  error
		evalError error
		value     int64
		fallback  bool
		wantError error
	}{
		{name: "缓存命中成功", value: 1},
		{name: "正常锁竞争返回零", value: 0},
		{name: "未命中后正常回退", shaError: cacheMiss, value: 1, fallback: true},
		{name: "未命中回退后正常锁竞争", shaError: cacheMiss, value: 0, fallback: true},
		{name: "服务端ERR前缀仍正常回退", shaError: testRedisServerError("ERR NOSCRIPT No matching script"), value: 1, fallback: true},
		{name: "包装服务端错误仍正常回退", shaError: fmt.Errorf("script cache: %w", cacheMiss), value: 1, fallback: true},
		{name: "回退执行失败保留原因", shaError: cacheMiss, evalError: permissionDenied, fallback: true, wantError: permissionDenied},
		{name: "摘要执行权限不足", shaError: permissionDenied, wantError: permissionDenied},
		{name: "网络超时", shaError: timeout, wantError: timeout},
		{name: "普通错误不能按文本忽略", shaError: ordinaryError, wantError: ordinaryError},
	}
	for _, readOnly := range []bool{false, true} {
		suffix := ""
		if readOnly {
			suffix = "_ro"
		}
		for _, tc := range cases {
			t.Run("evalsha"+suffix+"/"+tc.name, func(t *testing.T) {
				handler := &captureHandler{}
				client := &hookedScriptClient{
					hook:     NewLoggingHook("global-config", time.Second, testDependencies(t, slog.New(handler))),
					shaError: tc.shaError, evalError: tc.evalError, value: tc.value,
				}
				script := goredis.NewScript("return ARGV[1] -- private-script-content")
				var result *goredis.Cmd
				if readOnly {
					result = script.RunRO(context.Background(), client, []string{"private-lock-key"}, "private-lock-owner")
				} else {
					result = script.Run(context.Background(), client, []string{"private-lock-key"}, "private-lock-owner")
				}
				if result.Err() != tc.wantError {
					t.Fatalf("日志钩子改变原始错误：实际 %v，期望 %v", result.Err(), tc.wantError)
				}
				wantCommands := []string{"evalsha" + suffix}
				if tc.fallback {
					wantCommands = append(wantCommands, "eval"+suffix)
				}
				if !reflect.DeepEqual(client.commands, wantCommands) {
					t.Fatalf("脚本重试流程改变：实际 %v，期望 %v", client.commands, wantCommands)
				}
				if tc.wantError == nil {
					if result.Val() != tc.value || len(handler.records) != 0 {
						t.Fatalf("成功、正常锁竞争或缓存回退被误报：结果 %v，日志数 %d", result.Val(), len(handler.records))
					}
					return
				}
				assertRedisErrorRecord(t, handler.records, "redis command error", wantCommands[len(wantCommands)-1], tc.wantError)
			})
		}
	}
}

// TestLoggingHookOtherCommandErrors 验证完整脚本和其他命令的 NOSCRIPT 不会被过滤。
func TestLoggingHookOtherCommandErrors(t *testing.T) {
	for _, operation := range []string{"eval", "eval_ro", "get"} {
		t.Run(operation, func(t *testing.T) {
			handler := &captureHandler{}
			hook := NewLoggingHook("global-config", time.Second, testDependencies(t, slog.New(handler)))
			cmd := goredis.NewCmd(context.Background(), operation, "private-script-content", "private-lock-key", "private-lock-owner")
			failure := testRedisServerError("NOSCRIPT returned by the operation itself")
			err := hook.ProcessHook(func(context.Context, goredis.Cmder) error { return failure })(context.Background(), cmd)
			if err != failure {
				t.Fatalf("原始命令错误改变：%v", err)
			}
			assertRedisErrorRecord(t, handler.records, "redis command error", operation, failure)
		})
	}
}

// TestLoggingHookPipelineErrorDetails 验证流水线错误保留具体原因，且不会误用单命令回退规则。
func TestLoggingHookPipelineErrorDetails(t *testing.T) {
	for _, failure := range []error{testRedisServerError("NOSCRIPT No matching script"), errors.New("read tcp: i/o timeout")} {
		t.Run(failure.Error(), func(t *testing.T) {
			handler := &captureHandler{}
			hook := NewLoggingHook("global-config", time.Second, testDependencies(t, slog.New(handler)))
			cmd := goredis.NewCmd(context.Background(), "evalsha", "private-script-content", "private-lock-key", "private-lock-owner")
			err := hook.ProcessPipelineHook(func(context.Context, []goredis.Cmder) error { return failure })(context.Background(), []goredis.Cmder{cmd})
			if err != failure {
				t.Fatalf("流水线原始错误改变：%v", err)
			}
			assertRedisErrorRecord(t, handler.records, "redis pipeline error", "pipeline", failure)
		})
	}
}

func assertRedisErrorRecord(t *testing.T, records []slog.Record, message, operation string, failure error) {
	t.Helper()
	if len(records) != 1 {
		t.Fatalf("真实失败应只记录一条错误日志，实际 %d 条", len(records))
	}
	record := records[0]
	if record.Level != slog.LevelError || record.Message != message {
		t.Fatalf("真实失败的日志级别或操作描述错误：%s %s", record.Level, record.Message)
	}
	attrs := map[string]any{}
	record.Attrs(func(attr slog.Attr) bool {
		attrs[attr.Key] = attr.Value.Any()
		return true
	})
	if fmt.Sprint(attrs["error"]) != failure.Error() || attrs["db.operation"] != operation || attrs["db.instance"] != "global-config" {
		t.Fatalf("日志缺少具体错误或操作定位信息：%v", attrs)
	}
	serialized := fmt.Sprint(attrs)
	for _, secret := range []string{"private-script-content", "private-lock-key", "private-lock-owner"} {
		if strings.Contains(serialized, secret) {
			t.Fatal("Redis 错误日志泄露脚本、键或持有者参数")
		}
	}
}
