# 服务端日志文件部署

`drama-server` 通过 Styx 将同一条结构化 JSON 日志分别写入 stdout 和本地文件。stdout 继续供 `docker logs` 使用；宿主机 LogCollector 只采集文件，避免重复上传。

## 配置

`otel.log.enabled` 只控制 OTel 远程日志导出；`otel.log.file` 独立控制本地文件输出。

服务只读取所选 `config.yaml` 的 `otel.log.file`，字段结构由 Styx 的 `styx.config.v1.LogFileConfig` 定义：`directory` 可写绝对路径或当前工作目录下的相对路径，`prefix` 是文件名前缀，`rotation` 为 `day` 或 `hour`，`retention_days` 为正整数。Styx 直接把相对目录解析为进程工作目录下的绝对路径。开发、测试、生产示例分别在 `configs/config.yaml`、`configs/config.test.yaml`、`configs/config.prod.yaml`。省略 `otel.log.file` 才会只写 stdout；一旦配置，四个字段都必须有效，环境变量不会覆盖它们。测试示例按小时切割，生产示例按日切割，均保留 3 天。

文件名分别为 `<prefix>-YYYY-MM-DD.jsonl` 和 `<prefix>-YYYY-MM-DD-HH.jsonl`。切割以 UTC 自然日或小时为准，在新时段第一条日志写入时发生；过期文件在启动和切割时清理，不创建空时段文件。文件权限为 `0640`，新建目录权限为 `0750`。

`deploy.sh` 固定用 `-v` 把服务器上的 `<drama-server 仓库>/logs` 挂载到容器的 `/app/logs`，与测试和生产配置中的 `directory: ./logs` 对应。修改这两个环境的日志目录时，也必须同步调整部署挂载路径。部署脚本在替换旧容器前使用新镜像为挂载目录和已有日志文件设置 `app` 用户的所有权与写权限，保留原有的组权限；宿主机 LogCollector 仍需有读取权限。`logs/` 已从 Git 和 Docker 构建上下文排除，不要手动提交日志文件。

Styx 文件输出写入失败会在 stderr 输出实际错误，原有 stdout 日志继续输出。启动时若目录或当前文件无法打开，服务拒绝启动，避免无提示地失去文件归档。磁盘容量与保留天数应按最大日志量及 TLS 故障恢复窗口一起计算；清理仅针对当前文件名前缀且超过保留时间的文件。修改切割粒度后，旧粒度文件也按同一保留期限清理。

## BytePlus TLS 采集

在宿主机安装 LogCollector，并创建**宿主机文件**的 JSON 模式增量采集规则。采集路径由宿主机日志目录和 `otel.log.file.prefix` 组成；配置为 `./logs` 时，路径是 `<drama-server 仓库>/logs/drama-server-*.jsonl`。不要启用 Kubernetes 容器日志采集，也不要直接读取 Docker 的 `*-json.log`。在 TLS 中用原始日志的 `time` 字段解析 UTC 日志时间；先验证一条样本的时间、`level`、`msg` 和 `request_id`。解析失败日志上传与原始日志上传保持关闭，避免未经筛选的内容进入 TLS。

上线前检查业务日志中是否仍包含 Token、Cookie、签名、密钥、媒体 URL 或带敏感标识的路径；采集器的脱敏规则不能代替源头控制。按环境隔离 Topic，并监控 LogCollector 心跳、发送失败、磁盘可用空间和文件输出失败。

## 依赖版本

此功能使用 Styx `v1.10.0` 的相对目录解析，并沿用 `v1.9.0` 引入的 `LogFileConfig`、时间切割 writer 和 `Config.OTel` 配置通道。`drama-server/go.mod` 已锁定 `v1.10.0`，正式构建无需本地 Go workspace。
