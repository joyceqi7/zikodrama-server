package redis

import (
	"testing"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
)

func TestBuildRedisTLSConfigRequiresCertificatePair(t *testing.T) {
	if _, err := buildRedisTLSConfig(&configv1.RedisTLSConfig{CertFile: "client.pem"}); err == nil {
		t.Fatal("Redis 客户端证书和私钥未成对配置时必须失败")
	}
}
