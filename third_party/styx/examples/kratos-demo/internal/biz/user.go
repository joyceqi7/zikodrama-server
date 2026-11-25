// Package biz implements the business logic layer.
package biz

import (
	"context"
	"log/slog"
)

// User is the domain entity.
type User struct {
	ID       int64
	Username string
	Email    string
}

// UserRepo is the user repository interface.
// Defined in biz layer, implemented in data layer (Dependency Inversion).
type UserRepo interface {
	GetUser(ctx context.Context, id int64) (*User, error)
	CreateUser(ctx context.Context, user *User) error
	ListUsers(ctx context.Context, offset, limit int) ([]*User, int64, error)
}

// UserUsecase implements the user business logic.
type UserUsecase struct {
	repo UserRepo
}

// NewUserUsecase creates a new UserUsecase.
func NewUserUsecase(repo UserRepo) *UserUsecase {
	return &UserUsecase{repo: repo}
}

// GetUser gets a user by ID with business logic.
func (uc *UserUsecase) GetUser(ctx context.Context, id int64) (*User, error) {
	user, err := uc.repo.GetUser(ctx, id)
	if err != nil {
		// Log with trace_id automatically injected via otelslog
		slog.ErrorContext(ctx, "failed to get user", "user_id", id, "error", err)
		return nil, err
	}
	return user, nil
}

// CreateUser creates a new user with business validation.
func (uc *UserUsecase) CreateUser(ctx context.Context, user *User) error {
	// Business validation example
	if user.Username == "" {
		return ErrUsernameRequired
	}
	if user.Email == "" {
		return ErrEmailRequired
	}

	if err := uc.repo.CreateUser(ctx, user); err != nil {
		slog.ErrorContext(ctx, "failed to create user", "username", user.Username, "error", err)
		return err
	}

	slog.InfoContext(ctx, "user created", "user_id", user.ID, "username", user.Username)
	return nil
}

// ListUsers lists users with pagination.
func (uc *UserUsecase) ListUsers(ctx context.Context, page, pageSize int) ([]*User, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize
	return uc.repo.ListUsers(ctx, offset, pageSize)
}
