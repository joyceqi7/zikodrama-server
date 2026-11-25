package operations

import (
	"drama-server/internal/pkg/identifier"
	"sort"
	"strings"

	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
)

// 校验集合与表单引用数据来自同一官方目录，避免前端可选值与服务端白名单各自维护。
var supportedDramaStatuses = optionValues(operations.DramaMetadataCatalog().Statuses)
var supportedDramaLanguages = optionValues(operations.DramaMetadataCatalog().Languages)
var supportedDramaTypes = typeValues(operations.DramaMetadataCatalog().DramaTypes)
var supportedDramaTags = tikTokTagValues(domain.TikTokTagCatalog())

// validDramaInput 检查已规范化短剧的名称、简介等必填项及非负年份；状态、语言和制作类型枚举由 normalizeDramaMetadata 检查。
func validDramaInput(input operations.DramaInput) bool {
	return strings.TrimSpace(input.Title) != "" && strings.TrimSpace(input.Description) != "" && input.Year >= 0 && input.Language != "" && input.DramaType > 0
}

// validEpisodeInputs 允许保存空分集草稿；存在分集时要求集号从 1 连续且已有分集 ID 不重复。
// 检查时排序副本，既允许前端乱序提交，也不改变请求中的分集顺序；分集 ID 的数据库归属由 Service 判断。
func validEpisodeInputs(inputs []operations.EpisodeInput) bool {
	items := append([]operations.EpisodeInput(nil), inputs...)
	sort.Slice(items, func(left, right int) bool { return items[left].Number < items[right].Number })
	seenIDs := make(map[string]struct{}, len(items))
	for index, item := range items {
		if item.Number != index+1 || strings.TrimSpace(item.Title) == "" || len(item.Description) > 65_535 || item.DurationSeconds < 0 {
			return false
		}
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		if _, exists := seenIDs[id]; exists {
			return false
		}
		seenIDs[id] = struct{}{}
	}
	return true
}

// validTikTokTagIDs 只允许官方目录中的标签 ID，最多三个且不能重复；标识不转成数字。
func validTikTokTagIDs(values []string) bool {
	if len(values) > 3 {
		return false
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if _, exists := supportedDramaTags[value]; !exists {
			return false
		}
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

// normalizeDramaMetadata 补齐更新中、zh 和真人本地化这三个表单默认值，再检查官方枚举。
// 返回 false 表示字段非法；这里不判断草稿是否可编辑或是否符合平台送审条件。
func normalizeDramaMetadata(input operations.DramaInput) (operations.DramaInput, bool) {
	input.Status = strings.TrimSpace(input.Status)
	if input.Status == "" {
		input.Status = operations.DramaStatusUpdating
	}
	input.Language = strings.ToLower(strings.TrimSpace(input.Language))
	if input.Language == "" {
		input.Language = "zh"
	}
	if input.DramaType == 0 {
		input.DramaType = 4
	}
	if _, ok := supportedDramaStatuses[input.Status]; !ok {
		return operations.DramaInput{}, false
	}
	if _, ok := supportedDramaLanguages[input.Language]; !ok {
		return operations.DramaInput{}, false
	}
	if _, ok := supportedDramaTypes[input.DramaType]; !ok {
		return operations.DramaInput{}, false
	}
	normalizedTagIDs := make([]string, len(input.TagIDs))
	for index, value := range input.TagIDs {
		normalizedTagIDs[index] = strings.TrimSpace(value)
	}
	if !validTikTokTagIDs(normalizedTagIDs) {
		return operations.DramaInput{}, false
	}
	input.TagIDs = normalizedTagIDs
	return input, true
}

// optionValues 把官方状态或语言选项转换为入口校验使用的集合。
func optionValues(options []operations.DramaMetadataOption) map[string]struct{} {
	values := make(map[string]struct{}, len(options))
	for _, option := range options {
		values[option.Value] = struct{}{}
	}
	return values
}

// typeValues 把官方制作类型转换为整数集合，用于拒绝目录外的值。
func typeValues(options []operations.DramaTypeOption) map[int]struct{} {
	values := make(map[int]struct{}, len(options))
	for _, option := range options {
		values[option.Value] = struct{}{}
	}
	return values
}

// tikTokTagValues 提取官方标签的稳定 ID；显示名称不参与标识校验。
func tikTokTagValues(options []domain.TikTokTag) map[string]struct{} {
	values := make(map[string]struct{}, len(options))
	for _, option := range options {
		values[option.ID] = struct{}{}
	}
	return values
}

// normalizeDramaInput 校验短剧和连续分集序号，允许先保存不含视频的草稿。
// 分集归属、内容可编辑状态以及送审时的媒资就绪条件由业务层检查。
func normalizeDramaInput(input operations.DramaInput, create bool) (operations.DramaInput, error) {
	// PUT 整体替换分集时必须显式提供清单，防止仅更新资料的旧调用方误删全部分集。
	if !create && input.Episodes == nil {
		return input, operations.ErrInvalidInput
	}
	input, valid := normalizeDramaMetadata(input)
	input.Title = strings.TrimSpace(input.Title)
	input.Description = strings.TrimSpace(input.Description)
	input.CoverURL = strings.TrimSpace(input.CoverURL)
	input.CoverUploadID = strings.TrimSpace(input.CoverUploadID)
	if !valid || !validDramaInput(input) || (input.CoverUploadID != "" && !identifier.ValidDatabaseID(input.CoverUploadID)) {
		return input, operations.ErrInvalidInput
	}
	for i := range input.Episodes {
		episode := &input.Episodes[i]
		episode.ID = strings.TrimSpace(episode.ID)
		episode.Title = strings.TrimSpace(episode.Title)
		episode.Description = strings.TrimSpace(episode.Description)
		episode.PosterURL = strings.TrimSpace(episode.PosterURL)
		// 创建短剧时所有分集都由数据库生成新 ID；更新时允许保留 ID，随后由 Service 检查是否属于该剧。
		if episode.ID != "" && (create || !identifier.ValidDatabaseID(episode.ID)) {
			return input, operations.ErrInvalidInput
		}
	}
	// 所有分集文案规范化后再检查长度和序号，保证校验的是实际提交给业务层的内容。
	if !validEpisodeInputs(input.Episodes) {
		return input, operations.ErrInvalidInput
	}
	return input, nil
}
