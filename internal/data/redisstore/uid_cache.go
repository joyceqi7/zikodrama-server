package redisstore

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"

	"drama-server/internal/data/idgen"

	"github.com/redis/go-redis/v9"
)

const (
	uidMaxSegmentSize = 10_000_000
	uidMaxDelay       = 24 * time.Hour
)

// UIDCache 保存可整体丢弃的共享号段游标；MySQL 已预留的号段永不回收。
// 应用重启复用已有状态；Redis 恢复旧快照前必须先隔离发号并丢弃旧缓存。
type UIDCache struct {
	client    redis.UniversalClient
	initErr   error
	closeOnce sync.Once
	closeErr  error
}

var _ idgen.Cache = (*UIDCache)(nil)

// NewUIDCache 从现有客户端复制配置，创建禁止命令重发且遵守上下文期限的专用连接池。
// 不修改原客户端，也不读取、初始化或清空缓存；调用方必须关闭返回的缓存。
func NewUIDCache(client redis.UniversalClient) *UIDCache {
	switch source := client.(type) {
	case *redis.Client:
		if source == nil {
			return &UIDCache{}
		}
		options := *source.Options()
		options.MaxRetries = -1
		options.ContextTimeoutEnabled = true
		if options.ReadTimeout <= 0 {
			options.ReadTimeout = 3 * time.Second
		}
		if options.WriteTimeout <= 0 {
			options.WriteTimeout = 3 * time.Second
		}
		if options.TLSConfig != nil {
			options.TLSConfig = options.TLSConfig.Clone()
		}
		if options.MaintNotificationsConfig != nil {
			config := *options.MaintNotificationsConfig
			options.MaintNotificationsConfig = &config
		}
		// 推送处理器持有连接状态，专用池不能沿用原客户端的处理器。
		options.PushNotificationProcessor = nil
		return &UIDCache{client: redis.NewClient(&options)}
	case *redis.ClusterClient:
		if source == nil {
			return &UIDCache{}
		}
		options := *source.Options()
		options.Addrs = append([]string(nil), options.Addrs...)
		options.MaxRetries = -1
		options.MaxRedirects = -1
		options.ContextTimeoutEnabled = true
		if options.ReadTimeout <= 0 {
			options.ReadTimeout = 3 * time.Second
		}
		if options.WriteTimeout <= 0 {
			options.WriteTimeout = 3 * time.Second
		}
		// 节点池也必须使用受控构造器，避免自定义工厂重新启用网络重试。
		options.NewClient = redis.NewClient
		if options.TLSConfig != nil {
			options.TLSConfig = options.TLSConfig.Clone()
		}
		if options.MaintNotificationsConfig != nil {
			config := *options.MaintNotificationsConfig
			options.MaintNotificationsConfig = &config
		}
		options.PushNotificationProcessor = nil
		return &UIDCache{client: redis.NewClusterClient(&options)}
	case nil:
		return &UIDCache{}
	default:
		return &UIDCache{initErr: fmt.Errorf("unsupported UID Redis client type %T", client)}
	}
}

// Close 仅关闭本缓存持有的专用连接池；可重复调用，不关闭构造时传入的共享客户端。
func (c *UIDCache) Close() error {
	if c == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		if c.client != nil {
			c.closeErr = c.client.Close()
		}
	})
	return c.closeErr
}

