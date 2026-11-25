package adminrbac

import (
	"strconv"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

const adminTokenIssuer = "drama-admin"
const adminTokenAudience = "drama-operations-api"

// adminClaims 只携带账号标识、认证版本和签发边界，角色和权限每次从仓储读取。
type adminClaims struct {
	// RegisteredClaims 包含固定用途、账号主键以及签发和过期时间。
	jwt.RegisteredClaims
	// AuthVersion 与账号当前版本一致时才允许访问。
	AuthVersion int64 `json:"auth_version"`
}

// parseToken 严格限定算法与用途；验证失败统一返回业务错误，防止日志包含输入凭据。
func (s *Service) parseToken(raw string) (*adminClaims, error) {
	if len(s.signingKey) < 32 || len(raw) > 8192 {
		return nil, ErrUnauthorized
	}
	claims := &adminClaims{}
	token, err := jwt.ParseWithClaims(strings.TrimSpace(raw), claims, func(_ *jwt.Token) (any, error) {
		return s.signingKey, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(adminTokenIssuer), jwt.WithAudience(adminTokenAudience),
		jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithTimeFunc(s.now), jwt.WithStrictDecoding())
	if err != nil || !token.Valid {
		return nil, ErrUnauthorized
	}
	id, err := strconv.ParseUint(claims.Subject, 10, 64)
	if err != nil || id == 0 || strconv.FormatUint(id, 10) != claims.Subject || claims.AuthVersion < 1 || claims.IssuedAt == nil || !claims.ExpiresAt.After(claims.IssuedAt.Time) {
		return nil, ErrUnauthorized
	}
	return claims, nil
}
