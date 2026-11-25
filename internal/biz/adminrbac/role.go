package adminrbac

import (
	"context"
	"log/slog"
	"strings"
	"time"
)

// Role 聚合一组可分配给管理员的权限。
type Role struct {
	// ID 是数据库自增主键，以十进制字符串传递。
	ID string `json:"id"`
	// Name 是后台展示名称，角色之间不能重复。
	Name string `json:"name"`
	// Code 是规范化为小写的业务编码，角色之间不能重复。
	Code string `json:"code"`
	// Remark 是运营维护的角色用途说明。
	Remark string `json:"remark"`
	// Status 控制角色是否参与有效权限计算。
	Status string `json:"status"`
	// PermissionIDs 是角色分配的权限主键，由关联仓储单独读取。
	PermissionIDs []string `json:"permissionIds"`
	// CreatedAt 记录角色创建时间。
	CreatedAt time.Time `json:"createdAt"`
	// UpdatedAt 记录角色资料最近一次更新时间。
	UpdatedAt time.Time `json:"updatedAt"`
}

// RoleInput 定义由入口规范化并校验的角色资料，不接受主键或权限分配。
type RoleInput struct {
	// Name 是后台展示名称，角色之间不能重复。
	Name string `json:"name"`
	// Code 是规范化为小写的业务编码，角色之间不能重复。
	Code string `json:"code"`
	// Remark 是运营维护的角色用途说明。
	Remark string `json:"remark"`
	// Status 控制角色是否参与有效权限计算。
	Status string `json:"status"`
}

// RoleRepository 声明角色和角色权限分配功能需要的持久化能力。
type RoleRepository interface {
	// ListAdminRoles 列出全部角色，权限关联由服务层补齐。
	ListAdminRoles(context.Context) ([]Role, error)
	// FindAdminRoleByID 按角色标识读取资料，不存在时返回 ErrNotFound。
	FindAdminRoleByID(context.Context, string) (Role, error)
	// CreateAdminRole 保存角色并回填数据库自增 ID，输入 ID 必须为空；名称或编码冲突时返回 ErrConflict。
	CreateAdminRole(context.Context, *Role) error
	// UpdateAdminRole 更新角色资料，保留主键和权限关联。
	UpdateAdminRole(context.Context, Role) error
	// DeleteAdminRole 删除角色及其权限关联；已被账号引用时返回 ErrConflict。
	DeleteAdminRole(context.Context, string) error
	// AdminRoleUserCount 统计角色被账号引用的数量，供删除门禁使用。
	AdminRoleUserCount(context.Context, string) (int, error)
	// ListAdminRolePermissionIDs 返回角色直接分配的权限标识。
	ListAdminRolePermissionIDs(context.Context, string) ([]string, error)
	// ReplaceAdminRolePermissions 原子替换角色的全部权限关联，空列表表示移除全部权限。
	ReplaceAdminRolePermissions(context.Context, string, []string) error
}

// ListRoles 读取角色及其权限关联，供管理端展示。
func (s *Service) ListRoles(ctx context.Context) ([]Role, error) {
	roles, err := s.repository.ListAdminRoles(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "list admin roles failed", "error", err)
		return nil, err
	}
	for index := range roles {
		roles[index].PermissionIDs, err = s.repository.ListAdminRolePermissionIDs(ctx, roles[index].ID)
		if err != nil {
			slog.ErrorContext(ctx, "list admin role permissions failed", "role_id", roles[index].ID, "error", err)
			return nil, err
		}
	}
	return roles, nil
}

// CreateRole 保存入口已规范化并校验的角色资料，使用仓储回填的自增主键。
func (s *Service) CreateRole(ctx context.Context, input RoleInput) (Role, error) {
	now := s.now().UTC()
	role := Role{Name: input.Name, Code: input.Code, Remark: input.Remark, Status: input.Status, CreatedAt: now, UpdatedAt: now}
	if err := s.repository.CreateAdminRole(ctx, &role); err != nil {
		slog.ErrorContext(ctx, "create admin role failed", "error", err)
		return Role{}, err
	}
	role.PermissionIDs = []string{}
	return role, nil
}

// UpdateRole 更新角色资料并返回当前权限关联，主键保持不变。
func (s *Service) UpdateRole(ctx context.Context, id string, input RoleInput) (Role, error) {
	role, err := s.repository.FindAdminRoleByID(ctx, strings.TrimSpace(id))
	if err != nil {
		slog.ErrorContext(ctx, "find admin role failed", "role_id", id, "error", err)
		return Role{}, err
	}

	role.Name, role.Code, role.Remark, role.Status = input.Name, input.Code, input.Remark, input.Status
	role.UpdatedAt = s.now().UTC()
	if err := s.repository.UpdateAdminRole(ctx, role); err != nil {
		slog.ErrorContext(ctx, "update admin role failed", "role_id", role.ID, "error", err)
		return Role{}, err
	}
	role.PermissionIDs, err = s.repository.ListAdminRolePermissionIDs(ctx, role.ID)
	if err != nil {
		slog.ErrorContext(ctx, "list admin role permissions failed", "role_id", role.ID, "error", err)
	}
	return role, err
}

// DeleteRole 仅允许删除没有账号引用的角色。
func (s *Service) DeleteRole(ctx context.Context, id string) error {
	if _, err := s.repository.FindAdminRoleByID(ctx, strings.TrimSpace(id)); err != nil {
		slog.ErrorContext(ctx, "find admin role failed", "role_id", id, "error", err)
		return err
	}
	count, err := s.repository.AdminRoleUserCount(ctx, strings.TrimSpace(id))
	if err != nil {
		slog.ErrorContext(ctx, "count admin role users failed", "role_id", id, "error", err)
		return err
	}
	if count > 0 {
		slog.WarnContext(ctx, "delete role rejected", "error", ErrConflict)
		return ErrConflict
	}
	err = s.repository.DeleteAdminRole(ctx, strings.TrimSpace(id))
	if err != nil {
		slog.ErrorContext(ctx, "delete admin role failed", "role_id", id, "error", err)
	}
	return err
}

// SetRolePermissions 校验角色和权限均存在后去重并整体替换权限关联。
func (s *Service) SetRolePermissions(ctx context.Context, roleID string, permissionIDs []string) error {
	if _, err := s.repository.FindAdminRoleByID(ctx, strings.TrimSpace(roleID)); err != nil {
		slog.ErrorContext(ctx, "find admin role failed", "role_id", roleID, "error", err)
		return err
	}
	permissionIDs = uniqueStrings(permissionIDs)
	for _, permissionID := range permissionIDs {
		if _, err := s.repository.FindAdminPermissionByID(ctx, permissionID); err != nil {
			slog.ErrorContext(ctx, "find admin permission failed", "permission_id", permissionID, "error", err)
			return err
		}
	}
	err := s.repository.ReplaceAdminRolePermissions(ctx, strings.TrimSpace(roleID), permissionIDs)
	if err != nil {
		slog.ErrorContext(ctx, "replace admin role permissions failed", "role_id", roleID, "error", err)
	}
	return err
}
