package visitor

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"drama-server/internal/biz/campaign"
	"drama-server/internal/biz/sharding"
)

const visitorCredentialVersion = 3
const defaultVisitorTokenTTL = 24 * time.Hour

// Config 提供 VisitorToken 保护所需的部署密钥。
type Config struct {
	// RequireTikTokLogin 在预发和生产启用；配置缺失或静默登录失败不得降级为匿名身份。
	RequireTikTokLogin bool
	// IdentityTokenEncryptionSecret 是部署根密钥，用于派生用途隔离的访问凭据密钥，不得写入日志。
	IdentityTokenEncryptionSecret string
	// VisitorTokenTTL 是凭据绝对有效期；运行时从配置文件注入，独立构造未设置时为 24 小时。
	VisitorTokenTTL time.Duration
}

// Service 负责静默登录身份解析、稳定用户识别和无服务端 Session 的凭据签发。
type Service struct {
	// requireTikTokLogin 固定于服务启动，不能由客户端参数关闭。
	requireTikTokLogin bool
	applications       ApplicationResolver
	campaigns          CampaignResolver
	store              Store
	protector          *credentialProtector
	tokenTTL           time.Duration
	now                func() time.Time
}

// credentialClaims 是 VisitorToken 的加密载荷，不直接作为 HTTP DTO 使用。
type credentialClaims struct {
	// Version 是访问凭据载荷版本，解密后必须与当前协议一致。
	Version int `json:"v"`
	// IssuedAt 和 ExpiresAt 是签发、过期 Unix 秒数，不依赖客户端时钟。
	IssuedAt  int64 `json:"iat"`
	ExpiresAt int64 `json:"exp"`
	// AuthVersion 与 MySQL 用户行版本一致，支持单账号撤销旧凭据。
	AuthVersion uint64 `json:"av"`
	// ApplicationID 是签发时确认的内部应用自增主键。
	ApplicationID string `json:"a"`
	// TikTokAppID 是用于重新解析权威应用的官方 AppID。
	TikTokAppID string `json:"t"`
	// VisitorID 是已持久化的内部访客标识，后续请求仍需验证主体存在。
	VisitorID string `json:"u"`
	// BucketID 是用户域稳定逻辑桶，存储访问必须使用该值路由。
	BucketID uint16 `json:"b"`
	// IdentityState 区分匿名主体和 TikTok 已认证主体，生产只接受后者。
	IdentityState IdentityState `json:"i"`
	// Campaign 是本次启动的可信投放快照；新一轮自然启动需将其清空。
	Campaign *campaign.Attribution `json:"m,omitempty"`
}

// NewService 校验装配并初始化凭据保护器；仓储同时实现 CampaignResolver 时启用可信投放解析。
func NewService(applications ApplicationResolver, store Store, config Config) (*Service, error) {
	if applications == nil || store == nil {
		slog.Warn("new service rejected", "error", errServiceNotConfigured)
		return nil, errServiceNotConfigured
	}
	protector, err := newCredentialProtector(config.IdentityTokenEncryptionSecret)
	if err != nil {
		slog.Error("new credential protector failed", "error", err)
		return nil, err
	}
	service := &Service{
		requireTikTokLogin: config.RequireTikTokLogin,
		applications:       applications, store: store, protector: protector,
		tokenTTL: config.VisitorTokenTTL, now: time.Now,
	}
	if service.tokenTTL <= 0 {
		service.tokenTTL = defaultVisitorTokenTTL
	}
	service.campaigns, _ = store.(CampaignResolver)
	return service, nil
}

