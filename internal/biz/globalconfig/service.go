// Package globalconfig 管理跨应用的 JSON 配置，媒资密钥只在服务端读取和使用。
package globalconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"regexp"
	"time"

	"drama-server/internal/biz/mediaconfig"
)

// ErrInvalid 表示配置键、JSON 或媒资字段不合法。
var ErrInvalid = errors.New("invalid global configuration")

// ErrNotFound 表示配置尚未保存。
var ErrNotFound = errors.New("global configuration not found")

// Entry 是后台可编辑的一条配置；媒资敏感字段返回掩码。
type Entry struct {
	// Key 是配置的稳定唯一标识。
	Key string `json:"key"`
	// Value 保存 JSON 对象，media 使用媒资配置合同。
	Value json.RawMessage `json:"value"`
	// Remark 是运营填写的配置用途说明。
	Remark string `json:"remark"`
	// CreatedAt 是首次记录的创建时间；历史配置没有该值时不伪造历史时间。
	CreatedAt *time.Time `json:"createdAt,omitempty"`
	// UpdatedAt 是最近一次保存时间。
	UpdatedAt *time.Time `json:"updatedAt,omitempty"`
	// UpdatedBy 来自已认证管理员，不接受请求体中的操作人。
	UpdatedBy string `json:"updatedBy,omitempty"`
}

// Repository 是全局配置的持久化边界。
type Repository interface {
	// List 返回全部已保存配置，仅限服务端内部调用。
	List(context.Context) ([]Entry, error)
	// Get 返回一个配置的原始值。
	Get(context.Context, string) (Entry, error)
	// Put 按唯一键保存配置。
	Put(context.Context, Entry) error
	// Delete 删除一个配置。
	Delete(context.Context, string) error
}

// Service 负责 JSON 校验、密钥保留和后台脱敏。
type Service struct {
	repo Repository
}

// NewService 创建全局配置管理用例。
func NewService(repo Repository) *Service { return &Service{repo: repo} }

var keyPattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)

const mask = "********"

var secrets = []string{"access_key_id", "secret_access_key", "callback_private_key"}

// ValidateInput 在 HTTP 入口校验配置键和 JSON 对象，限制单条大小。
func ValidateInput(key string, value json.RawMessage) error {
	if key == "distribution" || !keyPattern.MatchString(key) || len(value) > 65536 {
		return ErrInvalid
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(value, &object) != nil || object == nil {
		return ErrInvalid
	}
	return nil
}

// List 返回脱敏后的配置列表。
func (s *Service) List(ctx context.Context) ([]Entry, error) {
	rows, err := s.repo.List(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "list global configurations failed", "error", err)
		return nil, err
	}
	for i := range rows {
		rows[i] = redact(rows[i])
	}
	return rows, nil
}

// Media 读取本次操作需要的完整媒资配置；不把密钥传入 HTTP 响应。
func (s *Service) Media(ctx context.Context) (*mediaconfig.Config, error) {
	row, err := s.repo.Get(ctx, "media")
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		slog.ErrorContext(ctx, "load global media configuration failed", "error", err)
		return nil, err
	}
	media, err := parseMedia(row.Value)
	if err != nil {
		slog.ErrorContext(ctx, "validate stored media configuration failed", "error", err)
	}
	return media, err
}

// Save 校验媒资配置，保留界面没有替换的掩码密钥及省略的共享空间配置，避免旧版表单清空当前上传空间。
func (s *Service) Save(ctx context.Context, row Entry) (Entry, error) {
	if err := ValidateInput(row.Key, row.Value); err != nil {
		slog.WarnContext(ctx, "global configuration rejected", "error", err)
		return Entry{}, err
	}
	if len([]rune(row.Remark)) > 500 {
		slog.WarnContext(ctx, "global configuration remark rejected", "error", ErrInvalid)
		return Entry{}, ErrInvalid
	}

	if row.Key == "media" {
		var values map[string]json.RawMessage
		_ = json.Unmarshal(row.Value, &values)
		old, err := s.repo.Get(ctx, row.Key)
		if err != nil && !errors.Is(err, ErrNotFound) {
			slog.ErrorContext(ctx, "read previous media configuration failed", "error", err)
			return Entry{}, err
		}
		var previous map[string]json.RawMessage
		_ = json.Unmarshal(old.Value, &previous)
		// 旧版表单可能省略空间，保留已配置的共享空间，避免保存凭据时清空上传目标。
		if _, present := values["space_name"]; !present && previous["space_name"] != nil {
			values["space_name"] = previous["space_name"]
		}
		for _, key := range secrets {
			var value string
			raw, present := values[key]
			_ = json.Unmarshal(raw, &value)
			if !present || value == mask {
				if previous[key] != nil {
					values[key] = previous[key]
				} else {
					delete(values, key)
				}
			}
		}
		row.Value, _ = json.Marshal(values)
		if _, err = parseMedia(row.Value); err != nil {
			slog.WarnContext(ctx, "media configuration rejected", "error", err)
			return Entry{}, err
		}
	}
	if err := s.repo.Put(ctx, row); err != nil {
		slog.ErrorContext(ctx, "save global configuration failed", "error", err)
		return Entry{}, err
	}
	// 仓储原子保留创建时间，重新读取持久化结果用于返回操作记录。
	saved, err := s.repo.Get(ctx, row.Key)
	if err != nil {
		slog.ErrorContext(ctx, "read saved global configuration failed", "error", err)
		return Entry{}, err
	}
	return redact(saved), nil
}

// Delete 仅删除普通配置；媒资与分发配置是运行依赖，只允许有效编辑。
func (s *Service) Delete(ctx context.Context, key string) error {
	if !keyPattern.MatchString(key) || key == "media" || key == "distribution" {
		slog.WarnContext(ctx, "global configuration deletion rejected", "error", ErrInvalid)
		return ErrInvalid
	}
	if err := s.repo.Delete(ctx, key); err != nil {
		slog.ErrorContext(ctx, "delete global configuration failed", "error", err)
		return err
	}
	return nil
}
func parseMedia(raw json.RawMessage) (*mediaconfig.Config, error) {
	var stored struct {
		// Config 保存全局共享的 VOD 配置。
		mediaconfig.Config
		// SharedSpaceName 保留字段存在性，拒绝显式 null 或空值，同时兼容尚未配置空间的旧记录。
		SharedSpaceName json.RawMessage `json:"space_name"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&stored) != nil || decoder.Decode(new(any)) != io.EOF {
		return nil, ErrInvalid
	}
	if len(stored.SharedSpaceName) > 0 {
		var space string
		if json.Unmarshal(stored.SharedSpaceName, &space) != nil || len(bytes.TrimSpace([]byte(space))) == 0 {
			return nil, ErrInvalid
		}
		stored.Config.SpaceName = string(bytes.TrimSpace([]byte(space)))
	}
	if stored.Config.Validate() != nil {
		return nil, ErrInvalid
	}
	return &stored.Config, nil
}
func redact(row Entry) Entry {
	if row.Key != "media" && row.Key != "distribution" {
		return row
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(row.Value, &values) != nil {
		return Entry{Key: row.Key, Value: json.RawMessage(`{}`)}
	}
	secretFields := secrets
	// 主体迁移保留旧发布方记录，旧密钥同样不得在配置列表中回显。
	if row.Key == "distribution" {
		secretFields = []string{"client_secret"}
	}
	for _, key := range secretFields {
		var value string
		_ = json.Unmarshal(values[key], &value)
		if value != "" {
			values[key], _ = json.Marshal(mask)
		}
	}
	row.Value, _ = json.Marshal(values)
	return row
}
