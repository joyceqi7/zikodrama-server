# 入剧广告与 VIP 接口流程测试报告

## 2026-10-09：VIP 入剧自动拉起倒计时补充验证

本次在 `feat/ad` 增加可选 `entryAdCountdownSeconds`（0–300 秒）。隔离数据库 HTTP 测试通过 4 种新旧 VIP 配置、非法配置入口校验及入剧/VIP 顺序流程，耗时 18.986 秒。平台换码仍为测试替身，没有调用真实广告或修改业务库。

| 场景 | 验证结果 |
| --- | --- |
| 倒计时保存与下发 | 管理接口保存 7 秒并读回；Bootstrap 的 `monetization.vip` 与 `vipOffer.vip` 均为 7 秒，旧 `delaySeconds=5` 保持原值 |
| 缺省、新开关关闭、立即强弹 | 旧规则省略新增倒计时字段；原完播、手动领取、并发幂等、用户隔离及午夜到期流程均通过 |
| 非法倒计时 | -1、301、小数、字符串、null 在管理 HTTP 入口返回 400，已保存策略未被覆盖 |
| 服务端不执行强弹计时 | 配置倒计时后免费集仍可解析；广告完播只累计进度，达标仍须手动领取 VIP |
| Mini 自动调度 | 首次倒计时到零才拉起；提前关闭和完播未达标后从配置秒数重计，后台暂停，关闭弹窗或离页取消 |
| Mini 兼容与顺序 | 省略或 0 秒立即拉起；首页自愿领取仍手动开始；入剧广告结束后才开始 VIP 倒计时，领取前不挂载播放器 |
| Mini 并发与领取 | 点击可提前开始，同轮重复触发只拉起一次；达标停止广告并等待用户手动领取 |
| 后台配置表单 | 仅开启入剧强制领取时显示倒计时；关闭时同时清除倒计时字段，重新开启默认 0 秒；范围与类型校验通过 |

Mini 的两个聚焦文件共 25 项测试通过，另覆盖倒计时拉起失败后的停止、脱敏日志和手动恢复；后台两个聚焦文件共 10 项测试通过；服务端策略及运营 Handler 聚焦测试通过。后台和 Mini 的桌面（1280 像素）及移动（390 像素）布局已检查，无横向溢出。真实 TikTok Minis 广告真机验收、历史 Mini 二进制包验证未执行。

三个项目声明的全量验证均通过：Server `make verify`（Go 测试、vet、构建）；Mini `make verify`（383 项 Vitest、31 项 Node 测试、类型检查、构建）；Admin `npx pnpm@11.11.0 verify`（398 项 Vitest、19 项部署测试、lint、类型检查及构建）。界面检查临时文件已删除，后台 lint 对最终文件再次执行并通过；隔离 HTTP 服务、数据库与容器均已清理。全量日志为 `/tmp/drama-vip-countdown-{server,mini,admin}-verify.log`。

补充接口复跑命令（先启动文末的隔离 MySQL/Redis）：

```sh
TEST_REDIS_ADDR=127.0.0.1:36379 \
TEST_MYSQL_DSN='root:drama_local_test@tcp(127.0.0.1:33306)/?parseTime=true&loc=UTC&multiStatements=true' \
go test ./internal/data/persistence/mysqlstore \
  -run 'TestVIPCompatibilityHTTPIntegration|TestVIPEntryCountdownInvalidHTTPIntegration|TestEntryThenVIPHTTPIntegration' \
  -count=1 -v
```

日志：`/tmp/drama-vip-countdown-http-integration.log`、`/tmp/drama-vip-countdown-mini-focused.log`、`/tmp/drama-vip-countdown-admin-focused.log`、`/tmp/drama-vip-countdown-server-focused.log`。

## 测试环境与结论

