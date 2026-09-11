package wiring

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/context-flow/ic/internal/agent"
	"github.com/context-flow/ic/internal/asset"
	"github.com/context-flow/ic/internal/exec"
	"github.com/context-flow/ic/internal/graph"
	"github.com/context-flow/ic/internal/platform"
	"github.com/context-flow/ic/internal/prompt"
)

// Agent 的外部能力适配。
//
// 集中在一个文件里是为了让「Agent 能做什么」一目了然：
// 每个适配器对应 Agent 工具表里的一个能力组（素材检索 / 提示词检索 /
// 运行查询 / 触发运行 / 附件落库 / Skills）。漏接一个会在 wiring 测试里变红。

// agentRuns 把执行引擎暴露成 agent.RunTrigger + agent.RunLister。
type agentRuns struct {
	engine   *exec.Engine
	graph    *graph.Service
	compiler *exec.Compiler
}

// TriggerRun 见 agent.RunTrigger：触发一次真实运行并返回 runId。
//
// 与画布手动点「生成」走完全相同的路径（编译 → 调度 → 回写），
// 差别只有 Trigger 标记为 agent——这样审计日志里能区分来源，
// 而重试/计量/限额行为完全一致。
func (a *agentRuns) TriggerRun(ctx context.Context, canvasID string, nodeIDs []string, actor string) (string, error) {
	doc, err := a.graph.Get(ctx, canvasID)
	if err != nil {
		return "", err
	}
	run, err := a.engine.Create(ctx, exec.RunRequest{
		WorkspaceID: a.workspaceOf(ctx, canvasID),
		CanvasID:    canvasID,
		ProjectID:   doc.ProjectID,
		TargetNodes: nodeIDs,
		Trigger:     "agent",
		ActorID:     actor,
	})
	if err != nil {
		return "", err
	}
	// 编译与画布手动触发完全一致：同一个 Compiler、同一套凭据解析。
	// 差别只有 Trigger 标记，便于审计区分来源。
	plan, err := a.compiler.Compile(doc, nodeIDs)
	if err != nil {
		a.engine.MarkFailed(ctx, run, err)
		return run.ID, nil
	}
	go func() {
		bg, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if err := a.engine.Execute(bg, run, plan, doc); err != nil {
			a.engine.MarkFailed(bg, run, err)
		}
	}()
	return run.ID, nil
}

// ListRunsBrief 见 agent.RunLister。
func (a *agentRuns) ListRunsBrief(ctx context.Context, wsID, canvasID string, limit int) ([]map[string]any, error) {
	runs, _, err := a.engine.List(ctx, wsID, canvasID, limit, "")
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(runs))
	for _, r := range runs {
		out = append(out, map[string]any{
			"id": r.ID, "status": string(r.Status), "trigger": r.Trigger,
			"targets": r.TargetNodes, "costMicros": r.Usage.CostMicros,
			"startedAt": r.StartedAt, "finishedAt": r.FinishedAt,
		})
	}
	return out, nil
}

// GetRunBrief 见 agent.RunLister。
func (a *agentRuns) GetRunBrief(ctx context.Context, wsID, runID string) (map[string]any, error) {
	run, err := a.engine.Get(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run.WorkspaceID != wsID {
		// 跨工作区不可见：返回 not_found 而不是 forbidden（不泄露存在性）
		return nil, platform.ErrNotFound("run")
	}
	steps := make([]map[string]any, 0, len(run.Steps))
	for _, st := range run.Steps {
		steps = append(steps, map[string]any{
			"id": st.ID, "nodeId": st.NodeID, "status": string(st.Status),
			"attempts": len(st.Attempts), "text": st.Text,
		})
	}
	out := map[string]any{
		"id": run.ID, "status": string(run.Status), "steps": steps,
		"costMicros": run.Usage.CostMicros, "images": run.Usage.Images,
		"startedAt": run.StartedAt, "finishedAt": run.FinishedAt,
	}
	if run.Error != nil {
		out["error"] = map[string]any{"code": run.Error.Code, "message": run.Error.Message}
	}
	return out, nil
}

func (a *agentRuns) workspaceOf(ctx context.Context, canvasID string) string {
	if ws, err := a.graph.WorkspaceOf(ctx, canvasID); err == nil {
		return ws
	}
	return ""
}

// agentAssets 把资产服务暴露成 agent.AssetLister + agent.AttachmentFetcher。
type agentAssets struct {
	assets *asset.Service
	fetch  URLFetcher
}

// SearchAssets 见 agent.AssetLister。
func (a *agentAssets) SearchAssets(ctx context.Context, wsID, query, kind string, limit int) ([]map[string]any, error) {
	items, _, err := a.assets.List(ctx, wsID, kind, limit, "")
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(items))
	q := strings.ToLower(strings.TrimSpace(query))
	for _, it := range items {
		// 关键词过滤在 Go 侧做：资产列表本身是游标分页的，
		// 下推到 SQL 需要加索引与迁移，而单次检索量很小（<=100 条）。
		if q != "" && !strings.Contains(strings.ToLower(it.Name), q) {
			continue
		}
		out = append(out, map[string]any{
			"id": it.ID, "kind": it.Kind, "name": it.Name, "mime": it.MIME,
			"size": it.Size, "createdAt": it.CreatedAt,
		})
	}
	return out, nil
}

