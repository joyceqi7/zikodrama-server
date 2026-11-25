// Package apierror 提供稳定 API 错误定义及单语言、多语言消息解析。
package apierror

import (
	"context"
	"fmt"
	"strings"

	"gitee.com/linpbin/styx/i18n"
)

// Definition 把传输状态、稳定错误码、消息键和单语言默认文案绑定在一起。
type Definition struct {
	Status         int
	Code           string
	MessageID      string
	DefaultMessage string
}

// New 创建错误定义，并使用稳定错误码作为消息键。
func New(status int, code, defaultMessage string) (Definition, error) {
	return NewWithMessageID(status, code, code, defaultMessage)
}

// NewWithMessageID 创建使用独立消息键的错误定义。
func NewWithMessageID(status int, code, messageID, defaultMessage string) (Definition, error) {
	definition, err := normalize(Definition{
		Status: status, Code: strings.TrimSpace(code),
		MessageID: strings.TrimSpace(messageID), DefaultMessage: strings.TrimSpace(defaultMessage),
	})
	if err != nil {
		return Definition{}, err
	}
	if definition.DefaultMessage == "" {
		return Definition{}, fmt.Errorf("apierror: default message is required")
	}
	return definition, nil
}

// NewCatalog 创建只依赖消息目录的错误定义，并使用稳定错误码作为消息键。
func NewCatalog(status int, code string) (Definition, error) {
	return NewCatalogWithMessageID(status, code, code)
}

// NewCatalogWithMessageID 创建使用独立消息键、只依赖消息目录的错误定义。
func NewCatalogWithMessageID(status int, code, messageID string) (Definition, error) {
	return normalize(Definition{Status: status, Code: code, MessageID: messageID})
}

func normalize(definition Definition) (Definition, error) {
	definition.Code = strings.TrimSpace(definition.Code)
	definition.MessageID = strings.TrimSpace(definition.MessageID)
	definition.DefaultMessage = strings.TrimSpace(definition.DefaultMessage)
	if definition.Status < 400 || definition.Status > 599 {
		return Definition{}, fmt.Errorf("apierror: status must be between 400 and 599")
	}
	if definition.Code == "" {
		return Definition{}, fmt.Errorf("apierror: code is required")
	}
	if definition.MessageID == "" {
		definition.MessageID = definition.Code
	}
	return definition, nil
}

// Must 创建错误定义，并使用稳定错误码作为消息键。
func Must(status int, code, defaultMessage string) Definition {
	definition, err := New(status, code, defaultMessage)
	if err != nil {
		panic(err)
	}
	return definition
}

// MustWithMessageID 创建使用独立消息键的错误定义，配置非法时 panic。
func MustWithMessageID(status int, code, messageID, defaultMessage string) Definition {
	definition, err := NewWithMessageID(status, code, messageID, defaultMessage)
	if err != nil {
		panic(err)
	}
	return definition
}

// MustCatalog 创建只依赖消息目录的错误定义，配置非法时 panic。
func MustCatalog(status int, code string) Definition {
	definition, err := NewCatalog(status, code)
	if err != nil {
		panic(err)
	}
	return definition
}

// MustCatalogWithMessageID 创建使用独立消息键、只依赖消息目录的错误定义。
func MustCatalogWithMessageID(status int, code, messageID string) Definition {
	definition, err := NewCatalogWithMessageID(status, code, messageID)
	if err != nil {
		panic(err)
	}
	return definition
}

// Message 在存在请求级 Localizer 时返回翻译，否则返回单语言默认文案。
func (definition Definition) Message(ctx context.Context, templateData any) string {
	fallback := definition.DefaultMessage
	if fallback == "" {
		fallback = definition.MessageID
	}
	return i18n.LocalizeOr(ctx, definition.MessageID, templateData, fallback)
}

// Registry 保存一组不可变错误定义。
type Registry struct {
	definitions []Definition
	byCode      map[string]Definition
}

// NewRegistry 创建错误注册表并拒绝重复稳定码。
func NewRegistry(definitions ...Definition) (*Registry, error) {
	registry := &Registry{
		definitions: make([]Definition, 0, len(definitions)),
		byCode:      make(map[string]Definition, len(definitions)),
	}
	for _, definition := range definitions {
		validated, err := normalize(definition)
		if err != nil {
			return nil, err
		}
		if _, exists := registry.byCode[validated.Code]; exists {
			return nil, fmt.Errorf("apierror: duplicate code %q", validated.Code)
		}
		registry.definitions = append(registry.definitions, validated)
		registry.byCode[validated.Code] = validated
	}
	return registry, nil
}

// MustRegistry 创建注册表，配置非法时 panic。
func MustRegistry(definitions ...Definition) *Registry {
	registry, err := NewRegistry(definitions...)
	if err != nil {
		panic(err)
	}
	return registry
}

// Lookup 按稳定错误码查找定义。
func (registry *Registry) Lookup(code string) (Definition, bool) {
	if registry == nil {
		return Definition{}, false
	}
	definition, ok := registry.byCode[strings.TrimSpace(code)]
	return definition, ok
}

// Definitions 返回注册定义的副本。
func (registry *Registry) Definitions() []Definition {
	if registry == nil {
		return nil
	}
	return append([]Definition(nil), registry.definitions...)
}
