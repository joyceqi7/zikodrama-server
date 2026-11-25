package mysql

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

type mysqlSQLContextKey struct{}

// registerMySQLSQLCallbacks 在 GORM 完成语句构造后保存可记录的 SQL 模板。
// Scan 使用 GORM 自带的 Recorder，不能依赖 logger.ParamsFilter 获取未插值的 SQL。
func registerMySQLSQLCallbacks(db *gorm.DB) error {
	callbacks := []struct {
		name     string
		register func(string, func(*gorm.DB)) error
	}{
		{"create", db.Callback().Create().After("*").Register},
		{"query", db.Callback().Query().After("*").Register},
		{"update", db.Callback().Update().After("*").Register},
		{"delete", db.Callback().Delete().After("*").Register},
		{"row", db.Callback().Row().After("*").Register},
		{"raw", db.Callback().Raw().After("*").Register},
	}
	for _, callback := range callbacks {
		if err := callback.register("styx:sql_log:"+callback.name, captureMySQLSQL); err != nil {
			return fmt.Errorf("register %s callback: %w", callback.name, err)
		}
	}
	return nil
}

// captureMySQLSQL 仅保存当前语句的模板；每次执行都覆盖旧值，避免复用 Session 时串日志。
func captureMySQLSQL(db *gorm.DB) {
	if db.Statement == nil || db.Statement.Context == nil {
		return
	}
	safeSQL := redactMySQLSQL(db.Statement.SQL.String())
	db.Statement.Context = context.WithValue(db.Statement.Context, mysqlSQLContextKey{}, safeSQL)
}

// redactMySQLSQL 隐去内联字符串、数字和注释，保留标识符与占位符以便定位慢查询。
// SQL 不完整时不记录模板，避免把残余的字面量写入日志。
func redactMySQLSQL(sql string) string {
	if sql == "" {
		return ""
	}
	var out strings.Builder
	out.Grow(len(sql))
	for i := 0; i < len(sql); {
		switch {
		case sql[i] == '\'' || sql[i] == '"':
			quote := sql[i]
			out.WriteByte('?')
			i++
			closed := false
			for i < len(sql) {
				if sql[i] == '\\' {
					i += 2
					continue
				}
				if sql[i] == quote {
					i++
					if i < len(sql) && sql[i] == quote {
						i++
						continue
					}
					closed = true
					break
				}
				i++
			}
			if !closed || i > len(sql) {
				return ""
			}
		case sql[i] == '`':
			start := i
			i++
			closed := false
			for i < len(sql) {
				if sql[i] == '`' {
					i++
					if i < len(sql) && sql[i] == '`' {
						i++
						continue
					}
					closed = true
					break
				}
				i++
			}
			if !closed {
				return ""
			}
			out.WriteString(sql[start:i])
		case sql[i] == '/' && i+1 < len(sql) && sql[i+1] == '*':
			end := strings.Index(sql[i+2:], "*/")
			if end < 0 {
				return ""
			}
			i += end + 4
			out.WriteByte(' ')
		case sql[i] == '#' || sql[i] == '-' && i+1 < len(sql) && sql[i+1] == '-' &&
			(i+2 == len(sql) || sql[i+2] <= ' '):
			for i < len(sql) && sql[i] != '\n' {
				i++
			}
			out.WriteByte(' ')
		case sql[i] >= '0' && sql[i] <= '9' &&
			(i == 0 || !isSQLIdentifierByte(sql[i-1])):
			out.WriteByte('?')
			i++
			for i < len(sql) && (isSQLIdentifierByte(sql[i]) || sql[i] == '.') {
				i++
			}
		default:
			out.WriteByte(sql[i])
			i++
		}
	}
	return out.String()
}

func isSQLIdentifierByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' ||
		b >= '0' && b <= '9' || b == '_'
}
