package operations

import (
	"net/url"
	"regexp"
	"strings"
	"time"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/operations"
	"drama-server/internal/pkg/identifier"
)

var applicationOfficialIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,255}$`)

// normalizeApplicationInput 校验完整更新配置与播放修订；省略播放器字段时保留原通道，身份与切换并发约束由业务层检查。
func normalizeApplicationInput(input operations.ApplicationInput) (operations.ApplicationInput, error) {
	return normalizeApplicationConfiguration(input, false)
}

// normalizeCreateApplicationInput 要求官方 AppID、名称、Client Key、Client Secret 和变现模式；默认创建停用官方应用，直连参数统一由全局配置提供。
func normalizeCreateApplicationInput(input operations.ApplicationInput) (operations.ApplicationInput, error) {
	return normalizeApplicationConfiguration(input, true)
}

// normalizeApplicationConfiguration 规范化应用配置并按创建或更新入口校验，唯一性由业务层检查。
func normalizeApplicationConfiguration(input operations.ApplicationInput, creating bool) (operations.ApplicationInput, error) {
	// 省略地域和时区时使用现有合同默认值；仅补默认值，不根据服务器本地时区推断应用所在地。
	regionCode := strings.ToUpper(strings.TrimSpace(input.RegionCode))
	if regionCode == "" {
		regionCode = "GLOBAL"
	}
	timeZone := strings.TrimSpace(input.TimeZone)
	if timeZone == "" {
		timeZone = "UTC"
	}
	// 新建和完整更新均要求明确模式，不接受旧请求省略字段。
	mode := application.MonetizationMode(strings.ToUpper(strings.TrimSpace(string(input.MonetizationMode))))
	switch mode {
	case application.MonetizationModeIAA, application.MonetizationModeIAP, application.MonetizationModeHybrid:
	default:
		return operations.ApplicationInput{}, operations.ErrInvalidInput
	}
	app := operations.ApplicationInput{
		MiniReview:       input.MiniReview,
		SubjectID:        strings.TrimSpace(input.SubjectID),
		PlaybackProvider: strings.TrimSpace(input.PlaybackProvider),
		PlaybackRevision: input.PlaybackRevision,
		MonetizationMode: mode,
		Name:             strings.TrimSpace(input.Name), LogoURL: strings.TrimSpace(input.LogoURL),
		TikTokAppID: strings.TrimSpace(input.TikTokAppID), ClientKey: strings.TrimSpace(input.ClientKey),
		SecretRef: strings.TrimSpace(input.SecretRef), ClientSecret: input.ClientSecret,
		RegionCode: regionCode, TimeZone: timeZone, ComplianceBaseURL: strings.TrimSpace(input.ComplianceBaseURL),
		AdUnits: input.AdUnits.Normalized(), Status: strings.ToLower(strings.TrimSpace(input.Status)),
	}
	if review := app.MiniReview; review != nil {
		review.Version = strings.TrimSpace(review.Version)
		if !normalizeMiniReviewVersions(review) || review.Revision < 0 || review.Revision >= 9007199254740991 || len(review.DramaIDs) > 100 || (creating && review.Revision != 0) {
			return operations.ApplicationInput{}, operations.ErrInvalidInput
		}
		seen := map[string]bool{}
		for i, id := range review.DramaIDs {
			id = strings.TrimSpace(id)
			if !identifier.ValidDatabaseID(id) || seen[id] {
				return operations.ApplicationInput{}, operations.ErrInvalidInput
			}
			seen[id], review.DramaIDs[i] = true, id
		}
		if review.DramaIDs == nil {
			review.DramaIDs = []string{}
		}
	}
	if !identifier.ValidDatabaseID(app.SubjectID) {
		return operations.ApplicationInput{}, operations.ErrInvalidInput
	}
	// 只在创建时补官方默认值；旧更新请求省略通道必须留给业务层保留既有配置。
	if creating && app.PlaybackProvider == "" {
		app.PlaybackProvider = "tiktok_minis"
	}
	if app.PlaybackProvider != "" && app.PlaybackProvider != "tiktok_minis" && app.PlaybackProvider != "byteplus_web" {
		return operations.ApplicationInput{}, operations.ErrInvalidInput
	}
	if app.PlaybackRevision != nil && (*app.PlaybackRevision < 0 || *app.PlaybackRevision >= 9007199254740991) {
		return operations.ApplicationInput{}, operations.ErrInvalidInput
	}
	// 密钥不裁剪或改写；创建必填，更新空值代表保留，拒绝空白与控制字符。
	if (creating && app.ClientSecret == "") || len(app.ClientSecret) > 255 ||
		(app.ClientSecret != "" && (strings.TrimSpace(app.ClientSecret) == "" || strings.ContainsAny(app.ClientSecret, "\r\n\t\x00"))) {
		return operations.ApplicationInput{}, operations.ErrInvalidInput
	}
	if creating && app.Status == "" {
		app.Status = application.StatusDisabled
	}
	if app.Name == "" || len(app.Name) > 128 ||
		(app.Status != application.StatusActive && app.Status != application.StatusDisabled) {
		return operations.ApplicationInput{}, operations.ErrInvalidInput
	}
	if !validRegionCode(app.RegionCode) {
		return operations.ApplicationInput{}, operations.ErrInvalidInput
	}
	// 新建必须提供分发所需 Client Key；历史停用记录仍允许独立维护，启用前必须补齐。
	if !applicationOfficialIDPattern.MatchString(app.TikTokAppID) || ((creating || app.Status == application.StatusActive) && app.ClientKey == "") ||
		len(app.TikTokAppID) > 255 || len(app.ClientKey) > 255 || strings.ContainsAny(app.TikTokAppID+app.ClientKey, "\r\n\t") {
		return operations.ApplicationInput{}, operations.ErrInvalidInput
	}
	if len(app.AdUnits.RewardedVideo) > application.MaxRewardedAdUnits || len(app.AdUnits.Interstitial) > 1 ||
		!validAdUnitCodes(app.AdUnits.RewardedVideo) || !validAdUnitCodes(app.AdUnits.Interstitial) {
		return operations.ApplicationInput{}, operations.ErrInvalidInput
	}
	if _, err := time.LoadLocation(app.TimeZone); err != nil {
		return operations.ApplicationInput{}, operations.ErrInvalidInput
	}
	// 对外地址要求 HTTPS 且不能嵌入 userinfo 凭据。
	if app.LogoURL != "" {
		parsedLogo, err := url.Parse(app.LogoURL)
		if err != nil || parsedLogo.Scheme != "https" || parsedLogo.Host == "" || parsedLogo.User != nil {
			return operations.ApplicationInput{}, operations.ErrInvalidInput
		}
	}
	// 兼容已有合规地址；新应用不再要求单独配置，提供时仍校验 HTTPS 格式。
	if app.ComplianceBaseURL != "" {
		parsedCompliance, err := url.Parse(app.ComplianceBaseURL)
		if err != nil || parsedCompliance.Scheme != "https" || parsedCompliance.Host == "" || parsedCompliance.User != nil {
			return operations.ApplicationInput{}, operations.ErrInvalidInput
		}
	}
	if app.Status == application.StatusActive && app.MonetizationMode != application.MonetizationModeIAP && !app.AdUnits.Complete() {
		return operations.ApplicationInput{}, operations.ErrApplicationAdUnitsIncomplete
	}
	return app, nil
}

// validAdUnitCodes 检查编码长度；调用前已规范化并按广告类型校验数量。
// 这里只检查编码形态，是否被消费设置引用由 Service 检查。
func validAdUnitCodes(values []string) bool {
	for _, value := range values {
		if value == "" || len(value) > 255 {
			return false
		}
	}
	return true
}

// validRegionCode 接受 GLOBAL 或两位大写字母地域码；这里只检查形态，不验证国家名录。
func validRegionCode(value string) bool {
	if value == "GLOBAL" {
		return true
	}
	return len(value) == 2 && value[0] >= 'A' && value[0] <= 'Z' && value[1] >= 'A' && value[1] <= 'Z'
}

// validApplicationCode 校验投放活动的稳定编码：最多 64 字节，首位为小写字母或数字，后续可含 - 和 _。
// 该规则与管理员用户名、角色编码的 RBAC 规则不同。
func validApplicationCode(code string) bool {
	if code == "" || len(code) > 64 {
		return false
	}
	for index, character := range code {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || index > 0 && (character == '-' || character == '_') {
			continue
		}
		return false
	}
	return true
}
