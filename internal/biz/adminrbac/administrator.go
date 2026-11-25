package adminrbac

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	// StatusActive 表示管理员可登录并参与后台权限校验。
	StatusActive = "active"
	// StatusDisabled 表示管理员已停用，不能继续使用后台会话。
	StatusDisabled = "disabled"
)

// User 是运营后台管理员聚合根；超级管理员身份只能通过受控部署 SQL 创建。
type User struct {
	// ID 是数据库生成的自增主键，以十进制字符串对外传递。
	ID string `json:"id"`
	// Username 是规范化为小写的登录名，创建后不可修改。
	Username string `json:"username"`
	// PasswordHash 保存 bcrypt 密码摘要，不得通过 API 返回。
	PasswordHash string `json:"-"`
	// Nickname 是后台展示名称，不参与身份认证。
	Nickname string `json:"nickname"`
	// Status 控制账号是否启用，取 StatusActive 或 StatusDisabled。
	Status string `json:"status"`
	// IsSuper 标记超级管理员，普通账号管理操作不能授予此身份。
	IsSuper bool `json:"isSuper"`
	// AuthVersion 在密码或启用状态变化时递增，使旧会话失效。
	AuthVersion int64 `json:"-"`
	// RoleIDs 是账号分配的角色标识，由角色关联仓储单独读取。
	RoleIDs []string `json:"roleIds"`
	// CreatedAt 记录账号创建时间，持久化时统一为 UTC。
	CreatedAt time.Time `json:"createdAt"`
	// UpdatedAt 记录账号资料或密码最近一次更新的时间。
	UpdatedAt time.Time `json:"updatedAt"`
}

// UserCreateInput 定义由入口规范化并校验的普通管理员创建参数，不接受超级管理员身份或角色分配。
type UserCreateInput struct {
	// Username 经去除首尾空白并转为小写后校验和保存。
	Username string `json:"username"`
	// Password 仅用于生成 bcrypt 摘要，不保存明文。
	Password string `json:"password"`
	// Nickname 是必填的后台展示名称。
	Nickname string `json:"nickname"`
	// Status 指定账号创建后启用或停用。
	Status string `json:"status"`
}

// UserUpdateInput 由入口校验，仅允许修改展示名称与启用状态，密码和角色通过独立操作管理。
type UserUpdateInput struct {
	// Nickname 是修改后的后台展示名称，去除首尾空白后不能为空。
	Nickname string `json:"nickname"`
	// Status 指定账号新的启用状态，变化时使旧会话失效。
	Status string `json:"status"`
}

// AdministratorRepository 声明管理员账号和用户角色功能需要的持久化能力。
type AdministratorRepository interface {
	// RevokeAdminTokens 仅在账号版本匹配时递增认证版本；已撤销或账号不存在时幂等成功。
	RevokeAdminTokens(context.Context, string, int64, time.Time) error
	// FindAdminUserByUsername 按规范化登录名读取账号及密码摘要供认证使用；不存在时返回 ErrNotFound。
	FindAdminUserByUsername(context.Context, string) (User, error)
	// FindAdminUserByID 按账号标识读取当前资料，供会话与管理操作校验；不存在时返回 ErrNotFound。
	FindAdminUserByID(context.Context, string) (User, error)
	// ListAdminUsers 返回包含停用账号的管理员列表；角色关联和密码摘要脱敏由服务层处理。
	ListAdminUsers(context.Context) ([]User, error)
	// CreateAdminUser 保存账号及密码摘要，成功后将数据库自增 ID 回填到 user；输入 ID 必须为空，用户名冲突时返回 ErrConflict。
	CreateAdminUser(context.Context, *User) error
	// UpdateAdminUser 更新昵称、状态和更新时间；状态变化时递增 AuthVersion，并禁止停用最后一名有效超级管理员。
	UpdateAdminUser(context.Context, User) error
	// UpdateAdminPassword 按账号标识、新 bcrypt 摘要、更新时间的顺序传参；递增 AuthVersion 使旧会话失效，不接收明文密码。
	UpdateAdminPassword(context.Context, string, string, time.Time) error
	// ListAdminUserRoleIDs 返回账号直接分配的角色标识，不展开角色权限或过滤停用角色。
	ListAdminUserRoleIDs(context.Context, string) ([]string, error)
	// ReplaceAdminUserRoles 原子替换账号的全部角色关联；空列表表示移除全部角色，不改变超级管理员身份。
	ReplaceAdminUserRoles(context.Context, string, []string) error
}

// ListUsers 汇总账号及其角色标识，并清除密码摘要后供管理端展示。
func (s *Service) ListUsers(ctx context.Context) ([]User, error) {
	users, err := s.repository.ListAdminUsers(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "list admin users failed", "error", err)
		return nil, err
	}
	for index := range users {
		users[index].PasswordHash = ""
		users[index].RoleIDs, err = s.repository.ListAdminUserRoleIDs(ctx, users[index].ID)
		if err != nil {
			slog.ErrorContext(ctx, "list admin user roles failed", "user_id", users[index].ID, "error", err)
			return nil, err
		}
	}
	return users, nil
}

