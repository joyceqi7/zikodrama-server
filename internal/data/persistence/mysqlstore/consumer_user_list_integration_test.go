package mysqlstore

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/operations"
	operationshttp "drama-server/internal/service/operations"
)

func consumerListHandler(f *iaaFixture) http.Handler {
	return operationshttp.NewHandler(operations.NewService(operations.Repositories{ConsumerUsers: f.store, Application: operations.ApplicationPorts{Subjects: f.store, Registry: f.store}, Content: operations.ContentPorts{Catalog: f.store, Publications: f.store}, Reporting: operations.ReportingPorts{Metrics: f.store, Audits: f.store}, Monetization: operations.MonetizationPorts{Settings: f.store}}, nil), adminrbac.NewService(f.store, adminrbac.Config{SigningKey: []byte("test-only-admin-jwt-signing-key-32-bytes")}), operationshttp.Config{})
}

// seedConsumerList 覆盖 16 个物理分片、同时间排序、大整数主键和跨应用隔离。
func seedConsumerList(t *testing.T, f *iaaFixture) {
	t.Helper()
	now := time.Date(2026, 9, 15, 8, 0, 0, 0, time.UTC)
	hash := "test-private-openid-hash"
	for i := 0; i < 41; i++ {
		var identity *string
		if i%2 == 0 {
			value := fmt.Sprintf("%s-%d", hash, i)
			identity = &value
		}
		model := consumerUserModel{ID: fmt.Sprint(9007199254740993 + int64(i)), BucketID: uint16(i % 16), ApplicationID: "101", OpenIDHash: identity, CreatedAt: now, UpdatedAt: now}
		if err := f.store.data.DB().Table(userTable("users", model.BucketID)).Create(&model).Error; err != nil {
			t.Fatal(err)
		}
	}
	other := consumerUserModel{ID: "9007199254741993", BucketID: 15, ApplicationID: "102", CreatedAt: now.Add(time.Hour), UpdatedAt: now}
	if err := f.store.data.DB().Table("users_15").Create(&other).Error; err != nil {
		t.Fatal(err)
	}
}

