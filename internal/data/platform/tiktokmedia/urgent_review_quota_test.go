package tiktokmedia

import (
	"context"
	"io"
	"net/http"
	"testing"
)

// TestUrgentReviewQuotaClassification 验证官方额度错误在各响应格式中保持明确失败，并保留脱敏诊断。
func TestUrgentReviewQuotaClassification(t *testing.T) {
	for _, response := range []string{
		`{"error":{"code":"22018","message":"Daily urgent review quota exceeded for this organization","log_id":"quota-log"}}`,
		`{"error":{"code":22018}}`,
		`{"BaseResp":{"StatusCode":22018}}`,
		`{"base_resp":{"status_code":22018}}`,
	} {
		t.Run(response, func(t *testing.T) {
			logs := capturePlatformDiagnostics(t)
			client := contractClient(t, func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, response) })
			_, err := client.SubmitReview(context.Background(), "123", 1, 1)
			assertPlatformFailureUnchanged(t, err, "urgent_review_quota_exceeded", false, false)
			diagnostic := onePlatformDiagnostic(t, logs)
			if diagnostic["level"] != "WARN" || diagnostic["platform_code"] != "22018" {
				t.Fatalf("unexpected diagnostic: %v", diagnostic)
			}
			assertDiagnosticSecretsAbsent(t, logs.String(), "test-token", "test-secret")
		})
	}
}