// Bootstrap 使用静默登录建立当前 TikTok 账号身份；匿名启动仅供本地开发与测试。
func (s *Service) Bootstrap(ctx context.Context, input BootstrapInput) (result Context, resultErr error) {
	if err := ctx.Err(); err != nil {
		slog.Log(ctx, visitorFailureLogLevel(err), "visitor bootstrap context ended", "error", err)
		return Context{}, err
	}
	application, provider, err := s.resolveApplication(ctx, input.TikTokAppID)
	if err != nil {
		slog.ErrorContext(ctx, "resolve application failed", "error", err)
		return Context{}, err
	}
	var currentIdentity *Identity
	code := strings.TrimSpace(input.LoginCode)
	if s.requireTikTokLogin && code == "" {
		slog.WarnContext(ctx, "bootstrap rejected", "error", ErrUnauthorized)
		return Context{}, ErrUnauthorized
	}
	if s.requireTikTokLogin && provider == nil {
		slog.WarnContext(ctx, "bootstrap rejected", "error", ErrIdentityUnavailable)
		return Context{}, ErrIdentityUnavailable
	}
	if code != "" && provider != nil {
		identity, exchangeErr := provider.ExchangeCode(ctx, code)
		if exchangeErr != nil {
			slog.Log(ctx, visitorFailureLogLevel(exchangeErr), "exchange code failed", "error", exchangeErr)
		}
		if exchangeErr != nil && ctx.Err() != nil {
			cancelErr := ctx.Err()
			slog.WarnContext(ctx, "visitor bootstrap cancelled", "error", cancelErr)
			return Context{}, cancelErr
		}
		// 只有官方明确拒绝一次性登录码才属于 401；配置、平台和网络失败保留独立分类。
		if exchangeErr != nil && s.requireTikTokLogin {
			if errors.Is(exchangeErr, ErrUnauthorized) {
				return Context{}, exchangeErr
			}
			return Context{}, DependencyError(ErrIdentityUnavailable, exchangeErr)
		}
		if exchangeErr == nil && strings.TrimSpace(identity.OpenID) != "" {
			currentIdentity = &identity
		}
	}
	if s.requireTikTokLogin && currentIdentity == nil {
		slog.WarnContext(ctx, "bootstrap rejected", "error", ErrIdentityUnavailable)
		return Context{}, ErrIdentityUnavailable
	}

	var claims credentialClaims
	if currentIdentity != nil {
		// 只按本次官方身份查找或创建用户，不读取或合并旧账号、匿名账号的权益。
		claims, err = s.resolveTikTokUser(ctx, application, *currentIdentity)
	} else {
		var valid bool
		claims, valid, err = s.decodeCredential(ctx, strings.TrimSpace(input.VisitorToken), application)
		if err == nil && !valid {
			claims, err = s.createAnonymousVisitor(ctx, application)
		}
	}
	if err != nil {
		slog.ErrorContext(ctx, "resolve bootstrap user failed", "error", err)
		return Context{}, err
	}

	campaign, err := s.resolveCampaign(ctx, application.ID, input.LaunchQuery)
	if err != nil {
		slog.ErrorContext(ctx, "resolve campaign failed", "error", err)
		return Context{}, err
	}
	// 归因属于本次启动，不属于永久 Visitor。没有有效 campaign 时必须清掉旧凭据中的快照，
	// 避免用户从投放入口退出后，后续自然启动仍被错误归因。
	claims.Campaign = campaign
	token, err := s.sealClaims(ctx, claims)
	if err != nil {
		slog.ErrorContext(ctx, "seal claims failed", "error", err)
		return Context{}, err
	}
	result = contextFromClaims(application, claims, token)
	if currentIdentity != nil {
		result.EventOpenID = currentIdentity.OpenID
	}
	return result, nil
}

// Resolve 校验客户端持有的 VisitorToken，并实时确认应用和 Visitor 仍有效。
func (s *Service) Resolve(ctx context.Context, token string) (result Context, resultErr error) {
	if err := ctx.Err(); err != nil {
		slog.Log(ctx, visitorFailureLogLevel(err), "resolve visitor credential context ended", "error", err)
		return Context{}, err
	}
	claims, err := s.openClaims(ctx, strings.TrimSpace(token))
	if err != nil {
		slog.WarnContext(ctx, "open claims failed", "error", err)
		return Context{}, ErrUnauthorized
	}
	if s.requireTikTokLogin && claims.IdentityState != IdentityStateTikTokAuthenticated {
		slog.WarnContext(ctx, "resolve rejected", "error", ErrUnauthorized)
		return Context{}, ErrUnauthorized
	}
	application, _, err := s.resolveApplication(ctx, claims.TikTokAppID)
	if err != nil {
		slog.ErrorContext(ctx, "resolve application failed", "error", err)
		return Context{}, err
	}
	if !matchesApplicationClaims(application, claims) {
		slog.WarnContext(ctx, "resolve rejected", "error", ErrApplicationForbidden)
		return Context{}, ErrApplicationForbidden
	}
	version, exists, err := s.visitorVersion(ctx, application.ID, UserRef{ID: claims.VisitorID, BucketID: claims.BucketID})
	if err != nil {
		slog.ErrorContext(ctx, "visitor credential version lookup failed", "error", err)
		return Context{}, DependencyError(ErrServiceUnavailable, err)
	}
	if !exists || version != claims.AuthVersion {
		slog.WarnContext(ctx, "resolve rejected", "error", ErrUnauthorized)
		return Context{}, ErrUnauthorized
	}
	return contextFromClaims(application, claims, token), nil
}

