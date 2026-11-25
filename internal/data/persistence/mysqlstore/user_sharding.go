package mysqlstore

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strconv"

	"drama-server/internal/biz/sharding"
)

// logicalBucket 用稳定身份材料计算 1024 个逻辑桶；物理分片扩容不会改变结果。
func logicalBucket(applicationID, identity string) uint16 {
	sum := sha256.Sum256([]byte(applicationID + "\x00" + identity))
	return uint16(binary.BigEndian.Uint16(sum[:2]) % sharding.BucketCount)
}

// userTable 返回当前单主库中的用户域物理表名。
func userTable(base string, bucketID uint16) string {
	return fmt.Sprintf("%s_%02d", base, bucketID%sharding.TableShardCount)
}

// requestBucket 只接受凭据携带并写入 context 的可信桶号。
func requestBucket(ctx context.Context) (uint16, error) {
	bucketID, ok := sharding.BucketIDFromContext(ctx)
	if !ok {
		return 0, fmt.Errorf("user shard bucket is missing")
	}
	return bucketID, nil
}

// nextID 仅为新用户消费 Redis 共享的 MySQL 权威号段，并保持十进制字符串合同。
func (s *Store) nextID(ctx context.Context) (string, error) {
	id, err := s.ids.NextID(ctx)
	if err != nil {
		return "", err
	}
	return strconv.FormatUint(id, 10), nil
}
