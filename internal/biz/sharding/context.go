// Package sharding 保存已经由身份凭据校验的用户分片路由信息。
package sharding

import "context"

const (
	// BucketCount 是稳定逻辑桶数量，扩容物理库表时不得修改。
	BucketCount uint16 = 1024
	// TableShardCount 是单库初始物理分表数量。
	TableShardCount uint16 = 16
)

type bucketContextKey struct{}

// WithBucketID 把可信逻辑桶写入调用链；bucketID 必须由服务端身份解析得到。
func WithBucketID(ctx context.Context, bucketID uint16) context.Context {
	return context.WithValue(ctx, bucketContextKey{}, bucketID)
}

// BucketIDFromContext 返回可信逻辑桶；缺失时返回 found=false，存储层不得猜测路由。
func BucketIDFromContext(ctx context.Context) (bucketID uint16, found bool) {
	if ctx == nil {
		return 0, false
	}
	bucketID, found = ctx.Value(bucketContextKey{}).(uint16)
	return bucketID, found && bucketID < BucketCount
}
