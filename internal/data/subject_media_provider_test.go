package data

import (
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/mediaconfig"
	"drama-server/internal/biz/operations"
	"errors"
	"testing"
)

// TestSharedMediaSpaceRouting 验证不同主体共用全局空间，历史任务冻结的空间不随配置更新改变。
func TestSharedMediaSpaceRouting(t *testing.T) {
	settings := &mediaconfig.Config{AccessKeyID: "test-ak", SecretAccessKey: "test-sk", VODAppID: "001", Region: "ap-southeast-1", SpaceName: "shared-space", MaxFileBytes: 1 << 30, UploadTokenTTLSeconds: 600, CoverDomain: "https://fixture.test", ProcessingTimeoutSeconds: 900}
	config, err := mediaconfig.NewService(settings)
	if err != nil {
		t.Fatal(err)
	}
	p := &currentDramaMediaProvider{config: config}
	for _, id := range []string{"1", "2", "1"} {
		_, space, err := p.PublicationMediaScope(application.WithSubject(t.Context(), id))
		if err != nil || space != "shared-space" {
			t.Fatal("主体未使用共享上传空间", err, space)
		}
	}
	_, space, err := p.PublicationMediaScope(operations.WithMediaSpace(application.WithSubject(t.Context(), "2"), "retained-space"))
	if err != nil || space != "retained-space" {
		t.Fatal("历史任务空间被改写", err)
	}
	_, space, err = p.PublicationMediaScope(application.WithSubject(t.Context(), "1"))
	if err != nil || space != "shared-space" {
		t.Fatal("历史任务覆盖了后续上传空间", err)
	}
	settings.SpaceName = ""
	config, err = mediaconfig.NewService(settings)
	if err != nil {
		t.Fatal(err)
	}
	p = &currentDramaMediaProvider{config: config}
	if _, _, err = p.PublicationMediaScope(application.WithSubject(t.Context(), "2")); !errors.Is(err, operations.ErrMediaNotConfigured) {
		t.Fatal("未配置共享空间时允许新上传", err)
	}
	if _, space, err = p.PublicationMediaScope(operations.WithMediaSpace(t.Context(), "retained-space")); err != nil || space != "retained-space" {
		t.Fatal("历史素材错误依赖当前共享空间", err)
	}
}
