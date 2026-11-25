package operations_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	styxapierror "gitee.com/linpbin/styx/apierror"
	styxi18n "gitee.com/linpbin/styx/i18n"
	"golang.org/x/crypto/bcrypt"

	"drama-server/internal/biz/adminrbac"
	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/mediaconfig"
	"drama-server/internal/biz/operations"
	"drama-server/internal/pkg/apimessages"
	operationshttp "drama-server/internal/service/operations"
	"drama-server/internal/testsupport/memory"
)

type responseEnvelope struct {
	Data    json.RawMessage `json:"data"`
	Code    int             `json:"code"`
	Reason  string          `json:"reason"`
	Message string          `json:"message"`
}

type failingAuditStore struct {
	*memory.Store
}

type finalizingAuditStore struct {
	*memory.Store
	writes int
}

func TestDataSubjectRoutesAreNotRegistered(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	handler := operationshttp.NewHandler(operations.NewService(memory.OperationsRepositories(store), nil), newRBACService(t, store), operationshttp.Config{})
	login := request(t, handler, http.MethodPost, "/admin/v1/session", "application/json", "", strings.NewReader(`{"username":"admin","password":"local-admin-password"}`))
	var credentials struct {
		Token string `json:"token"`
	}
	decodeData(t, login, &credentials)

	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		response := request(t, handler, method, "/admin/v1/data-subjects/visitor-1", "", credentials.Token, nil)
		if response.Code != http.StatusNotFound {
			t.Fatalf("removed data-subject route %s returned %d", method, response.Code)
		}
	}
}

func TestMediaConfigurationRoutesAreNotRegistered(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	handler := operationshttp.NewHandler(operations.NewService(memory.OperationsRepositories(store), nil), newRBACService(t, store), operationshttp.Config{})

	for _, item := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/admin/v1/media-config"},
		{http.MethodPut, "/admin/v1/media-config"},
		{http.MethodGet, "/admin/v1/media/readiness"},
	} {
		response := request(t, handler, item.method, item.path, "application/json", "", nil)
		if response.Code != http.StatusNotFound {
			t.Fatalf("已删除的媒资配置路由 %s %s 返回 %d", item.method, item.path, response.Code)
		}
	}
}

func TestVODCallbackRouteFollowsOptionalConfiguration(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	service := operations.NewService(memory.OperationsRepositories(store), nil)
	rbac := newRBACService(t, store)
	withoutCallback, err := mediaconfig.NewService(nil)
	if err != nil {
		t.Fatal(err)
	}
	disabled := operationshttp.NewHandler(service, rbac, operationshttp.Config{MediaConfig: withoutCallback})
	if response := request(t, disabled, http.MethodPost, "/callbacks/v1/byteplus/vod", "application/json", "", strings.NewReader(`{}`)); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("未配置 VOD 回调时应拒绝处理，实际 %d", response.Code)
	}

	withCallback, err := mediaconfig.NewService(&mediaconfig.Config{
		AccessKeyID: "access-key", SecretAccessKey: "secret-key", VODAppID: "1038811",
		Region: mediaconfig.BytePlusRegionJohor, SpaceName: "short-drama",
		MaxFileBytes: mediaconfig.DefaultMaxFileBytes, UploadTokenTTLSeconds: 3600,
		CoverDomain:              "https://cover.example.com",
		ProcessingTimeoutSeconds: 900,
		CallbackURL:              "http://api.example.com/callbacks/v1/byteplus/vod", CallbackPrivateKey: "Callback1234",
	})
	if err != nil {
		t.Fatal(err)
	}
	enabled := operationshttp.NewHandler(service, rbac, operationshttp.Config{MediaConfig: withCallback})
	if response := request(t, enabled, http.MethodPost, "/callbacks/v1/byteplus/vod", "application/json", "", strings.NewReader(`{}`)); response.Code != http.StatusUnauthorized {
		t.Fatalf("配置 VOD 回调时应注册并执行鉴权，实际 %d", response.Code)
	}
}

func TestTikTokContentPublicationRoutesAreNotRegistered(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	handler := operationshttp.NewHandler(operations.NewService(memory.OperationsRepositories(store), nil), newRBACService(t, store), operationshttp.Config{})

	for _, action := range []string{"sync", "review", "status"} {
		response := request(t, handler, http.MethodPost, "/admin/v1/dramas/5008/"+action, "", "", nil)
		if response.Code != http.StatusNotFound {
			t.Fatalf("已删除的 TikTok 内容路由 %s 返回 %d", action, response.Code)
		}
	}
}

