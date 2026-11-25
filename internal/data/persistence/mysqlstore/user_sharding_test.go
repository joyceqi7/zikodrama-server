package mysqlstore

import (
	"testing"

	"drama-server/internal/biz/sharding"
)

// TestLogicalBucketAndPhysicalTableAreStable 验证身份桶固定为 1024 个，并始终映射到 16 组物理表。
func TestLogicalBucketAndPhysicalTableAreStable(t *testing.T) {
	t.Parallel()
	first := logicalBucket("101", "openid-hash-a")
	if first != logicalBucket("101", "openid-hash-a") || first >= sharding.BucketCount {
		t.Fatalf("相同身份没有得到稳定逻辑桶: %d", first)
	}
	if got := userTable("users", first); got != userTable("users", first%sharding.TableShardCount) {
		t.Fatalf("逻辑桶没有稳定映射到物理表: %s", got)
	}
	seen := map[uint16]bool{}
	for i := 0; i < 4096; i++ {
		bucket := logicalBucket("101", string(rune(i))+"identity")
		seen[bucket%sharding.TableShardCount] = true
	}
	if len(seen) != int(sharding.TableShardCount) {
		t.Fatalf("测试身份未覆盖全部物理分片: %d", len(seen))
	}
}
