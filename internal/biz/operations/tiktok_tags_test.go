package operations

import (
	"bytes"
	"context"
	domain "drama-server/internal/biz/consumer"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

type failingTagRepository struct{ err error }

// CountTikTokTagDramas 模拟统计存储结果。
func (r failingTagRepository) CountTikTokTagDramas(context.Context) (map[string]int64, error) {
	return nil, r.err
}

// ListTikTokTags 模拟目录读取故障，不包含表单或凭据。
func (r failingTagRepository) ListTikTokTags(context.Context) ([]domain.TikTokTag, error) {
	return domain.TikTokTagCatalog(), r.err
}

// SaveTikTokTagEnglishName 模拟写入结果，便于核对错误传播与日志。
func (r failingTagRepository) SaveTikTokTagEnglishName(context.Context, string, string) error {
	return r.err
}

// TestTikTokTagFailureLogging 检查读写错误详情、表单保护及成功路径不误报。
func TestTikTokTagFailureLogging(t *testing.T) {
	var output bytes.Buffer
	before := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	defer slog.SetDefault(before)
	cause := errors.New("translation database unavailable")
	service := NewDramaService(ContentPorts{Tags: failingTagRepository{cause}}, MediaPorts{}, nil, nil)
	if _, err := service.ListTikTokTags(context.Background()); !errors.Is(err, cause) {
		t.Fatal("读取错误未传播")
	}
	if _, err := service.UpdateTikTokTag(context.Background(), "21", TikTokTagInput{EnglishName: "Mystery"}); !errors.Is(err, cause) {
		t.Fatal("保存错误未传播")
	}
	for _, detail := range []string{"list TikTok tag translations failed", "save TikTok tag translation failed", cause.Error()} {
		if !strings.Contains(output.String(), detail) {
			t.Fatalf("缺少诊断 %s", detail)
		}
	}
	if strings.Contains(output.String(), "Mystery") {
		t.Fatal("不得记录表单内容")
	}
	output.Reset()
	service = NewDramaService(ContentPorts{Tags: failingTagRepository{}}, MediaPorts{}, nil, nil)
	if _, err := service.UpdateTikTokTag(context.Background(), "21", TikTokTagInput{}); err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 {
		t.Fatal("成功路径误报日志")
	}
}

// failingTagCountRepository 仅在数量统计时失败，确保不会误报为零。
type failingTagCountRepository struct{ failingTagRepository }

// ListTikTokTags 保持目录读取成功以覆盖统计错误分支。
func (r failingTagCountRepository) ListTikTokTags(context.Context) ([]domain.TikTokTag, error) {
	return domain.TikTokTagCatalog(), nil
}

// TestTikTokTagCountFailureLogging 验证统计故障有具体日志并传播，成功的零关联不误报。
func TestTikTokTagCountFailureLogging(t *testing.T) {
	var output bytes.Buffer
	before := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	defer slog.SetDefault(before)
	cause := errors.New("tag count database unavailable")
	service := NewDramaService(ContentPorts{Tags: failingTagCountRepository{failingTagRepository{cause}}}, MediaPorts{}, nil, nil)
	if tags, err := service.ListTikTokTagSummaries(context.Background()); !errors.Is(err, cause) || tags != nil {
		t.Fatal("统计失败不能作为零关联返回")
	}
	for _, detail := range []string{"count TikTok tag dramas failed", cause.Error()} {
		if !strings.Contains(output.String(), detail) {
			t.Fatalf("缺少统计诊断 %s", detail)
		}
	}
	output.Reset()
	service = NewDramaService(ContentPorts{Tags: failingTagRepository{}}, MediaPorts{}, nil, nil)
	tags, err := service.ListTikTokTagSummaries(context.Background())
	if err != nil || len(tags) != 45 || tags[0].DramaCount != 0 || output.Len() != 0 {
		t.Fatal("零关联成功路径异常")
	}
}
