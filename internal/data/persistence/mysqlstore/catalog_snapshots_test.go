package mysqlstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/homepage"
	"github.com/DATA-DOG/go-sqlmock"
)

// TestCatalogSnapshotSummary 验证只解码展示字段，保留字符串标识、标签顺序及连载状态语义。
func TestCatalogSnapshotSummary(t *testing.T) {
	for _, status := range []string{"unpublished", "updating", "completed"} {
		t.Run(status, func(t *testing.T) {
			raw := `{"id":"快照内冗余标识","title":"线上标题","description":"含 \"引号\" 的简介","coverUrl":"/cover.jpg","status":"` + status + `","tagIds":["23","21","unknown"],"episodes":[{"id":"9007199254740993","vid":"first-vid","posterUrl":"/first.jpg","durationSeconds":{"列表无需解码":true}},{"id":"9007199254740994"}],"platformVersion":{"列表无需解码":true}}`
			var snapshot catalogSnapshot
			if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
				t.Fatal(err)
			}
			catalog := map[string]consumer.TikTokTag{"21": {ID: "21", EnglishName: "Mystery"}, "23": {ID: "23", EnglishName: "Romance"}}
			summary := snapshot.summary("9007199254740995", catalog)
			expectedStatus := "ongoing"
			if status == "completed" {
				expectedStatus = status
			}
			if summary.ID != "9007199254740995" || summary.Title != "线上标题" || summary.Description != `含 "引号" 的简介` || summary.CoverURL != "/cover.jpg" || summary.FirstEpisodeID != "9007199254740993" || summary.EpisodeCount != 2 || summary.Status != expectedStatus || summary.FirstEpisodePosterURL != "" || !reflect.DeepEqual(summary.Tags, []string{"Romance", "Mystery"}) || !reflect.DeepEqual(summary.TagIDs, []string{"23", "21", "unknown"}) {
				t.Fatalf("摘要合同改变：%+v", summary)
			}
		})
	}
	summary := (catalogSnapshot{}).summary("201", nil)
	if summary.EpisodeCount != 0 || summary.FirstEpisodeID != "" || summary.Tags == nil {
		t.Fatalf("空分集或空标签合同改变：%+v", summary)
	}
}

// snapshotCarouselRepository 只替换推荐配置，摘要读取与错误日志仍走实际业务层。
type snapshotCarouselRepository struct {
	// Repository 中未用到的写接口不参与本测试。
	homepage.Repository
}

// GetCarousel 返回一个可见引用，驱动首页批量快照及首帧图读取。
func (snapshotCarouselRepository) GetCarousel(context.Context) (homepage.Carousel, error) {
	return homepage.Carousel{DramaIDs: []string{"201"}, Items: []homepage.CarouselItem{{DramaID: "201", Visible: true}}}, nil
}

