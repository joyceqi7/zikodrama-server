package byteplusmedia

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/byteplus-sdk/byteplus-sdk-golang/base"
	"github.com/byteplus-sdk/byteplus-sdk-golang/service/vod"
)

// TestUploadAuthorizationUsesOfficialSDK 验证恢复官方签发方式，两个地域均沿用默认上传策略及配置有效期，不自行拼接资源。
func TestUploadAuthorizationUsesOfficialSDK(t *testing.T) {
	for _, region := range []string{base.RegionApSingapore, base.RegionApSouthEast1} {
		t.Run(region, func(t *testing.T) {
			client := vod.NewInstanceWithRegion(region)
			client.SetCredential(base.Credentials{AccessKeyID: "test-only-ak", SecretAccessKey: "test-only-sk", Region: region, Service: "vod"})
			p := &Provider{authClient: client, appID: "001", region: region, spaceName: "shared-space", maxFileBytes: 1 << 20, uploadTokenTTL: 5 * time.Minute}
			auth, err := p.UploadAuthorization(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(auth.Token.SessionToken, "STS2"))
			if err != nil {
				t.Fatal("官方上传令牌无法解码")
			}
			var token base.InnerToken
			if err := json.Unmarshal(raw, &token); err != nil {
				t.Fatal("官方上传令牌结构无效")
			}
			var policy base.Policy
			if err := json.Unmarshal([]byte(token.PolicyString), &policy); err != nil {
				t.Fatal("官方上传令牌缺少有效策略")
			}
			if len(policy.Statement) != 1 || policy.Statement[0].Effect != "Allow" || !reflect.DeepEqual(policy.Statement[0].Action, []string{"vod:ApplyUploadInfo", "vod:CommitUploadInfo"}) || len(policy.Statement[0].Resource) != 0 {
				t.Fatal("没有沿用官方默认上传策略")
			}
			current, err := time.Parse(time.RFC3339, auth.Token.CurrentTime)
			if err != nil {
				t.Fatal("官方签发时间无效")
			}
			expired, err := time.Parse(time.RFC3339, auth.Token.ExpiredTime)
			if err != nil || expired.Sub(current) != 5*time.Minute || auth.Region != region || auth.SpaceName != "shared-space" {
				t.Fatal("上传地域、空间或配置有效期发生变化")
			}
		})
	}
}

// TestOfficialUploadAuthorizationFailure 验证官方签发错误原样传播，请求取消时不调用 SDK，不因恢复旧方式改变失败行为。
func TestOfficialUploadAuthorizationFailure(t *testing.T) {
	cause := errors.New("official signer unavailable")
	client := &uploadClientStub{authError: cause}
	p := &Provider{authClient: client, appID: "001", region: base.RegionApSouthEast1, spaceName: "shared-space", maxFileBytes: 1 << 20, uploadTokenTTL: time.Minute}
	if _, err := p.UploadAuthorization(t.Context()); !errors.Is(err, cause) || client.authExpiry != time.Minute {
		t.Fatal("官方签发错误或有效期丢失", err)
	}
	client.authExpiry = 0
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := p.UploadAuthorization(ctx); !errors.Is(err, context.Canceled) || client.authExpiry != 0 {
		t.Fatal("请求取消后仍签发了凭据", err)
	}
}
