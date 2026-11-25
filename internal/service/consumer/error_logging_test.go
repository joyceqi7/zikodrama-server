package consumer

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConsumerHTTPLogsActualErrorWithoutExposingResponse(t *testing.T) {
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	var output bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	failure := errors.New("SELECT id FROM users: connection refused")
	response := httptest.NewRecorder()
	writeDomainError(response, httptest.NewRequest(http.MethodGet, "/api/v1/dramas", nil), failure)
	if !strings.Contains(output.String(), failure.Error()) {
		t.Fatalf("入口日志丢失原始错误：%s", output.String())
	}
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), failure.Error()) {
		t.Fatal("原始错误泄漏到HTTP响应或状态码被改变")
	}
	output.Reset()
	var input struct {
		Name string `json:"name"`
	}
	if decode(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/v1/bootstrap", strings.NewReader(`{"name":`)), &input) || !strings.Contains(output.String(), "unexpected EOF") {
		t.Fatal("解码失败没有记录实际错误")
	}
	output.Reset()
	if !decode(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/v1/bootstrap", strings.NewReader(`{"name":"ok"}`)), &input) || output.Len() != 0 {
		t.Fatal("正常JSON结束被误记为错误")
	}
}
