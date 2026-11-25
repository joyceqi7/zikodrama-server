package data

import (
	"net/http"
	"testing"

	domainapplication "drama-server/internal/biz/application"
)

// TestTikTokMediaClientPoolReusesClientAndRotatesCredentials 验证同一应用复用 OAuth
// Token 缓存，而凭据轮换后不会继续使用旧客户端。
func TestTikTokMediaClientPoolReusesClientAndRotatesCredentials(t *testing.T) {
	pool := &TikTokMediaClientPool{
		httpClient: http.DefaultClient,
		clients:    make(map[string]tikTokMediaClientEntry),
	}
	application := domainapplication.Application{SubjectID: "1",
		TikTokAppID: "application-1", ClientKey: "client-key", ClientSecret: "secret-one",
	}

	first := pool.ClientForApplication(application)
	second := pool.ClientForApplication(application)
	if first == nil || first != second {
		t.Fatal("同一应用凭据必须复用 TikTok 媒资客户端")
	}

	application.ClientSecret = "secret-two"
	rotated := pool.ClientForApplication(application)
	if rotated == nil || rotated == first {
		t.Fatal("应用凭据轮换后必须重建 TikTok 媒资客户端")
	}
}

// TestTikTokMediaClientPoolRejectsIncompleteCredentials 确保未配置官方凭据时安全阻断外部调用。
func TestTikTokMediaClientPoolRejectsIncompleteCredentials(t *testing.T) {
	pool := &TikTokMediaClientPool{
		httpClient: http.DefaultClient,
		clients:    make(map[string]tikTokMediaClientEntry),
	}
	if client := pool.ClientForApplication(domainapplication.Application{SubjectID: "1", TikTokAppID: "application-1"}); client != nil {
		t.Fatal("凭据不完整时不得创建 TikTok 媒资客户端")
	}
}

// TestApplicationClientSecretUsesFileMapping 验证历史引用只从配置映射解析，且应用密钥优先。
func TestApplicationClientSecretUsesFileMapping(t *testing.T) {
	t.Setenv("HISTORICAL_TIKTOK_SECRET", "ignored-environment-secret")
	app := domainapplication.Application{SubjectID: "1", ClientKey: "client-key", SecretRef: "HISTORICAL_TIKTOK_SECRET"}
	if secret := applicationClientSecret(app, nil); secret != "" {
		t.Fatal("缺少配置映射时不应读取环境变量")
	}
	secrets := map[string]string{"HISTORICAL_TIKTOK_SECRET": "file-secret"}
	if secret := applicationClientSecret(domainapplication.Application{}, map[string]string{"": "unexpected"}); secret != "" {
		t.Fatal("空引用不能匹配配置中的空键")
	}
	if secret := applicationClientSecret(app, secrets); secret != "file-secret" {
		t.Fatal("历史引用未使用配置文件映射")
	}
	app.ClientSecret = "application-secret"
	if secret := applicationClientSecret(app, secrets); secret != "application-secret" {
		t.Fatal("应用保存的密钥应优先于历史引用")
	}
}
