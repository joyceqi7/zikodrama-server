package i18n

import (
	"strings"

	"golang.org/x/text/language"
)

// ParseLocale 解析并规范化 BCP 47 语言标签。
func ParseLocale(value string) (string, bool) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "_", "-"))
	if value == "" {
		return "", false
	}
	tag, err := language.Parse(value)
	if err != nil {
		return "", false
	}
	return tag.String(), true
}

// PreferredLocale 返回 Accept-Language 中优先级最高的合法语言；请求缺失或非法时返回默认语言。
// 它不受消息目录支持语言限制，适合业务内容按请求语言自行匹配翻译。
func PreferredLocale(header, defaultLocale string) string {
	fallback, ok := ParseLocale(defaultLocale)
	if !ok {
		fallback = language.English.String()
	}
	tags, _, err := language.ParseAcceptLanguage(strings.TrimSpace(header))
	if err != nil || len(tags) == 0 {
		return fallback
	}
	return tags[0].String()
}

// Negotiate 根据 Accept-Language、默认语言和允许语言列表协商语言。
// 当允许列表只声明基础语言时，保留客户端请求的合法区域标签。
func Negotiate(header, defaultLocale string, supportedLocales []string) string {
	fallback, ok := ParseLocale(defaultLocale)
	if !ok {
		fallback = language.English.String()
	}
	type supportedLocale struct {
		canonical string
		base      string
		baseOnly  bool
	}
	allowed := make([]supportedLocale, 0, len(supportedLocales))
	for _, value := range supportedLocales {
		canonical, valid := ParseLocale(value)
		if !valid {
			continue
		}
		tag := language.MustParse(canonical)
		base, confidence := tag.Base()
		if confidence == language.No {
			continue
		}
		duplicate := false
		for _, existing := range allowed {
			if existing.canonical == canonical {
				duplicate = true
				break
			}
		}
		if !duplicate {
			allowed = append(allowed, supportedLocale{
				canonical: canonical,
				base:      base.String(),
				baseOnly:  canonical == base.String(),
			})
		}
	}
	if len(allowed) == 0 {
		return fallback
	}
	fallbackAllowed := false
	for _, candidate := range allowed {
		if candidate.canonical == fallback {
			fallbackAllowed = true
			break
		}
	}
	if !fallbackAllowed {
		fallback = allowed[0].canonical
	}

	tags, _, err := language.ParseAcceptLanguage(strings.TrimSpace(header))
	if err != nil {
		return fallback
	}
	for _, tag := range tags {
		canonical := tag.String()
		base, confidence := tag.Base()
		if confidence == language.No {
			continue
		}
		for _, candidate := range allowed {
			if candidate.canonical == canonical {
				return canonical
			}
			if candidate.baseOnly && candidate.base == base.String() {
				return canonical
			}
		}
		for _, candidate := range allowed {
			if candidate.base == base.String() {
				return candidate.canonical
			}
		}
	}
	return fallback
}
