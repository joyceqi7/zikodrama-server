package operations

import (
	"log/slog"
	"net/http"

	"drama-server/internal/pkg/apierr"

	"drama-server/internal/biz/adminrbac"
)

// registerRBACRoutes 注册用户、角色和权限树维护入口，每个写动作单独鉴权。
func (h *Handler) registerRBACRoutes() {
	h.mux.HandleFunc("GET /admin/v1/users", h.requirePermission(adminrbac.PermissionUserRead, h.listUsers))
	h.mux.HandleFunc("POST /admin/v1/users", h.requirePermission(adminrbac.PermissionUserCreate, h.createUser))
	h.mux.HandleFunc("PUT /admin/v1/users/{userID}", h.requirePermission(adminrbac.PermissionUserUpdate, h.updateUser))
	h.mux.HandleFunc("PUT /admin/v1/users/{userID}/password", h.requirePermission(adminrbac.PermissionUserReset, h.resetUserPassword))
	h.mux.HandleFunc("PUT /admin/v1/users/{userID}/roles", h.requirePermission(adminrbac.PermissionUserAssignRole, h.setUserRoles))
	h.mux.HandleFunc("GET /admin/v1/roles", h.requirePermission(adminrbac.PermissionRoleRead, h.listRoles))
	h.mux.HandleFunc("POST /admin/v1/roles", h.requirePermission(adminrbac.PermissionRoleCreate, h.createRole))
	h.mux.HandleFunc("PUT /admin/v1/roles/{roleID}", h.requirePermission(adminrbac.PermissionRoleUpdate, h.updateRole))
	h.mux.HandleFunc("DELETE /admin/v1/roles/{roleID}", h.requirePermission(adminrbac.PermissionRoleDelete, h.deleteRole))
	h.mux.HandleFunc("PUT /admin/v1/roles/{roleID}/permissions", h.requirePermission(adminrbac.PermissionRoleAssign, h.setRolePermissions))
	h.mux.HandleFunc("GET /admin/v1/permissions", h.requirePermission(adminrbac.PermissionPermissionRead, h.listPermissions))
	h.mux.HandleFunc("POST /admin/v1/permissions", h.requirePermission(adminrbac.PermissionPermissionCreate, h.createPermission))
	h.mux.HandleFunc("PUT /admin/v1/permissions/{permissionID}", h.requirePermission(adminrbac.PermissionPermissionUpdate, h.updatePermission))
	h.mux.HandleFunc("DELETE /admin/v1/permissions/{permissionID}", h.requirePermission(adminrbac.PermissionPermissionDelete, h.deletePermission))
}

// listUsers 返回管理端用户列表。
func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) {
	result, err := h.rbac.ListUsers(r.Context())
	writeResult(w, r, http.StatusOK, result, err)
}

// createUser 校验并创建管理端用户。
func (h *Handler) createUser(w http.ResponseWriter, r *http.Request) {
	var input adminrbac.UserCreateInput
	if !decode(w, r, &input) {
		return
	}
	if err := validateUserCreateInput(&input); err != nil {
		slog.WarnContext(r.Context(), "create admin user input rejected", "error", err)
		writeError(w, r, apierr.OperationsInvalidRequest)
		return
	}

	result, err := h.rbac.CreateUser(r.Context(), input)
	writeResult(w, r, http.StatusCreated, result, err)
}

// updateUser 以当前管理员身份更新目标用户。
func (h *Handler) updateUser(w http.ResponseWriter, r *http.Request) {
	var input adminrbac.UserUpdateInput
	if !decode(w, r, &input) {
		return
	}
	if err := validateUserUpdateInput(r.PathValue("userID"), &input); err != nil {
		slog.WarnContext(r.Context(), "update admin user input rejected", "error", err)
		writeError(w, r, apierr.OperationsInvalidRequest)
		return
	}

	result, err := h.rbac.UpdateUserAs(r.Context(), adminActorID(r.Context()), r.PathValue("userID"), input)
	writeResult(w, r, http.StatusOK, result, err)
}

// resetUserPassword 以当前管理员身份重置目标用户密码。
func (h *Handler) resetUserPassword(w http.ResponseWriter, r *http.Request) {
	var input struct {
		// Password 是需要设置的新密码。
		Password string `json:"password"`
	}
	if !decode(w, r, &input) {
		return
	}
	if err := validateRBACID(r.PathValue("userID")); err != nil {
		slog.WarnContext(r.Context(), "reset admin password input rejected", "error", err)
		writeError(w, r, apierr.OperationsInvalidRequest)
		return
	}

	if err := validateAdminPassword(input.Password); err != nil {
		slog.WarnContext(r.Context(), "reset admin password input rejected", "error", err)
		writeError(w, r, apierr.OperationsInvalidRequest)
		return
	}

	err := h.rbac.ResetUserPasswordAs(r.Context(), adminActorID(r.Context()), r.PathValue("userID"), input.Password)
	writeResult(w, r, http.StatusOK, map[string]any{}, err)
}

// setUserRoles 以当前管理员身份替换目标用户的角色集合。
func (h *Handler) setUserRoles(w http.ResponseWriter, r *http.Request) {
	var input struct {
		// RoleIDs 是替换用户现有分配的角色标识集合。
		RoleIDs []string `json:"roleIds"`
	}
	if !decode(w, r, &input) {
		return
	}
	if err := validateRBACAssignment(r.PathValue("userID"), input.RoleIDs); err != nil {
		slog.WarnContext(r.Context(), "assign admin roles input rejected", "error", err)
		writeError(w, r, apierr.OperationsInvalidRequest)
		return
	}

	err := h.rbac.SetUserRolesAs(r.Context(), adminActorID(r.Context()), r.PathValue("userID"), input.RoleIDs)
	writeResult(w, r, http.StatusOK, map[string]any{}, err)
}

