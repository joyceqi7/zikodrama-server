package consumer

var officialTikTokTags = []TikTokTag{
	{ID: "1", OfficialName: "医神"},
	{ID: "2", OfficialName: "赘婿"},
	{ID: "3", OfficialName: "鉴宝"},
	{ID: "4", OfficialName: "战神"},
	{ID: "5", OfficialName: "娱乐明星"},
	{ID: "6", OfficialName: "神医"},
	{ID: "7", OfficialName: "重生"},
	{ID: "8", OfficialName: "职场"},
	{ID: "9", OfficialName: "逆袭"},
	{ID: "10", OfficialName: "复仇"},
	{ID: "11", OfficialName: "青春"},
	{ID: "12", OfficialName: "官场"},
	{ID: "13", OfficialName: "家庭情感"},
	{ID: "14", OfficialName: "乡村"},
	{ID: "15", OfficialName: "正能量"},
	{ID: "16", OfficialName: "成长"},
	{ID: "17", OfficialName: "伦理"},
	{ID: "18", OfficialName: "都市情感"},
	{ID: "19", OfficialName: "社会话题"},
	{ID: "20", OfficialName: "灵异"},
	{ID: "21", OfficialName: "悬疑推理"},
	{ID: "22", OfficialName: "虐恋"},
	{ID: "23", OfficialName: "甜宠"},
	{ID: "24", OfficialName: "军婚"},
	{ID: "25", OfficialName: "年代"},
	{ID: "26", OfficialName: "萌宝"},
	{ID: "27", OfficialName: "腹黑"},
	{ID: "28", OfficialName: "总裁"},
	{ID: "29", OfficialName: "宫斗宅斗"},
	{ID: "30", OfficialName: "穿越"},
	{ID: "31", OfficialName: "种田经商"},
	{ID: "33", OfficialName: "民俗"},
	{ID: "34", OfficialName: "古装"},
	{ID: "35", OfficialName: "穿越战争"},
	{ID: "36", OfficialName: "现代军事"},
	{ID: "37", OfficialName: "奇幻"},
	{ID: "38", OfficialName: "科幻"},
	{ID: "39", OfficialName: "玄幻"},
	{ID: "40", OfficialName: "热血"},
	{ID: "41", OfficialName: "历史"},
	{ID: "42", OfficialName: "搞笑"},
	{ID: "43", OfficialName: "仙侠"},
	{ID: "44", OfficialName: "武侠"},
	{ID: "45", OfficialName: "二次元"},
	{ID: "46", OfficialName: "其他"},
}

// TikTokTagCatalog 返回带默认中文名称的官方标签目录副本。
func TikTokTagCatalog() []TikTokTag {
	return append([]TikTokTag(nil), officialTikTokTags...)
}

// FindTikTokTag 按官方字符串标识查找标签基本资料；不存在时返回 false。
func FindTikTokTag(id string) (TikTokTag, bool) {
	for _, tag := range officialTikTokTags {
		if tag.ID == id {
			return tag, true
		}
	}
	return TikTokTag{}, false
}

// AttachTikTokTags 按短剧 TagIDs 顺序补齐官方标签，跳过不存在的标签标识。
func AttachTikTokTags(drama *Drama) {
	AttachTikTokTagCatalog(drama, TikTokTagCatalog())
}

// DisplayName 优先返回运营英文翻译，未维护时保留官方名称。
func (t TikTokTag) DisplayName() string {
	if t.EnglishName != "" {
		return t.EnglishName
	}
	return t.OfficialName
}

// AttachTikTokTagCatalog 按短剧标签顺序关联当前目录，翻译不冻结到发布快照。
func AttachTikTokTagCatalog(drama *Drama, catalog []TikTokTag) {
	if drama == nil {
		return
	}
	byID := make(map[string]TikTokTag, len(officialTikTokTags))
	for _, tag := range catalog {
		byID[tag.ID] = tag
	}
	drama.TikTokTags = make([]TikTokTag, 0, len(drama.TagIDs))
	for _, id := range drama.TagIDs {
		if tag, exists := byID[id]; exists {
			drama.TikTokTags = append(drama.TikTokTags, tag)
		}
	}
}
