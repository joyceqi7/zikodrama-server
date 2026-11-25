package mysqlstore

import (
	"context"
	"strings"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/operations"
	"drama-server/internal/biz/sharding"
	"gorm.io/gorm"
)

// consumerUserListQuery 每个分片按索引取 pageSize+1 条，再合并最多 16*(pageSize+1) 条候选；不执行 COUNT 或 OFFSET。
func consumerUserListQuery(db *gorm.DB, applicationID string, input operations.ConsumerUserQuery) *gorm.DB {
	placeholders := make([]string, 0, sharding.TableShardCount)
	sources := make([]any, 0, sharding.TableShardCount)
	for shard := uint16(0); shard < sharding.TableShardCount; shard++ {
		query := db.Session(&gorm.Session{}).Table(userTable("users", shard)).Select("id, application_id, CASE WHEN open_id_hash IS NULL THEN 'guest' ELSE 'tiktok' END AS identity_type, created_at").Where("application_id = ?", applicationID)
		if input.UserID != "" {
			query = query.Where("id = ?", input.UserID)
		}
		if input.AfterID != "" {
			// 拆开时间与主键比较，让联合索引可定位排他范围；同一时间的用户不能漏读或重复。
			query = query.Where("created_at < ? OR (created_at = ? AND id < ?)", input.AfterCreatedAt, input.AfterCreatedAt, input.AfterID)
		}
		query = query.Order("created_at DESC, id DESC").Limit(input.PageSize + 1)
		placeholders = append(placeholders, "(?)")
		sources = append(sources, query)
	}
	return db.Session(&gorm.Session{}).Table("("+strings.Join(placeholders, " UNION ALL ")+") AS consumer_users", sources...).Order("created_at DESC, id DESC").Limit(input.PageSize + 1)
}

// ListConsumerUsers 单条查询读取当前应用的跨分片游标页；新增用户通过刷新首页查看，页间不保持长期事务。
func (s *Store) ListConsumerUsers(ctx context.Context, input operations.ConsumerUserQuery) (operations.ConsumerUserPage, error) {
	result := operations.ConsumerUserPage{Items: []operations.ConsumerUser{}, PageSize: input.PageSize}
	applicationID := application.IDFromContext(ctx)
	if applicationID == "" {
		return result, operations.ErrInvalidInput
	}
	if err := consumerUserListQuery(s.data.DB().WithContext(ctx), applicationID, input).Scan(&result.Items).Error; err != nil {
		return result, err
	}
	if len(result.Items) > input.PageSize {
		result.HasMore = true
		result.Items = result.Items[:input.PageSize]
		result.NextCursor = operations.ConsumerUserCursor(applicationID, input.UserID, result.Items[len(result.Items)-1])
	}
	return result, nil
}
