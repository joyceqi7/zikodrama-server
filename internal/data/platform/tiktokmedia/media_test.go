package tiktokmedia

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"drama-server/internal/biz/operations"
)

// contractClient 使用本地 HTTP 服务验证官方协议，不访问真实 TikTok 或 BytePlus。
func contractClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		if r.URL.Path == "/v2/oauth/token/" {
			_, _ = io.WriteString(w, `{"access_token":"test-token","expires_in":7200}`)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	client := NewClient("owner", "test-secret", server.Client())
	client.baseURL = server.URL
	return client
}

// TestTranscodeIdempotencyConflictKeepsPlatformDiagnostic 验证 23002 只在转码入口转为可识别分类。
func TestTranscodeIdempotencyConflictKeepsPlatformDiagnostic(t *testing.T) {
	client := contractClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"error":{"code":"23002","message":"Idempotency-Key conflicts with the original transcode request"}}`)
	})
	_, err := client.StartTranscode(context.Background(), operations.TikTokTranscodeInput{
		IdempotencyKey: "transcode_test-0", VIDs: []string{"vid-1"}, SpaceName: "drama", BytePlusAccountID: "123",
	})
	var platformErr *operations.PlatformCallError
	if !errors.As(err, &platformErr) || platformErr.Code != "transcode_idempotency_conflict" || platformErr.Retryable {
		t.Fatalf("转码冲突没有保留明确分类：%v", err)
	}
}

// TestExistingVIDRegistrationUsesOfficialContract 覆盖已存在 VID 登记和异步查询，不补充文档外响应字段。
func TestExistingVIDRegistrationUsesOfficialContract(t *testing.T) {
	client := contractClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/sg/shortdrama/video" {
			t.Errorf("unexpected endpoint %s", r.URL.Path)
		}
		if r.Method == http.MethodPost {
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["vid"] != "vid-1" || body["space_name"] != "drama" || body["byteplus_account_id"] != "123" || body["url"] != "" {
				t.Errorf("existing VID contract: %+v", body)
			}
			_, _ = io.WriteString(w, `{"data":{"result_type":2,"job_id":"job-1"}}`)
		} else {
			if r.URL.Query().Get("job_id") != "job-1" || r.URL.Query().Get("byteplus_account_id") != "123" {
				t.Errorf("query contract: %s", r.URL.RawQuery)
			}
			_, _ = io.WriteString(w, `{"data":{"upload_status":2,"vid":"vid-1"}}`)
		}
	})
	registered, err := client.RegisterVideo(context.Background(), operations.TikTokVideoInput{VID: "vid-1", Title: "Episode 1", SpaceName: "drama", BytePlusAccountID: "123"})
	if err != nil || registered.JobID != "job-1" {
		t.Fatalf("registration: %+v %v", registered, err)
	}
	observed, err := client.QueryVideo(context.Background(), registered.JobID, registered.VID, "123")
	if err != nil || observed.Status != 2 || observed.VID != "vid-1" {
		t.Fatalf("observation: %+v %v", observed, err)
	}
}

// TestTranscodeUsesStableKeyAndDefaultTemplate 覆盖幂等请求头、官方模板以及精确任务查询。
func TestTranscodeUsesStableKeyAndDefaultTemplate(t *testing.T) {
	keys := []string{}
	client := contractClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/sg/shortdrama/video/transcode/batch/" {
			t.Errorf("wrong transcode endpoint")
		}
		if r.Method == http.MethodPost {
			keys = append(keys, r.Header.Get("Idempotency-Key"))
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["template_id"] != "default_0" || body["template_type"] != float64(0) {
				t.Errorf("must use official template: %+v", body)
			}
			_, _ = io.WriteString(w, `{"data":{"task_id":"task-1","task_status":"PROCESSING","total_count":1}}`)
		} else {
			_, _ = io.WriteString(w, `{"data":{"task_id":"task-1","task_status":"SUCCESS","total_count":1,"success_count":1,"success_list":[{"vid":"vid-1"}],"failed_count":0,"failed_list":[],"pending_count":0,"pending_list":[]}}`)
		}
	})
	for range 2 {
		id, err := client.StartTranscode(context.Background(), operations.TikTokTranscodeInput{IdempotencyKey: "publication-1-batch-0", VIDs: []string{"vid-1"}, SpaceName: "drama", BytePlusAccountID: "123"})
		if err != nil || id != "task-1" {
			t.Fatalf("transcode: %s %v", id, err)
		}
	}
	if !reflect.DeepEqual(keys, []string{"publication-1-batch-0", "publication-1-batch-0"}) {
		t.Fatalf("retry changed key: %v", keys)
	}
	result, err := client.QueryTranscode(context.Background(), "task-1", "123")
	if err != nil || !reflect.DeepEqual(result.SuccessVIDs, []string{"vid-1"}) {
		t.Fatalf("transcode observation: %+v %v", result, err)
	}
}

// TestOfficialResponsesCannotSubstituteIdentities 拒绝长度正确但对象身份错误的官方响应。
func TestOfficialResponsesCannotSubstituteIdentities(t *testing.T) {
	for _, test := range []struct {
		name, response string
		call           func(*Client) error
	}{
		{"wrong authorization target", `{"data":{"client_key_result_list":[{"client_key":"other","auth_status":1}]}}`, func(c *Client) error { return c.AuthorizeAlbum(context.Background(), "123", []string{"target"}) }},
		{"wrong transcode task", `{"data":{"task_id":"other","task_status":"SUCCESS","total_count":1,"success_count":1,"success_list":[{"vid":"vid-1"}]}}`, func(c *Client) error { _, err := c.QueryTranscode(context.Background(), "task-1", "123"); return err }},
		{"incomplete transcode results", `{"data":{"task_id":"task-1","task_status":"SUCCESS","total_count":2,"success_count":2,"success_list":[{"vid":"vid-1"}]}}`, func(c *Client) error { _, err := c.QueryTranscode(context.Background(), "task-1", "123"); return err }},
		{"missing confirmed vid", `{"data":{"upload_status":2}}`, func(c *Client) error {
			_, err := c.QueryVideo(context.Background(), "job-1", "vid-1", "123")
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := contractClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, test.response) })
			if err := test.call(client); err == nil {
				t.Fatal("invalid official response was accepted")
			}
		})
	}
}

// TestOfficialMinisLinkAndReviewReasons 验证投放链接与审核意见来自官方结果。
func TestOfficialMinisLinkAndReviewReasons(t *testing.T) {
	client := contractClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/minis/link/generate/" {
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["minis_path"] != "/?campaignCode=launch-1" {
				t.Errorf("wrong route: %+v", body)
			}
			_, _ = io.WriteString(w, `{"data":{"minis_link":"https://www.tiktok.com/minis/official123"},"error":{"code":"OK"}}`)
		} else {
			_, _ = io.WriteString(w, `{"data":{"album_version_list":[{"version":2,"review_status":3,"review_fail_reasons":["Please update the cover"]}]}}`)
		}
	})
	link, err := client.GenerateMinisLink(context.Background(), operations.MinisLinkInput{Path: "/?campaignCode=launch-1"})
	if err != nil || link != "https://www.tiktok.com/minis/official123" {
		t.Fatalf("link: %q %v", link, err)
	}
	result, err := client.QueryAlbum(context.Background(), "123", 2)
	if err != nil || len(result.ReviewFailReasons) != 1 {
		t.Fatalf("review reasons: %+v %v", result, err)
	}
}
