package consumer_test

import (
	"encoding/json"
	"testing"

	domain "drama-server/internal/biz/consumer"
)

// TestConsumerSummaryKeepsPublicationVersionsInternal 验证发布版本留在运营域，已有 Mini 继续消费当前展示合同。
func TestConsumerSummaryKeepsPublicationVersionsInternal(t *testing.T) {
	drama := domain.Drama{
		ID:    "9007199254740993",
		Title: "当前上架内容", Episodes: []domain.Episode{{ID: "456", Number: 1}},
	}
	raw, err := json.Marshal(drama.Summary())
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"contentVersion", "contentVersionId", "snapshot", "publishStatus"} {
		if _, ok := data[field]; ok {
			t.Fatalf("消费者摘要泄露发布管理字段 %s", field)
		}
	}
	if string(data["id"]) != `"9007199254740993"` || string(data["title"]) != `"当前上架内容"` || string(data["firstEpisodeId"]) != `"456"` {
		t.Fatalf("当前内容合同改变: %s", raw)
	}
}
