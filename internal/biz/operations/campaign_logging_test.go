package operations_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/campaign"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"drama-server/internal/testsupport/memory"
)

// TestCampaignLinkLogsInternalAndPlatformApplicationIDs 验证官方链接日志区分内部应用主键和 TikTok App ID，失败仍不生成活动。
func TestCampaignLinkLogsInternalAndPlatformApplicationIDs(t *testing.T) {
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	for _, failure := range []error{nil, errors.New("Minis Link connection refused")} {
		name := "成功"
		if failure != nil {
			name = "平台失败"
		}
		t.Run(name, func(t *testing.T) {
			var logs bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			store := memory.NewStore(memory.Seed{PublishedOwnerID: "101", Applications: []application.Application{{ID: "101", Name: "Logging", TikTokAppID: "7637420375425239060", ClientKey: "client", Status: application.StatusActive}}, Dramas: []domain.Drama{{ID: "201", Title: "Drama", PublishStatus: "published", Episodes: []domain.Episode{{ID: "301", DramaID: "201", Number: 1}}}}})
			service := operations.NewService(memory.OperationsRepositories(store), nil)
			provider := &campaignLinkStub{failure: failure}
			if err := service.ConfigureTikTokPublications(func(application.Application) operations.TikTokMediaProvider { return provider }, operations.PublicationJobConfig{}); err != nil {
				t.Fatal(err)
			}
			ctx := application.WithContext(context.Background(), "101")
			item, err := service.CreateCampaign(ctx, operations.CampaignInput{Name: "Log test", DramaID: "201", EpisodeID: "301", Status: campaign.StatusActive})
			if failure == nil {
				if err != nil || item.ApplicationID != "101" || item.LandingURL == "" {
					t.Fatalf("创建结果改变: %v", err)
				}
			} else if !errors.Is(err, operations.ErrCampaignLinkUnavailable) {
				t.Fatalf("平台失败分类改变: %v", err)
			}
			raw := logs.String()
			decoder := json.NewDecoder(&logs)
			found := false
			for decoder.More() {
				var entry map[string]any
				if err := decoder.Decode(&entry); err != nil {
					t.Fatal(err)
				}
				if entry["msg"] == "official Minis link generated" || entry["msg"] == "generate minis link failed" {
					found = true
					if entry["application_id"] != "101" || entry["tiktok_app_id"] != "7637420375425239060" || entry["drama_id"] != "201" {
						t.Fatalf("应用标识混淆: %v", entry)
					}
					if failure != nil && entry["error"] != failure.Error() {
						t.Fatal("平台实际错误丢失")
					}
				}
			}
			if !found || strings.Contains(raw, "https://www.tiktok.com/minis/") {
				t.Fatal("缺少关联字段或泄漏链接")
			}
			if failure != nil {
				items, listErr := service.Campaigns(ctx)
				if listErr != nil || len(items) != 0 {
					t.Fatal("平台失败仍写入活动")
				}
			}
		})
	}
}
