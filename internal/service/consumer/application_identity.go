package consumer

import (
	"drama-server/internal/biz/visitor"
	"regexp"
	"strings"
)

// officialAppIDPattern 保留标识符原始字符串，不做数字转换或大小写折叠。
var officialAppIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,255}$`)

// readTikTokAppID 在 HTTP 入口规范化并校验官方 AppID，应用存在性由业务层确认。
func readTikTokAppID(input string) (string, error) {
	id := strings.TrimSpace(input)
	if !officialAppIDPattern.MatchString(id) {
		return "", visitor.ErrInvalidRequest
	}
	return id, nil
}
