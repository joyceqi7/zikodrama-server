package visitor_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"drama-server/internal/biz/visitor"
	"drama-server/internal/testsupport/memory"
)

func TestVisitorLogsProviderFailureIncludingAnonymousFallback(t *testing.T) {
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	failure := errors.New("identity provider connection refused")
	for _, required := range []bool{true, false} {
		var output bytes.Buffer
		slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
		service, err := visitor.NewService(applicationResolver{applications: map[string]visitor.Application{"app": {ID: "app", TikTokAppID: "app"}}, provider: identityProvider{err: failure}}, memory.NewStore(memory.Seed{}), visitor.Config{RequireTikTokLogin: required, IdentityTokenEncryptionSecret: "private-local-encryption-key-2026"})
		if err != nil {
			t.Fatal(err)
		}
		result, err := service.Bootstrap(context.Background(), visitor.BootstrapInput{TikTokAppID: "app", LoginCode: "private-login-code"})
		if required {
			if !errors.Is(err, visitor.ErrIdentityUnavailable) || !errors.Is(err, failure) {
				t.Fatalf("强制登录错误链改变：%v", err)
			}
		} else if err != nil || result.VisitorToken == "" {
			t.Fatalf("本地匿名降级被改变：%v", err)
		}
		raw := output.String()
		if !strings.Contains(raw, "exchange code failed") || !strings.Contains(raw, failure.Error()) {
			t.Fatalf("身份故障没有打印实际错误：%s", raw)
		}
		for _, secret := range []string{"private-login-code", "private-local-encryption-key-2026", result.VisitorToken} {
			if secret != "" && strings.Contains(raw, secret) {
				t.Fatal("日志泄漏登录码或凭据")
			}
		}
	}
}

// directLoginStore 确保正式登录只使用身份查找创建入口，不触发匿名用户过渡。
type directLoginStore struct {
	visitor.Store
	failure error
}

// CreateVisitor 正式登录调用匿名创建即为测试失败。
func (s directLoginStore) CreateVisitor(context.Context, string, *string) error {
	panic("official login created an anonymous user")
}

// VisitorExists 正式登录只认本次官方身份，不依赖客户端旧凭据来选择用户。
func (s directLoginStore) VisitorExists(context.Context, string, string) (bool, error) {
	panic("official login queried old token identity")
}

// ResolveTikTokIdentity 可注入数据库错误以验证错误链和脱敏日志。
func (s directLoginStore) ResolveTikTokIdentity(ctx context.Context, record visitor.TikTokIdentityRecord) (string, error) {
	if s.failure != nil {
		return "", s.failure
	}
	return s.Store.ResolveTikTokIdentity(ctx, record)
}

func TestDirectLoginLogsStorageFailureWithoutPlatformIdentity(t *testing.T) {
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	store := memory.NewStore(memory.Seed{})
	failure := errors.New("identity lookup database unavailable")
	for _, fail := range []error{nil, failure} {
		var output bytes.Buffer
		slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
		service, err := visitor.NewService(applicationResolver{
			applications: map[string]visitor.Application{"app": {ID: "app", TikTokAppID: "app"}},
			provider:     identityProvider{identity: visitor.Identity{OpenID: "private-open-id"}},
		}, directLoginStore{Store: store, failure: fail}, visitor.Config{
			RequireTikTokLogin: true, IdentityTokenEncryptionSecret: "private-test-credential-key",
		})
		if err != nil {
			t.Fatal(err)
		}
		result, err := service.Bootstrap(context.Background(), visitor.BootstrapInput{TikTokAppID: "app", LoginCode: "private-code", VisitorToken: "private-stale-token"})
		if fail == nil {
			if err != nil || result.VisitorID == "" || result.EventOpenID != "private-open-id" {
				t.Fatalf("direct login failed: %v", err)
			}
		} else if !errors.Is(err, failure) || !errors.Is(err, visitor.ErrServiceUnavailable) || !strings.Contains(output.String(), "resolve TikTok identity failed") || !strings.Contains(output.String(), failure.Error()) {
			t.Fatalf("identity store error lost: %v", err)
		}
		for _, secret := range []string{"private-open-id", "private-code", "private-stale-token", "private-test-credential-key", result.VisitorToken} {
			if secret != "" && strings.Contains(output.String(), secret) {
				t.Fatal("login log leaked identity or credentials")
			}
		}
	}
}

