// Package service 把业务用例适配为 HTTP 接口。
package service

import "github.com/google/wire"

// ProviderSet 声明 HTTP Service 构造函数。
var ProviderSet = wire.NewSet(NewHandlers)
