package homepage

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/consumer"
)

type repositoryStub struct {
	rankings map[string]Ranking
	config   Carousel
	hot      Carousel
	err      error
}

func (r *repositoryStub) GetCarousel(context.Context) (Carousel, error) {
	return r.config, r.err
}
func (r *repositoryStub) ReplaceCarousel(_ context.Context, input Carousel) (Carousel, error) {
	if input.Revision != r.config.Revision {
		return Carousel{}, ErrConflict
	}
	input.Revision++
	r.config = input
	return input, r.err
}
func (r *repositoryStub) GetHot(context.Context) (Carousel, error) { return r.hot, r.err }
func (r *repositoryStub) ReplaceHot(_ context.Context, input Carousel) (Carousel, error) {
	if input.Revision != r.hot.Revision {
		return Carousel{}, ErrConflict
	}
	input.Revision++
	r.hot = input
	return input, r.err
}

type catalogStub struct {
	rows []consumer.DramaSummary
	err  error
}

// FindVisibleRankingMembers 提供不携带摘要的最小投影，供分类分页用例复用。
func (c *catalogStub) FindVisibleRankingMembers(ctx context.Context, ids []string) ([]RankingMember, error) {
	rows, err := c.FindVisibleSummaries(ctx, ids)
	result := []RankingMember{}
	for _, row := range rows {
		result = append(result, RankingMember{DramaID: row.ID, TagIDs: row.TagIDs})
	}
	return result, err
}

// readTestSections 仅在领域测试中组合三个独立区域，生产服务不提供聚合读取。
func readTestSections(s *Service, ctx context.Context) (Sections, error) {
	carousel, err := s.HomeCarousel(ctx)
	if err != nil {
		return Sections{}, err
	}
	hot, err := s.HomeHot(ctx)
	if err != nil {
		return Sections{}, err
	}
	rankings, err := s.HomeRankings(ctx)
	if err != nil {
		return Sections{}, err
	}
	return Sections{Carousel: carousel, Hot: hot, Rankings: rankings}, nil
}

func (c *catalogStub) FindVisibleSummaries(_ context.Context, ids []string) ([]consumer.DramaSummary, error) {
	items := []consumer.DramaSummary{}
	for _, row := range c.rows {
		for _, id := range ids {
			if row.ID == id {
				items = append(items, row)
			}
		}
	}
	return items, c.err
}
func (c *catalogStub) DiscoverDramas(_ context.Context, input consumer.DiscoveryQuery) (consumer.DiscoveryPage, error) {
	end := input.Offset + input.Limit
	if end > len(c.rows) {
		end = len(c.rows)
	}
	page := consumer.DiscoveryPage{Items: c.rows[input.Offset:end]}
	if end < len(c.rows) {
		page.NextCursor = consumer.EncodeDiscoveryCursor(end)
	}
	return page, c.err
}

