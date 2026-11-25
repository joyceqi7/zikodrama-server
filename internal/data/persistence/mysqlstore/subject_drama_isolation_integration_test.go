package mysqlstore

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/directplay"
	"drama-server/internal/biz/operations"
	"drama-server/internal/biz/subject"
)

// TestSubjectDramaOwnershipIntegration 覆盖草稿、分集、协作、删除、直连发布和上传任务的直接主键越权。
func TestSubjectDramaOwnershipIntegration(t *testing.T) {
	f := newIAAFixture(t)
	a := application.WithSubject(t.Context(), "1")
	other, err := subject.NewService(f.store).Save(t.Context(), "", subject.Input{Name: "内容独立主体"})
	if err != nil {
		t.Fatal(err)
	}
	b := application.WithSubject(t.Context(), other.ID)
	draft := consumer.Drama{Title: "主体 B 独立草稿", Description: "简介", Status: "updating", Language: "en", TagIDs: []string{}, Episodes: []consumer.Episode{}}
	if err := f.store.SaveDramaAggregate(b, &draft, nil); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		ctx context.Context
		id  string
	}{{a, draft.ID}, {b, "201"}} {
		if _, err := f.store.FindDrama(tc.ctx, tc.id); !errors.Is(err, consumer.ErrNotFound) {
			t.Fatal("跨主体详情可读", err)
		}
		if err := f.store.SaveDrama(tc.ctx, consumer.Drama{ID: tc.id, Title: "越权写入"}); !errors.Is(err, consumer.ErrNotFound) {
			t.Fatal("跨主体草稿可写", err)
		}
		if err := f.store.SaveDramaOperations(tc.ctx, tc.id, "越权负责人", "", nil); !errors.Is(err, consumer.ErrNotFound) {
			t.Fatal("跨主体协作可写", err)
		}
		if _, err := NewDirectRepository(f.store).Accept(tc.ctx, tc.id, directplay.Command{Kind: "publish", RequestID: "cross-subject"}, "test"); !errors.Is(err, consumer.ErrNotFound) {
			t.Fatal("跨主体可发布", err)
		}
		if err := f.store.DeleteDrama(tc.ctx, tc.id); err == nil {
			t.Fatal("跨主体可删除")
		}
	}
	if _, err := f.store.FindEpisode(b, "301"); !errors.Is(err, consumer.ErrNotFound) {
		t.Fatal("跨主体分集可读", err)
	}
	if err := f.store.SaveEpisode(b, consumer.Episode{ID: "301", DramaID: draft.ID, Number: 1, Title: "越权搬移"}); !errors.Is(err, consumer.ErrNotFound) {
		t.Fatal("分集可以搬入其他主体", err)
	}
	for _, tc := range []struct {
		ctx   context.Context
		count int
	}{{a, 1}, {b, 1}, {t.Context(), 0}} {
		page, err := f.store.ListDramaPage(tc.ctx, operations.DramaListQuery{Page: 1, PageSize: 20})
		if err != nil || len(page.Items) != tc.count {
			t.Fatal("主体列表隔离失败", err, len(page.Items))
		}
	}
	cover := operations.MediaJob{Kind: operations.MediaJobCover, Status: operations.MediaJobUploaded, ResourceClass: operations.MediaResourceMaterial, SpaceName: "shared-space", RemoteVID: "cover-a", ProviderFileName: "cover-a.png", PublicURL: "https://fixture/cover-a.png", FileName: "cover-a.png", Size: 100, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), NextAttemptAt: time.Now().UTC()}
	if err := f.store.CreateMediaJob(a, &cover); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.FindMediaJob(b, cover.ID); !errors.Is(err, consumer.ErrNotFound) {
		t.Fatal("跨主体上传现场可读", err)
	}
	draft.CoverURL = cover.PublicURL
	if err := f.store.SaveDramaAggregate(b, &draft, &cover); !errors.Is(err, operations.ErrConflict) {
		t.Fatal("可以绑定其他主体封面", err)
	}
	saved, err := f.store.FindDrama(b, draft.ID)
	if err != nil || saved.CoverURL != "" {
		t.Fatal("封面拒绝后事务未回滚", err)
	}
	handler := f.handler(t)
	login := subjectAdminHTTP(t, handler, "POST", "/admin/v1/session", "", "", map[string]string{"username": "iaa-admin", "password": "LocalIAA123!"}, 200)
	var admin adminrbac.LoginResult
	if err := json.Unmarshal(login, &admin); err != nil {
		t.Fatal(err)
	}
	subjectAdminHTTP(t, handler, "GET", "/admin/v1/dramas/201", admin.Token, other.ID, nil, 404)
	subjectAdminHTTP(t, handler, "GET", "/admin/v1/media-jobs/"+cover.ID, admin.Token, other.ID, nil, 404)
	subjectAdminHTTP(t, handler, "GET", "/admin/v1/dramas", admin.Token, "", nil, 400)
	ep, err := f.store.FindEpisode(a, "301")
	if err != nil || ep.Title == "越权搬移" {
		t.Fatal("源分集被越权修改", err)
	}
}

// seedPreSubjectDrama 只在升级回归中按旧表列准备数据，不能让新版仓储假装兼容未迁移结构。
func seedPreSubjectDrama(t *testing.T, f *iaaFixture, draft consumer.Drama) {
	t.Helper()
	model, err := newDramaModel(draft)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.data.DB().Omit("SubjectID").Create(&model).Error; err != nil {
		t.Fatal(err)
	}
	for _, ep := range draft.Episodes {
		record := newEpisodeModel(ep)
		if err := f.store.data.DB().Create(&record).Error; err != nil {
			t.Fatal(err)
		}
	}
}
