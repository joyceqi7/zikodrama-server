package operations_test

import (
	"context"
	"drama-server/internal/biz/application"
	content "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"drama-server/internal/testsupport/memory"
	"testing"
)

// TestCampaignTargetsUseAuthorizedPublishedSnapshot 验证线上选择器不泄露其他应用或编辑内容。
func TestCampaignTargetsUseAuthorizedPublishedSnapshot(t *testing.T) {
	published := content.Drama{ID: "drama", Title: "Online title", PublishStatus: "published", OwnerApplicationID: "owner", AuthorizedApplicationIDs: []string{"granted"}, PlatformVersion: 2, Episodes: []content.Episode{{ID: "old-episode", Number: 1, Title: "Online episode"}}}
	draft := published
	draft.Title, draft.PublishStatus = "Private draft", "draft"
	draft.Published = &published
	draft.Episodes = []content.Episode{{ID: "new-episode", Number: 1}}
	store := memory.NewStore(memory.Seed{Applications: []application.Application{{ID: "owner"}, {ID: "granted"}, {ID: "unrelated"}}, Dramas: []content.Drama{draft}})
	s := operations.NewService(memory.OperationsRepositories(store), nil)
	for _, appID := range []string{"owner", "granted", "unrelated"} {
		targets, err := s.CampaignTargets(application.WithContext(context.Background(), appID))
		if err != nil {
			t.Fatal(err)
		}
		if appID == "unrelated" {
			if len(targets) != 0 {
				t.Fatal("跨应用目录泄露")
			}
			continue
		}
		if len(targets) != 1 || targets[0].Title != "Online title" || targets[0].Episodes[0].ID != "old-episode" {
			t.Fatalf("投放读取了草稿：%+v", targets)
		}
	}
}
