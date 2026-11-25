package operations

import (
	"reflect"
	"testing"
	"time"

	domain "drama-server/internal/biz/consumer"
)

// TestReusablePublicationEpisodeIDs 验证官方分集身份跟随本地集，且不能跨官方归属或借给新增集。
func TestReusablePublicationEpisodeIDs(t *testing.T) {
	previous := TikTokPublication{SubjectID: "1", PlatformAlbumID: "album", PlatformVersion: 2,
		ContentSnapshot:      &domain.Drama{Episodes: []domain.Episode{{ID: "old-1", Number: 1, VID: "vid-1"}, {ID: "old-2", Number: 2, VID: "vid-2"}}},
		EpisodeIDsBySequence: map[int]string{1: "official-1", 2: "official-2"}}
	draft := domain.Drama{Episodes: []domain.Episode{{ID: "old-2", Number: 1, VID: "replacement-vid"}, {ID: "new", Number: 2, VID: "new-vid"}, {ID: "old-1", Number: 3, VID: "vid-1"}}}
	want := map[int]string{1: "official-2", 3: "official-1"}
	if got := reusablePublicationEpisodeIDs(draft, previous, true); !reflect.DeepEqual(got, want) {
		t.Fatalf("复用分集身份错误：%v", got)
	}
	if got := reusablePublicationEpisodeIDs(draft, previous, false); len(got) != 0 {
		t.Fatal("归属不同仍复用官方标识", got)
	}
	previous.PlatformVersion = 0
	if got := reusablePublicationEpisodeIDs(draft, previous, true); len(got) != 0 {
		t.Fatal("未确认版本仍复用官方标识", got)
	}
}

// TestDefaultReviewPollInterval 验证生产默认配置统一每十分钟扫描审核任务。
func TestDefaultReviewPollInterval(t *testing.T) {
	runner := publicationJobRunner{reviewPollInterval: defaultPublicationReviewPollInterval}
	if got := runner.reviewPollDelay(TikTokPublication{SubjectID: "1"}); got != 10*time.Minute {
		t.Fatalf("审核扫描间隔=%v，期望10分钟", got)
	}
}

// TestPlanPublicationMediaReuse 验证封面、视频登记和转码按媒资身份独立复用。
func TestPlanPublicationMediaReuse(t *testing.T) {
	previous := TikTokPublication{SubjectID: "1",
		PlatformAlbumID: "album-1", PlatformCoverID: "cover-1",
		ContentSnapshot: &domain.Drama{CoverURL: "cover-url"},
		Videos: []PublicationVideo{
			{EpisodeID: "episode-1", VID: "vid-1", Status: "ready"},
			{EpisodeID: "episode-2", VID: "vid-2", Status: "ready"},
		},
		Transcodes: []PublicationTranscode{{
			VIDs: []string{"vid-1", "vid-2"}, CompletedVIDs: []string{"vid-1", "vid-2"}, Ready: true,
		}},
	}
	base := domain.Drama{CoverURL: "cover-url", Episodes: []domain.Episode{
		{ID: "episode-1", VID: "vid-1"}, {ID: "episode-2", VID: "vid-2"},
	}}

	t.Run("metadata only", func(t *testing.T) {
		plan := planPublicationMedia(base, previous, true, true)
		if plan.Stage != PublicationStageUpdateAlbum || plan.PlatformCoverID != "cover-1" {
			t.Fatalf("元数据修改未直接进入 Album 更新：%+v", plan)
		}
		if len(plan.Videos) != 2 || plan.Videos[0].Status != "ready" || len(plan.Transcodes) != 1 || !plan.Transcodes[0].Ready {
			t.Fatalf("元数据修改没有复用媒资事实：%+v", plan)
		}
	})

	t.Run("cover only", func(t *testing.T) {
		changed := base
		changed.CoverURL = "new-cover-url"
		plan := planPublicationMedia(changed, previous, true, true)
		if plan.Stage != PublicationStageUploadCover || plan.PlatformCoverID != "" || !plan.Transcodes[0].Ready {
			t.Fatalf("封面修改没有只安排封面处理：%+v", plan)
		}
	})

	t.Run("one video", func(t *testing.T) {
		changed := base
		changed.Episodes = append([]domain.Episode(nil), base.Episodes...)
		changed.Episodes[1].VID = "vid-2-new"
		plan := planPublicationMedia(changed, previous, true, true)
		if plan.Stage != PublicationStageRegisterVideos || plan.Videos[0].Status != "ready" || plan.Videos[1].Status != "pending" {
			t.Fatalf("单集替换没有只安排变化分集登记：%+v", plan)
		}
		if len(plan.Transcodes) != 2 || !plan.Transcodes[0].Ready || plan.Transcodes[1].Ready || len(plan.Transcodes[1].VIDs) != 1 || plan.Transcodes[1].VIDs[0] != "vid-2-new" {
			t.Fatalf("单集替换没有只安排变化 VID 转码：%+v", plan.Transcodes)
		}
	})
}

// TestSourceVideoMediaPlan 验证源视频策略仍登记新 VID，并保留历史转码事实供以后复用。
func TestSourceVideoMediaPlan(t *testing.T) {
	previous := TikTokPublication{SubjectID: "1", PlatformAlbumID: "album", PlatformCoverID: "cover", ContentSnapshot: &domain.Drama{CoverURL: "cover-url"}, Videos: []PublicationVideo{{EpisodeID: "old", VID: "old-vid", Status: "ready"}}, Transcodes: []PublicationTranscode{{VIDs: []string{"old-vid"}, CompletedVIDs: []string{"old-vid"}, Ready: true}}}
	d := domain.Drama{CoverURL: "cover-url", Episodes: []domain.Episode{{ID: "old", VID: "old-vid"}, {ID: "new", VID: "new-vid"}}}
	plan := planPublicationMedia(d, previous, true, false)
	if plan.Stage != PublicationStageRegisterVideos || len(plan.Transcodes) != 1 || !plan.Transcodes[0].Ready {
		t.Fatal("源视频跳过登记或丢失已有转码事实", plan)
	}
	plan.Videos[1].Status = "ready"
	p := TikTokPublication{SubjectID: "1", SkipTranscode: true, PlatformCoverID: plan.PlatformCoverID, PlatformAlbumID: previous.PlatformAlbumID, Videos: plan.Videos, Transcodes: plan.Transcodes}
	if stage := nextPublicationMediaStage(p); stage != PublicationStageUpdateAlbum {
		t.Fatal("源视频登记完成后仍转码", stage)
	}
}
