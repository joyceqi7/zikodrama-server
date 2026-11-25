package consumer

import (
	"bytes"
	"encoding/base64"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/visitor"
)

func TestConsumerInputRejectedBeforeBusiness(t *testing.T) {
	h := &Handler{}
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	for _, test := range []struct {
		name, body, query string
		handle            visitorHandler
	}{
		{name: "缺少播放标识", body: `{}`, handle: h.resolvePlay},
		{name: "批量播放缺少剧集", body: `{"dramaId":"1","episodeIds":[]}`, handle: h.resolvePlayBatch},
		{name: "批量播放包含重复剧集", body: `{"dramaId":"1","episodeIds":["2","2"]}`, handle: h.resolvePlayBatch},
		{name: "未完整观看不能领取", body: `{"claimId":"claim_not_ended_000001","offerId":"offer","completedAdUnitId":"rewarded","isEnded":false}`, handle: h.claimReward},
		{name: "领取幂等键太短", body: `{"claimId":"short","completedAdUnitId":"rewarded","isEnded":true}`, handle: h.claimReward},
		{name: "领取缺广告位", body: `{"claimId":"claim_complete_000001","isEnded":true}`, handle: h.claimReward},
		{name: "非法发现状态", query: "?status=archived", handle: h.discovery},
		{name: "非法发现游标", query: "?cursor=not-a-cursor", handle: h.discovery},
		{name: "负数发现游标", query: "?cursor=" + base64.RawURLEncoding.EncodeToString([]byte("v1:-1")), handle: h.discovery},
		{name: "发现页大小越界", query: "?limit=51", handle: h.discovery},
		{name: "发现查询过长", query: "?query=" + strings.Repeat("a", 201), handle: h.discovery},
		{name: "短剧路径为空", handle: h.drama},
	} {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			r := httptest.NewRequest(http.MethodPost, "/"+test.query, strings.NewReader(test.body))
			w := httptest.NewRecorder()
			test.handle(w, r, visitor.Context{VisitorID: "5021"})
			want := http.StatusBadRequest
			if strings.Contains(test.body, `"claimId"`) {
				want = http.StatusConflict
			}
			if w.Code != want || !strings.Contains(logs.String(), `"error":`) {
				t.Fatalf("入口应拒绝并记录错误：%d %s %s", w.Code, w.Body.String(), logs.String())
			}
		})
	}
	t.Run("启动缺应用编码", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"tiktokAppId":" "}`))
		w := httptest.NewRecorder()
		h.bootstrap(w, r)
		if w.Code != 400 {
			t.Fatalf("启动入口应拒绝缺失应用编码：%d", w.Code)
		}
	})
}

func TestDiscoveryNormalizationAtEntry(t *testing.T) {
	input, err := normalizeDiscoveryQuery(domain.DiscoveryQuery{Query: " keyword ", Tag: " Thriller ", Status: " COMPLETED "}, base64.RawURLEncoding.EncodeToString([]byte("v1:20")))
	if err != nil || input.Query != "keyword" || input.Tag != "Thriller" || input.Status != "completed" || input.Limit != 20 || input.Offset != 20 {
		t.Fatalf("入口分页参数规范化错误：%+v %v", input, err)
	}
}