func TestConsumerUserListIntegration(t *testing.T) {
	f := newIAAFixture(t)
	seedConsumerList(t, f)
	server := httptest.NewServer(consumerListHandler(f))
	defer server.Close()
	data := iaaRequest(t, server.URL, "POST", "/admin/v1/session", "", "", map[string]string{"username": "iaa-admin", "password": "LocalIAA123!"}, 200)
	var login struct {
		Token string           `json:"token"`
		Menus []adminrbac.Menu `json:"menus"`
	}
	if err := json.Unmarshal(data, &login); err != nil {
		t.Fatal(err)
	}
	// 超管未分配角色也应看到独立的用户管理目录，并能读取其下的用户列表。
	foundUserMenu := false
	for _, directory := range login.Menus {
		for _, menu := range directory.Children {
			if menu.Route == "/channels/users" {
				if directory.Name != "用户管理" || directory.Route != "" {
					t.Fatalf("用户列表应位于用户管理目录下，实际目录为 %q", directory.Name)
				}
				foundUserMenu = true
			}
		}
	}
	if !foundUserMenu {
		t.Fatal("超管会话缺少用户管理目录下的用户列表")
	}
	read := func(query, app string) operations.ConsumerUserPage {
		t.Helper()
		body := iaaRequest(t, server.URL, "GET", "/admin/v1/consumer-users"+query, login.Token, app, nil, 200)
		if strings.Contains(string(body), "openId") || strings.Contains(string(body), "test-private") {
			t.Fatal("查询泄漏身份摘要")
		}
		var result operations.ConsumerUserPage
		if err := json.Unmarshal(body, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := read("", "101")
	if !first.HasMore || first.NextCursor == "" || len(first.Items) != 20 || first.Items[0].ID != "9007199254741033" || first.Items[0].IdentityType != "tiktok" {
		t.Fatalf("第一页排序或总数错误: %+v", first)
	}
	second := read("?cursor="+url.QueryEscape(first.NextCursor)+"&pageSize=20", "101")
	if second.Items[0].ID != "9007199254741013" || !second.HasMore {
		t.Fatal("跨分片分页错误")
	}
	last := read("?cursor="+url.QueryEscape(second.NextCursor)+"&pageSize=20", "101")
	if last.HasMore || last.NextCursor != "" || len(last.Items) != 1 || last.Items[0].ID != "9007199254740993" {
		t.Fatal("末页结果错误")
	}
	if result := read("?userId=9007199254740993", "101"); len(result.Items) != 1 || result.Items[0].ID != "9007199254740993" {
		t.Fatal("大整数精确搜索错误")
	}
	if result := read("?userId=9007199254740993", "102"); len(result.Items) != 0 || result.Items == nil {
		t.Fatal("跨应用用户泄漏或空数组合同错误")
	}
	if result := read("", "102"); len(result.Items) != 1 || result.Items[0].ApplicationID != "102" {
		t.Fatal("切换应用未隔离")
	}
	for _, query := range []string{"?page=0", "?pageSize=101", "?page=-1", "?page=1000001", "?page=oops", "?userId=01", "?userId=18446744073709551616", "?userId=1e3"} {
		iaaRequest(t, server.URL, "GET", "/admin/v1/consumer-users"+query, login.Token, "101", nil, 400)
	}
	// 游标不能跨应用、筛选条件重放；非法编码和旧页码必须明确拒绝。
	for _, query := range []string{"?page=1", "?cursor=%%%", "?cursor=" + strings.Repeat("a", 513), "?cursor=" + url.QueryEscape(first.NextCursor) + "&userId=9007199254740993"} {
		iaaRequest(t, server.URL, "GET", "/admin/v1/consumer-users"+query, login.Token, "101", nil, 400)
	}
	iaaRequest(t, server.URL, "GET", "/admin/v1/consumer-users?cursor="+url.QueryEscape(first.NextCursor), login.Token, "102", nil, 400)
	for _, payload := range []string{
		"101||not-a-time|9007199254740993", "101||2026-09-15T08:00:00.000000001Z|9007199254740993",
		"101||2026-09-15T08:00:00Z|01", "101||2026-09-15T08:00:00Z|18446744073709551616",
	} {
		cursor := base64.RawURLEncoding.EncodeToString([]byte(payload))
		iaaRequest(t, server.URL, "GET", "/admin/v1/consumer-users?cursor="+cursor, login.Token, "101", nil, 400)
	}
	// 翻页期间新增较新用户，不得挤动已有游标边界；刷新首页才能看到新记录。
	now := time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC)
	if err := f.store.data.DB().Table("users_00").Create(&consumerUserModel{ID: "9007199254742993", BucketID: 0, ApplicationID: "101", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	afterInsert := read("?cursor="+url.QueryEscape(first.NextCursor), "101")
	if len(afterInsert.Items) != len(second.Items) || afterInsert.Items[0].ID != second.Items[0].ID {
		t.Fatal("新增用户扰乱后续页")
	}
	if refreshed := read("", "101"); refreshed.Items[0].ID != "9007199254742993" {
		t.Fatal("刷新首页未显示新增用户")
	}
	iaaRequest(t, server.URL, "GET", "/admin/v1/consumer-users", login.Token, "", nil, 400)
	iaaRequest(t, server.URL, "GET", "/admin/v1/consumer-users", "", "101", nil, 401)
	rbac := adminrbac.NewService(f.store, adminrbac.Config{SigningKey: []byte("test-only-admin-jwt-signing-key-32-bytes")})
	user, err := rbac.CreateUser(context.Background(), adminrbac.UserCreateInput{Username: "consumer-reader", Password: "local-reader-password", Nickname: "只读账号", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := rbac.Login(context.Background(), user.Username, "local-reader-password")
	if err != nil {
		t.Fatal(err)
	}
	iaaRequest(t, server.URL, "GET", "/admin/v1/consumer-users", reader.Token, "101", nil, 403)
	permissions, err := f.store.ListAdminPermissions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, permission := range permissions {
		if permission.Code == "menu.consumerusers" || permission.Code == "directory.consumerusers" || permission.Code == adminrbac.PermissionConsumerUserRead {
			ids = append(ids, permission.ID)
		}
	}
	role, err := rbac.CreateRole(context.Background(), adminrbac.RoleInput{Name: "观众只读", Code: "consumer.reader", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	if err := rbac.SetRolePermissions(context.Background(), role.ID, ids); err != nil {
		t.Fatal(err)
	}
	if err := f.store.ReplaceAdminUserRoles(context.Background(), user.ID, []string{role.ID}); err != nil {
		t.Fatal(err)
	}
	iaaRequest(t, server.URL, "GET", "/admin/v1/consumer-users", reader.Token, "101", nil, 200)
	iaaRequest(t, server.URL, "GET", "/admin/v1/users", reader.Token, "", nil, 403)
	if err := rbac.SetRolePermissions(context.Background(), role.ID, nil); err != nil {
		t.Fatal(err)
	}
	iaaRequest(t, server.URL, "GET", "/admin/v1/consumer-users", reader.Token, "101", nil, 403)

	// 无可信应用上下文的内部调用同样不得返回全库用户。
	if _, err := f.store.ListConsumerUsers(context.Background(), operations.ConsumerUserQuery{PageSize: 20}); err == nil {
		t.Fatal("无应用查询未拒绝")
	}
	if _, err := f.store.ListConsumerUsers(application.WithContext(context.Background(), "101"), operations.ConsumerUserQuery{PageSize: 20}); err != nil {
		t.Fatal(err)
	}
}
