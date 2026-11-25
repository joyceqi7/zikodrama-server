package mysqlstore

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"drama-server/internal/data/metricrefresh"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/plugin/dbresolver"
)

var errDashboardRefreshBusy = errors.New("dashboard refresh is already running")
var errDashboardRefreshLost = errors.New("dashboard refresh lease was superseded")

// dashboardRefreshLease 把可丢弃 Redis 互斥与 MySQL 权威发布资格关联起来。
type dashboardRefreshLease struct {
	store     *Store
	key       string
	owner     string
	state     metricSummaryState
	redisHeld bool
}

func (s *Store) dashboardRefreshKey(ctx context.Context, app string) (string, error) {
	var identity struct {
		// DatabaseName 防止同一 Redis 中不同逻辑库互相阻挡。
		DatabaseName string
		// ServerUUID 区分不同 MySQL 实例上的同名逻辑库。
		ServerUUID string
	}
	if err := s.metricWriteData().DB().WithContext(ctx).Clauses(dbresolver.Write).
		Table("information_schema.schemata").Select("schema_name AS database_name, @@server_uuid AS server_uuid").
		Where("schema_name = DATABASE()").Take(&identity).Error; err != nil {
		return "", err
	}
	if identity.DatabaseName == "" || identity.ServerUUID == "" {
		return "", errors.New("dashboard database identity is empty")
	}
	digest := sha256.Sum256([]byte(identity.DatabaseName + "\x00" + identity.ServerUUID))
	return fmt.Sprintf("drama:dashboard:refresh:%x:%s", digest, app), nil
}

// acquireDashboardRefresh 不等待其他计算者；行锁只存在于领取资格的短事务。
func (s *Store) acquireDashboardRefresh(ctx context.Context, app string, now time.Time, exists bool) (*dashboardRefreshLease, error) {
	lease := &dashboardRefreshLease{store: s, owner: rand.Text()}
	if s.metricRefreshLock != nil {
		key, err := s.dashboardRefreshKey(ctx, app)
		if err != nil {
			return nil, err
		}
		lease.key = key
		lockCtx, cancel := context.WithTimeout(ctx, time.Second)
		acquired, err := s.metricRefreshLock.Acquire(lockCtx, key, lease.owner, metricrefresh.LeaseDuration)
		cancel()
		if err != nil {
			// Redis 只抑制重复工作；故障时仍由 MySQL 条件更新保证唯一发布资格。
			slog.WarnContext(ctx, "acquire dashboard Redis lock failed; using MySQL lease", "error", err)
		} else if !acquired {
			return nil, errDashboardRefreshBusy
		} else {
			lease.redisHeld = true
		}
	}
	claimed := false
	err := s.metricWriteData().DB().WithContext(ctx).Clauses(dbresolver.Write).Transaction(func(tx *gorm.DB) error {
		if !exists {
			state := metricSummaryState{ApplicationID: app}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&state).Error; err != nil {
				return err
			}
		}
		result := tx.Model(&metricSummaryState{}).
			Where("application_id = ? AND (refresh_lease_until IS NULL OR refresh_lease_until <= UTC_TIMESTAMP(6))", app).
			Where("refreshed_at IS NULL OR refreshed_at <= ?", now.Add(-time.Minute)).
			Updates(map[string]any{"refresh_owner": lease.owner,
				"refresh_generation":  gorm.Expr("refresh_generation + 1"),
				"refresh_lease_until": gorm.Expr("TIMESTAMPADD(SECOND, ?, UTC_TIMESTAMP(6))", int(metricrefresh.LeaseDuration/time.Second))})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		claimed = true
		return tx.Where("application_id = ?", app).Take(&lease.state).Error
	})
	if err != nil || !claimed {
		lease.release(ctx)
		if err != nil {
			return nil, err
		}
		return nil, errDashboardRefreshBusy
	}
	return lease, nil
}

// renew 延长两层租约；任意层失去持有资格都停止当前计算，提交仍独立校验数据库版本。
func (l *dashboardRefreshLease) renew(ctx context.Context) error {
	if l.redisHeld {
		ok, err := l.store.metricRefreshLock.Renew(ctx, l.key, l.owner, metricrefresh.LeaseDuration)
		if err != nil {
			return err
		}
		if !ok {
			return errDashboardRefreshLost
		}
	}
	result := l.store.metricWriteData().DB().WithContext(ctx).Clauses(dbresolver.Write).Model(&metricSummaryState{}).
		Where("application_id = ? AND refresh_owner = ? AND refresh_generation = ? AND refresh_lease_until > UTC_TIMESTAMP(6)", l.state.ApplicationID, l.owner, l.state.RefreshGeneration).
		Update("refresh_lease_until", gorm.Expr("TIMESTAMPADD(SECOND, ?, UTC_TIMESTAMP(6))", int(metricrefresh.LeaseDuration/time.Second)))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errDashboardRefreshLost
	}
	return nil
}

// keepAlive 的停止函数等待续期协程退出，避免提交后仍有迟到续期或释放。
func (l *dashboardRefreshLease) keepAlive(ctx context.Context) (context.Context, func()) {
	jobCtx, cancel := context.WithCancelCause(ctx)
	heartbeatCtx, stop := context.WithCancel(jobCtx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(metricrefresh.RenewInterval)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				renewCtx, stop := context.WithTimeout(jobCtx, 3*time.Second)
				err := l.renew(renewCtx)
				stop()
				if err != nil {
					slog.ErrorContext(jobCtx, "renew dashboard refresh lease failed", "error", err)
					cancel(err)
					return
				}
			}
		}
	}()
	return jobCtx, func() { stop(); <-done }
}

func (l *dashboardRefreshLease) release(ctx context.Context) {
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	// 未领取成功时 state.ApplicationID 为空，只需安全清理可能持有的 Redis 锁。
	if l.state.ApplicationID != "" {
		err := l.store.metricWriteData().DB().WithContext(releaseCtx).Clauses(dbresolver.Write).Model(&metricSummaryState{}).
			Where("application_id = ? AND refresh_owner = ? AND refresh_generation = ?", l.state.ApplicationID, l.owner, l.state.RefreshGeneration).
			Updates(map[string]any{"refresh_owner": "", "refresh_lease_until": nil}).Error
		if err != nil {
			slog.ErrorContext(releaseCtx, "release dashboard MySQL lease failed", "error", err)
		}
	}
	if l.redisHeld {
		if err := l.store.metricRefreshLock.Release(releaseCtx, l.key, l.owner); err != nil {
			slog.ErrorContext(releaseCtx, "release dashboard Redis lock failed", "error", err)
		}
	}
}
