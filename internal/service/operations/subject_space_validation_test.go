package operations

import (
	"testing"

	"drama-server/internal/biz/subject"
)

// TestLegacySubjectSpaceIsIgnored 验证旧后台提交空间不再影响主体保存，发布密钥仍保持原值。
func TestLegacySubjectSpaceIsIgnored(t *testing.T) {
	for _, space := range []string{"", "old-space", "invalid/space"} {
		row, err := normalizeSubjectInput(subject.Input{Name: "主体", VODSpaceName: space, TikTokAppID: "123", ClientKey: "client", ClientSecret: " secret "})
		if err != nil || row.ClientSecret != " secret " {
			t.Fatal("废弃空间影响主体保存或密钥被改变", err)
		}
	}
}

// TestContentSubjectAndApplicationScopes 验证短剧及上传需要主体，应用投放链接仍从应用头解析归属。
func TestContentSubjectAndApplicationScopes(t *testing.T) {
	for _, pattern := range []string{"GET /admin/v1/tiktok-tags", "GET /admin/v1/dramas", "PUT /admin/v1/dramas/{dramaID}", "POST /admin/v1/covers", "POST /admin/v1/episodes/{episodeID}/media-uploads/{uploadID}/proxy", "GET /admin/v1/drama-report", "GET /admin/v1/drama-report/{dramaID}/episodes", "GET /admin/v1/campaign-report/{campaignID}/trend", "GET /admin/v1/reward-analysis"} {
		if !requiresSubjectContext(pattern) {
			t.Fatal("内容接口缺少主体要求", pattern)
		}
	}
	if requiresSubjectContext("POST /admin/v1/dramas/{dramaID}/campaign-link") {
		t.Fatal("应用级投放链接错误要求重复主体头")
	}
	if !requiresApplicationContext("POST /admin/v1/dramas/{dramaID}/campaign-link") {
		t.Fatal("投放链接丢失应用边界")
	}
}
