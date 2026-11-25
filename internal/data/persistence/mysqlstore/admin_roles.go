package mysqlstore

import (
	"context"
	"strconv"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"drama-server/internal/biz/adminrbac"
)

// adminRoleModel 映射可配置的管理员角色；权限集合保存在独立关联表中。
type adminRoleModel struct {
	// ID 是数据库分配的自增主键，转换为领域对象时使用十进制字符串。
	ID uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	// Name 是管理员看到的角色名称。
	Name string `gorm:"column:name"`
	// Code 是角色的稳定业务编码，区别于数据库主键。
	Code string `gorm:"column:code"`
	// Remark 是角色用途说明。
	Remark string `gorm:"column:remark"`
	// Status 决定该角色是否参与有效权限查询。
	Status string `gorm:"column:status"`
	// CreatedAt 是角色创建时间。
	CreatedAt time.Time `gorm:"column:created_at"`
	// UpdatedAt 是角色资料最近更新时间。
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

// TableName 指定管理员角色表。
func (adminRoleModel) TableName() string { return "admin_roles" }

// domain 将数据库自增主键转成十进制字符串，返回角色资料而不加载权限关联。
func (model adminRoleModel) domain() adminrbac.Role {
	return adminrbac.Role{ID: strconv.FormatUint(model.ID, 10), Name: model.Name, Code: model.Code, Remark: model.Remark, Status: model.Status, CreatedAt: model.CreatedAt, UpdatedAt: model.UpdatedAt}
}

// adminRoleModelFromDomain 构造新角色的资料与 UTC 时间，不复制 ID 以使用数据库自增值。
func adminRoleModelFromDomain(role adminrbac.Role) adminRoleModel {
	return adminRoleModel{Name: role.Name, Code: role.Code, Remark: role.Remark, Status: role.Status, CreatedAt: role.CreatedAt.UTC(), UpdatedAt: role.UpdatedAt.UTC()}
}

// adminRolePermissionModel 保存角色明确分配的权限树节点，不复制编码或隐式展开后代。
type adminRolePermissionModel struct {
	// RoleID 引用角色自增主键，是复合主键成员且不自增。
	RoleID uint64 `gorm:"column:role_id;primaryKey;autoIncrement:false"`
	// PermissionID 引用权限目录自增主键，鉴权时再关联稳定权限 Code。
	PermissionID uint64 `gorm:"column:permission_id;primaryKey;autoIncrement:false"`
	// CreatedAt 是本次权限关联建立时间。
	CreatedAt time.Time `gorm:"column:created_at"`
}

// TableName 指定角色权限关联表。
func (adminRolePermissionModel) TableName() string { return "admin_role_permissions" }

// ListAdminRoles 按创建时间和 ID 读取角色资料；权限 ID 集合由独立关联查询补充。
func (s *Store) ListAdminRoles(ctx context.Context) ([]adminrbac.Role, error) {
	var models []adminRoleModel
	if err := s.data.DB().WithContext(ctx).Order("created_at, id").Find(&models).Error; err != nil {
		return nil, err
	}
	items := make([]adminrbac.Role, 0, len(models))
	for _, model := range models {
		items = append(items, model.domain())
	}
	return items, nil
}

// FindAdminRoleByID 按规范十进制角色标识查询，不存在时返回 ErrNotFound。
func (s *Store) FindAdminRoleByID(ctx context.Context, id string) (adminrbac.Role, error) {
	dbID, err := parseAdminID(id)
	if err != nil {
		return adminrbac.Role{}, err
	}

	var model adminRoleModel
	err = s.data.DB().WithContext(ctx).Where("id = ?", dbID).First(&model).Error
	return model.domain(), adminError(err)
}

// CreateAdminRole 创建角色并回填数据库自增 ID；不接受调用方指定主键。
func (s *Store) CreateAdminRole(ctx context.Context, role *adminrbac.Role) error {
	if role == nil || role.ID != "" {
		return adminrbac.ErrInvalidInput
	}
	model := adminRoleModelFromDomain(*role)
	if err := adminError(s.data.DB().WithContext(ctx).Create(&model).Error); err != nil {
		return err
	}
	role.ID = strconv.FormatUint(model.ID, 10)
	return nil
}

// UpdateAdminRole 按规范角色 ID 更新资料并映射数据库错误；本方法不检查受影响行数，目标存在性由 Service 预先查询。
func (s *Store) UpdateAdminRole(ctx context.Context, role adminrbac.Role) error {
	dbID, err := parseAdminID(role.ID)
	if err != nil {
		return err
	}
	result := s.data.DB().WithContext(ctx).Model(&adminRoleModel{}).Where("id = ?", dbID).Updates(map[string]any{
		"name": role.Name, "code": role.Code, "remark": role.Remark, "status": role.Status, "updated_at": role.UpdatedAt.UTC(),
	})
	return adminError(result.Error)
}

// DeleteAdminRole 锁定角色后拒绝账号引用，并在同一事务清理权限关联；不存在返回 ErrNotFound，被引用返回 ErrConflict。
func (s *Store) DeleteAdminRole(ctx context.Context, id string) error {
	dbID, err := parseAdminID(id)
	if err != nil {
		return err
	}

	return s.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var role adminRoleModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&role, "id = ?", dbID).Error; err != nil {
			return adminError(err)
		}
		// 角色分配也先锁定角色，避免检查后又有账号引用被删角色。
		var count int64
		if err := tx.Model(&adminUserRoleModel{}).Where("role_id = ?", dbID).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return adminrbac.ErrConflict
		}
		if err := tx.Where("role_id = ?", dbID).Delete(&adminRolePermissionModel{}).Error; err != nil {
			return adminError(err)
		}
		return adminMutationResult(tx.Delete(&role))
	})
}