func TestApplicationMonetizationSettingsAreScopedToSelectedApplication(t *testing.T) {
	t.Parallel()

	store := memory.NewStore(memory.Seed{Applications: []application.Application{
		{MonetizationMode: application.MonetizationModeIAA, TikTokAppID: "5004", Name: "Indonesia", Status: application.StatusActive, AdUnits: application.AdUnits{RewardedVideo: []string{"unlock_reward", "vip_reward", "launch_vip_count"}}},
		{MonetizationMode: application.MonetizationModeIAA, TikTokAppID: "5003", Name: "Brazil", Status: application.StatusActive},
	}})
	service := operations.NewService(memory.OperationsRepositories(store), nil)
	handler := operationshttp.NewHandler(service, newRBACService(t, store), operationshttp.Config{})
	login := request(t, handler, http.MethodPost, "/admin/v1/session", "application/json", "", strings.NewReader(`{"username":"admin","password":"local-admin-password"}`))
	var credentials struct {
		Token string `json:"token"`
	}
	decodeData(t, login, &credentials)
	if login.Code != http.StatusOK || credentials.Token == "" {
		t.Fatalf("admin login failed: %d %s", login.Code, login.Body.String())
	}
	missingApplication := request(t, handler, http.MethodGet, "/admin/v1/application-monetization", "", credentials.Token, nil)
	if missingApplication.Code != http.StatusBadRequest {
		t.Fatalf("应用级接口缺少 X-Application-ID 时必须拒绝，实际 %d %s", missingApplication.Code, missingApplication.Body.String())
	}
	payload := `{"freeEpisodeCount":3,"episodeUnlockAdUnitCode":"unlock_reward","vipStrategy":{"enabled":true,"forceWatchCount":1,"requiredAdCount":2,"delaySeconds":2,"adUnitCode":"vip_reward","title":"Gratis hari ini","content":"Tonton iklan.","failureMessage":"Coba lagi."}}`

	saved := requestWithHeaders(t, handler, http.MethodPut, "/admin/v1/application-monetization", "application/json", credentials.Token, strings.NewReader(payload), map[string]string{"X-Application-ID": "5004"})
	if saved.Code != http.StatusOK || strings.Contains(saved.Body.String(), "planId") || !strings.Contains(saved.Body.String(), `"applicationId":"5004"`) {
		t.Fatalf("direct application monetization update failed: %d %s", saved.Code, saved.Body.String())
	}
	loaded := requestWithHeaders(t, handler, http.MethodGet, "/admin/v1/application-monetization", "", credentials.Token, nil, map[string]string{"X-Application-ID": "5004"})
	if loaded.Code != http.StatusOK || !strings.Contains(loaded.Body.String(), "Gratis hari ini") ||
		!strings.Contains(loaded.Body.String(), `"forceWatchCount":1`) ||
		!strings.Contains(loaded.Body.String(), `"delaySeconds":2`) {
		t.Fatalf("selected application configuration was not returned: %d %s", loaded.Code, loaded.Body.String())
	}
	other := requestWithHeaders(t, handler, http.MethodGet, "/admin/v1/application-monetization", "", credentials.Token, nil, map[string]string{"X-Application-ID": "5003"})
	if other.Code != http.StatusNotFound {
		t.Fatalf("another application must not see selected application's settings: %d %s", other.Code, other.Body.String())
	}
}

type directMediaProviderStub struct{}

// proxiedMediaProviderStub 读取代传文件，供请求超时后的后台上传测试确认文件未丢失。
type proxiedMediaProviderStub struct {
	directMediaProviderStub
	uploaded chan []byte
}

func (provider proxiedMediaProviderStub) UploadEpisodeMedia(_ context.Context, upload operations.Upload) (operations.MediaAsset, error) {
	data, err := io.ReadAll(upload.Reader)
	if err != nil {
		return operations.MediaAsset{}, err
	}
	provider.uploaded <- data
	return operations.MediaAsset{VID: "proxy-vid-1", UploadRequestID: "proxy-request-1"}, nil
}

// cancelOnLastByteReader 模拟网关超时恰好发生在大文件正文读完之后。
type cancelOnLastByteReader struct {
	reader *bytes.Reader
	cancel context.CancelFunc
}

func (reader *cancelOnLastByteReader) Read(buffer []byte) (int, error) {
	read, err := reader.reader.Read(buffer)
	if reader.reader.Len() == 0 {
		reader.cancel()
	}
	return read, err
}

func (directMediaProviderStub) Delete(context.Context, string, string, string, string) error {
	return nil
}

func (directMediaProviderStub) ProcessingTimeout(context.Context) (time.Duration, error) {
	return 15 * time.Minute, nil
}

func (directMediaProviderStub) UploadCover(context.Context, operations.Upload) (operations.CoverAsset, error) {
	return operations.CoverAsset{MID: "cover-1", SpaceName: "short-drama", ProviderFileName: "covers/cover-1.png", URL: "https://vod.example/covers/cover-1.png"}, nil
}

func (directMediaProviderStub) UploadAuthorization(context.Context) (operations.MediaUploadAuthorization, error) {
	return operations.MediaUploadAuthorization{
		Mode: operations.MediaUploadModeDirect, AppID: "1038811", SpaceName: "short-drama",
		Region: "ap-southeast-1", MaxFileBytes: 1024,
		Token: operations.MediaUploadToken{
			AccessKeyID: "temporary-ak", SecretAccessKey: "temporary-sk", SessionToken: "temporary-session",
			CurrentTime: "2026-07-14T00:00:00Z", ExpiredTime: "2026-07-14T01:00:00Z",
		},
	}, nil
}

func (directMediaProviderStub) SyncVODEpisodeMetadata(context.Context, operations.VODEpisodeMetadata, operations.MediaObservation) error {
	return nil
}

func (directMediaProviderStub) FinalizeUpload(_ context.Context, vid, requestID string) (operations.MediaAsset, error) {
	return operations.MediaAsset{VID: vid, Status: "ready"}, nil
}

func (failingAuditStore) SaveAuditRecord(context.Context, operations.AuditRecord) error {
	return errors.New("audit storage unavailable")
}

func (store *finalizingAuditStore) SaveAuditRecord(ctx context.Context, record operations.AuditRecord) error {
	store.writes++
	if store.writes == 2 {
		return errors.New("audit finalization unavailable")
	}
	return store.Store.SaveAuditRecord(ctx, record)
}

