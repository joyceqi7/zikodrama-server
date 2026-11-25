package mysqlstore

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	biz "drama-server/internal/biz"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/operations"
	"drama-server/internal/biz/visitor"
)

// TestIAAHTTPIntegrationOfficialAppID 通过真实 HTTP/MySQL 验证官方应用定位及凭据恢复，只模拟平台换码。
func TestIAAHTTPIntegrationOfficialAppID(t *testing.T) {
	f := newIAAFixture(t)
	ctx := context.Background()
	app, err := f.store.FindApplication(ctx, "101")
	if err != nil {
		t.Fatal(err)
	}
	app.TikTokAppID = "0009007199254740993123"
	if err := f.store.SaveApplication(ctx, app); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(f.handler(t))
	defer server.Close()
	raw := iaaRequest(t, server.URL, "POST", "/api/v1/bootstrap", "", "", visitor.BootstrapInput{TikTokAppID: app.TikTokAppID, LoginCode: "user-a"}, 200)
	var bootstrap biz.BootstrapResult
	if err := json.Unmarshal(raw, &bootstrap); err != nil {
		t.Fatal(err)
	}
	if bootstrap.TikTokAppID != app.TikTokAppID || bootstrap.ApplicationID != app.ID {
		t.Fatal("应用内部主键与官方 AppID 未正确分离")
	}
	iaaRequest(t, server.URL, "GET", "/api/v1/home", bootstrap.VisitorToken, "", nil, 200)
	iaaRequest(t, server.URL, "GET", "/api/v1/open/catalog?tiktokAppId="+app.TikTokAppID, "", "", nil, 200)
	iaaRequest(t, server.URL, "POST", "/api/v1/bootstrap", "", "", map[string]string{"appCode": app.TikTokAppID, "loginCode": "user-a"}, 400)
	for _, id := range []string{"1", "legacy-business-code", "missing"} {
		iaaRequest(t, server.URL, "POST", "/api/v1/bootstrap", "", "", visitor.BootstrapInput{TikTokAppID: id, LoginCode: "user-a"}, 403)
	}
	app.Status = application.StatusDisabled
	if err := f.store.SaveApplication(ctx, app); err != nil {
		t.Fatal(err)
	}
	iaaRequest(t, server.URL, "GET", "/api/v1/home", bootstrap.VisitorToken, "", nil, 403)
	iaaRequest(t, server.URL, "GET", "/api/v1/open/catalog?tiktokAppId="+app.TikTokAppID, "", "", nil, 403)

	// 默认数据库排序规则不能将大小写不同的官方标识当作同一个应用。
	app.Status, app.TikTokAppID = application.StatusActive, "OfficialCaseID"
	if err := f.store.SaveApplication(ctx, app); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.FindApplicationByTikTokAppID(ctx, "officialcaseid"); !errors.Is(err, application.ErrUnavailable) {
		t.Fatalf("官方 ID 未精确匹配: %v", err)
	}
	if found, err := f.store.FindApplicationByTikTokAppID(ctx, app.TikTokAppID); err != nil || found.TikTokAppID != app.TikTokAppID {
		t.Fatalf("官方 ID 无法读回: %v", err)
	}
	duplicate, err := f.store.FindApplication(ctx, "102")
	if err != nil {
		t.Fatal(err)
	}
	duplicate.TikTokAppID = app.TikTokAppID
	if err := f.store.CreateApplication(ctx, &duplicate); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("官方身份重复创建未被拒绝：%v", err)
	}
	iaaRequest(t, server.URL, "POST", "/api/v1/bootstrap", "", "", visitor.BootstrapInput{TikTokAppID: app.TikTokAppID, LoginCode: "user-a"}, 200)
}
