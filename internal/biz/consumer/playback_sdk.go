package consumer

const (
	// PlaybackProviderTikTokMinis 表示播放器构造器必须来自 TikTok Minis SDK。
	PlaybackProviderTikTokMinis = "tiktok_minis"
	// PlaybackChannelBytePlus 表示审核媒资存放在 TikTok 绑定的 BytePlus VOD。
	PlaybackChannelBytePlus = "byteplus"
)

// PlaybackSDKConfig 是 H5 选择应用当前播放通道所需的公开配置。
// 直连 License 由 Mini 随包提供，VOD Space、访问密钥与播放 Token 不通过 Bootstrap 暴露。
type PlaybackSDKConfig struct {
	// Provider 来自可信应用配置，官方为 tiktok_minis，直连为 byteplus_web。
	Provider string `json:"provider"`
	// Channel 固定为 byteplus；官方应用将此值原样传给 TTMinis.getPlayer。
	Channel string `json:"channel"`
	// Capabilities 固定关闭基础版的高级预加载。
	Capabilities *PlaybackCapabilities `json:"capabilities,omitempty"`
}

// PlaybackCapabilities 表示许可允许的客户端能力。
type PlaybackCapabilities struct {
	// MediaPreload 固定为 false，直连基础版不启用高级媒体预加载。
	MediaPreload bool `json:"mediaPreload"`
}
