package memory

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"drama-server/internal/biz/adminrbac"
)

// 查询并返回指定的管理端用户。
func (s *Store) FindAdminUserByUsername(_ context.Context, username string) (adminrbac.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	username = strings.ToLower(strings.TrimSpace(username))
	for _, user := range s.adminUsers {
		if user.Username == username {
			return cloneAdminUser(user), nil
		}
	}
	return adminrbac.User{}, adminrbac.ErrNotFound
}

// 查询并返回指定的管理端用户标识。
func (s *Store) FindAdminUserByID(_ context.Context, id string) (adminrbac.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	user, exists := s.adminUsers[id]
	if !exists {
		return adminrbac.User{}, adminrbac.ErrNotFound
	}
	return cloneAdminUser(user), nil
}

// 查询并返回符合条件的管理端用户列表。
func (s *Store) ListAdminUsers(_ context.Context) ([]adminrbac.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]adminrbac.User, 0, len(s.adminUsers))
	for _, user := range s.adminUsers {
		items = append(items, cloneAdminUser(user))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	return items, nil
}

// 校验并创建管理端用户。
func (s *Store) CreateAdminUser(_ context.Context, user *adminrbac.User) error {
	if user == nil || user.ID != "" {
		return adminrbac.ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.adminUsers {
		if existing.Username == user.Username {
			return adminrbac.ErrConflict
		}
	}
	s.adminUserSequence++
	user.ID = strconv.FormatUint(s.adminUserSequence, 10)
	s.adminUsers[user.ID] = cloneAdminUser(*user)
	return nil
}

// 校验并更新管理端用户。
func (s *Store) UpdateAdminUser(_ context.Context, user adminrbac.User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, exists := s.adminUsers[user.ID]
	if !exists {
		return adminrbac.ErrNotFound
	}
	if existing.IsSuper && existing.Status == adminrbac.StatusActive && user.Status != adminrbac.StatusActive {
		activeSupers := 0
		for _, item := range s.adminUsers {
			if item.IsSuper && item.Status == adminrbac.StatusActive {
				activeSupers++
			}
		}
		if activeSupers <= 1 {
			return adminrbac.ErrLastSuperAdmin
		}
	}
	user.AuthVersion = existing.AuthVersion
	if existing.Status != user.Status {
		user.AuthVersion++
	}
	s.adminUsers[user.ID] = cloneAdminUser(user)
	return nil
}

// 校验并更新管理端密码。
func (s *Store) UpdateAdminPassword(_ context.Context, id, hash string, updatedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, exists := s.adminUsers[id]
	if !exists {
		return adminrbac.ErrNotFound
	}
	user.PasswordHash, user.UpdatedAt = hash, updatedAt
	user.AuthVersion++
	s.adminUsers[id] = user
	return nil
}

// RevokeAdminTokens 模拟按版本条件撤销账号 JWT，重复退出保持幂等。
func (s *Store) RevokeAdminTokens(_ context.Context, id string, version int64, updatedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, exists := s.adminUsers[id]
	if exists && user.AuthVersion == version {
		user.AuthVersion++
		user.UpdatedAt = updatedAt
		s.adminUsers[id] = user
	}
	return nil
}

// 查询并返回符合条件的管理端用户角色标识集合列表。
func (s *Store) ListAdminUserRoleIDs(_ context.Context, userID string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return sortedSet(s.adminUserRoles[userID]), nil
}

// 使用新数据替换指定的管理端用户角色。
func (s *Store) ReplaceAdminUserRoles(_ context.Context, userID string, roleIDs []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.adminUsers[userID]; !exists {
		return adminrbac.ErrNotFound
	}
	s.adminUserRoles[userID] = stringSet(roleIDs)
	return nil
}

// 查询并返回符合条件的管理端角色列表。
func (s *Store) ListAdminRoles(_ context.Context) ([]adminrbac.Role, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]adminrbac.Role, 0, len(s.adminRoles))
	for _, role := range s.adminRoles {
		items = append(items, cloneAdminRole(role))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	return items, nil
}

// 查询并返回指定的管理端角色标识。
func (s *Store) FindAdminRoleByID(_ context.Context, id string) (adminrbac.Role, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	role, exists := s.adminRoles[id]
	if !exists {
		return adminrbac.Role{}, adminrbac.ErrNotFound
	}
	return cloneAdminRole(role), nil
}

// 校验并创建管理端角色。
func (s *Store) CreateAdminRole(_ context.Context, role *adminrbac.Role) error {
	if role == nil || role.ID != "" {
		return adminrbac.ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.adminRoles {
		if existing.Code == role.Code || existing.Name == role.Name {
			return adminrbac.ErrConflict
		}
	}
	s.adminRoleSequence++
	role.ID = strconv.FormatUint(s.adminRoleSequence, 10)
	s.adminRoles[role.ID] = cloneAdminRole(*role)
	return nil
}

// 校验并更新管理端角色。
func (s *Store) UpdateAdminRole(_ context.Context, role adminrbac.Role) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.adminRoles[role.ID]; !exists {
		return adminrbac.ErrNotFound
	}
	for id, existing := range s.adminRoles {
		if id != role.ID && (existing.Code == role.Code || existing.Name == role.Name) {
			return adminrbac.ErrConflict
		}
	}
	s.adminRoles[role.ID] = cloneAdminRole(role)
	return nil
}

