package redis

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

func TestAuditLoggingHookRedactsCommandValues(t *testing.T) {
	handler := &captureHandler{}
	cmd := goredis.NewStatusCmd(context.Background(), "set", "audit-key", "audit-secret")
	NewLoggingHook("audit", time.Nanosecond, testDependencies(t, slog.New(handler))).
		logCommand(context.Background(), cmd, time.Second, nil)

	serialized := ""
	for _, record := range handler.records {
		record.Attrs(func(attr slog.Attr) bool {
			serialized += fmt.Sprint(attr.Value.Any())
			return true
		})
	}
	if strings.Contains(serialized, "audit-secret") {
		t.Fatal("Redis 慢命令日志泄露了命令值")
	}
}
