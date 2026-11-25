package redisstore

import (
	"bytes"
	"context"
	"drama-server/internal/biz/globalconfig"
	"encoding/json"
	"errors"
	"github.com/redis/go-redis/v9"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

type configSource struct {
	row              globalconfig.Entry
	getErr, writeErr error
	reads            int
}

func (s *configSource) List(context.Context) ([]globalconfig.Entry, error) {
	return []globalconfig.Entry{s.row}, s.getErr
}
func (s *configSource) Get(context.Context, string) (globalconfig.Entry, error) {
	s.reads++
	return s.row, s.getErr
}
func (s *configSource) Put(_ context.Context, row globalconfig.Entry) error {
	if s.writeErr != nil {
		return s.writeErr
	}
	s.row = row
	return nil
}
func (s *configSource) Delete(context.Context, string) error {
	if s.writeErr != nil {
		return s.writeErr
	}
	s.getErr = globalconfig.ErrNotFound
	return nil
}

// TestGlobalConfigCacheIntegration 验证命中不查数据库、清缓存回源、修改/删除失效和数据库写失败保持缓存。
func TestGlobalConfigCacheIntegration(t *testing.T) {
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR not set")
	}
	ctx := context.Background()
	client := redis.NewClient(&redis.Options{Addr: addr, DB: 15})
	defer client.Close()
	key := "integration.cache"
	defer client.Del(ctx, globalConfigCacheKey(key))
	source := &configSource{row: globalconfig.Entry{Key: key, Value: json.RawMessage(`{"enabled":true}`)}}
	repo := NewCachedGlobalConfigRepository(source, client)
	if _, err := repo.Get(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Get(ctx, key); err != nil || source.reads != 1 {
		t.Fatal("命中缓存仍查数据库", err)
	}
	ttl, err := client.TTL(ctx, globalConfigCacheKey(key)).Result()
	if err != nil || ttl <= 0 {
		t.Fatal("缓存缺少 TTL", err)
	}
	source.writeErr = errors.New("database write unavailable")
	if err := repo.Put(ctx, source.row); err == nil {
		t.Fatal("数据库写故障被忽略")
	}
	if n := client.Exists(ctx, globalConfigCacheKey(key)).Val(); n != 1 {
		t.Fatal("写失败删除了缓存")
	}
	source.writeErr = nil
	next := source.row
	next.Value = json.RawMessage(`{"enabled":false}`)
	if err := repo.Put(ctx, next); err != nil {
		t.Fatal(err)
	}
	if n := client.Exists(ctx, globalConfigCacheKey(key)).Val(); n != 0 {
		t.Fatal("更新没有删除缓存")
	}
	row, err := repo.Get(ctx, key)
	if err != nil || string(row.Value) != string(next.Value) {
		t.Fatal("未读到更新", err)
	}
	client.Del(ctx, globalConfigCacheKey(key))
	if _, err := repo.Get(ctx, key); err != nil || source.reads != 3 {
		t.Fatal("清缓存后未回源", err)
	}
	// 损坏缓存不能影响数据库权威值，日志不能输出损坏载荷中的密钥。
	var diagnostics bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&diagnostics, nil)))
	if err := client.Set(ctx, globalConfigCacheKey(key), "corrupt-fixture-secret", time.Minute).Err(); err != nil {
		slog.SetDefault(previous)
		t.Fatal(err)
	}
	_, readErr := repo.Get(ctx, key)
	slog.SetDefault(previous)
	if readErr != nil || !strings.Contains(diagnostics.String(), "decode global configuration cache failed") || strings.Contains(diagnostics.String(), "corrupt-fixture-secret") {
		t.Fatal("损坏缓存未安全回源", readErr)
	}
	if err := repo.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Get(ctx, key); !errors.Is(err, globalconfig.ErrNotFound) {
		t.Fatal("已删除配置复活", err)
	}
	if n := client.Exists(ctx, globalConfigCacheKey(key)).Val(); n != 0 {
		t.Fatal("缺失结果被缓存")
	}
}

// TestGlobalConfigCacheFailure 验证缓存故障可回源、已提交写入仍成功，诊断包含原因且不包含配置密钥。
func TestGlobalConfigCacheFailure(t *testing.T) {
	ctx := context.Background()
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	client.Close()
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	source := &configSource{row: globalconfig.Entry{Key: "fixture", Value: json.RawMessage(`{"secret":"must-not-log"}`)}}
	repo := NewCachedGlobalConfigRepository(source, client)
	if row, err := repo.Get(ctx, "fixture"); err != nil || string(row.Value) != string(source.row.Value) {
		t.Fatal("缓存故障未回源", err)
	}
	if err := repo.Put(ctx, source.row); err != nil {
		t.Fatal("已持久化却误报失败", err)
	}
	for _, operation := range []string{"read global configuration cache failed", "write global configuration cache failed", "delete global configuration cache failed", "redis: client is closed"} {
		if !strings.Contains(logs.String(), operation) {
			t.Fatal("故障缺少具体日志", operation)
		}
	}
	if strings.Contains(logs.String(), "must-not-log") {
		t.Fatal("密钥写入日志")
	}
	source.getErr = errors.New("database read unavailable")
	if _, err := repo.Get(ctx, "fixture"); !errors.Is(err, source.getErr) {
		t.Fatal("数据库错误被隐藏", err)
	}
}
