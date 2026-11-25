package operations

// CampaignService 维护投放活动；内容目录仅用于验证目标的线上可见性。
type CampaignService struct {
	campaigns       CampaignRepository
	registry        ApplicationRepository
	catalog         CatalogRepository
	providerFactory TikTokMediaProviderFactory
}

// NewCampaignService 独立注入投放存储、目标查询和官方链接适配器。
func NewCampaignService(campaigns CampaignRepository, registry ApplicationRepository, catalog CatalogRepository, providerFactory TikTokMediaProviderFactory) *CampaignService {
	return &CampaignService{campaigns: campaigns, registry: registry, catalog: catalog, providerFactory: providerFactory}
}
