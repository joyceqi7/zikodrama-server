package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	styxconfig "gitee.com/linpbin/styx/api/config/v1"
	kratosconfig "github.com/go-kratos/kratos/v2/config"
	"github.com/go-kratos/kratos/v2/config/file"

	config "drama-server/internal/conf"
)

// TestNewConfigReadsYAML 验证业务配置和两套独立数据库连接从 YAML 分别加载。
func TestNewConfigReadsYAML(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.yaml")
	contents := `app:
  name: drama-api
  version: test
  env: test
server:
  http:
    addr: ":18080"
data:
  mysql:
    master: "shared-mysql-dsn"
  reporting_mysql:
    master: "reporting-mysql-dsn"
    max_open_conns: 8
    max_idle_conns: 3
business:
  visitor_token_ttl_seconds: 86400
  ad_error_requests_per_minute: 6
  direct_playback_token_ttl_seconds: 300
  identity_token_encryption_secret: test-identity-key
  admin_jwt_signing_secret: test-only-admin-jwt-signing-secret-32-bytes
  legacy_tiktok_client_secrets:
    OLD_APP_SECRET: test-only-legacy-secret

`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	loader := kratosconfig.New(kratosconfig.WithSource(file.NewSource(path)))
	t.Cleanup(func() {
		if err := loader.Close(); err != nil {
			t.Error("关闭测试配置加载器失败", err)
		}
	})
	if err := loader.Load(); err != nil {
		t.Fatal(err)
	}
	var bootstrap config.Bootstrap
	if err := loader.Scan(&bootstrap); err != nil {
		t.Fatal(err)
	}
	if reporting := bootstrap.GetData().GetReportingMysql(); reporting == nil || reporting.GetMaster() != "reporting-mysql-dsn" || reporting.GetMaxOpenConns() != 8 || reporting.GetMaxIdleConns() != 3 {
		t.Fatal("独立统计连接池配置未从 YAML 加载")
	}
	if bootstrap.GetData().GetMysql().GetMaster() != "shared-mysql-dsn" {
		t.Fatal("统计连接配置覆盖了业务连接配置")
	}
	loaded, err := config.NewConfig(&bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.AdErrorRequestsPerMinute != 6 {
		t.Fatal("广告日志限流参数未从 YAML 投影")
	}
	if loaded.VisitorTokenTTL != 24*time.Hour {
		t.Fatal("消费者凭据有效期未从 YAML 读取为 24 小时")
	}
	if loaded.DirectPlaybackTokenTTLSeconds != 300 || loaded.Environment != "test" || loaded.IdentityTokenEncryptionSecret != "test-identity-key" || loaded.AdminJWTSigningSecret != "test-only-admin-jwt-signing-secret-32-bytes" || loaded.LegacyTikTokClientSecrets["OLD_APP_SECRET"] != "test-only-legacy-secret" {
		t.Fatal("配置文件未正确投影业务配置")
	}
}

func TestNewConfigRejectsIncompleteBootstrap(t *testing.T) {
	t.Parallel()
	if _, err := config.NewConfig(&config.Bootstrap{}); !errors.Is(err, config.ErrMissingBootstrap) {
		t.Fatalf("不完整 Bootstrap 应被拒绝：%v", err)
	}
}

func TestNewConfigDoesNotValidateInfrastructureConfiguration(t *testing.T) {
	t.Parallel()
	bootstrap := &config.Bootstrap{
		App: &styxconfig.AppInfo{Env: "test"},
		Business: &config.Business{
			VisitorTokenTtlSeconds:        86400,
			AdErrorRequestsPerMinute:      6,
			IdentityTokenEncryptionSecret: "test-identity-key",
			AdminJwtSigningSecret:         "test-only-admin-jwt-signing-secret-32-bytes",
			DirectPlaybackTokenTtlSeconds: 300,
		},
	}
	if _, err := config.NewConfig(bootstrap); err != nil {
		t.Fatalf("业务配置不应校验 HTTP 或 MySQL：%v", err)
	}
}

func TestDevelopmentConfigurationIsValid(t *testing.T) {
	t.Parallel()
	settings := validDevelopmentConfig()
	if err := settings.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestTestConfigurationAllowsDatabaseMedia(t *testing.T) {
	t.Parallel()
	settings := validDevelopmentConfig()
	settings.Environment = "test"
	if err := settings.Validate(); err != nil {
		t.Fatalf("测试环境允许从全局配置表加载 media：%v", err)
	}
}

func TestProductionRequiresStrongSecrets(t *testing.T) {
	t.Parallel()
	settings := validProductionConfig()
	if err := settings.Validate(); err != nil {
		t.Fatalf("完整生产配置应通过：%v", err)
	}
	settings.IdentityTokenEncryptionSecret = ""
	if err := settings.Validate(); !errors.Is(err, config.ErrInvalid) {
		t.Fatalf("生产环境必须提供独立身份加密密钥：%v", err)
	}
}

func validDevelopmentConfig() config.Config {
	return config.Config{
		VisitorTokenTTL:               24 * time.Hour,
		AdErrorRequestsPerMinute:      6,
		Environment:                   "development",
		DirectPlaybackTokenTTLSeconds: 300,
		IdentityTokenEncryptionSecret: "local-development-identity-token-encryption-secret",
		AdminJWTSigningSecret:         "local-development-admin-jwt-signing-secret",
	}
}

func validProductionConfig() config.Config {
	settings := validDevelopmentConfig()
	settings.Environment = "production"
	settings.AdminJWTSigningSecret = "production-admin-jwt-signing-secret-at-least-32-bytes"
	settings.IdentityTokenEncryptionSecret = "production-identity-token-encryption-secret-at-least-32-bytes"
	return settings
}

func TestAdminJWTSecretValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, secret string }{
		{"missing", ""}, {"short", "short"},
		{"placeholder", "replace_with_admin_jwt_signing_secret"},
		{"development", "local-development-admin-jwt-signing-secret"},
		{"test", "test-only-admin-jwt-signing-secret-32-bytes"},
		{"whitespace", " production-admin-jwt-signing-secret-at-least-32-bytes"},
		{"consumer_reuse", validProductionConfig().IdentityTokenEncryptionSecret},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings := validProductionConfig()
			settings.AdminJWTSigningSecret = tc.secret
			err := settings.Validate()
			if !errors.Is(err, config.ErrInvalid) {
				t.Fatalf("invalid admin secret accepted: %v", err)
			}
			if tc.secret != "" && strings.Contains(err.Error(), tc.secret) {
				t.Fatal("error leaked secret")
			}
		})
	}
	settings := validDevelopmentConfig()
	settings.AdminJWTSigningSecret = ""
	if !errors.Is(settings.Validate(), config.ErrInvalid) {
		t.Fatal("missing admin secret allowed in development")
	}
}

