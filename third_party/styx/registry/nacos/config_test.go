package nacos

import (
	"testing"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
)

func TestResolveNotLoadCacheAtStart(t *testing.T) {
	tests := []struct {
		name string
		mode configv1.NacosCacheStartMode
		want bool
	}{
		{name: "未配置时默认跳过缓存", mode: configv1.NacosCacheStartMode_NACOS_CACHE_START_MODE_UNSPECIFIED, want: true},
		{name: "显式跳过缓存", mode: configv1.NacosCacheStartMode_NACOS_CACHE_START_MODE_SKIP, want: true},
		{name: "显式加载缓存", mode: configv1.NacosCacheStartMode_NACOS_CACHE_START_MODE_LOAD, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := resolveNotLoadCacheAtStart(test.mode)
			if err != nil {
				t.Fatalf("resolveNotLoadCacheAtStart() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("resolveNotLoadCacheAtStart() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestResolveNotLoadCacheAtStartRejectsUnknownValue(t *testing.T) {
	if _, err := resolveNotLoadCacheAtStart(configv1.NacosCacheStartMode(99)); err == nil {
		t.Fatal("resolveNotLoadCacheAtStart() error = nil, want invalid enum error")
	}
}
