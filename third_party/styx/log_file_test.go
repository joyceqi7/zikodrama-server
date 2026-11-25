package styx

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
)

func TestRotatingFileWriterCutsAtUTCDayAndRetainsFiles(t *testing.T) {
	directory := t.TempDir()
	now := time.Date(2026, 9, 20, 23, 59, 0, 0, time.UTC)
	writer, err := newRotatingFileWriter(LogFileConfig{
		Directory: directory, Prefix: "server", Rotation: LogRotationDay, RetentionDays: 2,
	}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	for _, step := range []struct {
		at      time.Time
		message string
	}{
		{now, "first\n"},
		{time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC), "second\n"},
		{time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC), "third\n"},
	} {
		now = step.at
		if _, err := writer.Write([]byte(step.message)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(directory, "server-2026-09-20.jsonl")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("超期日志仍存在：%v", err)
	}
	for name, want := range map[string]string{
		"server-2026-09-21.jsonl": "second\n",
		"server-2026-09-23.jsonl": "third\n",
	} {
		data, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil || string(data) != want {
			t.Fatalf("文件 %s 内容=%q，错误=%v", name, data, err)
		}
	}
}

func TestRotatingFileWriterPreservesConcurrentRecords(t *testing.T) {
	directory := t.TempDir()
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	writer, err := newRotatingFileWriter(LogFileConfig{Directory: directory, Prefix: "server"}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for range 16 {
		group.Add(1)
		go func() {
			defer group.Done()
			for range 32 {
				if _, err := writer.Write([]byte("complete-record\n")); err != nil {
					t.Errorf("并发写入失败：%v", err)
					return
				}
			}
		}()
	}
	group.Wait()
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "server-2026-09-23.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := bytes.Count(data, []byte("complete-record\n")); got != 16*32 || len(data) != 16*32*len("complete-record\n") {
		t.Fatalf("并发记录不完整：条数=%d，字节数=%d", got, len(data))
	}
}

func TestRotatingFileWriterCutsAtUTCHourAndKeepsOtherFiles(t *testing.T) {
	directory := t.TempDir()
	otherFile := filepath.Join(directory, "other-2026-09-20-23.jsonl")
	if err := os.WriteFile(otherFile, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 20, 23, 59, 0, 0, time.UTC)
	writer, err := newRotatingFileWriter(LogFileConfig{
		Directory: directory, Prefix: "server", Rotation: LogRotationHour, RetentionDays: 14,
	}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	if _, err := writer.Write([]byte("before\n")); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if _, err := writer.Write([]byte("after\n")); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"server-2026-09-20-23.jsonl": "before\n",
		"server-2026-09-21-00.jsonl": "after\n",
	} {
		data, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil || string(data) != want {
			t.Fatalf("文件 %s 内容=%q，错误=%v", name, data, err)
		}
	}
	if data, err := os.ReadFile(otherFile); err != nil || string(data) != "keep" {
		t.Fatalf("无关文件被改动：%q，错误=%v", data, err)
	}
}

func TestRotatingFileWriterRejectsUnsafeOptions(t *testing.T) {
	for _, config := range []LogFileConfig{
		{Directory: t.TempDir(), Prefix: "../server"},
		{Directory: t.TempDir(), Prefix: "server", Rotation: "weekly"},
		{Directory: t.TempDir(), Prefix: "server", RetentionDays: -1},
	} {
		if writer, err := NewRotatingFileWriter(config); err == nil {
			_ = writer.Close()
			t.Fatalf("无效配置被接受：%+v", config)
		}
	}
}

func TestNewWritesRuntimeLogsToFileAndClosesIt(t *testing.T) {
	directory := t.TempDir()
	runtime, err := New(context.Background(), Config{
		App: &configv1.AppInfo{Name: "file-log-test"},
		OTel: &configv1.OTelConfig{Log: &configv1.LogConfig{File: &configv1.LogFileConfig{
			Directory: directory, Prefix: "server", Rotation: string(LogRotationDay), RetentionDays: 14,
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime.Dependencies().Logger().Error("file log integration test", "error", errors.New("expected failure"))
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "server-"+time.Now().UTC().Format("2006-01-02")+".jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"msg":"file log integration test"`) || !strings.Contains(string(data), `"error":"expected failure"`) {
		t.Fatalf("文件未保留错误详情：%s", data)
	}
}

// TestNewWritesRelativeFileLogs 验证 Runtime 直接使用相对目录配置写入文件。
func TestNewWritesRelativeFileLogs(t *testing.T) {
	workingDirectory := t.TempDir()
	t.Chdir(workingDirectory)
	runtime, err := New(context.Background(), Config{
		App: &configv1.AppInfo{Name: "relative-file-log-test"},
		OTel: &configv1.OTelConfig{Log: &configv1.LogConfig{File: &configv1.LogFileConfig{
			Directory: "./logs", Prefix: "server", Rotation: string(LogRotationDay), RetentionDays: 3,
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime.Dependencies().Logger().Info("relative file log integration test")
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(workingDirectory, "logs", "server-"+time.Now().UTC().Format("2006-01-02")+".jsonl")
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), `"msg":"relative file log integration test"`) {
		t.Fatalf("相对目录日志未写入：路径=%s，内容=%s，错误=%v", path, data, err)
	}
}

func TestParseLogFileConfigOwnsSchemaValidation(t *testing.T) {
	if parsed, err := ParseLogFileConfig(nil); err != nil || parsed != nil {
		t.Fatalf("未配置文件日志时应保持关闭：配置=%+v，错误=%v", parsed, err)
	}
	for _, file := range []*configv1.LogFileConfig{
		{Directory: "", Prefix: "server", Rotation: "day", RetentionDays: 14},
		{Directory: "./logs\nother", Prefix: "server", Rotation: "day", RetentionDays: 14},
		{Directory: "/tmp/..", Prefix: "server", Rotation: "day", RetentionDays: 14},
		{Directory: "/tmp/logs", Prefix: "../server", Rotation: "day", RetentionDays: 14},
		{Directory: "/tmp/logs", Prefix: "server", Rotation: "week", RetentionDays: 14},
		{Directory: "/tmp/logs", Prefix: "server", Rotation: "day", RetentionDays: 0},
	} {
		if _, err := ParseLogFileConfig(file); err == nil {
			t.Fatalf("非法文件日志配置未被拒绝：%+v", file)
		}
	}
	parsed, err := ParseLogFileConfig(&configv1.LogFileConfig{
		Directory: "/tmp/logs", Prefix: "server", Rotation: "hour", RetentionDays: 7,
	})
	if err != nil || parsed.Directory != "/tmp/logs" || parsed.Prefix != "server" || parsed.Rotation != LogRotationHour || parsed.RetentionDays != 7 {
		t.Fatalf("Styx 配置解析错误：配置=%+v，错误=%v", parsed, err)
	}
}

// TestParseLogFileConfigResolvesRelativeDirectory 验证相对目录以进程工作目录为基准且不改写原配置。
func TestParseLogFileConfigResolvesRelativeDirectory(t *testing.T) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	source := &configv1.LogFileConfig{
		Directory: "./logs", Prefix: "server", Rotation: "day", RetentionDays: 3,
	}
	parsed, err := ParseLogFileConfig(source)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Directory != filepath.Join(workingDirectory, "logs") || source.Directory != "./logs" {
		t.Fatalf("相对目录解析错误：结果=%q，原配置=%q", parsed.Directory, source.Directory)
	}
}
