package operations_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/operations"
	"drama-server/internal/data/platform/tiktokmedia"
)

// alreadyOnlineTransport 仅在内存中返回平台合同，不向真实 TikTok 发起请求。
type alreadyOnlineTransport struct {
	calls []string
}

// RoundTrip 重现版本选择成功、重复上架被拒及查询仍返回旧版本的发布窗口。
func (transport *alreadyOnlineTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.calls = append(transport.calls, request.URL.Path)
	body := `{"data":{}}`
	switch request.URL.Path {
	case "/v2/oauth/token/":
		body = `{"access_token":"private-publish-token","expires_in":7200}`
	case "/v2/sg/shortdrama/album/status/":
		body = `{"error":{"code":4001,"message":"album is already online","log_id":"confirmed-listing"}}`
	case "/v2/sg/shortdrama/album/query/":
		body = `{"data":{"album_version_list":[{"version":2,"online_version":1,"publish_status":1,"review_status":2}]}}`
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
}

// TestPublishAlreadyOnlineCommitsSelectedSnapshot 验证真实适配器的明确已上架响应保存目标版本且不误报故障。
func TestPublishAlreadyOnlineCommitsSelectedSnapshot(t *testing.T) {
	service, store, _, dramaID, _ := synchronousFixture(t)
	ctx := context.Background()
	p, err := store.FindTikTokPublicationByDramaID(ctx, dramaID)
	if err != nil {
		t.Fatal(err)
	}
	p.ID, p.PlatformVersion, p.PlatformAlbumID = "402", 2, "7637420375425239060"
	p.Status, p.Stage, p.OnlineAt = operations.PublicationStatusApproved, operations.PublicationStageAwaitPublish, nil
	p.ContentSnapshot.Title = "目标版本 V2"
	p.CreatedAt = time.Now().UTC().Add(time.Second)
	store.SeedTikTokPublication(&p)
	transport := &alreadyOnlineTransport{}
	client := tiktokmedia.NewClient("key-owner", "private-publish-secret", &http.Client{Transport: transport})
	if err := service.ConfigureTikTokPublications(func(application.Application) operations.TikTokMediaProvider { return client }, operations.PublicationJobConfig{}); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	result, err := service.PublishTikTokPublication(ctx, dramaID, p.ID)
	if err != nil || result == nil || result.Status != operations.PublicationStatusOnline || result.PlatformVersion != 2 {
		t.Fatalf("明确已上架未完成发布：%+v / %v", result, err)
	}
	drama, err := store.FindDrama(ctx, dramaID)
	if err != nil || drama.Published == nil || drama.Published.PlatformVersion != 2 || drama.Published.Title != "目标版本 V2" {
		t.Fatalf("发布未保存目标内容快照：%+v / %v", drama, err)
	}
	state, err := service.FindTikTokReleaseState(ctx, dramaID)
	if err != nil || state == nil || state.SelectedPublicationID != p.ID || state.Status != operations.PublicationStatusOnline {
		t.Fatalf("已选版本确认事实未保存：%+v / %v", state, err)
	}
	if got := strings.Join(transport.calls, ","); got != "/v2/oauth/token/,/v2/sg/shortdrama/album/online_version/,/v2/sg/shortdrama/album/status/" {
		t.Fatalf("已确认状态仍依赖查询或重复写入：%s", got)
	}
	output := logs.String()
	if !strings.Contains(output, "TikTok album listing already confirmed") || strings.Contains(output, `"level":"ERROR"`) || strings.Contains(output, `"level":"WARN"`) {
		t.Fatalf("幂等发布缺少确认日志或被误报为故障：%s", output)
	}
	for _, secret := range []string{"private-publish-token", "private-publish-secret"} {
		if strings.Contains(output, secret) {
			t.Fatalf("确认日志泄露平台凭据：%s", secret)
		}
	}
}
