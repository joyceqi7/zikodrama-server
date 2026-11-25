package mysqlstore

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/homepage"
)

// TestCatalogSnapshotsIntegration 验证两个渠道均在 SQL 中先筛选分页，再从选中快照提取列表字段。
func TestCatalogSnapshotsIntegration(t *testing.T) {
	f := newIAAFixture(t)
	_, directContext := seedDirect(t, f)
	ctx := application.WithSubject(context.Background(), "1")
	db := f.store.data.DB()
	createdAt := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	for index, id := range []string{"201", "202", "203"} {
		snapshot := consumer.Drama{ID: id, Title: "线上目录 " + id, Description: "包含中文与 needle", CoverURL: "/cover-" + id, Status: "updating", TagIDs: []string{"21"}, PublishStatus: "published", OwnerApplicationID: "101", AuthorizedApplicationIDs: []string{"102"}, Episodes: []consumer.Episode{{ID: "301", VID: "fixture-vid-1", PosterURL: "/frozen-poster"}}}
		if index == 1 {
			snapshot.Status, snapshot.TagIDs = "completed", []string{"23"}
			snapshot.Episodes = []consumer.Episode{{ID: "301", VID: "changed-vid", PosterURL: "/other-frozen"}, {ID: "9007199254740993"}}
		}
		if index == 2 {
			snapshot.Status, snapshot.Episodes = "unpublished", []consumer.Episode{}
		}
		draft := snapshot
		draft.Title, draft.Episodes, draft.Published = "编辑草稿 "+id, nil, &snapshot
		if err := f.store.SaveDrama(ctx, draft); err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&dramaModel{}).Where("id = ?", id).Update("created_at", createdAt).Error; err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		// 普通编辑不会覆盖已有官方快照，测试显式准备同一套线上内容用于渠道对照。
		if err := seedOfficialProjection(db, id, raw); err != nil {
			t.Fatal(err)
		}
		version := directPublicationVersionModel{SubjectID: "1", DramaID: id, VersionNumber: 1, SourceDigest: strings.Repeat("a", 64), SnapshotJSON: raw, CreatedAt: createdAt}
		if err := db.Create(&version).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&directPublicationModel{SubjectID: "1", DramaID: id, Status: "published", CurrentVersionID: &version.ID}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Model(&episodeModel{}).Where("id = ?", "301").Update("poster_url", "/derived-poster").Error; err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		query consumer.DiscoveryQuery
		ids   []string
		next  string
	}{
		{name: "第一页及同时间按标识排序", query: consumer.DiscoveryQuery{Limit: 2}, ids: []string{"201", "202"}, next: consumer.EncodeDiscoveryCursor(2)},
		{name: "末页", query: consumer.DiscoveryQuery{Limit: 2, Offset: 2}, ids: []string{"203"}},
		{name: "游标恰好到末尾", query: consumer.DiscoveryQuery{Limit: 2, Offset: 3}, ids: []string{}},
		{name: "标签筛选后分页", query: consumer.DiscoveryQuery{Limit: 1, Offset: 1, TagID: "21"}, ids: []string{"203"}},
		{name: "标签展示名", query: consumer.DiscoveryQuery{Limit: 20, Tag: "悬疑推理"}, ids: []string{"201", "203"}},
		{name: "状态归一化", query: consumer.DiscoveryQuery{Limit: 20, Status: "ongoing"}, ids: []string{"201", "203"}},
		{name: "完结筛选", query: consumer.DiscoveryQuery{Limit: 1, Status: "completed"}, ids: []string{"202"}},
		{name: "搜索后分页", query: consumer.DiscoveryQuery{Limit: 1, Query: "202 NEEDLE"}, ids: []string{"202"}},
		{name: "标签搜索", query: consumer.DiscoveryQuery{Limit: 20, Query: "悬疑"}, ids: []string{"201", "203"}},
		{name: "草稿不参与搜索", query: consumer.DiscoveryQuery{Limit: 20, Query: "编辑草稿"}, ids: []string{}},
	}
	for _, channel := range []struct {
		name string
		ctx  context.Context
	}{{"直连", directContext}, {"官方", application.WithContext(ctx, "102")}} {
		t.Run(channel.name, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					page, err := f.store.DiscoverDramas(channel.ctx, tc.query)
					if err != nil {
						t.Fatal(err)
					}
					ids := make([]string, 0, len(page.Items))
					for _, item := range page.Items {
						ids = append(ids, item.ID)
						if item.Title != "线上目录 "+item.ID || item.FirstEpisodePosterURL != "" {
							t.Fatalf("草稿或海报改变了目录摘要：%+v", item)
						}
						if item.ID == "203" && (item.EpisodeCount != 0 || item.FirstEpisodeID != "") {
							t.Fatalf("空分集摘要改变：%+v", item)
						}
						if item.ID == "202" && item.EpisodeCount != 2 {
							t.Fatalf("快照分集数改变：%+v", item)
						}
					}
					if !reflect.DeepEqual(ids, tc.ids) || page.NextCursor != tc.next || len(page.Tags) != 2 {
						t.Fatalf("分页、筛选或全目录标签改变：%+v", page)
					}
				})
			}
			if _, err := f.store.DiscoverDramas(channel.ctx, consumer.DiscoveryQuery{Limit: 2, Offset: 4}); !errors.Is(err, consumer.ErrInvalidDiscovery) {
				t.Fatalf("越界游标未拒绝：%v", err)
			}
			items, err := f.store.FindVisibleSummaries(channel.ctx, []string{"201", "202", "203", "999999"})
			if err != nil || len(items) != 3 {
				t.Fatalf("首页摘要读取改变：%+v %v", items, err)
			}
			byID := map[string]consumer.DramaSummary{}
			for _, item := range items {
				byID[item.ID] = item
			}
			if byID["201"].FirstEpisodePosterURL != "/derived-poster" || byID["202"].FirstEpisodePosterURL != "/other-frozen" || byID["203"].FirstEpisodePosterURL != "" {
				t.Fatalf("海报未按线上分集标识与 VID 匹配：%+v", items)
			}
		})
	}
	for _, appID := range []string{"", "101", "999999"} {
		page, err := f.store.DiscoverDramas(application.WithPlayback(application.WithContext(ctx, appID), "byteplus_web"), consumer.DiscoveryQuery{Limit: 20})
		if err != nil || len(page.Items) != 0 || len(page.Tags) != 0 {
			t.Fatalf("直连应用门禁失效：application_id=%s %v", appID, err)
		}
	}
	if err := db.Model(&applicationModel{}).Where("id = ?", "102").Update("status", "inactive").Error; err != nil {
		t.Fatal(err)
	}
	page, err := f.store.DiscoverDramas(directContext, consumer.DiscoveryQuery{Limit: 20})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("停用应用仍可读取目录：%v", err)
	}
	management := application.WithPlayback(homepage.WithManagementContext(ctx), "byteplus_web")
	page, err = f.store.DiscoverDramas(management, consumer.DiscoveryQuery{Limit: 20})
	if err != nil || len(page.Items) != 3 {
		t.Fatalf("管理目录错误依赖应用身份：%v", err)
	}
}
