package mysqlstore

import (
	"errors"

	mysqldriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"

	"drama-server/internal/biz/adminrbac"
)

func adminMutationResult(result *gorm.DB) error {
	if err := adminError(result.Error); err != nil {
		return err
	}
	if result.RowsAffected == 0 {
		return adminrbac.ErrNotFound
	}
	return nil
}

func adminError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return adminrbac.ErrNotFound
	}
	var mysqlError *mysqldriver.MySQLError
	if errors.As(err, &mysqlError) && (mysqlError.Number == 1062 || mysqlError.Number == 1451 || mysqlError.Number == 1452) {
		return adminrbac.ErrConflict
	}
	return err
}
