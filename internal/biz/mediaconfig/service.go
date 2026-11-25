package mediaconfig

import (
	"context"
	"crypto/md5"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

const callbackTimeSkewSeconds int64 = 480

// Service 提供当前媒资配置以及 VOD 回调验签。
type Service struct {
	config *Config
	load   func(context.Context) (*Config, error)
	now    func() time.Time
}

// NewService 创建只读媒资配置服务。development 可传 nil 以关闭真实媒资能力；
// 其他环境是否允许缺失由启动配置层负责约束。
func NewService(input *Config) (*Service, error) {
	service := &Service{now: time.Now}
	if input == nil {
		return service, nil
	}
	config := *input
	config.normalize()
	if err := config.Validate(); err != nil {
		slog.Error("new service failed", "error", err)
		return nil, err
	}
	service.config = &config
	return service, nil
}

// NewDynamicService 在每个操作边界读取配置，运行中的请求继续使用自己的配置副本。
func NewDynamicService(load func(context.Context) (*Config, error)) *Service {
	return &Service{load: load, now: time.Now}
}

// Current 返回当前配置副本，存储故障不回退到旧密钥。
func (service *Service) Current(ctx context.Context) (Config, error) {
	if service != nil && service.load != nil {
		config, err := service.load(ctx)
		if err != nil {
			slog.ErrorContext(ctx, "read current media configuration failed", "error", err)
			return Config{}, err
		}
		if config == nil {
			return Config{}, ErrNotConfigured
		}
		return *config, nil
	}
	if service == nil || service.config == nil {
		slog.Warn("current rejected", "error", ErrNotConfigured)
		return Config{}, ErrNotConfigured
	}
	return *service.config, nil
}

// CallbackConfigured 报告当前环境是否成对配置了 VOD 回调地址与鉴权密钥。
// 未配置时媒体任务继续使用主动轮询，不影响上传、转码和播放。
func (service *Service) CallbackConfigured() bool {
	if service == nil {
		return false
	}
	config, err := service.Current(context.Background())
	return err == nil && config.CallbackURL != "" && config.CallbackPrivateKey != ""
}

// VerifyCallback 按官方规则验证原始请求字节、时间戳及签名；允许前后 480 秒偏差，成功返回当前密钥版本 current。
// 签名覆盖原始 JSON，调用方不能先重新编码请求再校验。
func (service *Service) VerifyCallback(ctx context.Context, timestamp, signature string, body []byte) (string, error) {
	config, err := service.Current(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "read callback configuration failed", "error", err)
		return "", err
	}
	if config.CallbackURL == "" || config.CallbackPrivateKey == "" {
		return "", ErrCallbackAuth
	}
	timestamp = strings.TrimSpace(timestamp)
	signature = strings.ToLower(strings.TrimSpace(signature))
	requestUnix, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		slog.ErrorContext(ctx, "verify callback failed", "error", err)
	}
	if err != nil || len(timestamp) != 10 {
		slog.WarnContext(ctx, "verify callback rejected", "error", ErrCallbackAuth)
		return "", ErrCallbackAuth
	}
	difference := service.now().UTC().Unix() - requestUnix
	if difference < 0 {
		difference = -difference
	}
	if difference > callbackTimeSkewSeconds {
		slog.WarnContext(ctx, "verify callback rejected", "error", ErrCallbackAuth)
		return "", ErrCallbackAuth
	}
	provided, err := hex.DecodeString(signature)
	if err != nil {
		slog.ErrorContext(ctx, "verify callback failed", "error", err)
	}
	if err != nil || len(provided) != md5.Size {
		slog.WarnContext(ctx, "verify callback rejected", "error", ErrCallbackAuth)
		return "", ErrCallbackAuth
	}
	callbackContent := base64.StdEncoding.EncodeToString(body)
	content := fmt.Sprintf("%s|%s|%s|%s", config.CallbackURL, timestamp, config.CallbackPrivateKey, callbackContent)
	digest := md5.Sum([]byte(content)) // #nosec G401 -- VOD 回调协议固定使用 MD5，比较采用常量时间。
	if subtle.ConstantTimeCompare(provided, digest[:]) == 1 {
		return "current", nil
	}
	slog.WarnContext(ctx, "verify callback rejected", "error", ErrCallbackAuth)
	return "", ErrCallbackAuth
}
