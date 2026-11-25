package mysqlstore

import (
	"strings"
	"testing"

	"drama-server/internal/biz/application"
	"gorm.io/gorm"
)

// TestDeprecatedContentColumnsIgnoredIntegration 验证原字段保留原值但不进入当前读取和保存合同。
func TestDeprecatedContentColumnsIgnoredIntegration(t *testing.T) {
	f := newIAAFixture(t)
	ctx := application.WithSubject(t.Context(), "1")
	db := f.store.data.DB().WithContext(ctx)
	before, err := f.store.FindDrama(ctx, "201")
	if err != nil || len(before.Episodes) == 0 {
		t.Fatal("读取当前剧集失败", err)
	}
	first := before.Episodes[0]
	for _, statement := range []string{
		"UPDATE dramas SET published_snapshot=JSON_OBJECT('id','deprecated-drama'),platform_album_id='deprecated-album' WHERE id=201",
		"UPDATE episodes SET platform_episode_id='deprecated-episode' WHERE id=" + first.ID,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	after, err := f.store.FindDrama(ctx, "201")
	if err != nil || after.ID != before.ID || after.PlatformAlbumID != before.PlatformAlbumID || after.Episodes[0].PlatformEpisodeID != first.PlatformEpisodeID {
		t.Fatal("运行时读取了原表的废弃字段", err)
	}
	if err := f.store.SaveDrama(ctx, *after); err != nil {
		t.Fatal(err)
	}
	if err := f.store.SaveEpisode(ctx, first); err != nil {
		t.Fatal(err)
	}
	var snapshotID, albumID, episodeID string
	if err := db.Raw("SELECT JSON_UNQUOTE(JSON_EXTRACT(published_snapshot,'$.id')),platform_album_id FROM dramas WHERE id=201").Row().Scan(&snapshotID, &albumID); err != nil {
		t.Fatal(err)
	}
	if err := db.Raw("SELECT platform_episode_id FROM episodes WHERE id=?", first.ID).Scan(&episodeID).Error; err != nil {
		t.Fatal(err)
	}
	if snapshotID != "deprecated-drama" || albumID != "deprecated-album" || episodeID != "deprecated-episode" {
		t.Fatal("保存当前剧集改写了废弃字段")
	}
	for _, query := range []string{
		ownedDramaQuery(db.Session(&gorm.Session{DryRun: true})).Find(&[]dramaModel{}).Statement.SQL.String(),
		ownedEpisodeQuery(db.Session(&gorm.Session{DryRun: true})).Find(&[]episodeModel{}).Statement.SQL.String(),
	} {
		if strings.Contains(query, "SELECT *") || strings.Contains(query, "published_snapshot") || strings.Contains(query, "platform_album_id") || strings.Contains(query, "platform_episode_id") {
			t.Fatal("当前目录查询仍选择了废弃列")
		}
	}
	var marked int64
	if err := db.Raw("SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND ((table_name='dramas' AND column_name IN ('published_snapshot','platform_album_id')) OR (table_name='episodes' AND column_name='platform_episode_id')) AND column_comment LIKE '已废弃%'").Scan(&marked).Error; err != nil || marked != 3 {
		t.Fatal("原字段未保留或缺少废弃说明", err)
	}
}
