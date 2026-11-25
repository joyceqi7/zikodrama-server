package operations

import (
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/operations"
	"drama-server/internal/pkg/identifier"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var adPlacementPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)
var adVersionPattern = regexp.MustCompile(`^(unknown|[0-9]+(?:\.[0-9]+){0,4})$`)

// adAnalytics 在 HTTP 边界验证日期、有限维度及绑定应用和筛选条件的游标。
func (h *Handler) adAnalytics(w http.ResponseWriter, r *http.Request) {
	v, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		rejectInvalidInput(w, r, "invalid ad analytics query", err)
		return
	}
	q := operations.AdAnalyticsQuery{From: strings.TrimSpace(v.Get("from")), To: strings.TrimSpace(v.Get("to")), AdType: strings.TrimSpace(v.Get("adType")), AdUnitID: strings.TrimSpace(v.Get("adUnitId")), Scene: strings.TrimSpace(v.Get("scene")), ClientVersion: strings.TrimSpace(v.Get("clientVersion")), OS: strings.TrimSpace(v.Get("os")), TikTokVersion: strings.TrimSpace(v.Get("tiktokVersion")), ErrorCode: strings.TrimSpace(v.Get("errorCode")), PageSize: 20}
	from, e1 := time.Parse(time.DateOnly, q.From)
	to, e2 := time.Parse(time.DateOnly, q.To)
	today := time.Now().UTC().Truncate(24 * time.Hour)
	valid := e1 == nil && e2 == nil && !to.Before(from) && !to.After(today) && !from.Before(today.AddDate(0, 0, -89)) && to.Sub(from) < 90*24*time.Hour
	valid = valid && (q.AdType == "" || q.AdType == "rewarded" || q.AdType == "interstitial") && (q.AdUnitID == "" || adPlacementPattern.MatchString(q.AdUnitID)) && (q.ClientVersion == "" || postbackVersionPattern.MatchString(q.ClientVersion)) && (q.ErrorCode == "" || postbackCodePattern.MatchString(q.ErrorCode)) && (q.OS == "" || q.OS == "ios" || q.OS == "android" || q.OS == "unknown") && (q.TikTokVersion == "" || len(q.TikTokVersion) <= 32 && adVersionPattern.MatchString(q.TikTokVersion))
	switch q.Scene {
	case "", "entry_reward", "episode_unlock", "vip_reward", "home_interstitial", "pause_interstitial":
	default:
		valid = false
	}
	if raw := v.Get("pageSize"); raw != "" {
		n, e := strconv.Atoi(raw)
		if e != nil || n < 1 || n > 100 {
			valid = false
		} else {
			q.PageSize = n
		}
	}
	if !valid || v.Has("page") {
		rejectInvalidInput(w, r, "ad analytics filters rejected", operations.ErrInvalidInput)
		return
	}
	if raw := v.Get("cursor"); raw != "" {
		b, e := base64.RawURLEncoding.DecodeString(raw)
		var parts []string
		if len(raw) > 1024 || e != nil || json.Unmarshal(b, &parts) != nil || len(parts) != 4 {
			rejectInvalidInput(w, r, "invalid ad analytics cursor", operations.ErrInvalidInput)
			return
		}
		at, e := time.Parse(time.RFC3339Nano, parts[1])
		if e != nil || at.Before(from) || !at.Before(to.AddDate(0, 0, 1)) || at.Nanosecond()%1000 != 0 || parts[0] != operations.AdAnalyticsBinding(application.IDFromContext(r.Context()), q) || !identifier.ValidDatabaseID(parts[2]) || !metricIDPattern.MatchString(parts[3]) {
			rejectInvalidInput(w, r, "ad analytics cursor does not match filters", operations.ErrInvalidInput)
			return
		}
		q.BeforeAt = at
		q.BeforeUser = parts[2]
		q.BeforeID = parts[3]
	}
	result, resultErr := h.service.AdAnalyticsService.ReadAdAnalytics(r.Context(), q)
	writeResult(w, r, http.StatusOK, result, resultErr)
}
