# 全局配置管理

## 直连基础版播放配置

直连播放 Token 有效期由服务端配置文件管理，不再读取 MySQL `playback.default`：

```yaml
business:
  direct_playback_token_ttl_seconds: 300
```

所有实例必须显式配置 60～900 秒，超出范围或缺失会拒绝启动；修改后重启全部实例生效，不使用环境变量覆盖。此值控制 `GetPlayAuthToken` 的有效期与播放响应的 `expiresAt`，和 BytePlus 控制台媒体地址有效期分开。

新版 Mini 上线后，应用编辑接口 `PUT /admin/v1/applications/{id}` 可修改 playbackProvider，由 applications.update 应用编辑权限控制，并携带当前 playbackRevision（历史无绑定为 0）。服务端始终装配直连能力，播放通道由可信应用配置决定。省略播放字段保留原通道，通道变化递增修订。应用不保存配置引用，也不提供单独修改引用的接口。

客户端启动配置只提供 provider、channel 与 mediaPreload=false。License 及 SDK AppID 由 Mini 随包提供，后台不再配置；许可仍须覆盖播放器实际顶层域名。服务端仍通过 `media` 读取区域和 AK/SK 本地签发短时播放 Token，视频空间来自已授权的发布快照，不要求主体的 TikTok 发布方或 `byteplus_account_id`。`media` 禁止删除；停止入口使用应用停用。已有 `playback.default` 不再参与运行，升级后可从普通配置列表删除，不需要数据库结构迁移。

服务端与 Mini 的启动合同须一起升级：旧 Mini 若仍校验服务端 appId，不能与省略该字段的新服务端配合。每个服务实例运行媒体、官方发布和指标任务，媒体与官方发布任务通过 MySQL 租约协调领取，实际步骤见 [上线手册](veplayer-release-runbook.md)。

后台入口：系统管理 → 配置管理。MySQL `global_configs` 是权威存储，保存 JSON、备注、创建/更新时间与操作人。当前运行时直接读 MySQL 主节点，不缓存全局配置，避免并发回填与失效竞争后读到旧值。权限为 `system.config.read` 和 `system.config.update`。

`media` 保存 BytePlus VOD 配置。接口对 access_key_id、secret_access_key、callback_private_key 返回 `********`。保存时掩码或省略字段保留原密钥，新值替换；可选回调密钥允许清空。media 不允许删除。

API 与 Worker 直接读取 MySQL。新增、修改、删除提交后下一次读取即可见，无需重启；已开始的单次 SDK 调用继续使用自己的客户端。上传任务保存原空间和处理截止时间；改空间不会迁移已存在的视频，新的访问密钥仍需有权限处理尚未完成的旧任务。数据库读取失败时明确报错；media 缺失时服务允许进入后台配置，上传等能力明确不可用，不写默认配置。

全局配置不依赖 Redis。Redis 仍承载其他可重建的业务缓存，不能因此清空整个 Redis DB。

服务启动参数与数据库、Redis 和认证凭据均以选定的配置文件为准，不再接受环境变量覆盖。历史应用的 `secretRef` 可通过 `business.legacy_tiktok_client_secrets` 映射到配置文件内的 TikTok Client Secret；新应用仍以 MySQL 中的 `client_secret` 为准。配置文件包含真实凭据时，应限制文件访问权限并按部署流程管理，禁止把凭据输出到日志。消费者用户 UID 由 MySQL `consumer_domain` 预留号段、Redis 共享消费进度，不需要实例机器号；Redis 不可用时拒绝新用户发号，已有用户登录无需发号，详见 [UID 分配与恢复](uid-allocation.md)。奖励、权益和 VIP 进度等内部记录使用所属物理表自增主键。部署脚本只检查文件存在、构建、启动、健康检查和回滚，不校验媒资或其他业务规则。应用自身负责启动与操作校验。

自动化测试使用隔离 MySQL 和平台替身，涉及新用户发号或其他缓存时接入隔离 Redis，禁止调用真实 TikTok/BytePlus 服务。

TikTok 发布方在「主体管理」维护，不再使用全局 `distribution`。`media.byteplus_account_id` 是共享 VOD 账号的数字字符串，视频直传空间授权和官方发布要求填写；已有直连启动、登录与播放不因其缺失而不可用。所有主体的新上传和官方发布统一使用 `media.space_name`，在「配置管理 → media」维护共享空间。旧版表单省略该字段时保留原值；其他未知字段仍拒绝。空间未配置不阻断已有播放，上传和新发布明确拒绝。历史素材、播放快照和任务继续使用自身冻结的空间，修改全局配置不会把历史处理目标迁往新空间。应用只生成同主体的候选关系，运营明确选择后授权。详见 [主体与迁移](subjects.md)。

后台以 key、value、备注管理配置，并展示创建时间、更新时间与最近操作人。value 使用类型化表单编辑，媒资使用专用表单；自定义对象可新增文本、数字、开关、分组和列表字段，不要求填写原始 JSON。接口保存体增加可选 `remark`（最多 500 字），响应增加 `createdAt`、`updatedAt`、`updatedBy`。时间和操作人由服务端生成，不接受客户端指定。

配置与元数据在 MySQL 同一行原子更新；创建时间、更新时间和操作人由服务端维护。

全局 `distribution` 已退出当前合同，不可重新保存。主体增量只将发布方配置复制到默认主体，`global_configs` 原记录和字段值保留；旧 `distribution` 记录读取时仍对 `client_secret` 脱敏，新主体接口仅返回 `clientSecretConfigured`。迁移后的发布方维护与全局配置权限分开，普通角色须明确授予主体菜单和操作权限。
