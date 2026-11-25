package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	styx "gitee.com/linpbin/styx"
)

// TestCredentialPathsOmitBodiesFromAccessLog 验证管理接口、观众登录与回调的正文（密码、JWT、密钥、VisitorToken）不进入访问日志。
func TestCredentialPathsOmitBodiesFromAccessLog(t *testing.T) {
	t.Parallel()
	const secret = "fixture-credential-value"
	for _, test := range []struct{ method, path string }{
		{http.MethodPost, "/admin/v1/session"},
		{http.MethodPut, "/admin/v1/global-configs/media"},
		{http.MethodPost, "/callbacks/v1/byteplus/vod"},
		{http.MethodPost, "/api/v1/session"},
		{http.MethodPost, "/api/v1/bootstrap"},
	} {
		t.Run(test.path, func(t *testing.T) {
			t.Parallel()
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if body, err := io.ReadAll(r.Body); err != nil || !strings.Contains(string(body), secret) {
					t.Errorf("业务 Handler 未收到完整正文：%v", err)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"data":{"token":"`+secret+`"}}`)
			})
			output := newSynchronizedBuffer()
			_, address := newTestEngine(t, Dependencies{
				Consumer: handler, Admin: handler,
				Styx: newHTTPTestRuntimeWithSlog(t, styx.NewLogger(nil, styx.WithConsoleOutput(output))),
			})
			request, err := http.NewRequest(test.method, address+test.path, strings.NewReader(`{"password":"`+secret+`"}`))
			if err != nil {
				t.Fatal(err)
			}
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			payload, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if !strings.Contains(string(payload), secret) {
				t.Fatal("过滤日志改变了响应正文")
			}
			logged := output.Wait(t)
			var entry map[string]any
			if err := json.Unmarshal([]byte(logged), &entry); err != nil {
				t.Fatal(err)
			}
			if entry["path"] != test.path || strings.Contains(logged, secret) || strings.Contains(logged, "request_body") || strings.Contains(logged, "response_body") {
				t.Fatalf("访问日志泄露正文：%s", logged)
			}
		})
	}
}
