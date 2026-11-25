# drama-server

当前版本已移除字幕与多语言：消费者界面固定英语，管理端固定中文，内容只维护主文案；原始音频语言仍作为官方发布元数据。
部署要求见 [单语言版本部署](docs/single-language-rollout.md)。服务端日志文件的切割、保留和 TLS 采集路径见 [日志文件部署](docs/log-file-deployment.md)。

TikTok Minis 官方短剧系统的 Go 服务端。

官方与独立 VePlayer 双通道已在 feat/veplayer 实现，消费者路径保持不变，旧官方应用继续使用原合同。设计与功能清单见 [完整实现方案](docs/veplayer-implementation-plan.md)，迁移、配置、验证与回滚见 [实施与上线手册](docs/veplayer-release-runbook.md)。直连默认关闭，真实 License 和 TikTok 容器验收完成后才开放。

## 运行单元

- HTTP：消费者接口、运营接口、可选 BytePlus 回调和健康检查。
- Worker：源媒资确认、官方视频登记与转码、送审轮询、上线及逐应用授权。

同一进程通过 Styx 生命周期启动 HTTP 与 Worker。多实例共享 MySQL 任务租约、发布检查点和修订号，不引入独立消息队列。Redis 保存可重建的内容缓存，以及由 MySQL 永久预留号段支持的 UID 共享发号进度；新用户发号需要 Redis 可用，详见 [UID 分配与恢复](docs/uid-allocation.md)。

当前流程和实施边界见工作区的 [官方方案重构说明](../docs/2026-09-05-official-refactor.md)。

## 开发

```bash
make test
make verify
```

默认配置只允许本地开发，不包含真实 TikTok、BytePlus 或数据库凭据。

### Styx 请求标识与正式构建

请求 ID 的生成、上游校验、响应头和 Context 均由 Styx 管理，Drama 不再生成请求 ID。业务层通过 `internal/pkg/requestmeta.LogFields` 转发 Styx 的日志字段，以遵守业务层不直接依赖框架的约束；该适配不维护标识或校验规则。关闭访问日志和链路追踪后，请求标识仍有效。

Styx `v1.7.0` 已提供该能力，正式构建使用 `go.mod` 固定的版本，并关闭本地 Go workspace：

```bash
GOWORK=off make verify
```

Docker 构建固定 `GOWORK=off`，构建上下文排除 `go.work` 与 `go.work.sum`。需要开发 Styx 时，可在本仓库执行 `go work init . ../../styx` 联调相邻源码；该文件不提交，正式构建仍须使用上述命令验证已发布依赖。

### 数据库集成验证

需要验证真实 MySQL Schema、仓储事务、发布版本隔离和媒资引用时，启动仅绑定本机端口、使用临时文件系统且不保留数据的隔离容器：

```bash
make integration-up
make test-integration-local
make integration-down
```

`integration-down` 只停止 `drama-integration` Compose 项目；不操作其他本地 MySQL、Redis 或数据卷。

## IAA 本机集成验证（无需真机）

`make test-integration-local` 同时执行 Schema、`TestIAAHTTPIntegration` 和 `TestIAAProcessRestartIntegration`。每次生成独立 `drama_iaa_*` 数据库，执行部署 Schema，结束仅删除本次创建的库；测试使用真实 HTTP Handler、管理员登录、Visitor 身份、业务服务和 MySQL，平台换码使用固定测试账号替身，不调用 TikTok／BytePlus。

HTTP 用例覆盖：未完整观看拒绝；同领取键八次并发只入账一次；不同领取键并发不突破每日上限；账号和应用隔离；管理端免费范围、解锁集数、每日上限联动；旧 Offer 拒绝与已入账旧领取恢复；提交后响应丢失；第二集权益写入失败时整笔回滚及错误日志；数据库断连恢复；线上快照撤回后不可凭旧权益起播。进程用例实际退出并重启后端，验证原凭据、已解锁权益和领取幂等性。

需要浏览器联调时，可选运行下列测试服务（只监听 `127.0.0.1:18080`；应先确认端口空闲）：

```bash
TEST_MYSQL_DSN='root:drama_local_test@tcp(127.0.0.1:33306)/?parseTime=true&loc=UTC&multiStatements=true' \
TEST_IAA_BROWSER=1 go test ./internal/data/persistence/mysqlstore \
  -run '^TestIAABrowserIntegration$' -v -count=1 -timeout=30m
```

该测试打印 `controller_pid`，向它发送 `SIGUSR1` 可重启后端并保留测试库；发送 `SIGINT` 正常结束并清理测试库。预置应用 `iaa-a`、`iaa-b`，测试登录码 `user-a`、`user-b`、`user-c`，本机管理员为 `iaa-admin`／`LocalIAA123!`。这些固定测试值只存在测试代码，不用于产品配置。H5 必须关闭 Demo API，平台 SDK 和播放器另用开发替身；管理端指向同源 Vite 代理。真实 SDK、广告填充、VOD 播放、官方送审／下架和回传接收仍需平台环境验收。

## 管理员 JWT 密钥

部署配置必须提供 `business.admin_jwt_signing_secret`（至少 32 字节），所有 API 实例使用同一个值；它仅用于管理员 HS256 JWT，不从消费者 `identity_token_encryption_secret` 派生，也不允许两者相同。示例配置中的 `local-development-` / `test-only-` 值仅供本地或测试使用，预发和生产会拒绝这些值。可在受控环境使用 `openssl rand -hex 32` 生成独立随机密钥，再通过部署配置安全注入，不要提交到 Git。

首次切换独立签名密钥或后续更换该密钥，会使已签发的管理员 JWT 失效，管理员需重新登录；消费者 VisitorToken 不受影响。管理员 JWT 默认有效期为 2 小时，角色权限继续实时读取，退出登录、禁用和密码重置继续通过认证版本撤销旧凭据。

管理员客户端在到期前五分钟调用 `POST /admin/v1/session/refresh` 自动续期，返回与登录相同的结构。续期只接受仍有效且认证版本匹配的 JWT，每次签发有效期两小时；已过期、禁用或撤销的凭据返回 401，客户端清除会话并显示登录页。
