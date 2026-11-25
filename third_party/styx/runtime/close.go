package runtime

import (
	"context"
	"errors"
	"sync"
	"time"
)

// BoundedClose 把不接收 Context 的第三方 Close 适配为 ShutdownFunc。
// Close 最多启动一次；调用方超时后可用新 Context 继续等待同一个关闭过程。
func BoundedClose(closeFunc func() error) ShutdownFunc {
	var once sync.Once
	done := make(chan struct{})
	var closeErr error
	return func(ctx context.Context) error {
		if ctx == nil {
			return newError("runtime", "bounded close", KindInvalidConfig, false, errors.New("shutdown context is required"))
		}
		if closeFunc == nil {
			return newError("runtime", "bounded close", KindInvalidConfig, false, errors.New("close function is required"))
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		once.Do(func() {
			go func() {
				closeErr = closeFunc()
				close(done)
			}()
		})
		select {
		case <-done:
			return closeErr
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// BoundedShutdown 使用独立的内部截止时间执行接收 Context 的关闭函数。
// 关闭过程最多启动一次；单个调用方超时只停止等待，不会取消底层关闭过程。
func BoundedShutdown(timeout time.Duration, shutdownFunc func(context.Context) error) ShutdownFunc {
	var once sync.Once
	done := make(chan struct{})
	var shutdownErr error
	return func(ctx context.Context) error {
		if ctx == nil {
			return newError("runtime", "bounded shutdown", KindInvalidConfig, false, errors.New("shutdown context is required"))
		}
		if timeout <= 0 {
			return newError("runtime", "bounded shutdown", KindInvalidConfig, false, errors.New("shutdown timeout must be positive"))
		}
		if shutdownFunc == nil {
			return newError("runtime", "bounded shutdown", KindInvalidConfig, false, errors.New("shutdown function is required"))
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		once.Do(func() {
			go func() {
				shutdownContext, cancel := context.WithTimeout(context.Background(), timeout)
				defer cancel()
				shutdownErr = shutdownFunc(shutdownContext)
				close(done)
			}()
		})
		select {
		case <-done:
			return shutdownErr
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
