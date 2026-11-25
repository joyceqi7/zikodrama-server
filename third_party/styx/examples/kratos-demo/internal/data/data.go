// Package data 演示由 Runtime 统一管理数据连接生命周期。
package data

import (
	"context"

	"github.com/google/wire"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
	styxmysql "gitee.com/linpbin/styx/mysql"
	styxredis "gitee.com/linpbin/styx/redis"
	styxruntime "gitee.com/linpbin/styx/runtime"
)

// ProviderSet 声明数据层 Wire 提供者。
var ProviderSet = wire.NewSet(
	NewDB,
	NewRedis,
	NewData,
	NewUserRepo,
)

// Data 聚合业务仓储所需的数据源。
type Data struct {
	db  *gorm.DB
	rdb redis.UniversalClient
}

// DB 返回 MySQL 连接。
func (d *Data) DB() *gorm.DB {
	return d.db
}

// Redis 返回 Redis 客户端。
func (d *Data) Redis() redis.UniversalClient {
	return d.rdb
}

// NewDB 创建 MySQL 连接，并把关闭职责交给 Runtime。
func NewDB(ctx context.Context, c *configv1.DataConfig, runtime *styxruntime.Runtime) (*gorm.DB, error) {
	if c == nil {
		return nil, nil
	}
	mysqlConf := c.GetMysql()["default"]
	if mysqlConf == nil {
		return nil, nil
	}
	return styxmysql.NewMySQL(ctx, "default", mysqlConf, runtime)
}

// NewRedis 创建 Redis 客户端，并把关闭职责交给 Runtime。
func NewRedis(ctx context.Context, c *configv1.DataConfig, runtime *styxruntime.Runtime) (redis.UniversalClient, error) {
	if c == nil {
		return nil, nil
	}
	redisConf := c.GetRedis()["cache"]
	if redisConf == nil {
		return nil, nil
	}
	return styxredis.New(ctx, "cache", redisConf, runtime)
}

// NewData 聚合 Wire 创建的数据源；资源关闭由 Runtime 负责。
func NewData(db *gorm.DB, rdb redis.UniversalClient) *Data {
	return &Data{
		db:  db,
		rdb: rdb,
	}
}
