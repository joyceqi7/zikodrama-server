package mysqlstore

import (
	"strconv"

	"drama-server/internal/biz/adminrbac"
)

// parseAdminID 把规范的正十进制字符串转换为 uint64；拒绝零、前导零、正号、空白和溢出，统一返回 adminrbac.ErrNotFound，避免 MySQL 隐式转换误命中。
func parseAdminID(id string) (uint64, error) {
	value, err := strconv.ParseUint(id, 10, 64)
	if err != nil || value == 0 || strconv.FormatUint(value, 10) != id {
		return 0, adminrbac.ErrNotFound
	}
	return value, nil
}

// parseAdminIDs 在删除旧关联前解析全部 ID，避免后续发现非法主键；保留顺序和重复项，不替代 Service 去重及存在性检查。
func parseAdminIDs(ids []string) ([]uint64, error) {
	values := make([]uint64, 0, len(ids))
	for _, id := range ids {
		value, err := parseAdminID(id)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}
