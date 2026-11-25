# Nacos Registry

```go
registry, err := nacos.NewRegistry(ctx, config, runtime)
```

同一服务的 HTTP 和 gRPC 端点都注册在原始服务名下，协议保存在实例 metadata 的 `kind` 字段中。Kratos resolver 会按 endpoint scheme 选择匹配协议。

Register 会先验证全部端点；批量注册中途失败时回滚已注册实例。GetService 和 Watch 使用相同 group、cluster 和 healthy 条件。支持 IPv4、IPv6，Watcher 会传递订阅回调错误且 Stop 幂等。

用户名/密码、AccessKey/SecretKey 必须成对配置。Nacos SDK 的日志适配器绑定 Runtime 的显式 Logger；Registry 自动接入 Runtime 的 Registrar、Discovery 和关闭流程。
