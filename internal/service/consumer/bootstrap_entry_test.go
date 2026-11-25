package consumer_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/visitor"
	httpapi "drama-server/internal/service/consumer"
	"drama-server/internal/testsupport/memory"
)

// entryIdentityProvider 为隔离测试按登录码返回稳定身份，不访问官方网络。
type entryIdentityProvider struct{}

func (entryIdentityProvider) ExchangeCode(_ context.Context, code string) (visitor.Identity, error) {
	return visitor.Identity{OpenID: "test-openid-" + code}, nil
}

// TestBootstrapHasNoProxyOrInstallationQuota 验证 31/100 位共享代理访客，以及同一客户端连续启动均不受旧入口配额影响。
func TestBootstrapHasNoProxyOrInstallationQuota(t *testing.T) {
	for _, sameVisitor := range []bool{false, true} {
		t.Run(fmt.Sprintf("same_visitor_%t", sameVisitor), func(t *testing.T) {
			store := memory.NewStore(memory.Seed{ApplicationMonetization: []monetization.ApplicationMonetizationConfig{{
				ApplicationID: memory.TestApplicationID, Status: monetization.MonetizationDisabled,
			}}})
			resolver := &mutableIdentityResolver{applicationID: memory.TestApplicationID, provider: entryIdentityProvider{}}
			visitors, err := visitor.NewService(resolver, store, visitor.Config{
				RequireTikTokLogin: true, IdentityTokenEncryptionSecret: "local-test-visitor-identity-protection-key",
			})
			if err != nil {
				t.Fatal(err)
			}
			handler := newConsumerHandler(domain.NewService(store, time.Now), visitors, httpapi.Config{})
			previousToken, previousVisitorID := "", ""
			for index := 1; index <= 100; index++ {
				loginCode := fmt.Sprintf("client-%d", index)
				if sameVisitor {
					loginCode = "same-client"
				}
				payload, _ := json.Marshal(visitor.BootstrapInput{TikTokAppID: "1", LoginCode: loginCode, VisitorToken: previousToken})
				request := httptest.NewRequest(http.MethodPost, "/api/v1/bootstrap", bytes.NewReader(payload))
				// 无论真实地址是否变化，代理连接地址都相同，不再共享应用层计数。
				request.RemoteAddr = "127.0.0.1:30000"
				request.Header.Set("X-Real-IP", fmt.Sprintf("203.0.113.%d", index))
				request.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", index))
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != http.StatusOK || response.Header().Get("retry-after") != "" {
					t.Fatalf("第%d次合法启动被拒绝: status=%d body=%s", index, response.Code, response.Body.String())
				}
				var result struct {
					Data struct{ VisitorID, VisitorToken string }
				}
				if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result.Data.VisitorID == "" || result.Data.VisitorToken == "" {
					t.Fatal("启动未返回业务身份")
				}
				if sameVisitor {
					if previousVisitorID != "" && previousVisitorID != result.Data.VisitorID {
						t.Fatal("同一客户端重复启动更换了业务主体")
					}
					previousToken, previousVisitorID = result.Data.VisitorToken, result.Data.VisitorID
				}
			}
		})
	}
}

// TestBootstrapRejectsRemovedInstallationID 保证已经删除的限流专属参数不会被当作仍受支持的协议。
func TestBootstrapRejectsRemovedInstallationID(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	handler := newConsumerHandler(domain.NewService(store, time.Now), newTestVisitorService(t, store), httpapi.Config{})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/bootstrap", bytes.NewBufferString(`{"tiktokAppId":"1","installationId":"removed-field"}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("废弃字段不应保留兼容分支: %d", response.Code)
	}
}
