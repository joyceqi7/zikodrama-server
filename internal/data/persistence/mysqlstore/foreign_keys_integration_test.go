package mysqlstore

import (
	"context"
	"database/sql"
	"drama-server/internal/biz/application"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
	mysqldriver "github.com/go-sql-driver/mysql"

	"drama-server/internal/biz/adminrbac"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
)

// assertNoDatabaseForeignKeys 从真实数据库元数据确认新库及升级后的业务库没有任何外键。
func assertNoDatabaseForeignKeys(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLE_CONSTRAINTS WHERE CONSTRAINT_SCHEMA = DATABASE() AND CONSTRAINT_TYPE = 'FOREIGN KEY'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("业务数据库仍有 %d 个外键", count)
	}
}

// newForeignKeyTestStore 复用部署 Schema 和现有 MySQL 测试运行时，只创建并清理本次隔离数据库。
func newForeignKeyTestStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("未配置 TEST_MYSQL_DSN，跳过隔离 MySQL 验证")
	}
	config, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.DBName, config.ParseTime, config.Loc = "", true, time.UTC
	server, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	name := fmt.Sprintf("drama_no_fk_%d", time.Now().UnixNano())
	if _, err := server.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := server.Exec("DROP DATABASE `" + name + "`"); err != nil {
			t.Errorf("清理隔离数据库失败：%v", err)
		}
	})
	config.DBName = name
	db, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range strings.Split(string(readDeploymentSchema(t)), ";") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("执行隔离 Schema 失败：%v", err)
		}
	}
	data, err := OpenData(application.WithSubject(context.Background(), "1"), &configv1.MySQLConfig{Master: config.FormatDSN()}, newMySQLIntegrationRuntime(t))
	if err != nil {
		t.Fatal(err)
	}
	return NewStore(data)
}

