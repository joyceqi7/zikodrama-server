package mysqlstore

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/homepage"
)

// TestHomeRankingPagesHTTPIntegration 使用隔离 MySQL 验证官方/直连的轻量分类、排序、隐藏、授权与公开分页合同。
func TestHomeRankingPagesHTTPIntegration(t *testing.T) {
	f := newIAAFixture(t)
	seedCategoryRankingDramas(t, f)
	_, directCtx := seedDirect(t, f)
	ctx := application.WithSubject(context.Background(), "1")
	db := f.store.data.DB()
	for _, id := range []string{"202", "203", "204", "205"} {
		drama, err := f.store.FindDrama(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(drama.Published)
		if err != nil {
			t.Fatal(err)
		}
		version := directPublicationVersionModel{SubjectID: "1", DramaID: id, VersionNumber: 1, SourceDigest: strings.Repeat("a", 64), SnapshotJSON: raw, CreatedAt: time.Now()}
		if err := db.Create(&version).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&directPublicationModel{SubjectID: "1", DramaID: id, Status: "published", CurrentVersionID: &version.ID}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"category_rankings", "direct_category_rankings"} {
		if err := db.Table(table).Where("subject_id=?", "1").Delete(&categoryRankingModel{}).Error; err != nil {
			t.Fatal(err)
		}
		for tagID, items := range map[string][]homepage.RankingItem{
			"21": {{DramaID: "203", Visible: true}, {DramaID: "205", Visible: true}, {DramaID: "202", Visible: true}, {DramaID: "204", Visible: true}, {DramaID: "999999", Visible: true}},
			"22": {{DramaID: "204", Visible: false}},
		} {
			raw, err := json.Marshal(items)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Table(table).Create(&categoryRankingModel{SubjectID: "1", TagID: tagID, ItemsJSON: raw}).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	home := homepage.NewService(f.store, f.store, f.store)
	server := httptest.NewServer(f.handler(t, home))
	defer server.Close()
	for _, tc := range []struct {
		app string
		ids []string
	}{{"101", []string{"203", "202"}}, {"102", []string{"203", "205", "202"}}} {
		t.Run(tc.app, func(t *testing.T) {
			raw := iaaRequest(t, server.URL, "GET", "/api/v1/open/home/ranking-categories?tiktokAppId="+tc.app, "", "", nil, 200)
			var categories []homepage.PublicRankingCategory
			if err := json.Unmarshal(raw, &categories); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(categories, []homepage.PublicRankingCategory{{TagID: "21", Name: "Mystery"}}) || strings.Contains(string(raw), "dramas") {
				t.Fatalf("目录不应加载摘要、空分类或未授权剧：%s", raw)
			}
			cursor := ""
			for index, id := range tc.ids {
				path := "/api/v1/open/home/rankings/21?tiktokAppId=" + tc.app + "&limit=1&cursor=" + url.QueryEscape(cursor)
				raw = iaaRequest(t, server.URL, "GET", path, "", "", nil, 200)
				var page homepage.RankingPage
				if err := json.Unmarshal(raw, &page); err != nil {
					t.Fatal(err)
				}
				if page.TagID != "21" || len(page.Dramas) != 1 || page.Dramas[0].ID != id {
					t.Fatalf("分类顺序或隔离错误：%s", raw)
				}
				cursor = page.NextCursor
				if (cursor == "") != (index == len(tc.ids)-1) {
					t.Fatalf("分页游标错误：%s", raw)
				}
				for _, field := range []string{"visitorToken", "vid", "playback", "episodes"} {
					if strings.Contains(string(raw), `"`+field+`"`) {
						t.Fatalf("公开页泄漏私有字段：%s", field)
					}
				}
			}
			for _, query := range []string{"limit=0", "limit=51", "limit=abc", "cursor=broken", "cursor=djE6LTE"} {
				iaaRequest(t, server.URL, "GET", "/api/v1/open/home/rankings/21?tiktokAppId="+tc.app+"&"+query, "", "", nil, 400)
			}
			iaaRequest(t, server.URL, "GET", "/api/v1/open/home/rankings/invalid?tiktokAppId="+tc.app, "", "", nil, 400)
			iaaRequest(t, server.URL, "GET", "/api/v1/open/home/rankings/999999?tiktokAppId="+tc.app, "", "", nil, 404)
			// 原接口仍保留完整数组合同，服务端可以先发布，旧 Mini 无需同步更新。
			var legacy []homepage.RankingSection
			if err := json.Unmarshal(iaaRequest(t, server.URL, "GET", "/api/v1/open/home/rankings?tiktokAppId="+tc.app, "", "", nil, 200), &legacy); err != nil || len(legacy) != 1 || len(legacy[0].Dramas) != len(tc.ids) {
				t.Fatalf("旧接口合同改变：%+v %v", legacy, err)
			}
		})
	}
	// 最小投影同样服从主体和应用门禁，不因列表只返回 ID 而扩大可见范围。
	for _, scope := range []context.Context{ctx, application.WithSubject(directCtx, "999999"), application.WithContext(directCtx, "999999")} {
		members, err := f.store.FindVisibleRankingMembers(scope, []string{"202", "205"})
		if err != nil || len(members) != 0 {
			t.Fatalf("最小投影跨越可见范围：%+v %v", members, err)
		}
	}
	// 分类在两次翻页之间被全部隐藏时，旧游标返回空数组并停止翻页。
	if _, err := home.SaveCategoryRanking(homepage.WithManagementContext(ctx), "21", homepage.Ranking{Items: []homepage.RankingItem{}}); err != nil {
		t.Fatal(err)
	}
	page, err := home.HomeRankingPage(application.WithContext(ctx, "101"), "21", 1, 20)
	if err != nil || page.Dramas == nil || len(page.Dramas) != 0 || page.NextCursor != "" {
		t.Fatalf("内容减少后的旧游标未结束：%+v %v", page, err)
	}
}
