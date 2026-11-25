package visitor_test

import (
	"context"
	"errors"
	"testing"

	"drama-server/internal/biz/campaign"
	"drama-server/internal/biz/visitor"
	"drama-server/internal/testsupport/memory"
)

type identityProvider struct {
	identity visitor.Identity
	err      error
}

// visitorStoreWithExistsError 只覆盖 Visitor 存在性查询，用于确认存储故障不会被误判为
// 客户端丢失凭据并静默创建新 Visitor。
type visitorStoreWithExistsError struct {
	visitor.Store
	err error
}

func (store visitorStoreWithExistsError) VisitorExists(context.Context, string, string) (bool, error) {
	return false, store.err
}

type campaignVisitorStore struct {
	visitor.Store
	campaigns map[string]campaign.Attribution
}

func (store campaignVisitorStore) ResolveCampaign(
	_ context.Context,
	applicationID string,
	code string,
) (campaign.Attribution, bool, error) {
	campaign, found := store.campaigns[applicationID+":"+code]
	return campaign, found, nil
}

func (provider identityProvider) ExchangeCode(context.Context, string) (visitor.Identity, error) {
	return provider.identity, provider.err
}

type applicationResolver struct {
	applications map[string]visitor.Application
	provider     visitor.IdentityProvider
}

func (resolver applicationResolver) Resolve(_ context.Context, code string) (visitor.Application, visitor.IdentityProvider, error) {
	application, exists := resolver.applications[code]
	if !exists {
		return visitor.Application{}, nil, visitor.ErrUnauthorized
	}
	return application, resolver.provider, nil
}

func TestAnonymousBootstrapIssuesPersistentVisitorCredentialWithoutLogin(t *testing.T) {
	service, store := newVisitorService(t, nil, "visitor-anonymous")

	created, err := service.Bootstrap(context.Background(), visitor.BootstrapInput{TikTokAppID: "1"})
	if err != nil {
		t.Fatal(err)
	}
	if created.VisitorID == "" || created.VisitorToken == "" ||
		created.IdentityState != visitor.IdentityStateAnonymous {
		t.Fatalf("匿名启动结果错误：%+v", created)
	}
	if exists, err := store.VisitorExists(context.Background(), "1", created.VisitorID); err != nil || !exists {
		t.Fatalf("匿名 Visitor 未持久化：exists=%v err=%v", exists, err)
	}
	resolved, err := service.Resolve(context.Background(), created.VisitorToken)
	if err != nil || resolved.VisitorID != created.VisitorID {
		t.Fatalf("VisitorToken 不能恢复同一消费者：context=%+v err=%v", resolved, err)
	}

	continued, err := service.Bootstrap(context.Background(), visitor.BootstrapInput{
		TikTokAppID: "1", VisitorToken: created.VisitorToken,
	})
	if err != nil || continued.VisitorID != created.VisitorID {
		t.Fatalf("再次启动没有继续同一 Visitor：context=%+v err=%v", continued, err)
	}
}

func TestOptionalTikTokLoginFailureDoesNotBlockAnonymousAccess(t *testing.T) {
	service, _ := newVisitorService(t, identityProvider{err: errors.New("oauth unavailable")}, "visitor-fallback")

	created, err := service.Bootstrap(context.Background(), visitor.BootstrapInput{
		TikTokAppID: "1", LoginCode: "optional-login-code",
	})
	if err != nil {
		t.Fatalf("可选 TikTok 登录失败不应阻断启动：%v", err)
	}
	if created.IdentityState != visitor.IdentityStateAnonymous || created.VisitorID == "" {
		t.Fatalf("登录失败后未保持匿名 Visitor：%+v", created)
	}
}

func TestTikTokLoginFindsExistingAccountWithoutMergingAnonymousUser(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	provider := identityProvider{identity: visitor.Identity{
		OpenID: "open-id",
	}}
	first := newVisitorServiceWithStore(t, store, provider, "tiktok-visitor")
	authenticated, err := first.Bootstrap(context.Background(), visitor.BootstrapInput{
		TikTokAppID: "1", LoginCode: "first-login",
	})
	if err != nil {
		t.Fatal(err)
	}
	if authenticated.IdentityState != visitor.IdentityStateTikTokAuthenticated {
		t.Fatalf("TikTok 身份未绑定：%+v", authenticated)
	}

	second := newVisitorServiceWithStore(t, store, provider, "anonymous-before-login")
	anonymous, err := second.Bootstrap(context.Background(), visitor.BootstrapInput{TikTokAppID: "1"})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := second.Bootstrap(context.Background(), visitor.BootstrapInput{
		TikTokAppID: "1", VisitorToken: anonymous.VisitorToken, LoginCode: "repeat-login",
	})
	if err != nil {
		t.Fatal(err)
	}
	if bound.VisitorID != authenticated.VisitorID || bound.IdentityState != visitor.IdentityStateTikTokAuthenticated {
		t.Fatalf("同一 OpenID 没有解析到同一 Visitor：first=%+v second=%+v", authenticated, bound)
	}
	if restored, err := second.Resolve(context.Background(), anonymous.VisitorToken); err != nil || restored.VisitorID != anonymous.VisitorID {
		t.Fatalf("独立匿名测试用户不应被合并或删除：%v", err)
	}
	if bound.VisitorID == anonymous.VisitorID {
		t.Fatal("匿名账号被改绑为正式账号")
	}
	if bound.EventOpenID != "open-id" {
		t.Fatal("未返回本次官方事件所需 OpenID")
	}
}

