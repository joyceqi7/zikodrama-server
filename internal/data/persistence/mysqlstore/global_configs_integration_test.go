package mysqlstore

import (
	"context"
	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/globalconfig"
	"drama-server/internal/biz/operations"
	"drama-server/internal/data/redisstore"
	operationshttp "drama-server/internal/service/operations"
	"encoding/json"
	"errors"
	"github.com/redis/go-redis/v9"
	"net/http/httptest"
	"os"
	"testing"
)

func TestGlobalConfigMySQLIntegration(t *testing.T) {
	ctx := context.Background()
	f := newIAAFixture(t)
	defer f.store.data.pool.Close()
	repo := f.store.GlobalConfigs()
	entry := globalconfig.Entry{Key: "test.config", Value: json.RawMessage(`{"enabled":true}`), Remark: "初始配置", UpdatedBy: "admin"}
	if err := repo.Put(ctx, entry); err != nil {
		t.Fatal(err)
	}
	first, err := repo.Get(ctx, entry.Key)
	if err != nil || first.CreatedAt == nil || first.UpdatedAt == nil || first.Remark != entry.Remark {
		t.Fatal("配置未持久化", err)
	}
	entry.Remark = "修改后"
	if err := repo.Put(ctx, entry); err != nil {
		t.Fatal(err)
	}
	second, err := repo.Get(ctx, entry.Key)
	if err != nil || second.CreatedAt == nil || !second.CreatedAt.Equal(*first.CreatedAt) || second.Remark != entry.Remark {
		t.Fatal("更新未保留创建时间", err)
	}
	if err := repo.Delete(ctx, entry.Key); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Get(ctx, entry.Key); !errors.Is(err, globalconfig.ErrNotFound) {
		t.Fatal("删除失败", err)
	}
	if err := repo.Delete(ctx, entry.Key); !errors.Is(err, globalconfig.ErrNotFound) {
		t.Fatal("重复删除未报告缺失", err)
	}
}

// TestGlobalConfigHTTPIntegration 验证真实管理接口写 MySQL、跨实例缓存刷新，以及缓存清空后的恢复。
func TestGlobalConfigHTTPIntegration(t *testing.T) {
	address := os.Getenv("TEST_REDIS_ADDR")
	if address == "" {
		t.Skip("TEST_REDIS_ADDR not set")
	}
	f := newIAAFixture(t)
	defer f.store.data.pool.Close()
	ctx := context.Background()
	client := redis.NewClient(&redis.Options{Addr: address, DB: 15})
	defer client.Close()
	key := "integration.httpconfig"
	cacheKey := "drama:global_config_cache:" + key
	defer client.Del(ctx, cacheKey)
	repo := redisstore.NewCachedGlobalConfigRepository(f.store.GlobalConfigs(), client)
	handler := operationshttp.NewHandler(operations.NewService(operations.Repositories{Reporting: operations.ReportingPorts{Audits: f.store}}, nil), adminrbac.NewService(f.store, adminrbac.Config{SigningKey: []byte("test-only-admin-jwt-signing-key-32-bytes")}), operationshttp.Config{GlobalConfigs: globalconfig.NewService(repo)})
	server := httptest.NewServer(handler)
	defer server.Close()
	var session struct {
		Token string `json:"token"`
	}
	mustDecodeApplicationStrategy(t, iaaRequest(t, server.URL, "POST", "/admin/v1/session", "", "", map[string]string{"username": "iaa-admin", "password": "LocalIAA123!"}, 200), &session)
	path := "/admin/v1/global-configs/" + key
	iaaRequest(t, server.URL, "PUT", path, session.Token, "", map[string]any{"value": map[string]any{"enabled": true}, "remark": "初始配置"}, 200)
	stored, err := f.store.GlobalConfigs().Get(ctx, key)
	if err != nil || stored.UpdatedBy != "iaa-admin" || stored.Remark != "初始配置" {
		t.Fatal("接口未持久化操作记录", err)
	}
	other := redisstore.NewCachedGlobalConfigRepository(f.store.GlobalConfigs(), client)
	if _, err := other.Get(ctx, key); err != nil {
		t.Fatal(err)
	}
	iaaRequest(t, server.URL, "PUT", path, session.Token, "", map[string]any{"value": map[string]any{"enabled": false}, "remark": "更新配置"}, 200)
	got, err := other.Get(ctx, key)
	if err != nil || got.Remark != "更新配置" {
		t.Fatal("跨实例缓存未刷新", err)
	}
	if err := client.Del(ctx, cacheKey).Err(); err != nil {
		t.Fatal(err)
	}
	got, err = other.Get(ctx, key)
	if err != nil || got.Remark != "更新配置" {
		t.Fatal("缓存清空导致配置丢失", err)
	}
	iaaRequest(t, server.URL, "DELETE", path, session.Token, "", nil, 200)
	if _, err := other.Get(ctx, key); !errors.Is(err, globalconfig.ErrNotFound) {
		t.Fatal("删除未生效", err)
	}
}