// 删除指定的管理端角色。
func (s *Store) DeleteAdminRole(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.adminRoles[id]; !exists {
		return adminrbac.ErrNotFound
	}
	delete(s.adminRoles, id)
	delete(s.adminRolePermissions, id)
	return nil
}

// 执行管理端角色用户对应的业务处理。
func (s *Store) AdminRoleUserCount(_ context.Context, roleID string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	count := 0
	for _, roles := range s.adminUserRoles {
		if _, exists := roles[roleID]; exists {
			count++
		}
	}
	return count, nil
}

// 查询并返回符合条件的管理端角色权限标识集合列表。
func (s *Store) ListAdminRolePermissionIDs(_ context.Context, roleID string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return sortedSet(s.adminRolePermissions[roleID]), nil
}

// 使用新数据替换指定的管理端角色权限。
func (s *Store) ReplaceAdminRolePermissions(_ context.Context, roleID string, permissionIDs []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.adminRoles[roleID]; !exists {
		return adminrbac.ErrNotFound
	}
	for _, id := range permissionIDs {
		if _, ok := s.adminPermissions[id]; !ok {
			return adminrbac.ErrNotFound
		}
	}
	s.adminRolePermissions[roleID] = stringSet(permissionIDs)
	return nil
}

// 查询并返回符合条件的管理端权限列表。
func (s *Store) ListAdminPermissions(_ context.Context) ([]adminrbac.Permission, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]adminrbac.Permission, 0, len(s.adminPermissions))
	for _, permission := range s.adminPermissions {
		items = append(items, permission)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Code < items[j].Code })
	return items, nil
}

// 查询并返回指定的管理端权限标识。
func (s *Store) FindAdminPermissionByID(_ context.Context, id string) (adminrbac.Permission, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	permission, exists := s.adminPermissions[id]
	if !exists {
		return adminrbac.Permission{}, adminrbac.ErrNotFound
	}
	return permission, nil
}

// ListAssignedAdminPermissionIDs 只读取启用角色直接分配的节点，不授予未来新建的子节点。
func (s *Store) ListAssignedAdminPermissionIDs(_ context.Context, userID string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make(map[string]struct{})
	for roleID := range s.adminUserRoles[userID] {
		role, exists := s.adminRoles[roleID]
		if !exists || role.Status != adminrbac.StatusActive {
			continue
		}
		for id := range s.adminRolePermissions[roleID] {
			ids[id] = struct{}{}
		}
	}
	return sortedSet(ids), nil
}

// 执行管理端用户对应的业务处理。
func cloneAdminUser(user adminrbac.User) adminrbac.User {
	user.RoleIDs = append([]string{}, user.RoleIDs...)
	return user
}

// 执行管理端角色对应的业务处理。
func cloneAdminRole(role adminrbac.Role) adminrbac.Role {
	role.PermissionIDs = append([]string{}, role.PermissionIDs...)
	return role
}

// 将字符串切片转换为去重集合。
func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

// 将字符串集合转换为排序后的切片。
func sortedSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

// CreateAdminPermission 模拟数据库自增主键，在互斥锁内检查树与唯一编码。
func (s *Store) CreateAdminPermission(_ context.Context, permission *adminrbac.Permission) error {
	if permission == nil || permission.ID != "" {
		return adminrbac.ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items := s.permissionItemsLocked()
	if err := adminrbac.ValidatePermissionChange(items, *permission); err != nil {
		return err
	}
	s.adminPermissionSequence++
	permission.ID = strconv.FormatUint(s.adminPermissionSequence, 10)
	s.adminPermissions[permission.ID] = *permission
	return nil
}

// UpdateAdminPermission 在同一锁内验证循环与父子类型，保留角色引用的稳定 ID。
func (s *Store) UpdateAdminPermission(_ context.Context, permission adminrbac.Permission) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.adminPermissions[permission.ID]; !ok {
		return adminrbac.ErrNotFound
	}
	if err := adminrbac.ValidatePermissionChange(s.permissionItemsLocked(), permission); err != nil {
		return err
	}
	s.adminPermissions[permission.ID] = permission
	return nil
}

// DeleteAdminPermission 拒绝删除仍被子节点或角色引用的权限。
func (s *Store) DeleteAdminPermission(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.adminPermissions[id]; !ok {
		return adminrbac.ErrNotFound
	}
	for _, item := range s.adminPermissions {
		if item.ParentID == id {
			return adminrbac.ErrConflict
		}
	}
	for _, ids := range s.adminRolePermissions {
		if _, ok := ids[id]; ok {
			return adminrbac.ErrConflict
		}
	}
	delete(s.adminPermissions, id)
	return nil
}

func (s *Store) permissionItemsLocked() []adminrbac.Permission {
	items := make([]adminrbac.Permission, 0, len(s.adminPermissions))
	for _, item := range s.adminPermissions {
		items = append(items, item)
	}
	return items
}
