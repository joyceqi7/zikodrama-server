package server

import (
	"context"
	"testing"
)

func TestNewWorkerServerRejectsNilWorker(t *testing.T) {
	t.Parallel()
	if _, err := NewWorkerServer(nil); err == nil {
		t.Fatal("缺少业务 Worker 时必须拒绝创建 Server")
	}
}

func TestUninitializedWorkerServerRejectsLifecycle(t *testing.T) {
	t.Parallel()
	server := &WorkerServer{}
	if err := server.Start(context.Background()); err == nil {
		t.Fatal("未初始化的 WorkerServer 不应启动")
	}
	if err := server.Stop(context.Background()); err == nil {
		t.Fatal("未初始化的 WorkerServer 不应停止")
	}
}
