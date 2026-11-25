# API Error

`apierror` 把 HTTP 状态、稳定错误码和消息键注册为一个定义。业务 Handler 只上报错误，
`HTTPFilter` 在统一响应出口编码 `{"error":{"code":"...","message":"..."}}`。

```go
var InvalidRequest = apierror.MustCatalog(
    http.StatusBadRequest,
    "invalid_request",
)

apierror.WriteHTTP(request.Context(), response, InvalidRequest)
```

服务端统一安装语言和错误 Filter：

```go
styxserver.WithHTTPFilter(styxi18n.HTTPFilter(messages))
styxserver.WithHTTPFilter(apierror.HTTPFilter())
```

单语言服务只加载一个语言目录；多语言服务加载多个目录。两种模式的业务定义和 Handler
调用完全相同。错误翻译只维护在消息目录中，业务错误映射、日志记录和内部错误脱敏仍由
应用负责。

默认使用稳定错误码作为消息键；仅在二者必须不同时使用 `MustCatalogWithMessageID`。
带默认文案的 `Must` 保留给不使用统一 Filter 的兼容场景，新服务应使用 `MustCatalog`。
