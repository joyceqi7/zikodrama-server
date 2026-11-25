// Package i18n 提供基于 go-i18n 的消息目录、语言协商和 Kratos 传输层接入。
package i18n

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

// Bundle 是可并发读取的本地化消息目录。
type Bundle struct {
	defaultLocale    string
	supportedLocales []string
	bundle           *goi18n.Bundle
}

type bundleOptions struct {
	files []messageFiles
}

type messageFiles struct {
	fs    fs.FS
	paths []string
}

// Option 配置消息目录。
type Option func(*bundleOptions) error

// WithMessageFiles 从 fs.FS 加载 go-i18n 消息文件。文件名必须包含语言标签，
// 例如 messages.en.json 或 active.zh-CN.toml。
func WithMessageFiles(fileSystem fs.FS, paths ...string) Option {
	return func(options *bundleOptions) error {
		if fileSystem == nil {
			return fmt.Errorf("i18n: message file system is required")
		}
		if len(paths) == 0 {
			return fmt.Errorf("i18n: at least one message file is required")
		}
		copied := make([]string, 0, len(paths))
		for _, path := range paths {
			path = strings.TrimSpace(path)
			if path == "" {
				return fmt.Errorf("i18n: message file path is required")
			}
			copied = append(copied, path)
		}
		options.files = append(options.files, messageFiles{fs: fileSystem, paths: copied})
		return nil
	}
}

// NewBundle 创建消息目录并验证默认语言和消息资源。
func NewBundle(defaultLocale string, optionList ...Option) (*Bundle, error) {
	defaultLocale, ok := ParseLocale(defaultLocale)
	if !ok {
		return nil, fmt.Errorf("i18n: invalid default locale")
	}
	var options bundleOptions
	for _, option := range optionList {
		if option == nil {
			return nil, fmt.Errorf("i18n: option is required")
		}
		if err := option(&options); err != nil {
			return nil, err
		}
	}
	inner := goi18n.NewBundle(language.MustParse(defaultLocale))
	for _, group := range options.files {
		for _, path := range group.paths {
			if _, err := inner.LoadMessageFileFS(group.fs, path); err != nil {
				return nil, fmt.Errorf("i18n: load message file %q: %w", path, err)
			}
		}
	}
	supported := make([]string, 0, len(inner.LanguageTags()))
	for _, tag := range inner.LanguageTags() {
		supported = append(supported, tag.String())
	}
	return &Bundle{defaultLocale: defaultLocale, supportedLocales: supported, bundle: inner}, nil
}

// DefaultLocale 返回目录的默认语言。
func (b *Bundle) DefaultLocale() string {
	if b == nil {
		return ""
	}
	return b.defaultLocale
}

// SupportedLocales 返回目录已加载的语言列表。
func (b *Bundle) SupportedLocales() []string {
	if b == nil {
		return nil
	}
	return append([]string(nil), b.supportedLocales...)
}

// WithLocale 把请求语言和按消息目录协商后的 Localizer 放入 context。
func (b *Bundle) WithLocale(ctx context.Context, locale string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	requested, ok := ParseLocale(locale)
	if !ok {
		requested = b.defaultLocale
	}
	resolved := Negotiate(requested, b.defaultLocale, b.supportedLocales)
	localizer := &Localizer{
		locale: resolved,
		inner:  goi18n.NewLocalizer(b.bundle, resolved, b.defaultLocale),
		bundle: b,
	}
	ctx = context.WithValue(ctx, localeContextKey{}, requested)
	return context.WithValue(ctx, localizerContextKey{}, localizer)
}

// WithAcceptLanguage 保存客户端首选语言，并按消息目录独立协商 Localizer。
func (b *Bundle) WithAcceptLanguage(ctx context.Context, header string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	requested := PreferredLocale(header, b.defaultLocale)
	resolved := Negotiate(header, b.defaultLocale, b.supportedLocales)
	localizer := &Localizer{
		locale: resolved,
		inner:  goi18n.NewLocalizer(b.bundle, resolved, b.defaultLocale),
		bundle: b,
	}
	ctx = context.WithValue(ctx, localeContextKey{}, requested)
	return context.WithValue(ctx, localizerContextKey{}, localizer)
}

// Localizer 封装单次请求使用的 go-i18n Localizer。
type Localizer struct {
	locale string
	inner  *goi18n.Localizer
	bundle *Bundle
}

// Locale 返回当前协商后的语言标签。
func (l *Localizer) Locale() string {
	if l == nil {
		return ""
	}
	return l.locale
}

// Localize 按消息 ID 和模板数据翻译文本。
func (l *Localizer) Localize(messageID string, templateData any) (string, error) {
	if l == nil || l.inner == nil {
		return "", fmt.Errorf("i18n: localizer is unavailable")
	}
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return "", fmt.Errorf("i18n: message ID is required")
	}
	message, err := l.inner.Localize(&goi18n.LocalizeConfig{
		MessageID:    messageID,
		TemplateData: templateData,
	})
	var missing *goi18n.MessageNotFoundErr
	if message != "" && errors.As(err, &missing) {
		return message, nil
	}
	return message, err
}

type localizerContextKey struct{}
type localeContextKey struct{}

// WithLocale 更新 context 的请求语言；已有 Bundle 时同步重建 Localizer。
func WithLocale(ctx context.Context, locale string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if localizer, ok := FromContext(ctx); ok && localizer.bundle != nil {
		return localizer.bundle.WithLocale(ctx, locale)
	}
	canonical, ok := ParseLocale(locale)
	if !ok {
		canonical = language.English.String()
	}
	return context.WithValue(ctx, localeContextKey{}, canonical)
}

// FromContext 返回当前请求的 Localizer。
func FromContext(ctx context.Context) (*Localizer, bool) {
	if ctx == nil {
		return nil, false
	}
	localizer, ok := ctx.Value(localizerContextKey{}).(*Localizer)
	return localizer, ok && localizer != nil
}

// LocaleFromContext 返回客户端请求语言；它不受消息目录支持语言限制。
// 未安装 i18n 时返回空字符串。
func LocaleFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if locale, ok := ctx.Value(localeContextKey{}).(string); ok {
		return locale
	}
	localizer, ok := FromContext(ctx)
	if !ok {
		return ""
	}
	return localizer.Locale()
}

// Localize 使用 context 中的 Localizer 翻译消息。
func Localize(ctx context.Context, messageID string, templateData any) (string, error) {
	localizer, ok := FromContext(ctx)
	if !ok {
		return "", fmt.Errorf("i18n: localizer is unavailable")
	}
	return localizer.Localize(messageID, templateData)
}

// LocalizeOr 翻译消息；目录不可用或消息缺失时返回调用方提供的安全回退文本。
func LocalizeOr(ctx context.Context, messageID string, templateData any, fallback string) string {
	message, err := Localize(ctx, messageID, templateData)
	if err != nil || message == "" {
		return fallback
	}
	return message
}
