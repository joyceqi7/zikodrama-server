package apimessages

import (
	"embed"

	styxi18n "gitee.com/linpbin/styx/i18n"
)

// DefaultLocale 固定消费者 API 错误文案为英语，不提供语言选择。
const DefaultLocale = "en"

//go:embed messages.*.json
var messageFiles embed.FS

var messageFileNames = []string{"messages.en.json"}

// NewMessageBundle 创建消费者 API 使用的不可变消息目录。
func NewMessageBundle() (*styxi18n.Bundle, error) {
	return styxi18n.NewBundle(
		DefaultLocale,
		styxi18n.WithMessageFiles(messageFiles, messageFileNames...),
	)
}