// TestVisitorProviderRejectionsKeepExpectedLevels 验证官方登录码拒绝使用 WARN，超时仍记 ERROR，且不改变身份错误分类。
func TestVisitorProviderRejectionsKeepExpectedLevels(t *testing.T) {
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	for _, tc := range []struct {
		name, level string
		cause, want error
	}{
		{name: "官方拒绝登录码", cause: visitor.ErrUnauthorized, want: visitor.ErrUnauthorized, level: "WARN"},
		{name: "依赖超时", cause: context.DeadlineExceeded, want: visitor.ErrIdentityUnavailable, level: "ERROR"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
			service, err := visitor.NewService(applicationResolver{applications: map[string]visitor.Application{"app": {ID: "app", TikTokAppID: "app"}}, provider: identityProvider{err: tc.cause}}, memory.NewStore(memory.Seed{}), visitor.Config{RequireTikTokLogin: true, IdentityTokenEncryptionSecret: "private-test-visitor-key"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.Bootstrap(context.Background(), visitor.BootstrapInput{TikTokAppID: "app", LoginCode: "private-code"})
			if !errors.Is(err, tc.want) || !errors.Is(err, tc.cause) || !strings.Contains(output.String(), `"level":"`+tc.level+`"`) || !strings.Contains(output.String(), tc.cause.Error()) {
				t.Fatalf("身份错误分类或级别改变: %v %s", err, output.String())
			}
			if strings.Contains(output.String(), "private-code") {
				t.Fatal("登录码泄漏")
			}
		})
	}
}

// cancellingDiagnosticProvider 在登录调用中取消 Context，分别返回取消或无关依赖故障。
type cancellingDiagnosticProvider struct {
	cancel  context.CancelFunc
	failure error
}

// ExchangeCode 只注入取消时机和错误，不访问真实身份平台。
func (p cancellingDiagnosticProvider) ExchangeCode(context.Context, string) (visitor.Identity, error) {
	p.cancel()
	return visitor.Identity{}, p.failure
}

// TestVisitorCancellationDoesNotHideProviderFailure 验证取消时记录实际平台故障，返回值仍遵循原有取消优先合同。
func TestVisitorCancellationDoesNotHideProviderFailure(t *testing.T) {
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	failure := errors.New("identity provider connection refused")
	for _, tc := range []struct {
		name, level string
		cause       error
	}{
		{name: "无关依赖故障", cause: failure, level: "ERROR"},
		{name: "主动取消", cause: fmt.Errorf("identity request: %w", context.Canceled), level: "INFO"},
		{name: "安全分类包装取消", cause: visitor.DependencyError(visitor.ErrIdentityUnavailable, context.Canceled), level: "INFO"},
		{name: "超时", cause: context.DeadlineExceeded, level: "ERROR"},
		{name: "合并取消和依赖故障", cause: fmt.Errorf("identity result: %w", errors.Join(context.Canceled, failure)), level: "ERROR"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			provider := cancellingDiagnosticProvider{cancel: cancel, failure: tc.cause}
			service, err := visitor.NewService(applicationResolver{applications: map[string]visitor.Application{"app": {ID: "app", TikTokAppID: "app"}}, provider: provider}, memory.NewStore(memory.Seed{}), visitor.Config{RequireTikTokLogin: true, IdentityTokenEncryptionSecret: "private-visitor-test-key"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.Bootstrap(ctx, visitor.BootstrapInput{TikTokAppID: "app", LoginCode: "private-login-code"})
			if err != context.Canceled {
				t.Fatalf("原取消优先返回语义改变: %v", err)
			}
			raw := output.String()
			decoder := json.NewDecoder(&output)
			found := 0
			for decoder.More() {
				var entry map[string]any
				if err := decoder.Decode(&entry); err != nil {
					t.Fatal(err)
				}
				if entry["msg"] == "exchange code failed" {
					found++
					if entry["level"] != tc.level || entry["error"] != tc.cause.Error() {
						t.Fatalf("实际平台故障被取消降级: %v", entry)
					}
				}
			}
			if found != 1 || strings.Contains(raw, "private-login-code") || strings.Contains(raw, "private-visitor-test-key") {
				t.Fatal("平台错误漏记、重复或泄漏凭据")
			}
		})
	}
}
