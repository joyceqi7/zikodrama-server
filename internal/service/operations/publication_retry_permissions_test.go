package operations

import (
	"bytes"
	"context"
	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	biz "drama-server/internal/biz/operations"
	"drama-server/internal/testsupport/memory"
	"drama-server/internal/testsupport/officialmedia"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPublicationRetryStagePermissions 验证三类权限互不越权，旧发布权限仍覆盖既有业务。
func TestPublicationRetryStagePermissions(t *testing.T) {
	for _, test := range []struct{ stage, permission string }{
		{"update_album", adminrbac.PermissionDramaSync},
		{"submit_review", adminrbac.PermissionDramaReview},
		{"poll_review", adminrbac.PermissionDramaReview},
		{"list_album", adminrbac.PermissionDramaRelease},
		{"delist_album", adminrbac.PermissionDramaRelease},
	} {
		for _, permission := range []string{adminrbac.PermissionDramaSync, adminrbac.PermissionDramaReview, adminrbac.PermissionDramaRelease, adminrbac.PermissionDramaPublish} {
			session := adminrbac.Session{Permissions: []string{permission}}
			want := permission == test.permission || permission == adminrbac.PermissionDramaPublish
			if got := canRetryPublication(session, test.stage); got != want {
				t.Errorf("%s / %s: got %v want %v", test.stage, permission, got, want)
			}
		}
	}
	if canRetryPublication(adminrbac.Session{}, "list_album") {
		t.Fatal("无权限用户可以重试上线")
	}
}

// TestPublicationRetryPermissionLogging 验证越权有诊断且不改任务，获准恢复不误报错误或记录凭据。
func TestPublicationRetryPermissionLogging(t *testing.T) {
	store := memory.NewStore(memory.Seed{Dramas: []domain.Drama{{ID: "201", PublishStatus: "draft"}}})
	p := biz.TikTokPublication{SubjectID: "1", ID: "901", DramaID: "201", Status: biz.PublicationStatusFailed, Stage: biz.PublicationStageListAlbum, OwnerApplicationID: "101", PublishRequested: true, ReviewStatus: 2, PlatformAlbumID: "album", PlatformVersion: 1, ContentSnapshot: &domain.Drama{ID: "201", Title: "fixture", Episodes: []domain.Episode{{ID: "301", Number: 1, VID: "fixture-vid"}}}, EpisodeIDsBySequence: map[int]string{1: "fixture-episode"}}
	store.SeedTikTokPublication(&p)
	h := &Handler{service: newRetryTestService(t, store)}
	var logs bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(original) })
	call := func(permission string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/admin/v1/dramas/201/publication/retry", nil)
		r.SetPathValue("dramaID", "201")
		r.Header.Set("Authorization", "Bearer private-fixture-token")
		r = r.WithContext(context.WithValue(r.Context(), adminSessionContextKey{}, adminrbac.Session{Permissions: []string{permission}}))
		w := httptest.NewRecorder()
		h.retryTikTokPublication(w, r)
		return w
	}
	if response := call(adminrbac.PermissionDramaSync); response.Code != http.StatusForbidden {
		t.Fatalf("同步权限越权上线: %d", response.Code)
	}
	if !strings.Contains(logs.String(), "retry publication stage permission rejected") || !strings.Contains(logs.String(), adminrbac.ErrForbidden.Error()) || strings.Contains(logs.String(), "private-fixture-token") {
		t.Fatal("拒绝日志不完整或泄露凭据")
	}
	unchanged, err := store.FindTikTokPublicationByDramaID(context.Background(), "201")
	if err != nil || unchanged.Status != biz.PublicationStatusFailed {
		t.Fatal("拒绝后任务发生变化")
	}
	logs.Reset()
	if response := call(adminrbac.PermissionDramaRelease); response.Code != http.StatusOK {
		t.Fatalf("上线权限不能恢复上线: %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(logs.String(), `"level":"ERROR"`) || strings.Contains(logs.String(), "permission rejected") || strings.Contains(logs.String(), "private-fixture-token") {
		t.Fatal("成功路径误报故障或泄露凭据")
	}
}

