# 服务端部署

## VePlayer 双通道升级

feat/veplayer 的迁移、公开配置、任务职责、验证证据和回滚步骤见 [实施与上线手册](veplayer-release-runbook.md)。先按 sql/README.md 执行全部尚未应用的增量，再部署当前三端和全部 Worker；播放通道由应用 provider 决定，历史应用保持官方，切换需通过已升级的运营接口按修订操作。业务库 SQL、真实 License 和目标容器测试须按环境完成，本次本机验证不等同部署。

每个实例在 YAML 显式配置 business.direct_playback_token_ttl_seconds（60～900 秒，示例 300 秒）；缺失或越界拒绝启动，修改后重启全部实例。播放签名不再读取 MySQL playback.default，SDK AppID 由 Mini 随包 License 决定，服务端与 Mini 启动合同须一起升级。每个实例运行媒体、官方发布和指标维护任务，媒体与官方发布任务通过 MySQL 租约协调领取。直连上下架同步完成。所有承载直连流量的实例需要一致配置，旧清理 Worker 未全部升级前不能开启直连写入。

冻结 sql/init.sql 不随迭代改写，新库执行基线与全部增量，已有库只执行尚未应用的增量。回滚不能移除新引用保护或恢复旧清理程序，不能清空 Redis 或重置 UID；本节覆盖下文早期部署记录中“只执行 init.sql”的旧说明。

服务器具备 Docker、Git、OpenSSH、可读取 Gitee 私有依赖的部署密钥及对应环境配置后，拉取仓库并执行即可：

```bash
./deploy.sh test    # 默认环境，也可以直接执行 ./deploy.sh
./deploy.sh prod
```

`deploy.sh` 包含拉取、构建和启动容器的完整流程。拉取环境分支后重新执行自身，使用内部标记跳过第二次拉取，确保使用更新后的部署逻辑与 Dockerfile。测试使用 `test` 分支和 `configs/config.test.yaml`，生产使用 `main` 分支和 `configs/config.prod.yaml`。Git 更新只允许快进，拉取或构建失败不会停止现有容器。部署成功后保留当前环境最新 5 个不同版本镜像。数据库建表 SQL 单独位于 `sql/`，部署脚本不执行 SQL 或业务初始化。

从旧的双脚本版本首次升级时，先在对应环境分支执行 `git pull --ff-only`，再执行 `bash deploy.sh test`（生产使用 `prod`）。这样入口先更新为单脚本版本，避免旧入口在拉取后尝试启动已删除的运行脚本。后续部署只需执行 `deploy.sh`。

## 数据库外键移除

数据库禁止使用外键。新库使用 `sql/init.sql`；已有库在发布本版事务关联校验代码时，先对比实际结构，根据差异生成并审核移除业务外键的 DDL，保留数据和索引；部署脚本不会自动执行。删除短剧、角色与权限的关联处理由应用事务显式完成。

## 独立网关与测试域名

测试环境 API 地址为 `https://testservice.tkdrama.site`。`drama-server` 与 `drama-admin` 同机部署，入口配置统一由独立 Docker 工程 [drama-nginx](../../drama-nginx/README.md) 管理，网关容器通过 host 网络转发到本机 `18080`。本工程不再保存域名或证书配置，`deploy.sh` 仅更新 API 容器，不安装、重载宿主机 Nginx。

初次迁移时，先按 `drama-nginx` 的说明将证书放入服务器工程 `certs/` 并发布网关，让它接管后台同源 `/admin/` 代理，再发布新版 admin；现有 server 容器可以继续提供 API。随后分别执行 admin、server 的原部署命令即可。域名、证书或回源地址变化时单独发布网关，无需重新构建业务镜像。

测试仍通过 `18080:18080` 发布容器端口，兼容现有调用方；生产继续使用 host 网络。API 路径及回调路径保持不变，业务 Handler 继续负责鉴权与参数校验。网关的 11 MiB 请求体限制覆盖封面文件及 multipart 开销，视频仍由浏览器直传 BytePlus。

```bash
curl --fail --show-error http://127.0.0.1:18080/api/v1/health
curl --fail --show-error https://testservice.tkdrama.site/api/v1/health
```

## SSH 构建认证

测试环境使用 Docker bridge 网络，并通过 `18080:18080` 发布 API 端口；`configs/config.test.yaml` 中服务监听 `:18080`。数据库和 Redis 地址须可从容器访问，不能使用指向容器自身的 `127.0.0.1`。生产环境继续使用 host 网络。修改网络模式后需重新部署，单纯重启旧容器不会新增端口映射。

运维负责在服务器上为部署账号配置私钥，以及包含 Gitee 主机记录的 `~/.ssh/known_hosts`。构建直接使用私钥文件，默认路径为 `~/.ssh/id_rsa`，其他路径通过 `DEPLOY_SSH_KEY` 指定；不依赖宿主机的 SSH Agent 或 `SSH_AUTH_SOCK`。密钥需具有 `gitee.com/linpbin/styx` 私有依赖的读取权限。使用其他主机记录文件时，通过 `DEPLOY_KNOWN_HOSTS_FILE` 指定路径。

```bash
# 使用自定义私钥时，由运维在部署账号的服务器环境中持久配置一次。
export DEPLOY_SSH_KEY=/path/to/private-key
# 此后每次部署只执行：
./deploy.sh test
```

