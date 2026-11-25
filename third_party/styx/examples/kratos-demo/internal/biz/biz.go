// Package biz implements the business logic layer.
package biz

import "github.com/google/wire"

// ProviderSet is the Wire provider set for biz layer.
var ProviderSet = wire.NewSet(NewUserUsecase)
