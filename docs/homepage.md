# 首页轮播、热门短剧与分类排行榜

## 官方与直连独立运营

以下原接口及 homepage_recommendations、category_rankings 仍属于官方通道。直连使用 `/admin/v1/direct/carousel`、`hot-dramas`、`category-rankings` 及各自 targets/保存路径，物理表为 direct_homepage_recommendations、direct_category_rankings；页面路由固定通道，顶部主体选择切换配置范围，同主体的应用共享配置。消费者原路径按已解析应用选择对应配置。

两套轮播、热播保留相同字段、数量和 revision 合同：最多 20/15、标题最多 200 字、顺序与显隐人工维护、创建时间由服务端保留；空配置或过滤后为空不回退官方，不自动补位。More 继续查询当前应用所属主体全部可见目录，推荐隐藏和分类隐藏不影响 More 或播放权益。

分类成员由当前主体与渠道已发布版本的 tagIds 事务维护，公共草稿不参与。保留成员的排名/隐藏不变，新增追加；标签移出或通道下架移除，恢复重新追加。官方某应用撤权只过滤该应用，不能重置主体榜单；某主体下架不能改动其他主体。标签展示名称共用现有翻译表。

官方与直连候选摘要在管理端附带 playbackProvider、publicationVersion、enabledApplicationIds，消费者卡片不增加这些运营字段。预览路径为 `/admin/v1/home-preview`（仅官方应用），参数 applicationId 是内部主键；返回实际 carousel、hot、rankings、filtered 过滤原因及 omittedSections。未授权栏目不读取、不泄露原因；过滤包括隐藏、下架或标签变化、应用停用、未分配或官方授权未完成。


两通道结果缓存键隔离，首期共用全局目录失效版本。直连写入可能让官方重新回源，但不改变官方配置；Redis 失败回源，不丢配置、不清 UID 缓存。上线与回滚见 [手册](veplayer-release-runbook.md)。

## 展示与数据来源

| 区域 | 当前规则 | 数据来源 |
| --- | --- | --- |
| 轮播 | 后台选择、排序和移除短剧，前台展示线上封面与播放入口；空配置隐藏区域 | MySQL `homepage_recommendations` 的 `carousel` 行；Redis 只缓存展示结果 |
| Hot Dramas | 后台人工选剧、排序、设置标题与展示状态，最多 15 部 | MySQL `homepage_recommendations` 的 `hot` 行；Redis 只缓存展示结果 |
| More Drama | 分页查询当前应用全部可见短剧，包含轮播或热门中已出现的剧目 | 现有公开目录分页接口 |
| Category Rankings | 全部已上线短剧按 TikTok 标签自动入榜，支持运营排序、隐藏、移除和恢复 | MySQL `category_rankings`，与发布快照事务同步；前台仍按应用授权过滤 |

轮播和热播在缓存未命中时过滤下架、未授权和不存在的剧目，使用线上版本封面。热播完全按人工配置顺序展示，不按访问量判断、不自动补位；未配置、清空或全部隐藏时返回空数组。前台轮播、热播和分类榜各自独立于 More Drama，并由客户端并行请求；任一区域先返回就先渲染，分类榜延迟不会阻塞首屏封面。单个区域失败时可重试，仍可浏览其他区域与目录。

## 查询结果缓存

完整轮播结果最多缓存 5 分钟；热门前 15 部与目录分页最多缓存 1 分钟。缓存包含可直接展示的剧名、封面、首集 ID 等摘要，命中后不查询短剧数据库。公开接口的有效应用检查和受保护接口的身份校验仍保留。

分页键包含主体、应用、渠道、页位置、每页数量及全部筛选参数，沿用现有搜索与分类结果缓存。每个分区共用有限 TTL，后续写入不会延长已有分区有效期。每个请求独立读 Redis，未命中时用自己的请求上下文查 MySQL 并写入 Redis；同键并发不合并查询，不共享取消或错误结果。各进程共享 Redis 结果，未引入跨进程锁。

保存轮播或热播配置、剧目/分集编辑、发布、下架或授权变化时，沿用目录写入装饰器统一失效分页、详情和首页结果。缓存版本同时更换，失效前的旧查询不能回填。失效失败由诊断日志和 TTL 兜底；Redis 故障时回源，数据库错误不写缓存。

这些 TTL 只作用于可重建结果，轮播与热播人工配置在 MySQL 中保留。实现分区键和容量规则见 [C 端短剧缓存](consumer-catalog-cache.md)。

## 消费者接口

