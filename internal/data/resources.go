// Package data 负责创建数据资源和外部适配器，并将具体实现绑定到业务端口。
// cmd 只负责应用装配，不应知道 MySQL 的连接细节，也不应直接执行
// 数据库迁移或初始化种子。具体 Store 可以同时实现多个端口，但上层只能通过这里的
// Provider 获得所需的窄接口，不能依赖一个包含全部数据能力的“万能 Store”。
package data

import (
	"context"
	"fmt"
	"log/slog"

	styxredis "gitee.com/linpbin/styx/redis"
	styxruntime "gitee.com/linpbin/styx/runtime"
	"github.com/redis/go-redis/v9"

	"drama-server/internal/biz/adminrbac"
	domainapplication "drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"drama-server/internal/biz/subject"
	"drama-server/internal/biz/visitor"
	conf "drama-server/internal/conf"
	"drama-server/internal/data/persistence/mysqlstore"
	"drama-server/internal/data/redisstore"
	"drama-server/internal/pkg/health"
)

// Resources 持有进程内共享的主数据资源。
// 它只向上层暴露业务仓储和就绪检查；MySQL 的创建、连接池配置和关闭顺序由 Styx
// Runtime 统一负责，Resources 不再提供第二套资源所有权。
type Resources struct {
	// store 是 data 层内部的具体 MySQL 实现。具体类型只留在组合根，
	// 向 biz 层返回时会被投影为下方的窄业务端口。
	store *mysqlstore.Store
	// reportingStore 仅供运营统计查询使用，与业务仓储连接同一库但不争用连接池。
	reportingStore *mysqlstore.Store
	// redis 仅持有可丢弃缓存连接，由 Runtime 关闭；缓存初始化失败时为空。
	redis redis.UniversalClient
	// catalogCache 只保存可重建的 C 端目录，业务配置独立持久化。
	catalogCache *redisstore.CatalogCache
	// 保存就绪检查。
	ready func(context.Context) error
}

// NewResources 直接消费 data 配置中的 Styx MySQL 参数并创建业务仓储。
// 数据库 Schema 和业务数据必须在部署服务前独立初始化；服务进程只连接并校验数据库，
// 不在启动期间创建或修改表，也不写入业务种子。所有部署环境都必须配置共享 MySQL；禁止
// 回退到进程内仓储，因为每个实例都会同时承载 HTTP 与后台任务。
// 业务与统计连接配置分别必填；统计配置不从业务配置补齐地址、凭据或连接池参数。
func NewResources(ctx context.Context, dataConfig *conf.Data, _ conf.Config, runtime *styxruntime.Runtime) (*Resources, error) {
	if dataConfig == nil || dataConfig.GetMysql() == nil || !conf.Configured(dataConfig.GetMysql().GetMaster()) {
		err := fmt.Errorf("%w: data.mysql.master is required for multi-instance deployment", conf.ErrInvalid)
		slog.ErrorContext(ctx, "validate MySQL data configuration failed", "error", err)
		return nil, err
	}
	// 在创建任一连接池前拒绝缺失的统计连接，避免静默连接到业务主库。
	if !conf.Configured(dataConfig.GetReportingMysql().GetMaster()) {
		err := fmt.Errorf("%w: data.reporting_mysql.master is required for the reporting connection pool", conf.ErrInvalid)
		slog.ErrorContext(ctx, "validate reporting MySQL data configuration failed", "error", err)
		return nil, err
	}
	mysqlData, err := mysqlstore.OpenData(ctx, dataConfig.GetMysql(), runtime)
	if err != nil {
		slog.ErrorContext(ctx, "initialize MySQL data resources failed", "error", err)
		return nil, err
	}
	reportingData, err := mysqlstore.OpenReportingData(ctx, dataConfig.GetReportingMysql(), runtime)
	if err != nil {
		slog.ErrorContext(ctx, "initialize reporting MySQL data resources failed", "error", err)
		return nil, err
	}
	redisClient, err := styxredis.New(ctx, "global-config", dataConfig.GetRedis(), runtime)
	if err != nil {
		if ctx.Err() != nil {
			slog.ErrorContext(ctx, "initialize cache interrupted", "error", err)
			return nil, err
		}
		// 目录和配置读取仍可回源 MySQL；已有用户继续登录，新用户因无法共享发号而暂时失败。
		// 不创建本地号段；本进程需重启以重新初始化缓存连接。
		slog.ErrorContext(ctx, "initialize Redis cache failed; serving from MySQL", "error", err)
		redisClient = nil
	}
	// UID 命令使用独立且禁止网络重发的连接池，避免迟到请求把旧补段租约重新激活。
	uidCache := redisstore.NewUIDCache(redisClient)
	if err := runtime.Own("redis uid allocator", styxruntime.BoundedClose(uidCache.Close)); err != nil {
		slog.ErrorContext(ctx, "register UID cache lifecycle failed", "error", err)
		if closeErr := uidCache.Close(); closeErr != nil {
			slog.ErrorContext(ctx, "close unregistered UID cache failed", "error", closeErr)
		}
		return nil, err
	}
	store := mysqlstore.NewStore(mysqlData, uidCache)
	reportingStore := mysqlstore.NewStore(reportingData)
	refreshLock := redisstore.NewMetricRefreshLock(redisClient)
	// API 与维护任务使用同一协调协议；重计算统一占用独立统计池。
	store.ConfigureMetricRefresh(refreshLock, reportingData)
	reportingStore.ConfigureMetricRefresh(refreshLock, reportingData, mysqlData)
	return &Resources{
		store: store, reportingStore: reportingStore, redis: redisClient, catalogCache: redisstore.NewCatalogCache(redisClient),
		ready: func(ctx context.Context) error {
			if err := mysqlData.Ping(ctx); err != nil {
				slog.ErrorContext(ctx, "check MySQL readiness failed", "error", err)
				return err
			}
			return nil
		},
	}, nil
}

