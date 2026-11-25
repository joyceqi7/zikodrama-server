package mysqlstore

import (
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/operations"
	"errors"
	"reflect"
	"sync"
	"testing"
)

// TestMiniReviewSeparateTablesIntegration 验证独立版本记录、共享清单及应用资料之间的写入边界。
func TestMiniReviewSeparateTablesIntegration(t *testing.T) {
	f := newIAAFixture(t)
	defer f.store.data.pool.Close()
	ctx := t.Context()
	create := func(key, provider string) application.Application {
		app := application.Application{SubjectID: "1", TikTokAppID: key, ClientKey: key, ClientSecret: "fixture-only", Name: key, MonetizationMode: application.MonetizationModeIAP, Status: application.StatusActive, TimeZone: "UTC", PlaybackProvider: provider}
		if err := f.store.CreateApplication(ctx, &app); err != nil {
			t.Fatal(err)
		}
		return app
	}
	a, b := create("review-table-a", "tiktok_minis"), create("review-table-b", "byteplus_web")
	var oldColumn int64
	if err := f.store.data.DB().Raw("SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='applications' AND COLUMN_NAME='mini_review_json'").Scan(&oldColumn).Error; err != nil || oldColumn != 0 {
		t.Fatal("应用表不应保留审核 JSON 字段", err)
	}
	var applicationBefore applicationModel
	if err := f.store.data.DB().First(&applicationBefore, "id=?", a.ID).Error; err != nil {
		t.Fatal(err)
	}
	shared, err := f.store.SaveMiniReview(ctx, a.ID, application.MiniReview{DramaIDs: []string{"201", "202"}})
	if err != nil || shared.Revision != 1 {
		t.Fatal("共享清单首次保存失败", err)
	}
	versions := []application.MiniReviewVersion{{Version: "1.0.0", Enabled: true}, {Version: "2.0.0", Enabled: false}}
	if _, err := f.store.SaveMiniReviewSettings(ctx, a.ID, application.MiniReview{Versions: versions, Revision: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.SaveMiniReviewSettings(ctx, b.ID, application.MiniReview{Versions: []application.MiniReviewVersion{{Version: "1.0.0"}}}); err != nil {
		t.Fatal("不同应用应能登记相同版本号", err)
	}
	readVersion := func(version string) applicationReviewVersionModel {
		var row applicationReviewVersionModel
		if err := f.store.data.DB().Where("application_id=? AND version=?", a.ID, version).Take(&row).Error; err != nil {
			t.Fatal(err)
		}
		return row
	}
	sameChannel := create("review-table-c", "tiktok_minis")
	inherited, err := f.store.FindApplication(ctx, sameChannel.ID)
	if err != nil || len(inherited.MiniReview.DramaIDs) != 2 || inherited.MiniReview.Revision != 2 || len(inherited.MiniReview.ConfiguredVersions()) != 0 {
		t.Fatal("同主体同渠道应共享清单，不复制版本开关", err)
	}
	first, other := readVersion("1.0.0"), readVersion("2.0.0")
	versions[0].Enabled = false
	if _, err := f.store.SaveMiniReviewSettings(ctx, a.ID, application.MiniReview{Versions: versions, Revision: 2}); err != nil {
		t.Fatal(err)
	}
	closed := readVersion("1.0.0")
	if closed.ID != first.ID || !closed.CreatedAt.Equal(first.CreatedAt) || closed.Enabled || !reflect.DeepEqual(other, readVersion("2.0.0")) {
		t.Fatal("关闭版本应保留登记记录且不改写其他版本")
	}
	if _, err := f.store.SaveMiniReviewSettings(ctx, a.ID, application.MiniReview{Versions: versions[1:], Revision: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.SaveMiniReviewSettings(ctx, a.ID, application.MiniReview{Versions: []application.MiniReviewVersion{}, Revision: 4}); err != nil {
		t.Fatal(err)
	}
	var remaining int64
	if err := f.store.data.DB().Model(&applicationReviewVersionModel{}).Where("application_id=?", a.ID).Count(&remaining).Error; err != nil || remaining != 0 {
		t.Fatal("删除最后一个版本应移除版本记录", err)
	}
	stored, err := f.store.FindApplicationByTikTokAppID(ctx, a.TikTokAppID)
	if err != nil || stored.MiniReview.Revision != 5 || len(stored.MiniReview.DramaIDs) != 2 || len(stored.MiniReview.ConfiguredVersions()) != 0 {
		t.Fatal("删除版本不应删除共享清单或重置修订", err)
	}
	// 两个菜单共用同一应用锁和修订，竞争写入只能有一个成功。
	start := make(chan struct{})
	errorsOut := make(chan error, 2)
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		<-start
		_, err := f.store.SaveMiniReviewSettings(ctx, a.ID, application.MiniReview{Revision: 5, Versions: []application.MiniReviewVersion{{Version: "3.0.0"}}})
		errorsOut <- err
	}()
	go func() {
		defer workers.Done()
		<-start
		_, err := f.store.SaveMiniReview(ctx, a.ID, application.MiniReview{Revision: 5, DramaIDs: []string{"202"}})
		errorsOut <- err
	}()
	close(start)
	workers.Wait()
	close(errorsOut)
	success, conflicts := 0, 0
	for err := range errorsOut {
		switch {
		case err == nil:
			success++
		case errors.Is(err, operations.ErrConflict):
			conflicts++
		default:
			t.Fatal(err)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatal("清单与版本竞争保存未按修订拒绝过期写入")
	}
	stored, err = f.store.FindApplication(ctx, a.ID)
	if err != nil || stored.MiniReview.Revision != 6 {
		t.Fatal("共享修订不应丢失或重复递增", err)
	}
	otherApp, err := f.store.FindApplication(ctx, b.ID)
	if err != nil || otherApp.MiniReview.Revision != 1 || len(otherApp.MiniReview.DramaIDs) != 0 || len(otherApp.MiniReview.ConfiguredVersions()) != 1 {
		t.Fatal("审核配置跨应用泄漏", err)
	}
	var applicationAfter applicationModel
	if err := f.store.data.DB().First(&applicationAfter, "id=?", a.ID).Error; err != nil || !reflect.DeepEqual(applicationBefore, applicationAfter) {
		t.Fatal("审核写入不应更改应用资料或其更新时间", err)
	}
}
