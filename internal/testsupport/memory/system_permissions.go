package memory

import (
	"drama-server/internal/biz/adminrbac"
	"strconv"
)

// systemPermissionFixture 模拟最终 SQL 的默认权限树，业务运行时不会自动重置自定义配置。
func systemPermissionFixture() map[string]adminrbac.Permission {
	items := []struct {
		name, code, kind, parent, route string
		order                           int
	}{
		{"内容管理", "directory.content", "directory", "", "", 0},
		{"应用管理", "directory.applications", "directory", "", "", 10},
		{"变现管理", "directory.commercialization", "directory", "", "", 20},
		{"运营数据", "directory.operations", "directory", "", "", 30},
		{"系统管理", "directory.system", "directory", "", "", 40},
		{"短剧管理", "menu.dramas", "menu", "directory.content", "/dramas", 0},
		{"TikTok 标签", "menu.tiktoktags", "menu", "directory.content", "/content/tiktok-tags", 10},
		{"维护 TikTok 标签英文翻译", "content.tiktok_tag.update", "action", "menu.tiktoktags", "", 10},
		{"主体管理", "menu.subjects", "menu", "directory.applications", "/channels/subjects", 19},
		{"查看主体", "subjects.read", "action", "menu.subjects", "", 0},
		{"创建主体", "subjects.create", "action", "menu.subjects", "", 1},
		{"编辑主体", "subjects.update", "action", "menu.subjects", "", 2},
		{"TikTok 应用", "menu.applications", "menu", "directory.applications", "/channels/applications", 20},
		{"解锁策略", "menu.unlockstrategies", "menu", "directory.commercialization", "/monetization/unlock-strategy", 30},
		{"投放配置", "menu.campaigns", "menu", "directory.operations", "/channels/campaigns", 60},
		{"配置管理", "menu.globalconfigs", "menu", "directory.system", "/system/configuration", 70},
		{"用户", "menu.users", "menu", "directory.system", "/system/users", 80},
		{"角色", "menu.roles", "menu", "directory.system", "/system/roles", 90},
		{"权限", "menu.permissions", "menu", "directory.system", "/system/permissions", 100},
		{"审计日志", "menu.audit", "menu", "directory.system", "/system/audit-log", 110},
		{"查看全局配置", "system.config.read", "action", "menu.globalconfigs", "", 0},
		{"管理全局配置", "system.config.update", "action", "menu.globalconfigs", "", 10},
		{"查看剧目", "content.drama.read", "action", "menu.dramas", "", 20},
		{"创建剧目", "content.drama.create", "action", "menu.dramas", "", 30},
		{"编辑剧目", "content.drama.update", "action", "menu.dramas", "", 40},
		{"删除剧目", "content.drama.delete", "action", "menu.dramas", "", 50},
		{"上传媒资", "content.media.upload", "action", "menu.dramas", "", 60},
		{"提交、重试与下架 TikTok 剧目", "content.drama.publish", "action", "menu.dramas", "", 70},
		{"同步短剧到 TikTok", "content.drama.sync", "action", "menu.dramas", "", 71},
		{"管理 TikTok 审核", "content.drama.review", "action", "menu.dramas", "", 72},
		{"上线和下架 TikTok 短剧", "content.drama.release", "action", "menu.dramas", "", 73},
		{"配置短剧应用分发", "content.drama.distribute", "action", "menu.dramas", "", 74},
		{"查看 TikTok 标签", "content.tiktok_tag.read", "action", "menu.tiktoktags", "", 80},
		{"查看 TikTok 应用", "applications.read", "action", "menu.applications", "", 90},
		{"创建 TikTok 应用", "applications.create", "action", "menu.applications", "", 100},
		{"编辑 TikTok 应用", "applications.update", "action", "menu.applications", "", 110},
		{"查看投放活动", "operations.campaign.read", "action", "menu.campaigns", "", 130},
		{"创建投放活动", "operations.campaign.create", "action", "menu.campaigns", "", 140},
		{"编辑投放活动", "operations.campaign.update", "action", "menu.campaigns", "", 150},
		{"查看用户", "rbac.user.read", "action", "menu.users", "", 160},
		{"创建用户", "rbac.user.create", "action", "menu.users", "", 170},
		{"编辑用户", "rbac.user.update", "action", "menu.users", "", 180},
		{"重置用户密码", "rbac.user.reset_password", "action", "menu.users", "", 190},
		{"分配用户角色", "rbac.user.assign_role", "action", "menu.users", "", 200},
		{"查看角色", "rbac.role.read", "action", "menu.roles", "", 210},
		{"创建角色", "rbac.role.create", "action", "menu.roles", "", 220},
		{"编辑角色", "rbac.role.update", "action", "menu.roles", "", 230},
		{"删除角色", "rbac.role.delete", "action", "menu.roles", "", 240},
		{"分配角色权限", "rbac.role.assign_permission", "action", "menu.roles", "", 250},
		{"查看权限", "rbac.permission.read", "action", "menu.permissions", "", 260},
		{"查看审计日志", "audit.read", "action", "menu.audit", "", 270},
		{"新增权限", "rbac.permission.create", "action", "menu.permissions", "", 280},
		{"编辑权限", "rbac.permission.update", "action", "menu.permissions", "", 290},
		{"删除权限", "rbac.permission.delete", "action", "menu.permissions", "", 300},
		{"分类排行榜", "menu.categoryrankings", "menu", "directory.content", "/content/category-rankings", 30},
		{"查看分类排行榜", "operations.ranking.read", "action", "menu.categoryrankings", "", 0},
		{"配置分类排行榜", "operations.ranking.update", "action", "menu.categoryrankings", "", 10},
		{"热播短剧", "menu.hot", "menu", "directory.content", "/content/hot-dramas", 20},
		{"查看热播", "operations.hot.read", "action", "menu.hot", "", 0},
		{"配置热播", "operations.hot.update", "action", "menu.hot", "", 10},
		{"轮播管理", "menu.carousel", "menu", "directory.content", "/content/carousel", 10},
		{"查看轮播", "operations.carousel.read", "action", "menu.carousel", "", 0},
		{"配置轮播", "operations.carousel.update", "action", "menu.carousel", "", 10},
		{"仪表盘", "menu.dashboard", "menu", "directory.operations", "/channels/dashboard", 0},
		{"查看仪表盘", "operations.dashboard.read", "action", "menu.dashboard", "", 0},
		{"回传日志", "menu.postbacklogs", "menu", "directory.operations", "/channels/postback-logs", 10},
		{"查看回传日志", "operations.postback_log.read", "action", "menu.postbacklogs", "", 0},
		{"用户管理", "directory.consumerusers", "directory", "", "", 35},
		{"用户列表", "menu.consumerusers", "menu", "directory.consumerusers", "/channels/users", 0},
		{"查看观众用户", "operations.consumer_user.read", "action", "menu.consumerusers", "", 0},
		{"广告配置", "menu.adconfiguration", "menu", "directory.commercialization", "/channels/ad-configuration", 20},
		{"剧目播放与解锁", "menu.drama_report", "menu", "directory.operations", "/channels/drama-report", 20},
		{"查看剧目播放与解锁", "operations.drama_report.read", "action", "menu.drama_report", "", 0},
		{"投放链接奖励贡献", "menu.campaign_report", "menu", "directory.operations", "/channels/campaign-report", 30},
		{"查看投放链接奖励贡献", "operations.campaign_report.read", "action", "menu.campaign_report", "", 0},
	}
	result := make(map[string]adminrbac.Permission, len(items))
	ids := make(map[string]string, len(items))
	for i, item := range items {
		id := strconv.Itoa(i + 1)
		ids[item.code] = id
		result[id] = adminrbac.Permission{ID: id, Name: item.name, Code: item.code, Type: item.kind, ParentID: ids[item.parent], Route: item.route, SortOrder: item.order, Status: adminrbac.StatusActive}
	}
	return result
}
