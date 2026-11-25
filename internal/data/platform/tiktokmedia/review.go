package tiktokmedia

import (
	"context"
	"drama-server/internal/biz/operations"
	"net/http"
)

// AppealReview 按官方整数合同编码 Album/Review ID，单项 ID 保留字符串。
func (c *Client) AppealReview(ctx context.Context, albumID, reviewID string, input operations.TikTokReviewAppealInput) error {
	album, err := numericID(albumID)
	if err != nil {
		return err
	}
	review, err := numericID(reviewID)
	if err != nil {
		return err
	}
	request := map[string]any{
		"client_key": c.clientKey, "album_id": album, "review_id": review,
		"appeal_item_type": input.ItemType, "item_id": input.ItemID, "appeal_reason": input.Reason,
	}
	if len(input.AttachmentURLs) > 0 {
		request["attachment_urls"] = input.AttachmentURLs
	}
	return c.doJSON(ctx, http.MethodPost, "/v2/sg/shortdrama/album/review/appeal/", nil, request, nil)
}

// WithdrawReview 使用官方版本撤回接口，成功响应没有业务数据。
func (c *Client) WithdrawReview(ctx context.Context, albumID string, version int) error {
	album, err := numericID(albumID)
	if err != nil {
		return err
	}
	return c.doJSON(ctx, http.MethodPost, "/v2/sg/shortdrama/album/review/withdraw/", nil, map[string]any{"album_id": album, "version": version}, nil)
}
