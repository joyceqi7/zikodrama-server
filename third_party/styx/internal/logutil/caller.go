// Package logutil provides caller-aware logging helpers for styx framework.
//
// When styx middleware, MySQL logger, or Redis hook call slog.InfoContext(),
// the source always points to styx framework code (e.g., middleware/access_log.go:20).
// This package walks the call stack to find the first business code frame,
// so the source points to the actual caller (e.g., internal/data/account.go:80).
package logutil

import (
	"context"
	"log/slog"
	"runtime"
	"strings"
	"time"
)

// Framework package prefixes to skip when walking the call stack.
var frameworkPrefixes = []string{
	"gitee.com/linpbin/styx",
	"gorm.io/",
	"github.com/redis/go-redis",
	"github.com/go-kratos/kratos",
	"google.golang.org/grpc",
}

// Standard library prefixes to skip.
var stdlibPrefixes = []string{
	"net/http.",
	"database/sql.",
	"runtime.",
	"reflect.",
}

// isFrameworkFrame reports whether the given function name belongs to a framework package.
func isFrameworkFrame(funcName string) bool {
	for _, prefix := range frameworkPrefixes {
		if strings.HasPrefix(funcName, prefix) {
			return true
		}
	}
	for _, prefix := range stdlibPrefixes {
		if strings.HasPrefix(funcName, prefix) {
			return true
		}
	}
	return false
}

// callerPC walks the call stack, skipping framework frames,
// and returns the PC of the first business code frame.
// Returns 0 if no business code frame is found.
func callerPC() uintptr {
	var pcs [25]uintptr
	n := runtime.Callers(3, pcs[:]) // skip: Callers, callerPC, Info/Warn/Error
	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		if !isFrameworkFrame(frame.Function) {
			return frame.PC
		}
		if !more {
			break
		}
	}
	return 0
}

// Info logs at INFO level with source pointing to the first non-framework caller.
func Info(logger *slog.Logger, ctx context.Context, msg string, args ...any) {
	logRecord(logger, ctx, slog.LevelInfo, msg, args...)
}

// Warn logs at WARN level with source pointing to the first non-framework caller.
func Warn(logger *slog.Logger, ctx context.Context, msg string, args ...any) {
	logRecord(logger, ctx, slog.LevelWarn, msg, args...)
}

// Error logs at ERROR level with source pointing to the first non-framework caller.
func Error(logger *slog.Logger, ctx context.Context, msg string, args ...any) {
	logRecord(logger, ctx, slog.LevelError, msg, args...)
}

// logRecord creates a slog.Record with a caller-aware PC and sends it through
// the default handler chain.
func logRecord(logger *slog.Logger, ctx context.Context, level slog.Level, msg string, args ...any) {
	handler := logger.Handler()
	if !handler.Enabled(ctx, level) {
		return
	}
	r := slog.NewRecord(time.Now(), level, msg, callerPC())
	r.Add(args...)
	_ = handler.Handle(ctx, r)
}
