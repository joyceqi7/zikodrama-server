package mysqlstore

import (
	"database/sql"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newTestStore 仅把 sqlmock 连接包装为 GORM，正式代码只能使用 Styx OpenData。
func newTestStore(db *sql.DB) *Store {
	if db == nil {
		return NewStore(&Data{})
	}
	orm, err := gorm.Open(mysql.New(mysql.Config{Conn: db, SkipInitializeWithVersion: true}), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return NewStore(&Data{pool: db})
	}
	return NewStore(&Data{db: orm, pool: db})
}