// 以下 Provider 是基础设施实现与应用/领域端口之间的装配边界。
// 当前单体后端由同一个 Store 实现多个能力，但每个用例只获得对应的 Repo 类型；后续拆表、
// 拆库或远程化时可独立替换某个 Repo，而不需要修改 biz 或 service。
type ConsumerRepository struct {
	// 嵌入消费者领域定义的数据存储端口。
	domain.Store
	// EntryPreviewRepository 允许消费者为已校验的直连首集查找或准备独立片段。
	domain.EntryPreviewRepository
}

// 定义管理端数据仓库。
type AdminRepository struct {
	// 嵌入管理权限领域定义的数据仓库端口。
	adminrbac.Repository
}

// VisitorRepository 只暴露匿名 Visitor、可选 TikTok 身份持久化与投放解析端口。
// 投放解析必须保留在这个窄投影中，否则 visitor.Service 对 CampaignResolver 的
// 可选能力探测会失败，导致有效 campaignCode 被当作自然流量入口。
type VisitorRepository struct {
	visitor.Store
	// ShardedStore 保留用户的稳定逻辑桶，避免包装后身份服务退回无桶接口并签发桶 0 的凭据。
	visitor.ShardedStore
	visitor.CampaignResolver
}

// 编译时保证生产身份仓储装配不会丢失分片路由能力。
var _ visitor.ShardedStore = (*VisitorRepository)(nil)

// 定义应用数据仓库。
type ApplicationRepository struct {
	// 嵌入应用聚合声明的仓储端口；data 层不重复定义同构接口。
	domainapplication.Repository
}

// 创建并返回消费者数据仓库实例。
func NewConsumerRepository(resources *Resources) *ConsumerRepository {
	return &ConsumerRepository{Store: &cachedConsumerStore{Store: resources.store, cache: resources.catalogCache}, EntryPreviewRepository: resources.store}
}

// NewOperationsRepositories 在组合根显式完成 operations 窄端口装配。
// 这里虽然由同一个 MySQL Store 实现多个端口，biz 层并不知道它们是同一对象；
// 任一端口以后改为独立表、独立库或远程服务时，只需修改这个装配点。
func NewOperationsRepositories(resources *Resources) operations.Repositories {
	reports := &reportingRepository{Store: resources.reportingStore, maintenance: resources.store}
	return operations.Repositories{
		ConsumerUsers: resources.store,
		Application: operations.ApplicationPorts{
			Registry: resources.store, Subjects: resources.store,
		},
		Monetization: operations.MonetizationPorts{Settings: resources.store},
		Campaign:     operations.CampaignPorts{Repository: resources.store},
		Content:      operations.ContentPorts{Records: resources.store, Tags: &invalidatingCatalogStore{catalogPersistence: resources.store, cache: resources.catalogCache}, OwnerApplication: subjectOwner(resources), OwnerApplicationID: subject.NewService(resources.store).OwnerID, Distributions: &invalidatingDistributionStore{DistributionRepository: resources.store, cache: resources.catalogCache}, Catalog: &invalidatingCatalogStore{catalogPersistence: resources.store, cache: resources.catalogCache}, Publications: &invalidatingCatalogStore{catalogPersistence: resources.store, cache: resources.catalogCache}},
		Media:        operations.MediaPorts{Jobs: resources.store},
		Reporting:    operations.ReportingPorts{Metrics: reports, Audits: resources.store, Postbacks: reports, Ads: reports},
	}
}

// 创建并返回管理端数据仓库实例。
func NewAdminRepository(resources *Resources) *AdminRepository {
	return &AdminRepository{Repository: resources.store}
}

// NewVisitorRepository 创建 消费者身份仓储投影。
func NewVisitorRepository(resources *Resources) *VisitorRepository {
	return &VisitorRepository{
		Store:            resources.store,
		ShardedStore:     resources.store,
		CampaignResolver: resources.store,
	}
}

// 创建并返回应用数据仓库实例。
func NewApplicationRepository(resources *Resources) *ApplicationRepository {
	return &ApplicationRepository{Repository: resources.store}
}

// 创建并返回健康状态实例。
func NewHealthCheck(resources *Resources) health.Check {
	return func(ctx context.Context) error {
		if resources == nil || resources.ready == nil {
			return fmt.Errorf("shared MySQL is unavailable")
		}
		if err := resources.ready(ctx); err != nil {
			return err
		}
		return nil
	}
}

// NewAnalyticsService 将指标窄端口注入业务用例，不改变播放和奖励仓储。
func NewAnalyticsService(resources *Resources) *domain.AnalyticsService {
	return domain.NewAnalyticsService(resources.store)
}
