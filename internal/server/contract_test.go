package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	configv1 "gitee.com/linpbin/styx/api/config/v1"

	application "drama-server/internal/biz"
	"drama-server/internal/biz/adminrbac"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"drama-server/internal/biz/visitor"
	httpapi "drama-server/internal/service/consumer"
	operationshttp "drama-server/internal/service/operations"
	"drama-server/internal/testsupport/memory"
)

type contractCase struct {
	Name   string `json:"name"`
	Method string `json:"method"`
	Path   string `json:"path"`
}

type contractApplicationResolver struct{}

func (contractApplicationResolver) Resolve(_ context.Context, code string) (visitor.Application, visitor.IdentityProvider, error) {
	if code != "1" {
		return visitor.Application{}, nil, visitor.ErrUnauthorized
	}
	return visitor.Application{SubjectID: "1", ID: "1", TikTokAppID: "1"}, nil, nil
}

func TestStyxMountPreservesRouterContract(t *testing.T) {
	t.Parallel()

	for _, testCase := range loadContractCases(t) {
		t.Run(testCase.Name, func(t *testing.T) {
			t.Parallel()
			deps := testContractDependencies(t)
			direct := executeDirect(t, testRouter(deps), testCase)
			_, address := newTestEngine(t, deps)
			styx := perform(t, address, testCase.Method, testCase.Path, nil)

			if direct.status != styx.status {
				t.Fatalf("直接路由与 Styx 挂载状态不一致：direct=%d Styx/Kratos=%d", direct.status, styx.status)
			}
			for _, header := range []string{"Content-Type", "Vary"} {
				if direct.header.Get(header) != styx.header.Get(header) {
					t.Fatalf("响应头 %s 不一致：direct=%q Styx/Kratos=%q", header, direct.header.Get(header), styx.header.Get(header))
				}
			}
			assertEquivalentBody(t, direct.body, styx.body, direct.header.Get("Content-Type"))
		})
	}
}

func TestBusinessHandlersLeaveCORSAtTheGatewayBoundary(t *testing.T) {
	t.Parallel()

	deps := testContractDependencies(t)
	for name, handler := range map[string]http.Handler{"consumer": deps.Consumer, "admin": deps.Admin} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodOptions, "/api/v1/health", nil)
			request.Header.Set("Origin", "https://untrusted.example")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			for _, header := range []string{"Access-Control-Allow-Origin", "Access-Control-Allow-Headers", "Access-Control-Allow-Methods"} {
				if value := response.Header().Get(header); value != "" {
					t.Fatalf("业务 Handler 不应设置 %s，实际为 %q", header, value)
				}
			}
		})
	}
}

func loadContractCases(t *testing.T) []contractCase {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join("testdata", "contract_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []contractCase
	if err := json.Unmarshal(contents, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("合同用例不能为空")
	}
	return cases
}

func executeDirect(t *testing.T, handler http.Handler, testCase contractCase) testResponse {
	t.Helper()
	request := httptest.NewRequest(testCase.Method, testCase.Path, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return testResponse{status: response.Code, header: response.Result().Header, body: response.Body.String()}
}

func assertEquivalentBody(t *testing.T, direct, styx, contentType string) {
	t.Helper()
	if direct == "" || styx == "" {
		if direct != styx {
			t.Fatalf("空响应不一致：direct=%q Styx/Kratos=%q", direct, styx)
		}
		return
	}
	if !bytes.Contains([]byte(contentType), []byte("application/json")) {
		if direct != styx {
			t.Fatalf("响应体不一致：direct=%q Styx/Kratos=%q", direct, styx)
		}
		return
	}
	var directJSON any
	var styxJSON any
	if err := json.Unmarshal([]byte(direct), &directJSON); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(styx), &styxJSON); err != nil {
		t.Fatal(err)
	}
	// 两次请求的响应生成时间不同；分别校验格式后只比较稳定合同字段。
	for _, body := range []any{directJSON, styxJSON} {
		if envelope, ok := body.(map[string]any); ok {
			if value, exists := envelope["serverTime"]; exists {
				stamp, ok := value.(string)
				if _, err := time.Parse(time.RFC3339Nano, stamp); !ok || err != nil {
					t.Fatalf("响应时间基准无效: %v", value)
				}
				delete(envelope, "serverTime")
			}
		}
	}
	if !reflect.DeepEqual(directJSON, styxJSON) {
		t.Fatalf("JSON 响应不一致：direct=%s Styx/Kratos=%s", direct, styx)
	}
}

func testContractDependencies(t *testing.T) Dependencies {
	t.Helper()
	store := memory.NewStore(memory.Seed{})
	visitorService, err := visitor.NewService(contractApplicationResolver{}, store, visitor.Config{
		IdentityTokenEncryptionSecret: "local-contract-visitor-identity-protection-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	consumerService := domain.NewService(store, time.Now)
	return Dependencies{
		Consumer: httpapi.NewHandler(
			consumerService,
			visitorService,
			application.NewConsumerBootstrap(visitorService, consumerService),
			httpapi.Config{},
		),
		Admin:  operationshttp.NewHandler(operations.NewService(memory.OperationsRepositories(store), nil), adminrbac.NewService(store, adminrbac.Config{SigningKey: []byte("test-only-admin-jwt-signing-key-32-bytes")}), operationshttp.Config{}),
		Styx:   newHTTPTestRuntime(t),
		Config: &configv1.HTTPServerConfig{Addr: ":0"},
	}
}
