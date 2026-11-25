package mysqlstore

import (
	"drama-server/internal/biz/operations"
	"gorm.io/gorm"
)

// touchDramaOperator 与业务写入共用事务；无可信管理员上下文的后台任务不会覆盖人工操作人。
// 调用方须先锁定剧目或以同一行更新完成业务写入，所有操作保持剧目在关联任务之前的锁顺序。
func touchDramaOperator(tx *gorm.DB, id string) error {
	actor := operations.PublicationActor(tx.Statement.Context)
	if actor == "" || id == "" {
		return nil
	}
	return tx.Model(&dramaModel{}).Where("id = ?", id).UpdateColumn("updated_by", actor).Error
}
