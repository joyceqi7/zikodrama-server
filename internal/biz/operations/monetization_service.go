package operations

// MonetizationService 维护共享解锁策略及应用绑定，不依赖内容 Worker 或报表仓储。
type MonetizationService struct {
	settings MonetizationRepository
	registry ApplicationRepository
}

// NewMonetizationService 装配策略存储与广告位归属查询。
func NewMonetizationService(settings MonetizationRepository, registry ApplicationRepository) *MonetizationService {
	return &MonetizationService{settings: settings, registry: registry}
}
