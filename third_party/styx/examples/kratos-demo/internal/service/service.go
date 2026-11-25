// Package service implements the gRPC/HTTP service handlers.
package service

import "github.com/google/wire"

// ProviderSet is the Wire provider set for service layer.
var ProviderSet = wire.NewSet(NewUserService)
