// Package nacos provides Nacos registry implementation for Kratos.
package nacos

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	nacoslogger "github.com/nacos-group/nacos-sdk-go/v2/common/logger"

	styxruntime "gitee.com/linpbin/styx/runtime"
)

var levelMap = map[string]slog.Level{
	"debug": slog.LevelDebug,
	"info":  slog.LevelInfo,
	"warn":  slog.LevelWarn,
	"error": slog.LevelError,
}

var sdkLoggerState = struct {
	sync.Mutex
	owner    *styxruntime.Runtime
	previous nacoslogger.Logger
}{}

// slogAdapter adapts slog to nacos-sdk-go Logger interface with level filtering.
type slogAdapter struct {
	level   slog.Level
	logger  *slog.Logger
	secrets []string
}

func newSlogAdapter(level string, logger *slog.Logger, secrets ...string) *slogAdapter {
	l, ok := levelMap[level]
	if !ok {
		l = slog.LevelWarn
	}
	filtered := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		if secret != "" {
			filtered = append(filtered, secret)
		}
	}
	return &slogAdapter{level: l, logger: logger, secrets: filtered}
}

func (l *slogAdapter) Info(args ...interface{}) {
	if l.level <= slog.LevelInfo {
		l.logger.Info(l.sanitize(fmt.Sprint(args...)))
	}
}

func (l *slogAdapter) Warn(args ...interface{}) {
	if l.level <= slog.LevelWarn {
		l.logger.Warn(l.sanitize(fmt.Sprint(args...)))
	}
}

func (l *slogAdapter) Error(args ...interface{}) {
	if l.level <= slog.LevelError {
		l.logger.Error(l.sanitize(fmt.Sprint(args...)))
	}
}

func (l *slogAdapter) Debug(args ...interface{}) {
	if l.level <= slog.LevelDebug {
		l.logger.Debug(l.sanitize(fmt.Sprint(args...)))
	}
}

func (l *slogAdapter) Infof(format string, args ...interface{}) {
	if l.level <= slog.LevelInfo {
		l.logger.Info(l.sanitize(fmt.Sprintf(format, args...)))
	}
}

func (l *slogAdapter) Warnf(format string, args ...interface{}) {
	if l.level <= slog.LevelWarn {
		l.logger.Warn(l.sanitize(fmt.Sprintf(format, args...)))
	}
}

func (l *slogAdapter) Errorf(format string, args ...interface{}) {
	if l.level <= slog.LevelError {
		l.logger.Error(l.sanitize(fmt.Sprintf(format, args...)))
	}
}

func (l *slogAdapter) Debugf(format string, args ...interface{}) {
	if l.level <= slog.LevelDebug {
		l.logger.Debug(l.sanitize(fmt.Sprintf(format, args...)))
	}
}

// Close implements logger.Logger interface.
func (l *slogAdapter) Close() error {
	return nil
}

// setSlogAdapter sets the slog adapter as the nacos logger with the given level.
func (l *slogAdapter) sanitize(message string) string {
	for _, secret := range l.secrets {
		message = strings.ReplaceAll(message, secret, "[REDACTED]")
	}
	return message
}

func acquireSlogAdapter(runtime *styxruntime.Runtime, level string, logger *slog.Logger, secrets ...string) (func(), error) {
	if runtime == nil || logger == nil {
		return nil, errors.New("nacos: runtime and logger are required")
	}
	sdkLoggerState.Lock()
	defer sdkLoggerState.Unlock()
	if sdkLoggerState.owner != nil {
		return nil, errors.New("nacos: SDK logger is already owned by another runtime")
	}
	previous := nacoslogger.GetLogger()
	adapter := newSlogAdapter(level, logger, secrets...)
	nacoslogger.SetLogger(adapter)
	sdkLoggerState.owner = runtime
	sdkLoggerState.previous = previous

	var once sync.Once
	return func() {
		once.Do(func() {
			sdkLoggerState.Lock()
			defer sdkLoggerState.Unlock()
			if sdkLoggerState.owner != runtime {
				return
			}
			if nacoslogger.GetLogger() == adapter {
				nacoslogger.SetLogger(sdkLoggerState.previous)
			}
			sdkLoggerState.owner = nil
			sdkLoggerState.previous = nil
		})
	}, nil
}
