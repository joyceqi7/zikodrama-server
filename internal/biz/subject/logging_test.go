package subject_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/subject"
	"drama-server/internal/testsupport/memory"
)

type failingSubjectRepository struct {
	subject.Repository
	find, list, update bool
}

func (r failingSubjectRepository) FindSubject(ctx context.Context, id string) (subject.Subject, error) {
	if r.find {
		return subject.Subject{}, errors.New("subject storage read unavailable")
	}
	return r.Repository.FindSubject(ctx, id)
}
func (r failingSubjectRepository) ListSubjects(ctx context.Context) ([]subject.Subject, error) {
	if r.list {
		return nil, errors.New("subject storage list unavailable")
	}
	return r.Repository.ListSubjects(ctx)
}
func (r failingSubjectRepository) UpdateSubject(ctx context.Context, row subject.Subject) error {
	if r.update {
		return errors.New("subject storage update unavailable")
	}
	return r.Repository.UpdateSubject(ctx, row)
}

// TestSubjectFailureLogging 验证依赖故障记录实际错误和操作，配置与身份拒绝按预期业务日志处理。
func TestSubjectFailureLogging(t *testing.T) {
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	for _, test := range []struct {
		name, operation, detail string
		repo                    failingSubjectRepository
		call                    func(*subject.Service) error
	}{
		{"列表故障", "list subjects failed", "subject storage list unavailable", failingSubjectRepository{list: true}, func(s *subject.Service) error { _, e := s.List(t.Context()); return e }},
		{"更新查询故障", "find subject for update failed", "subject storage read unavailable", failingSubjectRepository{find: true}, func(s *subject.Service) error {
			_, e := s.Save(t.Context(), "1", subject.Input{Name: "修改", ClientSecret: "never-log-this-secret"})
			return e
		}},
		{"更新写入故障", "save subject failed", "subject storage update unavailable", failingSubjectRepository{update: true}, func(s *subject.Service) error {
			_, e := s.Save(t.Context(), "1", subject.Input{Name: "修改", TikTokAppID: "123", ClientKey: "publisher", ClientSecret: "never-log-this-secret"})
			return e
		}},
		{"发布查询故障", "resolve subject publisher failed", "subject storage read unavailable", failingSubjectRepository{find: true}, func(s *subject.Service) error { _, e := s.OwnerID(application.WithSubject(t.Context(), "1")); return e }},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.repo.Repository = memory.NewStore(memory.Seed{})
			var output bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
			if err := test.call(subject.NewService(test.repo)); err == nil {
				t.Fatal("未传播仓储故障")
			}
			if !strings.Contains(output.String(), test.operation) || !strings.Contains(output.String(), test.detail) || !strings.Contains(output.String(), `"level":"ERROR"`) {
				t.Fatal("故障日志缺少操作或实际错误")
			}
			if strings.Contains(output.String(), "never-log-this-secret") {
				t.Fatal("故障日志泄露密钥")
			}
		})
	}
	var output bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	s := subject.NewService(memory.NewStore(memory.Seed{}))
	if _, err := s.Save(t.Context(), "", subject.Input{Name: "直连主体"}); err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 {
		t.Fatal("成功保存主体误报日志")
	}
	if _, err := s.OwnerID(application.WithSubject(t.Context(), "1")); !errors.Is(err, subject.ErrNotConfigured) {
		t.Fatal("未配置发布方未拒绝")
	}
	if !strings.Contains(output.String(), `"level":"WARN"`) || strings.Contains(output.String(), `"level":"ERROR"`) {
		t.Fatal("预期发布拒绝被误报为系统故障")
	}
}
