package mysqlstore

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
)

// preAuthorizationProvider 只确认本地测试授权，不访问真实平台。
type preAuthorizationProvider struct {
	syncDistributionFixtureProvider
	authorized, revoked int
}

// AuthorizeAlbum 记录测试授权次数，不向平台发送请求。
func (p *preAuthorizationProvider) AuthorizeAlbum(context.Context, string, []string) error {
	p.authorized++
	return nil
}

// RevokeAlbum 记录测试撤权次数，不向平台发送请求。
func (p *preAuthorizationProvider) RevokeAlbum(context.Context, string, []string) error {
	p.revoked++
	return nil
}

// TestDirectApplicationPreAuthorizationIntegration 验证预授权与播放器切换互不覆盖。
func TestDirectApplicationPreAuthorizationIntegration(t *testing.T) {
	f := newIAAFixture(t)
	defer f.store.data.pool.Close()
	ctx := application.WithSubject(context.Background(), "1")
	drama, err := f.store.FindDrama(ctx, "201")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	model, err := newTikTokPublicationModel(operations.TikTokPublication{SubjectID: "1", DramaID: "201", OwnerApplicationID: drama.Published.OwnerApplicationID, PlatformAlbumID: drama.PlatformAlbumID, Status: "online", Stage: "done", CreatedAt: now, UpdatedAt: now, NextAttemptAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.data.DB().Create(&model).Error; err != nil {
		t.Fatal(err)
	}
	target := application.Application{SubjectID: "1", TikTokAppID: "pre-authorize-direct", ClientKey: "pre-authorize-key", Name: "可切换的独立应用", Status: application.StatusActive, MonetizationMode: application.MonetizationModeIAP, PlaybackProvider: "byteplus_web", TimeZone: "UTC", CreatedAt: now, UpdatedAt: now}
	if err := f.store.CreateApplication(ctx, &target); err != nil {
		t.Fatal(err)
	}
	service := operations.NewService(operations.Repositories{
		Application: operations.ApplicationPorts{Subjects: f.store, Registry: f.store},
		Content:     operations.ContentPorts{OwnerApplication: f.store.FindApplication, Distributions: f.store, Catalog: f.store, Publications: f.store},
		Reporting:   operations.ReportingPorts{Audits: f.store},
	}, nil)
	provider := &preAuthorizationProvider{}
	if err := service.ConfigureTikTokPublications(func(application.Application) operations.TikTokMediaProvider { return provider }, operations.PublicationJobConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := service.ReconcileDistributions(ctx); err != nil {
		t.Fatal(err)
	}
	if provider.authorized != 0 {
		t.Fatal("新独立应用未经选择即自动授权")
	}
	// 兼容队列写入和对账路径，同步入口也允许独立应用预授权。
	if err := f.store.SetDistribution(ctx, "201", target.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := service.ReconcileDistributions(ctx); err != nil {
		t.Fatal(err)
	}
	if provider.authorized != 1 {
		t.Fatal("独立应用授权未实际执行", provider.authorized)
	}
	tracked, err := service.ApplicationDramaAuthorizations(ctx, target.ID, operations.ApplicationDramaAuthorizationQuery{View: "tracked", Page: 1, PageSize: 20})
	if err != nil || tracked.Total != 1 {
		t.Fatal("独立应用授权明细不可读", tracked, err)
	}
	directCtx := application.ContextForApplication(ctx, target)
	page, err := f.store.DiscoverDramas(directCtx, consumer.DiscoveryQuery{Limit: 20})
	if err != nil || len(page.Items) != 0 {
		t.Fatal("官方预授权污染直连目录", page, err)
	}
	// 切到官方后复用已确认授权；切回直连不会自动撤权。
	target, err = f.store.FindApplication(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	target.PlaybackProvider = "tiktok_minis"
	if err := f.store.SaveApplication(ctx, target); err != nil {
		t.Fatal(err)
	}
	page, err = f.store.DiscoverDramas(application.ContextForApplication(ctx, target), consumer.DiscoveryQuery{Limit: 20})
	if err != nil || len(page.Items) != 1 {
		t.Fatal("切换后无法复用预授权", page, err)
	}
	target, err = f.store.FindApplication(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	target.PlaybackProvider = "byteplus_web"
	if err := f.store.SaveApplication(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err := service.ReconcileDistributions(ctx); err != nil {
		t.Fatal(err)
	}
	if provider.revoked != 0 || provider.authorized != 1 {
		t.Fatal("切换播放器改变已有授权")
	}
	if err := service.SetDramaDistribution(ctx, "201", target.ID, false); err != nil {
		t.Fatal(err)
	}
	if provider.revoked != 1 {
		t.Fatal("独立应用不能撤销官方授权")
	}
	// 分发 HTTP 入口会带入目标应用的播放器上下文，仍须读取并更新官方上线快照。
	f.operations = service
	server := httptest.NewServer(f.handler(t))
	defer server.Close()
	var admin adminrbac.LoginResult
	login := iaaRequest(t, server.URL, http.MethodPost, "/admin/v1/session", "", "", map[string]string{"username": "iaa-admin", "password": "LocalIAA123!"}, 200)
	if err := json.Unmarshal(login, &admin); err != nil {
		t.Fatal(err)
	}
	iaaRequest(t, server.URL, http.MethodPut, "/admin/v1/dramas/201/distributions/"+target.ID, admin.Token, "", map[string]bool{"enabled": true}, 200)
	drama, err = f.store.FindDrama(ctx, "201")
	if err != nil || drama.Published == nil || !slices.Contains(drama.Published.AuthorizedApplicationIDs, target.ID) {
		t.Fatal("直连应用的 HTTP 预授权未写入官方快照", err)
	}
	count, err := service.AuthorizeApplicationDramas(ctx, target.ID, []string{"201"})
	if err != nil || count != 1 || provider.authorized != 2 {
		t.Fatal("独立应用的批量预授权未复用已确认授权", count, err)
	}
}
