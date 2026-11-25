// Package server 把业务 Worker 接入 Kratos 生命周期。
package server

import (
	"context"
	"fmt"

	"drama-server/internal/biz"
)

// WorkerServer 实现 Kratos transport.Server，让框架统一启动和停止媒资后台任务。
type WorkerServer struct {
	// worker 保存与框架无关的业务 Worker。
	worker *biz.Worker
}

// NewWorkerServer 创建媒资后台任务生命周期适配器。
func NewWorkerServer(worker *biz.Worker) (*WorkerServer, error) {
	if worker == nil {
		return nil, fmt.Errorf("worker server: business worker is required")
	}
	return &WorkerServer{worker: worker}, nil
}

// Start 启动媒资后台任务。
func (server *WorkerServer) Start(ctx context.Context) error {
	if server == nil || server.worker == nil {
		return fmt.Errorf("worker server is not initialized")
	}
	return server.worker.Start(ctx)
}

// Stop 停止媒资后台任务并等待在途处理结束。
func (server *WorkerServer) Stop(ctx context.Context) error {
	if server == nil || server.worker == nil {
		return fmt.Errorf("worker server is not initialized")
	}
	return server.worker.Stop(ctx)
}
