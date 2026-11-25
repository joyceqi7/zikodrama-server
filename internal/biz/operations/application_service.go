package operations

import "drama-server/internal/biz/subject"

// ApplicationService 维护应用身份，并通过明确端口检查广告位引用。
type ApplicationService struct {
	registry ApplicationRepository
	subjects subject.Repository
	settings MonetizationRepository
	catalog  MiniReviewCatalog
}

// NewApplicationService 仅注入应用维护需要的端口。
func NewApplicationService(registry ApplicationRepository, settings MonetizationRepository, subjects subject.Repository, catalogs ...MiniReviewCatalog) *ApplicationService {
	s := &ApplicationService{registry: registry, settings: settings, subjects: subjects}
	if len(catalogs) > 0 {
		s.catalog = catalogs[0]
	}
	return s
}
