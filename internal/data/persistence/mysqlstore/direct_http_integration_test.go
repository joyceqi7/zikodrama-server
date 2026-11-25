package mysqlstore

import (
	"context"
	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/directplay"
	"drama-server/internal/biz/homepage"
	"drama-server/internal/biz/visitor"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// directHTTPPlayback 只替换云签名，身份、发布、权益和 HTTP 合同仍走真实代码。
type directHTTPPlayback struct{ calls int }

// Configuration 返回基础版公开字段。
func (p *directHTTPPlayback) Configuration(context.Context) (consumer.PlaybackSDKConfig, error) {
	return consumer.PlaybackSDKConfig{Provider: "byteplus_web", Channel: "byteplus", Capabilities: &consumer.PlaybackCapabilities{MediaPreload: false}}, nil
}

// Sign 不调用真实平台，不在测试日志输出凭据。
func (p *directHTTPPlayback) Sign(context.Context, consumer.Episode) (string, time.Time, error) {
	p.calls++
	return "test-only-direct-play-auth", time.Now().Add(time.Minute), nil
}

// TestDirectHTTPIntegration 贯通真实认证、发布任务、固定应用解析及精确权限，不修改消费者路径。
func TestDirectHTTPIntegration(t *testing.T) {
	f := newIAAFixture(t)
	r, ctx := seedDirect(t, f)
	bg := application.WithSubject(context.Background(), "1")
	f.direct = directplay.NewService(r, nil)
	signer := &directHTTPPlayback{}
	f.signer = signer
	server := httptest.NewServer(f.handler(t))
	defer server.Close()
	raw := iaaRequest(t, server.URL, "POST", "/admin/v1/session", "", "", map[string]string{"username": "iaa-admin", "password": "LocalIAA123!"}, 200)
	var admin adminrbac.LoginResult
	if err := json.Unmarshal(raw, &admin); err != nil {
		t.Fatal(err)
	}
	base := "/admin/v1/direct/dramas/201/publication"
	raw = iaaRequest(t, server.URL, "GET", base, admin.Token, "", nil, 200)
	var w directplay.Workbench
	if err := json.Unmarshal(raw, &w); err != nil {
		t.Fatal(err)
	}
	raw = iaaRequest(t, server.URL, "GET", "/admin/v1/direct/dramas?status=pending", admin.Token, "", nil, 200)
	var pendingRows []directplay.Workbench
	if err := json.Unmarshal(raw, &pendingRows); err != nil || len(pendingRows) != 1 {
		t.Fatal("草稿不能直接进入直连发布列表", err)
	}
	_, err := f.store.PrepareDrama(bg, "201", "operator")
	if err != nil {
		t.Fatal(err)
	}
	raw = iaaRequest(t, server.URL, "GET", "/admin/v1/direct/dramas?status=pending", admin.Token, "", nil, 200)
	if err := json.Unmarshal(raw, &pendingRows); err != nil || len(pendingRows) != 1 {
		t.Fatal("就绪内容没有进入待上线列表", err)
	}
	command := directplay.Command{RequestID: "http-direct-publish", ExpectedRevision: w.Publication.Revision, Kind: "publish"}
	raw = iaaRequest(t, server.URL, "POST", base+"/operations", admin.Token, "", command, 200)
	var operation directplay.Operation
	if err := json.Unmarshal(raw, &operation); err != nil {
		t.Fatal(err)
	}
	iaaRequest(t, server.URL, "GET", "/admin/v1/direct/publication-operations/"+operation.ID, admin.Token, "", nil, 200)
	raw = iaaRequest(t, server.URL, "POST", "/api/v1/bootstrap", "", "", visitor.BootstrapInput{TikTokAppID: "102", LoginCode: "user-a"}, 200)
	var startup struct {
		VisitorToken string                     `json:"visitorToken"`
		PlaybackSDK  consumer.PlaybackSDKConfig `json:"playbackSdk"`
	}
	if err := json.Unmarshal(raw, &startup); err != nil || startup.PlaybackSDK.Provider != "byteplus_web" || startup.PlaybackSDK.Capabilities.MediaPreload {
		t.Fatal("启动合同错误", err)
	}
	raw = iaaRequest(t, server.URL, "POST", "/api/v1/play/resolve", startup.VisitorToken, "", map[string]string{"dramaId": "201", "episodeId": "301"}, 200)
	var play consumer.PlayResult
	if err := json.Unmarshal(raw, &play); err != nil || play.Status != "playable" || play.Playback == nil || play.Playback.PlayAuthToken == "" || play.Playback.AlbumID != "" {
		t.Fatal("直连播放合同错误", err)
	}
	before := signer.calls
	raw = iaaRequest(t, server.URL, "POST", "/api/v1/play/resolve", startup.VisitorToken, "", map[string]string{"dramaId": "201", "episodeId": "302"}, 200)
	if err := json.Unmarshal(raw, &play); err != nil || play.Status != "access_required" || play.Preload != nil || signer.calls != before {
		t.Fatal("锁定集签发凭据", err)
	}
	official, _ := iaaLogin(t, server.URL, "101", "user-b")
	raw = iaaRequest(t, server.URL, "POST", "/api/v1/play/resolve", official, "", map[string]string{"dramaId": "201", "episodeId": "301"}, 200)
	var legacy map[string]any
	if json.Unmarshal(raw, &legacy) != nil {
		t.Fatal("旧结果解析失败")
	}
	descriptor := legacy["playback"].(map[string]any)
	if descriptor["albumId"] != "fixture-album" || descriptor["playAuthToken"] != nil {
		t.Fatal("旧官方描述符被修改")
	}
	// 公开首页使用应用注册表的 provider，既无新 Header 也不创建身份。
	iaaRequest(t, server.URL, "GET", "/api/v1/open/catalog?tiktokAppId=102", "", "", nil, 200)
	home := homepage.NewService(f.store, f.store, f.store)
	management := homepage.WithManagementContext(ctx)
	if _, err := home.SaveCarousel(management, homepage.Carousel{DramaIDs: []string{"201"}, Items: []homepage.CarouselItem{{DramaID: "201", Visible: false}}}); err != nil {
		t.Fatal(err)
	}
	iaaRequest(t, server.URL, "POST", base+"/operations", admin.Token, "", map[string]any{"kind": "publish", "requestId": "invalid key", "expectedRevision": 0, "applicationIds": []string{"102"}}, 400)
	// 删除的管理功能不保留可写后门，已登录管理员也只能获得 404。
	for _, endpoint := range []struct{ method, path string }{
		{http.MethodGet, "/admin/v1/direct/releases"},
		{http.MethodGet, "/admin/v1/direct/home-preview?applicationId=102"},
		{http.MethodPost, "/admin/v1/direct/home-config-copy/preview"},
		{http.MethodPost, "/admin/v1/direct/home-config-copy"},
	} {
		removed := httptest.NewRecorder()
		request := httptest.NewRequest(endpoint.method, endpoint.path, nil)
		request.Header.Set("Authorization", "Bearer "+admin.Token)
		f.handler(t).ServeHTTP(removed, request)
		if removed.Code != http.StatusNotFound {
			t.Fatalf("废弃管理接口仍可访问: %s %s status=%d", endpoint.method, endpoint.path, removed.Code)
		}
	}
	raw = iaaRequest(t, server.URL, "GET", "/api/v1/open/home/carousel?tiktokAppId=102", "", "", nil, 200)
	var visible []consumer.DramaSummary
	if err := json.Unmarshal(raw, &visible); err != nil || len(visible) != 0 {
		t.Fatal("移除管理预览后隐藏轮播影响消费者合同", err)
	}
	// 引用修改路由已删除，不能继续暴露按应用选择全局播放配置的入口。
	removed := httptest.NewRecorder()
	removedRequest := httptest.NewRequest(http.MethodPut, "/admin/v1/applications/101/playback-profile", nil)
	removedRequest.Header.Set("Authorization", "Bearer "+admin.Token)
	f.handler(t).ServeHTTP(removed, removedRequest)
	if removed.Code != http.StatusNotFound {
		t.Fatal("已删除的应用播放配置引用接口仍可访问")
	}
	// 列表按钮使用同一发布命令往返状态，下线内容与从未上架内容统一进入待上线筛选。
	draft, err := f.store.FindDrama(bg, "201")
	if err != nil {
		t.Fatal(err)
	}
	draft.Title = "已上线后的新草稿"
	if err := f.store.SaveDrama(bg, *draft); err != nil {
		t.Fatal(err)
	}
	raw = iaaRequest(t, server.URL, "GET", "/admin/v1/direct/dramas?status=published", admin.Token, "", nil, 200)
	if err := json.Unmarshal(raw, &pendingRows); err != nil || len(pendingRows) != 1 || pendingRows[0].Draft.PublishStatus != "draft" {
		t.Fatal("编辑后已上线内容失去下线入口", err)
	}
	raw = iaaRequest(t, server.URL, "GET", "/admin/v1/direct/dramas?status=pending", admin.Token, "", nil, 200)
	if err := json.Unmarshal(raw, &pendingRows); err != nil || len(pendingRows) != 0 {
		t.Fatal("已上线内容残留待上线筛选", err)
	}
	w, _ = r.Workbench(bg, "201")
	// 隐藏不等于移除，直连下线前也必须清理已保存的推荐引用。
	iaaRequest(t, server.URL, "POST", base+"/operations", admin.Token, "", directplay.Command{RequestID: "list-direct-offline", ExpectedRevision: w.Publication.Revision, Kind: "offline"}, 409)
	carousel, err := f.store.GetCarousel(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ReplaceCarousel(ctx, homepage.Carousel{Revision: carousel.Revision, DramaIDs: []string{}}); err != nil {
		t.Fatal(err)
	}
	iaaRequest(t, server.URL, "POST", base+"/operations", admin.Token, "", directplay.Command{RequestID: "list-direct-offline", ExpectedRevision: w.Publication.Revision, Kind: "offline"}, 200)
	raw = iaaRequest(t, server.URL, "GET", "/admin/v1/direct/dramas?status=pending", admin.Token, "", nil, 200)
	if err := json.Unmarshal(raw, &pendingRows); err != nil || len(pendingRows) != 1 || pendingRows[0].Publication.Status != "offline" {
		t.Fatal("下线草稿缺少重新上线入口", err)
	}
	if _, err := f.store.PrepareDrama(bg, "201", "operator"); err != nil {
		t.Fatal(err)
	}
	raw = iaaRequest(t, server.URL, "GET", "/admin/v1/direct/dramas?status=pending", admin.Token, "", nil, 200)
	if err := json.Unmarshal(raw, &pendingRows); err != nil || len(pendingRows) != 1 || pendingRows[0].Publication.Status != "offline" {
		t.Fatal("下线后未回到待上线列表", err)
	}
	w, _ = r.Workbench(bg, "201")
	iaaRequest(t, server.URL, "POST", base+"/operations", admin.Token, "", directplay.Command{RequestID: "list-direct-republish", ExpectedRevision: w.Publication.Revision, Kind: "relist"}, 200)
	// 降为普通管理员；菜单和目录节点不授予发布操作。
	user, err := f.store.FindAdminUserByUsername(bg, "iaa-admin")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.data.DB().Table("admin_users").Where("id=?", user.ID).Update("is_super", false).Error; err != nil {
		t.Fatal(err)
	}
	role := adminrbac.Role{Name: "direct reader", Code: "direct-reader", Status: adminrbac.StatusActive}
	if err := f.store.CreateAdminRole(bg, &role); err != nil {
		t.Fatal(err)
	}
	if err := f.store.ReplaceAdminUserRoles(bg, user.ID, []string{role.ID}); err != nil {
		t.Fatal(err)
	}
	var ids []string
	if err := f.store.data.DB().Table("admin_permissions").Where("code IN ?", []string{"directory.direct", "menu.direct_publications", "operations.direct_publication.read"}).Pluck("id", &ids).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.store.ReplaceAdminRolePermissions(bg, role.ID, ids); err != nil {
		t.Fatal(err)
	}
	iaaRequest(t, server.URL, "GET", base, admin.Token, "", nil, 200)
	command.Kind = "offline"
	command.RequestID = "http-direct-offline"

	w, _ = r.Workbench(bg, "201")
	command.ExpectedRevision = w.Publication.Revision
	iaaRequest(t, server.URL, "POST", base+"/operations", admin.Token, "", command, 403)
	iaaRequest(t, server.URL, "PUT", "/admin/v1/direct/carousel", admin.Token, "", homepage.Carousel{DramaIDs: []string{}}, 403)
	iaaRequest(t, server.URL, "GET", base, "", "", nil, 401)
}
