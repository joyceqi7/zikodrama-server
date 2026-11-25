// Package operations 实现管理端 HTTP 入口及 BytePlus VOD 回调入口。
//
// 需要 action 权限的管理业务路由先通过 middleware.go 校验会话、action 权限与应用上下文；写请求还会限流并写入 started 审计。
// 具体 Handler 再依次解码、调用 *_validation.go 规范化/校验字段、调用 biz/operations 或 biz/adminrbac。
// 字段非法时在入口记录错误并返回；权限、唯一性、数据归属、事务和状态流转仍由业务层及仓储保证。
//
// Handler 返回值经过 http_response.go 映射为统一响应。写操作暂存响应，写完最终审计后再输出；
// 最终审计失败不会覆盖已完成的业务结果，避免客户端误重试。VOD 回调走独立的原始正文验签流程，
// 验签后同步幂等唤醒匹配任务，未匹配事件正常应答并忽略；HTTP 不等待后台媒资处理完成。
package operations
