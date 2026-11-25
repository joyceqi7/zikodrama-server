# I18N

`i18n` 基于 `go-i18n` 提供不可变消息目录、BCP 47 / `Accept-Language`
协商、请求级 Localizer，以及 Kratos middleware 和 HTTP Filter。

```go
//go:embed messages.*.json
var messages embed.FS

bundle, err := i18n.NewBundle("en", i18n.WithMessageFiles(
    messages,
    "messages.en.json",
    "messages.zh.json",
))
```

使用 Kratos 生成的 HTTP/gRPC Handler 时安装 `i18n.Middleware(bundle)`；服务包含
通过 `HandlePrefix` 挂载的 raw `net/http.Handler` 时，必须安装
`i18n.HTTPFilter(bundle)`，使所有路由共享同一条语言上下文链路。

```go
server.NewHTTPServer(config, runtime,
    server.WithHTTPFilter(i18n.HTTPFilter(bundle)),
)
```

业务处理通过 `i18n.Localize` 获取消息。应用在认证或租户解析后得到更精确的语言策略
时，可以用 `i18n.Negotiate` 重新协商，再用 `i18n.WithLocale` 更新当前 context；已有
Bundle 会同步重建 Localizer。

数据库内容翻译、租户语言白名单和领域字段回退仍由业务项目负责，不属于 Styx 静态消息
目录。

统一 API 错误可使用 `apierror` 包；同一个错误定义在没有 Localizer 时返回默认文案，在安装
本包的请求级 Localizer 后自动返回多语言文案。
