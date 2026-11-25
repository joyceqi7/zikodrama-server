package operations

import (
	"drama-server/internal/biz/operations"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

// normalizeReviewAppeal 拒绝未知对象类型及非公开 HTTPS 附件；不读取远程证据文件。
func normalizeReviewAppeal(input operations.TikTokReviewAppealInput) (operations.TikTokReviewAppealInput, error) {
	input.ItemID, input.Reason = strings.TrimSpace(input.ItemID), strings.TrimSpace(input.Reason)
	if (input.ItemType != 1 && input.ItemType != 2) || !regexp.MustCompile(`^[1-9][0-9]{0,39}$`).MatchString(input.ItemID) || input.Reason == "" || utf8.RuneCountInString(input.Reason) > 5000 || len(input.AttachmentURLs) > 10 {
		return input, fmt.Errorf("invalid review appeal fields")
	}
	for index, value := range input.AttachmentURLs {
		value = strings.TrimSpace(value)
		parsed, err := url.Parse(value)
		if err != nil || len(value) > 2048 || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
			return input, fmt.Errorf("invalid appeal attachment URL")
		}
		input.AttachmentURLs[index] = value
	}
	return input, nil
}

// appealTikTokReview 校验申诉参数后交给可恢复发布流程，返回排队状态。
func (h *Handler) appealTikTokReview(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "dramaID") {
		return
	}
	var input operations.TikTokReviewAppealInput
	if !decode(w, r, &input) {
		return
	}
	input, err := normalizeReviewAppeal(input)
	if rejectInvalidInput(w, r, "review appeal input rejected", err) {
		return
	}
	result, err := h.service.DramaService.AppealTikTokReview(r.Context(), r.PathValue("dramaID"), input)
	writeResult(w, r, http.StatusAccepted, result, err)
}

// withdrawTikTokReview 撤回对象由当前持久化任务确定，不能指定其他平台版本。
func (h *Handler) withdrawTikTokReview(w http.ResponseWriter, r *http.Request) {
	if !validatePathIDs(w, r, "dramaID") {
		return
	}
	result, err := h.service.DramaService.WithdrawTikTokReview(r.Context(), r.PathValue("dramaID"))
	writeResult(w, r, http.StatusAccepted, result, err)
}