- `GET /api/v1/open/home/carousel?tiktokAppId=<官方应用ID>`：返回轮播 `DramaSummary[]`。
- `GET /api/v1/open/home/hot?tiktokAppId=<官方应用ID>`：返回人工热播 `DramaSummary[]`。
- `GET /api/v1/open/home/ranking-categories?tiktokAppId=<官方应用ID>`：返回非空分类 `{tagId, name}[]`，按官方目录排序，不附带剧目列表。
- `GET /api/v1/open/home/rankings/{tagID}?tiktokAppId=<官方应用ID>&limit=20&cursor=`：返回单个分类的 `{tagId, name, dramas, nextCursor?}`；默认每页 20 部，`limit` 范围 1–50，游标沿用公开目录的编码。先按发布、授权、标签和分类展示状态筛选，再按运营顺序分页；只装载当前页摘要及首集海报。空分类或翻页期间内容减少到游标之前时返回空数组且不提供后续游标。
- `GET /api/v1/open/home/rankings?tiktokAppId=<官方应用ID>`：仅供旧版 Mini 兼容，保留完整 `{tagId, name, dramas}[]` 合同；新版首页不请求此接口。

以上接口均公开读取有效应用，参数规范化及校验沿用公开目录，不创建观众或触发登录。旧聚合接口不再注册。

新版 H5 首次进入首页读取分类目录，默认只加载第一个可用分类的第一页；已有有效分类选择时继续该分类。点击其他分类后请求其第一页，接近榜单底部时追加下一页，末页停止。切换分类会隔离旧请求，翻页防重并按剧目 ID 去重；失败保留已加载内容、显示固定英语提示并暂停自动翻页，点击重试恢复。前后台刷新保留当前分类已展开页数。轮播、热播和 More Drama 的请求与展示独立。

客户端使用独立 `public-home:v2` 缓存键保存轻量分类及访问过的分类第一页（完整 20 条和游标），按 AppID、API 环境、包版本及审核修订隔离；后续页不覆盖首屏缓存，也不预取未访问分类。服务端分类目录仅查询可见剧目的 ID 与线上标签，不下载全部快照；分页请求只解码当前页的完整摘要。此次不新增表或数据库迁移。
播放页访问统计已移除：H5 不再上报，服务端不再注册访问接口，也不读写累计次数或幂等键。旧 Redis 统计数据不会自动参与展示，本次不对已有环境执行数据清理。

## 运营接口

以下请求要求管理员登录、对应操作权限和 `X-Subject-ID`；不要求 `X-Application-ID`，应用头不改变主体配置范围。候选只包含当前主体、当前渠道的已上线短剧：

| 方法与路径 | 权限 | 合同 |
| --- | --- | --- |
| `GET /admin/v1/carousel` | `operations.carousel.read` | 返回 `{revision, dramaIds, items, dramas}`；完整有序引用及仍可见的摘要 |
| `GET /admin/v1/carousel/targets?query=&cursor=` | `operations.carousel.read` | 返回 `{items, nextCursor?}`；搜索全部已上线短剧，每页 20 部，搜索词最多 120 字符 |
| `PUT /admin/v1/carousel` | `operations.carousel.update` | 使用 `{revision, items}` 整体替换（兼容旧版 `dramaIds` 输入），返回新的配置与摘要 |

热播对应接口为 `GET /admin/v1/hot-dramas`、`GET /admin/v1/hot-dramas/targets` 与 `PUT /admin/v1/hot-dramas`，合同与轮播相同，分别要求 `operations.hot.read` 和 `operations.hot.update`。热播最多 15 部，建议 8–12 部。新库权限由 `sql/init.sql` 初始化；已有库根据权限数据差异生成并审核 DML。已有角色需显式分配，不自动扩权。

轮播最多配置 20 部，ID 不能重复，空数组清空轮播。新加入项必须当前可见；已失效的既有项可保留或逐项移除，后台明确标识其不展示。未配置时版本为 0，每次保存递增。旧版本写入返回 409，后台保留已读取状态并提供重新加载入口，避免覆盖别人的修改。

`sql/init.sql` 包含轮播权限。普通角色需要分配轮播菜单、查看及所需修改权限，无需为共享配置额外授予应用读取权限。

## 持久化与缓存边界

轮播、热播统一存入 `homepage_recommendations`，以 `(subject_id, section)` 唯一区分栏目，保存 `revision` 和完整有序 JSON（字符串 ID、标题、显隐及条目创建时间）。无数据库外键。管理端从 MySQL 主节点读取；整体替换在事务中锁定栏目、校验版本并递增，首次并发创建也只能有一个相同版本的请求成功。空数组保留版本，不自动恢复默认推荐。

C 端沿用一层完整结果缓存，不再额外缓存配置。保存先提交 MySQL，再失效结果；Redis 不可用时回源，缓存删除失败记录实际错误且不把已提交写入误报为失败。当前 `subject-home-v2`、`subject-direct-home-v2` 字段命名空间隔离升级前的共享展示结果。

Redis 清空不会丢失推荐配置，后续请求从 MySQL 重建缓存。

## 分类调研（2026-09-15）

