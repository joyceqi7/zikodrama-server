package data

import (
	"drama-server/internal/biz/homepage"
)

// NewHomepageService 注入 MySQL 推荐配置、全局分类和线上目录；Redis 仅缓存可重建的首页结果。
func NewHomepageService(resources *Resources) *homepage.Service {
	return homepage.NewService(resources.store, resources.store, resources.store, &homepageResultCache{cache: resources.catalogCache})
}
