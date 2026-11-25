package operations

import (
	"drama-server/internal/biz/operations"
	"strings"
	"testing"
)

func TestReviewAppealBoundaryValidation(t *testing.T) {
	valid := operations.TikTokReviewAppealInput{ItemType: 2, ItemID: "7637437361307027476", Reason: " reason ", AttachmentURLs: []string{" https://assets.example/proof "}}
	normalized, err := normalizeReviewAppeal(valid)
	if err != nil || normalized.Reason != "reason" || normalized.AttachmentURLs[0] != "https://assets.example/proof" {
		t.Fatalf("normalization failed: %v", err)
	}
	for _, mutate := range []func(*operations.TikTokReviewAppealInput){
		func(p *operations.TikTokReviewAppealInput) { p.ItemType = 3 },
		func(p *operations.TikTokReviewAppealInput) { p.ItemID = "1e20" },
		func(p *operations.TikTokReviewAppealInput) { p.Reason = " " },
		func(p *operations.TikTokReviewAppealInput) { p.Reason = strings.Repeat("长", 5001) },
		func(p *operations.TikTokReviewAppealInput) { p.AttachmentURLs = make([]string, 11) },
		func(p *operations.TikTokReviewAppealInput) {
			p.AttachmentURLs = []string{"http://assets.example/proof"}
		},
		func(p *operations.TikTokReviewAppealInput) {
			p.AttachmentURLs = []string{"https://user:secret@assets.example/proof"}
		},
	} {
		input := valid
		mutate(&input)
		if _, err := normalizeReviewAppeal(input); err == nil {
			t.Fatal("invalid appeal accepted")
		}
	}
}
