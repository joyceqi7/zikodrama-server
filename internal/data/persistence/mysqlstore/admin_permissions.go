package mysqlstore

import (
	"context"
	"strconv"
	"time"

	"drama-server/internal/biz/adminrbac"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// adminPermissionModel 保存可自定义的权限树，角色通过稳定自增主键关联。
type adminPermissionModel struct {
	// ID 是数据库分配的节点标识。
	ID uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	// ParentID 为 NULL 时表示根节点。
	ParentID *uint64 `gorm:"column:parent_id"`
	// Name 是节点显示名称。
	Name string `gorm:"column:name"`
	// Code 是唯一权限码，操作节点与业务鉴权精确匹配。
	Code string `gorm:"column:code"`
	// Type 区分目录、菜单和操作。
	Type string `gorm:"column:type"`
	// Route 是页面节点绑定的站内路径。
	Route string `gorm:"column:route"`
	// Icon 是导航使用的 Lucide 图标名称。
	Icon string `gorm:"column:icon"`
	// SortOrder 是同级顺序。
	SortOrder int `gorm:"column:sort_order"`
	// Status 控制整棵子树是否生效。
	Status string `gorm:"column:status"`
	// Remark 是节点用途说明。
	Remark string `gorm:"column:remark"`
	// CreatedAt 是节点创建时间。
	CreatedAt time.Time `gorm:"column:created_at"`
	// UpdatedAt 是最近更新时间。
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

// TableName 指定后台权限树表。
func (adminPermissionModel) TableName() string { return "admin_permissions" }

func (model adminPermissionModel) domain() adminrbac.Permission {
	parent := ""
	if model.ParentID != nil {
		parent = strconv.FormatUint(*model.ParentID, 10)
	}
	return adminrbac.Permission{ID: strconv.FormatUint(model.ID, 10), ParentID: parent, Name: model.Name, Code: model.Code, Type: model.Type, Route: model.Route, Icon: model.Icon, SortOrder: model.SortOrder, Status: model.Status, Remark: model.Remark, CreatedAt: model.CreatedAt, UpdatedAt: model.UpdatedAt}
}

func permissionModel(permission adminrbac.Permission) (adminPermissionModel, error) {
	model := adminPermissionModel{Name: permission.Name, Code: permission.Code, Type: permission.Type, Route: permission.Route, Icon: permission.Icon, SortOrder: permission.SortOrder, Status: permission.Status, Remark: permission.Remark, CreatedAt: permission.CreatedAt.UTC(), UpdatedAt: permission.UpdatedAt.UTC()}
	if permission.ParentID != "" {
		id, err := parseAdminID(permission.ParentID)
		if err != nil {
			return model, err
		}
		model.ParentID = &id
	}
	return model, nil
}

// ListAdminPermissions 按同级顺序返回完整树资料，保留停用节点供维护。
func (s *Store) ListAdminPermissions(ctx context.Context) ([]adminrbac.Permission, error) {
	var models []adminPermissionModel
	if err := s.data.DB().WithContext(ctx).Order("sort_order, code").Find(&models).Error; err != nil {
		return nil, err
	}
	return permissionDomains(models), nil
}

func permissionDomains(models []adminPermissionModel) []adminrbac.Permission {
	items := make([]adminrbac.Permission, 0, len(models))
	for _, model := range models {
		items = append(items, model.domain())
	}
	return items
}

// FindAdminPermissionByID 按规范十进制主键查询节点。
func (s *Store) FindAdminPermissionByID(ctx context.Context, id string) (adminrbac.Permission, error) {
	number, err := parseAdminID(id)
	if err != nil {
		return adminrbac.Permission{}, err
	}
	var model adminPermissionModel
	err = s.data.DB().WithContext(ctx).Where("id = ?", number).First(&model).Error
	return model.domain(), adminError(err)
}

// lockPermissionTree 按主键顺序锁定整棵小规模权限树，移动和删除在同一事务内重新检查关联。
func lockPermissionTree(tx *gorm.DB) ([]adminrbac.Permission, error) {
	var models []adminPermissionModel
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Order("id").Find(&models).Error; err != nil {
		return nil, err
	}
	return permissionDomains(models), nil
}

// CreateAdminPermission 在锁定树后校验父节点并创建，防止并发移动破坏树结构。
func (s *Store) CreateAdminPermission(ctx context.Context, permission *adminrbac.Permission) error {
	if permission == nil || permission.ID != "" {
		return adminrbac.ErrInvalidInput
	}
	model, err := permissionModel(*permission)
	if err != nil {
		return err
	}
	err = s.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		items, err := lockPermissionTree(tx)
		if err != nil {
			return err
		}
		if err := adminrbac.ValidatePermissionChange(items, *permission); err != nil {
			return err
		}
		return adminError(tx.Create(&model).Error)
	})
	if err == nil {
		permission.ID = strconv.FormatUint(model.ID, 10)
	}
	return err
}

