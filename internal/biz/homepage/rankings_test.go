package homepage

import (
	"bytes"
	"context"
	"drama-server/internal/biz/consumer"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
)

type rankingCatalogStub struct {
	*catalogStub
	tags  []consumer.TikTokTag
	query consumer.DiscoveryQuery
}

// ListTikTokTags 为翻译更新用例提供当前目录。
func (c *rankingCatalogStub) ListTikTokTags(context.Context) ([]consumer.TikTokTag, error) {
	return c.tags, c.err
}

// DiscoverDramas 记录官方标签过滤，避免以译名作为身份。
func (c *rankingCatalogStub) DiscoverDramas(_ context.Context, q consumer.DiscoveryQuery) (consumer.DiscoveryPage, error) {
	c.query = q
	return consumer.DiscoveryPage{}, c.err
}

// TestCategoryRankingLifecycle 验证顺序、隐藏、实时标签归属、失效清理和翻译变更。
func TestCategoryRankingLifecycle(t *testing.T) {
	ctx := context.Background()
	repo := &repositoryStub{rankings: map[string]Ranking{"21": {Items: []RankingItem{{DramaID: "1", Visible: true}, {DramaID: "2", Visible: true}}}}}
	catalog := &rankingCatalogStub{catalogStub: &catalogStub{rows: []consumer.DramaSummary{{ID: "1", TagIDs: []string{"21"}}, {ID: "2", TagIDs: []string{"21"}}, {ID: "3", TagIDs: []string{"22"}}}}, tags: []consumer.TikTokTag{{ID: "21", OfficialName: "悬疑", EnglishName: "Mystery"}, {ID: "22", EnglishName: "Mystery"}}}
	svc := NewService(repo, catalog, repo)
	view, err := svc.SaveCategoryRanking(ctx, "21", Ranking{Items: []RankingItem{{DramaID: "2", Visible: true}, {DramaID: "1", Visible: false}}})
	if err != nil || view.Revision != 1 {
		t.Fatal("保存失败", err)
	}
	sections, err := readTestSections(svc, ctx)
	if err != nil || len(sections.Rankings) != 1 || sections.Rankings[0].Name != "Mystery" || len(sections.Rankings[0].Dramas) != 1 || sections.Rankings[0].Dramas[0].ID != "2" {
		t.Fatal("前台顺序和隐藏错误", err)
	}
	if _, err = svc.SaveCategoryRanking(ctx, "21", Ranking{Revision: 1, Items: []RankingItem{{DramaID: "3", Visible: true}}}); !errors.Is(err, consumer.ErrNotFound) {
		t.Fatal("其他标签的新短剧未拒绝")
	}
	if _, err = svc.SaveCategoryRanking(ctx, "21", Ranking{Items: []RankingItem{}}); !errors.Is(err, ErrConflict) {
		t.Fatal("旧版本未拒绝")
	}
	if _, err = svc.RankingTargets(ctx, "21", consumer.DiscoveryQuery{Query: "test"}); err != nil || catalog.query.TagID != "21" {
		t.Fatal("候选未使用官方 ID")
	}
	catalog.tags[0].EnglishName = "New Mystery"
	categories, err := svc.RankingCategories(ctx)
	if err != nil || categories[0].Count != 2 || categories[0].Tag.DisplayName() != "New Mystery" {
		t.Fatal("标签数量或翻译未更新", err)
	}
	// 线上标签变更后保留后台引用，消费者立即过滤；运营仍可逐项移除。
	catalog.rows[1].TagIDs = []string{"22"}
	view, err = svc.CategoryRanking(ctx, "21")
	if err != nil || len(view.Items) != 2 || len(view.Dramas) != 1 {
		t.Fatal("失效引用丢失", err)
	}
	sections, err = readTestSections(svc, ctx)
	if err != nil || len(sections.Rankings) != 0 {
		t.Fatal("标签已变更的短剧仍展示", err)
	}
	view, err = svc.SaveCategoryRanking(ctx, "21", Ranking{Revision: 1, Items: []RankingItem{{DramaID: "2", Visible: true}}})
	if err != nil || !reflect.DeepEqual(view.Items, []RankingItem{{DramaID: "2", Visible: true}}) {
		t.Fatal("旧失效引用无法保留", err)
	}
	if _, err = svc.SaveCategoryRanking(ctx, "21", Ranking{Revision: 2, Items: []RankingItem{}}); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.CategoryRanking(ctx, "99999"); !errors.Is(err, consumer.ErrNotFound) {
		t.Fatal("未知标签未拒绝")
	}
}

type rankingWriteFailure struct {
	*repositoryStub
	failure error
}

// ReplaceRanking 模拟独立写入故障，不影响保存前读取。
func (r *rankingWriteFailure) ReplaceRanking(context.Context, string, Ranking) (Ranking, error) {
	return Ranking{}, r.failure
}

// TestCategoryRankingFailureLogs 验证依赖故障日志保留操作和实际原因，成功不误报。
func TestCategoryRankingFailureLogs(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	defer slog.SetDefault(previous)
	ctx := context.Background()
	repo := &rankingWriteFailure{repositoryStub: &repositoryStub{rankings: map[string]Ranking{"21": {Items: []RankingItem{{DramaID: "1", Visible: true}}}}}}
	catalog := &catalogStub{rows: []consumer.DramaSummary{{ID: "1", TagIDs: []string{"21"}}}}
	svc := NewService(repo, catalog, repo)
	if _, err := svc.RankingCategories(ctx); err != nil || output.Len() != 0 {
		t.Fatal("成功路径误报", err)
	}
	for _, tc := range []struct {
		name, operation string
		setup           func()
		run             func() error
	}{
		{"仓储读取", "load category rankings failed", func() { repo.err = errors.New("mysql read refused") }, func() error { _, e := svc.CategoryRanking(ctx, "21"); return e }},
		{"标签目录", "load ranking TikTok tags failed", func() { repo.err = nil; catalog.err = errors.New("tag query failed") }, func() error { _, e := svc.RankingCategories(ctx); return e }},
		{"候选读取", "load category ranking candidates failed", func() { catalog.err = errors.New("candidate query failed") }, func() error { _, e := svc.RankingTargets(ctx, "21", consumer.DiscoveryQuery{Limit: 1}); return e }},
		{"可见性查询", "validate category ranking candidates failed", func() { catalog.err = errors.New("visibility query failed") }, func() error {
			_, e := svc.SaveCategoryRanking(ctx, "21", Ranking{Items: []RankingItem{{DramaID: "1", Visible: true}}})
			return e
		}},
		{"持久保存", "save category ranking failed", func() { catalog.err = nil; repo.failure = errors.New("mysql write refused") }, func() error {
			_, e := svc.SaveCategoryRanking(ctx, "21", Ranking{Items: []RankingItem{{DramaID: "1", Visible: true}}})
			return e
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output.Reset()
			tc.setup()
			err := tc.run()
			if err == nil || !strings.Contains(output.String(), tc.operation) || !strings.Contains(output.String(), err.Error()) {
				t.Fatal("错误未传播或日志缺少操作与原因", output.String())
			}
		})
	}
}
