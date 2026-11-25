package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/homepage"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

// TestHomepageMySQLIntegration 使用独立 MySQL 验证版本冲突、栏目隔离与跨实例持久读回。
func TestHomepageMySQLIntegration(t *testing.T) {
	f := newIAAFixture(t)
	ctx := application.WithSubject(context.Background(), "1")
	repo := f.store
	// 存储层重新检查新增引用的线上状态，配置往返测试也使用真实线上剧目。
	for _, id := range []string{"1", "2", "3"} {
		drama := consumer.Drama{ID: id, Title: "推荐配置夹具", PublishStatus: "published"}
		online := drama
		drama.Published = &online
		if err := repo.SaveDrama(ctx, drama); err != nil {
			t.Fatal(err)
		}
		rawSnapshot, _ := json.Marshal(online)
		if err := seedOfficialProjection(f.store.data.DB().WithContext(ctx), drama.ID, rawSnapshot); err != nil {
			t.Fatal(err)
		}
	}
	other := NewStore(f.store.data)
	initial, err := repo.GetCarousel(ctx)
	if err != nil || initial.Revision != 0 || initial.DramaIDs == nil {
		t.Fatal("未配置轮播应为空数组和版本零")
	}
	updated, err := repo.ReplaceCarousel(ctx, homepage.Carousel{DramaIDs: []string{"2", "1"}, Items: []homepage.CarouselItem{{DramaID: "2", Title: "推荐标题", Visible: false, CreatedAt: "2026-09-15T08:00:00Z"}, {DramaID: "1", Visible: true}}})
	if err != nil || updated.Revision != 1 {
		t.Fatal("首次配置失败", err)
	}
	if _, err := other.ReplaceCarousel(ctx, homepage.Carousel{DramaIDs: []string{"3"}}); !errors.Is(err, homepage.ErrConflict) {
		t.Fatal("并发旧版本覆盖了轮播")
	}
	read, err := other.GetCarousel(ctx)
	if err != nil || len(read.DramaIDs) != 2 || read.DramaIDs[0] != "2" || len(read.Items) != 2 || read.Items[0].Title != "推荐标题" || read.Items[0].Visible || read.Items[0].CreatedAt != "2026-09-15T08:00:00Z" {
		t.Fatal("跨实例读回错误")
	}

	hot, err := repo.ReplaceHot(ctx, homepage.Carousel{DramaIDs: []string{"1"}, Items: []homepage.CarouselItem{{DramaID: "1", Title: "人工热播", Visible: true}}})
	if err != nil || hot.Revision != 1 {
		t.Fatal(err)
	}
	readHot, err := other.GetHot(ctx)
	if err != nil || readHot.Items[0].Title != "人工热播" {
		t.Fatal("热播未持久读回", err)
	}
	if _, err := other.ReplaceHot(ctx, homepage.Carousel{DramaIDs: []string{}}); !errors.Is(err, homepage.ErrConflict) {
		t.Fatal("热播旧版本未拒绝")
	}
	foreign, err := repo.GetHot(ctx)
	if err != nil || len(foreign.DramaIDs) != 1 {
		t.Fatal("共享热播读取不一致", err)
	}
	read, err = other.GetCarousel(ctx)
	if err != nil || len(read.DramaIDs) != 2 {
		t.Fatal("热播串改轮播", err)
	}
}

// TestHomepageConcurrentMySQLIntegration 验证首次并发创建、版本冲突和事务回滚不会覆盖配置。
func TestHomepageConcurrentMySQLIntegration(t *testing.T) {
	f := newIAAFixture(t)
	ctx := application.WithSubject(context.Background(), "1")
	var wg sync.WaitGroup
	var successes atomic.Int64
	start := make(chan struct{})
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := f.store.ReplaceCarousel(ctx, homepage.Carousel{DramaIDs: []string{"201"}})
			if err == nil {
				successes.Add(1)
			} else if !errors.Is(err, homepage.ErrConflict) {
				t.Error(err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("首次并发保存成功数应为一：%d", successes.Load())
	}
	if err := f.store.data.DB().Exec("CREATE TRIGGER reject_homepage_update BEFORE UPDATE ON homepage_recommendations FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='fixture-recommendation-write'").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ReplaceCarousel(ctx, homepage.Carousel{Revision: 1, DramaIDs: []string{"202"}}); err == nil {
		t.Fatal("数据库故障被吞掉")
	}
	value, err := f.store.GetCarousel(ctx)
	if err != nil || value.Revision != 1 || value.DramaIDs[0] != "201" {
		t.Fatal("失败事务改变了配置", err)
	}
}
