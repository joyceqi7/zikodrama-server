package mysqlstore

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/directplay"
	"drama-server/internal/biz/operations"
	"drama-server/internal/biz/visitor"
)

// TestDirectApplicationRegistrationHTTPIntegration 贯通后台创建、MySQL 配置持久化、兼容更新和小程序启动，不调用真实平台。
func TestDirectApplicationRegistrationHTTPIntegration(t *testing.T) {
	f := newIAAFixture(t)
	r, _ := seedDirect(t, f)
	f.direct = directplay.NewService(r, nil)
	f.signer = &directHTTPPlayback{}
	server := httptest.NewServer(f.handler(t))
	defer server.Close()
	raw := iaaRequest(t, server.URL, "POST", "/admin/v1/session", "", "", map[string]string{"username": "iaa-admin", "password": "LocalIAA123!"}, 200)
	var admin adminrbac.LoginResult
	if err := json.Unmarshal(raw, &admin); err != nil {
		t.Fatal(err)
	}
	input := operations.ApplicationInput{SubjectID: "1",
		Name: "直连创建测试", TikTokAppID: "0007637420375425239060", ClientKey: "direct-registration-client",
		ClientSecret: "test-only-secret", MonetizationMode: application.MonetizationModeIAP,
		PlaybackProvider: " byteplus_web ",
	}
	raw = iaaRequest(t, server.URL, "POST", "/admin/v1/applications", admin.Token, "", input, 201)
	if strings.Contains(string(raw), "playbackProfileKey") {
		t.Fatal("应用响应仍包含已删除的配置引用")
	}
	var created application.Application
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	if created.PlaybackProvider != "byteplus_web" || created.TikTokAppID != input.TikTokAppID {
		t.Fatal("创建响应丢失直连配置或改写字符串应用标识")
	}
	persisted, err := f.store.FindApplication(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.PlaybackProvider != "byteplus_web" || persisted.PlaybackRevision != 1 {
		t.Fatal("直连配置未原子保存至数据库")
	}
	// 客户端省略通道时仅更新普通配置，不能把已创建的直连应用改成官方。
	input.Status, input.ClientSecret = application.StatusActive, ""
	input.PlaybackProvider = ""
	iaaRequest(t, server.URL, "PUT", "/admin/v1/applications/"+created.ID, admin.Token, "", input, 200)
	raw = iaaRequest(t, server.URL, "POST", "/api/v1/bootstrap", "", "", visitor.BootstrapInput{TikTokAppID: created.TikTokAppID, LoginCode: "user-a"}, 200)
	var startup struct {
		PlaybackSDK consumer.PlaybackSDKConfig `json:"playbackSdk"`
	}
	if err := json.Unmarshal(raw, &startup); err != nil {
		t.Fatal(err)
	}
	if startup.PlaybackSDK.Provider != "byteplus_web" || startup.PlaybackSDK.Capabilities == nil || startup.PlaybackSDK.Capabilities.MediaPreload {
		t.Fatal("新建直连应用未进入独立播放器启动流程")
	}
	input.PlaybackProvider = "tiktok_minis"
	iaaRequest(t, server.URL, "PUT", "/admin/v1/applications/"+created.ID, admin.Token, "", input, 409)
	input.PlaybackProvider = "byteplus_web"
	iaaRequest(t, server.URL, "PUT", "/admin/v1/applications/"+created.ID, admin.Token, "", input, 200)
}
