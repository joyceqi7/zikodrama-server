package operations_test

import (
	"bytes"
	"context"
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

func campaignLinkFixture() (*memory.Store, *operations.CampaignService, *campaignLinkStub, context.Context) {
	store := memory.NewStore(memory.Seed{PublishedOwnerID: "5001", PublishedTargets: []string{"5002"}, Applications: []application.Application{
		{TikTokAppID: "5001", Name: "应用一", Status: "active"}, {TikTokAppID: "5002", Name: "应用二", Status: "active"}, {TikTokAppID: "5003", Name: "未授权应用", Status: "active"},
	}, Dramas: []domain.Drama{
		{ID: "5008", Title: "测试短剧", PublishStatus: "published", Episodes: []domain.Episode{{ID: "5015", DramaID: "5008", Number: 2}, {ID: "5014", DramaID: "5008", Number: 1}}},
		{ID: "5009", Title: "缺少第一集", PublishStatus: "published", Episodes: []domain.Episode{{ID: "5016", DramaID: "5009", Number: 2}}},
		{ID: "5010", Title: "草稿", PublishStatus: "draft"},
	}})
	provider := &campaignLinkStub{}
	service := operations.NewCampaignService(store, store, store, func(application.Application) operations.TikTokMediaProvider { return provider })
	return store, service, provider, application.WithContext(context.Background(), "5001")
}

func TestEnsureDramaCampaignReusesFirstEpisodeAndIsolatesApplications(t *testing.T) {
	store, service, provider, ctx := campaignLinkFixture()
	first, err := service.EnsureDramaCampaign(ctx, "5008")
	if err != nil || first.EpisodeID != "5014" {
		t.Fatalf("第一集选择错误：%+v %v", first, err)
	}
	again, err := service.EnsureDramaCampaign(ctx, "5008")
	if err != nil || again.ID != first.ID || provider.calls != 1 {
		t.Fatalf("未复用已有链接：%+v %v，调用数 %d", again, err, provider.calls)
	}
	second, err := service.EnsureDramaCampaign(application.WithContext(ctx, "5002"), "5008")
	if err != nil || second.ID == first.ID || second.ApplicationID != "5002" || provider.calls != 2 {
		t.Fatalf("应用未隔离：%+v %v", second, err)
	}
	for _, id := range []string{"5009", "5010"} {
		if _, err := service.EnsureDramaCampaign(ctx, id); !errors.Is(err, operations.ErrInvalidInput) {
			t.Fatalf("无效目标未拒绝：%s %v", id, err)
		}
	}
	if _, err := service.EnsureDramaCampaign(application.WithContext(ctx, "5003"), "5008"); !errors.Is(err, operations.ErrInvalidInput) {
		t.Fatalf("未授权应用未拒绝：%v", err)
	}
	first.Status = campaign.StatusDisabled
	if err := store.SaveCampaign(ctx, first); err != nil {
		t.Fatal(err)
	}
	if _, err := service.EnsureDramaCampaign(ctx, "5008"); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("不应复活停用入口：%v", err)
	}
}

func TestEnsureDramaCampaignReusesExistingManualLink(t *testing.T) {
	_, service, provider, ctx := campaignLinkFixture()
	old, err := service.CreateCampaign(ctx, operations.CampaignInput{Name: "历史投放", DramaID: "5008", EpisodeID: "5014", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	reused, err := service.EnsureDramaCampaign(ctx, "5008")
	if err != nil || reused.ID != old.ID || provider.calls != 1 {
		t.Fatalf("历史链接未复用：%+v %v", reused, err)
	}
}

type failingCampaignRepository struct {
	operations.CampaignRepository
	listErr error
	saveErr error
}

// ListCampaigns 模拟读取失败，正常时返回原仓库按应用隔离的结果。
func (r failingCampaignRepository) ListCampaigns(ctx context.Context) ([]campaign.Campaign, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	return r.CampaignRepository.ListCampaigns(ctx)
}

// SaveCampaign 模拟写入失败，正常时保留原仓库的唯一键约束。
func (r failingCampaignRepository) SaveCampaign(ctx context.Context, item *campaign.Campaign) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	return r.CampaignRepository.SaveCampaign(ctx, item)
}

func TestEnsureDramaCampaignFailureLogsAndDoesNotPersist(t *testing.T) {
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	for _, step := range []string{"list", "save"} {
		t.Run(step, func(t *testing.T) {
			store, _, provider, ctx := campaignLinkFixture()
			cause := errors.New("database unavailable")
			repo := failingCampaignRepository{CampaignRepository: store}
			operation := "list reusable campaigns failed"
			if step == "list" {
				repo.listErr = cause
			} else {
				repo.saveErr = cause
				operation = "save default campaign failed"
			}
			var logs bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			service := operations.NewCampaignService(repo, store, store, func(application.Application) operations.TikTokMediaProvider { return provider })
			if _, err := service.EnsureDramaCampaign(ctx, "5008"); !errors.Is(err, cause) {
				t.Fatalf("错误传播改变：%v", err)
			}
			if !strings.Contains(logs.String(), operation) || !strings.Contains(logs.String(), cause.Error()) || strings.Contains(logs.String(), "https://") {
				t.Fatalf("失败日志不合约：%s", logs.String())
			}
			items, err := store.ListCampaigns(ctx)
			if err != nil || len(items) != 0 {
				t.Fatalf("失败不应落库：%v", err)
			}
		})
	}
}

// racingCampaignRepository 模拟另一个进程先提交相同默认活动，当前写入收到唯一键冲突。
type racingCampaignRepository struct{ operations.CampaignRepository }

// SaveCampaign 先保存胜出记录，再返回冲突，验证用例会重新读取而非覆盖或返回未保存结果。
func (r racingCampaignRepository) SaveCampaign(ctx context.Context, item *campaign.Campaign) error {
	winner := *item
	winner.LandingURL = "https://www.tiktok.com/minis/winner"
	if err := r.CampaignRepository.SaveCampaign(ctx, &winner); err != nil {
		return err
	}
	return operations.ErrConflict
}

func TestEnsureDramaCampaignConcurrentWinnerAndProviderFailure(t *testing.T) {
	store, _, provider, ctx := campaignLinkFixture()
	service := operations.NewCampaignService(racingCampaignRepository{store}, store, store, func(application.Application) operations.TikTokMediaProvider { return provider })
	result, err := service.EnsureDramaCampaign(ctx, "5008")
	if err != nil || result.LandingURL != "https://www.tiktok.com/minis/winner" {
		t.Fatalf("未返回持久化胜出结果：%+v %v", result, err)
	}

	store, service, provider, ctx = campaignLinkFixture()
	provider.failure = errors.New("official link unavailable")
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	var logs bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	if _, err := service.EnsureDramaCampaign(ctx, "5008"); !errors.Is(err, operations.ErrCampaignLinkUnavailable) {
		t.Fatalf("平台错误未映射：%v", err)
	}
	if !strings.Contains(logs.String(), "official link unavailable") || !strings.Contains(logs.String(), "generate default campaign link failed") {
		t.Fatalf("平台故障日志缺失：%s", logs.String())
	}
	items, err := store.ListCampaigns(ctx)
	if err != nil || len(items) != 0 {
		t.Fatalf("平台失败后不应保存活动：%v", err)
	}
	logs.Reset()
	provider.failure = nil
	if _, err := service.EnsureDramaCampaign(ctx, "5008"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), `"level":"ERROR"`) {
		t.Fatalf("成功路径误报错误：%s", logs.String())
	}
}
