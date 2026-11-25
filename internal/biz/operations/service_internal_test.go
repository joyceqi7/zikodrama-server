package operations

import (
	"context"
	"errors"
	"testing"

	"drama-server/internal/biz/application"
)

type applicationRepositoryStub struct {
	items []application.Application
}

func (repository applicationRepositoryStub) ListApplications(context.Context) ([]application.Application, error) {
	return append([]application.Application(nil), repository.items...), nil
}

func (applicationRepositoryStub) FindApplication(context.Context, string) (application.Application, error) {
	return application.Application{}, errors.New("not found")
}

func (applicationRepositoryStub) CreateApplication(context.Context, *application.Application) error {
	return nil
}

func (applicationRepositoryStub) SaveMiniReview(_ context.Context, _ string, value application.MiniReview) (application.MiniReview, error) {
	return value, nil
}

func (applicationRepositoryStub) SaveApplication(context.Context, application.Application) error {
	return nil
}

// SaveMiniReviewSettings 让应用用例测试可独立替换审核配置存储，而不依赖完整运营仓库。
func (applicationRepositoryStub) SaveMiniReviewSettings(_ context.Context, _ string, value application.MiniReview) (application.MiniReview, error) {
	return value, nil
}

// GetReviewWhitelist 为不涉及清单的应用端口测试提供空渠道清单。
func (applicationRepositoryStub) GetReviewWhitelist(context.Context) (ReviewWhitelist, error) {
	return ReviewWhitelist{}, nil
}

// SaveReviewWhitelist 在应用端口测试中原样返回已校验的渠道清单。
func (applicationRepositoryStub) SaveReviewWhitelist(_ context.Context, value ReviewWhitelist) (ReviewWhitelist, error) {
	return value, nil
}

func TestApplicationUseCaseDoesNotRequireUnrelatedRepositories(t *testing.T) {
	t.Parallel()

	service := NewApplicationService(
		applicationRepositoryStub{items: []application.Application{{TikTokAppID: "app-1"}}}, nil, nil,
	)
	items, err := service.Applications(context.Background())
	if err != nil {
		t.Fatalf("应用查询不应依赖媒资、审计等无关仓储: %v", err)
	}
	if len(items) != 1 || items[0].TikTokAppID != "app-1" {
		t.Fatalf("返回了非预期应用: %+v", items)
	}
}

func TestFinalizeDirectEpisodeMediaRejectsNilService(t *testing.T) {
	t.Parallel()

	var service *DramaService
	_, err := service.finalizeDirectEpisodeMedia(context.Background(), MediaJob{RemoteVID: "vid"})
	if !errors.Is(err, ErrMediaNotConfigured) {
		t.Fatalf("空 Service 必须返回 ErrMediaNotConfigured，实际为 %v", err)
	}
}
