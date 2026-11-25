package consumer_test

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
	"time"

	bizapp "drama-server/internal/biz"
	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/visitor"
	httpapi "drama-server/internal/service/consumer"
	"drama-server/internal/testsupport/memory"
)

// mutableIdentityResolver 让持有既有凭据的请求遇到不同应用故障，不替换 HTTP 路由。
type mutableIdentityResolver struct {
	// err 在故障阶段返回稳定领域分类或未预期内部错误。
	err error
	// applicationID 用于模拟应用编码被重新绑定到另一个应用。
	applicationID string
	// provider 只用于模拟服务端官方身份端口。
	provider visitor.IdentityProvider
}

func (resolver *mutableIdentityResolver) Resolve(context.Context, string) (visitor.Application, visitor.IdentityProvider, error) {
	return visitor.Application{SubjectID: "1", ID: resolver.applicationID, TikTokAppID: resolver.applicationID}, resolver.provider, resolver.err
}

// failureIdentityProvider 不接触真实平台，仅测试业务错误是否正确穿透 HTTP。
type failureIdentityProvider struct{ err error }

func (provider failureIdentityProvider) ExchangeCode(context.Context, string) (visitor.Identity, error) {
	return visitor.Identity{}, provider.err
}

// failureVisitorStore 保留真实内存主体，分别模拟读写持久化故障。
type failureVisitorStore struct {
	visitor.Store
	// existsErr 和 createErr 只对相应端口注入故障。
	existsErr, createErr error
}

func (store *failureVisitorStore) VisitorExists(ctx context.Context, appID, visitorID string) (bool, error) {
	if store.existsErr != nil {
		return false, store.existsErr
	}
	return store.Store.VisitorExists(ctx, appID, visitorID)
}

func (store *failureVisitorStore) CreateVisitor(ctx context.Context, appID string, visitorID *string) error {
	if store.createErr != nil {
		return store.createErr
	}
	return store.Store.CreateVisitor(ctx, appID, visitorID)
}

// TestAuthenticatedRequestsClassifyFailures 保证目录、播放和奖励门禁不会把服务故障返回 401。
func TestAuthenticatedRequestsClassifyFailures(t *testing.T) {
	for _, test := range []struct {
		name, code                     string
		status                         int
		resolverErr, storeErr          error
		wrongApplication, invalidToken bool
	}{
		{name: "应用停用", resolverErr: application.ErrUnavailable, status: 403, code: "application_unavailable"},
		{name: "应用越范围", wrongApplication: true, status: 403, code: "application_forbidden"},
		{name: "应用存储故障", resolverErr: visitor.DependencyError(visitor.ErrServiceUnavailable, errors.New("db-secret")), status: 503, code: "service_unavailable"},
		{name: "访客存储故障", storeErr: errors.New("db-secret"), status: 503, code: "service_unavailable"},
		{name: "访客查询超时", storeErr: context.DeadlineExceeded, status: 504, code: "request_timeout"},
		{name: "访客查询取消", storeErr: context.Canceled, status: 408, code: "request_cancelled"},
		{name: "应用查询取消", resolverErr: visitor.DependencyError(visitor.ErrServiceUnavailable, context.Canceled), status: 408, code: "request_cancelled"},
		{name: "未知内部错误", resolverErr: errors.New("internal-secret"), status: 500, code: "internal_error"},
		{name: "真正无效凭证", invalidToken: true, status: 401, code: "unauthorized"},
	} {
		t.Run(test.name, func(t *testing.T) {
			memoryStore := memory.NewStore(memory.Seed{})
			store := &failureVisitorStore{Store: memoryStore}
			resolver := &mutableIdentityResolver{applicationID: "1"}
			visitors := newTestVisitorServiceWithResolver(t, resolver, store)
			created, err := visitors.Bootstrap(context.Background(), visitor.BootstrapInput{TikTokAppID: "1"})
			if err != nil {
				t.Fatal(err)
			}
			resolver.err, store.existsErr = test.resolverErr, test.storeErr
			if test.wrongApplication {
				resolver.applicationID = "other-app"
			}
			if test.invalidToken {
				created.VisitorToken = "invalid-secret"
			}
			handler := newConsumerHandler(domain.NewService(memoryStore, time.Now), visitors, httpapi.Config{})
			for _, route := range []struct{ method, path string }{
				{http.MethodGet, "/api/v1/home"},
				{http.MethodGet, "/api/v1/me/entry-ad"},
				{http.MethodGet, "/api/v1/me/ad-experience"},
				{http.MethodPost, "/api/v1/me/ad-shows"},
				{http.MethodGet, "/api/v1/me/ad-shows?scene=home_entry"},
				{http.MethodPost, "/api/v1/play/resolve"},
				{http.MethodPost, "/api/v1/reward-grants"},
			} {
				request := httptest.NewRequest(route.method, route.path, strings.NewReader(`{}`))
				request.Header.Set("authorization", "Bearer "+created.VisitorToken)
				request.Header.Set("accept-language", "zh")
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				assertIdentityError(t, response, test.status, test.code)
			}
			// 依赖恢复后原凭据仍有效，不要求签发新凭据或创建新用户。
			if test.storeErr != nil {
				store.existsErr = nil
				resolved, err := visitors.Resolve(context.Background(), created.VisitorToken)
				if err != nil || resolved.VisitorID != created.VisitorID {
					t.Fatalf("恢复后旧凭据未保留主体: %v", err)
				}
			}
		})
	}
}

