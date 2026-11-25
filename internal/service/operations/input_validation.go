package operations

import (
	"drama-server/internal/pkg/identifier"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"drama-server/internal/biz/operations"
	"drama-server/internal/pkg/apierr"
)

// rejectInvalidInput 在入口校验失败时记录实际错误；广告配置未配齐返回可操作的专用提示。
func rejectInvalidInput(w http.ResponseWriter, r *http.Request, operation string, err error) bool {
	if err == nil {
		return false
	}
	slog.WarnContext(r.Context(), operation, "error", err)
	if errors.Is(err, operations.ErrApplicationAdUnitsIncomplete) {
		writeError(w, r, apierr.OperationsApplicationAdUnitsIncomplete)
	} else {
		writeError(w, r, apierr.OperationsInvalidRequest)
	}
	return true
}

// validatePathIDs 规范化并校验数据库自增主键的十进制字符串，拒绝 MySQL 可能隐式转换的输入。
func validatePathIDs(w http.ResponseWriter, r *http.Request, names ...string) bool {
	for _, name := range names {
		value := strings.TrimSpace(r.PathValue(name))
		if !identifier.ValidDatabaseID(value) {
			rejectInvalidInput(w, r, "path parameter rejected", operations.ErrInvalidInput)
			return false
		}
		// 写回清理后的路径值，后续 Handler 与 Service 使用同一字符串标识。
		r.SetPathValue(name, value)
	}
	return true
}
