package operations_test

import (
	"net/http"
	"strings"
	"testing"

	"drama-server/internal/biz/operations"
	operationshttp "drama-server/internal/service/operations"
	"drama-server/internal/testsupport/memory"
)

func TestDramaMetadataReferenceRequiresReadPermissionAndReturnsVersionedCatalog(t *testing.T) {
	t.Parallel()

	store := memory.NewStore(memory.Seed{})
	handler := operationshttp.NewHandler(operations.NewService(memory.OperationsRepositories(store), nil), newRBACService(t, store), operationshttp.Config{})
	unauthorized := request(t, handler, http.MethodGet, "/admin/v1/drama-reference-data", "", "", nil)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("reference data must require authentication, got %d", unauthorized.Code)
	}
	login := request(t, handler, http.MethodPost, "/admin/v1/session", "application/json", "", strings.NewReader(`{"username":"admin","password":"local-admin-password"}`))
	var credentials struct {
		Token string `json:"token"`
	}
	decodeData(t, login, &credentials)
	response := request(t, handler, http.MethodGet, "/admin/v1/drama-reference-data", "", credentials.Token, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("reference data failed: %d %s", response.Code, response.Body.String())
	}
	var catalog operations.DramaMetadataReference
	decodeData(t, response, &catalog)
	if catalog.SchemaVersion != "2026-07-13" || len(catalog.Languages) != 19 || len(catalog.Tags) != 45 {
		t.Fatalf("unexpected reference data: %+v", catalog)
	}
}
