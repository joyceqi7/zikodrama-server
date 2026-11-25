package mediaconfig_test

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"

	"drama-server/internal/biz/mediaconfig"
)

func TestServiceReturnsFrozenConfigurationCopy(t *testing.T) {
	input := validConfig()
	service, err := mediaconfig.NewService(&input)
	if err != nil {
		t.Fatal(err)
	}
	input.SpaceName = "mutated"
	first, err := service.Current(context.Background())
	if err != nil || first.SpaceName != "short-drama" {
		t.Fatalf("启动配置未冻结：config=%+v err=%v", first, err)
	}
	first.SpaceName = "changed-copy"
	second, err := service.Current(context.Background())
	if err != nil || second.SpaceName != "short-drama" {
		t.Fatalf("Current 必须返回副本：config=%+v err=%v", second, err)
	}
}

func TestServiceAllowsUnconfiguredDevelopmentMode(t *testing.T) {
	service, err := mediaconfig.NewService(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Current(context.Background()); !errors.Is(err, mediaconfig.ErrNotConfigured) {
		t.Fatalf("缺少 development 媒资配置时应返回未配置：%v", err)
	}
}

func TestServiceRejectsInvalidConfiguration(t *testing.T) {
	for name, mutate := range map[string]func(*mediaconfig.Config){
		"missing access key": func(input *mediaconfig.Config) { input.AccessKeyID = "" },
		"domestic region":    func(input *mediaconfig.Config) { input.Region = "cn-north-1" },
		"callback URL only":  func(input *mediaconfig.Config) { input.CallbackPrivateKey = "" },
		"callback key only":  func(input *mediaconfig.Config) { input.CallbackURL = "" },
		"invalid callback":   func(input *mediaconfig.Config) { input.CallbackURL = "ftp://api.example.com/callback" },
	} {
		t.Run(name, func(t *testing.T) {
			input := validConfig()
			mutate(&input)
			if _, err := mediaconfig.NewService(&input); !errors.Is(err, mediaconfig.ErrInvalid) {
				t.Fatalf("无效配置应拒绝启动：%v", err)
			}
		})
	}
}

func TestServiceAllowsPollingWithoutCallback(t *testing.T) {
	input := validConfig()
	input.CallbackURL = ""
	input.CallbackPrivateKey = ""
	service, err := mediaconfig.NewService(&input)
	if err != nil {
		t.Fatal(err)
	}
	if service.CallbackConfigured() {
		t.Fatal("未配置回调时不应启用回调能力")
	}
	if _, err := service.VerifyCallback(context.Background(), "", "", nil); !errors.Is(err, mediaconfig.ErrCallbackAuth) {
		t.Fatalf("未配置回调时必须拒绝回调验签：%v", err)
	}
}

func TestServiceVerifiesOfficialCallbackSignature(t *testing.T) {
	input := validConfig()
	service, err := mediaconfig.NewService(&input)
	if err != nil {
		t.Fatal(err)
	}
	if !service.CallbackConfigured() {
		t.Fatal("成对填写回调地址和密钥时应启用回调能力")
	}
	body := []byte(`{"EventType":"FileUploadComplete"}`)
	timestamp := fmt.Sprintf("%d", time.Now().UTC().Unix())
	signature := callbackSignature(input.CallbackURL, timestamp, input.CallbackPrivateKey, body)
	version, verifyErr := service.VerifyCallback(context.Background(), timestamp, signature, body)
	if verifyErr != nil || version != "current" {
		t.Fatalf("当前回调密钥验签失败：version=%q err=%v", version, verifyErr)
	}
	if _, err := service.VerifyCallback(context.Background(), timestamp, "invalid", body); !errors.Is(err, mediaconfig.ErrCallbackAuth) {
		t.Fatalf("无效签名必须拒绝：%v", err)
	}
	staleTimestamp := fmt.Sprintf("%d", time.Now().UTC().Add(-481*time.Second).Unix())
	staleSignature := callbackSignature(input.CallbackURL, staleTimestamp, input.CallbackPrivateKey, body)
	if _, err := service.VerifyCallback(context.Background(), staleTimestamp, staleSignature, body); !errors.Is(err, mediaconfig.ErrCallbackAuth) {
		t.Fatalf("超过服务端固定时间窗口的回调必须拒绝：%v", err)
	}
}

func callbackSignature(callbackURL, timestamp, key string, body []byte) string {
	content := fmt.Sprintf("%s|%s|%s|%s", callbackURL, timestamp, key, base64.StdEncoding.EncodeToString(body))
	digest := md5.Sum([]byte(content)) // #nosec G401 -- 测试遵循 VOD 固定协议。
	return hex.EncodeToString(digest[:])
}

func validConfig() mediaconfig.Config {
	return mediaconfig.Config{
		AccessKeyID: "access-key", SecretAccessKey: "secret-key", VODAppID: "1038811",
		Region: mediaconfig.BytePlusRegionJohor, SpaceName: "short-drama",
		MaxFileBytes: mediaconfig.DefaultMaxFileBytes, UploadTokenTTLSeconds: 3600,
		CoverDomain:              "https://cover.example.com",
		ProcessingTimeoutSeconds: 900,
		CallbackURL:              "http://api.example.com/callbacks/v1/byteplus/vod", CallbackPrivateKey: "Callback1234",
	}
}
