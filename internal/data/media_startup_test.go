package data

import (
	"context"
	"errors"
	"testing"
	"time"

	"drama-server/internal/biz/mediaconfig"
	"drama-server/internal/biz/operations"
)

// TestMediaResourcesInjectsSnapshotBytePlusProvider 验证剧操作使用配置副本，调用方修改对象不会改变既有客户端。
// 测试使用虚拟配置；构造 SDK 和读取本地配置不会请求官方平台。
func TestMediaResourcesInjectsSnapshotBytePlusProvider(t *testing.T) {
	input := mediaconfig.Config{
		AccessKeyID: "test-ak", SecretAccessKey: "test-sk", VODAppID: "1",
		Region: mediaconfig.BytePlusRegionJohor, SpaceName: "test-space",
		MaxFileBytes: mediaconfig.DefaultMaxFileBytes, UploadTokenTTLSeconds: 3600,
		CoverDomain: "https://cover.example.test", ProcessingTimeoutSeconds: 900,
	}
	config, err := mediaconfig.NewService(&input)
	if err != nil {
		t.Fatal(err)
	}
	resources, err := NewMediaResources(config, &Resources{})
	if err != nil {
		t.Fatal(err)
	}
	provider, ok := resources.Provider.(*currentDramaMediaProvider)
	if !ok || NewMediaProvider(resources) != provider {
		t.Fatal("媒资端口必须复用读取当前配置的 BytePlus 适配器")
	}
	// 修改调用方的配置对象不应改变进程中已经准入并冻结的媒资空间。
	input.Region, input.SpaceName, input.ProcessingTimeoutSeconds = mediaconfig.BytePlusRegionSingapore, "other-space", 3600
	region, space, err := provider.PublicationMediaScope(context.Background())
	if err != nil || region != mediaconfig.BytePlusRegionJohor || space != "test-space" {
		t.Fatalf("启动媒资范围发生变化：%s %s %v", region, space, err)
	}
	timeout, err := provider.ProcessingTimeout(context.Background())
	if err != nil || timeout != 15*time.Minute {
		t.Fatalf("启动处理上限发生变化：%v %v", timeout, err)
	}
}

// TestMediaResourcesRejectsMissingConfiguration 验证开发缺省配置是禁用能力，不延迟构造或尝试外部写入。
func TestMediaResourcesRejectsMissingConfiguration(t *testing.T) {
	config, err := mediaconfig.NewService(nil)
	if err != nil {
		t.Fatal(err)
	}
	resources, err := NewMediaResources(config, &Resources{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resources.Provider.(*currentDramaMediaProvider); !ok {
		t.Fatal("媒资端口必须支持后续动态配置")
	}
	if err := resources.Provider.Delete(context.Background(), "space", "Video", "vid", ""); !errors.Is(err, operations.ErrMediaNotConfigured) {
		t.Fatalf("禁用能力必须返回友好配置错误：%v", err)
	}
}

// TestMediaStartupRejectsInvalidConfigBeforeProviderCreation 验证无效启动配置不会隐藏到首次上传才失败。
func TestMediaStartupRejectsInvalidConfigBeforeProviderCreation(t *testing.T) {
	if _, err := mediaconfig.NewService(&mediaconfig.Config{AccessKeyID: "test-ak"}); !errors.Is(err, mediaconfig.ErrInvalid) {
		t.Fatalf("不完整配置必须在启动时拒绝：%v", err)
	}
}
