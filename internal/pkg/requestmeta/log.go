// Package requestmeta 为业务层转发 Styx 日志关联字段，隔离框架依赖。
package requestmeta

import (
	"context"

	styxrequestmeta "gitee.com/linpbin/styx/requestmeta"
)

// LogFields 返回 Styx Context 中的 request_id 和 trace_id 键值对；不生成、校验或保存请求标识。
func LogFields(ctx context.Context) []any {
	return styxrequestmeta.LogFields(ctx)
}