// TestManualHot 验证人工顺序、标题、隐藏、下架过滤和空配置不自动补齐。
func TestManualHot(t *testing.T) {
	repo := &repositoryStub{config: Carousel{DramaIDs: []string{"1"}}}
	catalog := &catalogStub{rows: []consumer.DramaSummary{{ID: "1", Title: "原名"}, {ID: "2", Title: "第二部"}, {ID: "3", Title: "第三部"}}}
	svc := NewService(repo, catalog, repo)
	ctx := application.WithContext(context.Background(), "101")
	empty, err := readTestSections(svc, ctx)
	if err != nil || len(empty.Hot) != 0 {
		t.Fatal("未配置热播不应自动补位", err)
	}
	view, err := svc.SaveHot(ctx, Carousel{DramaIDs: []string{"3", "1", "2"}, Items: []CarouselItem{{DramaID: "3", Visible: true, Title: "人工标题"}, {DramaID: "1", Visible: false}, {DramaID: "2", Visible: true}}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := readTestSections(svc, ctx)
	if err != nil || len(got.Hot) != 2 || got.Hot[0].ID != "3" || got.Hot[0].Title != "人工标题" || got.Hot[1].ID != "2" || len(got.Carousel) != 1 {
		t.Fatal("人工配置未生效或串改轮播", err)
	}
	catalog.rows = catalog.rows[:2]
	got, err = readTestSections(svc, ctx)
	if err != nil || len(got.Hot) != 1 || got.Hot[0].ID != "2" {
		t.Fatal("下架未过滤", err)
	}
	if _, err = svc.SaveHot(ctx, Carousel{Revision: 0, DramaIDs: []string{"2"}}); !errors.Is(err, ErrConflict) {
		t.Fatal("旧版本未被拒绝")
	}
	if _, err = svc.SaveHot(ctx, Carousel{Revision: view.Revision, DramaIDs: []string{"999"}}); !errors.Is(err, consumer.ErrNotFound) {
		t.Fatal("不可见新引用未被拒绝")
	}
	if _, err = svc.SaveHot(ctx, Carousel{Revision: view.Revision, DramaIDs: []string{}}); err != nil {
		t.Fatal(err)
	}
	got, err = readTestSections(svc, ctx)
	if err != nil || len(got.Hot) != 0 {
		t.Fatal("清空后不应补位", err)
	}
}

// TestHomeZeroVisitsAndCarouselEditing 验证空热播、失效引用逐项移除及拒绝新加入不可见内容。
func TestHomeZeroVisitsAndCarouselEditing(t *testing.T) {
	repo := &repositoryStub{config: Carousel{Revision: 4, DramaIDs: []string{"old-1", "old-2", "1"}}}
	catalog := &catalogStub{rows: []consumer.DramaSummary{{ID: "1"}, {ID: "2"}, {ID: "3"}}}
	svc := NewService(repo, catalog, repo)
	ctx := context.Background()
	home, err := readTestSections(svc, ctx)
	if err != nil || len(home.Hot) != 0 {
		t.Fatal("空热播错误")
	}
	view, err := svc.SaveCarousel(ctx, Carousel{Revision: 4, DramaIDs: []string{"old-2", "1"}})
	if err != nil || view.Revision != 5 || !reflect.DeepEqual(view.DramaIDs, []string{"old-2", "1"}) {
		t.Fatal("无法逐项移除失效引用")
	}
	if _, err := svc.SaveCarousel(ctx, Carousel{Revision: 5, DramaIDs: []string{"new-hidden"}}); !errors.Is(err, consumer.ErrNotFound) {
		t.Fatal("新加入不可见短剧未被拒绝")
	}
	if _, err := svc.SaveCarousel(ctx, Carousel{Revision: 4, DramaIDs: []string{"1"}}); !errors.Is(err, ErrConflict) {
		t.Fatal("旧版本未被拒绝")
	}
}

// TestHomepageFailureLogs 保留实际依赖错误且不泄漏访问参数，成功路径不得产生错误日志。
func TestHomepageFailureLogs(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	defer slog.SetDefault(previous)
	catalog := &catalogStub{rows: []consumer.DramaSummary{{ID: "1"}}}
	repo := &repositoryStub{config: Carousel{DramaIDs: []string{}}}
	svc := NewService(repo, catalog, repo)
	if _, err := svc.Hot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 {
		t.Fatal("成功路径误报错误")
	}
	repo.err = errors.New("redis connection refused")
	if _, err := svc.SaveHot(context.Background(), Carousel{DramaIDs: []string{"1"}}); !errors.Is(err, repo.err) {
		t.Fatal("热播写入故障未传播")
	}
	if !strings.Contains(output.String(), "load recommendation before replacement failed") || !strings.Contains(output.String(), "redis connection refused") {
		t.Fatal("热播失败日志缺少具体原因")
	}
	output.Reset()
	catalog.err = errors.New("query unavailable")
	if _, err := readTestSections(svc, context.Background()); err == nil {
		t.Fatal("读取故障被吞掉")
	}
	if !strings.Contains(output.String(), "redis connection refused") {
		t.Fatal("轮播读取故障缺少原因")
	}
}

// TestCarouselPresentation 验证自定义标题、隐藏、创建时间与旧版默认值。
func TestCarouselPresentation(t *testing.T) {
	repo := &repositoryStub{config: Carousel{DramaIDs: []string{"1", "2"}}}
	catalog := &catalogStub{rows: []consumer.DramaSummary{{ID: "1", Title: "原名"}, {ID: "2", Title: "第二部"}, {ID: "3", Title: "第三部"}}}
	svc := NewService(repo, catalog, repo)
	before, err := readTestSections(svc, context.Background())
	if err != nil || len(before.Carousel) != 2 {
		t.Fatal("旧配置未默认展示", err)
	}
	input := Carousel{DramaIDs: []string{"1", "2", "3"}, Items: []CarouselItem{{DramaID: "1", Title: "推荐标题", Visible: true}, {DramaID: "2", Visible: false}, {DramaID: "3", Visible: true, CreatedAt: "伪造时间"}}}
	view, err := svc.SaveCarousel(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if view.Items[2].CreatedAt == "" || view.Items[2].CreatedAt == "伪造时间" {
		t.Fatal("新建时间未由服务端生成")
	}
	// 旧版仅重排 ID 的保存不能丢失新版标题与展示状态。
	_, err = svc.SaveCarousel(context.Background(), Carousel{Revision: view.Revision, DramaIDs: []string{"1", "2", "3"}})
	if err != nil {
		t.Fatal(err)
	}
	after, err := readTestSections(svc, context.Background())
	if err != nil || len(after.Carousel) != 2 || after.Carousel[0].Title != "推荐标题" || after.Carousel[1].ID != "3" {
		t.Fatal("展示设置未应用到前台", err)
	}
}

// GetRankings 返回测试仓储分类配置。
func (r *repositoryStub) GetRankings(context.Context) (map[string]Ranking, error) {
	return r.rankings, r.err
}

// ReplaceRanking 模拟分类修订冲突与整体保存。
func (r *repositoryStub) ReplaceRanking(_ context.Context, id string, input Ranking) (Ranking, error) {
	if r.err != nil {
		return Ranking{}, r.err
	}
	if input.Revision != r.rankings[id].Revision {
		return Ranking{}, ErrConflict
	}
	if r.rankings == nil {
		r.rankings = map[string]Ranking{}
	}
	input.Revision++
	r.rankings[id] = input
	return input, nil
}

// ListTikTokTags 使用固定标签身份，为分类用例提供目录。
func (c *catalogStub) ListTikTokTags(context.Context) ([]consumer.TikTokTag, error) {
	return consumer.TikTokTagCatalog(), c.err
}
