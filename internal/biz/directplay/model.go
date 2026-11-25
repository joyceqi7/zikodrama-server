// Package directplay 管理不依赖 TikTok 媒资库的主体内直连目录的上下架状态。
package directplay

import (
	"context"
	"drama-server/internal/biz/consumer"
	"errors"
	"time"
)

// ErrConflict 表示修订、幂等输入或操作状态冲突。
var ErrConflict = errors.New("direct publication conflict")

// ErrInvalid 表示发布输入不满足门禁。
var ErrInvalid = errors.New("invalid direct publication")

// Publication 保留独立发布指针，不改动官方上线状态。
type Publication struct {
	// DramaID 是共享内容的内部主键。
	DramaID string `json:"dramaId"`
	// Status 为 unpublished、published 或 offline。
	Status string `json:"status"`
	// CurrentVersionID 是当前不可变快照。
	CurrentVersionID string `json:"currentVersionId,omitempty"`
	// Revision 是并发写入栅栏。
	Revision int64 `json:"revision"`
}

// Version 保存直连发布生成的不可变快照，版本号仅在同主体、同剧的直连渠道内递增。
type Version struct {
	// ID 是数据库分配的字符串版本主键。
	ID string `json:"id"`
	// DramaID 关联用于本次发布的短剧。
	DramaID string `json:"dramaId"`
	// Number 是该剧的直连发布版本号。
	Number int64 `json:"number"`
	// SourceDigest 是发布内容摘要，用于识别该渠道的内容变化。
	SourceDigest string `json:"sourceDigest"`
	// CreatedBy 是本次发布的管理员标识。
	CreatedBy string `json:"createdBy"`
	// Snapshot 是本次直连发布冻结的剧和集信息。
	Snapshot consumer.Drama `json:"snapshot"`
	// CreatedAt 是 UTC 版本生成时间。
	CreatedAt time.Time `json:"createdAt"`
}

// Workbench 提供发布工作台的草稿、线上与操作数据。
type Workbench struct {
	// Summary 提供当前版本、应用数、最近操作与待修改状态。
	Summary Summary `json:"summary"`
	// Publication 提供独立状态。
	Publication Publication `json:"publication"`
	// Draft 是待发布的共享内容。
	Draft consumer.Drama `json:"draft"`
	// SourceDigest 是编辑内容摘要，用于展示当前已保存内容与线上内容的差异。
	SourceDigest string `json:"sourceDigest"`
	// Versions 保留全部不可变历史。
	Versions []Version `json:"versions"`
}

// Command 是带修订与幂等键的发布命令。
type Command struct {
	// RequestID 固定一次操作，重试必须保持相同输入。
	RequestID string `json:"requestId"`
	// ExpectedRevision 是客户端观察到的修订。
	ExpectedRevision int64 `json:"expectedRevision"`
	// Kind 为 publish、offline 或 relist；publish 冻结当前内容，relist 恢复原版本。
	Kind string `json:"kind"`
}

// Operation 保存同步上下架的持久化结果，不包含临时播放凭据。
type Operation struct {
	// ID 是操作主键。
	ID string `json:"id"`
	// DramaID 是目标剧目。
	DramaID string `json:"dramaId"`
	// Command 保留原始幂等命令。
	Command Command `json:"command"`
	// Status 为 succeeded，只有事务成功才保存操作。
	Status string `json:"status"`
	// ResultVersionID 是本次生效版本。
	ResultVersionID string `json:"resultVersionId,omitempty"`
	// CreatedAt 是提交时间。
	CreatedAt time.Time `json:"createdAt"`
}

// Repository 定义独立发布持久化边界。
type Repository interface {
	// Workbench 读取独立发布和完整草稿工作台。
	Workbench(context.Context, string) (Workbench, error)
	// Accept 在共享剧目锁中校验并冻结已保存内容并幂等上架或下架；事务失败保留原状态，重复键不同输入返回冲突。
	Accept(context.Context, string, Command, string) (Operation, error)
	// Operation 读取持久化操作状态，未找到时返回错误。
	Operation(context.Context, string) (Operation, error)
	// List 批量读取目录摘要，不读取历史版本全文。
	List(context.Context) ([]Workbench, error)
}

// Digest 返回直连发布内容的确定性摘要。
func Digest(d consumer.Drama) string { return consumer.ContentDigest(d) }

// Summary 是仅用于运营的双渠道摘要，不进入消费者播放或首页卡片。
type Summary struct {
	// Revision 绑定列表所见的发布修订，用于同步上下架的并发保护。
	Revision int64 `json:"revision"`
	// SourceDigest 仅供仓储比较当前已保存内容与直连快照，不通过 API 返回。
	SourceDigest string `json:"-"`
	// Status 是直连通道状态。
	Status string `json:"status"`
	// Version 是当前不可变版本序号。
	Version int64 `json:"version"`
	// VersionID 是当前不可变版本主键。
	VersionID string `json:"versionId,omitempty"`
	// EnabledApplications 是当前主体有效直连应用的数量。
	EnabledApplications int `json:"enabledApplications"`
	// LastOperation 是最近完成的同步上下架操作。
	LastOperation *Operation `json:"lastOperation,omitempty"`
	// PendingChanges 表示已保存内容与直连发布快照不同，保存不会切换线上版本。
	PendingChanges bool `json:"pendingChanges"`
	// PublishedAt 是当前上架版本的生成时间，下架后仍保留。
	PublishedAt *time.Time `json:"publishedAt,omitempty"`
	// PublishedBy 是发布管理员标识。
	PublishedBy string `json:"publishedBy,omitempty"`
}
