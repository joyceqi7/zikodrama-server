package styx

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
)

// LogRotation 指定日志文件按 UTC 日期或小时切割。
type LogRotation string

const (
	// LogRotationDay 每个 UTC 日期使用一个日志文件。
	LogRotationDay LogRotation = "day"
	// LogRotationHour 每个 UTC 小时使用一个日志文件。
	LogRotationHour LogRotation = "hour"
)

// LogFileConfig 配置 JSON 行日志的持久化目录、切割粒度和保留时间。
type LogFileConfig struct {
	// Directory 是日志文件所在目录；相对路径以进程工作目录为基准，进程必须拥有写入权限。
	Directory string
	// Prefix 是安全文件名前缀，只允许字母、数字、连字符和下划线。
	Prefix string
	// Rotation 是 day 或 hour；空值默认按日切割。
	Rotation LogRotation
	// RetentionDays 是文件保留天数；零值默认 14 天，负值无效。
	RetentionDays int
}

// ParseLogFileConfig 将 Styx 的 protobuf 配置校验并转换为文件 writer 使用的配置。
// nil 表示未启用文件输出；相对目录以进程工作目录为基准，结果始终使用绝对路径。
// 配置存在时四个字段都必须明确有效。
func ParseLogFileConfig(source *configv1.LogFileConfig) (*LogFileConfig, error) {
	if source == nil {
		return nil, nil
	}
	directory := strings.TrimSpace(source.GetDirectory())
	if directory == "" {
		return nil, errors.New("log file directory is required")
	}
	if strings.ContainsAny(directory, "\r\n") {
		return nil, errors.New("log file directory contains unsupported characters")
	}
	directory, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("resolve log file directory: %w", err)
	}
	if directory == string(filepath.Separator) {
		return nil, errors.New("log file directory must not be root")
	}
	if !validLogPrefix(source.GetPrefix()) {
		return nil, errors.New("log file prefix must contain only letters, digits, hyphens or underscores")
	}
	rotation := LogRotation(source.GetRotation())
	if rotation != LogRotationDay && rotation != LogRotationHour {
		return nil, fmt.Errorf("unsupported log rotation %q", source.GetRotation())
	}
	if source.GetRetentionDays() < 1 {
		return nil, errors.New("log file retention days must be positive")
	}
	return &LogFileConfig{
		Directory:     directory,
		Prefix:        source.GetPrefix(),
		Rotation:      rotation,
		RetentionDays: int(source.GetRetentionDays()),
	}, nil
}

// RotatingFileWriter 将完整 JSON 行写入按 UTC 时间命名的文件。
// 调用方应让每条日志由一次 Write 写入，并在退出时关闭 writer。
type RotatingFileWriter struct {
	mu            sync.Mutex
	config        LogFileConfig
	now           func() time.Time
	file          *os.File
	currentPeriod string
	closed        bool
	errorReported bool
}

// NewRotatingFileWriter 创建日志目录、打开当前文件并清理超过保留期的旧文件。
func NewRotatingFileWriter(config LogFileConfig) (*RotatingFileWriter, error) {
	return newRotatingFileWriter(config, time.Now)
}

func newRotatingFileWriter(config LogFileConfig, now func() time.Time) (*RotatingFileWriter, error) {
	config.Directory = strings.TrimSpace(config.Directory)
	if config.Directory == "" {
		return nil, errors.New("log file directory is required")
	}
	if !validLogPrefix(config.Prefix) {
		return nil, errors.New("log file prefix must contain only letters, digits, hyphens or underscores")
	}
	if config.Rotation == "" {
		config.Rotation = LogRotationDay
	}
	if config.Rotation != LogRotationDay && config.Rotation != LogRotationHour {
		return nil, fmt.Errorf("unsupported log rotation %q", config.Rotation)
	}
	if config.RetentionDays < 0 {
		return nil, errors.New("log file retention days must not be negative")
	}
	if config.RetentionDays == 0 {
		config.RetentionDays = 14
	}
	if err := os.MkdirAll(config.Directory, 0o750); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	writer := &RotatingFileWriter{config: config, now: now}
	if err := writer.rotate(writer.now().UTC()); err != nil {
		return nil, err
	}
	return writer, nil
}

// Write 写入一条完整日志；切割失败时继续写旧文件并在标准错误输出诊断。
func (writer *RotatingFileWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.closed {
		return 0, os.ErrClosed
	}
	rotationError := writer.rotate(writer.now().UTC())
	if rotationError != nil {
		writer.reportError(rotationError)
	}
	written, err := writer.file.Write(data)
	if err != nil {
		writer.reportError(fmt.Errorf("write log file: %w", err))
	} else if rotationError == nil {
		writer.errorReported = false
	}
	return written, err
}

// Close 同步并关闭当前文件；重复调用不会重复关闭文件。
func (writer *RotatingFileWriter) Close() error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.closed {
		return nil
	}
	writer.closed = true
	if writer.file == nil {
		return nil
	}
	return errors.Join(writer.file.Sync(), writer.file.Close())
}

func (writer *RotatingFileWriter) rotate(now time.Time) error {
	period := now.Format(writer.layout())
	if writer.currentPeriod == period {
		return nil
	}
	path := filepath.Join(writer.config.Directory, writer.config.Prefix+"-"+period+".jsonl")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	previous := writer.file
	writer.file = file
	writer.currentPeriod = period
	if previous != nil {
		if err := previous.Close(); err != nil {
			writer.reportError(fmt.Errorf("close previous log file: %w", err))
		}
	}
	if err := writer.removeExpired(now); err != nil {
		writer.reportError(fmt.Errorf("remove expired log files: %w", err))
	}
	return nil
}

func (writer *RotatingFileWriter) layout() string {
	if writer.config.Rotation == LogRotationHour {
		return "2006-01-02-15"
	}
	return "2006-01-02"
}

func (writer *RotatingFileWriter) removeExpired(now time.Time) error {
	entries, err := os.ReadDir(writer.config.Directory)
	if err != nil {
		return err
	}
	cutoff := now.AddDate(0, 0, -writer.config.RetentionDays)
	var failures []error
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, writer.config.Prefix+"-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		period := strings.TrimSuffix(strings.TrimPrefix(name, writer.config.Prefix+"-"), ".jsonl")
		layout := "2006-01-02"
		duration := 24 * time.Hour
		if len(period) == len("2006-01-02-15") {
			layout = "2006-01-02-15"
			duration = time.Hour
		}
		start, parseErr := time.ParseInLocation(layout, period, time.UTC)
		if parseErr != nil || start.Add(duration).After(cutoff) || name == writer.config.Prefix+"-"+writer.currentPeriod+".jsonl" {
			continue
		}
		if removeErr := os.Remove(filepath.Join(writer.config.Directory, name)); removeErr != nil {
			failures = append(failures, fmt.Errorf("remove %s: %w", name, removeErr))
		}
	}
	return errors.Join(failures...)
}

func (writer *RotatingFileWriter) reportError(err error) {
	if writer.errorReported {
		return
	}
	writer.errorReported = true
	_, _ = fmt.Fprintf(os.Stderr, "styx log file output failed: %v\n", err)
}

func validLogPrefix(prefix string) bool {
	if prefix == "" {
		return false
	}
	for _, character := range prefix {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}
