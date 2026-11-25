package consumer_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/monetization"
	"drama-server/internal/biz/visitor"
	httpapi "drama-server/internal/service/consumer"
	"drama-server/internal/testsupport/memory"
)

// TestEntryAdHTTPContract 验证通用事实合同、展示幂等和 Handler 对外部参数的校验。
func TestEntryAdHTTPContract(t *testing.T) {
	now := time.Now().UTC()
	store := memory.NewStore(memory.Seed{
		PublishedOwnerID: memory.TestApplicationID,
		Applications: []application.Application{{TikTokAppID: "1", Status: application.StatusActive,
			AdUnits: application.AdUnits{RewardedVideo: []string{"entry-ad"}}}},
		Dramas: []domain.Drama{{ID: "5008", PublishStatus: "published", PlatformAlbumID: "album"}},
		ApplicationMonetization: []monetization.ApplicationMonetizationConfig{{
			ApplicationID: memory.TestApplicationID, Status: monetization.MonetizationEnabled,
			StrategyID: "entry-strategy", Document: &monetization.StrategyDocument{SchemaVersion: 1, Enabled: true,
				Rules: []monetization.UnlockRule{{Code: "entry", Type: "entry_rewarded_ad", Enabled: true,
					Config: json.RawMessage(`{"requiredAdCount":2,"scope":"user_drama","countBy":"shown"}`)}}},
			Bindings: map[string]monetization.RuleBinding{"entry": {AdUnitID: "entry-ad"}},
		}},
	})
	visitors := newTestVisitorService(t, store)
	identity, err := visitors.Bootstrap(t.Context(), visitor.BootstrapInput{TikTokAppID: "1"})
	if err != nil {
		t.Fatal(err)
	}
	handler := newConsumerHandler(domain.NewService(store, func() time.Time { return now }), visitors, httpapi.Config{})
	request := func(method, path, payload string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(payload))
		r.Header.Set("authorization", "Bearer "+identity.VisitorToken)
		r.Header.Set("content-type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	readStatus := func(path string) domain.EntryAdStatus {
		t.Helper()
		w := request(http.MethodGet, path, "")
		var result struct {
			Data domain.EntryAdStatus `json:"data"`
		}
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatalf("入剧状态读取失败：status=%d body=%s", w.Code, w.Body)
		}
		return result.Data
	}
	status := readStatus("/api/v1/me/entry-ad?dramaId=%205008%20")
	if status.Scope != "user_drama" || status.CountBy != "shown" || status.ShownCount != 0 || status.Offer == nil {
		t.Fatalf("现代合同未保留模式与零进度：%+v", status)
	}
	input := monetization.AdShowInput{EventID: "http-entry-show-0001", Scene: monetization.AdSceneEntryReward, DramaID: "5008", AdUnitID: status.Offer.AdUnitID, OccurredAt: now}
	payload, _ := json.Marshal(input)
	for range 2 {
		w := request(http.MethodPost, "/api/v1/me/ad-shows", string(payload))
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"accepted":true`) {
			t.Fatalf("展示事实确认失败：status=%d body=%s", w.Code, w.Body)
		}
	}
	status = readStatus("/api/v1/me/entry-ad?dramaId=5008")
	if status.ShownCount != 1 || status.CompletedCount != 0 {
		t.Fatalf("展示重试不能重复计数或产生完整观看奖励：%+v", status)
	}
	home := monetization.AdShowInput{EventID: "http-home-show-0001", Scene: "home_entry", AdUnitID: input.AdUnitID, OccurredAt: now}
	homeBody, _ := json.Marshal(home)
	if w := request(http.MethodPost, "/api/v1/me/ad-shows", string(homeBody)); w.Code != http.StatusOK {
		t.Fatalf("无剧目标场景不应依赖 Offer: %d %s", w.Code, w.Body)
	}
	w := request(http.MethodGet, "/api/v1/me/ad-shows?scene=home_entry", "")
	var homeResult struct {
		Data domain.AdShowStatus `json:"data"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &homeResult) != nil || homeResult.Data.ShownCount != 1 || homeResult.Data.ValidUntil == "" {
		t.Fatalf("通用场景查询错误: %d %s", w.Code, w.Body)
	}
	for _, path := range []string{"/api/v1/me/ad-shows", "/api/v1/me/ad-shows?scene=bad/scene", "/api/v1/me/ad-shows?scene=home_entry&episodeId=5014"} {
		if w := request(http.MethodGet, path, ""); w.Code != http.StatusBadRequest {
			t.Fatalf("非法通用查询未拒绝: %s %d", path, w.Code)
		}
	}
	for _, test := range []struct {
		name   string
		change func(*monetization.AdShowInput)
	}{
		{"空幂等键", func(in *monetization.AdShowInput) { in.EventID = "" }},
		{"非法幂等键", func(in *monetization.AdShowInput) { in.EventID = "bad/claim" }},
		{"未知场景", func(in *monetization.AdShowInput) { in.Scene = "bad/scene" }},
		{"空广告位", func(in *monetization.AdShowInput) { in.AdUnitID = "" }},
		{"非字符串数字剧目", func(in *monetization.AdShowInput) { in.DramaID = "bad" }},
		{"剧集缺少剧目", func(in *monetization.AdShowInput) { in.DramaID = ""; in.EpisodeID = "5014" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			invalid := input
			test.change(&invalid)
			body, _ := json.Marshal(invalid)
			w := request(http.MethodPost, "/api/v1/me/ad-shows", string(body))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("非法展示参数未在入口拒绝：status=%d body=%s", w.Code, w.Body)
			}
		})
	}
	if w := request(http.MethodGet, "/api/v1/me/entry-ad", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("来源剧目必须提供: %d", w.Code)
	}
	if w := request(http.MethodGet, "/api/v1/me/entry-ad?dramaId=bad", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("非法剧目查询应在 Handler 拒绝：status=%d", w.Code)
	}
}
