package adminrbac_test

import (
	"context"
	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/testsupport/memory"
	"errors"
	"github.com/golang-jwt/jwt/v5"
	"testing"
	"time"
)

func TestRefreshTwoHourSession(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	now := time.Now().UTC().Truncate(time.Second)
	service := adminrbac.NewService(store, adminrbac.Config{SigningKey: []byte("test-only-admin-jwt-signing-key-32-bytes"), Now: func() time.Time { return now }})
	createTestSuperAdministrator(t, store, "admin", "local-admin-password")
	ctx := context.Background()
	login, err := service.Login(ctx, "admin", "local-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	parsed, _, err := jwt.NewParser().ParseUnverified(login.Token, jwt.MapClaims{})
	if err != nil {
		t.Fatal(err)
	}
	claims := parsed.Claims.(jwt.MapClaims)
	if claims["exp"].(float64)-claims["iat"].(float64) != 7200 {
		t.Fatal("JWT must last two hours")
	}
	now = now.Add(116 * time.Minute)
	renewed, err := service.Refresh(ctx, login.Token)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(5 * time.Minute)
	if _, err := service.Refresh(ctx, login.Token); !errors.Is(err, adminrbac.ErrUnauthorized) {
		t.Fatalf("expired token refreshed: %v", err)
	}
	if _, err := service.Authenticate(ctx, renewed.Token); err != nil {
		t.Fatal(err)
	}
	if err := service.Logout(ctx, renewed.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Refresh(ctx, renewed.Token); !errors.Is(err, adminrbac.ErrUnauthorized) {
		t.Fatalf("revoked token refreshed: %v", err)
	}
}
