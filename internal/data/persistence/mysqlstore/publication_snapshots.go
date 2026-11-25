package mysqlstore

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"time"

	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PrepareDrama 仅校验并确认当前内容就绪，不生成版本或修改渠道任务。
func (s *Store) PrepareDrama(ctx context.Context, id, actor string) (*consumer.Drama, error) {
	if err := s.PrepareDramas(ctx, []string{id}, actor); err != nil {
		return nil, err
	}
	return s.FindDrama(ctx, id)
}

// PrepareDramas 先按主键顺序锁定并校验所有草稿，再统一确认就绪；事务失败不留下部分就绪内容。
func (s *Store) PrepareDramas(ctx context.Context, ids []string, actor string) error {
	ordered := slices.Clone(ids)
	// Handler 已校验十进制主键，统一加锁顺序避免重叠批次互相等待。
	slices.SortFunc(ordered, func(a, b string) int {
		left, _ := strconv.ParseUint(a, 10, 64)
		right, _ := strconv.ParseUint(b, 10, 64)
		if left < right {
			return -1
		}
		if left > right {
			return 1
		}
		return 0
	})
	err := s.data.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		rows := make([]dramaModel, 0, len(ordered))
		for _, id := range ordered {
			var row dramaModel
			if err := ownedDramaQuery(tx).Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "id = ?", id).Error; err != nil {
				return err
			}
			rows = append(rows, row)
		}
		for _, row := range rows {
			snapshot, err := frozenDraft(tx, row.ID)
			if err != nil {
				return err
			}
			if err := operations.CheckContentReady(snapshot); err != nil {
				return err
			}
		}
		for _, row := range rows {
			if row.PublishStatus == "ready" {
				continue
			}
			if err := tx.Model(&row).Updates(map[string]any{"publish_status": "ready", "updated_at": time.Now().UTC()}).Error; err != nil {
				return err
			}
			if err := touchDramaOperator(tx, row.ID); err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return consumer.ErrNotFound
	}
	return err
}

// readyPublicationSnapshot 在发布事务内直接校验已保存内容，不要求运营先切换编辑状态。
func readyPublicationSnapshot(tx *gorm.DB, row dramaModel) (consumer.Drama, error) {
	draft, err := frozenDraft(tx, row.ID)
	if err != nil {
		return consumer.Drama{}, err
	}
	if err := operations.CheckContentReady(draft); err != nil {
		return consumer.Drama{}, err
	}
	return consumer.FreezeContent(draft), nil
}

// retainedContentSnapshots 读取直连发布版本与 TK 任务各自的固定快照，用于媒资及分集保护。
func retainedContentSnapshots(tx *gorm.DB, id string) ([]consumer.Drama, error) {
	type row struct{ SnapshotJSON []byte }
	var versions, publications, states []row
	if err := tx.Table("direct_publication_versions").Select("snapshot_json").Where("drama_id = ?", id).Find(&versions).Error; err != nil {
		return nil, err
	}
	if err := tx.Table("tiktok_publications").Select("JSON_EXTRACT(workflow_json, '$.content') AS snapshot_json").
		Where("drama_id = ? AND JSON_TYPE(JSON_EXTRACT(workflow_json, '$.content')) = 'OBJECT'", id).Find(&publications).Error; err != nil {
		return nil, err
	}
	if err := tx.Table("tiktok_drama_states").Select("published_snapshot AS snapshot_json").Where("drama_id=? AND published_snapshot IS NOT NULL AND JSON_TYPE(published_snapshot)='OBJECT'", id).Find(&states).Error; err != nil {
		return nil, err
	}
	snapshots := make([]consumer.Drama, 0, len(versions)+len(publications)+len(states))
	for _, value := range append(append(versions, publications...), states...) {
		var snapshot consumer.Drama
		if err := json.Unmarshal(value.SnapshotJSON, &snapshot); err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

// retainedContentEpisodeIDs 保护直连发布版本和官方任务的分集身份，草稿删集只移出编辑区。
func retainedContentEpisodeIDs(tx *gorm.DB, id string) ([]string, error) {
	snapshots, err := retainedContentSnapshots(tx, id)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, snapshot := range snapshots {
		for _, episode := range snapshot.Episodes {
			ids = append(ids, episode.ID)
		}
	}
	return ids, nil
}
