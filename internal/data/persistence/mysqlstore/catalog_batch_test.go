package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// TestListAllDramasLoadsEpisodesInOneQuery 验证多部短剧的分集批量装载且按主体批量装载官方投影，不逐行查询渠道版本。
func TestListAllDramasLoadsEpisodesInOneQuery(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := newTestStore(db)
	now := time.Date(2026, time.September, 20, 4, 0, 0, 0, time.UTC)
	dramaColumns := []string{"subject_id", "created_by", "updated_by", "assignee", "operation_note", "operation_due_at", "cover_mid", "cover_space_name", "cover_file_name", "id", "title", "description", "cover_url", "status", "year", "language", "drama_type", "tag_ids_json", "publish_status", "created_at", "updated_at"}
	for i, name := range dramaColumns {
		dramaColumns[i] = "`dramas`.`" + name + "`"
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT " + strings.Join(dramaColumns, ",") + " FROM `dramas` WHERE dramas.subject_id = ? ORDER BY created_at DESC, id ASC")).
		WithArgs("1").WillReturnRows(sqlmock.NewRows([]string{"id", "title", "tag_ids_json", "created_at", "updated_at"}).
		AddRow(uint64(1), "Drama 1", `[]`, now, now).
		AddRow(uint64(2), "Drama 2", `[]`, now, now))
	mock.ExpectQuery("SELECT \\* FROM `tiktok_drama_states`").WithArgs("1", "1", "2").WillReturnRows(sqlmock.NewRows([]string{"subject_id", "drama_id", "published_snapshot"}))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `tiktok_tag_english_names`")).
		WillReturnRows(sqlmock.NewRows([]string{"tag_id", "english_name", "updated_at"}))
	episodeColumns := []string{"media_space_name", "is_draft", "id", "drama_id", "episode_number", "title", "description", "vid", "poster_url", "duration_seconds", "media_status", "created_at", "updated_at"}
	for i, name := range episodeColumns {
		episodeColumns[i] = "`episodes`.`" + name + "`"
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT "+strings.Join(episodeColumns, ",")+" FROM `episodes` WHERE drama_id IN (?,?) AND is_draft = ? ORDER BY drama_id ASC, episode_number ASC, id ASC")).
		WithArgs("1", "2", true).
		WillReturnRows(sqlmock.NewRows([]string{"id", "drama_id", "episode_number", "is_draft", "title"}).
			AddRow(uint64(11), uint64(1), 1, true, "Episode 1").
			AddRow(uint64(12), uint64(1), 2, true, "Episode 2").
			AddRow(uint64(21), uint64(2), 1, true, "Episode 1"))

	dramas, err := store.ListAllDramas(application.WithSubject(context.Background(), "1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(dramas) != 2 || len(dramas[0].Episodes) != 2 || len(dramas[1].Episodes) != 1 {
		t.Fatalf("批量分集装载结果不符: %+v", dramas)
	}
	if dramas[0].Episodes[0].Number != 1 || dramas[0].Episodes[1].Number != 2 || dramas[1].Episodes[0].DramaID != "2" {
		t.Fatalf("分集归属或顺序不符: %+v", dramas)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
