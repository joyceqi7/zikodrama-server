package mysqlstore

import (
	"context"
	"strconv"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"drama-server/internal/biz/adminrbac"
)

// adminUserModel 映射管理员账号表；数字主键只在持久化边界使用，密码摘要仅用于认证。
type adminUserModel struct {
	// ID 是数据库分配的自增主键，转换为领域对象时使用十进制字符串。
	ID uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	// Username 是登录查询使用的规范化用户名。
	Username string `gorm:"column:username"`
	// PasswordHash 是 Service 生成的密码摘要，不是明文密码，不得写入日志。
	PasswordHash string `gorm:"column:password_hash"`
	// Nickname 是管理员展示昵称，不参与登录匹配。
	Nickname string `gorm:"column:nickname"`
	// Status 是账号启用状态；状态变更时同条 UPDATE 推进认证版本。
	Status string `gorm:"column:status"`
	// IsSuper 是可信超级管理员身份；普通账号更新语句不修改此字段。
	IsSuper bool `gorm:"column:is_super"`
	// AuthVersion 是认证版本，登录会话保存其快照以检测密码或状态变更。
	AuthVersion int64 `gorm:"column:auth_version"`
	// CreatedAt 是账号创建时间。
	CreatedAt time.Time `gorm:"column:created_at"`
	// UpdatedAt 是账号资料最近更新时间。
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

// TableName 指定管理员账号表，避免 ORM 依据类型名推导表名。
func (adminUserModel) TableName() string { return "admin_users" }

// domain 把数据库 uint64 主键无损转成十进制字符串；保留密码摘要供认证，API 返回前由 Service 清除。
func (model adminUserModel) domain() adminrbac.User {
	return adminrbac.User{
		ID: strconv.FormatUint(model.ID, 10), Username: model.Username, PasswordHash: model.PasswordHash, Nickname: model.Nickname,
		Status: model.Status, IsSuper: model.IsSuper, AuthVersion: model.AuthVersion,
		CreatedAt: model.CreatedAt, UpdatedAt: model.UpdatedAt,
	}
}

// adminUserModelFromDomain 构造新增账号记录并统一写入 UTC 时间；刻意不复制 ID，让数据库分配自增主键。
func adminUserModelFromDomain(user adminrbac.User) adminUserModel {
	return adminUserModel{
		Username: user.Username, PasswordHash: user.PasswordHash, Nickname: user.Nickname,
		Status: user.Status, IsSuper: user.IsSuper, AuthVersion: user.AuthVersion,
		CreatedAt: user.CreatedAt.UTC(), UpdatedAt: user.UpdatedAt.UTC(),
	}
}

// adminUserRoleModel 映射账号与角色的关联，一对主键只允许出现一次。
type adminUserRoleModel struct {
	// UserID 引用账号表 BIGINT UNSIGNED 主键，是复合主键成员且不自增。
	UserID uint64 `gorm:"column:user_id;primaryKey;autoIncrement:false"`
	// RoleID 引用角色表 BIGINT UNSIGNED 主键，是复合主键成员且不自增。
	RoleID uint64 `gorm:"column:role_id;primaryKey;autoIncrement:false"`
	// CreatedAt 是本次角色关联建立时间；整体替换时重新生成。
	CreatedAt time.Time `gorm:"column:created_at"`
}

// TableName 指定账号角色关联表。
func (adminUserRoleModel) TableName() string { return "admin_user_roles" }

// FindAdminUserByUsername 按调用方已规范化的用户名查询账号及密码摘要；没有记录时映射为 adminrbac.ErrNotFound。
func (s *Store) FindAdminUserByUsername(ctx context.Context, username string) (adminrbac.User, error) {
	var model adminUserModel
	err := s.data.DB().WithContext(ctx).Where("username = ?", username).First(&model).Error
	return model.domain(), adminError(err)
}

// FindAdminUserByID 严格解析规范十进制账号 ID 后查询，避免 MySQL 隐式转换误命中；非法 ID 与不存在记录均返回 adminrbac.ErrNotFound。
func (s *Store) FindAdminUserByID(ctx context.Context, id string) (adminrbac.User, error) {
	dbID, err := parseAdminID(id)
	if err != nil {
		return adminrbac.User{}, err
	}

	var model adminUserModel
	err = s.data.DB().WithContext(ctx).Where("id = ?", dbID).First(&model).Error
	return model.domain(), adminError(err)
}

// ListAdminUsers 按创建时间和 ID 读取全部账号；此 DAO 返回密码摘要，Service 必须在输出列表前清除。
func (s *Store) ListAdminUsers(ctx context.Context) ([]adminrbac.User, error) {
	var models []adminUserModel
	if err := s.data.DB().WithContext(ctx).Order("created_at, id").Find(&models).Error; err != nil {
		return nil, err
	}
	items := make([]adminrbac.User, 0, len(models))
	for _, model := range models {
		items = append(items, model.domain())
	}
	return items, nil
}

// CreateAdminUser 创建账号并回填数据库自增 ID；不接受调用方指定主键。
func (s *Store) CreateAdminUser(ctx context.Context, user *adminrbac.User) error {
	if user == nil || user.ID != "" {
		return adminrbac.ErrInvalidInput
	}
	model := adminUserModelFromDomain(*user)
	if err := adminError(s.data.DB().WithContext(ctx).Create(&model).Error); err != nil {
		return err
	}
	user.ID = strconv.FormatUint(model.ID, 10)
	return nil
}

// UpdateAdminUser 更新昵称与状态，依赖 Service 传入从数据库读取的可信 IsSuper；停用超级管理员时锁定启用集合以保护最后一名，状态变化同条语句推进认证版本。
func (s *Store) UpdateAdminUser(ctx context.Context, user adminrbac.User) error {
	dbID, err := parseAdminID(user.ID)
	if err != nil {
		return err
	}
	return s.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 先锁定同一启用集合，再判断是否最后一人，避免两个停用请求各自看到另一名管理员仍可用。
		if user.IsSuper && user.Status != adminrbac.StatusActive {
			var active []adminUserModel
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").
				Where("is_super = ? AND status = ?", true, adminrbac.StatusActive).Order("id").Find(&active).Error; err != nil {
				return err
			}
			if len(active) == 1 && active[0].ID == dbID {
				return adminrbac.ErrLastSuperAdmin
			}
		}
		// 认证版本与状态一起写入；只改昵称不增加版本，停用后再启用也不能恢复旧会话。
		result := tx.Model(&adminUserModel{}).Where("id = ?", dbID).Updates(map[string]any{
			"auth_version": gorm.Expr("auth_version + IF(status <> ?, 1, 0)", user.Status),
			"nickname":     user.Nickname, "status": user.Status, "updated_at": user.UpdatedAt.UTC(),
		})
		return adminMutationResult(result)
	})
}