func TestVisitorCredentialRejectsTamperingAndApplicationReuse(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	resolver := applicationResolver{applications: map[string]visitor.Application{
		"1":     {ID: "1", TikTokAppID: "1"},
		"other": {ID: "other", TikTokAppID: "other"},
	}}
	service, err := visitor.NewService(resolver, store, visitor.Config{
		IdentityTokenEncryptionSecret: "local-test-visitor-identity-protection-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Bootstrap(context.Background(), visitor.BootstrapInput{TikTokAppID: "1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Resolve(context.Background(), created.VisitorToken+"tampered"); !errors.Is(err, visitor.ErrUnauthorized) {
		t.Fatalf("篡改凭据未被拒绝：%v", err)
	}
	other, err := service.Bootstrap(context.Background(), visitor.BootstrapInput{
		TikTokAppID: "other", VisitorToken: created.VisitorToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	if other.VisitorID == "" || other.VisitorID == created.VisitorID {
		t.Fatalf("跨应用 VisitorToken 不应被复用：%+v", other)
	}
}

func TestBootstrapDoesNotHideVisitorStoreFailure(t *testing.T) {
	service, store := newVisitorService(t, nil, "visitor-existing")
	created, err := service.Bootstrap(context.Background(), visitor.BootstrapInput{TikTokAppID: "1"})
	if err != nil {
		t.Fatal(err)
	}

	storeFailure := errors.New("visitor store unavailable")
	failingService, err := visitor.NewService(applicationResolver{
		applications: map[string]visitor.Application{
			"1": {ID: "1", TikTokAppID: "1", Name: "Drama Minis", TimeZone: "UTC"},
		},
	}, visitorStoreWithExistsError{Store: store, err: storeFailure}, visitor.Config{
		IdentityTokenEncryptionSecret: "local-test-visitor-identity-protection-key",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = failingService.Bootstrap(context.Background(), visitor.BootstrapInput{
		TikTokAppID: "1", VisitorToken: created.VisitorToken,
	})
	if !errors.Is(err, storeFailure) {
		t.Fatalf("Visitor 存储故障被误判为无效凭据：%v", err)
	}
	if exists, lookupErr := store.VisitorExists(context.Background(), "1", "must-not-be-created"); lookupErr != nil || exists {
		t.Fatalf("存储故障时仍创建了新 Visitor：exists=%v err=%v", exists, lookupErr)
	}
}

func TestBootstrapClearsCampaignFromLaterOrganicLaunch(t *testing.T) {
	store := campaignVisitorStore{
		Store: memory.NewStore(memory.Seed{}),
		campaigns: map[string]campaign.Attribution{
			"1:paid-entry": {ID: "5006", Code: "paid-entry"},
		},
	}
	service, err := visitor.NewService(applicationResolver{
		applications: map[string]visitor.Application{
			"1": {ID: "1", TikTokAppID: "1", Name: "Drama Minis", TimeZone: "UTC"},
		},
	}, store, visitor.Config{
		IdentityTokenEncryptionSecret: "local-test-visitor-identity-protection-key",
	})
	if err != nil {
		t.Fatal(err)
	}

	paid, err := service.Bootstrap(context.Background(), visitor.BootstrapInput{
		TikTokAppID: "1", LaunchQuery: map[string]string{"campaignCode": "paid-entry"},
	})
	if err != nil || paid.Campaign == nil || paid.Campaign.ID != "5006" {
		t.Fatalf("投放启动没有保存当前归因：context=%+v err=%v", paid, err)
	}
	organic, err := service.Bootstrap(context.Background(), visitor.BootstrapInput{
		TikTokAppID: "1", VisitorToken: paid.VisitorToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	if organic.Campaign != nil {
		t.Fatalf("自然启动错误继承了历史投放归因：%+v", organic.Campaign)
	}
}

func newVisitorService(t *testing.T, provider visitor.IdentityProvider, visitorID string) (*visitor.Service, *memory.Store) {
	t.Helper()
	store := memory.NewStore(memory.Seed{})
	return newVisitorServiceWithStore(t, store, provider, visitorID), store
}

func newVisitorServiceWithStore(
	t *testing.T,
	store *memory.Store,
	provider visitor.IdentityProvider,
	visitorID string,
) *visitor.Service {
	t.Helper()
	service, err := visitor.NewService(applicationResolver{
		applications: map[string]visitor.Application{
			"1": {ID: "1", TikTokAppID: "1", Name: "Drama Minis", TimeZone: "UTC"},
		},
		provider: provider,
	}, store, visitor.Config{
		IdentityTokenEncryptionSecret: "local-test-visitor-identity-protection-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}
