// Package server provides gRPC and HTTP server initialization using Styx.
package server

import "github.com/google/wire"

// ProviderSet is the Wire provider set for server layer.
var ProviderSet = wire.NewSet(NewGRPCServer, NewHTTPServer)
