package biz

import "github.com/go-kratos/kratos/v2/errors"

// Business error definitions.
// Use Kratos errors for proper HTTP/gRPC status code mapping.
var (
	ErrUsernameRequired = errors.BadRequest("USERNAME_REQUIRED", "username is required")
	ErrEmailRequired    = errors.BadRequest("EMAIL_REQUIRED", "email is required")
	ErrUserNotFound     = errors.NotFound("USER_NOT_FOUND", "user not found")
)
