package adminrbac

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

var dummyPasswordHash = func() []byte {
	hash, err := bcrypt.GenerateFromPassword([]byte("invalid-admin-password"), bcrypt.DefaultCost)
	if err != nil {
		slog.Error("hash dummy admin password failed", "error", err)
	}
	return hash
}()

// Session 包含管理端当前身份、有效操作权限和按授权生成的动态导航树。
type Session struct {
	// User 是去除密码摘要后的当前管理员资料。
	User User `json:"user"`
	// Permissions 是当前启用且已分配的操作权限码。
	Permissions []string `json:"permissions"`
	// Menus 是按已授权菜单和配置顺序生成的导航树。
	Menus []Menu `json:"menus"`
}

// LoginResult 返回一次性下发的会话凭据及当前身份，不将原始凭据持久化。
type LoginResult struct {
	// Token 仅用于客户端后续认证，不得写入日志。
	Token string `json:"token"`
	// Session 提供登录成功后的账号与有效权限。
	Session
}

// Login 校验输入密码并签发 JWT；账号不存在、停用或密码错误均返回 ErrUnauthorized。
func (s *Service) Login(ctx context.Context, username, password string) (LoginResult, error) {
	if s == nil || s.repository == nil || len(s.signingKey) < 32 {
		slog.WarnContext(ctx, "login rejected", "error", ErrUnauthorized)
		return LoginResult{}, ErrUnauthorized
	}
	user, err := s.repository.FindAdminUserByUsername(ctx, normalizeUsername(username))
	if err != nil {
		slog.Log(ctx, authenticationErrorLevel(err), "find admin user by username failed", "error", err)
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return LoginResult{}, err
	}
	// 不存在的账号也执行同成本的 bcrypt 比较，避免仅靠跳过密码计算暴露账号是否存在。
	hash := dummyPasswordHash
	if err == nil {
		hash = []byte(user.PasswordHash)
	}
	passwordErr := bcrypt.CompareHashAndPassword(hash, []byte(password))
	if passwordErr != nil {
		slog.WarnContext(ctx, "check admin password failed", "error", passwordErr)
	}
	passwordMatches := passwordErr == nil
	if err != nil || user.Status != StatusActive || !passwordMatches {
		slog.WarnContext(ctx, "login rejected", "error", ErrUnauthorized)
		return LoginResult{}, ErrUnauthorized
	}
	session, err := s.sessionForUser(ctx, user)
	if err != nil {
		slog.ErrorContext(ctx, "session for user failed", "error", err)
		return LoginResult{}, err
	}
	return s.signSession(ctx, session)
}

// Refresh 仅为仍有效且账号未被撤销的凭据续期，保留认证版本以支持统一退出。
func (s *Service) Refresh(ctx context.Context, token string) (LoginResult, error) {
	session, err := s.Authenticate(ctx, token)
	if err != nil {
		slog.WarnContext(ctx, "refresh admin session failed", "error", err)
		return LoginResult{}, err
	}
	return s.signSession(ctx, session)
}

// signSession 让登录和续期使用相同有效期及签名规则。
func (s *Service) signSession(ctx context.Context, session Session) (LoginResult, error) {
	user := session.User
	now := s.now().UTC()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, adminClaims{
		AuthVersion: user.AuthVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: adminTokenIssuer, Subject: user.ID, Audience: jwt.ClaimStrings{adminTokenAudience},
			IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(s.sessionTTL)),
		},
	}).SignedString(s.signingKey)
	if err != nil {
		slog.ErrorContext(ctx, "sign admin JWT failed", "error", err)
		return LoginResult{}, err
	}
	return LoginResult{Token: token, Session: session}, nil
}

