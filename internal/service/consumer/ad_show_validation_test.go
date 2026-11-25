package consumer

import (
	"drama-server/internal/biz/monetization"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestAdShowValidation 验证通用字段、可选内容目标和新场景编码，无需奖励 Offer 或扩展 JSON。
func TestAdShowValidation(t *testing.T) {
	now := time.Now().UTC()
	base := monetization.AdShowInput{EventID: " generic-event-0001 ", Scene: " home_entry ", AdUnitID: " ad ", OccurredAt: now}
	normalized, err := normalizeAdShow(base)
	if err != nil || normalized.EventID != "generic-event-0001" || normalized.Scene != "home_entry" || normalized.AdUnitID != "ad" {
		t.Fatalf("无内容新场景未正确规范化: %+v %v", normalized, err)
	}
	base.DramaID, base.EpisodeID = "9007199254740993", "18446744073709551615"
	normalized, err = normalizeAdShow(base)
	if err != nil || normalized.DramaID != base.DramaID || normalized.EpisodeID != base.EpisodeID {
		t.Fatal("内容标识精度或格式错误", err)
	}
	for _, change := range []func(*monetization.AdShowInput){
		func(in *monetization.AdShowInput) { in.Scene = "bad/scene" },
		func(in *monetization.AdShowInput) { in.Scene = strings.Repeat("a", 65) },
		func(in *monetization.AdShowInput) { in.AdUnitID = " " },
		func(in *monetization.AdShowInput) { in.AdUnitID = strings.Repeat("a", 256) },
		func(in *monetization.AdShowInput) { in.DramaID = "bad" },
		func(in *monetization.AdShowInput) { in.DramaID = "" },
	} {
		invalid := base
		change(&invalid)
		if _, err := normalizeAdShow(invalid); err == nil {
			t.Fatal("非法展示字段未拒绝")
		}
	}
}

// TestAdShowClientTimeIsOptional 验证新请求可省略手机时间，旧包的合法远期时间可解析但不再参与窗口拒绝。
func TestAdShowClientTimeIsOptional(t *testing.T) {
	for _, body := range []string{
		`{"eventId":"client-time-optional","scene":"home_entry","adUnitId":"ad"}`,
		`{"eventId":"client-time-past","scene":"home_entry","adUnitId":"ad","occurredAt":"2001-01-01T00:00:00Z"}`,
		`{"eventId":"client-time-future","scene":"home_entry","adUnitId":"ad","occurredAt":"2099-01-01T00:00:00Z"}`,
	} {
		var input monetization.AdShowInput
		if err := json.Unmarshal([]byte(body), &input); err != nil {
			t.Fatal(err)
		}
		if _, err := normalizeAdShow(input); err != nil {
			t.Fatalf("手机时间不应拒绝合法旧请求: %v", err)
		}
	}
	var input monetization.AdShowInput
	if err := json.Unmarshal([]byte(`{"occurredAt":"invalid"}`), &input); err == nil {
		t.Fatal("无效时间格式仍应在 JSON 入口拒绝")
	}
}
