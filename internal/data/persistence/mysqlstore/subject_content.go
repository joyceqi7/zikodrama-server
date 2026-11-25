package mysqlstore

import (
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/consumer"
	"gorm.io/gorm"
)

// subjectContentQuery 将运营配置和直连状态限制在可信主体；上下文缺失时绝不读取其他主体。
func subjectContentQuery(tx *gorm.DB) *gorm.DB {
	if id := application.SubjectIDFromContext(tx.Statement.Context); id != "" {
		return tx.Where("subject_id = ?", id)
	}
	return tx.Where("1=0")
}

// catalogQuery 仅选择模型实际映射的列，不读取旧表中保留但已废弃的快照和平台标识。
func catalogQuery(tx *gorm.DB) *gorm.DB {
	return tx.Session(&gorm.Session{QueryFields: true})
}

// ownedDramaQuery 对草稿和聚合写入实施主体门禁，缺少主体时返回空集合。
func ownedDramaQuery(tx *gorm.DB) *gorm.DB {
	return catalogQuery(tx).Where("dramas.subject_id = ?", application.SubjectIDFromContext(tx.Statement.Context))
}

// ownedEpisodeQuery 从所属短剧校验分集归属，不能仅凭分集主键越过主体边界。
func ownedEpisodeQuery(tx *gorm.DB) *gorm.DB {
	return catalogQuery(tx).Where("EXISTS (SELECT 1 FROM dramas owner_drama WHERE owner_drama.id=episodes.drama_id AND owner_drama.subject_id=?)", application.SubjectIDFromContext(tx.Statement.Context))
}

// requireOwnedDrama 在写事务内确认短剧属于当前主体，不存在和跨主体统一返回未找到。
func requireOwnedDrama(tx *gorm.DB, id string) error {
	var count int64
	if err := ownedDramaQuery(tx).Model(&dramaModel{}).Where("id=?", id).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return consumer.ErrNotFound
	}
	return nil
}