这里应填写服务器 SSH 配置中对应 `IdentityFile` 的实际路径。[Docker 的私钥文件方式](https://docs.docker.com/reference/dockerfile/#run---mounttypessh)不支持带解密口令的私钥；密钥和口令策略由运维管理。

部署脚本通过 `--ssh default=私钥路径` 将现有私钥交给 BuildKit，在构建期间提供 SSH 签名能力，私钥文件不复制进镜像；known_hosts 通过临时 secret 挂载。脚本不做前置环境检查；失败时保留 Git、Docker 返回的实际错误，不继续重启容器。脚本不管理 Agent、不修改 SSH 配置，仓库不保存任何 SSH 密钥。Dockerfile 在依赖下载和编译两个步骤都挂载同名 `gitee_known_hosts` 文件，开启严格主机校验。

配置文件通过只读挂载传给容器。生产配置中的连接参数和密钥需在部署前填写；容器不会自动继承宿主机导出的环境变量。构建使用的 SSH 凭据不提供运行时业务配置。

消费者用户 UID 由 `id_allocators` 表的 `consumer_domain` 命名空间永久预留号段，所有服务实例通过同一 Redis 共享当前段、备用段和发号进度，消耗到 80% 时异步预取，不需要实例机器号。应用每次发布或重启不得清除 UID 缓存，也不会为每个实例重新申请号段。首次部署时只在空库执行一次当前 `sql/init.sql`，创建号段表和初始水位，具体步骤见 `sql/README.md`。新增实例不能重复初始化数据库；号段一经预留，即使未全部使用也不得回收，不能在保留旧标识的库中把水位重置为 1。奖励、权益和 VIP 进度等内部记录改用物理表自增后，已有水位也必须保留。

新用户发号需要 Redis 可用；Redis 故障时返回失败供调用方重试，已有用户登录无需申请 UID。就绪检查仅依赖 MySQL，不能把健康检查通过视为新用户注册链路已经恢复。Redis 重启、恢复快照或主从切换可能使共享游标回退，必须在开放发号前完成隔离与清理，不能让旧游标先上线再异步清理。按 [UID 分配与恢复](uid-allocation.md) 中的步骤处理；此要求不适用于仅重启应用且 Redis 状态未变的正常发布。

## 数据库内部标识

消费者域仍按 1024 个逻辑桶路由到 16 组物理表。`users_00` 至 `users_15` 的 UID 保持号段分配的 `BIGINT UNSIGNED` 非自增主键；权益授予、权益投影、权益撤销、奖励流水和 VIP 进度使用各物理表独立的 `BIGINT UNSIGNED AUTO_INCREMENT`，其他内部实体继续使用数据库自增主键。内部记录 ID 仅在所属物理表内唯一，查询及 `grant_id` 等关联必须保留用户分片上下文。表间关联使用同类型普通字段和索引，不使用数据库外键。API 中内部 ID 均为十进制字符串；官方 TikTok AppID、BytePlus VID 及请求幂等键单独保存。已有库需先完成 [SQL 升级说明](../sql/README.md#已有库的非-uid-记录主键升级) 中 80 张物理表的结构变更，再运行省略内部 ID 的写入版本；应用启动不代为升级。

`sql/init.sql` 维护新库的最终结构；已有库的 80 张非 UID 记录分表使用 [`upgrade-non-uid-auto-increment.sql`](../sql/upgrade-non-uid-auto-increment.sql) 一次性升级。旧版字符串主键数据库不能直接运行当前版本；数据库切换须在维护窗口另行处理，并同步更新 server/admin。应用启动不会初始化或升级基础表；统计日期表由运行服务按模板动态创建和清理，账号需具有业务库内的 `CREATE`、`DROP` 权限。

## 2026-09-15：按业务使用情况清理字段

以当前调用链确认的使用情况为准，删除下列字段及对应运行模型、API 残留：

| 表 | 删除字段 | 依据 |
| --- | --- | --- |
| applications | brand_json | 固定写空对象且从不读取，展示使用 name、logo_url |
| applications | launch_url | 官方 Minis Link 使用路径、标题和封面生成，不读取该地址 |
| applications | byteplus_account_id、byteplus_space_name | 官方发布与授权使用 MySQL distribution 的独立拥有方配置，删除旧应用回退 |
| episodes | source_url、upload_job_id | 无实际消费入口，当前媒资使用 VID 和空间，上传追踪保存在 media_jobs |

当前初始化脚本包含 121 张基础表（其中 96 张分片表），逐列提供中文业务注释；动态统计日期表不计入该数量。secret_ref 仍服务于 OAuth，identity_hash_namespace 仍用于恢复历史用户，compliance_base_url 仍生成合规链接，不能按旧命名误删。

开发阶段只维护最新 `sql/init.sql` 与业务模型，不为本次清理保留历史升级文件或旧字段兼容。测试库直接执行必要的结构调整，服务和部署脚本不自动改库；生产变更与部署仍需独立授权。

验证：`TestSchemaColumnsMatchPersistenceModels` 检查持久化模型和字段集合；`TestCurrentSchemaApplicationContractsIntegration` 在隔离 MySQL 中通过后台登录、应用创建与数据库读回验证最新合同，所有实际列必须具有与最终定义一致的注释。
