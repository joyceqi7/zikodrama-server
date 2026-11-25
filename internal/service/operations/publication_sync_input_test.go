package operations_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	operationshttp "drama-server/internal/service/operations"
	"drama-server/internal/testsupport/memory"
)

// syncChoiceProvider 记录 HTTP 选项是否实际改变媒资调用，不连接真实平台。
type syncChoiceProvider struct {
	publicationProviderStub
	registered, transcoded int
}

// RegisterVideo 验证跳过转码不能同时跳过官方登记。
func (p *syncChoiceProvider) RegisterVideo(ctx context.Context, input operations.TikTokVideoInput) (operations.TikTokVideoRegistration, error) {
	p.registered++
	return p.Capabilities.RegisterVideo(ctx, input)
}

// StartTranscode 记录显式开启和旧客户端默认转码策略。
func (p *syncChoiceProvider) StartTranscode(ctx context.Context, input operations.TikTokTranscodeInput) (string, error) {
	p.transcoded++
	return p.Capabilities.StartTranscode(ctx, input)
}

// TestOfficialSyncTranscodeHTTPContract 覆盖真实路由传参、严格布尔输入和旧空对象兼容。
func TestOfficialSyncTranscodeHTTPContract(t *testing.T) {
	for _, tc := range []struct {
		body      string
		status    int
		transcode bool
	}{
		{`{}`, 200, true}, {`{"transcode":false}`, 200, false}, {`{"transcode":true}`, 200, true},
		{`{"transcode":"false"}`, 400, false}, {`{"ownerApplicationId":"999"}`, 400, false},
		{`{"transcode":false}`, 409, false},
	} {
		t.Run(tc.body, func(t *testing.T) {
			owner := application.Application{SubjectID: "1", ID: "101", TikTokAppID: "owner", ClientKey: "fixture", ClientSecret: "test-only", BytePlusAccountID: "account", BytePlusSpaceName: "space", Status: application.StatusActive}
			tags := []string{"1"}
			if tc.status == http.StatusConflict {
				tags = nil // 仅官方同步要求选标签，草稿仍允许没有标签。
			}
			draft := consumer.Drama{ID: "201", Title: "草稿", Description: "简介", CoverURL: "https://image.example/cover", Language: "en", PublishStatus: "draft", Status: consumer.DramaStatusCompleted, TagIDs: tags, Episodes: []consumer.Episode{{ID: "301", DramaID: "201", Number: 1, Title: "第1集", VID: "source", MediaStatus: "ready"}}}
			store := memory.NewStore(memory.Seed{Applications: []application.Application{owner}, Dramas: []consumer.Drama{draft}})
			repos := memory.OperationsRepositories(store)
			repos.Content.OwnerApplicationID = func(context.Context) (string, error) { return owner.ID, nil }
			repos.Content.OwnerApplication = func(context.Context, string) (application.Application, error) { return owner, nil }
			service := operations.NewService(repos, nil)
			provider := &syncChoiceProvider{}
			if err := service.ConfigureTikTokPublications(func(application.Application) operations.TikTokMediaProvider { return provider }, operations.PublicationJobConfig{}); err != nil {
				t.Fatal(err)
			}
			handler := operationshttp.NewHandler(service, newRBACService(t, store), operationshttp.Config{})
			var login struct {
				Token string `json:"token"`
			}
			decodeData(t, request(t, handler, "POST", "/admin/v1/session", "application/json", "", strings.NewReader(`{"username":"admin","password":"local-admin-password"}`)), &login)
			response := request(t, handler, http.MethodPost, "/admin/v1/dramas/201/publications", "application/json", login.Token, strings.NewReader(tc.body))
			if response.Code != tc.status {
				t.Fatalf("同步选项响应=%d，期望=%d", response.Code, tc.status)
			}
			if tc.status != http.StatusOK {
				if provider.registered != 0 || provider.transcoded != 0 {
					t.Fatal("前置校验失败仍调用平台")
				}
				if tc.status == http.StatusConflict {
					var rejection responseEnvelope
					if err := json.Unmarshal(response.Body.Bytes(), &rejection); err != nil {
						t.Fatal(err)
					}
					if rejection.Reason != "publication_not_ready" || !strings.Contains(rejection.Message, "TikTok tag") {
						t.Fatal("缺少标签未返回稳定错误码及补齐提示", rejection.Reason)
					}
				}
				return
			}
			if provider.registered != 1 || (provider.transcoded > 0) != tc.transcode {
				t.Fatal("HTTP 没有传递转码选项或跳过登记", provider.registered, provider.transcoded)
			}
			p, err := store.FindTikTokPublicationByDramaID(context.Background(), "201")
			if err != nil || p.SkipTranscode == tc.transcode {
				t.Fatal("转码选择未冻结", err)
			}
		})
	}
}
