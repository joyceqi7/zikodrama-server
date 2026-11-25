package apireply

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gitee.com/linpbin/styx/requestmeta"
	"go.opentelemetry.io/otel/trace"
)

// responseFailureWriter 只模拟响应写入故障，不保存正文内容。
type responseFailureWriter struct{ header http.Header }

// Header 返回用于模拟 HTTP 写入的响应头。
func (w *responseFailureWriter) Header() http.Header { return w.header }

// WriteHeader 不改变测试注入的正文写入结果。
func (w *responseFailureWriter) WriteHeader(int) {}

// Write 返回明确的传输故障。
func (w *responseFailureWriter) Write([]byte) (int, error) {
	return 0, errors.New("peer reset during response write")
}

// TestResponseFailureKeepsRequestContext 验证响应编码和写入错误关联原请求，响应和敏感数据处理保持不变。
func TestResponseFailureKeepsRequestContext(t *testing.T) {
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	for _, mode := range []string{"编码失败", "写入失败", "成功"} {
		t.Run(mode, func(t *testing.T) {
			var logs bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			sc := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}})
			request := httptest.NewRequest(http.MethodGet, "/api/v1/open/catalog", nil)
			request = request.WithContext(trace.ContextWithSpanContext(context.Background(), sc))
			request.Header.Set(requestmeta.HeaderName, "response-request-1")
			writer := httptest.NewRecorder()
			handler := requestmeta.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "编码失败" {
					Success(r.Context(), w, 200, map[string]any{"secret": "private-payload", "unsupported": make(chan int)})
				} else if mode == "写入失败" {
					Success(r.Context(), &responseFailureWriter{header: http.Header{}}, 200, map[string]string{"token": "private-response-token"})
				} else {
					Success(r.Context(), w, 200, map[string]bool{"accepted": true})
				}
			}))
			handler.ServeHTTP(writer, request)
			if mode == "成功" {
				if logs.Len() != 0 || writer.Code != 200 {
					t.Fatal("成功响应产生错误日志或状态改变")
				}
				return
			}
			var entry map[string]any
			if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
				t.Fatal(err)
			}
			message, detail := "marshal API response failed", "unsupported type: chan int"
			if mode == "写入失败" {
				message, detail = "write API response failed", "peer reset during response write"
			} else if writer.Code != 500 || !strings.Contains(writer.Body.String(), "internal_error") {
				t.Fatal("编码故障的 HTTP 错误映射改变")
			}
			if entry["msg"] != message || entry["request_id"] != "response-request-1" || entry["trace_id"] != sc.TraceID().String() || !strings.Contains(entry["error"].(string), detail) {
				t.Fatalf("响应失败缺少关联或实际错误：%v", entry)
			}
			if strings.Contains(logs.String(), "private-payload") || strings.Contains(logs.String(), "private-response-token") {
				t.Fatal("响应错误日志泄漏正文")
			}
		})
	}
}