// listRoles 返回角色列表。
func (h *Handler) listRoles(w http.ResponseWriter, r *http.Request) {
	result, err := h.rbac.ListRoles(r.Context())
	writeResult(w, r, http.StatusOK, result, err)
}

// createRole 校验并创建角色。
func (h *Handler) createRole(w http.ResponseWriter, r *http.Request) {
	var input adminrbac.RoleInput
	if !decode(w, r, &input) {
		return
	}
	if err := validateRoleInput(&input); err != nil {
		slog.WarnContext(r.Context(), "create admin role input rejected", "error", err)
		writeError(w, r, apierr.OperationsInvalidRequest)
		return
	}

	result, err := h.rbac.CreateRole(r.Context(), input)
	writeResult(w, r, http.StatusCreated, result, err)
}

// updateRole 校验并更新角色。
func (h *Handler) updateRole(w http.ResponseWriter, r *http.Request) {
	var input adminrbac.RoleInput
	if !decode(w, r, &input) {
		return
	}
	if err := validateRBACID(r.PathValue("roleID")); err != nil {
		slog.WarnContext(r.Context(), "update admin role input rejected", "error", err)
		writeError(w, r, apierr.OperationsInvalidRequest)
		return
	}

	if err := validateRoleInput(&input); err != nil {
		slog.WarnContext(r.Context(), "update admin role input rejected", "error", err)
		writeError(w, r, apierr.OperationsInvalidRequest)
		return
	}

	result, err := h.rbac.UpdateRole(r.Context(), r.PathValue("roleID"), input)
	writeResult(w, r, http.StatusOK, result, err)
}

// deleteRole 删除指定角色。
func (h *Handler) deleteRole(w http.ResponseWriter, r *http.Request) {
	if err := validateRBACID(r.PathValue("roleID")); err != nil {
		slog.WarnContext(r.Context(), "delete admin role input rejected", "error", err)
		writeError(w, r, apierr.OperationsInvalidRequest)
		return
	}

	err := h.rbac.DeleteRole(r.Context(), r.PathValue("roleID"))
	writeResult(w, r, http.StatusOK, map[string]any{}, err)
}

// setRolePermissions 替换指定角色的权限集合。
func (h *Handler) setRolePermissions(w http.ResponseWriter, r *http.Request) {
	var input struct {
		// PermissionIDs 是替换角色现有分配的权限标识集合。
		PermissionIDs []string `json:"permissionIds"`
	}
	if !decode(w, r, &input) {
		return
	}
	if err := validateRBACAssignment(r.PathValue("roleID"), input.PermissionIDs); err != nil {
		slog.WarnContext(r.Context(), "assign admin permissions input rejected", "error", err)
		writeError(w, r, apierr.OperationsInvalidRequest)
		return
	}

	err := h.rbac.SetRolePermissions(r.Context(), r.PathValue("roleID"), input.PermissionIDs)
	writeResult(w, r, http.StatusOK, map[string]any{}, err)
}

// listPermissions 返回包含父节点、类型和状态的权限树资料。
func (h *Handler) listPermissions(w http.ResponseWriter, r *http.Request) {
	result, err := h.rbac.ListPermissions(r.Context())
	writeResult(w, r, http.StatusOK, result, err)
}

// createPermission 在 HTTP 边界检查字段，再创建自定义权限节点。
func (h *Handler) createPermission(w http.ResponseWriter, r *http.Request) {
	var input adminrbac.PermissionInput
	if !decode(w, r, &input) {
		return
	}
	if err := validatePermissionInput(&input); err != nil {
		slog.WarnContext(r.Context(), "create admin permission input rejected", "error", err)
		writeError(w, r, apierr.OperationsInvalidRequest)
		return
	}
	result, err := h.rbac.CreatePermission(r.Context(), input)
	writeResult(w, r, http.StatusCreated, result, err)
}

// updatePermission 校验完整编辑资料，循环和关联约束由事务中的领域规则检查。
func (h *Handler) updatePermission(w http.ResponseWriter, r *http.Request) {
	var input adminrbac.PermissionInput
	if !decode(w, r, &input) {
		return
	}
	if err := validateRBACID(r.PathValue("permissionID")); err != nil {
		slog.WarnContext(r.Context(), "update admin permission ID rejected", "error", err)
		writeError(w, r, apierr.OperationsInvalidRequest)
		return
	}
	if err := validatePermissionInput(&input); err != nil {
		slog.WarnContext(r.Context(), "update admin permission input rejected", "error", err)
		writeError(w, r, apierr.OperationsInvalidRequest)
		return
	}
	result, err := h.rbac.UpdatePermission(r.Context(), r.PathValue("permissionID"), input)
	writeResult(w, r, http.StatusOK, result, err)
}

// deletePermission 只允许删除无子节点和角色引用的权限。
func (h *Handler) deletePermission(w http.ResponseWriter, r *http.Request) {
	if err := validateRBACID(r.PathValue("permissionID")); err != nil {
		slog.WarnContext(r.Context(), "delete admin permission ID rejected", "error", err)
		writeError(w, r, apierr.OperationsInvalidRequest)
		return
	}
	err := h.rbac.DeletePermission(r.Context(), r.PathValue("permissionID"))
	writeResult(w, r, http.StatusOK, map[string]any{}, err)
}
