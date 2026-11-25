package biz

import (
	"context"
	"drama-server/internal/biz/mediaconfig"
	"testing"
)

// 移除字幕模板配置后，普通媒资装配仍须保留处理超时及回调配置。
func TestMediaConfigServicePreservesProcessingConfiguration(t *testing.T) {
	service, err := mediaconfig.NewService(&mediaconfig.Config{
		AccessKeyID: "fixture-ak", SecretAccessKey: "fixture-sk", VODAppID: "fixture-vod",
		Region: "ap-singapore-1", SpaceName: "fixture-space", MaxFileBytes: 20 << 30,
		UploadTokenTTLSeconds: 600, ProcessingTimeoutSeconds: 900, CoverDomain: "https://cover.example",
		CallbackURL: "https://api.example/callback", CallbackPrivateKey: "fixture123",
	})
	if err != nil {
		t.Fatal(err)
	}
	current, err := service.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if current.ProcessingTimeoutSeconds != 900 || current.UploadTokenTTLSeconds != 600 || !service.CallbackConfigured() {
		t.Fatal("普通媒资配置在服务装配时丢失")
	}
}
