package operations_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"drama-server/internal/biz/operations"
	"drama-server/internal/data/platform/tiktokmedia"
)

// registrationHTTPProvider 将真实视频适配器接入发布 Worker，其余官方步骤使用已有替身。
type registrationHTTPProvider struct {
	publicationProviderStub
	client        *tiktokmedia.Client
	queryResponse string
}

// RegisterVideo 通过本地 HTTP 响应验证真实登记解析与业务检查点衔接。
func (p *registrationHTTPProvider) RegisterVideo(ctx context.Context, input operations.TikTokVideoInput) (operations.TikTokVideoRegistration, error) {
	p.record("video-register")
	return p.client.RegisterVideo(ctx, input)
}

// QueryVideo 通过真实适配器核验查询状态和 VID，不能由替身直接放行异常响应。
func (p *registrationHTTPProvider) QueryVideo(ctx context.Context, jobID, vid, accountID string) (operations.TikTokVideoObservation, error) {
	p.record("video-query")
	return p.client.QueryVideo(ctx, jobID, vid, accountID)
}

// registrationLocalTransport 强制所有视频适配器请求进入本地测试服务器。
type registrationLocalTransport struct{ target *url.URL }

// RoundTrip 只改写主机与协议，保留真实请求路径、查询参数、头和正文。
func (transport registrationLocalTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	local := request.Clone(request.Context())
	local.URL.Scheme, local.URL.Host = transport.target.Scheme, transport.target.Host
	return http.DefaultTransport.RoundTrip(local)
}

// newRegistrationHTTPProvider 构造受控平台响应，严禁访问真实 TikTok 或 BytePlus。
func newRegistrationHTTPProvider(t *testing.T, registrationResponse, queryResponse string) *registrationHTTPProvider {
	t.Helper()
	provider := &registrationHTTPProvider{queryResponse: queryResponse}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Tt-Logid", "local-registration-request")
		if r.URL.Path == "/v2/oauth/token/" {
			_, _ = io.WriteString(w, `{"access_token":"test-token","expires_in":7200}`)
			return
		}
		if r.URL.Path != "/v2/sg/shortdrama/video" {
			t.Errorf("意外平台请求：%s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method == http.MethodPost {
			var input map[string]string
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			_, _ = io.WriteString(w, strings.ReplaceAll(registrationResponse, "{{vid}}", input["vid"]))
			return
		}
		if !strings.HasPrefix(r.URL.Query().Get("vid"), "vid-") || r.URL.Query().Get("job_id") != "" || r.URL.Query().Get("byteplus_account_id") != "123" {
			t.Error("未知登记结果必须按原 VID 和账号查询")
		}
		provider.mu.Lock()
		response := provider.queryResponse
		provider.mu.Unlock()
		_, _ = io.WriteString(w, strings.ReplaceAll(response, "{{vid}}", r.URL.Query().Get("vid")))
	}))
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	provider.client = tiktokmedia.NewClient("key-owner", "test-secret", &http.Client{Transport: registrationLocalTransport{target: target}})
	return provider
}

// TestRegistrationUnknownResponseRequiresOfficialQuery 验证缺字段后的真实查询门禁，以及明确拒绝和错误身份不会推进发布。
func TestRegistrationUnknownResponseRequiresOfficialQuery(t *testing.T) {
	for _, test := range []struct {
		name, registration, query, status, code, dramaStatus string
		queryExpected                                        bool
	}{
		{"缺字段后查询确认成功", `{"data":{}}`, `{"data":{"upload_status":2,"vid":"vid-1"}}`, operations.PublicationStatusUnderReview, "", "publishing", true},
		{"非法响应后查询确认成功", `{"data":{"result_type":"invalid"}}`, `{"data":{"upload_status":2,"vid":"vid-1"}}`, operations.PublicationStatusUnderReview, "", "publishing", true},
		{"缺字段后查询明确失败", `{"data":{}}`, `{"data":{"upload_status":3,"vid":"vid-1"}}`, operations.PublicationStatusFailed, "video_registration_rejected", "draft", true},
		{"查询其他视频不得放行", `{"data":{}}`, `{"data":{"upload_status":2,"vid":"other-vid"}}`, operations.PublicationStatusFailed, "video_identity_mismatch", "publishing", true},
		{"明确登记拒绝不查询", `{"data":{"result_type":3}}`, `{"data":{"upload_status":2,"vid":"vid-1"}}`, operations.PublicationStatusFailed, "video_registration_rejected", "draft", false},
		{"仅VID受理仍须查询成功", `{"data":{"byteplus_vid":"vid-1"}}`, `{"data":{"upload_status":2,"vid":"vid-1"}}`, operations.PublicationStatusUnderReview, "", "publishing", true},
		{"仅VID受理后查询失败不能转码", `{"data":{"byteplus_vid":"vid-1"}}`, `{"data":{"upload_status":3,"vid":"vid-1"}}`, operations.PublicationStatusFailed, "video_registration_rejected", "draft", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			provider := newRegistrationHTTPProvider(t, test.registration, test.query)
			service, store, id := publicationFixture(t, provider)
			original, err := submitPreparedPublication(t, service, ctx, id, operations.TikTokPublicationInput{})
			if err != nil {
				t.Fatal(err)
			}
			waitForPublicationStatus(t, service, id, test.status)
			result, err := service.FindTikTokPublication(ctx, id)
			if err != nil || result.ID != original.ID || result.ErrorCode != test.code {
				t.Fatalf("未保留原任务或错误分类不正确：%+v %v", result, err)
			}
			drama, err := store.FindDrama(ctx, id)
			if err != nil || drama.PublishStatus != "ready" || drama.Published != nil {
				t.Fatalf("编辑锁或线上状态错误：%+v %v", drama, err)
			}
			calls := provider.callSnapshot()
			if strings.Count(strings.Join(calls, ","), "video-register") != 1 || slices.Contains(calls, "video-query") != test.queryExpected {
				t.Fatalf("登记被重放或查询门禁错误：%v", calls)
			}
			if test.status == operations.PublicationStatusFailed && slices.Contains(calls, "transcode-start") {
				t.Fatalf("未确认成功仍进入转码：%v", calls)
			}
		})
	}
}

