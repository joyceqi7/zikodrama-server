package operations

import (
	"regexp"
	"strconv"
	"strings"

	"drama-server/internal/biz/adminrbac"
)

// rbacCodePattern 保持既有用户名和角色编码格式，统一在 HTTP 入口检查。
var rbacCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{1,127}$`)

// menuRoutePattern 只允许站内绝对路径，禁止协议、查询串、片段和路径穿越字符。
var menuRoutePattern = regexp.MustCompile(`^/[a-z0-9][a-z0-9/_-]{0,198}$`)

// menuIconPattern 使用 Lucide 的小写短横线命名，不接受类名、URL 或 SVG 内容。
var menuIconPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// validateUserCreateInput 规范化登录名和昵称，校验必填项、长度与状态；密码保持原值。
func validateUserCreateInput(input *adminrbac.UserCreateInput) error {
	input.Username = strings.ToLower(strings.TrimSpace(input.Username))
	input.Nickname = strings.TrimSpace(input.Nickname)
	if len(input.Username) < 3 || len(input.Username) > 64 || !rbacCodePattern.MatchString(input.Username) || input.Nickname == "" || !validRBACStatus(input.Status) {
		return adminrbac.ErrInvalidInput
	}
	return validateAdminPassword(input.Password)
}

// validateUserUpdateInput 在读取目标账号前检查路径主键、昵称与启用状态。
func validateUserUpdateInput(id string, input *adminrbac.UserUpdateInput) error {
	if err := validateRBACID(id); err != nil {
		return err
	}
	input.Nickname = strings.TrimSpace(input.Nickname)
	if input.Nickname == "" || !validRBACStatus(input.Status) {
		return adminrbac.ErrInvalidInput
	}
	return nil
}

// validateAdminPassword 保持既有 12～72 字节限制，避免 bcrypt 截断或拒绝超长密码。
func validateAdminPassword(password string) error {
	if len(password) < 12 || len(password) > 72 {
		return adminrbac.ErrInvalidInput
	}
	return nil
}

// validateRoleInput 规范化角色名称、编码、备注与状态，检查必填项及编码格式。
func validateRoleInput(input *adminrbac.RoleInput) error {
	input.Name = strings.TrimSpace(input.Name)
	input.Code = strings.ToLower(strings.TrimSpace(input.Code))
	input.Remark = strings.TrimSpace(input.Remark)
	input.Status = strings.ToLower(strings.TrimSpace(input.Status))
	if input.Name == "" || !rbacCodePattern.MatchString(input.Code) || !validRBACStatus(input.Status) {
		return adminrbac.ErrInvalidInput
	}
	return nil
}

// validateRBACID 校验正数自增主键的规范十进制字符串，保留完整 uint64 精度。
func validateRBACID(id string) error {
	id = strings.TrimSpace(id)
	value, err := strconv.ParseUint(id, 10, 64)
	if err != nil || value == 0 || strconv.FormatUint(value, 10) != id {
		return adminrbac.ErrInvalidInput
	}
	return nil
}

// validateRBACAssignment 校验目标与关联主键的格式；空数组允许清空，存在性由 Service 判断。
func validateRBACAssignment(id string, ids []string) error {
	if err := validateRBACID(id); err != nil {
		return err
	}
	for _, relatedID := range ids {
		if err := validateRBACID(relatedID); err != nil {
			return err
		}
	}
	return nil
}

// validRBACStatus 只接受既有启用和停用状态。
func validRBACStatus(status string) bool {
	return status == adminrbac.StatusActive || status == adminrbac.StatusDisabled
}

// validatePermissionInput 规范化节点资料；页面节点填写站内路径，旧页面标识仅用于滚动升级兼容。
func validatePermissionInput(input *adminrbac.PermissionInput) error {
	input.ParentID = strings.TrimSpace(input.ParentID)
	input.Name = strings.TrimSpace(input.Name)
	input.Code = strings.ToLower(strings.TrimSpace(input.Code))
	input.Type = strings.ToLower(strings.TrimSpace(input.Type))
	input.Route = strings.TrimSpace(input.Route)
	input.Icon = strings.ToLower(strings.TrimSpace(input.Icon))
	input.Status = strings.ToLower(strings.TrimSpace(input.Status))
	input.Remark = strings.TrimSpace(input.Remark)
	if input.Name == "" || len([]rune(input.Name)) > 100 || !rbacCodePattern.MatchString(input.Code) || len([]rune(input.Remark)) > 500 || !validRBACStatus(input.Status) || input.SortOrder < 0 || input.SortOrder > 100000 {
		return adminrbac.ErrInvalidInput
	}
	if input.ParentID != "" {
		if err := validateRBACID(input.ParentID); err != nil {
			return err
		}
	}
	switch input.Type {
	case adminrbac.PermissionDirectory:
		if input.Route != "" {
			return adminrbac.ErrInvalidInput
		}
		if input.Icon != "" && !menuIconPattern.MatchString(input.Icon) {
			return adminrbac.ErrInvalidInput
		}
	case adminrbac.PermissionAction:
		if input.Route != "" || input.Icon != "" {
			return adminrbac.ErrInvalidInput
		}
	case adminrbac.PermissionMenu:
		if !validMenuRoute(input.Route) || (input.Icon != "" && !menuIconPattern.MatchString(input.Icon)) {
			return adminrbac.ErrInvalidInput
		}
	default:
		return adminrbac.ErrInvalidInput
	}
	return nil
}

// validMenuRoute 接受规范站内路径，并兼容旧后台在滚动升级期间提交的页面标识。
func validMenuRoute(route string) bool {
	// 已合并的记录页不能再绑定菜单，审核历史只从官方发布详情进入。
	if route == "/direct/releases" || route == "/dramas/reviews" {
		return false
	}
	if menuRoutePattern.MatchString(route) && !strings.Contains(route, "//") && !strings.HasSuffix(route, "/") {
		return true
	}
	switch route {
	case "reviewSettings", "/whitelist/review-settings", "officialReviewList", "/official/review-list", "directReviewList", "/direct/review-list", "officialPublications", "directCategoryRankings", "directCarousel", "directHotDramas", "releaseRecords", "adConfiguration", "categoryRankings", "tiktokTags", "dashboard", "adAnalytics", "postbackLogs", "consumerUsers", "globalConfigs", "dramas", "monetization", "unlockStrategies", "campaigns", "carousel", "hotDramas", "applications", "users", "roles", "permissions", "audit":
		return true
	default:
		return false
	}
}
