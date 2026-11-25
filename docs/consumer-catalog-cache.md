# C 端短剧缓存

C 端分页目录（公共目录、首页、筛选页）和短剧详情使用 Redis Cache Aside。按应用 ID、分页位置、页大小和全部筛选条件隔离；详情按应用 ID 和剧 ID 隔离。只缓存剧内容，不缓存 VisitorToken、个人权益、广告 Offer 或播放授权结果。播放和奖励解析仍通过权威 FindDrama 读取数据库。

B 端列表与详情直接查询 MySQL。创建、修改、删除剧，修改分集、绑定视频、更新视频状态，以及发布 Worker 投影上线、下架或应用授权后，由同一个存储装饰器在事务成功返回后删除 C 端目录缓存。失败或回滚不删除。当前整体清除全部应用的列表、详情、轮播与热门结果，避免遗漏搜索页、标签页或授权应用；不增加引用表或逐页依赖记录。

详情缓存键为 `drama:{consumer-catalog}:entries`；分页、热门和轮播分别使用同前缀的 `:list`、`:hot`、`:carousel` 分区，失效版本键为 `drama:{consumer-catalog}:version`，与全局配置缓存 `drama:global_config_cache:<key>` 分离；全局配置权威值在 MySQL。Lua 原子删除缓存并更换随机版本；回源请求只在版本未改变时回填，避免旧查询在失效后复活旧数据。全部分区及版本键使用同一 hash slot，支持 Redis Cluster，不使用 KEYS 或 SCAN。

详情与轮播分区最多存活 5 分钟，分页与热门分区最多存活 1 分钟；后续写入不延长既有分区有效期。每个分区最多 256 项，单项最多 256 KiB，超过限制直接返回数据库结果，四个分区内容总量上限约 256 MiB。每个请求独立执行“读 Redis → 未命中查 MySQL → 写 Redis”，不合并同键并发查询；数据库读取使用当前请求自己的上下文，取消或错误不会作为共享结果传给其他请求。并发成功查询可以覆盖同一缓存项，仍保留 TTL 及失效版本保护。版本键不设置过期时间，清库后用随机版本重新初始化，不复用旧版本。空列表可以缓存，数据库错误不会缓存。

Redis 读取、解码或回填失败会记录实际错误并回源；不影响 MySQL 的权威数据。提交后的失效使用独立于请求取消的上下文并尝试三次；失效失败不将已提交写入伪装成回滚。跨 MySQL/Redis 不承诺原子提交，故障时列表或详情最多可能暂留到缓存过期，播放与奖励仍读取数据库并检查当前权限和上架状态。

验证入口：`make integration-up` 后执行 `make test-integration-local`。缓存测试使用独立本地 Redis DB，覆盖应用与分页隔离、命中、发布/下架/删除失效、真实 GORM 提交/回滚边界、旧请求回填、TTL 和 Redis 断连；不调用真实平台。

首页缓存补充验收覆盖完整结果命中不查询短剧、人工热播结果过期后从 MySQL 配置重建、轮播修改失效、分页 TTL、应用与页位置隔离及同键并发请求独立查库。`TestHomepageResultCacheIntegration` 使用一次性 MySQL、独立 Redis 和可计数的目录替身；`TestIndependentCatalogMissesRedisIntegration` 验证同键并发请求各自查库、成功缓存及错误不缓存；`TestResultBucketsRedisIntegration` 验证各分区 TTL 与失效版本保护；`TestCatalogCacheRequestsKeepOwnContext` 验证缓存不可用时同键并发请求不会共享取消或错误。正式环境的 MySQL 性能和跨实例高并发负载未在这些用例中测量。
