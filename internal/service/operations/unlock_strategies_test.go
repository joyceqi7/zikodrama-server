package operations_test

import (
	"net/http"
	"strings"
	"testing"

	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/operations"
	operationshttp "drama-server/internal/service/operations"
	"drama-server/internal/testsupport/memory"
)

// TestApplicationStrategyHTTP 验证策略面板只接受当前应用的合法唯一解锁方式。
func TestApplicationStrategyHTTP(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	handler := operationshttp.NewHandler(operations.NewService(memory.OperationsRepositories(store), nil), newRBACService(t, store), operationshttp.Config{})
	login := request(t, handler, "POST", "/admin/v1/session", "application/json", "", strings.NewReader(`{"username":"admin","password":"local-admin-password"}`))
	var credentials struct {
		Token string `json:"token"`
	}
	decodeData(t, login, &credentials)

	for _, token := range []string{"", credentials.Token} {
		response := request(t, handler, "GET", "/admin/v1/application-strategy", "", token, nil)
		if response.Code == http.StatusOK {
			t.Fatal("缺少身份或应用仍返回配置")
		}
	}

	valid := `{"document":{"schemaVersion":1,"enabled":false,"freeEpisodeCount":1,"rules":[{"code":"episode","type":"rewarded_ad","enabled":true,"config":{"requiredAdCount":1},"grants":[{"type":"episode_access","config":{"selection":"next_locked_episodes","count":2,"validity":"unlimited"}}]}]},"rules":{}}`
	response := requestWithHeaders(t, handler, "PUT", "/admin/v1/application-strategy", "application/json", credentials.Token, strings.NewReader(valid), map[string]string{"X-Application-ID": memory.TestApplicationID})
	if response.Code != http.StatusOK {
		t.Fatalf("合法策略保存失败: %d %s", response.Code, response.Body)
	}

	// 插屏开关存入原策略 JSON，显式 false 必须原样回显，不能当成未配置。
	for _, flag := range []string{"false", "true"} {
		input := strings.Replace(valid, `"schemaVersion":1`, `"schemaVersion":1,"interstitialEnabled":`+flag, 1)
		response := requestWithHeaders(t, handler, "PUT", "/admin/v1/application-strategy", "application/json", credentials.Token, strings.NewReader(input), map[string]string{"X-Application-ID": memory.TestApplicationID})
		if response.Code != http.StatusOK {
			t.Fatalf("插屏开关保存失败: %d %s", response.Code, response.Body)
		}
		var saved monetization.ApplicationStrategy
		decodeData(t, response, &saved)
		if saved.Strategy == nil || saved.Strategy.Document.InterstitialEnabled == nil || *saved.Strategy.Document.InterstitialEnabled != (flag == "true") {
			t.Fatal("插屏开关未保存或回显")
		}
		read := requestWithHeaders(t, handler, "GET", "/admin/v1/application-strategy", "", credentials.Token, nil, map[string]string{"X-Application-ID": memory.TestApplicationID})
		var restored monetization.ApplicationStrategy
		decodeData(t, read, &restored)
		if restored.Strategy == nil || restored.Strategy.Document.InterstitialEnabled == nil || *restored.Strategy.Document.InterstitialEnabled != (flag == "true") {
			t.Fatal("重新读取后插屏开关丢失")
		}
	}
	invalidToggle := strings.Replace(valid, `"schemaVersion":1`, `"schemaVersion":1,"interstitialEnabled":"false"`, 1)
	if response := requestWithHeaders(t, handler, "PUT", "/admin/v1/application-strategy", "application/json", credentials.Token, strings.NewReader(invalidToggle), map[string]string{"X-Application-ID": memory.TestApplicationID}); response.Code != http.StatusBadRequest {
		t.Fatal("插屏开关必须为布尔值")
	}

	duplicate := `{"document":{"schemaVersion":1,"enabled":false,"freeEpisodeCount":1,"rules":[{"code":"episode","type":"rewarded_ad","enabled":true,"config":{"requiredAdCount":1},"grants":[{"type":"episode_access","config":{"selection":"next_locked_episodes","count":2,"validity":"unlimited"}}]},{"code":"episode_2","type":"rewarded_ad","enabled":true,"config":{"requiredAdCount":1},"grants":[{"type":"episode_access","config":{"selection":"next_locked_episodes","count":1,"validity":"unlimited"}}]}]},"rules":{}}`
	response = requestWithHeaders(t, handler, "PUT", "/admin/v1/application-strategy", "application/json", credentials.Token, strings.NewReader(duplicate), map[string]string{"X-Application-ID": memory.TestApplicationID})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("重复解锁方式未被拒绝: %d %s", response.Code, response.Body)
	}

	for _, path := range []string{"/admin/v1/unlock-strategies", "/admin/v1/application-unlock-strategy"} {
		if response := request(t, handler, "GET", path, "", credentials.Token, nil); response.Code != http.StatusNotFound {
			t.Fatalf("旧列表接口仍然可用: %s %d", path, response.Code)
		}
	}
}
