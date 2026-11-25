// Package mysqlstore 用 GORM/MySQL 实现 biz 声明的仓储端口。
//
// Store 复用 Styx 管理的数据源，不创建或迁移业务表。应用级查询从 context 读取范围；
// 短剧目录和管理员 RBAC 是全局数据，线上内容的拥有/授权关系另行限制消费者可见性。
// admin_*.go 在存储边界把字符串 ID 转为 BIGINT UNSIGNED，创建账号/角色后再回填字符串 ID。
//
// catalog.go 的聚合事务维护分集与媒资引用；consumer.go/vip_progress.go 保存奖励及权益事实；
// media_jobs.go 和 publications.go 使用数据库条件更新或行锁领取任务。每个 Claim/Update 的
// 具体竞争条件以各方法 SQL 为准，不能把所有 Update 方法都假设为租约比较并交换。
// drama_media_jobs.go 将回调同步应用到任务；剧和分集保存资源定位，删除任务与内容变更同事务。
//
// DAO 返回可由业务层判断的未找到/冲突或底层错误，不负责 HTTP 映射，也不要求逐层重复打印日志。
package mysqlstore