// TestRegistrationRecoveryAcrossElevenEpisodes 验证实际十一集规模及未知响应恢复，不能跨集耗尽技术重试额度。
func TestRegistrationRecoveryAcrossElevenEpisodes(t *testing.T) {
	for _, response := range []string{`{"data":{"byteplus_vid":"{{vid}}"}}`, `{"data":{}}`} {
		t.Run("十一集逐一确认", func(t *testing.T) {
			ctx := context.Background()
			provider := newRegistrationHTTPProvider(t, response, `{"data":{"upload_status":2,"vid":"{{vid}}"}}`)
			service, store, id := publicationFixture(t, provider)
			drama, err := service.FindDrama(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			input := operations.DramaInput{Title: drama.Title, Description: drama.Description, CoverURL: drama.CoverURL, Language: drama.Language, Status: drama.Status, TagIDs: drama.TagIDs}
			for i := 1; i <= 11; i++ {
				episode := operations.EpisodeInput{Number: i, Title: fmt.Sprintf("Episode %d", i)}
				if i == 1 {
					episode.ID = drama.Episodes[0].ID
				}
				input.Episodes = append(input.Episodes, episode)
			}
			drama, err = service.UpdateDrama(ctx, id, input)
			if err != nil {
				t.Fatal(err)
			}
			for i, episode := range drama.Episodes {
				previousVID := episode.VID
				episode.VID, episode.MediaStatus = fmt.Sprintf("vid-%d", i+1), "ready"
				if err := store.SaveEpisodeMedia(ctx, episode, *drama, previousVID, ""); err != nil {
					t.Fatal(err)
				}
			}
			original, err := submitPreparedPublication(t, service, ctx, id, operations.TikTokPublicationInput{})
			if err != nil {
				t.Fatal(err)
			}
			waitForPublicationStatus(t, service, id, operations.PublicationStatusUnderReview)
			result, err := service.FindTikTokPublication(ctx, id)
			if err != nil || result.ID != original.ID || result.Attempts != 0 || len(result.Videos) != 11 {
				t.Fatalf("多集恢复未完成或故障次数跨集累计：%+v %v", result, err)
			}
			for _, video := range result.Videos {
				if video.Status != "ready" {
					t.Fatal("存在未确认视频仍进入送审")
				}
			}
			calls := provider.callSnapshot()
			if strings.Count(strings.Join(calls, ","), "video-register") != 11 || strings.Count(strings.Join(calls, ","), "video-query") != 11 {
				t.Fatalf("多集发生重放或跳过查询：%v", calls)
			}
		})
	}
}

// TestRegistrationQueryRetryPreservesCheckpoint 验证查询耗尽及人工重试保持原任务、编辑锁和查询检查点。
func TestRegistrationQueryRetryPreservesCheckpoint(t *testing.T) {
	ctx := context.Background()
	provider := newRegistrationHTTPProvider(t, `{"data":{}}`, `{"data":{}}`)
	service, store, id := publicationFixture(t, provider)
	original, err := submitPreparedPublication(t, service, ctx, id, operations.TikTokPublicationInput{})
	if err != nil {
		t.Fatal(err)
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusFailed)
	failed, err := service.FindTikTokPublication(ctx, id)
	if err != nil || failed.ID != original.ID || failed.ErrorCode != "invalid_video_response" || failed.Videos[0].Status != "processing" || failed.Videos[0].JobID != "" {
		t.Fatalf("响应异常未保留查询检查点：%+v %v", failed, err)
	}
	drama, err := store.FindDrama(ctx, id)
	if err != nil || drama.PublishStatus != "ready" || !strings.Contains(failed.ErrorMessage, "查询原视频状态") {
		t.Fatalf("响应异常错误解除编辑锁或缺少提示：%+v %v", drama, err)
	}
	provider.mu.Lock()
	provider.queryResponse = `{"data":{"upload_status":2,"vid":"vid-1"}}`
	provider.mu.Unlock()
	retried, err := service.RetryTikTokPublication(ctx, id)
	if err != nil || retried.ID != original.ID {
		t.Fatalf("人工重试意外重建任务：%+v %v", retried, err)
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusUnderReview)
	if calls := provider.callSnapshot(); strings.Count(strings.Join(calls, ","), "video-register") != 1 {
		t.Fatalf("人工重试重复登记：%v", calls)
	}
}