// 号段起点全程保留十进制字符串，仅容量、偏移和毫秒时间参与 Lua 数值运算。
// 每次写操作前完整校验 Hash，字段丢失或损坏不能被当成一个从零开始的新游标。
const uidStateLua = `
local function uint(value, maximum)
  if not value or not string.match(value, '^%d+$') or (#value > 1 and string.sub(value, 1, 1) == '0') then return nil end
  if #value > 16 then return nil end
  local number = tonumber(value)
  if not number or number > maximum then return nil end
  return number
end
local function start_valid(value)
  if not value or not string.match(value, '^[1-9]%d*$') then return false end
  return #value < 19 or (#value == 19 and value <= '9223372036854775807')
end
local function segment_valid(start, size)
  if size == 0 then return start == '' end
  if not start_valid(start) then return false end
  -- 用十进制减法计算最大起点，避免完整 UID 经 tonumber 后丢失精度。
  local maximum = '9223372036854775807'
  local borrow = size - 1
  local digits = {}
  for index = #maximum, 1, -1 do
    local subtract = borrow % 10
    borrow = math.floor(borrow / 10)
    local digit = tonumber(string.sub(maximum, index, index)) - subtract
    if digit < 0 then digit = digit + 10; borrow = borrow + 1 end
    digits[index] = tostring(digit)
  end
  local limit = table.concat(digits)
  return #start < #limit or (#start == #limit and start <= limit)
end
local function clock_ms()
  local now = redis.call('TIME')
  return tonumber(now[1]) * 1000 + math.floor(tonumber(now[2]) / 1000)
end
local function read_state(key)
  local values = redis.call('HMGET', key, 'schema', 'start', 'size', 'used', 'standby_start', 'standby_size', 'pending_token', 'pending_until', 'retry_at')
  for index = 1, 9 do
    if values[index] == false then return nil end
  end
  if values[1] ~= '1' then return nil end
  local size = uint(values[3], 10000000)
  local used = uint(values[4], 10000000)
  local standby_size = uint(values[6], 10000000)
  local pending_until = uint(values[8], 9007199254740991)
  local retry_at = uint(values[9], 9007199254740991)
  if not size or not used or not standby_size or not pending_until or not retry_at then return nil end
  if used > size or not segment_valid(values[2], size) or not segment_valid(values[5], standby_size) then return nil end
  if size == 0 and standby_size > 0 then return nil end
  if (values[7] == '') ~= (pending_until == 0) then return nil end
  if standby_size > 0 and values[7] ~= '' then return nil end
  if values[7] ~= '' and retry_at ~= 0 then return nil end
  return {start=values[2], size=size, used=used, standby_start=values[5], standby_size=standby_size, pending_token=values[7], pending_until=pending_until, retry_at=retry_at}
end
`

var nextUID = redis.NewScript(uidStateLua + `
local kind = redis.call('TYPE', KEYS[1]).ok
if kind == 'none' then
  redis.call('HSET', KEYS[1], 'schema', '1', 'start', '', 'size', '0', 'used', '0', 'standby_start', '', 'standby_size', '0', 'pending_token', '', 'pending_until', '0', 'retry_at', '0')
elseif kind ~= 'hash' then
  return redis.error_reply('invalid UID cache type')
end
local state = read_state(KEYS[1])
if not state then return redis.error_reply('invalid UID cache state') end
local now = clock_ms()
if state.used == state.size and state.standby_size > 0 then
  state.start = state.standby_start
  state.size = state.standby_size
  state.used = 0
  state.standby_start = ''
  state.standby_size = 0
  redis.call('HSET', KEYS[1], 'start', state.start, 'size', state.size, 'used', 0, 'standby_start', '', 'standby_size', 0)
end
local available = 0
local start = ''
local offset = 0
if state.used < state.size then
  available = 1
  start = state.start
  offset = state.used
  state.used = state.used + 1
  redis.call('HSET', KEYS[1], 'used', state.used)
end
local refill = 0
local retry_ms = 0
if state.standby_size == 0 and state.used * 5 >= state.size * 4 then
  if state.pending_token == ARGV[1] and state.pending_until > now then
    -- 只识别尚有效的持有者，不延长租约；客户端仍禁止重发结果未知的命令。
    refill = 1
  elseif state.pending_until <= now and state.retry_at <= now and state.pending_token ~= ARGV[1] then
    state.pending_token = ARGV[1]
    state.pending_until = now + tonumber(ARGV[2])
    state.retry_at = 0
    redis.call('HSET', KEYS[1], 'pending_token', state.pending_token, 'pending_until', state.pending_until, 'retry_at', 0)
    refill = 1
  end
  if available == 0 and refill == 0 then
    retry_ms = math.min(50, math.max(1, math.max(state.pending_until, state.retry_at) - now))
  end
end
return {start, offset, available, refill, retry_ms}
`)

var publishUID = redis.NewScript(uidStateLua + `
local kind = redis.call('TYPE', KEYS[1]).ok
if kind == 'none' then return 0 end
if kind ~= 'hash' then return redis.error_reply('invalid UID cache type') end
local state = read_state(KEYS[1])
if not state then return redis.error_reply('invalid UID cache state') end
if state.pending_token ~= ARGV[1] or state.pending_until <= clock_ms() then return 0 end
if state.standby_size > 0 then return 0 end
-- MySQL 水位只前进；同 token 被错误复用时，旧段也不能覆盖已经发布的当前段。
if state.size > 0 and (#ARGV[2] < #state.start or (#ARGV[2] == #state.start and ARGV[2] <= state.start)) then return 0 end
if state.used == state.size then
  redis.call('HSET', KEYS[1], 'start', ARGV[2], 'size', ARGV[3], 'used', 0)
else
  redis.call('HSET', KEYS[1], 'standby_start', ARGV[2], 'standby_size', ARGV[3])
end
redis.call('HSET', KEYS[1], 'pending_token', '', 'pending_until', 0, 'retry_at', 0)
return 1
`)

