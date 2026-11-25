package consumer_test

import (
	"testing"

	domain "drama-server/internal/biz/consumer"
)

// TestFreezeContentOwnsMutableContent 验证渠道快照不与编辑内容共享标签和剧集切片。
func TestFreezeContentOwnsMutableContent(t *testing.T) {
	draft := domain.Drama{
		ID: "123", Title: "连载短剧", TagIDs: []string{"001"},
		Episodes: []domain.Episode{{ID: "456", Number: 1, PlatformEpisodeID: "tk-episode"}},
	}
	frozen := domain.FreezeContent(draft)
	digest := domain.ContentDigest(frozen)
	draft.TagIDs[0] = "002"
	draft.Episodes[0].Number = 2
	if frozen.TagIDs[0] != "001" || frozen.Episodes[0].Number != 1 {
		t.Fatalf("编辑内容变更影响了渠道快照: %+v", frozen)
	}
	if frozen.Episodes[0].PlatformEpisodeID != "" || draft.Episodes[0].PlatformEpisodeID != "tk-episode" {
		t.Fatal("渠道分集标识未从快照清除，或清除时影响了编辑内容")
	}
	if domain.ContentDigest(frozen) != digest || domain.ContentDigest(draft) == digest {
		t.Fatal("内容摘要未随实际内容独立变化")
	}
}

// TestFreezeContentKeepsEmptyTags 验证复制空标签时保留原始空数组，避免无内容改动却产生不同摘要。
func TestFreezeContentKeepsEmptyTags(t *testing.T) {
	for _, tags := range [][]string{nil, {}} {
		draft := domain.Drama{TagIDs: tags}
		frozen := domain.FreezeContent(draft)
		if (frozen.TagIDs == nil) != (draft.TagIDs == nil) {
			t.Fatal("冻结快照改变了空标签表示")
		}
	}
}

// TestOfficialContentDigestIgnoresLocalMedia 复现仅补齐首帧图便被误判为新版本。
func TestOfficialContentDigestIgnoresLocalMedia(t *testing.T) {
	d := domain.Drama{Title: "Original", Description: "Synopsis", CoverURL: "https://fixture/cover", TagIDs: []string{"002", "001"}, Episodes: []domain.Episode{{ID: "100", Number: 1, Title: "Episode", VID: "vid"}}}
	frozen := domain.FreezeContent(d)
	d.Episodes[0].PosterURL = "https://fixture/poster"
	d.Episodes[0].DurationSeconds = 100
	d.Episodes[0].MediaStatus = "ready"
	d.Episodes[0].Description = "Local description"
	d.CoverMID = "local-resource"
	d.TagIDs = []string{"001", "002"}
	d.Description += "\n"
	d.Episodes[0].Title += " "
	if domain.OfficialContentDigest(d) != domain.OfficialContentDigest(frozen) {
		t.Fatal("本地媒体元数据触发官方新版本")
	}
	if domain.ContentDigest(d) == domain.ContentDigest(frozen) {
		t.Fatal("直连完整内容摘要丢失媒体变化")
	}
	withoutTags := frozen
	withoutTags.TagIDs = nil
	emptyTags := withoutTags
	emptyTags.TagIDs = []string{}
	if domain.OfficialContentDigest(withoutTags) != domain.OfficialContentDigest(emptyTags) {
		t.Fatal("空标签的存储格式触发官方新版本")
	}
	d.Description = "Changed synopsis"
	if domain.OfficialContentDigest(d) == domain.OfficialContentDigest(frozen) {
		t.Fatal("官方实际参数变化未识别")
	}
}
