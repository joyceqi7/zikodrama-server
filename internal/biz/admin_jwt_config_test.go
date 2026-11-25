package biz_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"testing"

	"drama-server/internal/biz"
	"drama-server/internal/biz/adminrbac"
	config "drama-server/internal/conf"
	"drama-server/internal/testsupport/memory"
	"golang.org/x/crypto/bcrypt"
)

func TestAdminJWTUsesIndependentSigningSecret(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.NewStore(memory.Seed{})
	hash, err := bcrypt.GenerateFromPassword([]byte("test-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	user := adminrbac.User{Username: "admin", PasswordHash: string(hash), Status: adminrbac.StatusActive, IsSuper: true, AuthVersion: 1}
	if err := store.CreateAdminUser(ctx, &user); err != nil {
		t.Fatal(err)
	}
	settings := config.Config{Environment: "test", IdentityTokenEncryptionSecret: "consumer-secret-original-at-least-32-bytes", AdminJWTSigningSecret: "test-only-independent-admin-signing-secret"}
	service, err := biz.NewAdminRBACService(store, settings)
	if err != nil {
		t.Fatal(err)
	}
	login, err := service.Login(ctx, "admin", "test-password")
	if err != nil {
		t.Fatal(err)
	}
	// 独立配置原文可验证签名，证明不再依赖消费者派生密钥。
	direct := adminrbac.NewService(store, adminrbac.Config{SigningKey: []byte(settings.AdminJWTSigningSecret)})
	if _, err := direct.Authenticate(ctx, login.Token); err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, []byte(settings.IdentityTokenEncryptionSecret))
	mac.Write([]byte("drama/admin-jwt/signing/v1"))
	legacy := adminrbac.NewService(store, adminrbac.Config{SigningKey: mac.Sum(nil)})
	oldLogin, err := legacy.Login(ctx, "admin", "test-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, oldLogin.Token); !errors.Is(err, adminrbac.ErrUnauthorized) {
		t.Fatalf("legacy token accepted: %v", err)
	}
	settings.IdentityTokenEncryptionSecret = "consumer-secret-rotated-at-least-32-bytes"
	other, err := biz.NewAdminRBACService(store, settings)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Authenticate(ctx, login.Token); err != nil {
		t.Fatalf("consumer rotation affected admin: %v", err)
	}
	settings.AdminJWTSigningSecret = "test-only-rotated-admin-signing-secret"
	rotated, err := biz.NewAdminRBACService(store, settings)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rotated.Authenticate(ctx, login.Token); !errors.Is(err, adminrbac.ErrUnauthorized) {
		t.Fatalf("admin rotation did not invalidate token: %v", err)
	}
	settings.AdminJWTSigningSecret = ""
	if _, err := biz.NewAdminRBACService(store, settings); !errors.Is(err, config.ErrInvalid) {
		t.Fatalf("missing key must not fall back: %v", err)
	}
}
