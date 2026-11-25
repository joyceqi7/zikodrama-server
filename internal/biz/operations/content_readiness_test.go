package operations_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"drama-server/internal/testsupport/memory"
)

// readinessRepository 在批量就绪边界注入数据库故障，其余行为使用真实内存仓储。
type readinessRepository struct {
	operations.CatalogRepository
	failure error
}

// PrepareDramas 保留原始故障供业务层记录，不写入任何内容。
func (r readinessRepository) PrepareDramas(context.Context, []string, string) error { return r.failure }

// TestBatchContentReadiness 验证全部校验后统一就绪，失败整批不变，重复确认不生成 TK 任务或版本。
func TestBatchContentReadiness(t *testing.T) {
	ctx := context.Background()
	makeDrama := func(id string) consumer.Drama {
		return consumer.Drama{ID: id, Title: "就绪内容", Description: "简介", CoverURL: "https://image.example/cover", PublishStatus: "draft", Episodes: []consumer.Episode{{ID: id + "1", DramaID: id, Number: 1, VID: "source", MediaStatus: "ready"}}}
	}
	first, second := makeDrama("201"), makeDrama("202")
	second.Episodes[0].MediaStatus = "processing"
	store := memory.NewStore(memory.Seed{Dramas: []consumer.Drama{first, second}})
	svc := operations.NewService(memory.OperationsRepositories(store), nil)
	for _, test := range []struct {
		ids     []string
		failure error
	}{
		{[]string{"201", "202"}, operations.ErrPublicationNotReady},
		{[]string{"201", "999"}, consumer.ErrNotFound},
	} {
		if err := svc.PrepareDramas(ctx, test.ids); !errors.Is(err, test.failure) {
			t.Fatal("批量失败错误丢失", err)
		}
		d, _ := store.FindDrama(ctx, "201")
		if d.PublishStatus != "draft" {
			t.Fatal("失败批次部分确认就绪")
		}
	}
	second.Episodes[0].MediaStatus = "ready"
	if err := store.UpdateEpisodeMediaStatus(ctx, second.Episodes[0].ID, second.Episodes[0].VID, "ready"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := svc.PrepareDramas(ctx, []string{"202", "201"}); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"201", "202"} {
			d, err := svc.FindDrama(ctx, id)
			if err != nil || d.PublishStatus != "ready" {
				t.Fatal("批量未确认就绪", err)
			}
			if _, err := store.FindTikTokPublicationByDramaID(ctx, id); !errors.Is(err, consumer.ErrNotFound) {
				t.Fatal("内容确认生成渠道任务", err)
			}
		}
	}
}

// TestBatchContentReadinessFailureLogs 验证预期拒绝和实际故障记录正确操作与错误详情，成功不误报。
func TestBatchContentReadinessFailureLogs(t *testing.T) {
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	for _, failure := range []error{nil, operations.ErrPublicationNotReady, errors.New("UPDATE dramas: connection refused")} {
		var output bytes.Buffer
		slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
		store := memory.NewStore(memory.Seed{})
		repos := memory.OperationsRepositories(store)
		repos.Content.Catalog = readinessRepository{CatalogRepository: store, failure: failure}
		svc := operations.NewService(repos, nil)
		if err := svc.PrepareDramas(context.Background(), []string{"201"}); !errors.Is(err, failure) {
			t.Fatal("错误传播改变", err)
		}
		logs := output.String()
		if failure == nil {
			if logs != "" {
				t.Fatal("成功误报", logs)
			}
			continue
		}
		if !strings.Contains(logs, "prepare drama contents failed") || !strings.Contains(logs, failure.Error()) {
			t.Fatal("诊断信息丢失", logs)
		}
		level := "ERROR"
		if errors.Is(failure, operations.ErrPublicationNotReady) {
			level = "WARN"
		}
		if !strings.Contains(logs, `"level":"`+level+`"`) {
			t.Fatal("日志级别错误", logs)
		}
	}
}
