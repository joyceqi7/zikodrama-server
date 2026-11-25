package operations

import (
	"context"

	"drama-server/internal/biz/application"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
)

// validationTestService 为迁移来的用例提供入口准备步骤，业务约束仍调用实际 Service。
type validationTestService struct{ *operations.Service }

func newValidationTestService(repositories operations.Repositories, media operations.MediaProvider) validationTestService {
	return validationTestService{operations.NewService(repositories, media)}
}

// CreateApplication 使用创建入口的默认值和校验，再调用实际业务服务。
func (s validationTestService) CreateApplication(ctx context.Context, input operations.ApplicationInput) (*application.Application, error) {
	input, err := normalizeCreateApplicationInput(input)
	if err != nil {
		return nil, err
	}
	return s.Service.CreateApplication(ctx, input)
}
func (s validationTestService) CreateDrama(ctx context.Context, input operations.DramaInput) (*domain.Drama, error) {
	input, err := normalizeDramaInput(input, true)
	if err != nil {
		return nil, err
	}
	return s.Service.CreateDrama(ctx, input)
}
func (s validationTestService) UpdateDrama(ctx context.Context, id string, input operations.DramaInput) (*domain.Drama, error) {
	input, err := normalizeDramaInput(input, false)
	if err != nil {
		return nil, err
	}
	return s.Service.UpdateDrama(ctx, id, input)
}
func (s validationTestService) UpdateApplicationMonetization(ctx context.Context, input operations.ApplicationMonetizationInput) (*operations.ApplicationMonetizationView, error) {
	input, err := normalizeApplicationMonetizationInput(input)
	if err != nil {
		return nil, err
	}
	return s.Service.UpdateApplicationMonetization(ctx, input)
}

func (s validationTestService) UploadCover(ctx context.Context, input operations.Upload) (*operations.CoverAsset, error) {
	input, err := validatedCoverUpload(input)
	if err != nil {
		return nil, err
	}
	return s.Service.UploadCover(ctx, input)
}
