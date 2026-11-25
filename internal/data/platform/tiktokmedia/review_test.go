package tiktokmedia

import (
	"context"
	"drama-server/internal/biz/operations"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReviewEndpointsPreserveIdentifiersAndReadItemStatus(t *testing.T) {
	const album = "7637420375425239060"
	const review = "7637437361307027000"
	var requests []recordedRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/oauth/token/" {
			_, _ = io.WriteString(w, `{"access_token":"local-token","expires_in":7200}`)
			return
		}
		body, _ := io.ReadAll(r.Body)
		requests = append(requests, recordedRequest{Path: r.URL.Path, Body: body})
		if strings.HasSuffix(r.URL.Path, "/query/") {
			_, _ = io.WriteString(w, `{"data":{"album_version_list":[{"version":2,"review_status":3,"album_info":{"review_result":{"overall_review_status":6}},"episode_info_list":[{"episode_id":7637437361307027476,"review_result":{"overall_review_status":7}}]}]}}`)
			return
		}
		_, _ = io.WriteString(w, `{"BaseResp":{"status_code":0},"Error":{"code":"ok"}}`)
	}))
	defer server.Close()
	client := NewClient("local-client", "local-secret", server.Client())
	client.baseURL = server.URL
	ctx := context.Background()
	if err := client.AppealReview(ctx, album, review, operations.TikTokReviewAppealInput{ItemType: 2, ItemID: "7637437361307027476", Reason: "review", AttachmentURLs: []string{"https://assets.example/evidence"}}); err != nil {
		t.Fatal(err)
	}
	if err := client.WithdrawReview(ctx, album, 2); err != nil {
		t.Fatal(err)
	}
	observation, err := client.QueryAlbum(ctx, album, 2)
	if err != nil {
		t.Fatal(err)
	}
	if observation.ReviewStatus != 3 || observation.ReviewItems[album] != 6 || observation.ReviewItems["7637437361307027476"] != 7 {
		t.Fatalf("item status lost: %+v", observation)
	}
	if requests[0].Path != "/v2/sg/shortdrama/album/review/appeal/" || !strings.Contains(string(requests[0].Body), `"album_id":`+album) || !strings.Contains(string(requests[0].Body), `"review_id":`+review) || !strings.Contains(string(requests[0].Body), `"item_id":"7637437361307027476"`) {
		t.Fatalf("wrong appeal contract: %s", requests[0].Body)
	}
	if requests[1].Path != "/v2/sg/shortdrama/album/review/withdraw/" || string(requests[1].Body) != `{"album_id":7637420375425239060,"version":2}` {
		t.Fatalf("wrong withdraw contract: %+v", requests[1])
	}
}

// TestDuplicateAppealClassification 验证仅申诉接口识别重复提交，并保留脱敏的官方诊断信息。
func TestDuplicateAppealClassification(t *testing.T) {
	for _, response := range []string{
		`{"error":{"code":22003,"message":"This episode has already been appealed; duplicate appeals are not allowed","log_id":"appeal-log"}}`,
		`{"BaseResp":{"StatusCode":22003,"StatusMessage":"This episode has already been appealed; duplicate appeals are not allowed"},"log_id":"appeal-log"}`,
	} {
		t.Run(response, func(t *testing.T) {
			logs := capturePlatformDiagnostics(t)
			client := contractClient(t, func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, response) })
			err := client.AppealReview(context.Background(), "7637420375425239060", "7637437361307027000", operations.TikTokReviewAppealInput{ItemType: 2, ItemID: "7637437361307027476", Reason: "private-appeal-reason", AttachmentURLs: []string{"https://assets.example/private-proof"}})
			assertPlatformFailureUnchanged(t, err, "appeal_already_submitted", false, false)
			record := onePlatformDiagnostic(t, logs)
			if record["platform_code"] != "22003" || record["platform_request_id"] != "appeal-log" || record["level"] != "WARN" {
				t.Fatalf("重复申诉诊断丢失: %v", record)
			}
			assertDiagnosticSecretsAbsent(t, logs.String()+err.Error(), "test-secret", "test-token", "private-appeal-reason", "private-proof")
			err = client.WithdrawReview(context.Background(), "7637420375425239060", 2)
			assertPlatformFailureUnchanged(t, err, "api_rejected", false, false)
		})
	}
}
