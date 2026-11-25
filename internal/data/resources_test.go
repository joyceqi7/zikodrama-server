package data

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
	mysqldriver "github.com/go-sql-driver/mysql"
	"google.golang.org/protobuf/types/known/durationpb"

	styxobserve "gitee.com/linpbin/styx/observe"
	styxruntime "gitee.com/linpbin/styx/runtime"

	"drama-server/internal/biz/visitor"
	conf "drama-server/internal/conf"
	"drama-server/internal/data/persistence/mysqlstore"
)

// TestResourcesRedisFailureIntegration 验证 Redis 初始化故障不阻断 MySQL 服务和就绪检查。
func TestResourcesRedisFailureIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("需配置隔离 MySQL")
	}
	runtime, err := styxruntime.New(styxruntime.Config{Identity: styxruntime.Identity{Name: "cache-failure-test"}, Dependencies: styxobserve.NoopDependencies()})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Shutdown(context.Background())
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	resources, err := NewResources(context.Background(), &conf.Data{
		Mysql:          &configv1.MySQLConfig{Master: dsn},
		ReportingMysql: &configv1.MySQLConfig{Master: dsn, MaxOpenConns: 10, MaxIdleConns: 5},
		Redis:          &configv1.RedisConfig{Mode: "standalone", Addrs: []string{"127.0.0.1:1"}, DialTimeout: durationpb.New(50 * time.Millisecond)},
	}, conf.Config{}, runtime)
	if err != nil {
		t.Fatal("缓存故障阻止启动", err)
	}
	if resources.redis != nil || resources.catalogCache != nil {
		t.Fatal("故障缓存仍被启用")
	}
	if err := resources.ready(context.Background()); err != nil {
		t.Fatal("缓存故障阻止就绪", err)
	}
	if !strings.Contains(logs.String(), "initialize Redis cache failed; serving from MySQL") || !strings.Contains(logs.String(), "ping failed") {
		t.Fatal("缺少缓存降级实际错误日志", logs.String())
	}
}

// TestResourcesRejectMissingSharedMySQL 验证缺少业务主库时拒绝启动。
func TestResourcesRejectMissingSharedMySQL(t *testing.T) {
	runtime, err := styxruntime.New(styxruntime.Config{
		Identity:     styxruntime.Identity{Name: "data-test"},
		Dependencies: styxobserve.NoopDependencies(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Shutdown(context.Background()) })

	if _, err := NewResources(context.Background(), &conf.Data{}, conf.Config{}, runtime); !errors.Is(err, conf.ErrInvalid) {
		t.Fatalf("缺少共享 MySQL 时必须拒绝启动，got %v", err)
	}
}

// TestResourcesRejectMissingReportingMySQL 验证统计连接独立必填，在创建任何连接前失败并记录可诊断日志。
func TestResourcesRejectMissingReportingMySQL(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config *configv1.MySQLConfig
	}{
		{name: "缺少整个统计配置"},
		{name: "缺少统计地址", config: &configv1.MySQLConfig{MaxOpenConns: 10, MaxIdleConns: 5}},
		{name: "统计地址全为空白", config: &configv1.MySQLConfig{Master: "  \t "}},
		{name: "统计地址仍为占位内容", config: &configv1.MySQLConfig{Master: "REPLACE_WITH_REPORTING_MYSQL_DSN"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			defer slog.SetDefault(previous)
			// 不提供 Runtime；若先创建业务连接而未校验统计配置，错误就不再是配置缺失。
			resources, err := NewResources(context.Background(), &conf.Data{
				Mysql:          &configv1.MySQLConfig{Master: "private-business-connection"},
				ReportingMysql: tc.config,
			}, conf.Config{}, nil)
			if resources != nil || !errors.Is(err, conf.ErrInvalid) || !strings.Contains(err.Error(), "data.reporting_mysql.master") {
				t.Fatalf("缺失的统计连接没有在初始化前被拒绝：%v", err)
			}
			var record struct {
				Message string `json:"msg"`
				Error   string `json:"error"`
			}
			if err := json.Unmarshal(logs.Bytes(), &record); err != nil {
				t.Fatal("统计配置失败没有输出单条结构化日志", err)
			}
			if record.Message != "validate reporting MySQL data configuration failed" || record.Error != err.Error() {
				t.Fatal("统计配置失败日志缺少操作或具体错误原因")
			}
			if strings.Contains(logs.String(), "private-business-connection") || strings.Contains(logs.String(), "REPLACE_WITH_REPORTING_MYSQL_DSN") {
				t.Fatal("统计配置校验日志泄露了连接配置")
			}
		})
	}
}

