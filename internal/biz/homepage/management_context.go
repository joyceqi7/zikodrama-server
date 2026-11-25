package homepage

import "context"

type managementContextKey struct{}

// WithManagementContext 仅供已鉴权的主体推荐管理入口使用，允许查询当前主体全部已上线候选；消费者入口禁止调用。
func WithManagementContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, managementContextKey{}, true)
}

// IsManagementContext 判断调用链是否来自主体推荐管理入口；缺少应用上下文本身不授予全局读取权限。
func IsManagementContext(ctx context.Context) bool {
	value, _ := ctx.Value(managementContextKey{}).(bool)
	return value
}

// WithoutManagementContext 移除当前主体管理目录读取权限，用于真实应用预览。
func WithoutManagementContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, managementContextKey{}, false)
}