// TestCurrentSchemaIntegrationWithoutForeignKeys 在真实隔离 MySQL 中验证取消外键后发布、清理、权限拒绝与并发关联仍正确。
func TestCurrentSchemaIntegrationWithoutForeignKeys(t *testing.T) {
	ctx := application.WithSubject(context.Background(), "1")
	store := newForeignKeyTestStore(t)
	assertNoDatabaseForeignKeys(t, ctx, store.data.pool)
	t.Run("发布主体无需应用记录且删除清理原子提交", func(t *testing.T) {
		now := time.Now().UTC()
		drama := domain.Drama{ID: "1017", Title: "无外键发布", PublishStatus: "draft", Episodes: []domain.Episode{
			{ID: "1022", DramaID: "1017", Number: 1, MediaStatus: "ready", VID: "test-video", MediaSpaceName: "test-space"},
		}}
		if err := store.SaveDramaAggregate(ctx, &drama, nil); err != nil {
			t.Fatal(err)
		}
		publication := operations.TikTokPublication{SubjectID: "1", ID: "1023", DramaID: drama.ID, OwnerApplicationID: "independent-owner-not-an-app", ContentSnapshot: &drama,
			Status: operations.PublicationStatusQueued, Stage: operations.PublicationStageUploadCover, Priority: "normal", NextAttemptAt: now, CreatedAt: now, UpdatedAt: now}
		seedReadyPublicationContent(t, store, &publication)
		if err := store.StartTikTokPublication(ctx, &publication); err != nil {
			t.Fatalf("独立拥有方发布失败：%v", err)
		}
		publication.Status = operations.PublicationStatusFailed
		if err := store.UpdateTikTokPublication(ctx, publication, &drama); err != nil {
			t.Fatal(err)
		}
		db := store.data.DB().WithContext(ctx)
		if err := db.Create(&distributionModel{DramaDistribution: operations.DramaDistribution{DramaID: drama.ID, ApplicationID: "1034", OwnerApplicationID: publication.OwnerApplicationID, AlbumID: "test-album", Status: "revoked", NextAttemptAt: now, LeaseExpiresAt: now}}).Error; err != nil {
			t.Fatal(err)
		}
		epID := drama.Episodes[0].ID
		grant := rewardGrantModel{BucketID: 0, ClaimID: "no-fk-grant", ApplicationID: "1035", VisitorID: "1036", DramaID: &drama.ID, EpisodeID: &epID, GrantedAt: now}
		if err := db.Create(&grant).Error; err != nil {
			t.Fatal(err)
		}
		// 清理队列故障必须回滚所有显式删除和奖励引用更新。
		if err := db.Exec("CREATE TRIGGER reject_no_fk_cleanup BEFORE INSERT ON media_jobs FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'cleanup failure'").Error; err != nil {
			t.Fatal(err)
		}
		err := store.DeleteDrama(ctx, drama.ID)
		if dropErr := db.Exec("DROP TRIGGER reject_no_fk_cleanup").Error; dropErr != nil {
			t.Fatal(dropErr)
		}
		if err == nil {
			t.Fatal("清理失败仍提交了删除")
		}
		assertRows := func(expected int64) {
			t.Helper()
			for _, table := range []string{"episodes", "tiktok_publications", "drama_distributions"} {
				var count int64
				if err := db.Table(table).Where("drama_id = ?", drama.ID).Count(&count).Error; err != nil || count != expected {
					t.Fatalf("%s 关联数量=%d，期望=%d，错误=%v", table, count, expected, err)
				}
			}
		}
		assertRows(1)
		var saved rewardGrantModel
		if err := db.First(&saved, "claim_id = ?", grant.ClaimID).Error; err != nil || saved.DramaID == nil || saved.EpisodeID == nil {
			t.Fatalf("回滚丢失奖励引用：%v", err)
		}
		if err := store.DeleteDrama(ctx, drama.ID); err != nil {
			t.Fatal(err)
		}
		assertRows(0)
		saved = rewardGrantModel{}
		if err := db.First(&saved, "claim_id = ?", grant.ClaimID).Error; err != nil || saved.DramaID != nil || saved.EpisodeID != nil {
			t.Fatalf("奖励流水未保留或引用未清空：%v", err)
		}
		if _, err := store.FindDrama(ctx, drama.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("删除后仍可读取短剧：%v", err)
		}
	})
	t.Run("编辑移除分集保留奖励流水和线上引用", func(t *testing.T) {
		drama := domain.Drama{ID: "1018", Title: "编辑分集", PublishStatus: "draft", Episodes: []domain.Episode{
			{ID: "1021", DramaID: "1018", Number: 1},
			{ID: "1019", DramaID: "1018", Number: 2},
		}}
		if err := store.SaveDramaAggregate(ctx, &drama, nil); err != nil {
			t.Fatal(err)
		}
		db := store.data.DB().WithContext(ctx)
		removedID := drama.Episodes[0].ID
		grant := rewardGrantModel{BucketID: 0, ClaimID: "no-fk-edit-grant", ApplicationID: "1006", VisitorID: "1007", DramaID: &drama.ID, EpisodeID: &removedID, GrantedAt: time.Now().UTC()}
		if err := db.Create(&grant).Error; err != nil {
			t.Fatal(err)
		}
		drama.Episodes = drama.Episodes[1:]
		drama.Episodes[0].Number = 1
		if err := store.SaveDramaAggregate(ctx, &drama, nil); err != nil {
			t.Fatal(err)
		}
		var saved rewardGrantModel
		if err := db.First(&saved, "claim_id = ?", grant.ClaimID).Error; err != nil || saved.EpisodeID != nil || saved.DramaID == nil || *saved.DramaID != drama.ID {
			t.Fatalf("编辑后奖励引用错误：%v", err)
		}
		if _, err := store.FindEpisode(ctx, removedID); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("旧分集未删除：%v", err)
		}
		if _, err := store.FindEpisode(ctx, drama.Episodes[0].ID); err != nil {
			t.Fatalf("保留分集丢失：%v", err)
		}
		// 已上线分集即使移出编辑区，也必须保留实体及奖励引用。
		published := drama
		drama.Published = &published
		drama.PublishStatus = "published"
		publishedJSON, err := json.Marshal(published)
		if err != nil {
			t.Fatal(err)
		}
		// 上线快照只能由官方发布流程写入，此处直接准备已确认的平台事实。
		if err := seedOfficialProjection(db, drama.ID, publishedJSON); err != nil {
			t.Fatal(err)
		}
		publishedID := drama.Episodes[0].ID
		if err := db.Model(&rewardGrantModel{}).Where("claim_id = ?", grant.ClaimID).Update("episode_id", publishedID).Error; err != nil {
			t.Fatal(err)
		}
		drama.Episodes = []domain.Episode{{ID: "1020", DramaID: drama.ID, Number: 1}}
		if err := store.SaveDramaAggregate(ctx, &drama, nil); err != nil {
			t.Fatal(err)
		}
		var historical episodeModel
		if err := db.First(&historical, "id = ?", publishedID).Error; err != nil || historical.IsDraft {
			t.Fatalf("线上引用分集未保留为历史记录：%v", err)
		}
		current, err := store.FindDrama(ctx, drama.ID)
		if err != nil || current.Published == nil || len(current.Published.Episodes) != 1 || current.Published.Episodes[0].ID != publishedID {
			t.Fatalf("线上快照不可读取：%v", err)
		}
		saved = rewardGrantModel{}
		if err := db.First(&saved, "claim_id = ?", grant.ClaimID).Error; err != nil || saved.EpisodeID == nil || *saved.EpisodeID != publishedID {
			t.Fatalf("线上权益分集引用丢失：%v", err)
		}
	})
	t.Run("关联缺失拒绝且保留原分配", func(t *testing.T) {
		service := adminrbac.NewService(store, adminrbac.Config{SigningKey: []byte("test-only-admin-jwt-signing-key-32-bytes")})
		user, err := service.CreateUser(ctx, adminrbac.UserCreateInput{Username: "no-fk-user", Password: "test-password-123", Nickname: "关联校验", Status: adminrbac.StatusActive})
		if err != nil {
			t.Fatal(err)
		}
		role, err := service.CreateRole(ctx, adminrbac.RoleInput{Code: "no-fk-role", Name: "关联角色", Status: adminrbac.StatusActive})
		if err != nil {
			t.Fatal(err)
		}
		permission, err := service.CreatePermission(ctx, adminrbac.PermissionInput{Code: "no.fk.permission", Name: "关联权限", Type: "action", Status: "active"})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.ReplaceAdminRolePermissions(ctx, role.ID, []string{permission.ID}); err != nil {
			t.Fatal(err)
		}
		if err := store.ReplaceAdminUserRoles(ctx, user.ID, []string{role.ID}); err != nil {
			t.Fatal(err)
		}
		if err := store.DeleteAdminRole(ctx, role.ID); !errors.Is(err, adminrbac.ErrConflict) {
			t.Fatalf("已分配角色仍可删除：%v", err)
		}
		if err := store.DeleteAdminPermission(ctx, permission.ID); !errors.Is(err, adminrbac.ErrConflict) {
			t.Fatalf("已分配权限仍可删除：%v", err)
		}
		if err := store.ReplaceAdminUserRoles(ctx, user.ID, []string{"18446744073709551615"}); !errors.Is(err, adminrbac.ErrConflict) {
			t.Fatalf("不存在的角色未被拒绝：%v", err)
		}
		if err := store.ReplaceAdminRolePermissions(ctx, role.ID, []string{"18446744073709551615"}); !errors.Is(err, adminrbac.ErrConflict) {
			t.Fatalf("不存在的权限未被拒绝：%v", err)
		}
		roles, err := store.ListAdminUserRoleIDs(ctx, user.ID)
		if err != nil || !slices.Equal(roles, []string{role.ID}) {
			t.Fatalf("原角色分配丢失：%v %v", roles, err)
		}
		permissions, err := store.ListAdminRolePermissionIDs(ctx, role.ID)
		if err != nil || !slices.Equal(permissions, []string{permission.ID}) {
			t.Fatalf("原权限分配丢失：%v %v", permissions, err)
		}
		if err := store.ReplaceAdminUserRoles(ctx, user.ID, nil); err != nil {
			t.Fatal(err)
		}
		if err := store.DeleteAdminRole(ctx, role.ID); err != nil {
			t.Fatal(err)
		}
		permissions, err = store.ListAdminRolePermissionIDs(ctx, role.ID)
		if err != nil || len(permissions) != 0 {
			t.Fatalf("删除角色遗留权限关联：%v %v", permissions, err)
		}
		if err := store.DeleteAdminPermission(ctx, permission.ID); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("并发授权与删除不留孤立关系", func(t *testing.T) {
		service := adminrbac.NewService(store, adminrbac.Config{})
		role, err := service.CreateRole(ctx, adminrbac.RoleInput{Code: "no-fk-concurrent", Name: "并发角色", Status: adminrbac.StatusActive})
		if err != nil {
			t.Fatal(err)
		}
		permission, err := service.CreatePermission(ctx, adminrbac.PermissionInput{Code: "no.fk.concurrent", Name: "并发权限", Type: "action", Status: "active"})
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		results := make(chan error, 2)
		var wg sync.WaitGroup
		for _, operation := range []func() error{
			func() error { return store.ReplaceAdminRolePermissions(ctx, role.ID, []string{permission.ID}) },
			func() error { return store.DeleteAdminPermission(ctx, permission.ID) },
		} {
			wg.Go(func() { <-start; results <- operation() })
		}
		close(start)
		wg.Wait()
		close(results)
		success := 0
		for err := range results {
			if err == nil {
				success++
			} else if !errors.Is(err, adminrbac.ErrConflict) {
				t.Fatalf("并发操作意外失败：%v", err)
			}
		}
		if success != 1 {
			t.Fatalf("授权和删除应仅一个成功，实际=%d", success)
		}
		var orphaned int
		if err := store.data.pool.QueryRowContext(ctx, "SELECT COUNT(*) FROM admin_role_permissions rp LEFT JOIN admin_permissions p ON p.id = rp.permission_id WHERE p.id IS NULL").Scan(&orphaned); err != nil || orphaned != 0 {
			t.Fatalf("并发留下失效权限：%d %v", orphaned, err)
		}
	})
	t.Run("并发分配账号与删除角色不留孤立关系", func(t *testing.T) {
		service := adminrbac.NewService(store, adminrbac.Config{})
		user, err := service.CreateUser(ctx, adminrbac.UserCreateInput{Username: "no-fk-concurrent-user", Password: "test-password-123", Nickname: "并发账号", Status: adminrbac.StatusActive})
		if err != nil {
			t.Fatal(err)
		}
		role, err := service.CreateRole(ctx, adminrbac.RoleInput{Code: "no-fk-concurrent-user-role", Name: "并发账号角色", Status: adminrbac.StatusActive})
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		results := make(chan error, 2)
		var wg sync.WaitGroup
		for _, operation := range []func() error{
			func() error { return store.ReplaceAdminUserRoles(ctx, user.ID, []string{role.ID}) },
			func() error { return store.DeleteAdminRole(ctx, role.ID) },
		} {
			wg.Go(func() { <-start; results <- operation() })
		}
		close(start)
		wg.Wait()
		close(results)
		success := 0
		for err := range results {
			if err == nil {
				success++
			} else if !errors.Is(err, adminrbac.ErrConflict) {
				t.Fatalf("并发操作意外失败：%v", err)
			}
		}
		if success != 1 {
			t.Fatalf("分配账号和删除角色应仅一个成功，实际=%d", success)
		}
		var orphaned int
		if err := store.data.pool.QueryRowContext(ctx, "SELECT COUNT(*) FROM admin_user_roles ur LEFT JOIN admin_roles r ON r.id = ur.role_id WHERE r.id IS NULL").Scan(&orphaned); err != nil || orphaned != 0 {
			t.Fatalf("并发留下失效角色：%d %v", orphaned, err)
		}
	})
}