// CreateUser 使用入口已规范化并校验的参数创建普通管理员，密码仅以 bcrypt 摘要持久化，初始不分配角色。
func (s *Service) CreateUser(ctx context.Context, input UserCreateInput) (User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		slog.ErrorContext(ctx, "hash admin password failed", "error", err)
		return User{}, err
	}
	now := s.now().UTC()
	user := User{
		Username: input.Username, PasswordHash: string(hash), Nickname: input.Nickname,
		Status: input.Status, AuthVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.repository.CreateAdminUser(ctx, &user); err != nil {
		slog.ErrorContext(ctx, "create admin user failed", "error", err)
		return User{}, err
	}
	user.PasswordHash = ""
	user.RoleIDs = []string{}
	return user, nil
}

// UpdateUserAs 以有效操作者身份更新账号昵称和状态；只有超级管理员能修改超级管理员账号。
func (s *Service) UpdateUserAs(ctx context.Context, actorID, id string, input UserUpdateInput) (User, error) {
	actor, err := s.authorizedActor(ctx, actorID)
	if err != nil {
		slog.ErrorContext(ctx, "authorize admin actor failed", "error", err)
		return User{}, err
	}
	user, err := s.repository.FindAdminUserByID(ctx, strings.TrimSpace(id))
	if err != nil {
		slog.ErrorContext(ctx, "find admin user by id failed", "error", err)
		return User{}, err
	}
	if user.IsSuper && !actor.IsSuper {
		slog.WarnContext(ctx, "update admin user rejected", "error", ErrForbidden)
		return User{}, ErrForbidden
	}
	user.Nickname = input.Nickname
	user.Status = input.Status
	user.UpdatedAt = s.now().UTC()
	if err := s.repository.UpdateAdminUser(ctx, user); err != nil {
		slog.ErrorContext(ctx, "update admin user failed", "error", err)
		return User{}, err
	}
	user, err = s.repository.FindAdminUserByID(ctx, user.ID)
	if err != nil {
		slog.ErrorContext(ctx, "find admin user by id failed", "error", err)
		return User{}, err
	}
	user.PasswordHash = ""
	user.RoleIDs, err = s.repository.ListAdminUserRoleIDs(ctx, user.ID)
	if err != nil {
		slog.ErrorContext(ctx, "list admin user roles failed", "error", err)
	}
	return user, err
}

// ResetUserPasswordAs 使用入口已校验的新密码更新摘要；超级管理员密码只能由超级管理员重置。
func (s *Service) ResetUserPasswordAs(ctx context.Context, actorID, id, password string) error {
	actor, err := s.authorizedActor(ctx, actorID)
	if err != nil {
		slog.ErrorContext(ctx, "authorize admin actor failed", "error", err)
		return err
	}
	target, err := s.repository.FindAdminUserByID(ctx, strings.TrimSpace(id))
	if err != nil {
		slog.ErrorContext(ctx, "find admin user by id failed", "error", err)
		return err
	}
	if target.IsSuper && !actor.IsSuper {
		slog.WarnContext(ctx, "reset admin password rejected", "error", ErrForbidden)
		return ErrForbidden
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		slog.ErrorContext(ctx, "hash admin password failed", "error", err)
		return err
	}

	resultErr := s.repository.UpdateAdminPassword(ctx, strings.TrimSpace(id), string(hash), s.now().UTC())
	if resultErr != nil {
		slog.ErrorContext(ctx, "update admin password failed", "error", resultErr)
	}
	return resultErr
}

// SetUserRolesAs 校验操作者及目标角色，去重后整体替换账号角色；只有超级管理员能调整超级管理员的角色。
func (s *Service) SetUserRolesAs(ctx context.Context, actorID, userID string, roleIDs []string) error {
	actor, err := s.authorizedActor(ctx, actorID)
	if err != nil {
		slog.ErrorContext(ctx, "authorize admin actor failed", "error", err)
		return err
	}
	target, err := s.repository.FindAdminUserByID(ctx, strings.TrimSpace(userID))
	if err != nil {
		slog.ErrorContext(ctx, "find admin user by id failed", "error", err)
		return err
	}
	if target.IsSuper && !actor.IsSuper {
		slog.WarnContext(ctx, "assign admin user roles rejected", "error", ErrForbidden)
		return ErrForbidden
	}
	roleIDs = uniqueStrings(roleIDs)
	for _, roleID := range roleIDs {
		if _, err := s.repository.FindAdminRoleByID(ctx, roleID); err != nil {
			slog.ErrorContext(ctx, "find admin role by id failed", "error", err)
			return err
		}
	}

	resultErr := s.repository.ReplaceAdminUserRoles(ctx, strings.TrimSpace(userID), roleIDs)
	if resultErr != nil {
		slog.ErrorContext(ctx, "replace admin user roles failed", "error", resultErr)
	}
	return resultErr
}

// authorizedActor 确认操作者账号存在且启用；具体 action 权限由调用入口校验。
func (s *Service) authorizedActor(ctx context.Context, actorID string) (User, error) {
	actor, err := s.repository.FindAdminUserByID(ctx, strings.TrimSpace(actorID))
	if err != nil {
		slog.ErrorContext(ctx, "find admin user by id failed", "error", err)
	}
	if err != nil || actor.Status != StatusActive {
		if err != nil && !errors.Is(err, ErrNotFound) {
			return User{}, err
		}
		slog.WarnContext(ctx, "authorized actor rejected", "error", ErrForbidden)
		return User{}, ErrForbidden
	}
	return actor, nil
}

// normalizeUsername 统一登录名的首尾空白与大小写，不修改密码原文。
func normalizeUsername(input string) string {
	return strings.ToLower(strings.TrimSpace(input))
}