// createAnonymousVisitor 仅创建开发环境的独立匿名主体，不与正式 TikTok 账号合并。
func (s *Service) createAnonymousVisitor(ctx context.Context, application Application) (credentialClaims, error) {
	user, err := s.createVisitor(ctx, application.ID)
	if err != nil {
		slog.ErrorContext(ctx, "create visitor failed", "error", err)
		return credentialClaims{}, DependencyError(ErrServiceUnavailable, err)
	}
	return credentialClaims{
		Version: visitorCredentialVersion, ApplicationID: application.ID,
		TikTokAppID: application.TikTokAppID, VisitorID: user.ID, BucketID: user.BucketID, AuthVersion: effectiveAuthVersion(user.AuthVersion), IdentityState: IdentityStateAnonymous,
	}, nil
}

// decodeCredential 尝试恢复启动时的旧凭据；无效、跨应用或主体消失返回 valid=false，存储故障返回错误而不创建替代主体。
func (s *Service) decodeCredential(
	ctx context.Context,
	token string,
	application Application,
) (credentialClaims, bool, error) {
	if token == "" {
		return credentialClaims{}, false, nil
	}
	claims, err := s.openClaims(ctx, token)
	if err != nil {
		slog.WarnContext(ctx, "open claims failed", "error", err)
	}
	if err != nil || !matchesApplicationClaims(application, claims) {
		return credentialClaims{}, false, nil
	}
	version, exists, err := s.visitorVersion(ctx, application.ID, UserRef{ID: claims.VisitorID, BucketID: claims.BucketID})
	if err != nil {
		slog.ErrorContext(ctx, "visitor exists failed", "error", err)
		return credentialClaims{}, false, DependencyError(ErrServiceUnavailable, err)
	}
	return claims, exists && version == claims.AuthVersion, nil
}

// resolveTikTokUser 用应用范围的 OpenID 摘要恢复稳定用户，原始平台标识仅用于本次事件回传。
func (s *Service) resolveTikTokUser(ctx context.Context, application Application, identity Identity) (credentialClaims, error) {
	user, err := s.resolveTikTokIdentity(ctx, TikTokIdentityRecord{
		ApplicationID: application.ID, OpenIDHash: TikTokOpenIDHash(applicationIdentityNamespace(application), identity.OpenID),
	})
	if err != nil {
		slog.ErrorContext(ctx, "resolve TikTok identity failed", "error", err)
		return credentialClaims{}, DependencyError(ErrServiceUnavailable, err)
	}
	return credentialClaims{Version: visitorCredentialVersion, ApplicationID: application.ID, TikTokAppID: application.TikTokAppID,
		VisitorID: user.ID, BucketID: user.BucketID, AuthVersion: effectiveAuthVersion(user.AuthVersion), IdentityState: IdentityStateTikTokAuthenticated}, nil
}

// effectiveAuthVersion 仅让不具版本字段的旧测试替身使用首版凭据，生产仓储总从主库读取版本。
func effectiveAuthVersion(version uint64) uint64 {
	if version == 0 {
		return 1
	}
	return version
}

