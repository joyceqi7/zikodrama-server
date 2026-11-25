package operations

import (
	"context"
	"log/slog"

	domain "drama-server/internal/biz/consumer"
)

const (
	// DramaStatusUnpublished 表示官方内容连载状态为未发布，不等于本系统草稿可见性。
	DramaStatusUnpublished = domain.DramaStatusUnpublished
	// DramaStatusUpdating 表示内容仍在连载更新。
	DramaStatusUpdating = domain.DramaStatusUpdating
	// DramaStatusCompleted 表示内容已经完结。
	DramaStatusCompleted = domain.DramaStatusCompleted

	dramaMetadataSchemaVersion = "2026-07-13"
	dramaMetadataSourceURL     = "https://bytedance.larkoffice.com/wiki/L0SwwR5aoihgKZkEdpxcc8oynDb"
)

// DramaMetadataOption 定义运营表单可提交的状态或语言选项。
type DramaMetadataOption struct {
	// Value 是提交给接口的官方状态或语言代码。
	Value string `json:"value"`
	// Label 是运营表单展示的中文名称。
	Label string `json:"label"`
}

// DramaTypeOption 定义官方接受的短剧制作类型。
type DramaTypeOption struct {
	// Value 是提交给 TikTok 的制作类型整数编码。
	Value int `json:"value"`
	// Label 是制作类型的中文展示名称。
	Label string `json:"label"`
}

// DramaMetadataReference 返回官方元数据版本及运营表单的合法选项。
type DramaMetadataReference struct {
	// SchemaVersion 标识当前官方元数据版本。
	SchemaVersion string `json:"schemaVersion"`
	// SourceURL 指向官方元数据规范。
	SourceURL string `json:"sourceUrl"`
	// Statuses 列出官方连载状态，区别于本系统的发布流程状态。
	Statuses []DramaMetadataOption `json:"statuses"`
	// Languages 列出官方接受的原始音频语言代码。
	Languages []DramaMetadataOption `json:"languages"`
	// DramaTypes 列出官方接受的制作类型。
	DramaTypes []DramaTypeOption `json:"dramaTypes"`
	// Tags 是全局 TikTok 标签清单；查询参考数据时返回官方名称。
	Tags []domain.TikTokTag `json:"tags"`
}

// DramaReferenceData 返回官方选项，及官方标签名称。
func (s *DramaService) DramaReferenceData(ctx context.Context) (DramaMetadataReference, error) {
	catalog := DramaMetadataCatalog()
	tags, err := s.ListTikTokTags(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "list TikTok tags failed", "error", err)
		return DramaMetadataReference{}, err
	}
	catalog.Tags = tags
	return catalog, nil
}

var dramaStatusOptions = []DramaMetadataOption{
	{Value: DramaStatusUnpublished, Label: "未发布"},
	{Value: DramaStatusUpdating, Label: "更新中"},
	{Value: DramaStatusCompleted, Label: "已完结"},
}

var dramaLanguageOptions = []DramaMetadataOption{
	{Value: "ar", Label: "阿拉伯语"},
	{Value: "de", Label: "德语"},
	{Value: "en", Label: "英语"},
	{Value: "es", Label: "西班牙语"},
	{Value: "fr", Label: "法语"},
	{Value: "id", Label: "印度尼西亚语"},
	{Value: "it", Label: "意大利语"},
	{Value: "ja", Label: "日语"},
	{Value: "kk", Label: "哈萨克语"},
	{Value: "ko", Label: "韩语"},
	{Value: "pl", Label: "波兰语"},
	{Value: "pt", Label: "葡萄牙语"},
	{Value: "ro", Label: "罗马尼亚语"},
	{Value: "ru", Label: "俄语"},
	{Value: "th", Label: "泰语"},
	{Value: "tr", Label: "土耳其语"},
	{Value: "ug", Label: "维吾尔语"},
	{Value: "vi", Label: "越南语"},
	{Value: "zh", Label: "中文"},
}

var dramaTypeOptions = []DramaTypeOption{
	{Value: 1, Label: "AIGC"},
	{Value: 2, Label: "漫剧"},
	{Value: 3, Label: "真人配音"},
	{Value: 4, Label: "真人本地化"},
}

// DramaMetadataCatalog 返回官方元数据的独立副本，供入口枚举校验和表单展示使用。
func DramaMetadataCatalog() DramaMetadataReference {
	return DramaMetadataReference{
		SchemaVersion: dramaMetadataSchemaVersion,
		SourceURL:     dramaMetadataSourceURL,
		Statuses:      append([]DramaMetadataOption(nil), dramaStatusOptions...),
		Languages:     append([]DramaMetadataOption(nil), dramaLanguageOptions...),
		DramaTypes:    append([]DramaTypeOption(nil), dramaTypeOptions...),
		Tags:          domain.TikTokTagCatalog(),
	}
}
