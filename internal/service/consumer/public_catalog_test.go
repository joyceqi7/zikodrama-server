package consumer_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/visitor"
	httpapi "drama-server/internal/service/consumer"
	"drama-server/internal/testsupport/memory"
)

// catalogResolver 让两个应用共用目录数据，检验公开读取仍遵守发布归属。
type catalogResolver struct{}

func (catalogResolver) Resolve(_ context.Context, code string) (visitor.Application, visitor.IdentityProvider, error) {
	switch code {
	case "1":
		return visitor.Application{SubjectID: "1", ID: code, TikTokAppID: code}, nil, nil
	case "other-app":
		return visitor.Application{SubjectID: "1", ID: code, TikTokAppID: code}, nil, nil
	default:
		return visitor.Application{}, nil, visitor.ErrUnauthorized
	}
}

func TestPublicCatalogWithoutLoginKeepsApplicationAndCredentialBoundaries(t *testing.T) {
	store := memory.NewStore(memory.Seed{PublishedOwnerID: memory.TestApplicationID, Dramas: []domain.Drama{
		{ID: "5019", Title: "公开短剧", PublishStatus: "published", Episodes: []domain.Episode{{ID: "5013", Number: 1}}},
		{ID: "5007", Title: "未发布短剧", PublishStatus: "draft"},
	}})
	visitors, err := visitor.NewService(catalogResolver{}, store, visitor.Config{
		RequireTikTokLogin: true, IdentityTokenEncryptionSecret: "local-public-catalog-test-secret-only",
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := newConsumerHandler(domain.NewService(store, time.Now), visitors, httpapi.Config{})
	for _, tc := range []struct {
		query         string
		status, count int
	}{
		{"1", 200, 1}, {"other-app", 200, 0}, {"missing", 401, 0}, {"", 400, 0}, {"invalid!", 400, 0},
	} {
		t.Run(tc.query, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/open/catalog?tiktokAppId="+tc.query, nil))
			if response.Code != tc.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if tc.status != 200 {
				return
			}
			var body struct {
				Data struct {
					TikTokAppID string                `json:"tiktokAppId"`
					Dramas      []domain.DramaSummary `json:"dramas"`
				} `json:"data"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Data.TikTokAppID != tc.query || len(body.Data.Dramas) != tc.count {
				t.Fatalf("unexpected catalog: %s", response.Body.String())
			}
			for _, field := range []string{"visitorToken", "tiktokOpenId", "vipState", "adUnitId", "vid", "playback"} {
				if strings.Contains(response.Body.String(), `"`+field+`"`) {
					t.Fatalf("private field %s in catalog", field)
				}
			}
		})
	}
	for _, path := range []string{"/api/v1/play/resolve", "/api/v1/play/resolve-batch", "/api/v1/reward-grants"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`)))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s status=%d", path, response.Code)
		}
	}
	for _, path := range []string{"/api/v1/catalog", "/api/v1/home/carousel", "/api/v1/home/hot", "/api/v1/home/rankings"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path+"?tiktokAppId=1", nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("旧公开地址必须停止兼容: path=%s status=%d", path, response.Code)
		}
	}
}

type failingCatalogStore struct{ *memory.Store }

func (s failingCatalogStore) ListDramas(context.Context) ([]domain.Drama, error) {
	return nil, errors.New("catalog database unavailable")
}

func TestPublicCatalogFailureLogsActualCause(t *testing.T) {
	var logs bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(original) })
	store := memory.NewStore(memory.Seed{})
	handler := newConsumerHandler(domain.NewService(failingCatalogStore{store}, time.Now), newTestVisitorService(t, store), httpapi.Config{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/open/catalog?tiktokAppId=1", nil))
	if response.Code < 500 {
		t.Fatalf("unexpected status=%d", response.Code)
	}
	if !strings.Contains(logs.String(), "list public catalog summaries failed") || !strings.Contains(logs.String(), "catalog database unavailable") {
		t.Fatalf("missing diagnostic: %s", logs.String())
	}
	if strings.Contains(response.Body.String(), "catalog database unavailable") {
		t.Fatal("internal error exposed")
	}
}

// DiscoverDramas 模拟分页目录读取失败。
func (s failingCatalogStore) DiscoverDramas(context.Context, domain.DiscoveryQuery) (domain.DiscoveryPage, error) {
	return domain.DiscoveryPage{}, errors.New("catalog database unavailable")
}

// TestPublicCatalogFiltersAndTags 验证公开首页的筛选与分页使用真实 Handler 和目录业务层。
func TestPublicCatalogFiltersAndTags(t *testing.T) {
	store := memory.NewStore(memory.Seed{PublishedOwnerID: memory.TestApplicationID, Dramas: []domain.Drama{
		{ID: "5011", Title: "Mystery One", TagIDs: []string{"21"}, Status: "completed", PublishStatus: "published", Episodes: []domain.Episode{{ID: "6011", Number: 1}}},
		{ID: "5012", Title: "Mystery Two", TagIDs: []string{"21"}, Status: "completed", PublishStatus: "published", Episodes: []domain.Episode{{ID: "6012", Number: 1}}},
		{ID: "5013", Title: "Romance", TagIDs: []string{"23"}, Status: "ongoing", PublishStatus: "published", Episodes: []domain.Episode{{ID: "6013", Number: 1}}},
		{ID: "5014", Title: "Hidden draft", TagIDs: []string{"24"}, PublishStatus: "draft"},
	}})
	handler := newConsumerHandler(domain.NewService(store, time.Now), newTestVisitorService(t, store), httpapi.Config{})
	query := url.Values{"tiktokAppId": {"1"}, "query": {"Mystery"}, "tag": {"悬疑推理"}, "status": {"completed"}, "limit": {"1"}}
	read := func() (ids []string, tags []string, cursor string) {
		t.Helper()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/open/catalog?"+query.Encode(), nil))
		if response.Code != http.StatusOK {
			t.Fatalf("公开目录失败: status=%d body=%s", response.Code, response.Body.String())
		}
		var body struct {
			Data struct {
				Dramas     []domain.DramaSummary `json:"dramas"`
				Tags       []string              `json:"tags"`
				NextCursor string                `json:"nextCursor"`
			} `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		for _, drama := range body.Data.Dramas {
			ids = append(ids, drama.ID)
		}
		return ids, body.Data.Tags, body.Data.NextCursor
	}
	ids, tags, cursor := read()
	if len(ids) != 1 || ids[0] != "5011" || cursor == "" || strings.Join(tags, ",") != "悬疑推理,甜宠" {
		t.Fatalf("首屏筛选与完整分类不符: ids=%v tags=%v cursor=%s", ids, tags, cursor)
	}
	query.Set("cursor", cursor)
	ids, tags, cursor = read()
	if len(ids) != 1 || ids[0] != "5012" || cursor != "" || len(tags) != 2 {
		t.Fatalf("下一页丢失筛选或分类: ids=%v tags=%v cursor=%s", ids, tags, cursor)
	}
	query.Del("cursor")
	query.Set("query", "no-match")
	ids, tags, _ = read()
	if len(ids) != 0 || len(tags) != 2 {
		t.Fatalf("空搜索仍应保留完整分类: ids=%v tags=%v", ids, tags)
	}
}
