package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

// ApplyDistribution 串行调用官方授权接口，保存关系的确认事实，不写入待执行任务。
func (s *Store) ApplyDistribution(ctx context.Context, id, appID string, enabled, policy bool, call operations.DistributionCall) error {
	// 授权校验和结果投影始终使用官方快照，目标应用的播放器只影响消费端访问。
	ctx = application.WithPlayback(ctx, "tiktok_minis")
	var callErr error
	err := s.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var drama dramaModel
		if err := ownedDramaQuery(tx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", id).Take(&drama).Error; err != nil {
			return err
		}
		var app applicationModel
		if err := tx.Where("id=?", appID).Take(&app).Error; err != nil {
			return err
		}
		if selected := application.SubjectIDFromContext(ctx); selected != "" && selected != app.SubjectID {
			return operations.ErrConflict
		}
		tx = tx.WithContext(application.WithSubject(ctx, app.SubjectID))
		if err := attachSubjectState(tx, &drama); err != nil {
			return err
		}
		// 官方授权预先绑定目标应用，是否消费该通道仍由应用当前播放器决定。

		var published *domain.Drama
		if len(drama.PublishedSnapshot) > 0 {
			if err := json.Unmarshal(drama.PublishedSnapshot, &published); err != nil {
				return err
			}
		}
		desired := enabled && published != nil && published.PublishStatus == "published" && app.Status == application.StatusActive && app.ClientKey != ""
		if enabled && !desired && !policy {
			return operations.ErrConflict
		}
		p, err := lockedLatestPublication(tx, id)
		if err != nil {
			return err
		}
		if p.PlatformAlbumID == "" {
			return operations.ErrConflict
		}
		var row distributionModel
		err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("drama_id=? AND application_id=?", id, appID).Take(&row).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil && row.Enabled == enabled && row.Desired == desired && (desired && row.Status == "authorized" || !desired && row.Status == "revoked") {
			return nil
		}
		if err == nil && row.LeaseOwner != "" && row.LeaseExpiresAt.After(time.Now().UTC()) {
			return operations.ErrConflict
		}
		row.DramaID, row.ApplicationID, row.OwnerApplicationID, row.AlbumID = id, appID, p.OwnerApplicationID, p.PlatformAlbumID
		row.Enabled, row.Desired = enabled, desired
		row.Revision++
		row.LeaseOwner = ""
		row.LeaseExpiresAt = time.Unix(0, 0).UTC()
		row.NextAttemptAt = time.Unix(0, 0).UTC()
		row.Attempts = 0
		row.ErrorCode = ""
		callCtx, cancel := context.WithTimeout(application.WithSubject(ctx, app.SubjectID), 30*time.Second)
		defer cancel()
		callErr = call(callCtx, row.DramaDistribution)
		row.Status = "revoked"
		if desired {
			row.Status = "authorized"
		}
		if callErr != nil {
			row.Status = "failed"
			row.ErrorCode = "dependency_error"
			row.Attempts = 1
			var platformErr *operations.PlatformCallError
			if errors.As(callErr, &platformErr) {
				row.ErrorCode = platformErr.Code
			}
		}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "drama_id"}, {Name: "application_id"}}, DoUpdates: clause.AssignmentColumns([]string{"enabled", "desired", "status", "revision", "attempts", "error_code", "next_attempt_at", "lease_owner", "lease_expires_at"})}).Create(&row).Error; err != nil {
			return err
		}
		if callErr == nil || !desired {
			if err := projectDistribution(tx, &drama, appID, desired && callErr == nil); err != nil {
				return err
			}
		}
		return touchDramaOperator(tx, id)
	})
	if err != nil {
		return err
	}
	return callErr
}
