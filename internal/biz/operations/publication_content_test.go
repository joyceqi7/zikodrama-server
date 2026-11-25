package operations_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
)

// TestPublicationContentSummary 验证首次及连载更新范围固定，公开摘要不包含媒资地址。
func TestPublicationContentSummary(t *testing.T) {
	drama := domain.Drama{CoverURL: "private-cover", Episodes: []domain.Episode{{Number: 1, VID: "private-vid"}, {Number: 2}}}
	first := operations.NewPublicationContentSummary(drama)
	if first.PreviousEpisodeNumbers == nil || len(first.PreviousEpisodeNumbers) != 0 {
		t.Fatal("首次发布必须明确没有旧线上分集")
	}
	drama.Published = &domain.Drama{Episodes: []domain.Episode{{Number: 1}}}
	update := operations.NewPublicationContentSummary(drama)
	drama.Episodes[1].Number = 3
	if !slices.Equal(update.EpisodeNumbers, []int{1, 2}) || !slices.Equal(update.PreviousEpisodeNumbers, []int{1}) {
		t.Fatal("后续修改改变了本次同步范围")
	}
	raw, err := json.Marshal(update)
	if err != nil || strings.Contains(string(raw), "private") {
		t.Fatal("范围摘要包含媒体数据或无法序列化")
	}
}

// TestNextBatchDuringReview 验证审核使用 V1 固定内容，草稿可修改旧集并新增集，确认后形成 V2。
func TestNextBatchDuringReview(t *testing.T) {
	ctx := context.Background()
	provider := &publicationProviderStub{reviewStatus: 1}
	service, store, id := publicationFixture(t, provider)
	first, err := submitPreparedPublication(t, service, ctx, id, operations.TikTokPublicationInput{})
	if err != nil {
		t.Fatal(err)
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusUnderReview)
	before, err := service.FindDrama(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	input := operations.DramaInput{Title: before.Title + " 新版本", Description: before.Description, CoverURL: before.CoverURL, Status: domain.DramaStatusUpdating, Year: before.Year, Language: before.Language, DramaType: before.DramaType, TagIDs: before.TagIDs, Episodes: []operations.EpisodeInput{{ID: before.Episodes[0].ID, Number: 1, Title: "第一集修改"}, {Number: 2, Title: "新增第二集"}}}
	next, err := service.UpdateDrama(ctx, id, input)
	if err != nil || next.PublishStatus != "draft" {
		t.Fatal("审核中编辑被锁定或草稿保存发号", err)
	}
	for _, episode := range next.Episodes {
		old := episode.VID
		episode.VID = "next-vid-" + episode.ID
		episode.MediaStatus = "ready"
		if err := store.SaveEpisodeMedia(ctx, episode, *next, old, ""); err != nil {
			t.Fatal(err)
		}
	}
	ready, err := service.PrepareDrama(ctx, id)
	if err != nil || ready.PublishStatus != "ready" {
		t.Fatal("内容未确认就绪", err)
	}
	p, err := service.FindTikTokPublication(ctx, id)
	if err != nil || p.ID != first.ID || len(p.ContentSnapshot.Episodes) != 1 || p.ContentSnapshot.Title != before.Title || p.ContentSnapshot.Episodes[0].VID != before.Episodes[0].VID {
		t.Fatal("V2 改变审核中的 V1", err)
	}
	provider.mu.Lock()
	provider.reviewStatus = 2
	provider.mu.Unlock()
	manuallyApproveAndPublish(t, service, id)
	after, err := service.FindDrama(ctx, id)
	if err != nil || after.PublishStatus != "ready" || len(after.Episodes) != 2 || !slices.Equal(after.OnlineEpisodeNumbers, []int{1}) {
		t.Fatal("发布 V1 覆盖 V2 编辑状态或集数", err)
	}
	second, err := service.SubmitTikTokPublication(ctx, id, operations.TikTokPublicationInput{})
	if err != nil || len(second.ContentSnapshot.Episodes) != 2 {
		t.Fatal("TK 未同步指定 V2", err)
	}
}

// TestSerialPublicationKeepsOnlineScope 验证上传新集、同步和审核期间线上集数始终来自旧快照。
func TestSerialPublicationKeepsOnlineScope(t *testing.T) {
	ctx := context.Background()
	service, store, id := publicationFixture(t, &publicationProviderStub{})
	if _, err := submitPreparedPublication(t, service, ctx, id, operations.TikTokPublicationInput{}); err != nil {
		t.Fatal(err)
	}
	manuallyApproveAndPublish(t, service, id)
	drama, err := service.FindDrama(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(drama.OnlineEpisodeNumbers, []int{1}) {
		t.Fatal("线上范围缺失")
	}
	draft, err := service.UpdateDrama(ctx, id, operations.DramaInput{Title: drama.Title, Description: drama.Description, CoverURL: drama.CoverURL, Language: drama.Language, Status: domain.DramaStatusUpdating, TagIDs: drama.TagIDs, Episodes: []operations.EpisodeInput{{ID: drama.Episodes[0].ID, Number: 1, Title: drama.Episodes[0].Title}, {Number: 2, Title: "新增第二集"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(draft.OnlineEpisodeNumbers, []int{1}) {
		t.Fatal("新增分集提前进入线上")
	}
	episode := draft.Episodes[1]
	episode.VID, episode.MediaStatus = "vid-2", "ready"
	if err := store.SaveEpisodeMedia(ctx, episode, *draft, "", ""); err != nil {
		t.Fatal(err)
	}
	p, err := submitPreparedPublication(t, service, ctx, id, operations.TikTokPublicationInput{})
	if err != nil {
		t.Fatal(err)
	}
	if p.ContentSummary == nil || !slices.Equal(p.ContentSummary.EpisodeNumbers, []int{1, 2}) || !slices.Equal(p.ContentSummary.PreviousEpisodeNumbers, []int{1}) {
		t.Fatal("同步未固定本次范围")
	}
	waitForPublicationStatus(t, service, id, operations.PublicationStatusSynced)
	during, err := service.FindDrama(ctx, id)
	if err != nil || !slices.Equal(during.OnlineEpisodeNumbers, []int{1}) {
		t.Fatal("同步改变了旧线上内容")
	}
	manuallyApproveAndPublish(t, service, id)
	after, err := service.FindDrama(ctx, id)
	if err != nil || !slices.Equal(after.OnlineEpisodeNumbers, []int{1, 2}) {
		t.Fatal("上线成功后没有更新线上范围")
	}
}