// AdminRoleUserCount 统计引用角色的账号关联数，包括停用账号；空结果不说明角色本身是否存在。
func (s *Store) AdminRoleUserCount(ctx context.Context, roleID string) (int, error) {
	dbID, err := parseAdminID(roleID)
	if err != nil {
		return 0, err
	}

	var count int64
	err = s.data.DB().WithContext(ctx).Model(&adminUserRoleModel{}).Where("role_id = ?", dbID).Count(&count).Error
	return int(count), err
}

// ListAdminRolePermissionIDs 按角色 ID 查询排序后的权限主键字符串；空结果不区分角色不存在与未分配权限。
func (s *Store) ListAdminRolePermissionIDs(ctx context.Context, roleID string) ([]string, error) {
	dbID, err := parseAdminID(roleID)
	if err != nil {
		return nil, err
	}

	var ids []string
	err = s.data.DB().WithContext(ctx).Model(&adminRolePermissionModel{}).
		Where("role_id = ?", dbID).Order("permission_id").Pluck("permission_id", &ids).Error
	return ids, err
}

// ReplaceAdminRolePermissions 在同一事务锁定权限树和角色并整体替换关联；角色不存在返回 ErrNotFound，权限缺失返回 ErrConflict，空集合清空，失败回滚。
func (s *Store) ReplaceAdminRolePermissions(ctx context.Context, roleID string, permissionIDs []string) error {
	dbID, err := parseAdminID(roleID)
	if err != nil {
		return err
	}

	permissionNumbers, err := parseAdminIDs(permissionIDs)
	if err != nil {
		return err
	}
	return s.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 与权限删除共用树锁，保证已校验的权限在关联提交前不会消失。
		permissions, err := lockPermissionTree(tx)
		if err != nil {
			return err
		}
		existing := make(map[string]bool, len(permissions))
		for _, permission := range permissions {
			existing[permission.ID] = true
		}
		for _, permissionID := range permissionIDs {
			if !existing[permissionID] {
				return adminrbac.ErrConflict
			}
		}
		var role adminRoleModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&role, "id = ?", dbID).Error; err != nil {
			return adminError(err)
		}
		// 整体替换不是追加；事务保证新权限插入失败时旧权限集合不会丢失。
		if err := tx.Where("role_id = ?", dbID).Delete(&adminRolePermissionModel{}).Error; err != nil {
			return adminError(err)
		}
		models := make([]adminRolePermissionModel, 0, len(permissionIDs))
		now := time.Now().UTC()
		for _, permissionID := range permissionNumbers {
			models = append(models, adminRolePermissionModel{RoleID: dbID, PermissionID: permissionID, CreatedAt: now})
		}
		if len(models) == 0 {
			return nil
		}
		return adminError(tx.Create(&models).Error)
	})
}
