// Package biz 包含 Drama 的领域规则和业务用例。
package biz

import "github.com/google/wire"

// ProviderSet 声明业务层构造函数。业务包不创建数据库、HTTP Server 或 Styx Runtime。
var ProviderSet = wire.NewSet(
	NewOperationsService,
	NewAdminRBACService,
	NewApplicationResolver,
	NewConsumerService,
	NewVisitorService,
	NewConsumerBootstrap,
	NewWorker,
)
