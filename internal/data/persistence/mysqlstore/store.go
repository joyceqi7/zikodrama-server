package mysqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
	styxmysql "gitee.com/linpbin/styx/mysql"
	styxruntime "gitee.com/linpbin/styx/runtime"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/data/idgen"
	"drama-server/internal/data/metricrefresh"
)

// Data 聚合 Styx 创建的 GORM 数据源与仅用于健康检查的底层连接池。
// 业务 Repository 只能通过 DB 返回的 GORM 会话访问 MySQL。
type Data struct {
	// 保存数据库连接。
	db *gorm.DB
	// 保存连接池。
	pool *sql.DB
}

// 创建并返回数据实例。
func NewData(db *gorm.DB) (*Data, error) {
	if db == nil {
		return &Data{}, nil
	}
	pool, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("获取 MySQL 连接池: %w", err)
	}
	return &Data{db: db, pool: pool}, nil
}

// 返回底层数据库连接。
func (data *Data) DB() *gorm.DB {
	if data == nil {
		return nil
	}
	return data.db
}

// 定义存储。
type Store struct {
	// 保存数据。
	data *Data
	// ids 仅为消费者用户分配 MySQL 权威号段主键；奖励、权益和 VIP 进度由各物理表自增。
	ids *segmentIDAllocator
	// metricTableState 保存可丢弃的进程内分表准备状态；MySQL DDL 仍保证多实例一致。
	metricTableState metricTableState
	// metricRefreshLock 仅减少重复刷新；数据库租约及发布版本始终保留。
	metricRefreshLock metricrefresh.Locker
	// metricReadData 将重统计导向独立连接池，不改变业务写入及维护清理的连接。
	metricReadData *Data
	// metricCoordData 为短租约与发布保留业务连接，避免统计池耗尽时无法续期。
	metricCoordData *Data
}

// ConfigureMetricRefresh 在启动装配阶段注入刷新协调和只读计算连接，运行期间不得修改。
func (s *Store) ConfigureMetricRefresh(lock metricrefresh.Locker, readData *Data, coordination ...*Data) {
	s.metricRefreshLock, s.metricReadData = lock, readData
	if len(coordination) > 0 {
		s.metricCoordData = coordination[0]
	}
}

// metricWriteData 将刷新协调的短写入与重统计连接隔离，未装配时兼容单池仓储。
func (s *Store) metricWriteData() *Data {
	if s.metricCoordData != nil {
		return s.metricCoordData
	}
	return s.data
}

// OpenData 将 Bootstrap 中的 MySQLConfig 原样交给 Styx；连接池、读副本、Telemetry
// 和关闭顺序全部由 Styx Runtime 负责。本层只把 GORM 投影为业务仓储所需的 Data，
// 不重新解释 MySQL 配置，也不保存第二个 Close 入口。
func OpenData(ctx context.Context, styxConfig *configv1.MySQLConfig, runtime *styxruntime.Runtime) (*Data, error) {
	return openData(ctx, "drama", styxConfig, runtime)
}

// OpenReportingData 创建独立的运营统计连接池，连接和指标由 Runtime 按 drama-reporting 单独管理。
// 统计与业务仍访问同一逻辑库；刷新重计算走此池，租约及发布走业务池。
func OpenReportingData(ctx context.Context, styxConfig *configv1.MySQLConfig, runtime *styxruntime.Runtime) (*Data, error) {
	return openData(ctx, "drama-reporting", styxConfig, runtime)
}

// openData 每次调用都创建新的底层连接池，不使用 GORM Session 冒充资源隔离。
func openData(ctx context.Context, name string, styxConfig *configv1.MySQLConfig, runtime *styxruntime.Runtime) (*Data, error) {
	orm, err := styxmysql.NewMySQL(ctx, name, styxConfig, runtime)
	if err != nil {
		return nil, fmt.Errorf("initialize styx mysql: %w", err)
	}
	return NewData(orm)
}

// NewStore 从共享 Data 创建仓储；权益写入以 MySQL 用户行锁为准。
// caches 的首个缓存用于共享 UID 发号；省略时仍可查询及写入其他业务，但不能创建用户。
func NewStore(data *Data, caches ...idgen.Cache) *Store {
	var cache idgen.Cache
	if len(caches) > 0 {
		cache = caches[0]
	}
	return &Store{data: data, ids: newSegmentIDAllocator(data, consumerIDNamespace, cache)}
}

// readSnapshot 在数据源边界设置只读可重复读选项，报表业务继续只使用 GORM 查询与事务。
func (s *Store) readSnapshot(ctx context.Context, fn func(*gorm.DB) error) error {
	if s == nil || s.data == nil || s.data.DB() == nil {
		return fmt.Errorf("report snapshot MySQL unavailable: %w", domain.ErrServiceUnavailable)
	}
	return s.data.DB().WithContext(ctx).Transaction(fn, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
}

// userTransaction 在权威用户行上取得事务锁，使奖励、权益及 VIP 进度写入跨实例串行。
// 锁与业务变更共用同一 MySQL 事务；Redis 不可用时不影响持久写入。
func (s *Store) userTransaction(ctx context.Context, applicationID, visitorID string, fn func(*gorm.DB) error) error {
	bucketID, err := requestBucket(ctx)
	if err != nil {
		return err
	}
	if s == nil || s.data == nil || s.data.DB() == nil {
		return fmt.Errorf("user transaction MySQL unavailable: %w", domain.ErrServiceUnavailable)
	}
	return s.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user consumerUserModel
		err := tx.Table(userTable("users", bucketID)).Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id").Where("bucket_id = ? AND application_id = ? AND id = ?", bucketID, applicationID, visitorID).Take(&user).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("user row missing in bucket %d: %w: %w", bucketID, domain.ErrServiceUnavailable, err)
		}
		if err != nil {
			return err
		}
		return fn(tx)
	})
}

// gormAffectedOrNotFound 把条件更新或删除的零影响行统一映射为领域 NotFound。
func gormAffectedOrNotFound(result *gorm.DB) error {
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// Ping 只检查 Data 持有的主连接池，不执行业务查询。
func (data *Data) Ping(ctx context.Context) error {
	if data == nil || data.pool == nil {
		return errors.New("mysql store is not configured")
	}
	return data.pool.PingContext(ctx)
}
