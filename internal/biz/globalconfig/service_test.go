package globalconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

type memoryRepo struct{ rows map[string]Entry }

func (r *memoryRepo) List(context.Context) ([]Entry, error) {
	rows := []Entry{}
	for _, v := range r.rows {
		rows = append(rows, v)
	}
	return rows, nil
}

// TestOrdinaryConfigDeletion 验证播放参数不再占用特殊配置命名空间，普通项可删除，运行依赖的拒绝有日志且成功不误报。
func TestOrdinaryConfigDeletion(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	repo := &memoryRepo{rows: map[string]Entry{}}
	s := NewService(repo)
	if _, err := s.Save(t.Context(), Entry{Key: "playback.default", Value: json.RawMessage(`{"note":"unused"}`)}); err != nil {
		t.Fatal("普通配置被专用播放校验拒绝", err)
	}
	if err := s.Delete(t.Context(), "playback.default"); err != nil || len(repo.rows) != 0 || logs.Len() != 0 {
		t.Fatal("普通配置删除失败或成功误报", err)
	}
	for _, key := range []string{"media", "distribution", "invalid key"} {
		logs.Reset()
		if err := s.Delete(t.Context(), key); !errors.Is(err, ErrInvalid) {
			t.Fatal("无效删除未拒绝", err)
		}
		if !strings.Contains(logs.String(), "global configuration deletion rejected") || !strings.Contains(logs.String(), ErrInvalid.Error()) || !strings.Contains(logs.String(), `"level":"WARN"`) {
			t.Fatal("预期拒绝缺少正确日志")
		}
	}
}
func (r *memoryRepo) Get(_ context.Context, k string) (Entry, error) {
	v, ok := r.rows[k]
	if !ok {
		return Entry{}, ErrNotFound
	}
	return v, nil
}
func (r *memoryRepo) Put(_ context.Context, v Entry) error     { r.rows[v.Key] = v; return nil }
func (r *memoryRepo) Delete(_ context.Context, k string) error { delete(r.rows, k); return nil }

const mediaJSON = `{"access_key_id":"secret-id","secret_access_key":"secret-value","vod_app_id":"001","region":"ap-southeast-1","max_file_bytes":1073741824,"upload_token_ttl_seconds":3600,"cover_domain":"https://cover.example.test","processing_timeout_seconds":900,"callback_url":"","callback_private_key":""}`

func TestMediaSecretsStayServerSideAndSurviveMaskedEdit(t *testing.T) {
	repo := &memoryRepo{rows: map[string]Entry{}}
	s := NewService(repo)
	ctx := context.Background()
	saved, err := s.Save(ctx, Entry{Key: "media", Value: json.RawMessage(mediaJSON)})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved.Value), "secret-value") || strings.Contains(string(saved.Value), "secret-id") {
		t.Fatal("secret leaked")
	}
	if _, err = s.Save(ctx, saved); err != nil {
		t.Fatal(err)
	}
	runtime, err := s.Media(ctx)
	if err != nil || runtime.SecretAccessKey != "secret-value" || runtime.VODAppID != "001" {
		t.Fatal("masked edit lost credentials or string ID")
	}
	rows, err := s.List(ctx)
	if err != nil || strings.Contains(string(rows[0].Value), "secret-value") {
		t.Fatal("list leaked secret")
	}
	if !errors.Is(s.Delete(ctx, "media"), ErrInvalid) {
		t.Fatal("runtime configuration must not be deleted")
	}
}

