package operations_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"drama-server/internal/biz/operations"
)

// quotaReviewProvider 仅在加急送审时模拟组织额度用尽，普通审核可正常受理。
type quotaReviewProvider struct{ reviewProviderStub }

// SubmitReview 按实际优先级验证额度失败后的恢复，不调用真实官方服务。
func (p *quotaReviewProvider) SubmitReview(ctx context.Context, albumID string, version, priority int) (string, error) {
	if priority == 1 {
		return "", &operations.PlatformCallError{Code: "urgent_review_quota_exceeded"}
	}
	return p.reviewProviderStub.SubmitReview(ctx, albumID, version, priority)
}

// TestReviewQuotaCanRetryWithNormal 验证额度失败不会自动重试，重选普通审核复用冻结版本。
func TestReviewQuotaCanRetryWithNormal(t *testing.T) {
	ctx := context.Background()
	provider := &quotaReviewProvider{}
	service, _, id := publicationFixture(t, provider)
	if _, err := submitPreparedPublication(t, service, ctx, id, operations.TikTokPublicationInput{}); err != nil {
		t.Fatal(err)
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusSynced)
	if _, err := service.SubmitTikTokReview(ctx, id, operations.TikTokPublicationInput{Priority: "urgent"}); err == nil {
		t.Fatal("额度拒绝未直接返回错误")
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusFailed)
	failed, err := service.FindTikTokPublication(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if failed.ErrorCode != "urgent_review_quota_exceeded" || !strings.Contains(failed.ErrorMessage, "普通审核") || failed.ExternalWriteStarted {
		t.Fatalf("unexpected failure: %+v", failed)
	}
	calls := provider.callSnapshot()
	next, err := service.RetryTikTokPublication(ctx, id, operations.TikTokPublicationInput{Priority: "normal"})
	if err != nil {
		t.Fatal(err)
	}
	if next.ID != failed.ID || next.PlatformVersion != failed.PlatformVersion || next.Priority != "normal" {
		t.Fatalf("changed frozen content: %+v", next)
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusUnderReview)
	provider.mu.Lock()
	priority := provider.reviewPriority
	provider.mu.Unlock()
	if priority != 2 || !slices.Equal(provider.callSnapshot(), append(calls, "review")) {
		t.Fatal("retry did not exclusively submit normal review")
	}
	if _, err := service.RetryTikTokPublication(ctx, id, operations.TikTokPublicationInput{Priority: "urgent"}); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("already accepted review was resubmitted: %v", err)
	}
}
