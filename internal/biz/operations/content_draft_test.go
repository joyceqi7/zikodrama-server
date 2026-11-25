package operations

import (
	"testing"

	domain "drama-server/internal/biz/consumer"
)

// TestDraftSavingDoesNotRelaxPublicationMediaReadiness 验证草稿放宽后送审仍要求全部媒资就绪。
func TestDraftSavingDoesNotRelaxPublicationMediaReadiness(t *testing.T) {
	t.Parallel()
	for _, item := range []struct {
		name     string
		episodes []domain.Episode
		ready    bool
	}{
		{"空分集草稿不能送审", nil, false},
		{"已登记但未上传的分集不能送审", []domain.Episode{{ID: "pending", MediaStatus: "pending"}}, false},
		{"部分上传成功仍不能送审", []domain.Episode{{ID: "ready", VID: "vid-ready", MediaStatus: "ready"}, {ID: "pending", MediaStatus: "pending"}}, false},
		{"全部媒资就绪且有简介才允许送审", []domain.Episode{{ID: "ready", VID: "vid-ready", MediaStatus: "ready"}}, true},
	} {
		t.Run(item.name, func(t *testing.T) {
			if got := dramaPublicationReady(domain.Drama{Description: "短剧简介", CoverURL: "https://example.test/cover.png", TagIDs: []string{"1"}, Episodes: item.episodes}); got != item.ready {
				t.Fatalf("送审媒资门槛不符合预期：got=%v want=%v", got, item.ready)
			}
		})
	}
	if dramaPublicationReady(domain.Drama{Description: " ", CoverURL: "https://example.test/cover.png", TagIDs: []string{"1"}, Episodes: []domain.Episode{{VID: "vid-ready", MediaStatus: "ready"}}}) {
		t.Fatal("空简介不能生成必然被平台拒绝的同步任务")
	}
}

// TestCanEditRejectedAlbumPublication 仅把平台明确拒绝且没有待确认写入的未送审任务交给内容编辑恢复。
func TestCanEditRejectedAlbumPublication(t *testing.T) {
	base := TikTokPublication{SubjectID: "1", Status: PublicationStatusFailed, Stage: PublicationStageUpdateAlbum, ErrorCode: "api_rejected"}
	if !CanEditRejectedAlbumPublication(base) {
		t.Fatal("明确拒绝的 Album 草稿应允许修正内容")
	}
	for _, item := range []struct {
		name   string
		change func(*TikTokPublication)
	}{
		{"官方写入待核对", func(p *TikTokPublication) { p.ExternalWriteStarted = true }},
		{"仍持有租约", func(p *TikTokPublication) { p.LeaseOwner = "worker" }},
		{"已经送审", func(p *TikTokPublication) { p.ReviewID = "review" }},
		{"错误分类不同", func(p *TikTokPublication) { p.ErrorCode = "external_write_uncertain" }},
		{"阶段不同", func(p *TikTokPublication) { p.Stage = PublicationStageSubmitReview }},
	} {
		t.Run(item.name, func(t *testing.T) {
			p := base
			item.change(&p)
			if CanEditRejectedAlbumPublication(p) {
				t.Fatal("不可确认安全性的任务被放行编辑")
			}
		})
	}
}
