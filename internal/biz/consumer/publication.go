package consumer

import "slices"

// ConfirmedAuthorizationCount 统计仍在线上快照中的其他小程序授权，去重且不包含拥有方或空标识。
func (drama *Drama) ConfirmedAuthorizationCount() int {
	if drama == nil || drama.PublishStatus != "published" {
		return 0
	}
	ids := make(map[string]struct{}, len(drama.AuthorizedApplicationIDs))
	for _, id := range drama.AuthorizedApplicationIDs {
		if id != "" && id != drama.OwnerApplicationID {
			ids[id] = struct{}{}
		}
	}
	return len(ids)
}

// PublishedForApplication 只返回目标应用可用的线上快照，绝不降级到未送审的编辑内容。
func (drama Drama) PublishedForApplication(applicationID string) (*Drama, bool) {
	if applicationID == "" || drama.Published == nil || drama.Published.PublishStatus != "published" {
		return nil, false
	}
	published := drama.Published
	if published.OwnerApplicationID != applicationID && !slices.Contains(published.AuthorizedApplicationIDs, applicationID) {
		return nil, false
	}
	return published, true
}
