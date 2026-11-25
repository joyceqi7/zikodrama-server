package operations_test

import (
	"context"
	"drama-server/internal/biz/globalconfig"
	"drama-server/internal/biz/operations"
	operationshttp "drama-server/internal/service/operations"
	"drama-server/internal/testsupport/memory"
	"net/http"
	"strings"
	"testing"
)

type configRepo struct {
	row  globalconfig.Entry
	puts int
}

func (r *configRepo) List(context.Context) ([]globalconfig.Entry, error) {
	return []globalconfig.Entry{r.row}, nil
}
func (r *configRepo) Get(context.Context, string) (globalconfig.Entry, error) { return r.row, nil }
func (r *configRepo) Put(_ context.Context, e globalconfig.Entry) error {
	r.row = e
	r.puts++
	return nil
}
func (r *configRepo) Delete(context.Context, string) error { return nil }
func TestGlobalConfigurationRequiresAuthenticationAndSupportsGlobalScope(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	repo := &configRepo{}
	handler := operationshttp.NewHandler(operations.NewService(memory.OperationsRepositories(store), nil), newRBACService(t, store), operationshttp.Config{GlobalConfigs: globalconfig.NewService(repo)})
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		path := "/admin/v1/global-configs"
		if method != http.MethodGet {
			path += "/test"
		}
		response := request(t, handler, method, path, "application/json", "", strings.NewReader(`{"value":{}}`))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated config request: %d", response.Code)
		}
	}
	login := request(t, handler, http.MethodPost, "/admin/v1/session", "application/json", "", strings.NewReader(`{"username":"admin","password":"local-admin-password"}`))
	var credentials struct {
		Token string `json:"token"`
	}
	decodeData(t, login, &credentials)
	response := request(t, handler, http.MethodPut, "/admin/v1/global-configs/test", "application/json", credentials.Token, strings.NewReader(`{"value":{"enabled":true},"remark":"功能开关"}`))
	if response.Code != http.StatusOK || repo.row.Key != "test" || repo.row.Remark != "功能开关" || repo.row.UpdatedBy != "admin" {
		t.Fatalf("global save failed: %d", response.Code)
	}
	response = request(t, handler, http.MethodGet, "/admin/v1/global-configs", "application/json", credentials.Token, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("global read failed: %d", response.Code)
	}
}