- 测试日期：2026-10-08；分支：`feat/ad`。
- 使用本机隔离的 MySQL 8.4、Redis 7，以及监听本机临时端口的 HTTP 服务。
- 请求经过真实 HTTP、Handler、身份认证、业务 Service 和 MySQL 仓储；Redis 用于现有 UID 发号协议。
- 基线和全部增量 SQL 只加载到本次创建的一次性数据库。
- 通过 `POST /admin/v1/applications` 新建 IAA 测试应用，先确认其策略为空，再通过管理接口保存测试策略。
- 测试应用官方 AppID 为字符串 `9007199254740993123`，使用 `Asia/Shanghai` 时区。预置两部已发布的测试剧和各两集内容。
- 新增的 12 组流程用例全部通过，另有 3 项现有 HTTP 集成回归通过。完整集成命令耗时 17.648 秒。
- Mini 的 4 个相关测试文件、56 项测试通过；服务端 `make verify` 通过。
- 本次新增测试和报告，并为既有测试装配增加业务时钟注入；没有修改业务实现。

## 接口调用链

| 步骤 | 接口 | 验证内容 |
| --- | --- | --- |
| 管理登录 | `POST /admin/v1/session` | 获取测试管理员身份 |
| 创建应用 | `POST /admin/v1/applications` | 创建带激励/插屏广告位的新应用，长数字 ID 保留为字符串 |
| 读取策略 | `GET /admin/v1/application-strategy` | 新应用 `strategy=null` |
| 配置策略 | `PUT /admin/v1/application-strategy` | 配置入剧模式、次数和 VIP 开关，并 GET 读回 |
| 用户登录及落地 | `POST /api/v1/bootstrap` | 身份、当前策略、VIP 状态、VIP Offer、剧集落地目标 |
| 入剧进度 | `GET /api/v1/me/entry-ad` | 当前模式、范围、当天进度、N、午夜截止和入口 |
| 展示事实 | `POST /api/v1/me/ad-shows` | 上报场景和可选剧目/剧集；同一 `eventId` 幂等 |
| 展示查询 | `GET /api/v1/me/ad-shows` | 按场景、可选剧目/剧集读取当天事实 |
| 完播结算 | `POST /api/v1/reward-grants` | `isEnded=true` 后按 `claimId` 幂等结算；首次 201，重试 200 |
| 手动领取 VIP | `POST /api/v1/vip/claim` | 未达标 409；达标后领取，重复请求 200 |
| 播放解析 | `POST /api/v1/play/resolve` | 免费集、未领取 VIP 的锁定集、领取后的锁定集 |

## 场景结果

以下各行均已执行并通过，N 常规设置为 2。