[TikTok 官方媒资接口](https://developers.tiktok.com/docs/en/media-asset-api-reference) 将 `tag_list` 定义为标签 ID 列表；`drama_type` 则区分 AIGC、漫剧、真人配音及真人本地化。两者都不能直接推断为截图中的 Forbidden Love、Prenup Contract Marr 或 BL Romance。

当前代码 `internal/biz/consumer/tiktok_tags.go` 保存 1–46 的官方原名映射（缺少 32）。2026-09-15 起，运营可通过独立「TikTok 标签」菜单维护站内英文展示名，存于 `tiktok_tag_english_names`，未维护时回退原名；英文展示名不代表 TikTok 官方英文枚举。查阅的官方接口页使用 `[101]` 示例，未给出完整题材标签枚举；示例差异本身不能证明本地字典错误。分类排行榜使用当前官方标签 ID 关联，不新增截图里的题材分类。运营英文翻译只改变展示名称；相同译名的不同标签仍分别维护。

## 轮播管理扩展字段

`items` 数组的顺序就是前台推荐顺序，每项包含 `dramaId`、`title`、`visible`、`createdAt`。标题最多 200 个字符，留空使用线上剧名；隐藏项保留配置但不出现在前台。服务端维护新项创建时间，既有项保持原时间；历史记录没有创建时间时显示“—”，不伪造历史时间。旧 `dramaIds` 配置读取时默认展示，旧版保存会保留已有标题和开关。标题、展示开关与排序保存均触发完整首页结果缓存失效。

后台采用列表加添加/编辑弹窗：选择短剧、可选轮播标题、展示状态；列表支持上下移、数字排序、展示开关和删除。推荐 6–8 部是运营提示，数量上限仍为 20。封面继续来自关联短剧，不增加独立上传。

人工热播复用同一推荐位编辑器和字段约束，独立菜单、权限、MySQL 配置及版本。列表展示序号、封面、短剧、数字排序与上下移、展示开关、编辑和移除；弹窗支持选剧、可选标题和展示开关。

## 分类排行榜

独立菜单 `categoryRankings` 全局展示全部 45 个 TikTok 标签与关联数量。标签优先展示英文翻译，未维护时回退官方名称。数量包含全部关联成员，隐藏不减少数量；默认选中第一个非空分类。上线剧自动关联，无需手动添加，也没有原人工选剧的 100 部上限；请求体仍受统一 1 MiB 限制。

官方分类表 `category_rankings` 每主体、每个官方标签唯一，`items_json` 保存字符串剧目 ID 及顺序、`visible` 展示状态，`revision` 用于并发校验。不重复存储剧名、封面和译名，无数据库外键。发布、正式标签变更、下架与分类成员在同一个 MySQL 事务中更新，失败整体回滚。授权和撤权只影响各小程序的消费可见性，不重置主体内排序和隐藏状态；普通草稿编辑不改变线上分类。

| 方法与路径 | 权限 | 合同 |
| --- | --- | --- |
| `GET /admin/v1/category-rankings` | `operations.ranking.read` | `{tag, count}[]`，包含当前没有成员的标签 |
| `GET /admin/v1/category-rankings/{tagID}` | `operations.ranking.read` | `{revision, items, dramas}`，items 包含隐藏成员并保留排名 |
| `GET /admin/v1/category-rankings/{tagID}/targets?query=&cursor=` | `operations.ranking.read` | 保留兼容只读查询，管理页不再使用候选选剧 |
| `PUT /admin/v1/category-rankings/{tagID}` | `operations.ranking.update` | `{revision, items:[{dramaId, visible}]}`，仅调整已自动关联成员；旧版本返回 409 |

四个接口均需登录及对应操作权限，需要主体头，不需要应用头。运营仅调整排序和展示开关，隐藏保留当前排名，重新开启后按原排名展示。省略成员的请求将省略项设为隐藏，空数组表示全部隐藏，不删除关联。下架或正式移走标签会删除相应成员；重新上线关联时按新成员处理，默认展示并追加至榜尾。其他上线成员按创建时间倒序、主键升序追加。

成员变化与运营保存均增加分类修订号，防止排序保存覆盖刚上线的新剧。后台保存失败保留已读取状态并锁定写操作，重新加载后继续。公开分类榜读取当前主体的分类表，每次批量复核当前发布、授权、标签归属以及英文翻译，过滤隐藏项，并省略空分类。H5 响应合同保持 `{tagId,name,dramas}`，点击短剧沿用原有登录和播放门禁。

新库的分类榜与首页推荐表由 `sql/init.sql` 及后续增量按顺序创建，服务启动不建表。API/Worker 读写 MySQL，Redis 只做缓存；管理端与小程序 API 合同不变。缓存启动失败时本进程禁用缓存并回源 MySQL，重启后重试缓存初始化；运行中断连由客户端恢复。就绪检查仅依赖 MySQL。

2026-10-11 简化直连运营：移除直连首页预览和复制官方首页配置能力。直连栏目通过各自分类、轮播、热播管理，不改变已保存配置及消费者 `/api/v1/open/home/*` 合同。

2026-11-06 主体内容增量：`homepage_recommendations`、`direct_homepage_recommendations`、`category_rankings`、`direct_category_rankings` 均新增主体字段。部署顺序、原数据归属、旧 Mini 与缓存升级约束见 [主体与内容运营隔离](subjects.md#内容运营隔离增量2026-11-06)。