var backoffUID = redis.NewScript(uidStateLua + `
local kind = redis.call('TYPE', KEYS[1]).ok
if kind == 'none' then return 0 end
if kind ~= 'hash' then return redis.error_reply('invalid UID cache type') end
local state = read_state(KEYS[1])
if not state then return redis.error_reply('invalid UID cache state') end
local now = clock_ms()
if state.pending_token ~= ARGV[1] or state.pending_until <= now then return 0 end
redis.call('HSET', KEYS[1], 'pending_token', '', 'pending_until', 0, 'retry_at', now + tonumber(ARGV[2]))
return 1
`)

// Next 原子消费一个偏移并在消耗达到 80% 后竞争预取租约；缺少可用段时返回等待间隔。
// 每次调用必须使用新 token；请求结果未知时不得重发相同请求，只允许跳过已消费的 UID。
func (c *UIDCache) Next(ctx context.Context, key, token string, lease time.Duration) (idgen.Allocation, error) {
	if err := c.validateRequest(key, token); err != nil {
		return idgen.Allocation{}, err
	}
	if lease < time.Millisecond || lease > uidMaxDelay {
		return idgen.Allocation{}, fmt.Errorf("invalid UID refill lease")
	}
	values, err := nextUID.Run(ctx, c.client, []string{key}, token, lease.Milliseconds()).Slice()
	if err != nil {
		return idgen.Allocation{}, fmt.Errorf("consume UID cache: %w", err)
	}
	if len(values) != 5 {
		return idgen.Allocation{}, fmt.Errorf("invalid UID cache response length")
	}
	start, startOK := values[0].(string)
	offset, offsetOK := values[1].(int64)
	available, availableOK := values[2].(int64)
	refill, refillOK := values[3].(int64)
	retryMS, retryOK := values[4].(int64)
	if !startOK || !offsetOK || offset < 0 || offset >= uidMaxSegmentSize || !availableOK || available < 0 || available > 1 || !refillOK || refill < 0 || refill > 1 || !retryOK || retryMS < 0 || retryMS > 50 {
		return idgen.Allocation{}, fmt.Errorf("invalid UID cache response fields")
	}
	return idgen.Allocation{Start: start, Offset: uint32(offset), Available: available == 1, Refill: refill == 1, RetryAfter: time.Duration(retryMS) * time.Millisecond}, nil
}

// Publish 仅向相同且未过期的补段租约发布已提交段；重复发布和键丢失均返回 false。
// 发布结果未知时不能重新安装旧段；调用方应允许永久跳过该段。
func (c *UIDCache) Publish(ctx context.Context, key, token string, segment idgen.Segment) (bool, error) {
	if err := c.validateRequest(key, token); err != nil {
		return false, err
	}
	if segment.Start == 0 || segment.Start > math.MaxInt64 || segment.Size == 0 || segment.Size > uidMaxSegmentSize || uint64(segment.Size-1) > math.MaxInt64-segment.Start {
		return false, fmt.Errorf("invalid UID segment")
	}
	installed, err := publishUID.Run(ctx, c.client, []string{key}, token, strconv.FormatUint(segment.Start, 10), segment.Size).Int()
	if err != nil {
		return false, fmt.Errorf("publish UID cache segment: %w", err)
	}
	return installed == 1, nil
}

// Backoff 仅释放尚有效的相同租约并设置共享退避，保留仍可消费的当前号段。
func (c *UIDCache) Backoff(ctx context.Context, key, token string, delay time.Duration) error {
	if err := c.validateRequest(key, token); err != nil {
		return err
	}
	if delay < 0 || delay > uidMaxDelay {
		return fmt.Errorf("invalid UID refill backoff")
	}
	if err := backoffUID.Run(ctx, c.client, []string{key}, token, delay.Milliseconds()).Err(); err != nil {
		return fmt.Errorf("back off UID cache refill: %w", err)
	}
	return nil
}

func (c *UIDCache) validateRequest(key, token string) error {
	if c != nil && c.initErr != nil {
		return c.initErr
	}
	if c == nil || c.client == nil {
		return fmt.Errorf("UID Redis cache is unavailable")
	}
	if key == "" || token == "" {
		return fmt.Errorf("invalid UID cache key or refill token")
	}
	return nil
}