// TestResourcesReportingConnectionFailureIntegration 验证统计地址不可用时保留真实错误，不回退业务连接。
func TestResourcesReportingConnectionFailureIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("需配置隔离 TEST_MYSQL_DSN")
	}
	reportingConfig, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		t.Fatal("隔离 MySQL DSN 格式错误")
	}
	reportingConfig.Net = "tcp"
	reportingConfig.Addr = "127.0.0.1:1"
	reportingConfig.Timeout = 50 * time.Millisecond
	runtime, err := styxruntime.New(styxruntime.Config{
		Identity:     styxruntime.Identity{Name: "reporting-connection-failure-test"},
		Dependencies: styxobserve.NoopDependencies(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := runtime.Shutdown(context.Background()); err != nil {
			t.Error("关闭隔离数据库运行依赖失败", err)
		}
	})
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	resources, err := NewResources(context.Background(), &conf.Data{
		Mysql:          &configv1.MySQLConfig{Master: dsn},
		ReportingMysql: &configv1.MySQLConfig{Master: reportingConfig.FormatDSN()},
	}, conf.Config{}, runtime)
	if resources != nil || err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("统计连接失败被业务配置掩盖或丢失真实原因：%v", err)
	}
	var record struct {
		Message string `json:"msg"`
		Error   string `json:"error"`
	}
	if parseErr := json.Unmarshal(logs.Bytes(), &record); parseErr != nil {
		t.Fatal("统计连接失败没有输出单条结构化日志", parseErr)
	}
	if record.Message != "initialize reporting MySQL data resources failed" || record.Error != err.Error() {
		t.Fatal("统计连接失败日志没有保留具体操作与错误详情")
	}
	if strings.Contains(logs.String(), dsn) || strings.Contains(logs.String(), reportingConfig.FormatDSN()) {
		t.Fatal("统计连接失败日志泄露了连接串")
	}
}

// TestVisitorRepositoryKeepsShardedStore 防止身份仓储包装丢失桶路由而把所有用户错误定位到桶 0。
func TestVisitorRepositoryKeepsShardedStore(t *testing.T) {
	store := mysqlstore.NewStore(nil)
	repository := NewVisitorRepository(&Resources{store: store})
	if _, ok := any(repository).(visitor.ShardedStore); !ok {
		t.Fatal("生产身份仓储必须保留创建、恢复和校验用户的分桶接口")
	}
	if repository.ShardedStore != store {
		t.Fatal("生产分桶接口必须指向同一个 MySQL 身份仓储，不能为空或另建身份来源")
	}
}

// TestVisitorRepositoryKeepsCampaignResolver 防止组合根在收窄 Visitor 仓储时
// 丢失投放解析能力；否则有效 campaignCode 会被静默当作自然流量。
func TestVisitorRepositoryKeepsCampaignResolver(t *testing.T) {
	repository := NewVisitorRepository(&Resources{store: mysqlstore.NewStore(nil)})
	if repository.Store == nil {
		t.Fatal("Visitor 仓储必须保留身份持久化端口")
	}
	if repository.CampaignResolver == nil {
		t.Fatal("Visitor 仓储必须保留投放解析端口")
	}
}

// routedVisitorFixture 只实现带桶端口，旧端口为 nil 可及时暴露生产包装的能力丢失。
type routedVisitorFixture struct {
	visitor.Store
}

// CreateVisitorRef 模拟非零桶匿名用户，验证凭据签发不丢失路由。
func (routedVisitorFixture) CreateVisitorRef(context.Context, string) (visitor.UserRef, error) {
	return visitor.UserRef{ID: "9007199254740993", BucketID: 727}, nil
}

// ResolveTikTokIdentityRef 模拟恢复同一非零桶的官方身份。
func (routedVisitorFixture) ResolveTikTokIdentityRef(context.Context, visitor.TikTokIdentityRecord) (visitor.UserRef, error) {
	return visitor.UserRef{ID: "9007199254740993", BucketID: 727}, nil
}

// VisitorExistsRef 拒绝错误的桶号，避免测试通过跨表查找掩盖路由丢失。
func (routedVisitorFixture) VisitorExistsRef(_ context.Context, _ string, user visitor.UserRef) (bool, error) {
	return user.ID == "9007199254740993" && user.BucketID == 727, nil
}

type routedApplicationFixture struct{}

// Resolve 返回隔离应用和测试身份提供方，不调用真实 TikTok。
func (routedApplicationFixture) Resolve(context.Context, string) (visitor.Application, visitor.IdentityProvider, error) {
	return visitor.Application{SubjectID: "1", ID: "1", TikTokAppID: "app"}, routedIdentityFixture{}, nil
}

type routedIdentityFixture struct{}

// ExchangeCode 仅返回测试 OpenID，验证官方登录也保留非零桶。
func (routedIdentityFixture) ExchangeCode(context.Context, string) (visitor.Identity, error) {
	return visitor.Identity{OpenID: "test-open-id"}, nil
}

// TestVisitorRepositoryCredentialKeepsBucket 覆盖生产包装下匿名与官方登录、凭据恢复的完整桶号传递。
func TestVisitorRepositoryCredentialKeepsBucket(t *testing.T) {
	for _, code := range []string{"", "test-login-code"} {
		t.Run(code, func(t *testing.T) {
			backing := routedVisitorFixture{}
			repository := &VisitorRepository{Store: backing, ShardedStore: backing}
			service, err := visitor.NewService(routedApplicationFixture{}, repository, visitor.Config{IdentityTokenEncryptionSecret: "local-test-sharded-visitor-secret"})
			if err != nil {
				t.Fatal(err)
			}
			created, err := service.Bootstrap(context.Background(), visitor.BootstrapInput{TikTokAppID: "app", LoginCode: code})
			if err != nil {
				t.Fatal(err)
			}
			restored, err := service.Resolve(context.Background(), created.VisitorToken)
			if err != nil {
				t.Fatal(err)
			}
			if created.BucketID != 727 || restored.BucketID != 727 {
				t.Fatal("签发或恢复凭据丢失了用户分桶")
			}
		})
	}
}
