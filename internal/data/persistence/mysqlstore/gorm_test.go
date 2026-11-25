package mysqlstore

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/gorm"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/sharding"
)

func TestNewInitializesGORM(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectPing()
	store := newTestStore(db)
	if store.data == nil || store.data.DB() == nil {
		t.Fatal("mysql store must initialize a GORM session")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestListApplicationsUsesGORMModel 验证应用列表读回内部主键、官方标识、变现模式和广告位。
func TestListApplicationsUsesGORMModel(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store := newTestStore(db)
	createdAt := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `applications` ORDER BY tiktok_app_id ASC")).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "logo_url", "tiktok_app_id", "client_key", "secret_ref",
			"region_code", "time_zone",
			"compliance_base_url", "ad_units_json", "monetization_mode", "status", "created_at", "updated_at",
		}).AddRow(
			uint64(12), "Drama ID", "", "tiktok-app-id", "", "", "ID", "Asia/Jakarta",
			"", `{"rewardedVideo":["rewarded-1"],"interstitial":[]}`, application.MonetizationModeIAA, "active", createdAt, createdAt,
		))

	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `application_playback_profiles` WHERE application_id IN (?)")).WillReturnRows(sqlmock.NewRows([]string{"application_id", "provider", "revision"}))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `review_content_lists`")).WillReturnRows(sqlmock.NewRows([]string{"subject_id", "playback_provider", "drama_ids_json", "revision"}))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `application_review_versions`")).WillReturnRows(sqlmock.NewRows([]string{"application_id", "version", "enabled"}))
	mock.ExpectCommit()
	applications, err := store.ListApplications(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(applications) != 1 || applications[0].ID != "12" || applications[0].TikTokAppID != "tiktok-app-id" || applications[0].MonetizationMode != application.MonetizationModeIAA ||
		!applications[0].AdUnits.HasRewardedVideo("rewarded-1") {
		t.Fatalf("unexpected applications: %+v", applications)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRepositoryModelQueryRunsThroughGORMCallbacks(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := newTestStore(db)

	callbackCalled := false
	if err := store.data.DB().Callback().Query().Before("gorm:query").Register("test:styx_model_path", func(*gorm.DB) {
		callbackCalled = true
	}); err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT count(*) FROM `reward_grants_07` WHERE bucket_id = ? AND application_id = ? AND visitor_id = ? AND reward_type = ? AND granted_at >= ?")).
		WithArgs(7, "1033", "visitor-1", "vip", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))

	ctx := application.WithContext(context.Background(), "1033")
	ctx = sharding.WithBucketID(ctx, 7)
	count, err := store.CountRewardGrants(ctx, "visitor-1", "vip", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 || !callbackCalled {
		t.Fatalf("业务 Model 查询未经过 GORM callback：count=%d callback=%t", count, callbackCalled)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
