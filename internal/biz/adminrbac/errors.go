package adminrbac

import "errors"

var (
	// ErrNotFound 表示管理员、角色、权限或有效会话记录不存在。
	ErrNotFound = errors.New("admin rbac record not found")
	// ErrConflict 表示唯一键冲突或当前关联状态不允许执行操作。
	ErrConflict = errors.New("admin rbac conflict")
	// ErrInvalidInput 表示 RBAC 入口参数不满足格式约束。
	ErrInvalidInput = errors.New("invalid admin rbac input")
	// ErrUnauthorized 表示无法建立有效管理员身份，需重新登录。
	ErrUnauthorized = errors.New("admin authentication required")
	// ErrForbidden 表示已识别的主体缺少操作权限或不可修改该目标账号。
	ErrForbidden = errors.New("admin permission denied")
	// ErrLastSuperAdmin 表示停用操作会使系统没有任何有效超级管理员。
	ErrLastSuperAdmin = errors.New("the last active super administrator cannot be disabled")
)
