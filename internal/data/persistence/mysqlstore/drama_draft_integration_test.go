package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
)

// TestCurrentSchemaIntegrationEmptyDramaDraftHTTP 通过真实 HTTP 与 MySQL 验证先存资料、稍后补集和显式清空分集。
func TestCurrentSchemaIntegrationEmptyDramaDraftHTTP(t *testing.T) {
	f := newIAAFixture(t)
	server := httptest.NewServer(f.handler(t))
	defer server.Close()
	var session struct {
		// Token 仅用于本次隔离测试的后台认证，不输出到日志。
		Token string `json:"token"`
	}
	if err := json.Unmarshal(iaaRequest(t, server.URL, http.MethodPost, "/admin/v1/session", "", "",
		map[string]string{"username": "iaa-admin", "password": "LocalIAA123!"}, http.StatusOK), &session); err != nil {
		t.Fatal(err)
	}
	request := func(method, path string, input any, status int) json.RawMessage {
		t.Helper()
		return iaaRequest(t, server.URL, method, path, session.Token, "", input, status)
	}
	// 空分集草稿仍要求基本资料完整；简介是当前 HTTP 合同的必填字段。
	input := map[string]any{"title": "先保存基本资料", "description": "空分集草稿联调", "episodes": []any{}}
	var created domain.Drama
	if err := json.Unmarshal(request(http.MethodPost, "/admin/v1/dramas", input, http.StatusCreated), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" {
		t.Fatal("创建草稿未返回标识")
	}
	path := "/admin/v1/dramas/" + created.ID
	read := func(title string, episodes int) domain.Drama {
		t.Helper()
		var drama domain.Drama
		if err := json.Unmarshal(request(http.MethodGet, path, nil, http.StatusOK), &drama); err != nil {
			t.Fatal(err)
		}
		if drama.Title != title || drama.PublishStatus != "draft" || len(drama.Episodes) != episodes {
			t.Fatal("数据库读回的草稿标题、状态或分集数不符合预期")
		}
		return drama
	}
	read("先保存基本资料", 0)
	input["title"] = "修改基本资料"
	request(http.MethodPut, path, input, http.StatusOK)
	read("修改基本资料", 0)
	input["episodes"] = []map[string]any{{"number": 1, "title": "第一集"}}
	request(http.MethodPut, path, input, http.StatusOK)
	added := read("修改基本资料", 1)
	if added.Episodes[0].ID == "" || added.Episodes[0].MediaStatus != "pending" {
		t.Fatal("补充的分集应具有标识并等待上传")
	}
	// 更新时仍须显式提交数组，避免缺失字段把已有分集误删。
	for _, invalid := range []map[string]any{
		{"title": "遗漏分集"},
		{"title": "空值分集", "episodes": nil},
		{"title": "非法集序", "episodes": []map[string]any{{"number": 2, "title": "第二集"}}},
	} {
		request(http.MethodPut, path, invalid, http.StatusBadRequest)
		read("修改基本资料", 1)
	}
	input["episodes"] = []any{}
	request(http.MethodPut, path, input, http.StatusOK)
	read("修改基本资料", 0)
	if f.count(t, "episodes", "drama_id = ?", created.ID) != 0 {
		t.Fatal("显式清空后仍残留未发布分集")
	}
}

