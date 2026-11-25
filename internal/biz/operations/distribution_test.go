package operations

import (
	"bytes"
	"context"
	"drama-server/internal/biz/application"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

type distributionQueueRepo struct {
	DistributionRepository
	count int
	err   error
}

type applicationAuthorizationReadRepo struct {
	DistributionRepository
	page ApplicationDramaAuthorizationPage
	err  error
}

func (r applicationAuthorizationReadRepo) ListApplicationDramaAuthorizations(context.Context, string, ApplicationDramaAuthorizationQuery) (ApplicationDramaAuthorizationPage, error) {
	return r.page, r.err
}

// TestApplicationDramaAuthorizations 验证读取服务保留分页结果并记录存储故障详情。
func TestApplicationDramaAuthorizations(t *testing.T) {
	var logs bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(original) })
	page := ApplicationDramaAuthorizationPage{Items: []ApplicationDramaAuthorizationItem{{ID: "201", Title: "已授权", Status: "authorized"}}, Total: 1}
	for _, tc := range []struct {
		name    string
		repo    applicationAuthorizationReadRepo
		wantErr string
	}{
		{"成功", applicationAuthorizationReadRepo{page: page}, ""},
		{"存储故障", applicationAuthorizationReadRepo{err: errors.New("distribution read unavailable")}, "distribution read unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs.Reset()
			service := NewService(Repositories{Application: ApplicationPorts{Registry: distributionQueueApps{}}, Content: ContentPorts{Distributions: tc.repo}}, nil)
			result, err := service.ApplicationDramaAuthorizations(context.Background(), "42", ApplicationDramaAuthorizationQuery{Page: 1, PageSize: 20, View: "tracked"})
			if tc.wantErr == "" {
				if err != nil || result.Total != 1 || result.Items[0].ID != "201" || logs.Len() != 0 {
					t.Fatalf("读取结果=%+v err=%v logs=%s", result, err, logs.String())
				}
			} else if err == nil || !strings.Contains(logs.String(), "list application drama authorizations failed") || !strings.Contains(logs.String(), tc.wantErr) {
				t.Fatalf("失败缺少诊断: err=%v logs=%s", err, logs.String())
			}
		})
	}
	logs.Reset()
	service := NewService(Repositories{}, nil)
	_, err := service.ApplicationDramaAuthorizations(context.Background(), "42", ApplicationDramaAuthorizationQuery{Page: 1, PageSize: 20, View: "tracked"})
	if !errors.Is(err, ErrPublicationUnavailable) || !strings.Contains(logs.String(), "list application drama authorizations unavailable") || !strings.Contains(logs.String(), err.Error()) {
		t.Fatalf("仓储缺失未记录错误: err=%v logs=%s", err, logs.String())
	}
}

func (r distributionQueueRepo) DistributionSources(context.Context) ([]DistributionSource, error) {
	return []DistributionSource{{DramaID: "201", Online: true}}, r.err
}
func (r distributionQueueRepo) ApplyDistribution(context.Context, string, string, bool, bool, DistributionCall) error {
	return r.err
}

type distributionQueueApps struct {
	ApplicationRepository
	app application.Application
}

func (r distributionQueueApps) FindApplication(context.Context, string) (application.Application, error) {
	return r.app, nil
}

// TestAuthorizeApplicationDramas 验证批量入口的应用门禁、确认数量及失败日志。
func TestAuthorizeApplicationDramas(t *testing.T) {
	var logs bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(original) })
	app := application.Application{SubjectID: "1", Status: application.StatusActive, ClientKey: "private-client-key"}
	failure := errors.New("distribution storage unavailable")
	for _, tc := range []struct {
		name      string
		app       application.Application
		repo      distributionQueueRepo
		wantCount int
		wantErr   error
		wantLog   string
	}{
		{"确认授权", app, distributionQueueRepo{count: 1}, 1, nil, ""},
		{"停用拒绝", application.Application{SubjectID: "1", Status: application.StatusDisabled, ClientKey: app.ClientKey}, distributionQueueRepo{}, 0, ErrConflict, "authorize application dramas rejected"},
		{"所选剧目状态变化", app, distributionQueueRepo{err: ErrConflict}, 0, ErrConflict, "validate batch authorization sources failed"},
		{"存储失败", app, distributionQueueRepo{err: failure}, 0, failure, "validate batch authorization sources failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs.Reset()
			service := NewService(Repositories{Application: ApplicationPorts{Registry: distributionQueueApps{app: tc.app}}, Content: ContentPorts{Distributions: tc.repo}}, nil)
			count, err := service.AuthorizeApplicationDramas(context.Background(), "42", []string{"201"})
			if count != tc.wantCount || !errors.Is(err, tc.wantErr) {
				t.Fatalf("count=%d err=%v", count, err)
			}
			if tc.wantLog == "" {
				if logs.Len() != 0 {
					t.Fatalf("成功路径出现日志: %s", logs.String())
				}
			} else if !strings.Contains(logs.String(), tc.wantLog) || !strings.Contains(logs.String(), err.Error()) || strings.Contains(logs.String(), app.ClientKey) {
				t.Fatalf("失败日志缺少原因或包含 Client Key: %s", logs.String())
			}
			if tc.name == "停用拒绝" && strings.Contains(logs.String(), `"level":"ERROR"`) {
				t.Fatalf("预期业务拒绝被记录为故障: %s", logs.String())
			}
		})
	}
}

