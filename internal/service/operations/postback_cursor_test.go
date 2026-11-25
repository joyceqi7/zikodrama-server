package operations

import (
	"context"
	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/operations"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// TestPostbackCursorFilterIsolation 拒绝改变筛选及应用后复用游标，防止混淆明细范围。
func TestPostbackCursorFilterIsolation(t *testing.T) {
	query := domain.TikTokPostbackLogQuery{PageSize: 20, ClientVersion: "build-42"}
	item := domain.TikTokPostbackLog{UserID: "777", EventID: "cursor-event-1234567", Event: "ep_play", Outcome: "success", ReceivedAt: time.Now().UTC().Truncate(time.Microsecond)}
	cursor := domain.TikTokPostbackLogCursor("101", query, item)
	for _, raw := range []string{"?clientVersion=build-43&cursor=" + url.QueryEscape(cursor), "?cursor=" + url.QueryEscape(cursor)} {
		r := httptest.NewRequest("GET", "/admin/v1/tiktok-postback-logs"+raw, nil).WithContext(application.WithContext(context.Background(), "101"))
		w := httptest.NewRecorder()
		(&Handler{}).tiktokPostbackLogs(w, r)
		if w.Code != 400 {
			t.Fatalf("筛选变化未拒绝 %d", w.Code)
		}
	}
	r := httptest.NewRequest("GET", "/admin/v1/tiktok-postback-logs?clientVersion=build-42&cursor="+url.QueryEscape(cursor), nil).WithContext(application.WithContext(context.Background(), "102"))
	w := httptest.NewRecorder()
	(&Handler{}).tiktokPostbackLogs(w, r)
	if w.Code != 400 {
		t.Fatalf("跨应用游标未拒绝 %d", w.Code)
	}
}
