package consumer_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/visitor"
	"drama-server/internal/pkg/ratelimit"
	httpapi "drama-server/internal/service/consumer"
	"drama-server/internal/testsupport/memory"
)

// TestAdErrorAuthenticatedRoute 验证新路由复用 Visitor 鉴权，日志身份只取认证上下文。
func TestAdErrorAuthenticatedRoute(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	store := memory.NewStore(memory.Seed{})
	visitors := newTestVisitorService(t, store)
	identity, err := visitors.Bootstrap(context.Background(), visitor.BootstrapInput{TikTokAppID: "1"})
	if err != nil {
		t.Fatal(err)
	}
	handler := newConsumerHandler(domain.NewService(store, time.Now), visitors, httpapi.Config{RateLimiter: ratelimit.NewMemory(10), AdErrorRequestsPerMinute: 6})
	body := `{"events":[{"id":"ad-auth-example-0001","ad":{"adType":"interstitial","adUnitId":"slot","scene":"home_interstitial","clientVersion":"build","os":"android","tiktokVersion":"unknown","stage":"show","reason":"sdk_error","errorCode":"50000"},"failure":{"error":"SDK failed","errorDocument":"visible","errorNative":"unknown","errorPrerender":false,"shown":false,"userInitiated":false}}]}`
	for _, token := range []string{"", "invalid-token", identity.VisitorToken} {
		logs.Reset()
		request := httptest.NewRequest("POST", "/api/v1/ad-errors", strings.NewReader(body))
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if token != identity.VisitorToken {
			if response.Code != 401 || strings.Contains(logs.String(), "client ad failed") {
				t.Fatal("未授权请求进入广告日志入口")
			}
		} else if response.Code != 200 || !strings.Contains(logs.String(), `"application_id":"1"`) || !strings.Contains(logs.String(), `"user_id":"`+identity.VisitorID+`"`) {
			t.Fatalf("可信身份缺失: %d %s", response.Code, logs.String())
		}
		if strings.Contains(logs.String(), identity.VisitorToken) || strings.Contains(logs.String(), "invalid-token") {
			t.Fatal("鉴权日志泄漏凭据")
		}
	}
}

// adErrorAuthReadStore 记录生产鉴权对应的凭据版本查询。
type adErrorAuthReadStore struct {
	*memory.Store
	reads int
}

// VisitorCredentialVersionRef 按真实鉴权端口读取主体版本，不让入口限流伪造身份。
func (s *adErrorAuthReadStore) VisitorCredentialVersionRef(ctx context.Context, app string, user visitor.UserRef) (uint64, bool, error) {
	s.reads++
	exists, err := s.Store.VisitorExists(ctx, app, user.ID)
	return 1, exists, err
}

// TestAdErrorPreAuthLimit 验证超限请求在鉴权前被拒绝，不再读取凭据存储。
func TestAdErrorPreAuthLimit(t *testing.T) {
	store := &adErrorAuthReadStore{Store: memory.NewStore(memory.Seed{})}
	visitors := newTestVisitorService(t, store)
	identity, err := visitors.Bootstrap(context.Background(), visitor.BootstrapInput{TikTokAppID: "1"})
	if err != nil {
		t.Fatal(err)
	}
	handler := newConsumerHandler(domain.NewService(store, time.Now), visitors, httpapi.Config{RateLimiter: ratelimit.NewMemory(10), AdErrorRequestsPerMinute: 1})
	body := `{"events":[{"id":"review-limit-example-0001","ad":{"adType":"interstitial","adUnitId":"slot","scene":"home_interstitial","clientVersion":"build","os":"android","tiktokVersion":"unknown","stage":"show","reason":"sdk_error","errorCode":"50000"},"failure":{"error":"SDK failed","errorDocument":"visible","errorNative":"unknown","errorPrerender":false,"shown":false,"userInitiated":false}}]}`
	before := store.reads
	for i := 0; i < 4; i++ {
		request := httptest.NewRequest("POST", "/api/v1/ad-errors", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+identity.VisitorToken)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if i == 0 && response.Code != 200 || i > 0 && response.Code != 429 {
			t.Fatalf("限流状态错误: %d", response.Code)
		}
		if i > 0 && response.Header().Get("Retry-After") == "" {
			t.Fatal("超限响应缺少重试窗口")
		}
	}
	t.Logf("1 个请求获准，3 个请求被限流，凭据存储读取次数=%d", store.reads-before)
	if store.reads-before != 1 {
		t.Fatal("日志请求在限流后仍逐次查询凭据存储")
	}
}

// TestAdErrorRenewedCredentialLimit 验证凭据更新不绕过同用户额度，伪造凭据也不能通过完整鉴权。
func TestAdErrorRenewedCredentialLimit(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	store := &adErrorAuthReadStore{Store: memory.NewStore(memory.Seed{})}
	visitors := newTestVisitorService(t, store)
	first, err := visitors.Bootstrap(context.Background(), visitor.BootstrapInput{TikTokAppID: "1"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := visitors.Bootstrap(context.Background(), visitor.BootstrapInput{TikTokAppID: "1", VisitorToken: first.VisitorToken})
	if err != nil {
		t.Fatal(err)
	}
	if first.VisitorID != second.VisitorID || first.VisitorToken == second.VisitorToken {
		t.Fatal("测试必须取得同用户的两个不同有效凭据")
	}
	handler := newConsumerHandler(domain.NewService(store, time.Now), visitors, httpapi.Config{RateLimiter: ratelimit.NewMemory(10), AdErrorRequestsPerMinute: 1})
	body := `{"events":[{"id":"ad-renewed-example-01","ad":{"adType":"interstitial","adUnitId":"slot","scene":"home_interstitial","clientVersion":"build","os":"android","tiktokVersion":"unknown","stage":"show","reason":"sdk_error"},"failure":{"error":"SDK failed","errorDocument":"visible","errorNative":"unknown","errorPrerender":false,"shown":false,"userInitiated":false}}]}`
	before := store.reads
	for i, token := range []string{first.VisitorToken, second.VisitorToken, "invalid-token"} {
		request := httptest.NewRequest("POST", "/api/v1/ad-errors", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != []int{200, 429, 401}[i] {
			t.Fatalf("凭据或用户额度隔离失败: %d", response.Code)
		}
	}
	if store.reads-before != 2 || strings.Count(logs.String(), `"msg":"client ad failed"`) != 1 {
		t.Fatal("获准凭据没有经过完整鉴权，或更换凭据绕过用户额度")
	}
	for _, secret := range []string{first.VisitorToken, second.VisitorToken, "invalid-token"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatal("限流或鉴权日志泄漏凭据")
		}
	}
}
