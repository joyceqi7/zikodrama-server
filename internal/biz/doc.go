// Package biz 组合短剧服务的领域规则和业务用例，并声明 data 层及外部平台需要实现的端口。
//
// 本包不感知 Styx、Kratos、HTTP Server、数据库连接或具体 SDK Client。biz.go 中的
// ProviderSet 只列出构造函数；具体实现绑定和最终依赖图由 data 与 cmd/server 声明。
package biz
