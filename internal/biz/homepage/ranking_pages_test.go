package homepage

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"drama-server/internal/biz/consumer"
)

type pagingCatalogStub struct {
	*rankingCatalogStub
	summaryReads [][]string
	memberErr    error
}

// FindVisibleRankingMembers 单独模拟轻量投影故障，与摘要读取故障区分。
func (c *pagingCatalogStub) FindVisibleRankingMembers(ctx context.Context, ids []string) ([]RankingMember, error) {
	if c.memberErr != nil {
		return nil, c.memberErr
	}
	return c.catalogStub.FindVisibleRankingMembers(ctx, ids)
}

// FindVisibleSummaries 记录实际装载的摘要 ID，验证先分页再读取内容。
func (c *pagingCatalogStub) FindVisibleSummaries(ctx context.Context, ids []string) ([]consumer.DramaSummary, error) {
	c.summaryReads = append(c.summaryReads, append([]string{}, ids...))
	return c.catalogStub.FindVisibleSummaries(ctx, ids)
}

// TestHomeRankingPages 验证非空分类、人工顺序、隐藏、失效引用、分类隔离和分页边界。
func TestHomeRankingPages(t *testing.T) {
	ctx := context.Background()
	repo := &repositoryStub{rankings: map[string]Ranking{
		"21": {Items: []RankingItem{{DramaID: "3", Visible: true}, {DramaID: "hidden"}, {DramaID: "unavailable", Visible: true}, {DramaID: "moved", Visible: true}, {DramaID: "1", Visible: true}, {DramaID: "2", Visible: true}}},
		"22": {Items: []RankingItem{{DramaID: "other", Visible: true}}},
		"23": {Items: []RankingItem{{DramaID: "hidden"}}},
	}}
	catalog := &pagingCatalogStub{rankingCatalogStub: &rankingCatalogStub{
		catalogStub: &catalogStub{rows: []consumer.DramaSummary{
			{ID: "1", TagIDs: []string{"21"}}, {ID: "2", TagIDs: []string{"21"}}, {ID: "3", TagIDs: []string{"21"}},
			{ID: "hidden", TagIDs: []string{"21", "23"}}, {ID: "other", TagIDs: []string{"22"}}, {ID: "moved", TagIDs: []string{"22"}},
		}},
		tags: []consumer.TikTokTag{{ID: "21", EnglishName: "Mystery"}, {ID: "22", EnglishName: "Romance"}, {ID: "23", EnglishName: "Hidden"}},
	}}
	svc := NewService(repo, catalog, repo)
	categories, err := svc.HomeRankingCategories(ctx)
	if err != nil || !reflect.DeepEqual(categories, []PublicRankingCategory{{TagID: "21", Name: "Mystery"}, {TagID: "22", Name: "Romance"}}) || len(catalog.summaryReads) != 0 {
		t.Fatalf("分类目录应只返回非空身份，不加载任何摘要：%+v %v %v", categories, catalog.summaryReads, err)
	}
	first, err := svc.HomeRankingPage(ctx, "21", 0, 2)
	if err != nil || len(first.Dramas) != 2 || first.Dramas[0].ID != "3" || first.Dramas[1].ID != "1" || first.NextCursor != consumer.EncodeDiscoveryCursor(2) {
		t.Fatalf("第一页或人工顺序错误：%+v %v", first, err)
	}
	if !reflect.DeepEqual(catalog.summaryReads, [][]string{{"3", "1"}}) {
		t.Fatalf("不应加载页外摘要：%v", catalog.summaryReads)
	}
	last, err := svc.HomeRankingPage(ctx, "21", 2, 2)
	if err != nil || len(last.Dramas) != 1 || last.Dramas[0].ID != "2" || last.NextCursor != "" {
		t.Fatalf("末页错误：%+v %v", last, err)
	}
	other, err := svc.HomeRankingPage(ctx, "22", 0, 20)
	if err != nil || len(other.Dramas) != 1 || other.Dramas[0].ID != "other" {
		t.Fatalf("分类隔离错误：%+v %v", other, err)
	}
	for _, offset := range []int{3, 1000000000} {
		empty, err := svc.HomeRankingPage(ctx, "21", offset, 20)
		if err != nil || empty.Dramas == nil || len(empty.Dramas) != 0 || empty.NextCursor != "" {
			t.Fatalf("旧游标应自然结束：%+v %v", empty, err)
		}
	}
	if _, err := svc.HomeRankingPage(ctx, "999", 0, 20); !errors.Is(err, consumer.ErrNotFound) {
		t.Fatalf("未知分类未拒绝：%v", err)
	}
}

// TestHomeRankingPageFailureLogs 验证最小投影失败传播实际原因，日志不输出剧目内容。
func TestHomeRankingPageFailureLogs(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	defer slog.SetDefault(previous)
	repo := &repositoryStub{rankings: map[string]Ranking{"21": {Items: []RankingItem{{DramaID: "1", Visible: true}}}}}
	catalog := &pagingCatalogStub{rankingCatalogStub: &rankingCatalogStub{catalogStub: &catalogStub{}, tags: []consumer.TikTokTag{{ID: "21"}}}, memberErr: errors.New("ranking projection unavailable")}
	svc := NewService(repo, catalog, repo)
	for _, run := range []func() error{
		func() error { _, err := svc.HomeRankingCategories(context.Background()); return err },
		func() error { _, err := svc.HomeRankingPage(context.Background(), "21", 0, 20); return err },
	} {
		output.Reset()
		if err := run(); !errors.Is(err, catalog.memberErr) || !strings.Contains(output.String(), "load homepage ranking member identities failed") || !strings.Contains(output.String(), catalog.memberErr.Error()) {
			t.Fatalf("故障未记录或未传播：%s %v", output.String(), err)
		}
	}
}
