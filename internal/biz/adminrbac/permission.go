package adminrbac

import (
	"context"
	"log/slog"
	"sort"
	"time"
)

const (
	// PermissionDirectory 组织下级目录和菜单，本身不授予接口操作能力。
	PermissionDirectory = "directory"
	// PermissionMenu 绑定一个后台页面，角色需要明确勾选该节点。
	PermissionMenu = "menu"
	// PermissionAction 以精确编码授权业务操作，不按前缀或父节点隐式继承。
	PermissionAction = "action"
)

// Permission 是可维护的权限树节点，主键保持稳定以保留角色分配关系。
type Permission struct {
	// ID 是数据库自增主键的十进制字符串。
	ID string `json:"id"`
	// ParentID 为空时表示根节点；父节点停用会使整个子树失效。
	ParentID string `json:"parentId"`
	// Name 是目录、页面或操作的显示名称。
	Name string `json:"name"`
	// Code 是唯一业务编码，action 编码与接口鉴权精确对应。
	Code string `json:"code"`
	// Type 区分目录、页面和操作，页面在协议中沿用 menu 值，只有操作节点参与接口授权。
	Type string `json:"type"`
	// Route 是页面节点绑定的站内路径，其他节点为空。
	Route string `json:"route"`
	// Icon 是目录或页面使用的 Lucide 图标名称，操作节点为空。
	Icon string `json:"icon"`
	// SortOrder 决定同级节点的显示顺序。
	SortOrder int `json:"sortOrder"`
	// Status 控制当前节点及其后代是否生效。
	Status string `json:"status"`
	// Remark 记录权限用途。
	Remark string `json:"remark"`
	// CreatedAt 是节点创建时间。
	CreatedAt time.Time `json:"createdAt"`
	// UpdatedAt 是最近修改时间。
	UpdatedAt time.Time `json:"updatedAt"`
}

// PermissionInput 是新增或编辑节点的资料；角色关系不在此修改。
type PermissionInput struct {
	// ParentID 为空表示根节点。
	ParentID string `json:"parentId"`
	// Name 是管理员可见的节点名称。
	Name string `json:"name"`
	// Code 是唯一权限编码，操作节点与业务代码保持一致。
	Code string `json:"code"`
	// Type 取 directory、menu 或 action。
	Type string `json:"type"`
	// Route 是页面节点填写的站内绝对路径。
	Route string `json:"route"`
	// Icon 是可选的 Lucide 图标名称。
	Icon string `json:"icon"`
	// SortOrder 是同级顺序，较小值优先。
	SortOrder int `json:"sortOrder"`
	// Status 为 active 或 disabled。
	Status string `json:"status"`
	// Remark 是权限用途说明。
	Remark string `json:"remark"`
}

// Menu 是会话可见的导航树，不包含未授权的操作节点。
type Menu struct {
	// ID 是菜单或目录节点的稳定标识。
	ID string `json:"id"`
	// Name 是自定义导航名称。
	Name string `json:"name"`
	// Route 是已授权页面的站内路径，目录为空。
	Route string `json:"route"`
	// Icon 是导航节点配置的 Lucide 图标名称。
	Icon string `json:"icon"`
	// Children 按配置顺序包含可见子节点。
	Children []Menu `json:"children"`
}

// PermissionRepository 维护树和直接分配，写操作必须在同一数据库事务内串行校验树约束。
type PermissionRepository interface {
	// ListAdminPermissions 返回全部节点，包括停用节点，供编辑和角色分配。
	ListAdminPermissions(context.Context) ([]Permission, error)
	// FindAdminPermissionByID 按自增标识读取节点，不存在时返回 ErrNotFound。
	FindAdminPermissionByID(context.Context, string) (Permission, error)
	// CreateAdminPermission 锁定权限树、校验关联后创建并回填数据库自增 ID。
	CreateAdminPermission(context.Context, *Permission) error
	// UpdateAdminPermission 锁定权限树并校验循环、层级和唯一编码后保存。
	UpdateAdminPermission(context.Context, Permission) error
	// DeleteAdminPermission 只删除无子节点且未被角色分配的节点。
	DeleteAdminPermission(context.Context, string) error
	// ListAssignedAdminPermissionIDs 返回当前启用角色明确分配的节点主键，不扩展后代。
	ListAssignedAdminPermissionIDs(context.Context, string) ([]string, error)
}

// ListPermissions 返回可编辑的权限树资料，树形展示由 ParentID 还原。
func (s *Service) ListPermissions(ctx context.Context) ([]Permission, error) {
	items, err := s.repository.ListAdminPermissions(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "list admin permissions failed", "error", err)
	}
	return items, err
}

