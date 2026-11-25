package consumer_test

import (
	"context"
	"testing"

	"drama-server/internal/biz/visitor"
)

type testApplicationResolver struct{}

func (testApplicationResolver) Resolve(_ context.Context, code string) (visitor.Application, visitor.IdentityProvider, error) {
	if code != "1" {
		return visitor.Application{}, nil, visitor.ErrUnauthorized
	}
	return visitor.Application{SubjectID: "1",
		ID: "1", TikTokAppID: "1", Name: "Drama Minis", TimeZone: "UTC",
		ComplianceBaseURL: "https://legal.example/apps/1",
	}, nil, nil
}

func newTestVisitorService(t *testing.T, store visitor.Store) *visitor.Service {
	t.Helper()
	service, err := visitor.NewService(testApplicationResolver{}, store, visitor.Config{
		IdentityTokenEncryptionSecret: "local-test-visitor-identity-protection-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func newTestVisitorServiceWithResolver(t *testing.T, resolver visitor.ApplicationResolver, store visitor.Store) *visitor.Service {
	t.Helper()
	service, err := visitor.NewService(resolver, store, visitor.Config{
		IdentityTokenEncryptionSecret: "local-test-visitor-identity-protection-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}
