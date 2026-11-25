package tlsutil

import (
	"testing"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
)

func TestLoadClientTLSConfigRequiresCertificatePair(t *testing.T) {
	for _, config := range []*configv1.TLSConfig{
		{CertFile: "client.pem"},
		{KeyFile: "client-key.pem"},
	} {
		if _, err := LoadClientTLSConfig(config); err == nil {
			t.Fatal("客户端证书和私钥未成对配置时必须失败")
		}
	}
}

func TestLoadClientTLSConfigUsesTLS12Minimum(t *testing.T) {
	config, err := LoadClientTLSConfig(&configv1.TLSConfig{})
	if err != nil {
		t.Fatalf("创建客户端 TLS 配置：%v", err)
	}
	if config.MinVersion == 0 {
		t.Fatal("客户端 TLS 必须设置最低协议版本")
	}
}
