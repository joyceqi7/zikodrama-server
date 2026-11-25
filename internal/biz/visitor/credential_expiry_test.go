package visitor

import (
	"context"
	"errors"
	"testing"
	"time"
)

type expiryApplicationResolver struct{}

func (expiryApplicationResolver) Resolve(context.Context, string) (Application, IdentityProvider, error) {
	return Application{SubjectID: "1", ID: "101", TikTokAppID: "mini-app"}, expiryIdentityProvider{}, nil
}

type expiryIdentityProvider struct{}

func (expiryIdentityProvider) ExchangeCode(context.Context, string) (Identity, error) {
	return Identity{OpenID: "same-official-account"}, nil
}

type expiryVisitorStore struct{ version uint64 }

func (s *expiryVisitorStore) CreateVisitor(context.Context, string, *string) error {
	return errors.New("unexpected anonymous create")
}
func (s *expiryVisitorStore) VisitorExists(context.Context, string, string) (bool, error) {
	return true, nil
}
func (s *expiryVisitorStore) ResolveTikTokIdentity(context.Context, TikTokIdentityRecord) (string, error) {
	return "101000", nil
}
func (s *expiryVisitorStore) VisitorCredentialVersionRef(context.Context, string, UserRef) (uint64, bool, error) {
	return s.version, true, nil
}

func TestVisitorCredentialExpiryAndSilentSameAccountRecovery(t *testing.T) {
	store := &expiryVisitorStore{version: 1}
	service, err := NewService(expiryApplicationResolver{}, store, Config{
		RequireTikTokLogin: true, IdentityTokenEncryptionSecret: "local-test-visitor-identity-protection-key",
		VisitorTokenTTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return clock }
	first, err := service.Bootstrap(context.Background(), BootstrapInput{TikTokAppID: "mini-app", LoginCode: "code-one"})
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(61 * time.Second)
	if _, err := service.Resolve(context.Background(), first.VisitorToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("到期凭据应返回可静默恢复的未授权分类: %v", err)
	}
	second, err := service.Bootstrap(context.Background(), BootstrapInput{
		TikTokAppID: "mini-app", LoginCode: "code-two", VisitorToken: first.VisitorToken,
	})
	if err != nil || second.VisitorID != first.VisitorID || second.VisitorToken == first.VisitorToken {
		t.Fatalf("静默登录应恢复同一用户并签发新凭据: first=%q second=%q err=%v", first.VisitorID, second.VisitorID, err)
	}
	if _, err := service.Resolve(context.Background(), second.VisitorToken); err != nil {
		t.Fatalf("新凭据应立即可用: %v", err)
	}
	store.version++
	if _, err := service.Resolve(context.Background(), second.VisitorToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("版本递增应撤销旧凭据并触发同一恢复路径: %v", err)
	}
}

// TestVisitorCredential24HourLifetime 确认新凭据超过两小时仍有效，并在签发满 24 小时的边界失效。
func TestVisitorCredential24HourLifetime(t *testing.T) {
	for _, test := range []struct {
		name string
		ttl  time.Duration
	}{
		{"领域默认值", 0},
		{"显式配置", 24 * time.Hour},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, err := NewService(expiryApplicationResolver{}, &expiryVisitorStore{version: 1}, Config{
				RequireTikTokLogin: true, IdentityTokenEncryptionSecret: "local-test-visitor-identity-protection-key",
				VisitorTokenTTL: test.ttl,
			})
			if err != nil {
				t.Fatal(err)
			}
			issuedAt := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
			clock := issuedAt
			service.now = func() time.Time { return clock }
			created, err := service.Bootstrap(context.Background(), BootstrapInput{TikTokAppID: "mini-app", LoginCode: "code"})
			if err != nil {
				t.Fatal(err)
			}
			for _, elapsed := range []time.Duration{2*time.Hour + time.Second, 24*time.Hour - time.Second} {
				clock = issuedAt.Add(elapsed)
				if _, err := service.Resolve(context.Background(), created.VisitorToken); err != nil {
					t.Fatalf("24 小时内的凭据不应提前失效：elapsed=%s err=%v", elapsed, err)
				}
			}
			clock = issuedAt.Add(24 * time.Hour)
			if _, err := service.Resolve(context.Background(), created.VisitorToken); !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("普通请求不能续期，签发满 24 小时应拒绝旧凭据：%v", err)
			}
		})
	}
}

// TestVisitorCredentialLifetimeChangePreservesIssuedExpiry 确认调整配置不延长旧凭据，重新登录才签发新有效期。
func TestVisitorCredentialLifetimeChangePreservesIssuedExpiry(t *testing.T) {
	store := &expiryVisitorStore{version: 1}
	issuedAt := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	clock := issuedAt
	newService := func(ttl time.Duration) *Service {
		service, err := NewService(expiryApplicationResolver{}, store, Config{
			RequireTikTokLogin: true, IdentityTokenEncryptionSecret: "local-test-visitor-identity-protection-key",
			VisitorTokenTTL: ttl,
		})
		if err != nil {
			t.Fatal(err)
		}
		service.now = func() time.Time { return clock }
		return service
	}
	previous := newService(2 * time.Hour)
	old, err := previous.Bootstrap(context.Background(), BootstrapInput{TikTokAppID: "mini-app", LoginCode: "old-code"})
	if err != nil {
		t.Fatal(err)
	}
	current := newService(24 * time.Hour)
	clock = issuedAt.Add(2*time.Hour - time.Second)
	if _, err := current.Resolve(context.Background(), old.VisitorToken); err != nil {
		t.Fatalf("配置调整不应提前撤销尚未到期的凭据：%v", err)
	}
	clock = issuedAt.Add(2 * time.Hour)
	if _, err := current.Resolve(context.Background(), old.VisitorToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("旧凭据仍应按原来的两小时到期：%v", err)
	}
	recovered, err := current.Bootstrap(context.Background(), BootstrapInput{
		TikTokAppID: "mini-app", LoginCode: "new-code", VisitorToken: old.VisitorToken,
	})
	if err != nil || recovered.VisitorID != old.VisitorID {
		t.Fatalf("重新登录应恢复同一用户：%v", err)
	}
	clock = clock.Add(24*time.Hour - time.Second)
	if _, err := current.Resolve(context.Background(), recovered.VisitorToken); err != nil {
		t.Fatalf("重新签发的凭据应使用 24 小时有效期：%v", err)
	}
	clock = clock.Add(time.Second)
	if _, err := current.Resolve(context.Background(), recovered.VisitorToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("重新签发的凭据满 24 小时后应失效：%v", err)
	}
}