// Authenticate 验证 JWT 签名和有效期、账号状态与认证版本，返回最新有效权限。
func (s *Service) Authenticate(ctx context.Context, token string) (Session, error) {
	if s == nil || s.repository == nil || strings.TrimSpace(token) == "" {
		slog.WarnContext(ctx, "authenticate rejected", "error", ErrUnauthorized)
		return Session{}, ErrUnauthorized
	}
	record, err := s.parseToken(token)
	if err != nil {
		slog.WarnContext(ctx, "verify admin JWT failed", "error", err)
		return Session{}, ErrUnauthorized
	}
	user, err := s.repository.FindAdminUserByID(ctx, record.Subject)
	if err != nil {
		slog.Log(ctx, authenticationErrorLevel(err), "find admin user by id failed", "error", err)
	}
	if errors.Is(err, ErrNotFound) {
		slog.WarnContext(ctx, "authenticate rejected", "error", ErrUnauthorized)
		return Session{}, ErrUnauthorized
	}
	if err != nil {
		return Session{}, err
	}
	// 账号停用或密码重置都会使旧版本会话失效；权限本身仍在下面实时读取。
	if user.Status != StatusActive || user.AuthVersion != record.AuthVersion {
		slog.WarnContext(ctx, "authenticate rejected", "error", ErrUnauthorized)
		return Session{}, ErrUnauthorized
	}

	result1, resultErr := s.sessionForUser(ctx, user)
	if resultErr != nil {
		slog.ErrorContext(ctx, "session for user failed", "error", resultErr)
	}
	return result1, resultErr
}

// Logout 推进账号认证版本，使该账号本版本的全部 JWT 失效；重复或过期退出不影响新版本。
func (s *Service) Logout(ctx context.Context, token string) error {
	if s == nil || s.repository == nil {
		slog.WarnContext(ctx, "logout rejected", "error", ErrUnauthorized)
		return ErrUnauthorized
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}

	claims, err := s.parseToken(token)
	if err != nil {
		slog.WarnContext(ctx, "verify logout JWT failed", "error", err)
		return nil
	}
	resultErr := s.repository.RevokeAdminTokens(ctx, claims.Subject, claims.AuthVersion, s.now().UTC())
	if resultErr != nil {
		slog.ErrorContext(ctx, "revoke admin tokens failed", "error", resultErr)
	}
	return resultErr
}

// Authorize 认证会话并精确检查操作编码，超级管理员保留全部业务操作能力。
func (s *Service) Authorize(ctx context.Context, token, permission string) (Session, error) {
	session, err := s.Authenticate(ctx, token)
	if err != nil {
		slog.Log(ctx, authenticationErrorLevel(err), "authenticate failed", "error", err)
		return Session{}, err
	}
	if session.User.IsSuper || contains(session.Permissions, permission) {
		return session, nil
	}
	slog.WarnContext(ctx, "authorize rejected", "error", ErrForbidden)
	return Session{}, ErrForbidden
}

// sessionForUser 每次认证重新读取有效权限与角色，并清除密码摘要；角色调整无需等待会话重新签发。
func (s *Service) sessionForUser(ctx context.Context, user User) (Session, error) {
	items, err := s.repository.ListAdminPermissions(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "list admin permissions failed", "error", err)
		return Session{}, err
	}
	var assigned []string
	if !user.IsSuper {
		assigned, err = s.repository.ListAssignedAdminPermissionIDs(ctx, user.ID)
		if err != nil {
			slog.ErrorContext(ctx, "list assigned admin permissions failed", "error", err)
			return Session{}, err
		}
	}
	codes, menus := PermissionAccess(items, assigned, user.IsSuper)
	user.PasswordHash = ""
	user.RoleIDs, err = s.repository.ListAdminUserRoleIDs(ctx, user.ID)
	if err != nil {
		slog.ErrorContext(ctx, "list admin user roles failed", "error", err)
		return Session{}, err
	}
	return Session{User: user, Permissions: codes, Menus: menus}, nil
}

// contains 在当前有效权限编码中进行精确匹配，不进行前缀或通配授权。
func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// authenticationErrorLevel 将失效会话、账号不存在与请求取消和真实依赖故障分开，不改变认证结果。
func authenticationErrorLevel(err error) slog.Level {
	if errors.Is(err, context.Canceled) {
		return slog.LevelInfo
	}
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrForbidden) {
		return slog.LevelWarn
	}
	return slog.LevelError
}
