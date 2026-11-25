package data

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"drama-server/internal/data/redisstore"
	"github.com/redis/go-redis/v9"
)

// TestCatalogWithoutCacheErrors 验证无缓存时数据库错误保留实际原因，成功路径不误报日志。
func TestCatalogWithoutCacheErrors(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	want := errors.New("fixture MySQL connection refused")
	_, err := catalogRead(context.Background(), nil, "fixture", func() ([]string, error) { return nil, want })
	if !errors.Is(err, want) || !strings.Contains(logs.String(), "load consumer catalog without cache failed") || !strings.Contains(logs.String(), want.Error()) {
		t.Fatal("无缓存回源错误缺少操作、原因或传播", err, logs.String())
	}
	logs.Reset()
	value, err := catalogRead(context.Background(), nil, "fixture", func() ([]string, error) { return []string{"ok"}, nil })
	if err != nil || len(value) != 1 || logs.Len() != 0 {
		t.Fatal("成功回源结果异常或误报故障", err, logs.String())
	}
}

// TestCatalogCacheRequestsKeepOwnContext 验证同键请求独立查库，先到请求的取消不能阻塞或污染后到请求。
func TestCatalogCacheRequestsKeepOwnContext(t *testing.T) {
	// 使用已关闭的客户端模拟 Redis 不可用，不连接真实 Redis 或业务数据库。
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	cache := redisstore.NewCatalogCache(client)
	firstContext, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	firstStarted := make(chan struct{})
	firstResult := make(chan error, 1)
	go func() {
		_, err := catalogRead(firstContext, cache, "same-application-and-query", func() ([]string, error) {
			close(firstStarted)
			<-firstContext.Done()
			return nil, firstContext.Err()
		})
		firstResult <- err
	}()
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("首个请求未开始查库")
	}
	secondContext := context.Background()
	type result struct {
		value []string
		err   error
	}
	secondResult := make(chan result, 1)
	go func() {
		value, err := catalogRead(secondContext, cache, "same-application-and-query", func() ([]string, error) {
			return []string{"second-request"}, secondContext.Err()
		})
		secondResult <- result{value: value, err: err}
	}()
	select {
	case second := <-secondResult:
		if second.err != nil || len(second.value) != 1 || second.value[0] != "second-request" {
			t.Fatalf("后到请求未独立返回自己的查询结果：%+v", second)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("后到请求仍在等待首个请求的回源任务")
	}
	cancelFirst()
	select {
	case err := <-firstResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("首个请求没有保留自己的取消结果：%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("首个请求取消后没有结束")
	}
	if secondContext.Err() != nil {
		t.Fatal("首个请求取消影响了其他请求的上下文")
	}
}
