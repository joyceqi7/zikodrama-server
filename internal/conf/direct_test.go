package config_test

import (
	config "drama-server/internal/conf"
	"errors"
	"testing"
)

// TestDirectPlaybackTokenTTL 拒绝任何实例缺失或越界的播放 Token 有效期。
func TestDirectPlaybackTokenTTL(t *testing.T) {
	for _, tc := range []struct {
		ttl   int
		valid bool
	}{
		{0, false}, {59, false}, {60, true}, {300, true}, {900, true}, {901, false}, {-1, false},
	} {
		c := validDevelopmentConfig()
		c.DirectPlaybackTokenTTLSeconds = tc.ttl
		err := c.Validate()
		if tc.valid && err != nil || !tc.valid && !errors.Is(err, config.ErrInvalid) {
			t.Fatalf("直连签名有效期校验错误：%+v %v", tc, err)
		}
	}
}
