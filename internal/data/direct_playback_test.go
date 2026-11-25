package data

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/globalconfig"
	"drama-server/internal/biz/mediaconfig"
	conf "drama-server/internal/conf"
)

// directPlaybackConfigRepo 只提供测试用媒资配置，其他配置一律不存在。
type directPlaybackConfigRepo struct {
	media json.RawMessage
	err   error
}

// List 返回空列表，播放签名测试不依赖配置列表。
func (r *directPlaybackConfigRepo) List(context.Context) ([]globalconfig.Entry, error) {
	return nil, nil
}

// Put 不改变只读测试夹具。
func (r *directPlaybackConfigRepo) Put(context.Context, globalconfig.Entry) error { return nil }

// Delete 不改变只读测试夹具。
func (r *directPlaybackConfigRepo) Delete(context.Context, string) error { return nil }

// Get 只提供媒资配置，其他键返回未找到；依赖故障按原错误返回。
func (r *directPlaybackConfigRepo) Get(_ context.Context, key string) (globalconfig.Entry, error) {
	if r.err != nil {
		return globalconfig.Entry{}, r.err
	}
	if key != "media" || r.media == nil {
		return globalconfig.Entry{}, globalconfig.ErrNotFound
	}
	return globalconfig.Entry{Key: key, Value: r.media}, nil
}

const directPlaybackTestMedia = `{"access_key_id":"test-only-access-key","secret_access_key":"test-only-secret-key","vod_app_id":"001","region":"ap-southeast-1","max_file_bytes":1073741824,"upload_token_ttl_seconds":3600,"cover_domain":"https://cover.example.test","processing_timeout_seconds":900,"callback_url":"","callback_private_key":""}`

// TestDirectPlaybackWithRetainedLegacySpace 验证保留旧配置不会阻断新版官方 SDK 本地签名，也不覆盖已校验发布快照的空间。
func TestDirectPlaybackWithRetainedLegacySpace(t *testing.T) {
	media := strings.Replace(directPlaybackTestMedia, `{`, `{"space_name":"legacy-space",`, 1)
	p := NewDirectPlaybackProvider(globalconfig.NewService(&directPlaybackConfigRepo{media: json.RawMessage(media)}), conf.Config{DirectPlaybackTokenTTLSeconds: 300})
	for _, space := range []string{"legacy-space", "subject-b-space"} {
		token, expires, err := p.Sign(t.Context(), consumer.Episode{VID: "test-vid", MediaSpaceName: space})
		if err != nil || token == "" || !expires.After(time.Now().UTC()) {
			t.Fatal("保留旧配置后播放签名失败", err)
		}
	}
}

// TestDirectPlaybackConfiguredTTL 验证配置文件有效期进入官方 SDK 的本地签名且与响应到期时间一致，不需要数据库播放配置。
func TestDirectPlaybackConfiguredTTL(t *testing.T) {
	for _, ttl := range []int{60, 300, 900} {
		t.Run(strconv.Itoa(ttl), func(t *testing.T) {
			p := NewDirectPlaybackProvider(globalconfig.NewService(&directPlaybackConfigRepo{media: json.RawMessage(directPlaybackTestMedia)}), conf.Config{DirectPlaybackTokenTTLSeconds: ttl})
			cfg, err := p.Configuration(t.Context())
			if err != nil || cfg.Provider != "byteplus_web" || cfg.Channel != "byteplus" || cfg.Capabilities == nil || cfg.Capabilities.MediaPreload {
				t.Fatal("公开配置不符合基础版合同", err)
			}
			encoded, err := json.Marshal(cfg)
			if err != nil || strings.Contains(string(encoded), "appId") || strings.Contains(string(encoded), "token") {
				t.Fatal("启动合同不应包含许可标识或签名参数", err)
			}
			before := time.Now().UTC()
			token, expiry, err := p.Sign(t.Context(), consumer.Episode{VID: "test-vid", MediaSpaceName: "test-space"})
			if err != nil {
				t.Fatal(err)
			}
			payload, err := base64.StdEncoding.DecodeString(token)
			if err != nil {
				t.Fatal("签名未使用官方 V2 编码")
			}
			var auth map[string]string
			if json.Unmarshal(payload, &auth) != nil {
				t.Fatal("签名格式错误")
			}
			query, err := url.ParseQuery(auth["GetPlayInfoToken"])
			if err != nil || query.Get("X-Expires") != strconv.Itoa(ttl) || query.Get("Vid") != "test-vid" || query.Get("Ssl") != "1" {
				t.Fatal("官方签名未使用配置有效期或目标 VID")
			}
			if expiry.Before(before.Add(time.Duration(ttl)*time.Second)) || expiry.After(time.Now().UTC().Add(time.Duration(ttl)*time.Second)) {
				t.Fatal("响应到期时间与签名有效期不一致")
			}
		})
	}
}

// TestDirectPlaybackFailuresLogged 验证配置和 SDK 故障保留实际原因，拒绝不生成凭据，成功路径不误报且日志不含密钥。
func TestDirectPlaybackFailuresLogged(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	readErr := errors.New("fixture MySQL connection refused")
	for _, tc := range []struct {
		name, operation, detail string
		repo                    directPlaybackConfigRepo
		episode                 consumer.Episode
		want                    error
	}{
		{"读取故障", "load direct playback media configuration failed", readErr.Error(), directPlaybackConfigRepo{err: readErr}, consumer.Episode{}, readErr},
		{"缺少媒资配置", "direct playback media configuration unavailable", mediaconfig.ErrNotConfigured.Error(), directPlaybackConfigRepo{}, consumer.Episode{}, mediaconfig.ErrNotConfigured},
		{"空间缺失", "direct playback media configuration unavailable", mediaconfig.ErrNotConfigured.Error(), directPlaybackConfigRepo{media: json.RawMessage(directPlaybackTestMedia)}, consumer.Episode{VID: "test-vid", MediaSpaceName: ""}, mediaconfig.ErrNotConfigured},
		{"SDK拒绝", "sign direct playback authorization failed", "传入的Vid为空", directPlaybackConfigRepo{media: json.RawMessage(directPlaybackTestMedia)}, consumer.Episode{MediaSpaceName: "test-space"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs.Reset()
			p := NewDirectPlaybackProvider(globalconfig.NewService(&tc.repo), conf.Config{DirectPlaybackTokenTTLSeconds: 300})
			token, expiry, err := p.Sign(t.Context(), tc.episode)
			if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) || token != "" || !expiry.IsZero() {
				t.Fatal("故障结果或错误传播异常", err)
			}
			if !strings.Contains(logs.String(), tc.operation) || !strings.Contains(logs.String(), tc.detail) || strings.Contains(logs.String(), "test-only-access-key") || strings.Contains(logs.String(), "test-only-secret-key") {
				t.Fatal("日志缺少操作/原因或泄露密钥")
			}
		})
	}
	logs.Reset()
	p := NewDirectPlaybackProvider(globalconfig.NewService(&directPlaybackConfigRepo{media: json.RawMessage(directPlaybackTestMedia)}), conf.Config{DirectPlaybackTokenTTLSeconds: 300})
	if _, _, err := p.Sign(t.Context(), consumer.Episode{VID: "test-vid", MediaSpaceName: "test-space"}); err != nil || logs.Len() != 0 {
		t.Fatal("成功签名误报故障", err)
	}
}