// TestRetainedDistributionSecretIsMasked 验证保留旧配置记录后，读取仅返回脱敏副本且不改写原值。
func TestRetainedDistributionSecretIsMasked(t *testing.T) {
	original := json.RawMessage(`{"client_key":"historical-publisher","client_secret":"historical-secret"}`)
	repo := &memoryRepo{rows: map[string]Entry{"distribution": {Key: "distribution", Value: original}}}
	rows, err := NewService(repo).List(t.Context())
	if err != nil || len(rows) != 1 {
		t.Fatal("历史配置读取失败", err)
	}
	if strings.Contains(string(rows[0].Value), "historical-secret") || !strings.Contains(string(rows[0].Value), mask) {
		t.Fatal("保留的历史发布方配置泄漏密钥")
	}
	if string(repo.rows["distribution"].Value) != string(original) {
		t.Fatal("读取脱敏改写了原数据库配置")
	}
}
func TestRejectMalformedMediaWithoutOverwriting(t *testing.T) {
	repo := &memoryRepo{rows: map[string]Entry{}}
	s := NewService(repo)
	ctx := context.Background()
	for _, value := range []string{`[]`, `null`, `{"secret_access_key":"********"}`, strings.Replace(mediaJSON, `"region":"ap-southeast-1"`, `"region":"invalid"`, 1), strings.Replace(mediaJSON, `"region":"ap-southeast-1"`, `"unknown":"test"`, 1)} {
		if _, err := s.Save(ctx, Entry{Key: "media", Value: json.RawMessage(value)}); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid media accepted")
		}
	}
	if len(repo.rows) != 0 {
		t.Fatal("invalid values persisted")
	}
}

// TestSharedMediaSpaceSurvivesAdminSave 验证共享空间进入运行配置，旧版表单省略时保留原值。
func TestSharedMediaSpaceSurvivesAdminSave(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	legacy := strings.Replace(mediaJSON, `{`, `{"space_name":"legacy-space",`, 1)
	repo := &memoryRepo{rows: map[string]Entry{"media": {Key: "media", Value: json.RawMessage(legacy)}}}
	s := NewService(repo)
	config, err := s.Media(t.Context())
	if err != nil || config.SpaceName != "legacy-space" || config.SecretAccessKey != "secret-value" {
		t.Fatal("共享空间没有进入运行配置", err)
	}
	if _, err := s.Save(t.Context(), Entry{Key: "media", Value: json.RawMessage(mediaJSON)}); err != nil {
		t.Fatal("新版表单不能保存含历史空间的配置", err)
	}
	var retained map[string]json.RawMessage
	if err := json.Unmarshal(repo.rows["media"].Value, &retained); err != nil || string(retained["space_name"]) != `"legacy-space"` {
		t.Fatal("新版保存删除或改写旧空间", err)
	}
	rows, err := s.List(t.Context())
	if err != nil || !strings.Contains(string(rows[0].Value), `"space_name":"legacy-space"`) || strings.Contains(string(rows[0].Value), "secret-value") {
		t.Fatal("历史字段或密钥脱敏合同错误", err)
	}
	if _, err := s.Save(t.Context(), rows[0]); err != nil {
		t.Fatal("带历史字段的脱敏配置不能保存", err)
	}
	if logs.Len() != 0 {
		t.Fatal("配置兼容成功路径误报日志")
	}
	spaced := strings.Replace(mediaJSON, `{`, `{"space_name":"  shared-space  ",`, 1)
	if _, err := s.Save(t.Context(), Entry{Key: "media", Value: json.RawMessage(spaced)}); err != nil {
		t.Fatal(err)
	}
	config, err = s.Media(t.Context())
	if err != nil || config.SpaceName != "shared-space" {
		t.Fatal("共享空间未规范化", err)
	}
	before := string(repo.rows["media"].Value)
	for _, extra := range []string{`"space_name":123`, `"space_name":null`, `"space_name":""`, `"space_name":"  "`, `"space_name":"invalid/space"`, `"space_name":"Uppercase"`, `"unexpected_space":"legacy-space"`} {
		logs.Reset()
		value := strings.Replace(mediaJSON, `{`, `{`+extra+`,`, 1)
		if _, err := s.Save(t.Context(), Entry{Key: "media", Value: json.RawMessage(value)}); !errors.Is(err, ErrInvalid) {
			t.Fatal("历史字段兼容放宽了格式或未知字段校验", err)
		}
		if string(repo.rows["media"].Value) != before || !strings.Contains(logs.String(), "media configuration rejected") || !strings.Contains(logs.String(), ErrInvalid.Error()) || strings.Contains(logs.String(), "secret-value") {
			t.Fatal("拒绝覆盖或诊断日志不符合合同")
		}
	}
}
