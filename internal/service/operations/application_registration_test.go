package operations_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/operations"
	operationshttp "drama-server/internal/service/operations"
	"drama-server/internal/testsupport/memory"
)

// TestApplicationRegistrationRequiresIdentityNameAndMode 覆盖完整分发身份创建、缺失 Client Key 拒绝、独立广告位维护及身份不可变的 HTTP 流程。
func TestApplicationRegistrationRequiresIdentityNameAndMode(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	handler := operationshttp.NewHandler(operations.NewService(memory.OperationsRepositories(store), nil), newRBACService(t, store), operationshttp.Config{})
	login := request(t, handler, http.MethodPost, "/admin/v1/session", "application/json", "", strings.NewReader(`{"username":"admin","password":"local-admin-password"}`))
	var credentials struct {
		Token string `json:"token"`
	}
	decodeData(t, login, &credentials)

	created := request(t, handler, http.MethodPost, "/admin/v1/applications", "application/json", credentials.Token, strings.NewReader(`{"subjectId":"1","clientSecret":"test-application-secret","monetizationMode":"IAA","tiktokAppId":" 7637420375425239060 ","name":" First Drama ","clientKey":" first-client-key "}`))
	if created.Code != http.StatusCreated {
		t.Fatalf("应用创建失败：%d %s", created.Code, created.Body.String())
	}
	if strings.Contains(created.Body.String(), "test-application-secret") || strings.Contains(created.Body.String(), `"clientSecret":`) {
		t.Fatal("响应泄漏密钥")
	}
	var first application.Application
	decodeData(t, created, &first)
	if first.TikTokAppID == "" || first.TikTokAppID != "7637420375425239060" || first.Name != "First Drama" ||
		!first.ClientSecretConfigured || first.Status != application.StatusDisabled || first.RegionCode != "GLOBAL" || first.TimeZone != "UTC" || first.ClientKey != "first-client-key" || first.ComplianceBaseURL != "" {
		t.Fatalf("创建默认值不符合预期：%+v", first)
	}
	secondResponse := request(t, handler, http.MethodPost, "/admin/v1/applications", "application/json", credentials.Token, strings.NewReader(`{"subjectId":"1","clientSecret":"test-application-secret","monetizationMode":"IAA","tiktokAppId":"7637420375425239061","name":"Second Drama","clientKey":"second-client-key"}`))
	if secondResponse.Code != http.StatusCreated {
		t.Fatalf("完整分发身份的应用应该可以共存：%d %s", secondResponse.Code, secondResponse.Body.String())
	}
	var second application.Application
	decodeData(t, secondResponse, &second)
	if first.ID == "" || second.ID == "" || first.ID == second.ID {
		t.Fatal("服务端生成的内部应用主键必须非空且唯一")
	}
	for _, secret := range []string{"", "   ", strings.Repeat("a", 256), "bad\nsecret"} {
		body, _ := json.Marshal(map[string]string{"name": "Missing secret", "subjectId": "1", "tiktokAppId": "missing-secret", "clientKey": "another-key", "monetizationMode": "IAA", "clientSecret": secret})
		response := request(t, handler, http.MethodPost, "/admin/v1/applications", "application/json", credentials.Token, bytes.NewReader(body))
		if response.Code != http.StatusBadRequest {
			t.Fatal("未拒绝无效密钥")
		}
	}
	for _, invalid := range []struct {
		body string
		want int
	}{
		{`{"subjectId":"1","clientSecret":"test-application-secret","monetizationMode":"IAA","tiktokAppId":"duplicate-client","name":"Duplicate key","clientKey":"first-client-key"}`, http.StatusConflict},
		{`{"subjectId":"1","clientSecret":"test-application-secret","monetizationMode":"IAA","tiktokAppId":"missing-key","name":"Missing key"}`, http.StatusBadRequest},
		{`{"subjectId":"1","clientSecret":"test-application-secret","monetizationMode":"IAA","tiktokAppId":"blank-key","name":"Blank key","clientKey":"   "}`, http.StatusBadRequest},
		{`{"subjectId":"1","clientSecret":"test-application-secret","monetizationMode":"IAA","tiktokAppId":"7637420375425239060","name":"Duplicate","clientKey":"duplicate-key"}`, http.StatusConflict},
		{`{"subjectId":"1","clientSecret":"test-application-secret","monetizationMode":"IAA","tiktokAppId":"7637420375425239062","name":" ","clientKey":"validation-key"}`, http.StatusBadRequest},
		{`{"subjectId":"1","clientSecret":"test-application-secret","monetizationMode":"IAA","tiktokAppId":"invalid!","name":"Invalid ID","clientKey":"validation-key"}`, http.StatusBadRequest},
		{`{"subjectId":"1","clientSecret":"test-application-secret","monetizationMode":"IAA","tiktokAppId":"7637420375425239062","name":"Legacy code","clientKey":"validation-key","code":"old-code"}`, http.StatusBadRequest},
		{`{"subjectId":"1","clientSecret":"test-application-secret","monetizationMode":"IAA","name":"Missing ID","clientKey":"validation-key"}`, http.StatusBadRequest},
		{`{"subjectId":"1","clientSecret":"test-application-secret","monetizationMode":"IAA","tiktokAppId":"7637420375425239062","name":"Active without credentials","status":"active"}`, http.StatusBadRequest},
		{`{"subjectId":"1","clientSecret":"test-application-secret","monetizationMode":"IAA","tiktokAppId":"7637420375425239062","name":"Invalid legacy URL","clientKey":"validation-key","complianceBaseUrl":"http://legal.example.test"}`, http.StatusBadRequest},
	} {
		response := request(t, handler, http.MethodPost, "/admin/v1/applications", "application/json", credentials.Token, strings.NewReader(invalid.body))
		if response.Code != invalid.want {
			t.Errorf("创建校验结果错误：want %d, got %d", invalid.want, response.Code)
		}
	}

	update := func(id string, input operations.ApplicationInput, want int) {
		t.Helper()
		body, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		response := request(t, handler, http.MethodPut, "/admin/v1/applications/"+id, "application/json", credentials.Token, bytes.NewReader(body))
		if response.Code != want {
			t.Fatalf("更新应用失败：want %d, got %d %s", want, response.Code, response.Body.String())
		}
	}
	input := operations.ApplicationInput{SubjectID: "1", MonetizationMode: "IAA", Name: first.Name, TikTokAppID: first.TikTokAppID, ClientKey: first.ClientKey, Status: application.StatusDisabled,
		AdUnits: application.AdUnits{RewardedVideo: []string{"episode-reward"}, Interstitial: []string{"launch-ad"}}}
	// 新建不要求广告位，创建后独立补齐再启用；完整更新保留不可变的分发身份。
	update(first.ID, input, http.StatusOK)
	kept, err := store.FindApplication(t.Context(), first.ID)
	if err != nil || kept.ClientSecret != "test-application-secret" {
		t.Fatal("更新广告位清空了密钥", err)
	}
	input.ClientSecret = "rotated-test-secret"
	update(first.ID, input, http.StatusOK)
	input.ClientSecret = ""
	input.Status = application.StatusActive
	update(first.ID, input, http.StatusOK)

	// 首次保存后保持平台身份不可变，也不能通过清空 Client Key 解除绑定。
	changed := input
	changed.ClientKey = "replacement-key"
	update(first.ID, changed, http.StatusConflict)
	changed.ClientKey, changed.Status = "", application.StatusDisabled
	update(first.ID, changed, http.StatusConflict)
	changed = input
	changed.TikTokAppID = "7637420375425239069"
	update(first.ID, changed, http.StatusConflict)
	secondInput := operations.ApplicationInput{SubjectID: "1", MonetizationMode: "IAA", Name: second.Name, TikTokAppID: second.TikTokAppID, Status: application.StatusDisabled, ClientKey: input.ClientKey}
	update(second.ID, secondInput, http.StatusConflict)

	loaded, err := store.FindApplication(t.Context(), first.ID)
	if err != nil || loaded.ClientSecret != "rotated-test-secret" || loaded.ClientKey != input.ClientKey || loaded.TikTokAppID != first.TikTokAppID || len(loaded.AdUnits.RewardedVideo) != 1 {
		t.Fatalf("后续冲突不能覆盖已保存的身份及广告位：%+v %v", loaded, err)
	}
}
