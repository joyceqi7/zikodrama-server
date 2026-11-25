package visitor_test

import (
	"context"
	"drama-server/internal/biz/visitor"
	"drama-server/internal/testsupport/memory"
	"errors"
	"testing"
)

// TestAccountSwitchNeverLinksPreviousAccount 确认同一浏览器凭据不能把两个 TikTok 账号合并。
func TestAccountSwitchNeverLinksPreviousAccount(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	first := newVisitorServiceWithStore(t, store, identityProvider{identity: visitor.Identity{OpenID: "account-a"}}, "5021")
	a, err := first.Bootstrap(context.Background(), visitor.BootstrapInput{TikTokAppID: "1", LoginCode: "code-a"})
	if err != nil {
		t.Fatal(err)
	}
	second := newVisitorServiceWithStore(t, store, identityProvider{identity: visitor.Identity{OpenID: "account-b"}}, "visitor-b")
	b, err := second.Bootstrap(context.Background(), visitor.BootstrapInput{TikTokAppID: "1", LoginCode: "code-b", VisitorToken: a.VisitorToken})
	if err != nil {
		t.Fatal(err)
	}
	if a.VisitorID == b.VisitorID || b.EventOpenID != "account-b" {
		t.Fatal("两个 TikTok 账号共享了业务主体")
	}
	if _, err := first.Resolve(context.Background(), a.VisitorToken); err != nil {
		t.Fatal("原账号被错误合并或删除", err)
	}
	again, err := first.Bootstrap(context.Background(), visitor.BootstrapInput{TikTokAppID: "1", LoginCode: "code-a-again", VisitorToken: a.VisitorToken})
	if err != nil || again.VisitorID != a.VisitorID {
		t.Fatal("同账号没有继续原主体", err)
	}
}

// TestProductionRequiresSilentLogin 禁止缺配置、空登录码或交换失败时使用匿名兜底。
func TestProductionRequiresSilentLogin(t *testing.T) {
	for _, provider := range []visitor.IdentityProvider{nil, identityProvider{err: errors.New("network failure")}} {
		s, err := visitor.NewService(applicationResolver{applications: map[string]visitor.Application{"app": {ID: "app", TikTokAppID: "app"}}, provider: provider}, memory.NewStore(memory.Seed{}), visitor.Config{RequireTikTokLogin: true, IdentityTokenEncryptionSecret: "local-test-visitor-identity-protection-key"})
		if err != nil {
			t.Fatal(err)
		}
		for _, code := range []string{"", "invalid-code"} {
			want := visitor.ErrUnauthorized
			if code != "" {
				want = visitor.ErrIdentityUnavailable
			}
			if result, err := s.Bootstrap(context.Background(), visitor.BootstrapInput{TikTokAppID: "app", LoginCode: code}); !errors.Is(err, want) || result.VisitorToken != "" {
				t.Fatalf("生产匿名降级或错误分类不正确：want=%v got=%v", want, err)
			}
		}
	}
}