| 编号 | 场景 | 实际结果 |
| --- | --- | --- |
| 01 | 应用未配置策略 | Bootstrap 返回 `disabled`，VIP Offer 为空、入剧 `enabled=false`；通过管理 API 补配置后立即生效 |
| 02 | 每人每天展示 N 次 | 两部剧各展示一次，同一用户的入剧 `shownCount=2`；可不传剧目读取全局进度 |
| 03 | 每部剧每天展示 N 次 | 两部剧各展示一次，各自 `shownCount=1`；当前剧再展示一次达到 2，另一部仍为 1 |
| 04 | 每人每天看完 N 次 | 两部剧各成功结算一次，全局 `completedCount=2`；展示事实不充当完播 |
| 05 | 每部剧每天看完 N 次 | 两部剧各成功结算一次，各自 `completedCount=1`；当前剧再结算一次达到 2 |
| 06 | 仅展示、提前关闭 | 成功展示增加展示进度；`isEnded=false` 的奖励请求返回 409，奖励账本和完播次数均不增加 |
| 07 | 展示和完播并发重试 | 同一展示事实 8 次并发重放仍只累计一次；同一完播 ClaimID 8 次并发请求只结算一次 |
| 08 | 展示事实不可变 | 原 `eventId` 改为另一部剧重新提交返回 400，原始事实保持不变 |
| 09 | 用户和应用隔离 | 用户 B 的次数为零；其他应用看不到当前应用展示事实，使用当前应用的 Offer 领奖返回 409 |
| 10 | VIP 旧配置缺少新开关 | 管理保存和 Bootstrap 成功；消费响应省略 `requireBeforePlayback`；原 VIP 广告和领取接口正常 |
| 11 | VIP 显式关闭新开关 | 消费响应同样省略新字段，旧字段 `requiredAdCount`、`forceWatchCount`、`delaySeconds` 保留 |
| 12 | VIP 开启强制开关 | Bootstrap 下发 `requireBeforePlayback=true`，VIP 进度与领取接口沿用原合同 |
| 13 | VIP 展示但未看完 | 上报 `vip_reward` 展示不增加 `vipState.adCount`；提前关闭不结算 |
| 14 | VIP 未达标领取 | 0 次和 1 次成功完播时领取均返回 409，不产生 VIP 权益 |
| 15 | VIP 达标但尚未领取 | 第 2 次完播后 `adCount=2`、`active=false`；只读启动不自动领取，锁定集仍不可播放 |
| 16 | 领取 VIP、并发重复领取 | 8 次并发领取均成功，只产生一笔 VIP 权益；启动返回 `active=true`、VIP Offer 为空，锁定集可播放 |
| 17 | VIP 达标后继续提交新广告 | 新 ClaimID 返回 409，VIP 奖励账本保持 2 笔 |
| 18 | 入剧和 VIP 同时开启 | 按 Mini 流程先结算 2 次入剧，再结算 2 次 VIP，最后手动领取；两份进度独立，领取后锁定集可播放 |
| 19 | 已有 VIP 仍执行入剧策略 | 把入剧 N 从 2 改为 3，已领取 VIP 的用户仍返回入剧 `completedCount=2`、`requiredCount=3`；补一条完播后达到 3 |
| 20 | 动态开关及旧 Offer | VIP 新开关从缺省变为 true，广告开始时取得的旧修订 Offer 仍可按当前规则结算，已完成 ClaimID 可重试 |
| 21 | 入剧规则停用 | 入剧状态立即变为关闭，既有已领取 VIP 继续有效 |
| 22 | 当地午夜重置 | 把业务时钟推进到上海午夜，入剧次数归零；VIP 进度归零、权益过期并重新提供 Offer |
| 23 | 跨午夜回补和奖励重放 | 午夜后补报前一天 `occurredAt` 的展示、重放前一天已成功 ClaimID，均不累计到新一天 |
| 24 | 重新连接及 HTTP 服务重建 | 原 Token 仍能查询进度，重新登录恢复原用户，策略、3 次入剧事实和已领取 VIP 均保留 |
| 25 | N 的范围和真实最大值 | N=1、N=100 可保存；展示及完播分别通过 99 次到 100 次边界，未被旧的 10 次上限截断 |
| 26 | 非法策略保存 | N=0、N=101、非法 scope/countBy、null scope、空 countBy、在入剧规则加入 VIP 开关，共 7 类请求返回 400，原 N=100 配置保留 |
| 27 | 首页等通用场景复用 | 没有策略、没有剧目时 `home_entry` 可以记录并查询 2 次；入剧、VIP、剧集场景各自独立，不发放奖励或权益 |
| 28 | 可选剧集及插屏广告位 | 绑定插屏位可以记录剧集展示；按场景+剧目+剧集查询返回 1 次 |
| 29 | 非法展示输入 | 未绑定广告位、缺少剧目的剧集、非法场景、超过 48 小时的时间返回 400；剧集与剧目不匹配返回 404 |
| 30 | 展示表真实写入故障 | SQL 触发器注入故障，展示请求返回 500；完整观看仍可独立结算。移除故障后用原 eventId 回补，8 次重放仍只记录一次 |
| 31 | 奖励账本真实写入故障 | 请求返回 503，原先 1 次完播保持不变；修复后使用原 ClaimID 首次返回 201、重复返回 200，最终为 2 次 |
| 32 | VIP 权益事务真实故障 | 达标后的领取返回 503；权益及投影均零写入，2 次广告进度保留；修复后重新领取成功，旧广告请求重放不重复发权益 |
| 33 | 故障日志 | 记录具体展示/奖励/VIP 发放操作和实际 SQL 故障；断言日志不含消费者或管理员 Token |
| 34 | 业务接口鉴权 | 入剧进度、展示上报、奖励结算、VIP 领取四个接口缺少凭据均返回 401 |