// visitorVersion 同时验证主体与凭据版本；生产实现按桶查询 MySQL 主库。
func (s *Service) visitorVersion(ctx context.Context, applicationID string, user UserRef) (uint64, bool, error) {
	if store, ok := s.store.(CredentialVersionStore); ok {
		return store.VisitorCredentialVersionRef(ctx, applicationID, user)
	}
	exists, err := s.visitorExists(ctx, applicationID, user)
	return 1, exists, err
}

// createVisitor 优先使用带逻辑桶的生产仓储；兼容测试仓储固定使用桶 0。
func (s *Service) createVisitor(ctx context.Context, applicationID string) (UserRef, error) {
	if sharded, ok := s.store.(ShardedStore); ok {
		return sharded.CreateVisitorRef(ctx, applicationID)
	}
	var id string
	err := s.store.CreateVisitor(ctx, applicationID, &id)
	return UserRef{ID: id}, err
}

// visitorExists 使用凭据携带的桶精确查询生产分片。
func (s *Service) visitorExists(ctx context.Context, applicationID string, user UserRef) (bool, error) {
	if sharded, ok := s.store.(ShardedStore); ok {
		return sharded.VisitorExistsRef(ctx, applicationID, user)
	}
	return s.store.VisitorExists(ctx, applicationID, user.ID)
}

// resolveTikTokIdentity 优先返回生产分片路由，测试仓储固定使用桶 0。
func (s *Service) resolveTikTokIdentity(ctx context.Context, record TikTokIdentityRecord) (UserRef, error) {
	if sharded, ok := s.store.(ShardedStore); ok {
		return sharded.ResolveTikTokIdentityRef(ctx, record)
	}
	id, err := s.store.ResolveTikTokIdentity(ctx, record)
	return UserRef{ID: id}, err
}

// contextFromClaims 组合实时应用资料与已验证的凭据身份，复制归因数据避免共享可变对象。
func contextFromClaims(application Application, claims credentialClaims, token string) Context {
	return Context{
		MiniReview: application.MiniReview, MonetizationMode: application.MonetizationMode,
		ApplicationID:   application.ID,
		TikTokAppID:     application.TikTokAppID,
		ApplicationName: application.Name, ApplicationLogoURL: application.LogoURL,
		ApplicationComplianceBaseURL: application.ComplianceBaseURL,
		InterstitialAdUnitID:         application.InterstitialAdUnitID,
		TimeZone:                     application.TimeZone,
		SubjectID:                    application.SubjectID, PlaybackProvider: application.PlaybackProvider,
		VisitorID: claims.VisitorID, BucketID: claims.BucketID, VisitorToken: token, IdentityState: claims.IdentityState,
		Campaign: cloneCampaign(claims.Campaign),
	}
}

// sealClaims 序列化已确认身份并加密签发 VisitorToken，不将该载荷作为公开 JSON 返回。
func (s *Service) sealClaims(ctx context.Context, claims credentialClaims) (string, error) {
	now := s.now().UTC()
	claims.IssuedAt = now.Unix()
	claims.ExpiresAt = now.Add(s.tokenTTL).Unix()
	if claims.ExpiresAt <= claims.IssuedAt {
		claims.ExpiresAt = claims.IssuedAt + 1
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		slog.ErrorContext(ctx, "seal claims failed", "error", err)
		return "", err
	}

	result1, resultErr := s.protector.sealVisitor(payload)
	if resultErr != nil {
		slog.ErrorContext(ctx, "seal visitor failed", "error", resultErr)
	}
	return result1, resultErr
}

// openClaims 验证凭据密文与载荷版本、应用、主体及身份状态；不在此查询数据库有效性。
func (s *Service) openClaims(ctx context.Context, token string) (credentialClaims, error) {
	payload, err := s.protector.openVisitor(token)
	if err != nil {
		slog.WarnContext(ctx, "open visitor failed", "error", err)
		return credentialClaims{}, err
	}
	var claims credentialClaims
	{
		err := json.Unmarshal(payload, &claims)
		if err != nil {
			slog.WarnContext(ctx, "open claims failed", "error", err)
		}
		if err != nil || claims.Version != visitorCredentialVersion ||
			claims.AuthVersion == 0 || claims.IssuedAt <= 0 || claims.ExpiresAt <= claims.IssuedAt ||
			claims.ExpiresAt <= s.now().Unix() || claims.IssuedAt > s.now().Unix()+30 ||
			strings.TrimSpace(claims.ApplicationID) == "" || strings.TrimSpace(claims.TikTokAppID) == "" ||
			strings.TrimSpace(claims.VisitorID) == "" || claims.BucketID >= sharding.BucketCount ||
			(claims.IdentityState != IdentityStateAnonymous && claims.IdentityState != IdentityStateTikTokAuthenticated) {
			slog.WarnContext(ctx, "open claims rejected", "error", ErrUnauthorized)
			return credentialClaims{}, ErrUnauthorized
		}
	}
	return claims, nil
}

