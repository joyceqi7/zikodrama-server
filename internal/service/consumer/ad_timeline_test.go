package consumer

import (
	"context"
	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/visitor"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const timelineVisible = `"documentVisibility":"visible","nativeVisibility":"visible","prerendering":false`
const timelineRequest = `{"atMs":900,` + timelineVisible + `,"request":{"userInitiated":false,"previousAttemptId":"previous-attempt-01","previousCloseAtMs":100,"lifecycleTruncated":false,"lifecycle":[{"atMs":300,"documentVisibility":"visible","nativeVisibility":"hidden","prerendering":false,"source":"minis.hide"},{"atMs":600,` + timelineVisible + `,"source":"minis.show"}]}}`

func adTimelineMetric(id, kind, stage, detail, timeline string) string {
	return fmt.Sprintf(`{"id":%q,"kind":%q,"ad":{"adType":"rewarded","adUnitId":"slot","scene":"entry_reward","clientVersion":"build","os":"ios","tiktokVersion":"46.1","stage":%q,"showElapsedMs":100%s},"adTimeline":%s}`,
		id, kind, stage, detail, timeline)
}

func sendTimelineMetrics(h *Handler, events ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/metrics", strings.NewReader(`{"events":[`+strings.Join(events, ",")+`]}`))
	r = r.WithContext(application.WithContext(r.Context(), "101"))
	w := httptest.NewRecorder()
	h.collectMetrics(w, r, visitor.Context{VisitorID: "107"})
	return w
}

// TestAdTimelineAcrossBatches 验证不同批次和乱序接收仍使用原调用键，普通指标与诊断日志各自处理。
func TestAdTimelineAcrossBatches(t *testing.T) {
	output := adErrorLogs(t)
	repo := &serverTimeMetricRepository{}
	h := &Handler{config: Config{Analytics: domain.NewAnalyticsService(repo)}}
	result := `{"atMs":1000,` + timelineVisible + `}`
	shown := adTimelineMetric("current-attempt-02", "ad_shown", "show", `,"evidence":"show_promise"`, result)
	closed := adTimelineMetric("previous-attempt-01", "ad_closed", "close", "", `{"atMs":100,`+timelineVisible+`}`)
	request := adTimelineMetric("current-attempt-02", "ad_request", "show", "", timelineRequest)
	failed := adTimelineMetric("current-attempt-03", "ad_failure", "show", `,"reason":"container_unavailable","errorCode":"101150000","subErrorCode":"50008"`, result)
	for _, batch := range [][]string{{shown}, {closed}, {request, request}, {failed}} {
		w := sendTimelineMetrics(h, batch...)
		if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "timeline") {
			t.Fatalf("统计采集失败或响应回显诊断: %d %s", w.Code, w.Body)
		}
	}
	logs := output.String()
	if strings.Count(logs, `"msg":"client ad timeline"`) != 4 || strings.Contains(logs, `"level":"ERROR"`) {
		t.Fatalf("日志缺失、重复或误报: %s", logs)
	}
	for _, want := range []string{`"application_id":"101"`, `"user_id":"107"`, `"event_id":"previous-attempt-01"`,
		`"event_id":"current-attempt-02"`, `"kind":"ad_shown"`, `"kind":"ad_failure"`, `"kind":"ad_closed"`, `"kind":"ad_request"`,
		`"previousAttemptId":"previous-attempt-01"`, `"previousCloseAtMs":100`, `"source":"minis.hide"`, `"source":"minis.show"`, `"subErrorCode":"50008"`} {
		if !strings.Contains(logs, want) {
			t.Fatalf("时序缺少 %s: %s", want, logs)
		}
	}
}

// TestAdTimelineRejectsInvalidInput 验证异常关联和非白名单数据在写入统计及输出时序前被拒绝。
func TestAdTimelineRejectsInvalidInput(t *testing.T) {
	output := adErrorLogs(t)
	repo := &serverTimeMetricRepository{}
	h := &Handler{config: Config{Analytics: domain.NewAnalyticsService(repo)}}
	request := adTimelineMetric("current-attempt-02", "ad_request", "show", "", timelineRequest)
	for _, tc := range []struct{ name, from, to string }{
		{"自关联", "previous-attempt-01", "current-attempt-02"},
		{"事件早于关闭", `"atMs":300`, `"atMs":99`},
		{"事件晚于展示", `"atMs":600`, `"atMs":901`},
		{"宿主事件与状态矛盾", "minis.hide", "minis.show"},
		{"结果携带请求区间", `"kind":"ad_request"`, `"kind":"ad_shown"`},
		{"非白名单字段", `"source":"minis.hide"`, `"source":"minis.hide","raw":"private-value"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output.Reset()
			w := sendTimelineMetrics(h, strings.Replace(request, tc.from, tc.to, 1))
			logs := output.String()
			if w.Code != http.StatusBadRequest || len(repo.events) != 0 ||
				!strings.Contains(logs, "rejected") || strings.Contains(logs, "client ad timeline") || strings.Contains(logs, "private-value") {
				t.Fatalf("拒绝路径不符合预期: %d %s", w.Code, logs)
			}
		})
	}
}

type failingTimelineRepository struct{}

// RecordMetrics 注入存储失败，检查未接收批次不会输出成功采集的时序。
func (failingTimelineRepository) RecordMetrics(context.Context, string, []domain.MetricEvent) error {
	return errors.New("metrics database unavailable")
}

// TestAdTimelineRecordFailure 验证失败传播及实际错误日志；原始阶段可由客户端重试。
func TestAdTimelineRecordFailure(t *testing.T) {
	output := adErrorLogs(t)
	h := &Handler{config: Config{Analytics: domain.NewAnalyticsService(failingTimelineRepository{})}}
	w := sendTimelineMetrics(h, adTimelineMetric("current-attempt-02", "ad_request", "show", "", timelineRequest))
	logs := output.String()
	if w.Code != http.StatusInternalServerError || !strings.Contains(logs, "record consumer metrics failed") ||
		!strings.Contains(logs, "metrics database unavailable") || strings.Contains(logs, "client ad timeline") {
		t.Fatalf("存储失败未按原合同处理: %d %s", w.Code, logs)
	}
}