## 现有流程回归

复跑并通过：`TestIAAHTTPIntegrationOfficialAppID`、`TestCurrentSchemaIntegrationApplicationStrategy`、`TestIAAHTTPIntegration`。其中原 IAA 流程还覆盖：

- 未完播拒绝、8 次并发领奖只入账一次。
- 权益关联到实际数据库记录、重新登录恢复、账号及应用隔离。
- 管理配置修改、旧 Offer 按最新规则结算。
- 不同领取键并发不能突破每日上限。
- 数据库已经提交但 HTTP 响应丢失时，原 ClaimID 恢复结果。
- 多集权益发放中途失败，奖励及权益整笔回滚，原 ClaimID 可重试。
- 数据库连接重建后保持身份和权益。
- 已发布快照撤回后，历史权益不能绕过内容可见性校验。

## Mini 的补充验证

HTTP 接口返回规则、事实及权益；策略执行顺序和自动拉起由 Mini 实现。以下 56 项相关测试全部通过：

```sh
npx vitest run src/pages/theater/EntryAdGate.test.tsx \
  src/components/VipDialog.test.tsx \
  src/api/httpMiniDramaApi.test.ts \
  src/runtime/runtime.test.ts
```

其中覆盖四种计数模式、展示模式提前关闭后重拉、完整观看模式未达标重拉、入剧先于 VIP、旧 VIP 配置不强制、新开关自动拉起、手动领取后挂载播放器、广告未退出时不播放、后台回来继续、午夜重新确认、身份切换、待确认事实与奖励回补。

## 验证边界

- 平台换码使用测试替身，广告事实和 `isEnded` 由测试请求提交；未调用真实 TikTok、BytePlus 或进行真机广告观看。
- 服务端免费集在 VIP 强制开关开启时仍可解析为可播放，符合由 Mini 执行入剧/VIP 门禁的要求。
- HTTP 顺序用例验证按 Mini 顺序执行的状态结果，Mini 补充测试验证实际先后调度和自动重拉；服务端不强制广告的先后顺序。
- VIP 兼容验证覆盖缺省/关闭/开启字段合同和旧修订 Offer，未运行历史 Mini 安装包或旧版服务端。
- 首页仅测试通用展示事实接口的复用，没有增加首页门禁产品功能。
- 测试数据库、HTTP 服务及临时容器在结束后清理；没有变更开发业务库或生产应用配置。

## 复跑方式

在 `drama-server` 目录运行现有隔离集成环境：

```sh
docker compose -f internal/testsupport/compose.integration.yaml up -d --wait mysql redis
TEST_REDIS_ADDR=127.0.0.1:36379 \
TEST_MYSQL_DSN='root:drama_local_test@tcp(127.0.0.1:33306)/?parseTime=true&loc=UTC&multiStatements=true' \
go test ./internal/data/persistence/mysqlstore \
  -run 'Test(EntryFourModesHTTP|VIPCompatibilityHTTP|EntryThenVIPHTTP|VIPClaimHTTPRecovery|EntryPolicyHTTPBoundary|AdSceneHTTPReuse|AdFactsHTTPRecovery)Integration$' \
  -count=1 -v
make verify
docker compose -f internal/testsupport/compose.integration.yaml down --volumes --remove-orphans
```

仅 `go test ./...` 未配置集成环境时会跳过 MySQL/Redis 用例。本报告的接口结果来自显式配置隔离数据库后执行的集成命令。

本次日志位于：

- `/tmp/drama-entry-vip-http-integration.log`
- `/tmp/drama-entry-vip-mini-focused.log`
- `/tmp/drama-entry-vip-server-verify.log`