// resolveApplication 按入口已校验的官方 AppID 获取应用与登录能力，服务未装配属于内部配置错误。
func (s *Service) resolveApplication(ctx context.Context, tiktokAppID string) (Application, IdentityProvider, error) {
	if s == nil || s.applications == nil {
		slog.WarnContext(ctx, "resolve application rejected", "error", errServiceNotConfigured)
		return Application{}, nil, errServiceNotConfigured
	}
	result1, result2, resultErr := s.applications.Resolve(ctx, tiktokAppID)
	if resultErr != nil {
		slog.ErrorContext(ctx, "resolve application failed", "error", resultErr)
	}
	if resultErr == nil && result1.TikTokAppID != tiktokAppID {
		slog.WarnContext(ctx, "resolve application identity mismatch", "error", ErrApplicationForbidden)
		return Application{}, nil, ErrApplicationForbidden
	}
	return result1, result2, resultErr
}

// resolveCampaign 只采用服务端能解析的当前应用投放编码；无效或不存在的编码视为自然启动，依赖故障阻止启动。
func (s *Service) resolveCampaign(ctx context.Context, applicationID string, query map[string]string) (*campaign.Attribution, error) {
	if s.campaigns == nil {
		return nil, nil
	}
	code := strings.ToLower(firstLaunchValue(query, "campaignCode", "campaign_code", "campaign"))
	if code == "" || len(code) > 64 {
		return nil, nil
	}
	resolved, found, err := s.campaigns.ResolveCampaign(ctx, applicationID, code)
	if err != nil {
		slog.ErrorContext(ctx, "resolve campaign failed", "error", err)
		return nil, DependencyError(ErrServiceUnavailable, err)
	}
	if !found {
		return nil, nil
	}
	return &resolved, nil
}

// firstLaunchValue 按兼容字段优先级取首个非空启动参数，不将多个别名拼接。
func firstLaunchValue(query map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(query[key]); value != "" {
			return value
		}
	}
	return ""
}

// cloneCampaign 复制归因快照以避免上下文修改影响原始凭据载荷。
func cloneCampaign(value *campaign.Attribution) *campaign.Attribution {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// applicationIdentityNamespace 保留历史摘要算法的输入，防止迁移后同一用户被创建成新账号。
func applicationIdentityNamespace(app Application) string {
	if app.IdentityHashNamespace != "" {
		return app.IdentityHashNamespace
	}
	return app.TikTokAppID
}

// matchesApplicationClaims 同时校验官方身份与内部应用主键，禁止跨应用复用凭据。
func matchesApplicationClaims(app Application, claims credentialClaims) bool {
	return claims.TikTokAppID == app.TikTokAppID && claims.ApplicationID == app.ID
}

// visitorFailureLogLevel 按实际根因区分拒绝、取消和依赖故障；合并错误中的故障不会被取消降级。
func visitorFailureLogLevel(err error) slog.Level {
	if dependency, ok := err.(dependencyError); ok {
		return visitorFailureLogLevel(dependency.cause)
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		level := slog.LevelInfo
		for _, cause := range joined.Unwrap() {
			if cause != nil {
				level = max(level, visitorFailureLogLevel(cause))
			}
		}
		return level
	}
	if cause := errors.Unwrap(err); cause != nil {
		return visitorFailureLogLevel(cause)
	}
	if errors.Is(err, context.Canceled) {
		return slog.LevelInfo
	}
	if errors.Is(err, ErrUnauthorized) {
		return slog.LevelWarn
	}
	return slog.LevelError
}
