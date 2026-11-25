package visitor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"gitee.com/linpbin/styx"
	configv1 "gitee.com/linpbin/styx/api/config/v1"
	"go.opentelemetry.io/otel/trace"
)

// TestVisitorCredentialLogsKeepContextAndExpectedLevels 验证无效凭据和请求取消携带 Trace，正常登录不产生错误日志且凭据不进入日志。
func TestVisitorCredentialLogsKeepContextAndExpectedLevels(t *testing.T) {
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	service, err := NewService(expiryApplicationResolver{}, &expiryVisitorStore{version: 1}, Config{RequireTikTokLogin: true, IdentityTokenEncryptionSecret: "private-local-test-visitor-key"})
	if err != nil {
		t.Fatal(err)
	}
	span := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}})
	ctx := trace.ContextWithSpanContext(context.Background(), span)
	for _, mode := range []string{"无效密文", "无效载荷", "取消", "成功"} {
		t.Run(mode, func(t *testing.T) {
			var logs bytes.Buffer
			slog.SetDefault(styx.NewLogger(&configv1.LogConfig{Level: "debug", Console: true}, styx.WithConsoleOutput(&logs)))
			var result Context
			var err error
			switch mode {
			case "无效密文":
				_, err = service.Resolve(ctx, "private-invalid-visitor-token")
			case "无效载荷":
				token, sealErr := service.protector.sealVisitor([]byte(`{"secret":"private-payload",`))
				if sealErr != nil {
					t.Fatal(sealErr)
				}
				_, err = service.Resolve(ctx, token)
			case "取消":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				_, err = service.Bootstrap(cancelled, BootstrapInput{})
			case "成功":
				result, err = service.Bootstrap(ctx, BootstrapInput{TikTokAppID: "mini-app", LoginCode: "private-code"})
			}
			if mode == "成功" {
				if err != nil || result.VisitorToken == "" || logs.Len() != 0 {
					t.Fatalf("成功路径或日志改变: %v %s", err, logs.String())
				}
				return
			}
			want := ErrUnauthorized
			level := "WARN"
			if mode == "取消" {
				want = context.Canceled
				level = "INFO"
			}
			if !errors.Is(err, want) {
				t.Fatalf("错误分类改变: %v", err)
			}
			raw := logs.String()
			decoder := json.NewDecoder(&logs)
			count := 0
			for decoder.More() {
				var entry map[string]any
				if err := decoder.Decode(&entry); err != nil {
					t.Fatal(err)
				}
				count++
				if entry["level"] != level || entry["trace_id"] != span.TraceID().String() || entry["error"] == nil {
					t.Fatalf("凭据错误丢失Context或级别错误: %v", entry)
				}
			}
			if count == 0 {
				t.Fatal("缺少凭据故障日志")
			}
			for _, secret := range []string{"private-invalid-visitor-token", "private-payload", "private-local-test-visitor-key"} {
				if strings.Contains(raw, secret) {
					t.Fatal("凭据载荷泄漏")
				}
			}
		})
	}
}
