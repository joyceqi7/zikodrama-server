package biz_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"drama-server/internal/biz"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/visitor"
	conf "drama-server/internal/conf"
	"drama-server/internal/testsupport/memory"
)

// visitorPlaybackFixture 只观察依赖门禁，不签发真实播放凭据。
type visitorPlaybackFixture struct {
	err   error
	calls int
}

// Configuration 返回静态基础版配置，或注入门禁依赖故障。
func (p *visitorPlaybackFixture) Configuration(context.Context) (consumer.PlaybackSDKConfig, error) {
	p.calls++
	return consumer.PlaybackSDKConfig{Provider: "byteplus_web", Channel: "byteplus", Capabilities: &consumer.PlaybackCapabilities{}}, p.err
}

// Sign 拒绝身份解析测试不应调用的播放签名。
func (p *visitorPlaybackFixture) Sign(context.Context, consumer.Episode) (string, time.Time, error) {
	return "", time.Time{}, errors.New("fixture unexpected playback signing")
}

// TestDirectVisitorDependencies 验证应用通道决定依赖门禁，移除部署开关后直连可解析，故障保留实际原因且不泄露密钥。
func TestDirectVisitorDependencies(t *testing.T) {
	logs := captureWorkerLogs(t)
	dependencyErr := errors.New("fixture playback dependency unavailable")
	for _, tc := range []struct {
		name, operation, detail string
		provider                consumer.DirectPlaybackProvider
	}{
		{"正常", "", "", &visitorPlaybackFixture{}},
		{"缺少依赖", "direct playback provider unavailable", visitor.ErrServiceUnavailable.Error(), nil},
		{"依赖故障", "configure visitor direct playback failed", dependencyErr.Error(), &visitorPlaybackFixture{err: dependencyErr}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs.Reset()
			repo := &applicationRepository{apps: map[string]application.Application{
				"official": {TikTokAppID: "official", Status: application.StatusActive},
				"direct":   {TikTokAppID: "direct", Status: application.StatusActive, PlaybackProvider: "byteplus_web"},
			}}
			resolver, err := biz.NewApplicationResolver(repo, func(application.Application) visitor.IdentityProvider { return nil })
			if err != nil {
				t.Fatal(err)
			}
			if _, err := biz.NewVisitorService(conf.Config{IdentityTokenEncryptionSecret: "test-only-visitor-key"}, resolver, memory.NewStore(memory.Seed{}), tc.provider); err != nil {
				t.Fatal(err)
			}
			if _, _, err := resolver.Resolve(t.Context(), "official"); err != nil || logs.Len() != 0 {
				t.Fatal("官方通道不应依赖直连配置", err)
			}
			resolved, _, err := resolver.Resolve(t.Context(), "direct")
			if tc.operation == "" {
				if err != nil || resolved.PlaybackProvider != "byteplus_web" || logs.Len() != 0 {
					t.Fatal("正常直连应用解析失败或误报日志", err)
				}
			} else if !errors.Is(err, visitor.ErrServiceUnavailable) || !strings.Contains(logs.String(), tc.operation) || !strings.Contains(logs.String(), tc.detail) {
				t.Fatal("直连依赖故障传播或日志异常", err)
			}
			if strings.Contains(logs.String(), "test-only-visitor-key") {
				t.Fatal("日志泄露身份凭据密钥")
			}
			if fixture, ok := tc.provider.(*visitorPlaybackFixture); ok && fixture.calls != 1 {
				t.Fatal("官方通道调用了直连门禁")
			}
		})
	}
}

// TestDirectVisitorAssemblyFailureLogged 验证身份服务装配失败在组合根记录实际原因。
func TestDirectVisitorAssemblyFailureLogged(t *testing.T) {
	logs := captureWorkerLogs(t)
	resolver, err := biz.NewApplicationResolver(&applicationRepository{}, func(application.Application) visitor.IdentityProvider { return nil })
	if err != nil {
		t.Fatal(err)
	}
	service, err := biz.NewVisitorService(conf.Config{}, resolver, memory.NewStore(memory.Seed{}), &visitorPlaybackFixture{})
	if err == nil || service != nil || !strings.Contains(logs.String(), "configure visitor service failed") || !strings.Contains(logs.String(), err.Error()) {
		t.Fatal("装配失败缺少实际错误日志或返回值异常", err)
	}
}