// TestCurrentSchemaIntegrationEmptyDramaDraftMedia 验证空草稿封面保存，以及清空分集时的原子清理和线上引用保护。
func TestCurrentSchemaIntegrationEmptyDramaDraftMedia(t *testing.T) {
	store := newForeignKeyTestStore(t)
	ctx := application.WithSubject(context.Background(), "1")
	db := store.data.DB().WithContext(ctx)
	t.Run("无分集仍可绑定和保留封面", func(t *testing.T) {
		now := time.Now().UTC()
		cover := operations.MediaJob{ID: "1009", IdempotencyKey: "1009", Kind: operations.MediaJobCover,
			ResourceClass: operations.MediaResourceMaterial, SpaceName: "test-space", RemoteVID: "test-cover-mid",
			ProviderFileName: "cover.png", PublicURL: "https://cover.example.test/draft.png", Status: operations.MediaJobUploaded,
			CreatedAt: now, UpdatedAt: now, NextAttemptAt: now}
		if err := store.CreateMediaJob(ctx, &cover); err != nil {
			t.Fatal(err)
		}
		drama := domain.Drama{ID: "1008", Title: "只有封面的草稿", PublishStatus: "draft", CoverURL: cover.PublicURL}
		if err := store.SaveDramaAggregate(ctx, &drama, &cover); err != nil {
			t.Fatalf("空分集草稿绑定封面失败：%v", err)
		}
		drama.Title = "修改封面草稿资料"
		if err := store.SaveDramaAggregate(ctx, &drama, nil); err != nil {
			t.Fatal(err)
		}
		saved, err := store.FindDrama(ctx, drama.ID)
		if err != nil || saved.CoverMID != cover.RemoteVID || saved.CoverSpaceName != cover.SpaceName || saved.CoverFileName != cover.ProviderFileName {
			t.Fatalf("封面媒资信息未保留：%v", err)
		}
		job, err := store.FindMediaJob(ctx, cover.ID)
		if err != nil || job.Status != operations.MediaJobSucceeded || job.DramaID != drama.ID {
			t.Fatalf("封面上传任务未绑定草稿：%v", err)
		}
	})
	for _, published := range []bool{false, true} {
		name := "清空草稿分集并原子安排清理"
		if published {
			name = "清空编辑区仍保护线上分集"
		}
		t.Run(name, func(t *testing.T) {
			drama := domain.Drama{Title: "清空前", PublishStatus: "draft"}
			episode := domain.Episode{DramaID: drama.ID, Number: 1, Title: "第一集", MediaStatus: "ready", VID: name, MediaSpaceName: "test-space"}
			drama.Episodes = []domain.Episode{episode}
			if err := store.SaveDramaAggregate(ctx, &drama, nil); err != nil {
				t.Fatal(err)
			}
			episode = drama.Episodes[0]
			grant := rewardGrantModel{BucketID: 0, ClaimID: name, ApplicationID: "1033", VisitorID: "1036", DramaID: &drama.ID, EpisodeID: &episode.ID, GrantedAt: time.Now().UTC()}
			if err := db.Create(&grant).Error; err != nil {
				t.Fatal(err)
			}
			if published {
				// 仅在隔离库准备已上线快照；普通编辑不得改变该快照或删除其引用。
				snapshot, err := json.Marshal(drama)
				if err != nil {
					t.Fatal(err)
				}
				if err := seedOfficialProjection(db, drama.ID, snapshot); err != nil {
					t.Fatal(err)
				}
				drama.PublishStatus = "published"
			}
			drama.Title, drama.Episodes = "清空后", []domain.Episode{}
			if !published {
				// 清理任务写入失败必须回滚资料、分集与奖励引用，不能出现部分保存。
				if err := db.Exec("CREATE TRIGGER reject_empty_draft_cleanup BEFORE INSERT ON media_jobs FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'empty draft cleanup failure'").Error; err != nil {
					t.Fatal(err)
				}
				err := store.SaveDramaAggregate(ctx, &drama, nil)
				if dropErr := db.Exec("DROP TRIGGER reject_empty_draft_cleanup").Error; dropErr != nil {
					t.Fatal(dropErr)
				}
				if err == nil || !strings.Contains(err.Error(), "empty draft cleanup failure") {
					t.Fatalf("清理故障未原样传播：%v", err)
				}
				saved, err := store.FindDrama(ctx, drama.ID)
				if err != nil || saved.Title != "清空前" || len(saved.Episodes) != 1 || saved.Episodes[0].ID != episode.ID {
					t.Fatalf("清理失败未回滚短剧与分集：%v", err)
				}
				var savedGrant rewardGrantModel
				if err := db.First(&savedGrant, "claim_id = ?", grant.ClaimID).Error; err != nil || savedGrant.EpisodeID == nil || *savedGrant.EpisodeID != episode.ID {
					t.Fatalf("清理失败未回滚奖励引用：%v", err)
				}
			}
			if err := store.SaveDramaAggregate(ctx, &drama, nil); err != nil {
				t.Fatalf("清空分集失败：%v", err)
			}
			saved, err := store.FindDrama(ctx, drama.ID)
			if err != nil || saved.Title != "清空后" || len(saved.Episodes) != 0 {
				t.Fatalf("空编辑区未保存：%v", err)
			}
			var savedGrant rewardGrantModel
			if err := db.First(&savedGrant, "claim_id = ?", grant.ClaimID).Error; err != nil {
				t.Fatal(err)
			}
			if published {
				var historical episodeModel
				if err := db.First(&historical, "id = ?", episode.ID).Error; err != nil || historical.IsDraft {
					t.Fatalf("线上分集实体未保留为历史行：%v", err)
				}
				if saved.Published == nil || len(saved.Published.Episodes) != 1 || saved.Published.Episodes[0].ID != episode.ID || savedGrant.EpisodeID == nil || *savedGrant.EpisodeID != episode.ID {
					t.Fatal("清空编辑区破坏了线上快照或奖励引用")
				}
			} else {
				var count int64
				if err := db.Model(&episodeModel{}).Where("id = ?", episode.ID).Count(&count).Error; err != nil || count != 0 || savedGrant.EpisodeID != nil {
					t.Fatalf("未发布分集或失效奖励引用未清除：%v", err)
				}
			}
			var cleanup mediaJobModel
			if err := db.Where("kind = ? AND remote_vid = ?", operations.MediaJobDelete, episode.VID).First(&cleanup).Error; err != nil {
				t.Fatalf("没有安排移出编辑区的媒资清理：%v", err)
			}
			used, err := store.IsMediaResourceUsed(ctx, operations.MediaJob{ResourceClass: operations.MediaResourceVideo, SpaceName: episode.MediaSpaceName, RemoteVID: episode.VID})
			if err != nil || used != published {
				t.Fatalf("媒资引用保护错误：used=%v published=%v err=%v", used, published, err)
			}
		})
	}
}
