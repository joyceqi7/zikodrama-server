# Mini 版本审核首页

审核开关按应用和 Mini 包版本维护，最多一百个版本，关闭保留版本记录，删除移除该版本。审核内容按发布主体与渠道共享：官方发布和直连发布各自维护一份审核列表，不按应用复制。

## 菜单与范围

- 「加白管理 → 审核开关」维护当前应用的包版本及开关。
- 「官方发布 → 审核列表」维护当前主体官方渠道已发布的短剧。
- 「直连发布 → 审核列表」维护当前主体直连渠道已发布的短剧。

两份列表不依赖外壳应用选择器，只允许当前主体相应渠道的已发布内容。消费者仍须经过应用授权和上线状态检查，列表不授予播放权限。保留已有失效引用供运营移除，新增项必须当前可用。

## 独立审核首页

新增 `GET /api/v1/open/review-home?tiktokAppId=...`，通过 `X-Mini-Version` 精确匹配已开启版本，返回 `{tiktokAppId,enabled,revision,dramas}`。未开启、未命中或没有版本头时返回 `enabled:false` 和空列表。命中时按应用真实主体和当前播放渠道读取对应审核列表，并按清单顺序返回当前应用可见的摘要，最多一百部。读取依赖故障报错，不降级展示普通首页。

新版 Mini 先请求这个新增接口。命中时只挂载审核列表，原短剧列表、分类、轮播、热播均不挂载；未命中继续使用原首页。原分类、轮播、热播、公开目录接口保持原结果和分页参数，不根据审核状态过滤，也不接受新增审核游标合同。旧版客户端不调用新接口，原行为保持不变。

纯 IAA 的已命中版本沿用既有审核策略覆盖：只保留逐集激励规则，关闭审核恢复正常策略。搜索、详情、分享直达及已授予权益保持原规则。

## 存储与修订

- `application_review_versions`：按 `(application_id,version)` 唯一保存独立开关，关闭保留主键和登记时间，删除移除版本行。
- `review_content_lists`：按 `(subject_id,playback_provider)` 唯一保存共享有序短剧清单和修订，官方与直连分别保存。

`applications` 不增加审核 JSON 字段。应用读取在同一事务快照中组合真实渠道、版本及共享清单，普通应用资料编辑不回写审核表。审核版本与列表在同一主体渠道内共用修订；同渠道其他应用的审核变更也会使旧表单失效。删除最后一个版本仍保留清单及修订。切换播放渠道同时递增原、新渠道修订，过期配置不能因往返切换再次生效。

## 管理接口与权限

- `GET/PUT /admin/v1/review-settings`：固定 `X-Application-ID`，使用 `operations.review_settings.read/update`。写入 `{versions:[{version,enabled}],revision}`。
- `GET/PUT /admin/v1/official/review-list` 及其 `/targets`：固定 `X-Subject-ID` 和官方渠道，使用 `operations.official_review_list.read/update`。
- `GET/PUT /admin/v1/direct/review-list` 及其 `/targets`：固定 `X-Subject-ID` 和直连渠道，使用 `operations.direct_review_list.read/update`。

列表写入只提交 `{revision,dramaIds}`，最多一百部；读取候选按当前主体渠道分页，不需要应用资料编辑权限。同渠道任一应用仍有已开启版本时，不能清空列表。普通角色必须明确获得相应菜单和精确操作权限，不自动扩权。

## SQL 与部署

执行全部前序增量后，执行 `20261106_001_mini_review.sql` 创建两张审核表及渠道列表菜单，再执行 `20261107_001_review_settings_menu.sql` 增加独立开关菜单。随后部署 Server、Admin、Mini，网关允许 `X-Mini-Version`。应用不自动迁移。

按用户要求，尚未合入 main 的审核增量直接修订，不保留已取消的应用审核 JSON 列，不标记废弃。2026-11-07 只读核对测试库尚无旧审核列、审核表或本轮菜单，可以直接执行最新脚本。

验证覆盖独立表结构、主体与渠道共享、应用独立版本、关闭及删除保留边界、修订竞争、独立权限、新审核首页版本匹配，以及原首页接口和旧版客户端行为保留。测试使用隔离 MySQL/Redis 和本地测试替身，不调用真实 TikTok 或 BytePlus。
