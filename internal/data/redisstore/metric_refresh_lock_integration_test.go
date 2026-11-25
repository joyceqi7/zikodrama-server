package redisstore

import (
	"context"
	"crypto/rand"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// TestMetricRefreshLockOwnershipIntegration 验证应用隔离、续期、失效接管和迟到释放，不清理其他 Redis 数据。
func TestMetricRefreshLockOwnershipIntegration(t *testing.T) {
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("需要隔离 TEST_REDIS_ADDR")
	}
	client := redis.NewClient(&redis.Options{Addr: addr, DB: 10, Username: os.Getenv("TEST_REDIS_USERNAME"), Password: os.Getenv("TEST_REDIS_PASSWORD")})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	key := "test:dashboard:refresh:" + rand.Text()
	other := key + ":other-app"
	t.Cleanup(func() { _ = client.Del(ctx, key, other).Err() })
	lock := NewMetricRefreshLock(client)
	if ok, err := lock.Acquire(ctx, key, "first", time.Minute); err != nil || !ok {
		t.Fatal("首次领取失败", err)
	}
	if ok, err := lock.Acquire(ctx, key, "second", time.Minute); err != nil || ok {
		t.Fatal("同一应用出现第二个持有者", err)
	}
	if ok, err := lock.Acquire(ctx, other, "second", time.Minute); err != nil || !ok {
		t.Fatal("另一应用被阻挡", err)
	}
	if ok, err := lock.Renew(ctx, key, "second", time.Minute); err != nil || ok {
		t.Fatal("非持有者续期成功", err)
	}
	if err := lock.Release(ctx, key, "second"); err != nil {
		t.Fatal(err)
	}
	if ok, err := lock.Renew(ctx, key, "first", time.Minute); err != nil || !ok {
		t.Fatal("非持有者释放影响原锁", err)
	}
	if ttl := client.PTTL(ctx, key).Val(); ttl <= 0 || ttl > time.Minute {
		t.Fatalf("锁缺少有限 TTL: %s", ttl)
	}
	// 确定性模拟 TTL 到期；只操作本测试刚创建的键。
	if err := client.PExpire(ctx, key, -time.Second).Err(); err != nil {
		t.Fatal(err)
	}
	if ok, err := lock.Acquire(ctx, key, "second", time.Minute); err != nil || !ok {
		t.Fatal("失效后接管失败", err)
	}
	if ok, err := lock.Renew(ctx, key, "first", time.Minute); err != nil || ok {
		t.Fatal("旧持有者续期了新锁", err)
	}
	if err := lock.Release(ctx, key, "first"); err != nil {
		t.Fatal(err)
	}
	if got := client.Get(ctx, key).Val(); got != "second" {
		t.Fatal("迟到释放删除了新持有者")
	}
	if err := lock.Release(ctx, key, "second"); err != nil {
		t.Fatal(err)
	}
	if client.Exists(ctx, key).Val() != 0 {
		t.Fatal("正常释放后锁仍存在")
	}
}