// UpdateAdminPermission 在同一事务内拒绝循环、无效层级和重复编码，保留角色关联。
func (s *Store) UpdateAdminPermission(ctx context.Context, permission adminrbac.Permission) error {
	id, err := parseAdminID(permission.ID)
	if err != nil {
		return err
	}
	model, err := permissionModel(permission)
	if err != nil {
		return err
	}
	return s.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		items, err := lockPermissionTree(tx)
		if err != nil {
			return err
		}
		found := false
		for _, item := range items {
			if item.ID == permission.ID {
				found = true
				break
			}
		}
		if !found {
			return adminrbac.ErrNotFound
		}
		if err := adminrbac.ValidatePermissionChange(items, permission); err != nil {
			return err
		}
		return adminError(tx.Model(&adminPermissionModel{}).Where("id = ?", id).Updates(map[string]any{
			"parent_id": model.ParentID, "name": model.Name, "code": model.Code, "type": model.Type, "route": model.Route, "icon": model.Icon, "sort_order": model.SortOrder, "status": model.Status, "remark": model.Remark, "updated_at": model.UpdatedAt,
		}).Error)
	})
}

// DeleteAdminPermission 与权限分配共用树锁，拒绝有子节点或角色引用的节点；不存在返回 ErrNotFound，被引用返回 ErrConflict。
func (s *Store) DeleteAdminPermission(ctx context.Context, id string) error {
	number, err := parseAdminID(id)
	if err != nil {
		return err
	}
	return s.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		items, err := lockPermissionTree(tx)
		if err != nil {
			return err
		}
		found := false
		for _, item := range items {
			if item.ParentID == id {
				return adminrbac.ErrConflict
			}
			if item.ID == id {
				found = true
			}
		}
		if !found {
			return adminrbac.ErrNotFound
		}
		var count int64
		if err := tx.Model(&adminRolePermissionModel{}).Where("permission_id = ?", number).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return adminrbac.ErrConflict
		}
		return adminMutationResult(tx.Where("id = ?", number).Delete(&adminPermissionModel{}))
	})
}

// ListAssignedAdminPermissionIDs 只返回启用角色直接分配的节点，不按目录展开后代。
func (s *Store) ListAssignedAdminPermissionIDs(ctx context.Context, userID string) ([]string, error) {
	id, err := parseAdminID(userID)
	if err != nil {
		return nil, err
	}
	var ids []string
	err = s.data.DB().WithContext(ctx).Table("admin_user_roles AS user_role").Distinct("role_permission.permission_id").
		Joins("JOIN admin_roles AS role ON role.id = user_role.role_id AND role.status = ?", adminrbac.StatusActive).
		Joins("JOIN admin_role_permissions AS role_permission ON role_permission.role_id = role.id").
		Where("user_role.user_id = ?", id).Order("role_permission.permission_id").Pluck("role_permission.permission_id", &ids).Error
	return ids, err
}
