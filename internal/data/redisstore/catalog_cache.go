package redisstore

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	catalogEntriesKey = "drama:{consumer-catalog}:entries"
	catalogVersionKey = "drama:{consumer-catalog}:version"
	catalogTTL        = 5 * time.Minute
	// 限制缓存总量，避免测试环境中挤占全局配置的持久化空间。
	catalogMaxEntries    = 256
	catalogMaxValueBytes = 256 * 1024
)

// CatalogCache 保存 C 端剧摘要与详情；与全局配置使用独立 Redis 键。
type CatalogCache struct {
	client  redis.UniversalClient
	entries string
	ttl     time.Duration
}

// NewCatalogCache 复用共享客户端，不缓存用户身份、权益或播放决策。
func NewCatalogCache(client redis.UniversalClient) *CatalogCache {
	if client == nil {
		return nil
	}
	return &CatalogCache{client: client, entries: catalogEntriesKey, ttl: catalogTTL}
}

// Bucket 返回共享失效版本的缓存分区；普通分页与热门一分钟，轮播五分钟，详情沿用默认分区。
func (c *CatalogCache) Bucket(kind string) *CatalogCache {
	if c == nil {
		return nil
	}
	next := *c
	switch kind {
	case "list", "hot":
		next.entries = catalogEntriesKey + ":" + kind
		next.ttl = time.Minute
	case "carousel":
		next.entries = catalogEntriesKey + ":carousel"
		next.ttl = 5 * time.Minute
	}
	return &next
}

// CatalogField 将应用、分页及过滤参数编码为固定长度字段名，不暴露搜索内容。
func CatalogField(kind string, input any) string {
	data, _ := json.Marshal(input)
	return fmt.Sprintf("%s:%x", kind, sha256.Sum256(data))
}

var readCatalog = redis.NewScript(`
redis.call('SET',KEYS[1],ARGV[1],'NX')
return {redis.call('GET',KEYS[1]),redis.call('HGET',KEYS[2],ARGV[2]) or ''}
`)
var fillCatalog = redis.NewScript(`
if redis.call('GET',KEYS[1]) ~= ARGV[1] then return 0 end
if redis.call('HEXISTS',KEYS[2],ARGV[2]) == 0 and redis.call('HLEN',KEYS[2]) >= tonumber(ARGV[5]) then return 0 end
redis.call('HSET',KEYS[2],ARGV[2],ARGV[3])
if redis.call('PTTL',KEYS[2]) < 0 then redis.call('PEXPIRE',KEYS[2],ARGV[4]) end
return 1
`)
var deleteCatalog = redis.NewScript(`
redis.call('SET',KEYS[1],ARGV[1])
return redis.call('DEL',KEYS[2],KEYS[3],KEYS[4],KEYS[5])
`)

func catalogNonce() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}

// Read 原子读取失效版本与缓存值；空值表示未命中。
func (c *CatalogCache) Read(ctx context.Context, field string) (version string, value []byte, err error) {
	nonce, err := catalogNonce()
	if err != nil {
		return "", nil, err
	}
	result, err := readCatalog.Run(ctx, c.client, []string{catalogVersionKey, c.entries}, nonce, field).Slice()
	if err != nil {
		return "", nil, err
	}
	if len(result) != 2 {
		return "", nil, fmt.Errorf("invalid catalog cache response")
	}
	version, ok := result[0].(string)
	if !ok {
		return "", nil, fmt.Errorf("invalid catalog cache version")
	}
	raw, ok := result[1].(string)
	if !ok {
		return "", nil, fmt.Errorf("invalid catalog cache value")
	}
	return version, []byte(raw), nil
}

// Fill 仅在查询期间未发生失效时回填；超限内容直接由数据库返回，不进入缓存。
func (c *CatalogCache) Fill(ctx context.Context, version, field string, value []byte) error {
	if version == "" || len(value) > catalogMaxValueBytes {
		return nil
	}
	return fillCatalog.Run(ctx, c.client, []string{catalogVersionKey, c.entries}, version, field, string(value), c.ttl.Milliseconds(), catalogMaxEntries).Err()
}

// Invalidate 原子删除全部目录页、详情和首页结果并更换版本，旧查询无法回填过期内容。
// 使用同一 Redis hash slot，不需要 KEYS、SCAN 或逐页枚举。
func (c *CatalogCache) Invalidate(ctx context.Context) error {
	nonce, err := catalogNonce()
	if err != nil {
		return err
	}
	return deleteCatalog.Run(ctx, c.client, []string{catalogVersionKey, catalogEntriesKey, catalogEntriesKey + ":list", catalogEntriesKey + ":hot", catalogEntriesKey + ":carousel"}, nonce).Err()
}
