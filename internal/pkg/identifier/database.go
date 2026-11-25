// Package identifier 校验内部数据库主键，平台标识与请求幂等键不使用此规则。
package identifier

import "strconv"

// ValidDatabaseID 仅接受非零 uint64 的规范十进制字符串，不接受符号、前导零、指数或溢出值。
func ValidDatabaseID(value string) bool {
	id, err := strconv.ParseUint(value, 10, 64)
	return err == nil && id != 0 && strconv.FormatUint(id, 10) == value
}
