package mysqlstore

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/plugin/dbresolver"

	"drama-server/internal/biz/visitor"
)

// CreateVisitor 仅供开发环境创建独立的匿名用户，不承载生产登录的中间状态。
func (s *Store) CreateVisitorRef(ctx context.Context, applicationID string) (visitor.UserRef, error) {
	now := time.Now().UTC()
	id, err := s.nextID(ctx)
	if err != nil {
		return visitor.UserRef{}, err
	}
	bucketID := logicalBucket(applicationID, id)
	model := consumerUserModel{ID: id, BucketID: bucketID, ApplicationID: applicationID, AuthVersion: 1, CreatedAt: now, UpdatedAt: now}
	if err := s.data.DB().WithContext(ctx).Table(userTable("users", bucketID)).Create(&model).Error; err != nil {
		return visitor.UserRef{}, err
	}
	return visitor.UserRef{ID: id, BucketID: bucketID, AuthVersion: 1}, nil
}

// VisitorExists 验证用户仍属于访问凭据声明的应用。
func (s *Store) VisitorExistsRef(ctx context.Context, applicationID string, user visitor.UserRef) (bool, error) {
	var count int64
	err := s.data.DB().WithContext(ctx).Clauses(dbresolver.Write).Table(userTable("users", user.BucketID)).Where("application_id = ? AND id = ? AND bucket_id = ?", applicationID, user.ID, user.BucketID).Count(&count).Error
	return count == 1, err
}

// VisitorCredentialVersionRef 从主库读取用户当前凭据版本，避免复制延迟误判刚恢复的登录。
func (s *Store) VisitorCredentialVersionRef(ctx context.Context, applicationID string, user visitor.UserRef) (uint64, bool, error) {
	var model consumerUserModel
	err := s.data.DB().WithContext(ctx).Clauses(dbresolver.Write).Table(userTable("users", user.BucketID)).
		Select("auth_version").Where("application_id = ? AND id = ? AND bucket_id = ?", applicationID, user.ID, user.BucketID).
		Take(&model).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return model.AuthVersion, true, nil
}

// ResolveTikTokIdentityRef 按应用内部 ID 和 OpenID 摘要从主库恢复用户及其凭据版本。
// 已存在的身份不访问发号缓存；首次登录消费共享 UID，身份唯一键冲突时复用已有主体。
// 应用或摘要为空时拒绝认证，Redis 或 MySQL 故障返回原错误供 Service 映射为可重试失败。
func (s *Store) ResolveTikTokIdentityRef(ctx context.Context, record visitor.TikTokIdentityRecord) (visitor.UserRef, error) {
	if record.ApplicationID == "" || record.OpenIDHash == "" {
		return visitor.UserRef{}, visitor.ErrUnauthorized
	}
	bucketID := logicalBucket(record.ApplicationID, record.OpenIDHash)
	// 主库路由必须保留；每个查询克隆干净会话，避免首次未找到的错误污染后续创建。
	db := s.data.DB().WithContext(ctx).Clauses(dbresolver.Write).Session(&gorm.Session{})
	table := userTable("users", bucketID)
	var existing consumerUserModel
	query := func() error {
		return db.Table(table).Select("id", "auth_version").Where("application_id = ? AND open_id_hash = ?", record.ApplicationID, record.OpenIDHash).Take(&existing).Error
	}
	if err := query(); err == nil {
		return visitor.UserRef{ID: existing.ID, BucketID: bucketID, AuthVersion: existing.AuthVersion}, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return visitor.UserRef{}, err
	}
	id, err := s.nextID(ctx)
	if err != nil {
		return visitor.UserRef{}, err
	}
	// 身份唯一键冲突时保留已存在的行；号段主键不会覆盖其他账号。
	candidate := consumerUserModel{ID: id, BucketID: bucketID, ApplicationID: record.ApplicationID, OpenIDHash: &record.OpenIDHash, AuthVersion: 1}
	if err := db.Table(table).Clauses(clause.OnConflict{DoUpdates: clause.Assignments(map[string]any{"id": gorm.Expr("id")})}).Create(&candidate).Error; err != nil {
		return visitor.UserRef{}, err
	}
	if err := query(); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return visitor.UserRef{}, visitor.ErrUnauthorized
		}
		return visitor.UserRef{}, err
	}
	return visitor.UserRef{ID: existing.ID, BucketID: bucketID, AuthVersion: existing.AuthVersion}, nil
}

// CreateVisitor 保留旧端口形态，生产 Service 会优先调用 CreateVisitorRef。
func (s *Store) CreateVisitor(ctx context.Context, applicationID string, visitorID *string) error {
	user, err := s.CreateVisitorRef(ctx, applicationID)
	if err == nil {
		*visitorID = user.ID
	}
	return err
}

// VisitorExists 为无桶号的内部兼容调用依次查询 16 个物理分片。
func (s *Store) VisitorExists(ctx context.Context, applicationID, visitorID string) (bool, error) {
	for shard := uint16(0); shard < 16; shard++ {
		var count int64
		if err := s.data.DB().WithContext(ctx).Clauses(dbresolver.Write).Table(userTable("users", shard)).Where("application_id = ? AND id = ?", applicationID, visitorID).Count(&count).Error; err != nil {
			return false, err
		}
		if count == 1 {
			return true, nil
		}
	}
	return false, nil
}

// ResolveTikTokIdentity 保留旧端口形态，生产 Service 会优先调用带桶结果的方法。
func (s *Store) ResolveTikTokIdentity(ctx context.Context, record visitor.TikTokIdentityRecord) (string, error) {
	user, err := s.ResolveTikTokIdentityRef(ctx, record)
	return user.ID, err
}