// CreatePermission 创建节点；仓储在同一事务内校验父节点和唯一性。
func (s *Service) CreatePermission(ctx context.Context, input PermissionInput) (Permission, error) {
	now := s.now().UTC()
	permission := permissionFromInput(input)
	permission.CreatedAt, permission.UpdatedAt = now, now
	err := s.repository.CreateAdminPermission(ctx, &permission)
	if err != nil {
		slog.ErrorContext(ctx, "create admin permission failed", "error", err)
	}
	return permission, err
}

// UpdatePermission 保留主键、创建时间和角色关系，移动或停用节点立即影响后续鉴权。
func (s *Service) UpdatePermission(ctx context.Context, id string, input PermissionInput) (Permission, error) {
	existing, err := s.repository.FindAdminPermissionByID(ctx, id)
	if err != nil {
		slog.ErrorContext(ctx, "find admin permission failed", "permission_id", id, "error", err)
		return Permission{}, err
	}
	permission := permissionFromInput(input)
	permission.ID, permission.CreatedAt, permission.UpdatedAt = id, existing.CreatedAt, s.now().UTC()
	err = s.repository.UpdateAdminPermission(ctx, permission)
	if err != nil {
		slog.ErrorContext(ctx, "update admin permission failed", "permission_id", id, "error", err)
	}
	return permission, err
}

// DeletePermission 删除无引用的叶节点，防止静默清除角色授权或整棵子树。
func (s *Service) DeletePermission(ctx context.Context, id string) error {
	err := s.repository.DeleteAdminPermission(ctx, id)
	if err != nil {
		slog.ErrorContext(ctx, "delete admin permission failed", "permission_id", id, "error", err)
	}
	return err
}

func permissionFromInput(input PermissionInput) Permission {
	return Permission{ParentID: input.ParentID, Name: input.Name, Code: input.Code, Type: input.Type, Route: input.Route, Icon: input.Icon, SortOrder: input.SortOrder, Status: input.Status, Remark: input.Remark}
}

// ValidatePermissionChange 校验写入后的完整树；仓储必须在持有写锁期间调用，防止并发移动形成环。
func ValidatePermissionChange(items []Permission, changed Permission) error {
	byID := make(map[string]Permission, len(items)+1)
	for _, item := range items {
		if item.ID != changed.ID && item.Code == changed.Code {
			return ErrConflict
		}
		byID[item.ID] = item
	}
	id := changed.ID
	if id == "" {
		id = "new"
	}
	byID[id] = changed
	for nodeID, item := range byID {
		visited := map[string]bool{nodeID: true}
		parentID := item.ParentID
		child := item
		for depth := 0; parentID != ""; depth++ {
			if depth >= 16 || visited[parentID] {
				return ErrInvalidInput
			}
			parent, ok := byID[parentID]
			if !ok {
				return ErrNotFound
			}
			if parent.Type == PermissionAction || (parent.Type == PermissionMenu && child.Type != PermissionAction) {
				return ErrInvalidInput
			}
			visited[parentID] = true
			child, parentID = parent, parent.ParentID
		}
	}
	return nil
}

// PermissionAccess 只激活已分配且所有祖先启用的节点；父节点授权不会隐式扩展到新子节点。
func PermissionAccess(items []Permission, assigned []string, super bool) ([]string, []Menu) {
	byID := make(map[string]Permission, len(items))
	children := make(map[string][]Permission)
	selected := make(map[string]bool, len(assigned))
	for _, id := range assigned {
		selected[id] = true
	}
	for _, item := range items {
		byID[item.ID] = item
		children[item.ParentID] = append(children[item.ParentID], item)
	}
	for parent := range children {
		sort.Slice(children[parent], func(i, j int) bool {
			a, b := children[parent][i], children[parent][j]
			if a.SortOrder != b.SortOrder {
				return a.SortOrder < b.SortOrder
			}
			return a.Code < b.Code
		})
	}
	enabled := func(item Permission) bool {
		visited := make(map[string]bool)
		for {
			if item.Status != StatusActive || visited[item.ID] {
				return false
			}
			visited[item.ID] = true
			if item.ParentID == "" {
				return true
			}
			parent, ok := byID[item.ParentID]
			if !ok {
				return false
			}
			item = parent
		}
	}
	codes := []string{}
	for _, item := range items {
		if item.Type == PermissionAction && (super || selected[item.ID]) && enabled(item) {
			codes = append(codes, item.Code)
		}
	}
	sort.Strings(codes)
	var walk func(string) []Menu
	walk = func(parent string) []Menu {
		result := []Menu{}
		for _, item := range children[parent] {
			if item.Type == PermissionAction || !enabled(item) {
				continue
			}
			nested := walk(item.ID)
			route := ""
			if item.Type == PermissionMenu && (super || selected[item.ID]) {
				route = item.Route
			}
			if route != "" || len(nested) > 0 {
				result = append(result, Menu{ID: item.ID, Name: item.Name, Route: route, Icon: item.Icon, Children: nested})
			}
		}
		return result
	}
	return codes, walk("")
}