// TestVisitorTokenTTLValidation 拒绝缺失或非正有效期，允许配置文件指定正的消费者凭据时长。
func TestVisitorTokenTTLValidation(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		ttl   time.Duration
		valid bool
	}{
		{"未配置", 0, false},
		{"负值", -time.Second, false},
		{"二十四小时", 24 * time.Hour, true},
		{"其他有效配置", 6 * time.Hour, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			settings := validDevelopmentConfig()
			settings.VisitorTokenTTL = test.ttl
			err := settings.Validate()
			if test.valid && err != nil {
				t.Fatalf("正的凭据有效期应通过校验：%v", err)
			}
			if !test.valid && (!errors.Is(err, config.ErrInvalid) || !strings.Contains(err.Error(), "visitor_token_ttl_seconds")) {
				t.Fatalf("无效凭据有效期应返回明确的配置错误：%v", err)
			}
		})
	}
}

// TestAdErrorRateConfiguration 验证广告日志限流参数来自配置且缺失或越界时拒绝启动。
func TestAdErrorRateConfiguration(t *testing.T) {
	for _, limit := range []int{-1, 0, 1, 6, 60, 61} {
		settings := validDevelopmentConfig()
		settings.AdErrorRequestsPerMinute = limit
		err := settings.Validate()
		if (limit >= 1 && limit <= 60) != (err == nil) {
			t.Fatalf("广告日志额度校验错误: %d %v", limit, err)
		}
	}
}
