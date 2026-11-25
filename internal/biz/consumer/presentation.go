package consumer

// ConsumerView 保留已发布内容的主文案，转换观众状态并隐藏内部标签目录。
func (d Drama) ConsumerView() Drama {
	view := d
	view.TikTokTags = nil
	if view.Status == DramaStatusUnpublished || view.Status == DramaStatusUpdating {
		view.Status = "ongoing"
	}
	view.Tags = make([]string, 0, len(d.TikTokTags))
	for _, tag := range d.TikTokTags {
		view.Tags = append(view.Tags, tag.DisplayName())
	}
	view.Episodes = append([]Episode(nil), d.Episodes...)
	return view
}
