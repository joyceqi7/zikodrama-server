# Middleware

所有中间件都接收 `observe.Dependencies`，不读取进程级全局 Provider。

- `HTTPAccessLog(dependencies)`：记录 `method`、`path`、`client_ip`、`status`、`duration_ms` 和 `request_id`。`path` 使用应用改写之前的原始转义路径，404 同样记录，不包含查询参数；路径中的标识符或 Token 不会脱敏。毫秒耗时保留小数，4xx 使用 WARN、5xx 使用 ERROR，其余状态使用 INFO。优先采用应用最终响应中的安全 `X-Request-ID`；没有应用标识时复用安全的上游值，或生成新的随机标识写入响应头。`client_ip` 优先采用 Nginx 或网关覆盖写入且格式有效的 `X-Real-IP`，否则回退到 TCP 对端地址；业务服务不处理代理信任与转发链。
- `HTTPTracing(dependencies)`：使用显式 Propagator 和 TracerProvider。
- `Logging(dependencies)`：记录 RPC operation、code、reason 和耗时。
- `ClientLogging(dependencies)`：只记录失败调用的结构化元数据。
- `Recovery(serviceName, dependencies)`：恢复 panic 并累计低基数指标。
- `Validate()`：执行 Protobuf 参数校验。

请求标识统一由 `requestmeta.HTTPMiddleware` 校验和生成，并写入 Context 与 `X-Request-ID` 响应头；业务代码通过 `requestmeta.RequestID(ctx)` 或 `requestmeta.LogFields(ctx)` 读取。`HTTPAccessLog` 单独使用时也装配该中间件，已有 Context 标识不会再次生成。应用应直接复用框架标识；为兼容已有应用，访问日志仍优先记录实际发送的安全响应标识。

访问日志不采集其他 header、query、panic 值和堆栈。请求和响应正文各最多保存前 4 KiB；请求正文随业务读取采集，未读取时不输出正文。完整合法 JSON 直接写入 `request_body` 或 `response_body`；其他 UTF-8 文本作为字符串，非 UTF-8 字节写入 `*_body_base64`。超过上限或业务未读完请求正文时，直接输出已采集的前缀，不附加状态字段或提示，也不把前缀当作完整 JSON 解析。正文不脱敏，可能包含凭据或业务数据，日志存储和访问权限需据此管理。链路字段仍由日志器按 Context 注入；未启用链路追踪时也可通过 `request_id` 关联请求。`HTTPAccessLog` 应位于 `HTTPRecovery` 外层，以便记录恢复后的 500；Styx HTTP Server 已按此顺序装配。HTTP ResponseWriter 包装保留 Flush、Hijack、Push 和 Unwrap 能力。
