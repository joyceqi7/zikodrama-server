# Elasticsearch

```go
client, err := elasticsearch.NewElasticsearch(ctx, "default", config, runtime)
```

工厂创建受治理的 `http.Transport`：包含 Dial、TLS 握手、响应头和空闲连接超时，并启用 HTTP/2。初始化 Ping 使用调用方 Context；Runtime 会关闭空闲连接。

OTel Instrumentation 绑定显式 TracerProvider，且不会把搜索请求体写入 Span。TLS 客户端证书与私钥必须成对配置，最低 TLS 1.2。
