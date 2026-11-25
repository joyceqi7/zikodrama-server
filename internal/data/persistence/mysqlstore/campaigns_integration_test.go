package mysqlstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/campaign"
	"drama-server/internal/biz/operations"
	"gorm.io/gorm"
)

// TestCurrentSchemaIntegrationCampaigns 使用隔离库验证投放配置的唯一性、更新隔离和失效归因。
func TestCurrentSchemaIntegrationCampaigns(t *testing.T) {
	f := newIAAFixture(t)
	ctx := application.WithContext(context.Background(), "101")
	original := campaign.Campaign{ID: "1002", ApplicationID: "101", Code: "entry", Name: "原配置", DramaID: "201", EpisodeID: "301", Status: campaign.StatusActive, LandingURL: "https://example.test/original", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := f.store.SaveCampaign(ctx, &original); err != nil {
		t.Fatal(err)
	}
	t.Run("重复编码不能覆盖原配置", func(t *testing.T) {
		duplicate := original
		duplicate.ID, duplicate.EpisodeID, duplicate.LandingURL = "2011", "302", "https://example.test/new"
		if err := f.store.SaveCampaign(ctx, &duplicate); !errors.Is(err, operations.ErrConflict) {
			t.Fatalf("预期编码冲突，实际 %v", err)
		}
		saved, err := f.store.FindCampaign(ctx, original.ID)
		if err != nil || saved.EpisodeID != original.EpisodeID || saved.LandingURL != original.LandingURL {
			t.Fatalf("原配置被覆盖: %+v, %v", saved, err)
		}
	})
	t.Run("按ID更新且允许清空可选字段", func(t *testing.T) {
		updated := original
		updated.Name, updated.ExternalAdID = "新名称", "ad-1"
		if err := f.store.SaveCampaign(ctx, &updated); err != nil {
			t.Fatal(err)
		}
		updated.ExternalAdID = ""
		if err := f.store.SaveCampaign(ctx, &updated); err != nil {
			t.Fatal(err)
		}
		saved, err := f.store.FindCampaign(ctx, original.ID)
		if err != nil || saved.Name != updated.Name || saved.ExternalAdID != "" || saved.LandingURL != original.LandingURL {
			t.Fatalf("更新结果不符: %+v, %v", saved, err)
		}
		updated.ApplicationID = "102"
		if err := f.store.SaveCampaign(application.WithContext(context.Background(), "102"), &updated); err == nil {
			t.Fatal("跨应用更新不应成功")
		}
	})
	t.Run("有效目标保留归因", func(t *testing.T) {
		attribution, found, err := f.store.ResolveCampaign(ctx, "101", original.Code)
		if err != nil || !found || attribution.EpisodeID != original.EpisodeID {
			t.Fatalf("有效目标解析失败: %+v, %v, %v", attribution, found, err)
		}
	})
	for _, target := range []struct{ name, dramaID, episodeID string }{
		{"目标短剧已不存在", "2012", "301"},
		{"目标分集已不存在", "201", "2013"},
	} {
		t.Run(target.name, func(t *testing.T) {
			invalid := original
			invalid.ID, invalid.Code, invalid.DramaID, invalid.EpisodeID = target.dramaID+target.episodeID, target.dramaID+target.episodeID, target.dramaID, target.episodeID
			if err := f.store.SaveCampaign(ctx, &invalid); err != nil {
				t.Fatal(err)
			}
			attribution, found, err := f.store.ResolveCampaign(ctx, "101", invalid.Code)
			if err != nil || found || attribution.ID != "" {
				t.Fatalf("失效目标不能保留归因: %+v, %v, %v", attribution, found, err)
			}
		})
	}
	t.Run("查询故障不能伪装为有效归因", func(t *testing.T) {
		failure := errors.New("campaign target database unavailable")
		name := "test:campaign_target_failure"
		if err := f.store.data.DB().Callback().Query().Before("gorm:query").Register(name, func(db *gorm.DB) {
			if db.Statement.Table == "dramas" {
				db.AddError(failure)
			}
		}); err != nil {
			t.Fatal(err)
		}
		defer f.store.data.DB().Callback().Query().Remove(name)
		attribution, found, err := f.store.ResolveCampaign(ctx, "101", original.Code)
		if !errors.Is(err, failure) || found || attribution.ID != "" {
			t.Fatalf("查询错误未传播: %+v, %v, %v", attribution, found, err)
		}
	})
}