// TestReviewRetryPriorityInput 验证可选参数兼容旧客户端，枚举和阶段错误不会改变任务。
func TestReviewRetryPriorityInput(t *testing.T) {
	for _, test := range []struct {
		name, body, stage, wantPriority string
		status                          int
	}{
		{"old empty body", "", biz.PublicationStageSubmitReview, "urgent", 200},
		{"normal retry", `{"priority":" NORMAL "}`, biz.PublicationStageSubmitReview, "normal", 200},
		{"urgent retry", `{"priority":"urgent"}`, biz.PublicationStageSubmitReview, "urgent", 200},
		{"invalid priority", `{"priority":"fast"}`, biz.PublicationStageSubmitReview, "urgent", 400},
		{"reject unknown fields", `{"priority":"normal","secret":"private-input"}`, biz.PublicationStageSubmitReview, "urgent", 400},
		{"other stage", `{"priority":"normal"}`, biz.PublicationStagePollReview, "urgent", 409},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := memory.NewStore(memory.Seed{Dramas: []domain.Drama{{ID: "201", PublishStatus: "draft"}}})
			p := biz.TikTokPublication{SubjectID: "1", ID: "901", DramaID: "201", Status: biz.PublicationStatusFailed, Stage: test.stage, Priority: "urgent", OwnerApplicationID: "101", PlatformAlbumID: "album", PlatformVersion: 1, ContentSnapshot: &domain.Drama{ID: "201", Title: "fixture"}}
			store.SeedTikTokPublication(&p)
			h := &Handler{service: newRetryTestService(t, store)}
			r := httptest.NewRequest(http.MethodPost, "/admin/v1/dramas/201/publication/retry", strings.NewReader(test.body))
			r.SetPathValue("dramaID", "201")
			r = r.WithContext(context.WithValue(r.Context(), adminSessionContextKey{}, adminrbac.Session{Permissions: []string{adminrbac.PermissionDramaReview}}))
			w := httptest.NewRecorder()
			h.retryTikTokPublication(w, r)
			if w.Code != test.status {
				t.Fatalf("status=%d want=%d", w.Code, test.status)
			}
			got, err := store.FindTikTokPublicationByDramaID(context.Background(), "201")
			if err != nil || got.Priority != test.wantPriority {
				t.Fatalf("priority changed unexpectedly: %v %v", got, err)
			}
			if test.status != 200 && got.Status != biz.PublicationStatusFailed {
				t.Fatal("rejected input changed task")
			}
		})
	}
}

type retryTestProvider struct{ officialmedia.Capabilities }

func (*retryTestProvider) SubmitReview(context.Context, string, int, int) (string, error) {
	return "review", nil
}
func (*retryTestProvider) SetAlbumListed(context.Context, string, bool) error  { return nil }
func (*retryTestProvider) SetOnlineVersion(context.Context, string, int) error { return nil }
func newRetryTestService(t *testing.T, store *memory.Store) *biz.Service {
	t.Helper()
	repo := memory.OperationsRepositories(store)
	repo.Content.OwnerApplication = func(context.Context, string) (application.Application, error) {
		return application.Application{}, nil
	}
	s := biz.NewService(repo, nil)
	if err := s.ConfigureTikTokPublications(func(application.Application) biz.TikTokMediaProvider { return &retryTestProvider{} }, biz.PublicationJobConfig{}); err != nil {
		t.Fatal(err)
	}
	return s
}

func (*retryTestProvider) AuthorizeAlbum(context.Context, string, []string) error { return nil }
func (*retryTestProvider) CreateImage(context.Context, string) (string, error)    { return "image", nil }
func (*retryTestProvider) CreateAlbum(context.Context) (biz.CreateAlbumResult, error) {
	return biz.CreateAlbumResult{AlbumID: "album"}, nil
}
func (*retryTestProvider) UpdateAlbum(context.Context, biz.AlbumDraft) (biz.UpdateAlbumResult, error) {
	return biz.UpdateAlbumResult{}, nil
}
func (*retryTestProvider) QueryAlbum(context.Context, string, int) (biz.AlbumObservation, error) {
	return biz.AlbumObservation{ReviewStatus: 2}, nil
}
