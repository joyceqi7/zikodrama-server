package config

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrInvalid = errors.New("invalid application configuration")
var ErrMissingBootstrap = errors.New("缺少启动配置")

// 定义配置。
type Config struct {
	// AdErrorRequestsPerMinute 限制广告错误日志请求，不影响原有统计。
	AdErrorRequestsPerMinute int
	// VisitorTokenTTL 是配置文件指定的消费者凭据绝对有效期，普通 API 请求不延长到期时间。
	VisitorTokenTTL time.Duration
	// DirectPlaybackTokenTTLSeconds 控制直连播放签名有效期，须为 60～900 秒。
	DirectPlaybackTokenTTLSeconds int
	// 保存当前运行环境名称。
	Environment string
	// IdentityTokenEncryptionSecret 仅用于消费者 VisitorToken，管理员认证不得复用。
	IdentityTokenEncryptionSecret string
	// AdminJWTSigningSecret 是管理员 JWT 独立签名密钥，各 API 实例须一致。
	AdminJWTSigningSecret string
	// LegacyTikTokClientSecrets 保存旧应用 secret_ref 到 Client Secret 的配置文件映射。
	LegacyTikTokClientSecrets map[string]string
}

// NewConfig 从启动配置投影并校验业务用例实际消费的参数。
// HTTP、MySQL、OTel 与 Registry 配置由对应 Styx 工厂直接消费。
func NewConfig(bootstrap *Bootstrap) (Config, error) {
	if bootstrap == nil || bootstrap.GetApp() == nil || bootstrap.GetBusiness() == nil {
		return Config{}, ErrMissingBootstrap
	}
	business := bootstrap.GetBusiness()
	settings := Config{
		AdErrorRequestsPerMinute:      int(business.GetAdErrorRequestsPerMinute()),
		VisitorTokenTTL:               time.Duration(business.GetVisitorTokenTtlSeconds()) * time.Second,
		Environment:                   strings.ToLower(strings.TrimSpace(bootstrap.GetApp().GetEnv())),
		IdentityTokenEncryptionSecret: business.GetIdentityTokenEncryptionSecret(),
		AdminJWTSigningSecret:         business.GetAdminJwtSigningSecret(),
		LegacyTikTokClientSecrets:     business.GetLegacyTiktokClientSecrets(),
		DirectPlaybackTokenTTLSeconds: int(business.GetDirectPlaybackTokenTtlSeconds()),
	}
	if err := settings.Validate(); err != nil {
		return Config{}, err
	}
	return settings, nil
}

// 校验配置的必填项、取值范围和生产安全约束。
func (c Config) Validate() error {
	if c.Environment != "development" && c.Environment != "test" && c.Environment != "staging" && c.Environment != "production" {
		return fmt.Errorf("%w: APP_ENV must be development, test, staging, or production", ErrInvalid)
	}
	if err := c.ValidateAdminJWTSecret(); err != nil {
		return err
	}
	if c.AdErrorRequestsPerMinute < 1 || c.AdErrorRequestsPerMinute > 60 {
		return fmt.Errorf("%w: ad_error_requests_per_minute must be between 1 and 60", ErrInvalid)
	}
	if c.VisitorTokenTTL <= 0 {
		return fmt.Errorf("%w: visitor_token_ttl_seconds must be positive", ErrInvalid)
	}
	if c.DirectPlaybackTokenTTLSeconds < 60 || c.DirectPlaybackTokenTTLSeconds > 900 {
		return fmt.Errorf("%w: direct_playback_token_ttl_seconds must be between 60 and 900", ErrInvalid)
	}
	if c.IsProductionLike() {
		if !Configured(c.IdentityTokenEncryptionSecret) || c.IdentityTokenEncryptionSecret == "local-development-identity-token-encryption-secret" || len(c.IdentityTokenEncryptionSecret) < 32 {
			return fmt.Errorf("%w: production requires a dedicated 32+ character IDENTITY_TOKEN_ENCRYPTION_SECRET", ErrInvalid)
		}
	}
	// 媒资配置持久化在 MySQL，Redis 仅缓存；启动配置层不要求 YAML 重复保存。
	return nil
}

// 判断当前环境是否执行生产级安全校验。
func (c Config) IsProductionLike() bool {
	return c.Environment == "staging" || c.Environment == "production"
}

// 判断配置值是否为有效的非占位内容。
func Configured(input string) bool {
	normalized := strings.ToLower(strings.TrimSpace(input))
	return normalized != "" && !strings.HasPrefix(normalized, "replace_with_") && !strings.HasPrefix(normalized, "your_")
}

// ValidateAdminJWTSecret 拒绝缺失、占位、空白边界和跨端复用的管理员签名密钥；错误不包含密钥原文。
func (c Config) ValidateAdminJWTSecret() error {
	secret := c.AdminJWTSigningSecret
	if len(secret) < 32 || strings.TrimSpace(secret) != secret || !Configured(secret) {
		return fmt.Errorf("%w: admin_jwt_signing_secret must be a dedicated 32+ byte secret", ErrInvalid)
	}
	if secret == strings.TrimSpace(c.IdentityTokenEncryptionSecret) {
		return fmt.Errorf("%w: admin_jwt_signing_secret must differ from the consumer secret", ErrInvalid)
	}
	if c.IsProductionLike() && (strings.HasPrefix(secret, "local-development-") || strings.HasPrefix(secret, "test-only-")) {
		return fmt.Errorf("%w: production requires a dedicated admin_jwt_signing_secret", ErrInvalid)
	}
	return nil
}
