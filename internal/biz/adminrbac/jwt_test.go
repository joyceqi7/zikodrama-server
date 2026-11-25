package adminrbac_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"drama-server/internal/biz/adminrbac"
	"github.com/golang-jwt/jwt/v5"
)

func TestAdminJWTValidation(t *testing.T) {
	service, store := newService(t)
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
	original := parsed.Claims.(jwt.MapClaims)
	key := []byte("test-only-admin-jwt-signing-key-32-bytes")
	if _, err := service.Authenticate(ctx, login.Token); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		alter  func(jwt.MapClaims)
		method jwt.SigningMethod
		key    any
	}{
		{name: "wrong signature", key: []byte("another-admin-jwt-signing-key-32-bytes")},
		{name: "wrong algorithm", method: jwt.SigningMethodHS384},
		{name: "unsigned", method: jwt.SigningMethodNone, key: jwt.UnsafeAllowNoneSignatureType},
		{name: "wrong issuer", alter: func(c jwt.MapClaims) { c["iss"] = "visitor" }},
		{name: "missing issuer", alter: func(c jwt.MapClaims) { delete(c, "iss") }},
		{name: "wrong audience", alter: func(c jwt.MapClaims) { c["aud"] = "consumer-api" }},
		{name: "missing audience", alter: func(c jwt.MapClaims) { delete(c, "aud") }},
		{name: "expired", alter: func(c jwt.MapClaims) { c["exp"] = c["iat"] }},
		{name: "missing expiration", alter: func(c jwt.MapClaims) { delete(c, "exp") }},
		{name: "missing issued time", alter: func(c jwt.MapClaims) { delete(c, "iat") }},
		{name: "future issued time", alter: func(c jwt.MapClaims) { c["iat"] = c["exp"] }},
		{name: "missing account", alter: func(c jwt.MapClaims) { delete(c, "sub") }},
		{name: "numeric alias", alter: func(c jwt.MapClaims) { c["sub"] = "01" }},
		{name: "missing version", alter: func(c jwt.MapClaims) { delete(c, "auth_version") }},
		{name: "wrong version", alter: func(c jwt.MapClaims) { c["auth_version"] = 999 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims := jwt.MapClaims{}
			for k, v := range original {
				claims[k] = v
			}
			if tc.alter != nil {
				tc.alter(claims)
			}
			if tc.method == nil {
				tc.method = jwt.SigningMethodHS256
			}
			if tc.key == nil {
				tc.key = key
			}
			token, err := jwt.NewWithClaims(tc.method, claims).SignedString(tc.key)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.Authenticate(ctx, token); !errors.Is(err, adminrbac.ErrUnauthorized) {
				t.Fatalf("invalid JWT accepted: %v", err)
			}
		})
	}
	for _, raw := range []string{"", "old-opaque-token", strings.Repeat("x", 8193)} {
		if _, err := service.Authenticate(ctx, raw); !errors.Is(err, adminrbac.ErrUnauthorized) {
			t.Fatalf("malformed token accepted: %v", err)
		}
	}
	for _, key := range [][]byte{nil, []byte("too-short")} {
		unconfigured := adminrbac.NewService(store, adminrbac.Config{SigningKey: key})
		if _, err := unconfigured.Login(ctx, "admin", "local-admin-password"); !errors.Is(err, adminrbac.ErrUnauthorized) {
			t.Fatalf("login without valid key: %v", err)
		}
		if _, err := unconfigured.Authenticate(ctx, login.Token); !errors.Is(err, adminrbac.ErrUnauthorized) {
			t.Fatalf("authentication without valid key: %v", err)
		}
	}
}

func TestAdminJWTLogoutRevokesAccountVersionAndIsIdempotent(t *testing.T) {
	service, store := newService(t)
	createTestSuperAdministrator(t, store, "admin", "local-admin-password")
	ctx := context.Background()
	login := func() string {
		t.Helper()
		result, err := service.Login(ctx, "admin", "local-admin-password")
		if err != nil {
			t.Fatal(err)
		}
		return result.Token
	}
	first, second := login(), login()
	if err := service.Logout(ctx, first); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, second); !errors.Is(err, adminrbac.ErrUnauthorized) {
		t.Fatalf("other login of same version remained valid: %v", err)
	}
	fresh := login()
	if err := service.Logout(ctx, first); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, fresh); err != nil {
		t.Fatalf("replayed logout revoked fresh login: %v", err)
	}
	if err := service.Logout(ctx, "malformed-token"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, fresh); err != nil {
		t.Fatal(err)
	}
}

func TestAdminJWTLogoutLogsRepositoryFailureWithoutToken(t *testing.T) {
	service, store := newService(t)
	createTestSuperAdministrator(t, store, "admin", "local-admin-password")
	ctx := context.Background()
	login, err := service.Login(ctx, "admin", "local-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("database unavailable")
	repo := &serviceLoggingRepository{failure: failure}
	service = adminrbac.NewService(repo, adminrbac.Config{
		SigningKey: []byte("test-only-admin-jwt-signing-key-32-bytes"),
		Now:        func() time.Time { return time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC) },
	})
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	var output bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	if err := service.Logout(ctx, login.Token); err != failure {
		t.Fatalf("repository error lost: %v", err)
	}
	if !strings.Contains(output.String(), "revoke admin tokens failed") || !strings.Contains(output.String(), failure.Error()) {
		t.Fatal("missing failure log")
	}
	if strings.Contains(output.String(), login.Token) {
		t.Fatal("token leaked into logs")
	}
}
