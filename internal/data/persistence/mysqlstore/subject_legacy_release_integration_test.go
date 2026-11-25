package mysqlstore

import (
	"context"
	"encoding/json"
	"testing"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"drama-server/internal/biz/subject"
)

// TestSubjectLegacyReleaseIsolationIntegration 覆盖实际列表的历史回退，不允许缺少独立上架记录时借用另一主体版本。
func TestSubjectLegacyReleaseIsolationIntegration(t *testing.T) {
	f := newIAAFixture(t)
	seedDirect(t, f)
	other, err := subject.NewService(f.store).Save(t.Context(), "", subject.Input{Name: "历史版本隔离主体"})
	if err != nil {
		t.Fatal(err)
	}
	a := application.WithSubject(t.Context(), "1")
	b := application.WithSubject(t.Context(), other.ID)
	bDraft := consumer.Drama{Title: "主体 B 草稿", TagIDs: []string{}}
	if err := f.store.SaveDramaAggregate(b, &bDraft, nil); err != nil {
		t.Fatal(err)
	}

	now := f.store.data.DB().NowFunc()
	first := tiktokPublicationModel{ID: "9901", SubjectID: "1", DramaID: "201", OwnerApplicationID: "subject-1", Priority: "normal", Status: "online", Stage: "done", PlatformVersion: 3, WorkflowJSON: []byte(`{}`), EpisodeIDsJSON: []byte(`{}`), OnlineAt: &now, NextAttemptAt: now, LeaseExpiresAt: now, CreatedAt: now, UpdatedAt: now}
	if err := f.store.data.DB().Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	read := func(ctx context.Context) *operations.TikTokReleaseState {
		t.Helper()
		page, err := f.store.ListDramaPage(ctx, operations.DramaListQuery{Page: 1, PageSize: 20})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			want := "201"
			if application.SubjectIDFromContext(ctx) == other.ID {
				want = bDraft.ID
			}
			if item.ID == want {
				return item.OfficialRelease
			}
		}
		if application.SubjectIDFromContext(ctx) != "" {
			t.Fatal("本主体草稿未出现在列表")
		}
		return nil
	}
	t.Run("新主体没有上架历史时保持空选择", func(t *testing.T) {
		if state := read(b); state != nil {
			t.Fatalf("继承了其他主体的上架版本：%+v", state)
		}
	})
	second := first
	second.ID, second.SubjectID, second.PlatformVersion = "9902", other.ID, 7
	second.DramaID = bDraft.ID
	second.UpdatedAt = now.AddDate(0, 0, 1)
	if err := f.store.data.DB().Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	t.Run("两个主体都缺少独立记录时只回退本主体", func(t *testing.T) {
		for _, tc := range []struct {
			ctx     context.Context
			id      string
			version int
		}{{a, first.ID, 3}, {b, second.ID, 7}} {
			if state := read(tc.ctx); state == nil || state.SelectedPublicationID != tc.id || state.Version != tc.version {
				t.Errorf("历史任务串主体：%+v", state)
			}
		}
	})
	t.Run("缺少主体上下文也不回退其他主体", func(t *testing.T) {
		if state := read(t.Context()); state != nil {
			t.Fatalf("缺少主体仍读到了上架版本：%+v", state)
		}
	})
	third := first
	third.ID, third.PlatformVersion, third.UpdatedAt = "9903", 4, now.AddDate(0, 0, 2)
	if err := f.store.data.DB().Create(&third).Error; err != nil {
		t.Fatal(err)
	}
	t.Run("同主体仍选最近上线历史且不跨主体竞争", func(t *testing.T) {
		if state := read(a); state == nil || state.SelectedPublicationID != third.ID || state.Version != 4 {
			t.Fatalf("本主体历史回退改变：%+v", state)
		}
		if state := read(b); state == nil || state.SelectedPublicationID != second.ID {
			t.Fatalf("其他主体较新历史覆盖了本主体：%+v", state)
		}
	})
	// 权威上架记录存在时，仍优先保留明确选择，不能改为本主体或其他主体较新的历史任务。
	raw, err := json.Marshal(operations.TikTokPublication{SubjectID: "1", ID: first.ID, DramaID: "201", PlatformVersion: 3})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.data.DB().Table("tiktok_release_records").Create(&publicationRecordModel{SubjectID: "1", ID: first.ID, DramaID: "201", Title: "历史明确选择", Status: "offline", RecordJSON: raw, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	t.Run("已有明确选择不被历史回退覆盖", func(t *testing.T) {
		if state := read(a); state == nil || state.SelectedPublicationID != first.ID || state.Version != 3 {
			t.Fatalf("已选版本被历史任务覆盖：%+v", state)
		}
	})
}