// UpdateAdminPassword 按账号 ID 写入 Service 已生成的 hash，并在同条语句递增认证版本；不保存明文密码，旧会话在下次认证时失效。
func (s *Store) UpdateAdminPassword(ctx context.Context, id, hash string, updatedAt time.Time) error {
	dbID, err := parseAdminID(id)
	if err != nil {
		return err
	}

	result := s.data.DB().WithContext(ctx).Model(&adminUserModel{}).Where("id = ?", dbID).Updates(map[string]any{
		"password_hash": hash, "auth_version": gorm.Expr("auth_version + 1"), "updated_at": updatedAt.UTC(),
	})
	return adminMutationResult(result)
}

// ListAdminUserRoleIDs 按账号 ID 读取排序后的角色主键字符串；只查关联，空结果不区分账号不存在与未分配角色。
func (s *Store) ListAdminUserRoleIDs(ctx context.Context, userID string) ([]string, error) {
	dbID, err := parseAdminID(userID)
	if err != nil {
		return nil, err
	}

	var ids []string
	err = s.data.DB().WithContext(ctx).Model(&adminUserRoleModel{}).
		Where("user_id = ?", dbID).Order("role_id").Pluck("role_id", &ids).Error
	return ids, err
}

// ReplaceAdminUserRoles 在事务内锁定目标角色及账号并整体替换关联；账号不存在返回 ErrNotFound，角色缺失返回 ErrConflict，空集合清空，失败回滚且不推进认证版本。
func (s *Store) ReplaceAdminUserRoles(ctx context.Context, userID string, roleIDs []string) error {
	dbID, err := parseAdminID(userID)
	if err != nil {
		return err
	}

	roleNumbers, err := parseAdminIDs(roleIDs)
	if err != nil {
		return err
	}
	return s.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 按主键顺序持有角色锁，与删除角色互斥，避免并发写入失效引用。
		if len(roleNumbers) > 0 {
			var roles []adminRoleModel
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN ?", roleNumbers).Order("id").Find(&roles).Error; err != nil {
				return adminError(err)
			}
			if len(roles) != len(roleNumbers) {
				return adminrbac.ErrConflict
			}
		}
		var user adminUserModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, "id = ?", dbID).Error; err != nil {
			return adminError(err)
		}
		// 先删后建处于同一事务，任一新关联写入失败都保留原分配。
		if err := tx.Where("user_id = ?", dbID).Delete(&adminUserRoleModel{}).Error; err != nil {
			return adminError(err)
		}
		models := make([]adminUserRoleModel, 0, len(roleIDs))
		now := time.Now().UTC()
		for _, roleID := range roleNumbers {
			models = append(models, adminUserRoleModel{UserID: dbID, RoleID: roleID, CreatedAt: now})
		}
		if len(models) == 0 {
			return nil
		}
		return adminError(tx.Create(&models).Error)
	})
}

// RevokeAdminTokens 原子递增匹配的认证版本；旧 JWT 的重复退出不会撤销后来登录的凭据。
func (s *Store) RevokeAdminTokens(ctx context.Context, id string, version int64, updatedAt time.Time) error {
	dbID, err := parseAdminID(id)
	if err != nil {
		return err
	}
	return s.data.DB().WithContext(ctx).Model(&adminUserModel{}).
		Where("id = ? AND auth_version = ?", dbID, version).
		Updates(map[string]any{"auth_version": gorm.Expr("auth_version + 1"), "updated_at": updatedAt.UTC()}).Error
}
