package mediaconfig

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

var (
	// ErrNotConfigured 表示部署未启用真实媒资能力或未加载媒资配置。
	ErrNotConfigured = errors.New("media configuration is not configured")
	// ErrInvalid 表示部署媒资字段不满足格式或产品约束。
	ErrInvalid = errors.New("invalid media configuration")
	// ErrCallbackAuth 表示 VOD 回调配置、时间戳或签名校验不通过。
	ErrCallbackAuth = errors.New("invalid VOD callback authentication")
)

const (
	// BytePlusRegionSingapore 是允许配置的新加坡 VOD 区域编码。
	BytePlusRegionSingapore = "ap-singapore-1"
	// BytePlusRegionJohor 是允许配置的柔佛 VOD 区域编码。
	BytePlusRegionJohor = "ap-southeast-1"

	// DefaultMaxFileBytes 是单文件默认上传上限 20 GiB，单位为字节。
	DefaultMaxFileBytes int64 = 20 << 30
	// MaxFileBytesUpperBound 是部署允许配置的单文件上限最大值 100 GiB。
	MaxFileBytesUpperBound int64 = 100 << 30
)

// Config 是随部署环境发布的只读 BytePlus VOD 与官方播放器配置。
// 配置可保存在全局配置表；敏感字段在管理端返回掩码，不进入日志。
type Config struct {
	// BytePlusAccountID 用于官方视频登记；缺少时不阻断官方上传授权和已有直连播放。
	BytePlusAccountID string `json:"byteplus_account_id"`
	// AccessKeyID 是 BytePlus 服务端访问密钥标识，不对外下发。
	AccessKeyID string `json:"access_key_id"`
	// SecretAccessKey 是 BytePlus 服务端签名密钥，禁止进入日志或业务 DTO。
	SecretAccessKey string `json:"secret_access_key"`
	// VODAppID 是官方播放器及 VOD 接入使用的应用标识，按字符串保存。
	VODAppID string `json:"vod_app_id"`
	// Region 选择受支持的 BytePlus VOD 地域。
	Region string `json:"region"`
	// SpaceName 是所有主体共用的 VOD 空间，历史素材和任务仍使用自身冻结的空间。
	SpaceName string `json:"space_name"`
	// MaxFileBytes 限制单个媒体上传文件的字节数。
	MaxFileBytes int64 `json:"max_file_bytes"`
	// UploadTokenTTLSeconds 是签发客户端直传临时授权的有效秒数。
	UploadTokenTTLSeconds int `json:"upload_token_ttl_seconds"`
	// CoverDomain 是生成封面展示地址的 HTTPS 源站，不含路径、查询或凭据。
	CoverDomain string `json:"cover_domain"`
	// ProcessingTimeoutSeconds 是媒资后台处理等待超时秒数。
	ProcessingTimeoutSeconds int `json:"processing_timeout_seconds"`
	// CallbackURL 是参与官方签名计算的回调地址，必须与平台登记值一致。
	CallbackURL string `json:"callback_url"`
	// CallbackPrivateKey 是 VOD 回调鉴权密钥，须与地址成对配置且不得记录日志。
	CallbackPrivateKey string `json:"callback_private_key"`
}

var vodSpacePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$`)

// Validate 只校验启动配置的格式和产品约束，不主动调用 VOD 控制面探测连通性。
func (config Config) Validate() error {
	config.normalize()
	if config.AccessKeyID == "" || config.SecretAccessKey == "" || config.VODAppID == "" ||
		config.Region == "" {
		return ErrInvalid
	}
	// 未配置空间不阻断已有播放，上传和新发布在各自入口拒绝；填写时遵循平台名称规则。
	if config.SpaceName != "" && !vodSpacePattern.MatchString(config.SpaceName) {
		return ErrInvalid
	}
	if config.MaxFileBytes < 1<<20 || config.MaxFileBytes > MaxFileBytesUpperBound {
		return ErrInvalid
	}
	if config.UploadTokenTTLSeconds < 60 || config.UploadTokenTTLSeconds > 7200 ||
		config.ProcessingTimeoutSeconds < 60 || config.ProcessingTimeoutSeconds > 7200 {
		return ErrInvalid
	}
	if config.Region != BytePlusRegionSingapore && config.Region != BytePlusRegionJohor {
		return ErrInvalid
	}
	// 官方 Player 不依赖业务配置播放域名。
	if !validHTTPSOrigin(config.CoverDomain) {
		return ErrInvalid
	}
	callbackConfigured := config.CallbackURL != "" || config.CallbackPrivateKey != ""
	if callbackConfigured && (!validHTTPCallbackURL(config.CallbackURL) || !validCallbackPrivateKey(config.CallbackPrivateKey)) {
		return ErrInvalid
	}
	return nil
}

// normalize 清理部署字段空白并规范域名与地域，不执行远程验证。
func (config *Config) normalize() {
	config.AccessKeyID = strings.TrimSpace(config.AccessKeyID)
	config.SecretAccessKey = strings.TrimSpace(config.SecretAccessKey)
	config.VODAppID = strings.TrimSpace(config.VODAppID)
	config.Region = strings.ToLower(strings.TrimSpace(config.Region))
	config.SpaceName = strings.TrimSpace(config.SpaceName)
	config.CoverDomain = strings.TrimRight(strings.TrimSpace(config.CoverDomain), "/")
	config.CallbackURL = strings.TrimSpace(config.CallbackURL)
	config.CallbackPrivateKey = strings.TrimSpace(config.CallbackPrivateKey)
}

// validHTTPCallbackURL 接受无用户信息和片段的 HTTP 或 HTTPS 回调地址。
func validHTTPCallbackURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" && parsed.User == nil && parsed.Fragment == ""
}

// validCallbackPrivateKey 校验平台要求的 8 至 20 位英数字回调鉴权密钥。
func validCallbackPrivateKey(value string) bool {
	if len(value) < 8 || len(value) > 20 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') {
			return false
		}
	}
	return true
}

// validHTTPSOrigin 只接受没有路径、查询、片段或用户信息的 HTTPS 封面源站。
func validHTTPSOrigin(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment == "" && parsed.User == nil
}
