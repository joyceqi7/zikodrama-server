// Package consumer 实现 H5/小程序消费者接口的 HTTP 适配。
// bootstrap 是公开入口：校验 tiktokAppId 后调用 biz.ConsumerBootstrap 完成应用、身份和启动数据编排。
// 其他消费路由通过 withVisitor 恢复应用、Visitor、时区与归因，再校验路径、查询或正文参数。
// Handler 不从正文接受另一名 Visitor，不直接操作数据库，也不决定最终播放权益。
// 目录/详情返回可展示的线上内容；播放和奖励由业务 Service 检查权限与幂等事实。
// 发现页的不透明游标在入口解析为数字偏移；奖励领取沿用客户端 claimId，重复成功返回 200。
// 用户可见错误统一映射为稳定 code 和固定消息，底层错误仅用于服务端日志。
package consumer
