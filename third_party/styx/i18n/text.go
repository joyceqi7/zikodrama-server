package i18n

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"

	"golang.org/x/text/language"
)

// TextMap 是持久化业务文案使用的多语言值，键为 BCP 47 语言标签。
type TextMap map[string]string

// Value 把多语言 Map 编码为 JSON，供 database/sql 持久化。
func (texts TextMap) Value() (driver.Value, error) {
	return json.Marshal(texts)
}

// Scan 从数据库 JSON 字段恢复多语言 Map。
func (texts *TextMap) Scan(source any) error {
	if texts == nil {
		return fmt.Errorf("i18n: scan TextMap into nil receiver")
	}
	if source == nil {
		*texts = nil
		return nil
	}
	var data []byte
	switch value := source.(type) {
	case []byte:
		data = value
	case string:
		data = []byte(value)
	default:
		return fmt.Errorf("i18n: cannot scan TextMap from %T", source)
	}
	if len(data) == 0 {
		*texts = nil
		return nil
	}
	var decoded map[string]string
	if err := json.Unmarshal(data, &decoded); err != nil {
		return fmt.Errorf("i18n: scan TextMap: %w", err)
	}
	*texts = decoded
	return nil
}

// Get 按指定语言返回文案，并以英语作为默认回退。
func (texts TextMap) Get(locale string) string {
	return texts.GetOr(locale, language.English.String())
}

// GetOr 按“完整语言、基础语言、默认语言”的顺序返回第一个非空文案。
func (texts TextMap) GetOr(locale, defaultLocale string) string {
	values := make(map[string]string, len(texts))
	for key, value := range texts {
		canonical, ok := ParseLocale(key)
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		values[canonical] = value
	}
	for _, candidate := range textFallbackChain(locale, defaultLocale) {
		if value := values[candidate]; strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// TextFromContext 使用请求上下文中的客户端语言读取业务文案，并回退英语。
func TextFromContext(ctx context.Context, texts map[string]string) string {
	return TextMap(texts).Get(LocaleFromContext(ctx))
}

func textFallbackChain(locale, defaultLocale string) []string {
	fallback, ok := ParseLocale(defaultLocale)
	if !ok {
		fallback = language.English.String()
	}
	requested, ok := ParseLocale(locale)
	if !ok {
		requested = fallback
	}
	candidates := []string{requested}
	candidates = append(candidates, parentLocales(requested)...)
	candidates = append(candidates, fallback)
	candidates = append(candidates, parentLocales(fallback)...)
	result := make([]string, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		result = append(result, candidate)
	}
	return result
}

func parentLocales(locale string) []string {
	canonical, ok := ParseLocale(locale)
	if !ok {
		return nil
	}
	result := make([]string, 0, 2)
	for index := strings.LastIndexByte(canonical, '-'); index > 0; index = strings.LastIndexByte(canonical, '-') {
		canonical = canonical[:index]
		result = append(result, canonical)
	}
	return result
}
