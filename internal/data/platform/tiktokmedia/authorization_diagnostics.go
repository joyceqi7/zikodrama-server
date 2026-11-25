package tiktokmedia

import (
	"bytes"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

// responseShapeField 只描述协议路径和类型，数组仅记录长度及首项结构。
type responseShapeField struct {
	// Path 是经过格式限制和凭据脱敏的字段路径。
	Path string `json:"path"`
	// Kind 是 JSON 类型，不包含标量值。
	Kind string `json:"kind"`
	// Length 仅在数组上记录元素数，空数组也明确返回零。
	Length *int `json:"length,omitempty"`
}

var protocolFieldName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)
var sensitiveShapeField = regexp.MustCompile(`(?i)(token|secret|password|cookie|authorization|signature|credential|account|url|client_?key)`)

// responseShape 为授权异常提取有界结构；不输出标量、任意映射键、敏感子对象或完整响应。
// 最多 32 个节点、四层对象，数组只查看首项，防止响应内容扩大日志或泄漏业务值。
func (d *platformCallDiagnostic) responseShape(raw []byte) []responseShapeField {
	result := make([]responseShapeField, 0, 16)
	var visit func(json.RawMessage, string, int)
	visit = func(raw json.RawMessage, path string, depth int) {
		if len(result) >= 32 || depth > 4 {
			return
		}
		raw = bytes.TrimSpace(raw)
		kind := "invalid"
		var length *int
		var object map[string]json.RawMessage
		var array []json.RawMessage
		if len(raw) > 0 {
			switch raw[0] {
			case '{':
				kind = "object"
				_ = json.Unmarshal(raw, &object)
			case '[':
				kind = "array"
				if json.Unmarshal(raw, &array) == nil {
					n := len(array)
					length = &n
				}
			case '"':
				kind = "string"
			case 'n':
				kind = "null"
			case 't', 'f':
				kind = "boolean"
			default:
				kind = "number"
			}
		}
		result = append(result, responseShapeField{Path: path, Kind: kind, Length: length})
		keys := make([]string, 0, len(object))
		for key := range object {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			// 不记录可能夹带凭据、URL 或长业务标识的键；敏感字段只展示类型，不遍历其值。
			if !protocolFieldName.MatchString(key) || d.clean(key) != key {
				continue
			}
			next := path + "." + key
			// 结果容器属于协议字段，允许检查结构；其中 client_key 的值仍遮蔽。
			resultContainer := key == "client_key_result" || key == "client_key_result_list"
			if sensitiveShapeField.MatchString(key) && !resultContainer {
				if len(result) < 32 {
					result = append(result, responseShapeField{Path: next, Kind: "redacted"})
				}
				continue
			}
			// 动态映射不是协议结构；不遍历用用户或应用标识作为键的 map。
			if strings.HasSuffix(strings.ToLower(key), "_map") {
				continue
			}
			visit(object[key], next, depth+1)
		}
		if len(array) > 0 {
			visit(array[0], path+"[]", depth+1)
		}
	}
	visit(raw, "$", 0)
	return result
}