// TestBootstrapClassifiesLoginAndDependencyErrors 确认只有登录码缺失/拒绝属于 bootstrap_failed。
func TestBootstrapClassifiesLoginAndDependencyErrors(t *testing.T) {
	for _, test := range []struct {
		name, code, tiktokAppId, loginCode string
		status                             int
		provider                           visitor.IdentityProvider
		createErr, resolverErr             error
		requireLogin                       bool
	}{
		{name: "缺应用参数", status: 400, code: "invalid_request"},
		{name: "缺静默登录码", tiktokAppId: "1", requireLogin: true, status: 401, code: "bootstrap_failed"},
		{name: "明确登录码拒绝", tiktokAppId: "1", loginCode: "code", requireLogin: true, provider: failureIdentityProvider{visitor.ErrUnauthorized}, status: 401, code: "bootstrap_failed"},
		{name: "缺平台配置", tiktokAppId: "1", loginCode: "code", requireLogin: true, status: 503, code: "identity_unavailable"},
		{name: "平台网络故障", tiktokAppId: "1", loginCode: "code", requireLogin: true, provider: failureIdentityProvider{errors.New("network-secret")}, status: 503, code: "identity_unavailable"},
		{name: "平台调用超时", tiktokAppId: "1", loginCode: "code", requireLogin: true, provider: failureIdentityProvider{context.DeadlineExceeded}, status: 504, code: "request_timeout"},
		{name: "平台调用取消", tiktokAppId: "1", loginCode: "code", requireLogin: true, provider: failureIdentityProvider{context.Canceled}, status: 408, code: "request_cancelled"},
		{name: "创建主体DB故障", tiktokAppId: "1", createErr: errors.New("db-secret"), status: 503, code: "service_unavailable"},
		{name: "创建主体DB超时", tiktokAppId: "1", createErr: context.DeadlineExceeded, status: 504, code: "request_timeout"},
		{name: "应用停用", tiktokAppId: "1", resolverErr: application.ErrUnavailable, status: 403, code: "application_unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			memoryStore := memory.NewStore(memory.Seed{})
			store := &failureVisitorStore{Store: memoryStore, createErr: test.createErr}
			resolver := &mutableIdentityResolver{applicationID: "1", provider: test.provider, err: test.resolverErr}
			visitors, err := visitor.NewService(resolver, store, visitor.Config{
				RequireTikTokLogin: test.requireLogin, IdentityTokenEncryptionSecret: "local-test-visitor-identity-protection-key",
			})
			if err != nil {
				t.Fatal(err)
			}
			handler := newConsumerHandler(domain.NewService(memoryStore, time.Now), visitors, httpapi.Config{})
			body, _ := json.Marshal(visitor.BootstrapInput{TikTokAppID: test.tiktokAppId, LoginCode: test.loginCode})
			request := httptest.NewRequest(http.MethodPost, "/api/v1/bootstrap", bytes.NewReader(body))
			request.Header.Set("accept-language", "zh")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			assertIdentityError(t, response, test.status, test.code)
		})
	}
}

// failingApplicationRepository 验证实际业务应用解析器不会吞掉数据库故障的类别。
type failingApplicationRepository struct{ err error }

// FindApplicationByTikTokAppID 模拟应用仓储故障，保留依赖错误供分类和日志断言。
func (repository failingApplicationRepository) FindApplicationByTikTokAppID(context.Context, string) (application.Application, error) {
	return application.Application{}, repository.err
}

func TestApplicationResolverMarksPersistenceFailures(t *testing.T) {
	var logs bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(original) })
	for _, cause := range []error{errors.New("db-secret"), context.DeadlineExceeded, context.Canceled} {
		logs.Reset()
		resolver, err := bizapp.NewApplicationResolver(failingApplicationRepository{cause}, func(application.Application) visitor.IdentityProvider { return nil })
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = resolver.Resolve(context.Background(), "1")
		if !errors.Is(err, visitor.ErrServiceUnavailable) || !errors.Is(err, cause) || strings.Contains(err.Error(), "db-secret") {
			t.Fatalf("数据库原因被隐藏、误分类或泄露: %v", err)
		}
		if !strings.Contains(logs.String(), "find application by tiktok app id failed") || !strings.Contains(logs.String(), cause.Error()) {
			t.Fatal("应用查询故障缺少操作说明或实际原因")
		}
	}
}

// assertIdentityError 同时验证稳定协议与友好文案，不让 Go 异常或秘密字段进入响应。
func assertIdentityError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	var body struct {
		Reason, Message string
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != status || body.Reason != code || body.Message == "" || body.Message == code || strings.Contains(response.Body.String(), "secret") {
		t.Fatalf("错误响应不符合合同: status=%d body=%s", response.Code, response.Body.String())
	}
}