// TestHomepageSnapshotFailures 验证解析与海报读取失败有实际错误日志，成功不误报且不泄漏快照。
func TestHomepageSnapshotFailures(t *testing.T) {
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	failure := errors.New("SELECT poster_url: connection refused")
	for _, tc := range []struct {
		name      string
		malformed bool
		posterErr error
	}{{name: "成功补齐首帧图"}, {name: "快照解析失败", malformed: true}, {name: "海报读取失败", posterErr: failure}} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			store := newTestStore(db)
			ctx := application.WithPlayback(application.WithContext(application.WithSubject(context.Background(), "1"), "102"), "byteplus_web")
			mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `tiktok_tag_english_names`")).WillReturnRows(sqlmock.NewRows([]string{"tag_id", "english_name"}))
			raw := `{"title":"线上标题","coverUrl":"https://private.example/cover","token":"private-snapshot-token","episodes":[{"id":"301","vid":"online-vid","posterUrl":"/snapshot-poster"}]}`
			if tc.malformed {
				raw = `{"title":{},"coverUrl":"https://private.example/cover","token":"private-snapshot-token"}`
			}
			query := "SELECT p.drama_id AS id, v.snapshot_json AS snapshot_json FROM direct_publications AS p JOIN direct_publication_versions AS v ON v.id=p.current_version_id AND v.subject_id=p.subject_id AND v.drama_id=p.drama_id JOIN dramas AS owned_drama ON owned_drama.id=p.drama_id AND owned_drama.subject_id=p.subject_id JOIN applications AS a ON a.id=? AND a.status=? AND a.subject_id=p.subject_id JOIN application_playback_profiles AS f ON f.application_id=a.id AND f.provider=? WHERE (p.status = ? AND p.subject_id = ?) AND p.drama_id IN (?)"
			mock.ExpectQuery(regexp.QuoteMeta(query)).WithArgs("102", "active", "byteplus_web", "published", "1", "201").WillReturnRows(sqlmock.NewRows([]string{"id", "snapshot_json"}).AddRow("201", raw))
			if !tc.malformed {
				posters := mock.ExpectQuery("SELECT .* FROM .*episodes.* WHERE id IN \\(\\?\\)").WithArgs("301")
				if tc.posterErr != nil {
					posters.WillReturnError(tc.posterErr)
				} else {
					posters.WillReturnRows(sqlmock.NewRows([]string{"id", "vid", "poster_url"}).AddRow("301", "online-vid", "/derived-poster"))
				}
			}
			items, err := homepage.NewService(snapshotCarouselRepository{}, store, nil).HomeCarousel(ctx)
			if tc.malformed || tc.posterErr != nil {
				if err == nil || !strings.Contains(logs.String(), "load homepage visible drama summaries failed") || !strings.Contains(logs.String(), err.Error()) || !strings.Contains(logs.String(), "\"level\":\"ERROR\"") {
					t.Fatalf("首页故障缺少实际错误日志：%v %s", err, logs.String())
				}
				if tc.posterErr != nil && !errors.Is(err, tc.posterErr) {
					t.Fatal("海报读取错误链丢失")
				}
			} else if err != nil || len(items) != 1 || items[0].FirstEpisodePosterURL != "/derived-poster" || logs.Len() != 0 {
				t.Fatalf("首页摘要或成功日志改变：%+v %v %s", items, err, logs.String())
			}
			if strings.Contains(logs.String(), "private.example") || strings.Contains(logs.String(), "private-snapshot-token") {
				t.Fatal("日志泄漏快照")
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestDiscoveryReadsPageSnapshots 验证直连查询只返回当前页快照，读取及解析故障由业务层记录原始错误。
func TestDiscoveryReadsPageSnapshots(t *testing.T) {
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	queryFailure := errors.New("SELECT snapshot_json: connection refused")
	for _, tc := range []struct {
		name    string
		raw     string
		failure error
		wantErr bool
	}{
		{name: "成功不产生错误日志", raw: `{"title":"线上标题","description":"简介","coverUrl":"/cover.jpg","status":"updating","tagIds":["21"],"episodes":[{"id":"9007199254740993"}]}`},
		{name: "快照格式错误不泄漏内容", raw: `{"title":{},"coverUrl":"https://private.example/cover","token":"private-snapshot-token"}`, wantErr: true},
		{name: "数据库错误保留错误链", failure: queryFailure, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			store := newTestStore(db)
			ctx := application.WithPlayback(application.WithContext(application.WithSubject(context.Background(), "1"), "102"), "byteplus_web")
			mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `tiktok_tag_english_names`")).WillReturnRows(sqlmock.NewRows([]string{"tag_id", "english_name"}).AddRow("21", "Mystery"))
			from := " FROM direct_publications AS p JOIN direct_publication_versions AS v ON v.id=p.current_version_id AND v.subject_id=p.subject_id AND v.drama_id=p.drama_id JOIN dramas AS owned_drama ON owned_drama.id=p.drama_id AND owned_drama.subject_id=p.subject_id JOIN applications AS a ON a.id=? AND a.status=? AND a.subject_id=p.subject_id JOIN application_playback_profiles AS f ON f.application_id=a.id AND f.provider=? WHERE p.status = ? AND p.subject_id = ?"
			mock.ExpectQuery(regexp.QuoteMeta("SELECT DISTINCT JSON_EXTRACT(v.snapshot_json, '$.tagIds')"+from)).WithArgs("102", "active", "byteplus_web", "published", "1").WillReturnRows(sqlmock.NewRows([]string{"tags"}).AddRow(`["21"]`))
			mock.ExpectQuery(regexp.QuoteMeta("SELECT count(*)"+from)).WithArgs("102", "active", "byteplus_web", "published", "1").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
			query := mock.ExpectQuery(regexp.QuoteMeta("SELECT p.drama_id AS id, v.snapshot_json AS snapshot_json"+from+" ORDER BY v.created_at DESC, p.drama_id ASC LIMIT ?")).WithArgs("102", "active", "byteplus_web", "published", "1", 1)
			if tc.failure != nil {
				query.WillReturnError(tc.failure)
			} else {
				query.WillReturnRows(sqlmock.NewRows([]string{"id", "snapshot_json"}).AddRow("201", tc.raw))
			}
			page, err := consumer.NewService(store, nil).Discover(ctx, consumer.DiscoveryQuery{Limit: 1})
			if tc.wantErr {
				if err == nil || !strings.Contains(logs.String(), `"level":"ERROR"`) || strings.Count(logs.String(), "discover drama summaries failed") != 1 || !strings.Contains(logs.String(), err.Error()) {
					t.Fatalf("故障缺少操作描述或实际错误：%v %s", err, logs.String())
				}
				if tc.failure != nil && !errors.Is(err, tc.failure) {
					t.Fatal("数据库错误链丢失")
				}
			} else if err != nil || len(page.Items) != 1 || page.Items[0].FirstEpisodeID != "9007199254740993" || page.Items[0].Tags[0] != "Mystery" || page.NextCursor != consumer.EncodeDiscoveryCursor(1) || logs.Len() != 0 {
				t.Fatalf("列表结果或成功日志改变：%+v %v %s", page, err, logs.String())
			}
			if strings.Contains(logs.String(), "private.example") || strings.Contains(logs.String(), "private-snapshot-token") {
				t.Fatal("日志泄漏快照内容")
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
