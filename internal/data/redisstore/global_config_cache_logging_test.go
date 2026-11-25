package redisstore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"drama-server/internal/biz/globalconfig"
	"github.com/redis/go-redis/v9"
)

// TestGlobalConfigCacheDiagnosticsIntegration 验证损坏缓存和编码失败记录具体原因，同时保留回源结果且不输出配置载荷。
func TestGlobalConfigCacheDiagnosticsIntegration(t *testing.T) {
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR not set")
	}
	client := redis.NewClient(&redis.Options{Addr: addr, DB: 15})
	defer client.Close()
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	for _, tc := range []struct {
		name, raw, detail string
		encode            bool
	}{
		{name: "JSON损坏", raw: `{"value":"private-config-secret",`, detail: "unexpected end of JSON input"},
		{name: "键不匹配", raw: `{"key":"other","value":{"secret":"private-config-secret"}}`, detail: "cached configuration key mismatch"},
		{name: "回源值无法编码", encode: true, detail: "error calling MarshalJSON for type json.RawMessage"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key := fmt.Sprintf("logging.%d", time.Now().UnixNano())
			ctx := context.Background()
			defer client.Del(ctx, globalConfigCacheKey(key))
			source := &configSource{row: globalconfig.Entry{Key: key, Value: json.RawMessage(`{"secret":"private-config-secret"}`)}}
			if tc.encode {
				source.row.Value = json.RawMessage(`{"secret":"private-config-secret",`)
			} else if err := client.Set(ctx, globalConfigCacheKey(key), tc.raw, time.Minute).Err(); err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			row, err := NewCachedGlobalConfigRepository(source, client).Get(ctx, key)
			if err != nil || string(row.Value) != string(source.row.Value) || source.reads != 1 {
				t.Fatalf("原回源合同改变: %v", err)
			}
			var entry map[string]any
			if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
				t.Fatal(err)
			}
			if entry["config_key"] != key || !strings.Contains(entry["error"].(string), tc.detail) || strings.Contains(logs.String(), "private-config-secret") {
				t.Fatalf("缺少具体原因、配置键或泄漏载荷: %v", entry)
			}
		})
	}
}
