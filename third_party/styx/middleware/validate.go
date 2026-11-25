// Package middleware provides Kratos middleware utilities.
package middleware

import (
	validate "github.com/go-kratos/kratos/contrib/middleware/validate/v2"
	"github.com/go-kratos/kratos/v2/middleware"
)

// Validate returns a parameter validation middleware.
// It validates request parameters based on protoc-gen-validate generated rules.
//
// Usage requires proto files with validate annotations:
//
//	message CreateUserRequest {
//	    string name = 1 [(validate.rules).string = {min_len: 1, max_len: 100}];
//	    string email = 2 [(validate.rules).string.email = true];
//	}
//
// Example usage:
//
//	grpcSrv := grpc.NewServer(
//	    grpc.Middleware(
//	        middleware.Validate(),
//	    ),
//	)
func Validate() middleware.Middleware {
	return validate.ProtoValidate()
}
