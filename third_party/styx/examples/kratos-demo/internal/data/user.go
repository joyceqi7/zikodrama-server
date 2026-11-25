package data

import (
	"context"

	"gorm.io/gorm"

	"gitee.com/linpbin/styx/examples/kratos-demo/internal/biz"
)

// userRepo implements biz.UserRepo interface.
type userRepo struct {
	data *Data
}

// NewUserRepo creates a new user repository.
func NewUserRepo(data *Data) biz.UserRepo {
	return &userRepo{data: data}
}

// UserModel is the database model for users.
type UserModel struct {
	ID       int64  `gorm:"primaryKey;autoIncrement"`
	Username string `gorm:"size:255;uniqueIndex;not null"`
	Email    string `gorm:"size:255;uniqueIndex;not null"`
}

// TableName returns the table name.
func (UserModel) TableName() string {
	return "users"
}

// GetUser gets a user by ID.
func (r *userRepo) GetUser(ctx context.Context, id int64) (*biz.User, error) {
	var model UserModel
	if err := r.data.DB().WithContext(ctx).First(&model, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, biz.ErrUserNotFound
		}
		return nil, err
	}
	return &biz.User{
		ID:       model.ID,
		Username: model.Username,
		Email:    model.Email,
	}, nil
}

// CreateUser creates a new user.
func (r *userRepo) CreateUser(ctx context.Context, user *biz.User) error {
	model := &UserModel{
		Username: user.Username,
		Email:    user.Email,
	}
	if err := r.data.DB().WithContext(ctx).Create(model).Error; err != nil {
		return err
	}
	user.ID = model.ID
	return nil
}

// ListUsers lists users with pagination.
func (r *userRepo) ListUsers(ctx context.Context, offset, limit int) ([]*biz.User, int64, error) {
	var models []UserModel
	var total int64

	db := r.data.DB().WithContext(ctx).Model(&UserModel{})

	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if err := db.Offset(offset).Limit(limit).Find(&models).Error; err != nil {
		return nil, 0, err
	}

	users := make([]*biz.User, len(models))
	for i, m := range models {
		users[i] = &biz.User{
			ID:       m.ID,
			Username: m.Username,
			Email:    m.Email,
		}
	}

	return users, total, nil
}
