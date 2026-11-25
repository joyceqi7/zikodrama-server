package operations_test

import (
	"drama-server/internal/biz/operations"
	operationshttp "drama-server/internal/service/operations"
	"drama-server/internal/testsupport/memory"
	"net/http"
	"strings"
	"testing"
)

func TestSessionRefreshHTTP(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	handler := operationshttp.NewHandler(operations.NewService(memory.OperationsRepositories(store), nil), newRBACService(t, store), operationshttp.Config{})
	login := request(t, handler, http.MethodPost, "/admin/v1/session", "application/json", "", strings.NewReader(`{"username":"admin","password":"local-admin-password"}`))
	var credentials struct {
		Token string `json:"token"`
	}
	decodeData(t, login, &credentials)
	refreshed := request(t, handler, http.MethodPost, "/admin/v1/session/refresh", "", credentials.Token, nil)
	if refreshed.Code != http.StatusOK {
		t.Fatalf("refresh status %d: %s", refreshed.Code, refreshed.Body.String())
	}
	request(t, handler, http.MethodDelete, "/admin/v1/session", "", credentials.Token, nil)
	for _, token := range []string{"", "invalid-token", credentials.Token} {
		rejected := request(t, handler, http.MethodPost, "/admin/v1/session/refresh", "", token, nil)
		if rejected.Code != http.StatusUnauthorized {
			t.Fatalf("invalid refresh status %d", rejected.Code)
		}
	}
}
