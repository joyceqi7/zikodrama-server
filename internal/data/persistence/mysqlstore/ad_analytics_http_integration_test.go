package mysqlstore

import (
	"context"
	"drama-server/internal/biz/adminrbac"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

// TestAdAnalyticsHTTPIntegration 验证消费端认证采集、旧 Mini 兼容、应用隔离及独立菜单权限。
func TestAdAnalyticsHTTPIntegration(t *testing.T) {
	f := newIAAFixture(t)
	server := httptest.NewServer(f.handler(t))
	defer server.Close()
	token, _ := iaaLogin(t, server.URL, "101", "user-a")
	now := time.Now().UTC()
	ms := 125.0
	detail := domain.AdMetricDetails{AdType: "rewarded", AdUnitID: "slot-1", Scene: "entry_reward", ClientVersion: "build-42", OS: "ios", TikTokVersion: "46.1.0", Stage: "show", ShowElapsedMS: &ms}
	body := map[string]any{"events": []domain.MetricEvent{{ID: "http-ad-call-12345678", Kind: "ad_request", OccurredAt: now, Ad: &detail}, {ID: "legacy-ad-count-12345", Kind: "rewarded_show", OccurredAt: now}}}
	for i := 0; i < 2; i++ {
		iaaRequest(t, server.URL, "POST", "/api/v1/metrics", token, "102", body, 200)
	}
	// 即使伪装成有效 show 阶段，已移除的加载事件也须整批拒绝，不能写入同批的有效请求。
	for _, kind := range []string{"ad_load_request", "ad_loaded", "ad_load_unknown"} {
		for _, stage := range []string{"load", "show"} {
			removed := detail
			removed.Stage = stage
			invalid := map[string]any{"events": []domain.MetricEvent{
				{ID: "rejected-request-" + kind + stage, Kind: "ad_request", OccurredAt: now, Ad: &detail},
				{ID: "removed-stage-" + kind + stage, Kind: kind, OccurredAt: now, Ad: &removed},
			}}
			iaaRequest(t, server.URL, "POST", "/api/v1/metrics", token, "101", invalid, 400)
		}
	}
	raw := iaaRequest(t, server.URL, "POST", "/admin/v1/session", "", "", map[string]string{"username": "iaa-admin", "password": "LocalIAA123!"}, 200)
	var login struct{ Token string }
	if err := json.Unmarshal(raw, &login); err != nil {
		t.Fatal(err)
	}
	path := "/admin/v1/ad-analytics?from=" + now.Format(time.DateOnly) + "&to=" + now.Format(time.DateOnly)
	raw = iaaRequest(t, server.URL, "GET", path, login.Token, "101", nil, 200)
	var result operations.AdAnalytics
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result.Summary.Requests != 1 {
		t.Fatalf("请求不应重复或被请求头跨应用改绑 %+v", result.Summary)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"loadRequests", "loaded", "loadUnknown"} {
		if _, exists := payload["summary"].(map[string]any)[key]; exists {
			t.Fatalf("广告统计仍返回已移除字段 %s", key)
		}
	}
	// 游标只接受生成时的应用及筛选条件，不能借边界绕开应用认证。
	q := operations.AdAnalyticsQuery{From: now.Format(time.DateOnly), To: now.Format(time.DateOnly), PageSize: 20}
	cursor := operations.AdAnalyticsCursor("101", q, result.Items[0])
	iaaRequest(t, server.URL, "GET", path+"&cursor="+cursor, login.Token, "101", nil, 200)
	iaaRequest(t, server.URL, "GET", path+"&cursor="+cursor, login.Token, "102", nil, 400)
	iaaRequest(t, server.URL, "GET", path+"&adType=rewarded&cursor="+cursor, login.Token, "101", nil, 400)
	iaaRequest(t, server.URL, "GET", path, "", "101", nil, 401)
	iaaRequest(t, server.URL, "GET", path, login.Token, "", nil, 400)
	for _, extra := range []string{"&scene=invalid", "&errorCode=secret", "&pageSize=101", "&cursor=invalid"} {
		iaaRequest(t, server.URL, "GET", path+extra, login.Token, "101", nil, 400)
	}
	rbac := adminrbac.NewService(f.store, adminrbac.Config{SigningKey: []byte("test-only-admin-jwt-signing-key-32-bytes")})
	user, err := rbac.CreateUser(context.Background(), adminrbac.UserCreateInput{Username: "ad-reader", Password: "local-reader-password", Nickname: "广告只读", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := rbac.Login(context.Background(), user.Username, "local-reader-password")
	if err != nil {
		t.Fatal(err)
	}
	iaaRequest(t, server.URL, "GET", path, reader.Token, "101", nil, 403)
	permissions, err := f.store.ListAdminPermissions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, p := range permissions {
		if p.Code == "menu.ad_analytics" || p.Code == adminrbac.PermissionAdAnalyticsRead {
			ids = append(ids, p.ID)
		}
	}
	if len(ids) != 2 {
		t.Fatal("缺少独立菜单或操作节点")
	}
	role, err := rbac.CreateRole(context.Background(), adminrbac.RoleInput{Name: "广告只读", Code: "ad.reader", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	if err := rbac.SetRolePermissions(context.Background(), role.ID, ids); err != nil {
		t.Fatal(err)
	}
	if err := f.store.ReplaceAdminUserRoles(context.Background(), user.ID, []string{role.ID}); err != nil {
		t.Fatal(err)
	}
	iaaRequest(t, server.URL, "GET", path, reader.Token, "101", nil, 200)
	iaaRequest(t, server.URL, "GET", "/admin/v1/dashboard?from="+now.Format(time.DateOnly)+"&to="+now.Format(time.DateOnly), reader.Token, "101", nil, 403)
}