type distributionReadRepo struct {
	DistributionRepository
	requestedID string
	listedAll   bool
}

func (r *distributionReadRepo) DistributionSources(context.Context) ([]DistributionSource, error) {
	r.listedAll = true
	return nil, nil
}

func (r *distributionReadRepo) FindDistributionSource(_ context.Context, id string) (*DistributionSource, error) {
	r.requestedID = id
	return nil, nil
}

func (r *distributionReadRepo) ListDistributions(context.Context, string) ([]DramaDistribution, error) {
	return []DramaDistribution{}, nil
}

type distributionApps struct {
	ApplicationRepository
	disabled bool
}

func (r distributionApps) FindApplication(_ context.Context, id string) (application.Application, error) {
	status := application.StatusActive
	if r.disabled && id == "target" {
		status = application.StatusDisabled
	}
	return application.Application{SubjectID: "1", TikTokAppID: id, ClientKey: "key-" + id, Status: status}, nil
}

type distributionProvider struct {
	TikTokMediaProvider
	grant, revoke int
	failure       error
}

func (p *distributionProvider) AuthorizeAlbum(_ context.Context, album string, targets []string) error {
	p.grant++
	if album != "123" || len(targets) != 1 || targets[0] != "key-target" {
		return errors.New("wrong scope")
	}
	return p.failure
}
func (p *distributionProvider) RevokeAlbum(context.Context, string, []string) error {
	p.revoke++
	return p.failure
}

// TestDramaDistributionsOnlyReconcilesRequestedDrama 防止页面刷新触发全剧库分发对账。
func TestDramaDistributionsOnlyReconcilesRequestedDrama(t *testing.T) {
	repo := &distributionReadRepo{}
	service := NewService(Repositories{Content: ContentPorts{Distributions: repo}}, nil)
	rows, err := service.DramaDistributions(context.Background(), "drama-42")
	if err != nil || len(rows) != 0 {
		t.Fatalf("read distributions: rows=%v err=%v", rows, err)
	}
	if repo.requestedID != "" || repo.listedAll {
		t.Fatalf("unexpected reconciliation scope: requested=%q listed_all=%v", repo.requestedID, repo.listedAll)
	}
}

// TestDistributionCallUsesOwnerAndDesiredState 确认请求内使用拥有方凭据，授权和撤权直接返回，禁用应用不新增授权。
func TestDistributionCallUsesOwnerAndDesiredState(t *testing.T) {
	for _, test := range []struct {
		name                    string
		desired, disabled, fail bool
	}{
		{"授权", true, false, false}, {"撤权", false, false, false}, {"停用后不新增授权", true, true, false}, {"停用仍允许撤权", false, true, false}, {"平台失败直接返回", true, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			job := DramaDistribution{DramaID: "drama", ApplicationID: "target", OwnerApplicationID: "owner", AlbumID: "123", Desired: test.desired}
			provider := &distributionProvider{}
			if test.fail {
				provider.failure = errors.New("test failure")
			}
			service := NewService(Repositories{Application: ApplicationPorts{Registry: distributionApps{disabled: test.disabled}}, Content: ContentPorts{OwnerApplication: func(_ context.Context, id string) (application.Application, error) {
				return application.Application{SubjectID: "1", TikTokAppID: id, ClientSecret: "global-owner-secret"}, nil
			}}}, nil)
			service.tiktokProviderFactory = func(app application.Application) TikTokMediaProvider {
				if app.TikTokAppID != "owner" || app.ClientSecret != "global-owner-secret" {
					t.Error("未使用拥有方")
				}
				return provider
			}
			err := service.applyOfficialDistribution(context.Background(), job)
			if (err != nil) != (test.fail || (test.desired && test.disabled)) {
				t.Fatalf("同步结果错误 %v", err)
			}
			if !test.desired && provider.revoke != 1 {
				t.Fatal("未调用撤权")
			}
			if test.desired && test.disabled && provider.grant != 0 {
				t.Fatal("停用应用仍新增授权")
			}
		})
	}
}

// TestDistributionOwnerSkipsOfficialSelfAuthorization 验证内部应用主键独立于拥有方配置键，拥有方只更新本地可见性。
func TestDistributionOwnerSkipsOfficialSelfAuthorization(t *testing.T) {
	for _, desired := range []bool{true, false} {
		job := DramaDistribution{DramaID: "9007199254740993", ApplicationID: "42", OwnerApplicationID: "global-owner", AlbumID: "123", Desired: desired}
		service := NewService(Repositories{Application: ApplicationPorts{Registry: distributionApps{}}, Content: ContentPorts{OwnerApplication: func(context.Context, string) (application.Application, error) {
			return application.Application{SubjectID: "1", TikTokAppID: "42"}, nil
		}}}, nil)
		service.tiktokProviderFactory = func(application.Application) TikTokMediaProvider {
			t.Fatal("拥有方不应触发官方自授权")
			return nil
		}
		if err := service.applyOfficialDistribution(context.Background(), job); err != nil {
			t.Fatalf("拥有方同步确认失败: %v", err)
		}
	}
}
