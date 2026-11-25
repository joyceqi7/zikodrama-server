package operations_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"drama-server/internal/biz/operations"
	operationshttp "drama-server/internal/service/operations"
	"drama-server/internal/testsupport/memory"
)

// TestDirectApplicationCreationRequiresConfiguredProvider 验证创建入口不会将未启用的直连请求静默降级为官方应用。
func TestDirectApplicationCreationRequiresConfiguredProvider(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	handler := operationshttp.NewHandler(operations.NewService(memory.OperationsRepositories(store), nil), newRBACService(t, store), operationshttp.Config{})
	login := request(t, handler, http.MethodPost, "/admin/v1/session", "application/json", "", strings.NewReader(`{"username":"admin","password":"local-admin-password"}`))
	var credentials struct {
		Token string `json:"token"`
	}
	decodeData(t, login, &credentials)
	before, err := store.ListApplications(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	response := request(t, handler, http.MethodPost, "/admin/v1/applications", "application/json", credentials.Token, strings.NewReader(`{"subjectId":"1","clientSecret":"test-only-secret","monetizationMode":"IAP","tiktokAppId":"review-direct","name":"直连测试","clientKey":"review-client","playbackProvider":"byteplus_web"}`))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("未配置直连服务应拒绝创建，实际状态：%d", response.Code)
	}
	apps, err := store.ListApplications(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != len(before) {
		t.Fatal("拒绝的直连请求仍写入了应用")
	}
}

// TestApplicationPlaybackInputRejectionLogging 验证播放参数拒绝保留统一错误和具体诊断，不输出密钥或成功路径错误日志。
func TestApplicationPlaybackInputRejectionLogging(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	handler := operationshttp.NewHandler(operations.NewService(memory.OperationsRepositories(store), nil), newRBACService(t, store), operationshttp.Config{})
	login := request(t, handler, http.MethodPost, "/admin/v1/session", "application/json", "", strings.NewReader(`{"username":"admin","password":"local-admin-password"}`))
	var credentials struct {
		Token string `json:"token"`
	}
	decodeData(t, login, &credentials)
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	var output bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	input := operations.ApplicationInput{SubjectID: "1", Name: "输入校验测试", TikTokAppID: "playback-validation", ClientKey: "validation-client", ClientSecret: "test-private-secret", MonetizationMode: "IAP"}
	for _, provider := range []string{"custom", "BYTEPLUS_WEB"} {
		output.Reset()
		input.PlaybackProvider = provider
		body, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		response := request(t, handler, http.MethodPost, "/admin/v1/applications", "application/json", credentials.Token, bytes.NewReader(body))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("应拒绝播放参数，实际状态：%d", response.Code)
		}
		raw := output.String()
		if !strings.Contains(raw, "create application input rejected") || !strings.Contains(raw, operations.ErrInvalidInput.Error()) || !strings.Contains(raw, `"level":"WARN"`) {
			t.Fatal("参数拒绝缺少具体错误诊断")
		}
		if strings.Contains(raw, input.ClientSecret) || strings.Contains(raw, credentials.Token) {
			t.Fatal("参数拒绝日志泄漏凭据")
		}
	}
	output.Reset()
	input.PlaybackProvider = ""
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	response := request(t, handler, http.MethodPost, "/admin/v1/applications", "application/json", credentials.Token, bytes.NewReader(body))
	if response.Code != http.StatusCreated || strings.Contains(output.String(), `"level":"ERROR"`) {
		t.Fatal("旧官方应用创建成功路径发生错误或误报错误日志")
	}
}
