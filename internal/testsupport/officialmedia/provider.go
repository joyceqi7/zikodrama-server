// Package officialmedia 为本地业务测试提供有状态的官方媒体任务替身，禁止用于生产装配。
package officialmedia

import (
	"context"
	"sync"

	"drama-server/internal/biz/operations"
)

// Capabilities 模拟按稳定幂等键创建任务并保存逐视频结果。
type Capabilities struct {
	// mu 保护不同 Worker 并发读取测试任务。
	mu sync.Mutex
	// tasks 保存稳定任务标识与输入视频集合。
	tasks map[string][]string
}

// RegisterVideo 模拟已存在 VID 被平台受理。
func (*Capabilities) RegisterVideo(_ context.Context, input operations.TikTokVideoInput) (operations.TikTokVideoRegistration, error) {
	return operations.TikTokVideoRegistration{VID: input.VID}, nil
}

// QueryVideo 模拟平台确认指定 VID，而非只返回成功状态。
func (*Capabilities) QueryVideo(_ context.Context, _, vid, _ string) (operations.TikTokVideoObservation, error) {
	return operations.TikTokVideoObservation{Status: 2, VID: vid}, nil
}

// StartTranscode 同一幂等键始终返回同一任务。
func (provider *Capabilities) StartTranscode(_ context.Context, input operations.TikTokTranscodeInput) (string, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.tasks == nil {
		provider.tasks = map[string][]string{}
	}
	id := "task-" + input.IdempotencyKey
	if _, exists := provider.tasks[id]; !exists {
		provider.tasks[id] = append([]string(nil), input.VIDs...)
	}
	return id, nil
}

// QueryTranscode 仅对之前创建的任务返回精确的视频集合。
func (provider *Capabilities) QueryTranscode(_ context.Context, taskID, _ string) (operations.TikTokTranscodeObservation, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	vids, exists := provider.tasks[taskID]
	if !exists {
		return operations.TikTokTranscodeObservation{}, &operations.PlatformCallError{Code: "task_missing"}
	}
	return operations.TikTokTranscodeObservation{TaskID: taskID, Status: "SUCCESS", TotalCount: len(vids), SuccessVIDs: append([]string(nil), vids...)}, nil
}

// GenerateMinisLink 只提供本地测试夹具，真实适配器协议由 httptest 单独校验。
func (*Capabilities) GenerateMinisLink(context.Context, operations.MinisLinkInput) (string, error) {
	return "https://www.tiktok.com/minis/test-official-link", nil
}
