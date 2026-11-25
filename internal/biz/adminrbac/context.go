package adminrbac

import "time"

const defaultSessionTTL = 2 * time.Hour

// Config 只包含 RBAC 用例自身需要的可替换依赖。
type Config struct {
	// Now 提供会话签发及资料更新时间，未设置时使用系统时钟。
	Now func() time.Time
	// SessionTTL 是JWT 有效期；非正值使用默认 2 小时。
	SessionTTL time.Duration
	// SigningKey 是管理员 JWT 专用的至少 32 字节签名密钥，由独立部署配置注入。
	SigningKey []byte
}

// Service 是 RBAC 限界上下文的用例入口；具体行为按认证、管理员、角色和权限分文件维护。
type Service struct {
	repository Repository
	now        func() time.Time
	sessionTTL time.Duration
	signingKey []byte
}

// Repository 组合各功能所需的窄仓储端口，具体用例只调用所属功能接口。
type Repository interface {
	AdministratorRepository
	RoleRepository
	PermissionRepository
}

// NewService 装配 RBAC 仓储和可替换时钟、会话有效期及 JWT 签名密钥。
func NewService(repository Repository, config Config) *Service {
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.SessionTTL <= 0 {
		config.SessionTTL = defaultSessionTTL
	}
	return &Service{
		repository: repository, now: config.Now,
		sessionTTL: config.SessionTTL, signingKey: append([]byte(nil), config.SigningKey...),
	}
}
