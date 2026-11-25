// Package service implements the gRPC/HTTP service handlers.
package service

import (
	"context"

	"gitee.com/linpbin/styx/examples/kratos-demo/internal/biz"
)

// UserService handles user-related requests.
// In production, this would implement the generated protobuf service interface.
type UserService struct {
	uc *biz.UserUsecase
}

// NewUserService creates a new UserService.
func NewUserService(uc *biz.UserUsecase) *UserService {
	return &UserService{uc: uc}
}

// GetUser handles get user request.
// Example: implements userv1.UserServiceServer.GetUser
func (s *UserService) GetUser(ctx context.Context, id int64) (*biz.User, error) {
	return s.uc.GetUser(ctx, id)
}

// CreateUser handles create user request.
func (s *UserService) CreateUser(ctx context.Context, username, email string) (*biz.User, error) {
	user := &biz.User{
		Username: username,
		Email:    email,
	}
	if err := s.uc.CreateUser(ctx, user); err != nil {
		return nil, err
	}
	return user, nil
}

// ListUsers handles list users request.
func (s *UserService) ListUsers(ctx context.Context, page, pageSize int) ([]*biz.User, int64, error) {
	return s.uc.ListUsers(ctx, page, pageSize)
}
