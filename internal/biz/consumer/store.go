package consumer

import (
	"context"
	"errors"
	"time"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/monetization"
)

var (
	// ErrNotFound 表示记录不存在，或消费者无权看到该应用范围的已发布内容。
	ErrNotFound = errors.New("not found")
	// ErrRateLimited 表示奖励领取已达到服务端限制。
	ErrRateLimited = errors.New("rate limited")
	// ErrPlaybackCredential 表示播放解析缺少可信应用或访问主体等授权上下文。
	ErrPlaybackCredential = errors.New("playback credential is unavailable")
	// ErrInvalidDiscovery 表示发现页参数或分页位置无效。
	ErrInvalidDiscovery = errors.New("invalid discovery query")
)

// CatalogRepository 提供消费者目录读取能力。
// 该接口不包含保存、发布和媒资管理方法，避免消费者用例越过运营边界。
type CatalogRepository interface {
	// FindDramaDetail 读取可缓存的详情内容，不包含个人权益或播放授权结果。
	FindDramaDetail(context.Context, string) (*Drama, error)
	// DiscoverDramas 在存储层完成应用可见性过滤和分页，仅返回列表摘要。
	DiscoverDramas(context.Context, DiscoveryQuery) (DiscoveryPage, error)
	// ListDramas 读取目录聚合；应用授权、线上快照选择和语言投影由消费者 Service 处理。
	ListDramas(context.Context) ([]Drama, error)
	// FindDrama 按内部短剧标识读取聚合，未找到时返回 ErrNotFound；不接受官方 AlbumID。
	FindDrama(context.Context, string) (*Drama, error)
	// ListEntitlements 按访客标识和判定时间返回当前应用仍有效的分集权益，不包含目录级 VIP 权益。
	ListEntitlements(context.Context, string, time.Time) ([]monetization.EntitlementGrant, error)
	// FindOwnedEpisodeIDs 批量读取当前剧指定分集的有效访问权益，包含整剧和 VIP，不扫描其他剧。
	FindOwnedEpisodeIDs(context.Context, string, string, []string, time.Time) (map[string]bool, error)
}

// Store 是当前单体 MySQL 实现需要满足的组合端口。
// 分拆后的窄接口定义在领域层；组合接口只用于装配，不应作为新增用例的依赖类型。
type Store interface {
	CatalogRepository
	monetization.ConfigurationRepository
	application.AdUnitRepository
	monetization.BenefitRepository
	monetization.EntryAdRepository
	monetization.AdShowRepository
}
