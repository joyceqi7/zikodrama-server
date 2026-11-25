package consumer

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConsumerViewPreservesSingleCopyAndProjection(t *testing.T) {
	original := Drama{Title: "主标题", Description: "主简介", Status: DramaStatusUpdating,
		TikTokTags: []TikTokTag{{ID: "21", OfficialName: "悬疑推理"}}, Episodes: []Episode{{ID: "5013", Title: "第一集"}}}
	view := original.ConsumerView()
	if view.Title != original.Title || view.Description != original.Description || view.Status != "ongoing" || len(view.Tags) != 1 || view.Tags[0] != "悬疑推理" || view.TikTokTags != nil {
		t.Fatalf("消费者投影错误：%+v", view)
	}
	view.Episodes[0].Title = "changed"
	if original.Episodes[0].Title != "第一集" {
		t.Fatal("展示投影改写了原始快照")
	}
	data, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "translations") {
		t.Fatal("单份文案仍暴露翻译字典")
	}
}
