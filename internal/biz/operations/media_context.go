package operations

import "context"

type mediaSpaceKey struct{}

// WithMediaSpace 仅供服务端 Worker 安装任务冻结的空间，历史任务不能随主体配置改写目标。
func WithMediaSpace(ctx context.Context, space string) context.Context {
	return context.WithValue(ctx, mediaSpaceKey{}, space)
}

// MediaSpaceFromContext 返回任务冻结的空间；普通上传请求为空，必须读取当前主体配置。
func MediaSpaceFromContext(ctx context.Context) string {
	space, _ := ctx.Value(mediaSpaceKey{}).(string)
	return space
}