// StoreAttachment 见 agent.AttachmentFetcher：URL 或 data URI → 资产。
func (a *agentAssets) StoreAttachment(ctx context.Context, wsID, name, mime, url, dataURI string) (string, error) {
	switch {
	case dataURI != "":
		raw, mt, err := decodeDataURI(dataURI)
		if err != nil {
			return "", err
		}
		if mime == "" {
			mime = mt
		}
		if name == "" {
			name = "attachment"
		}
		dto, err := a.assets.Upload(ctx, wsID, name, mime, strings.NewReader(string(raw)), int64(len(raw)))
		if err != nil {
			return "", err
		}
		return dto.ID, nil
	case url != "":
		if a.fetch == nil {
			return "", platform.NewError(501, platform.CodeNotImplemented, "fetch 未装配")
		}
		body, ct, size, err := a.fetch.Fetch(ctx, url, nil)
		if err != nil {
			return "", err
		}
		defer body.Close()
		if mime == "" {
			mime = ct
		}
		if name == "" {
			name = "attachment"
		}
		dto, err := a.assets.Upload(ctx, wsID, name, mime, body, size)
		if err != nil {
			return "", err
		}
		return dto.ID, nil
	default:
		return "", platform.ErrInvalid("attachments 必须提供 url 或 data")
	}
}

// decodeDataURI 解析 `data:<mime>;base64,<payload>`。
func decodeDataURI(uri string) ([]byte, string, error) {
	if !strings.HasPrefix(uri, "data:") {
		return nil, "", platform.ErrInvalid("不是合法的 data URI")
	}
	comma := strings.Index(uri, ",")
	if comma < 0 {
		return nil, "", platform.ErrInvalid("data URI 缺少 payload")
	}
	meta := uri[len("data:"):comma]
	payload := uri[comma+1:]
	mime := "application/octet-stream"
	if idx := strings.Index(meta, ";"); idx >= 0 {
		if meta[:idx] != "" {
			mime = meta[:idx]
		}
		meta = meta[idx+1:]
	} else if meta != "" {
		mime = meta
	}
	if strings.Contains(meta, "base64") {
		raw, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return nil, "", platform.NewError(422, platform.CodeInvalidRequest, "data URI base64 解码失败")
		}
		return raw, mime, nil
	}
	return []byte(payload), mime, nil
}

// agentPrompts 把提示词服务暴露成 agent.PromptSearcher。
type agentPrompts struct{ svc *prompt.Service }

// SearchPrompts 见 agent.PromptSearcher。
func (a *agentPrompts) SearchPrompts(ctx context.Context, wsID, query string, tags []string, limit int) ([]map[string]any, error) {
	items, _, err := a.svc.Search(ctx, wsID, query, tags, limit, "")
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		out = append(out, map[string]any{
			"id": it.ID, "title": it.Title, "tags": it.Tags,
			"content": it.Content, "variables": it.Variables,
		})
	}
	return out, nil
}

// agentSkills 是 Skills 存储的类型别名（避免 wiring 依赖 agent 内部命名）。
type agentSkills = agent.SkillsSQLStore

var (
	_ agent.RunTrigger        = (*agentRuns)(nil)
	_ agent.RunLister         = (*agentRuns)(nil)
	_ agent.AssetLister       = (*agentAssets)(nil)
	_ agent.AttachmentFetcher = (*agentAssets)(nil)
	_ agent.PromptSearcher    = (*agentPrompts)(nil)
	_ agent.SkillStore        = (*agent.SkillsSQLStore)(nil)
	_                         = sql.ErrNoRows
	_                         = fmt.Sprintf
)
