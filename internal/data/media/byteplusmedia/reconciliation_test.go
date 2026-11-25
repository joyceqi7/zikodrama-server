package byteplusmedia

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/byteplus-sdk/byteplus-sdk-golang/service/vod/models/business"
	"github.com/byteplus-sdk/byteplus-sdk-golang/service/vod/models/response"

	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"drama-server/internal/testsupport/memory"
)

// TestMediaWorkerSyncsBoundEpisodeVID 贯穿后台扫描、元数据组装与真实适配器，平台请求使用本地替身。
func TestMediaWorkerSyncsBoundEpisodeVID(t *testing.T) {
	const vid = "001234567890123456789"
	store := memory.NewStore(memory.Seed{Dramas: []domain.Drama{{
		ID: "5008", Title: "同步短剧", TagIDs: []string{"21"},
		Episodes: []domain.Episode{{ID: "5014", Number: 1, Title: "第一集", VID: vid, MediaStatus: "unavailable"}},
	}}})
	client := &uploadClientStub{mediaInfoResponse: &response.VodGetMediaInfosResponse{
		Result: &business.VodGetMediaInfosData{MediaInfoList: []*business.VodMediaInfo{{
			BasicInfo: &business.VodMediaBasicInfo{Vid: vid, PublishStatus: "Published", Title: "旧标题"},
		}}},
	}}
	service := operations.NewService(memory.OperationsRepositories(store), &Provider{client: client, spaceName: "test-space"})
	if err := service.ConfigureMediaJobs(operations.MediaJobConfig{ReconcileInterval: time.Hour}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stop := func() {
		cancel()
		waitCtx, stopWaiting := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopWaiting()
		if err := service.WaitMediaWorker(waitCtx); err != nil {
			t.Fatal("等待媒体任务退出失败", err)
		}
	}
	t.Cleanup(stop)
	if err := service.StartMediaWorker(ctx); err != nil {
		t.Fatal(err)
	}
	// 状态写回位于元数据同步之后，读回后停止 Worker，再检查请求以避免并发读取替身字段。
	ready := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		episode, err := store.FindEpisode(context.Background(), "5014")
		if err != nil {
			t.Fatal("读回分集失败", err)
		}
		if episode.MediaStatus == "ready" {
			ready = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	stop()
	if !ready {
		t.Fatal("扫描后未读回已就绪的媒体状态")
	}
	if client.mediaInfoUpdate.Get("Vid") != vid {
		t.Fatal("同步请求未携带分集绑定的完整 VID")
	}
	if client.mediaInfoUpdate.Get("Title") != "旧标题" ||
		!strings.Contains(client.mediaInfoUpdate.Get("Tags"), "tt-tag-21") || client.mediaInfoUpdate.Get("ClassificationId") != "" {
		t.Fatal("同步请求未保留原视频标题、同步标签，或错误写入 VOD 分类")
	}
}
