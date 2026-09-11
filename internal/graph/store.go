package graph

import (
	"context"
	"encoding/json"
	"time"
)

// Record 是一条已落库的 op 记录。
type Record struct {
	Seq       int64           `json:"seq"`
	CanvasID  string          `json:"canvasId"`
	ActorID   string          `json:"actorId"`
	Op        json.RawMessage `json:"op"`
	Version   int64           `json:"version"`
	CreatedAt time.Time       `json:"createdAt"`
}

// Store 是画布文档的持久化抽象（解耦清单要求 ≥2 个实现：SQL + 内存）。
type Store interface {
	// GetDocument 读取最新文档；不存在返回 ErrNotFound。
	GetDocument(ctx context.Context, canvasID string) (*CanvasDocument, error)
	// CreateDocument 创建文档（已存在返回冲突）。
	CreateDocument(ctx context.Context, doc *CanvasDocument) error
	// AppendOps 原子追加 op 并更新快照，返回新版本与 op 记录。
	// baseVersion 用于乐观并发；expectedSeq 用于顺序校验。
	AppendOps(ctx context.Context, canvasID string, baseVersion int64, doc *CanvasDocument,
		ops []json.RawMessage, actor string, now time.Time) (int64, []Record, error)
	// ListOps 增量拉取 op（since 为排他下界的 op seq）。
	ListOps(ctx context.Context, canvasID string, since int64, limit int) ([]Record, error)
	// ListOpsSinceVersion 拉取「版本 > sinceVersion」的 op（冲突 rebase 用）。
	ListOpsSinceVersion(ctx context.Context, canvasID string, sinceVersion int64, limit int) ([]Record, error)
	// ListCanvases 列出项目下的画布。
	ListCanvases(ctx context.Context, projectID string) ([]CanvasMeta, error)
	// SaveSettings 保存画布元数据（名称/设置）。
	UpdateMeta(ctx context.Context, canvasID string, name string, settings CanvasSettings) error
	// DeleteDocument 删除画布。
	DeleteDocument(ctx context.Context, canvasID string) error
}

// CanvasMeta 是画布的轻量元数据。
type CanvasMeta struct {
	ID        string         `json:"id"`
	ProjectID string         `json:"projectId"`
	Name      string         `json:"name"`
	Version   int64          `json:"version"`
	Nodes     int            `json:"nodes"`
	Settings  CanvasSettings `json:"settings"`
	UpdatedAt time.Time      `json:"updatedAt"`
}