func TestAdminHTTPLifecycleRequiresAuthenticationAndPublishes(t *testing.T) {
	t.Parallel()

	store := memory.NewStore(memory.Seed{})
	if err := store.SaveApplication(context.Background(), application.Application{SubjectID: "1",
		MonetizationMode: application.MonetizationModeIAA, TikTokAppID: "app-publication", Name: "Publication App", ClientKey: "mn-publication",
		BytePlusAccountID: "123456789", BytePlusSpaceName: "short-drama",
		SecretRef: "PUBLICATION_SECRET", Status: application.StatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	rbac := newRBACService(t, store)
	service := operations.NewService(memory.OperationsRepositories(store), directMediaProviderStub{})
	if err := service.ConfigureMediaJobs(operations.MediaJobConfig{PollInterval: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	publicationProvider := &publicationProviderStub{}
	if err := service.ConfigureTikTokPublications(func(application.Application) operations.TikTokMediaProvider { return publicationProvider }, operations.PublicationJobConfig{
		PollInterval: time.Millisecond, MediaPollInterval: time.Millisecond, RetryBaseDelay: time.Millisecond,
		LeaseDuration: time.Second, WorkerID: "handler-publication-worker",
	}); err != nil {
		t.Fatal(err)
	}
	workerContext, cancelWorker := context.WithCancel(context.Background())
	t.Cleanup(cancelWorker)
	if err := service.StartMediaWorker(workerContext); err != nil {
		t.Fatal(err)
	}
	if err := service.StartTikTokPublicationWorker(workerContext); err != nil {
		t.Fatal(err)
	}
	handler := operationshttp.NewHandler(service, rbac, operationshttp.Config{})

	unauthorized := request(t, handler, http.MethodGet, "/admin/v1/overview", "", "", nil)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("overview must require auth, got %d", unauthorized.Code)
	}

	login := request(t, handler, http.MethodPost, "/admin/v1/session", "application/json", "", strings.NewReader(`{"username":"admin","password":"local-admin-password"}`))
	if login.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", login.Code, login.Body.String())
	}
	var loginData struct {
		Token       string   `json:"token"`
		Permissions []string `json:"permissions"`
		User        struct {
			Username string `json:"username"`
			IsSuper  bool   `json:"isSuper"`
		} `json:"user"`
	}
	decodeData(t, login, &loginData)
	if loginData.Token == "" {
		t.Fatal("login did not return a token")
	}
	if loginData.User.Username != "admin" || !loginData.User.IsSuper || len(loginData.Permissions) == 0 {
		t.Fatalf("login did not return the RBAC session: %+v", loginData)
	}

	coverBytes := []byte("\x89PNG\r\n\x1a\ncover")
	var coverBody bytes.Buffer
	coverWriter := multipart.NewWriter(&coverBody)
	coverPart, err := coverWriter.CreateFormFile("file", "cover.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = coverPart.Write(coverBytes)
	_ = coverWriter.Close()
	coverResponse := request(t, handler, http.MethodPost, "/admin/v1/covers", coverWriter.FormDataContentType(), loginData.Token, &coverBody)
	if coverResponse.Code != http.StatusCreated {
		t.Fatalf("cover upload failed: %d %s", coverResponse.Code, coverResponse.Body.String())
	}
	var cover operations.CoverAsset
	decodeData(t, coverResponse, &cover)
	if cover.UploadID == "" || cover.ID != "cover-1" || cover.URL != "https://vod.example/covers/cover-1.png" {
		t.Fatalf("unexpected cover response: %+v", cover)
	}

	dramaResponse := request(t, handler, http.MethodPost, "/admin/v1/dramas", "application/json", loginData.Token,
		strings.NewReader(`{
			"title":"Published through HTTP",
			"description":"Short drama synopsis",
			"tagIds":["1"],
			"coverUrl":"https://vod.example/covers/cover-1.png",
			"coverUploadId":"`+cover.UploadID+`",
			"status":"updating",
			"episodes":[{"number":1,"title":"Episode 1"}]
		}`))
	if dramaResponse.Code != http.StatusCreated {
		t.Fatalf("create drama failed: %d %s", dramaResponse.Code, dramaResponse.Body.String())
	}
	var drama struct {
		ContentVersionID string `json:"contentVersionId"`
		ID               string `json:"id"`
		PublishStatus    string `json:"publishStatus"`
		Episodes         []struct {
			ID string `json:"id"`
		} `json:"episodes"`
	}
	decodeData(t, dramaResponse, &drama)
	if len(drama.Episodes) != 1 || drama.Episodes[0].ID == "" {
		t.Fatalf("aggregate response did not include the episode: %+v", drama)
	}
	assetID := reserveHTTPMedia(t, handler, loginData.Token, drama.Episodes[0].ID, "episode.mp4", 5)

	uploadResponse := requestWithHeaders(t, handler, http.MethodPost, "/admin/v1/episodes/"+drama.Episodes[0].ID+"/media/direct", "application/json", loginData.Token,
		strings.NewReader(`{"uploadId":"`+assetID+`","vid":"vod-video-1","uploadRequestId":"upload-request-1","fileName":"episode.mp4","size":5}`),
		map[string]string{"idempotency-key": "http-media-upload-key-1"})
	if uploadResponse.Code != http.StatusAccepted {
		t.Fatalf("upload failed: %d %s", uploadResponse.Code, uploadResponse.Body.String())
	}
	var mediaJob operations.MediaJob
	decodeData(t, uploadResponse, &mediaJob)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		jobResponse := request(t, handler, http.MethodGet, "/admin/v1/media-jobs/"+mediaJob.ID, "", loginData.Token, nil)
		decodeData(t, jobResponse, &mediaJob)
		if mediaJob.Status == operations.MediaJobSucceeded {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if mediaJob.Status != operations.MediaJobSucceeded {
		t.Fatalf("media job did not complete: %+v", mediaJob)
	}
	retryResponse := request(t, handler, http.MethodPost, "/admin/v1/media-jobs/"+mediaJob.ID+"/retry", "application/json", loginData.Token, strings.NewReader(`{}`))
	if retryResponse.Code != http.StatusConflict {
		t.Fatalf("completed job retry should conflict, got %d %s", retryResponse.Code, retryResponse.Body.String())
	}
	audits, err := store.ListAuditRecords(context.Background(), 20)
	if err != nil {
		t.Fatal(err)
	}
	foundJobAudit := false
	for _, audit := range audits {
		if audit.Action == "POST /admin/v1/media-jobs/{jobID}/retry" && audit.ResourceID == mediaJob.ID {
			foundJobAudit = true
			break
		}
	}
	if !foundJobAudit {
		t.Fatalf("media retry audit omitted job ID: %+v", audits)
	}

	readyResponse := request(t, handler, http.MethodPost, "/admin/v1/dramas/"+drama.ID+"/ready", "application/json", loginData.Token, strings.NewReader(`{}`))
	if readyResponse.Code != http.StatusOK {
		t.Fatal("确认待上线失败", readyResponse.Body.String())
	}
	decodeData(t, readyResponse, &drama)
	var readyFields map[string]json.RawMessage
	decodeData(t, readyResponse, &readyFields)
	for _, field := range []string{"contentVersionId", "contentVersion"} {
		if _, exists := readyFields[field]; exists {
			t.Fatalf("短剧管理仍包含版本字段 %s", field)
		}
	}
	// 新同步合同只接受当前就绪内容；版本选择和审核优先级不属于该入口。
	for _, body := range []string{`{"versionId":"123"}`, `{"priority":"urgent"}`} {
		invalid := request(t, handler, http.MethodPost, "/admin/v1/dramas/"+drama.ID+"/publications", "application/json", loginData.Token, strings.NewReader(body))
		if invalid.Code != http.StatusBadRequest {
			t.Fatalf("同步入口接受已删除字段: %d", invalid.Code)
		}
	}
	publishResponse := request(t, handler, http.MethodPost, "/admin/v1/dramas/"+drama.ID+"/publications", "application/json", loginData.Token,
		strings.NewReader(`{}`))
	if publishResponse.Code != http.StatusOK {
		t.Fatalf("publish failed: %d %s", publishResponse.Code, publishResponse.Body.String())
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		publicationResponse := request(t, handler, http.MethodGet, "/admin/v1/dramas/"+drama.ID+"/publication", "", loginData.Token, nil)
		var publication operations.TikTokPublication
		decodeData(t, publicationResponse, &publication)
		if publication.Status == operations.PublicationStatusSynced && publication.Stage == operations.PublicationStageAwaitSubmitReview {
			submitReview := request(t, handler, http.MethodPost, "/admin/v1/dramas/"+drama.ID+"/publication/submit-review", "application/json", loginData.Token, strings.NewReader(`{"priority":"normal"}`))
			if submitReview.Code != http.StatusOK {
				t.Fatalf("提交审核失败：%d %s", submitReview.Code, submitReview.Body.String())
			}
		}
		if publication.Status == operations.PublicationStatusUnderReview {
			refresh := request(t, handler, http.MethodPost, "/admin/v1/dramas/"+drama.ID+"/publication/refresh-review", "", loginData.Token, nil)
			if refresh.Code != http.StatusOK {
				t.Fatalf("手动查询失败：%d %s", refresh.Code, refresh.Body.String())
			}
			decodeData(t, refresh, &publication)
			if publication.Status != operations.PublicationStatusApproved {
				t.Fatalf("审核通过未停留待上线：%+v", publication)
			}
			readback := request(t, handler, http.MethodGet, "/admin/v1/dramas/"+drama.ID, "", loginData.Token, nil)
			var waiting struct {
				PublishStatus string `json:"publishStatus"`
				OnlineVersion int    `json:"onlineVersion"`
			}
			decodeData(t, readback, &waiting)
			if waiting.PublishStatus != "ready" || waiting.OnlineVersion != 0 {
				t.Fatal("人工确认前已对观众发布")
			}
			for _, path := range []string{"/admin/v1/dramas/" + drama.ID + "/publication/submit-review", "/admin/v1/dramas/" + drama.ID + "/publication/refresh-review", "/admin/v1/dramas/" + drama.ID + "/publications/" + publication.ID + "/publish"} {
				denied := request(t, handler, http.MethodPost, path, "", "", nil)
				if denied.Code != http.StatusUnauthorized {
					t.Fatalf("新入口缺少登录保护：%s %d", path, denied.Code)
				}
			}
			stale := request(t, handler, http.MethodPost, "/admin/v1/dramas/"+drama.ID+"/publications/99999999/publish", "", loginData.Token, nil)
			if stale.Code != http.StatusConflict {
				t.Fatalf("允许旧页面确认新版本：%d", stale.Code)
			}
			confirm := request(t, handler, http.MethodPost, "/admin/v1/dramas/"+drama.ID+"/publications/"+publication.ID+"/publish", "", loginData.Token, nil)
			if confirm.Code != http.StatusOK {
				t.Fatalf("确认上线失败：%d %s", confirm.Code, confirm.Body.String())
			}
		}
		if publication.Status == operations.PublicationStatusOnline {
			break
		}
		time.Sleep(time.Millisecond)
	}
	dramaResult := request(t, handler, http.MethodGet, "/admin/v1/dramas/"+drama.ID, "", loginData.Token, nil)
	decodeData(t, dramaResult, &drama)
	if drama.PublishStatus != "ready" {
		t.Fatalf("official publication did not reach the consumer catalog: %+v", drama)
	}
}

func TestAdminDirectMediaUploadAuthorizationAndCompletion(t *testing.T) {
	t.Parallel()

	store := memory.NewStore(memory.Seed{})
	rbac := newRBACService(t, store)
	service := operations.NewService(memory.OperationsRepositories(store), directMediaProviderStub{})
	if err := service.ConfigureMediaJobs(operations.MediaJobConfig{PollInterval: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	workerContext, cancelWorker := context.WithCancel(context.Background())
	t.Cleanup(cancelWorker)
	if err := service.StartMediaWorker(workerContext); err != nil {
		t.Fatal(err)
	}
	handler := operationshttp.NewHandler(service, rbac, operationshttp.Config{})

	login := request(t, handler, http.MethodPost, "/admin/v1/session", "application/json", "",
		strings.NewReader(`{"username":"admin","password":"local-admin-password"}`))
	var credentials struct {
		Token string `json:"token"`
	}
	decodeData(t, login, &credentials)

	authorizationResponse := request(t, handler, http.MethodPost, "/admin/v1/media-uploads/authorization", "application/json",
		credentials.Token, strings.NewReader(`{}`))
	if authorizationResponse.Code != http.StatusOK {
		t.Fatalf("upload authorization failed: %d %s", authorizationResponse.Code, authorizationResponse.Body.String())
	}
	var authorization operations.MediaUploadAuthorization
	decodeData(t, authorizationResponse, &authorization)
	if authorization.Mode != operations.MediaUploadModeDirect || authorization.AppID != "1038811" ||
		authorization.Token.SessionToken != "temporary-session" {
		t.Fatalf("unexpected upload authorization: %+v", authorization)
	}

	dramaResponse := request(t, handler, http.MethodPost, "/admin/v1/dramas", "application/json", credentials.Token,
		strings.NewReader(`{"title":"Direct upload","description":"Short drama synopsis","status":"updating","episodes":[{"number":1,"title":"Episode 1"}]}`))
	var drama struct {
		ID       string `json:"id"`
		Episodes []struct {
			ID string `json:"id"`
		} `json:"episodes"`
	}
	decodeData(t, dramaResponse, &drama)
	assetID := reserveHTTPMedia(t, handler, credentials.Token, drama.Episodes[0].ID, "episode.mp4", 5)
	refreshResponse := request(t, handler, http.MethodPost, "/admin/v1/episodes/"+drama.Episodes[0].ID+"/media-uploads/"+assetID+"/authorization",
		"application/json", credentials.Token, strings.NewReader(`{}`))
	if refreshResponse.Code != http.StatusOK {
		t.Fatalf("refresh upload authorization failed: %d %s", refreshResponse.Code, refreshResponse.Body.String())
	}
	var refreshedAuthorization operations.MediaUploadAuthorization
	decodeData(t, refreshResponse, &refreshedAuthorization)
	if refreshedAuthorization.Token.SessionToken != "temporary-session" || !strings.Contains(refreshedAuthorization.CallbackArgs, assetID) {
		t.Fatalf("unexpected refreshed upload authorization: %+v", refreshedAuthorization)
	}
	diagnosticResponse := request(t, handler, http.MethodPost, "/admin/v1/episodes/"+drama.Episodes[0].ID+"/media-uploads/"+assetID+"/diagnostics",
		"application/json", credentials.Token, strings.NewReader(`{"stage":"initUploadID","code":"1002","status":0,"reason":"request fail: ERR_CONNECTION_RESET"}`))
	if diagnosticResponse.Code != http.StatusOK {
		t.Fatalf("direct upload diagnostic failed: %d %s", diagnosticResponse.Code, diagnosticResponse.Body.String())
	}
	completionResponse := requestWithHeaders(t, handler, http.MethodPost, "/admin/v1/episodes/"+drama.Episodes[0].ID+"/media/direct",
		"application/json", credentials.Token,
		strings.NewReader(`{"uploadId":"`+assetID+`","vid":"direct-vid-1","uploadRequestId":"request-1","fileName":"episode.mp4","size":5}`),
		map[string]string{"idempotency-key": "direct-http-upload-key-1"})
	if completionResponse.Code != http.StatusAccepted {
		t.Fatalf("direct completion failed: %d %s", completionResponse.Code, completionResponse.Body.String())
	}
	var mediaJob operations.MediaJob
	decodeData(t, completionResponse, &mediaJob)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		jobResponse := request(t, handler, http.MethodGet, "/admin/v1/media-jobs/"+mediaJob.ID, "", credentials.Token, nil)
		decodeData(t, jobResponse, &mediaJob)
		if mediaJob.Status == operations.MediaJobSucceeded {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if mediaJob.Status != operations.MediaJobSucceeded || mediaJob.Episode == nil || mediaJob.Episode.VID != "direct-vid-1" {
		t.Fatalf("direct upload was not finalized authoritatively: %+v", mediaJob)
	}
	batchResponse := request(t, handler, http.MethodGet, "/admin/v1/dramas/"+drama.ID+"/media-jobs", "", credentials.Token, nil)
	if batchResponse.Code != http.StatusOK {
		t.Fatalf("drama media jobs failed: %d %s", batchResponse.Code, batchResponse.Body.String())
	}
	var jobs []operations.MediaJob
	decodeData(t, batchResponse, &jobs)
	if len(jobs) != 1 || jobs[0].ID != mediaJob.ID {
		t.Fatalf("unexpected drama media jobs: %+v", jobs)
	}
}

func TestProxyMediaUploadRegistersTaskAfterRequestTimeout(t *testing.T) {
	t.Parallel()

	store := memory.NewStore(memory.Seed{})
	provider := proxiedMediaProviderStub{uploaded: make(chan []byte, 1)}
	service := operations.NewService(memory.OperationsRepositories(store), provider)
	handler := operationshttp.NewHandler(service, newRBACService(t, store), operationshttp.Config{})
	login := request(t, handler, http.MethodPost, "/admin/v1/session", "application/json", "",
		strings.NewReader(`{"username":"admin","password":"local-admin-password"}`))
	var credentials struct {
		Token string `json:"token"`
	}
	decodeData(t, login, &credentials)
	dramaResponse := request(t, handler, http.MethodPost, "/admin/v1/dramas", "application/json", credentials.Token,
		strings.NewReader(`{"title":"Proxy upload","description":"Short drama synopsis","status":"updating","episodes":[{"number":1,"title":"Episode 1"}]}`))
	var drama struct {
		Episodes []struct {
			ID string `json:"id"`
		} `json:"episodes"`
	}
	decodeData(t, dramaResponse, &drama)
	media := []byte("video")
	uploadID := reserveHTTPMedia(t, handler, credentials.Token, drama.Episodes[0].ID, "episode.mp4", int64(len(media)))
	var body bytes.Buffer
	multipartWriter := multipart.NewWriter(&body)
	part, err := multipartWriter.CreateFormFile("file", "episode.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(media); err != nil {
		t.Fatal(err)
	}
	if err := multipartWriter.Close(); err != nil {
		t.Fatal(err)
	}
	path := "/admin/v1/episodes/" + drama.Episodes[0].ID + "/media-uploads/" + uploadID + "/proxy"
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body.Bytes()))
	request.Header.Set("X-Subject-ID", "1")
	request.Header.Set("content-type", multipartWriter.FormDataContentType())
	request.Header.Set("authorization", "Bearer "+credentials.Token)
	requestContext, cancel := context.WithCancel(request.Context())
	defer cancel()
	request = request.WithContext(requestContext)
	request.Body = io.NopCloser(&cancelOnLastByteReader{reader: bytes.NewReader(body.Bytes()), cancel: cancel})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("正文接收后请求超时仍应登记上传任务：status=%d", response.Code)
	}
	select {
	case uploaded := <-provider.uploaded:
		if !bytes.Equal(uploaded, media) {
			t.Fatal("后台上传收到的视频内容不一致")
		}
	case <-time.After(time.Second):
		t.Fatal("超时后后台上传未收到完整视频")
	}
}

func reserveHTTPMedia(t *testing.T, handler http.Handler, token, episodeID, fileName string, size int64) string {
	t.Helper()
	body, err := json.Marshal(operations.EpisodeMediaUploadReservationInput{FileName: fileName, Size: size})
	if err != nil {
		t.Fatal(err)
	}
	response := request(t, handler, http.MethodPost, "/admin/v1/episodes/"+episodeID+"/media-uploads", "application/json", token, bytes.NewReader(body))
	if response.Code != http.StatusCreated {
		t.Fatalf("reserve media failed: %d %s", response.Code, response.Body.String())
	}
	var reservation operations.EpisodeMediaUploadReservation
	decodeData(t, response, &reservation)
	if reservation.UploadID == "" || !strings.Contains(reservation.Authorization.CallbackArgs, reservation.UploadID) {
		t.Fatalf("reservation omitted managed media identity: %+v", reservation)
	}
	return reservation.UploadID
}

func TestAdminLoginWithoutLimiterDoesNotKeepProcessLocalFailureState(t *testing.T) {
	t.Parallel()

	store := memory.NewStore(memory.Seed{})
	rbac := newRBACService(t, store)
	handler := operationshttp.NewHandler(nil, rbac, operationshttp.Config{})
	for attempt := 1; attempt <= 10; attempt++ {
		response := request(t, handler, http.MethodPost, "/admin/v1/session", "application/json", "",
			strings.NewReader(`{"username":"admin","password":"wrong-password"}`))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("未注入跨实例限流器时，第 %d 次错误登录应只返回未授权，实际 %d", attempt, response.Code)
		}
	}
	success := request(t, handler, http.MethodPost, "/admin/v1/session", "application/json", "",
		strings.NewReader(`{"username":"admin","password":"local-admin-password"}`))
	if success.Code != http.StatusOK {
		t.Fatalf("正确凭据不应受此前失败次数影响，got %d %s", success.Code, success.Body.String())
	}
}

func TestAdminLogoutRevokesDatabaseSession(t *testing.T) {
	t.Parallel()

	store := memory.NewStore(memory.Seed{})
	handler := operationshttp.NewHandler(nil, newRBACService(t, store), operationshttp.Config{})
	login := request(t, handler, http.MethodPost, "/admin/v1/session", "application/json", "",
		strings.NewReader(`{"username":"admin","password":"local-admin-password"}`))
	var credentials struct {
		Token string `json:"token"`
	}
	decodeData(t, login, &credentials)
	logout := request(t, handler, http.MethodDelete, "/admin/v1/session", "", credentials.Token, nil)
	if logout.Code != http.StatusOK {
		t.Fatalf("退出状态=%d body=%s", logout.Code, logout.Body.String())
	}
	me := request(t, handler, http.MethodGet, "/admin/v1/me", "", credentials.Token, nil)
	if me.Code != http.StatusUnauthorized {
		t.Fatalf("退出后原会话仍可访问：status=%d body=%s", me.Code, me.Body.String())
	}
}

func TestRemovedConsumerMetricsAndAuditContract(t *testing.T) {
	t.Parallel()

	store := memory.NewStore(memory.Seed{})
	rbac := newRBACService(t, store)
	handler := operationshttp.NewHandler(operations.NewService(memory.OperationsRepositories(store), nil), rbac, operationshttp.Config{})
	login := request(t, handler, http.MethodPost, "/admin/v1/session", "application/json", "",
		strings.NewReader(`{"username":"admin","password":"local-admin-password"}`))
	var credentials struct {
		Token string `json:"token"`
	}
	decodeData(t, login, &credentials)

	metricsResponse := requestWithHeaders(
		t, handler, http.MethodGet, "/admin/v1/consumer-metrics?date=2026-07-10", "", credentials.Token, nil,
		map[string]string{"X-Application-ID": memory.TestApplicationID},
	)
	if metricsResponse.Code != http.StatusNotFound {
		t.Fatalf("已移除消费指标接口应返回 404，实际 %d", metricsResponse.Code)
	}

	payload := `{"freeEpisodeCount":2,"episodeUnlockAdUnitCode":"rewarded","vipStrategy":{"enabled":true,"forceWatchCount":2,"requiredAdCount":3,"delaySeconds":5,"adUnitCode":"rewarded","title":"Daily VIP","content":"Watch ads","failureMessage":"Try again"}}`
	update := requestWithHeaders(
		t, handler, http.MethodPut, "/admin/v1/application-monetization", "application/json", credentials.Token,
		strings.NewReader(payload), map[string]string{"X-Application-ID": memory.TestApplicationID},
	)
	if update.Code != http.StatusOK {
		t.Fatalf("audited mutation failed: %d %s", update.Code, update.Body.String())
	}
	auditResponse := request(t, handler, http.MethodGet, "/admin/v1/audit-logs?limit=20", "", credentials.Token, nil)
	var records []operations.AuditRecord
	decodeData(t, auditResponse, &records)
	if len(records) == 0 || records[0].Action != "PUT /admin/v1/application-monetization" || records[0].Result != "success" {
		t.Fatalf("mutation audit was not recorded: %+v", records)
	}
}

func TestAdminMutationDoesNotRunWhenAuditIntentCannotBePersisted(t *testing.T) {
	store := memory.NewStore(memory.Seed{})
	rbac := newRBACService(t, store)
	repositories := memory.OperationsRepositories(store)
	repositories.Reporting.Audits = failingAuditStore{Store: store}
	handler := operationshttp.NewHandler(operations.NewService(repositories, nil), rbac, operationshttp.Config{})
	login := request(t, handler, http.MethodPost, "/admin/v1/session", "application/json", "",
		strings.NewReader(`{"username":"admin","password":"local-admin-password"}`))
	var credentials struct {
		Token string `json:"token"`
	}
	decodeData(t, login, &credentials)
	payload := `{"freeEpisodeCount":2,"episodeUnlockAdUnitCode":"rewarded","vipStrategy":{"enabled":true,"forceWatchCount":2,"requiredAdCount":3,"delaySeconds":5,"adUnitCode":"rewarded","title":"Must not persist","content":"Watch ads","failureMessage":"Try again"}}`

	response := requestWithHeaders(
		t, handler, http.MethodPut, "/admin/v1/application-monetization", "application/json", credentials.Token,
		strings.NewReader(payload), map[string]string{"X-Application-ID": memory.TestApplicationID},
	)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("audit failure must stop mutation, got %d %s", response.Code, response.Body.String())
	}
	_, err := store.GetApplicationMonetization(context.Background())
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("mutation ran before its audit intent was durable")
	}
}

func TestAdminMutationKeepsBusinessResultWhenAuditFinalizationFails(t *testing.T) {
	base := memory.NewStore(memory.Seed{})
	store := &finalizingAuditStore{Store: base}
	rbac := newRBACService(t, base)
	repositories := memory.OperationsRepositories(base)
	repositories.Reporting.Audits = store
	handler := operationshttp.NewHandler(operations.NewService(repositories, nil), rbac, operationshttp.Config{})
	login := request(t, handler, http.MethodPost, "/admin/v1/session", "application/json", "",
		strings.NewReader(`{"username":"admin","password":"local-admin-password"}`))
	var credentials struct {
		Token string `json:"token"`
	}
	decodeData(t, login, &credentials)
	payload := `{"freeEpisodeCount":2,"episodeUnlockAdUnitCode":"rewarded","vipStrategy":{"enabled":true,"forceWatchCount":2,"requiredAdCount":3,"delaySeconds":5,"adUnitCode":"rewarded","title":"Daily VIP","content":"Watch ads","failureMessage":"Try again"}}`

	response := requestWithHeaders(
		t, handler, http.MethodPut, "/admin/v1/application-monetization", "application/json", credentials.Token,
		strings.NewReader(payload), map[string]string{"X-Application-ID": memory.TestApplicationID},
	)
	if response.Code != http.StatusOK {
		t.Fatalf("业务已经提交后必须保留原成功响应，实际为：%d %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"freeEpisodeCount":2`) {
		t.Fatalf("审计最终化失败时丢失了原业务响应：%s", response.Body.String())
	}
	saved, err := base.GetApplicationMonetization(context.Background())
	if err != nil || saved.FreeEpisodeCount != 2 {
		t.Fatalf("业务状态应与成功响应一致：saved=%+v err=%v", saved, err)
	}
}

func TestAdminRoutesEnforceCurrentRolePermissions(t *testing.T) {
	t.Parallel()

	store := memory.NewStore(memory.Seed{})
	rbac := newRBACService(t, store)
	admin, err := store.FindAdminUserByUsername(context.Background(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	role, err := rbac.CreateRole(context.Background(), adminrbac.RoleInput{Name: "只读运营", Code: "read_only", Status: adminrbac.StatusActive})
	if err != nil {
		t.Fatal(err)
	}
	permissions, err := rbac.ListPermissions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	readID := ""
	createID := ""
	for _, permission := range permissions {
		switch permission.Code {
		case adminrbac.PermissionDramaRead:
			readID = permission.ID
		case adminrbac.PermissionDramaCreate:
			createID = permission.ID
		}
	}
	if err := rbac.SetRolePermissions(context.Background(), role.ID, []string{readID}); err != nil {
		t.Fatal(err)
	}
	user, err := rbac.CreateUser(context.Background(), adminrbac.UserCreateInput{
		Username: "viewer", Password: "viewer-password", Nickname: "只读用户", Status: adminrbac.StatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := rbac.SetUserRolesAs(context.Background(), admin.ID, user.ID, []string{role.ID}); err != nil {
		t.Fatal(err)
	}
	handler := operationshttp.NewHandler(operations.NewService(memory.OperationsRepositories(store), nil), rbac, operationshttp.Config{})
	login := request(t, handler, http.MethodPost, "/admin/v1/session", "application/json", "",
		strings.NewReader(`{"username":"viewer","password":"viewer-password"}`))
	var credentials struct {
		Token string `json:"token"`
	}
	decodeData(t, login, &credentials)

	allowed := request(t, handler, http.MethodGet, "/admin/v1/dramas", "", credentials.Token, nil)
	if allowed.Code != http.StatusOK {
		t.Fatalf("read permission should allow list, got %d %s", allowed.Code, allowed.Body.String())
	}
	denied := request(t, handler, http.MethodPost, "/admin/v1/dramas", "application/json", credentials.Token,
		strings.NewReader(`{"title":"Denied","description":"Short drama synopsis","status":"updating"}`))
	if denied.Code != http.StatusForbidden {
		t.Fatalf("missing create permission should return 403, got %d %s", denied.Code, denied.Body.String())
	}

	for _, path := range []string{"/admin/v1/dramas/5010/publication/refresh-review", "/admin/v1/dramas/5010/publications/publication-a/publish"} {
		result := request(t, handler, http.MethodPost, path, "", credentials.Token, nil)
		if result.Code != http.StatusForbidden {
			t.Fatalf("只读用户可操作审核或上线：%s %d", path, result.Code)
		}
	}

	if err := rbac.SetRolePermissions(context.Background(), role.ID, []string{readID, createID}); err != nil {
		t.Fatal(err)
	}
	granted := request(t, handler, http.MethodPost, "/admin/v1/dramas", "application/json", credentials.Token,
		strings.NewReader(`{"title":"Granted","description":"Short drama synopsis","status":"updating","episodes":[{"number":1,"title":"Episode 1"}]}`))
	if granted.Code != http.StatusCreated {
		t.Fatalf("new grant must affect existing token, got %d %s", granted.Code, granted.Body.String())
	}
}

func TestSuperAdminCanManageUsersRolesAndPermissions(t *testing.T) {
	t.Parallel()

	store := memory.NewStore(memory.Seed{})
	rbac := newRBACService(t, store)
	handler := operationshttp.NewHandler(operations.NewService(memory.OperationsRepositories(store), nil), rbac, operationshttp.Config{})
	login := request(t, handler, http.MethodPost, "/admin/v1/session", "application/json", "",
		strings.NewReader(`{"username":"admin","password":"local-admin-password"}`))
	var credentials struct {
		Token string `json:"token"`
	}
	decodeData(t, login, &credentials)

	roleResponse := request(t, handler, http.MethodPost, "/admin/v1/roles", "application/json", credentials.Token,
		strings.NewReader(`{"name":"编辑","code":"editor","status":"active","remark":"内容编辑"}`))
	if roleResponse.Code != http.StatusCreated {
		t.Fatalf("create role failed: %d %s", roleResponse.Code, roleResponse.Body.String())
	}
	var role adminrbac.Role
	decodeData(t, roleResponse, &role)
	userResponse := request(t, handler, http.MethodPost, "/admin/v1/users", "application/json", credentials.Token,
		strings.NewReader(`{"username":"editor","password":"editor-password","nickname":"编辑","status":"active"}`))
	if userResponse.Code != http.StatusCreated {
		t.Fatalf("create user failed: %d %s", userResponse.Code, userResponse.Body.String())
	}
	var user adminrbac.User
	decodeData(t, userResponse, &user)
	assign := request(t, handler, http.MethodPut, "/admin/v1/users/"+user.ID+"/roles", "application/json", credentials.Token,
		strings.NewReader(`{"roleIds":["`+role.ID+`"]}`))
	if assign.Code != http.StatusOK {
		t.Fatalf("assign role failed: %d %s", assign.Code, assign.Body.String())
	}
	users := request(t, handler, http.MethodGet, "/admin/v1/users", "", credentials.Token, nil)
	if users.Code != http.StatusOK || !strings.Contains(users.Body.String(), `"roleIds":["`+role.ID+`"]`) {
		t.Fatalf("user roles were not returned: %d %s", users.Code, users.Body.String())
	}

}

func newRBACService(t *testing.T, store *memory.Store) *adminrbac.Service {
	t.Helper()
	rbac := adminrbac.NewService(store, adminrbac.Config{SigningKey: []byte("test-only-admin-jwt-signing-key-32-bytes"), SessionTTL: time.Hour})
	ctx := context.Background()
	hash, err := bcrypt.GenerateFromPassword([]byte("local-admin-password"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.CreateAdminUser(ctx, &adminrbac.User{
		Username: "admin", PasswordHash: string(hash), Nickname: "超级管理员",
		Status: adminrbac.StatusActive, IsSuper: true, AuthVersion: 1, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	return rbac
}

func request(t *testing.T, handler http.Handler, method, path, contentType, token string, body io.Reader) *httptest.ResponseRecorder {
	return requestWithHeaders(t, handler, method, path, contentType, token, body, nil)
}

func requestWithHeaders(t *testing.T, handler http.Handler, method, path, contentType, token string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	messages, err := apimessages.NewMessageBundle()
	if err != nil {
		t.Fatal(err)
	}
	handler = styxi18n.HTTPFilter(messages)(styxapierror.HTTPFilter()(handler))
	request := httptest.NewRequest(method, path, body)
	request.Header.Set("x-subject-id", "1")
	if contentType != "" {
		request.Header.Set("content-type", contentType)
	}
	if token != "" {
		request.Header.Set("authorization", "Bearer "+token)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func decodeData(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	var envelope responseEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Code != 0 {
		t.Fatalf("unexpected API error: %+v", envelope.Reason)
	}
	if err := json.Unmarshal(envelope.Data, target); err != nil {
		t.Fatal(err)
	}
}
