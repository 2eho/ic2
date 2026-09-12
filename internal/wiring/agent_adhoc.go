package wiring

import (
	"context"
	"strings"

	"github.com/context-flow/ic/internal/agent"
	"github.com/context-flow/ic/internal/api"
	"github.com/context-flow/ic/internal/exec"
	"github.com/context-flow/ic/internal/graph"
	"github.com/context-flow/ic/internal/platform"
)

// 上游工具面（9.5）需要的两个适配器。
//
// 与 agentRuns 的关系：agentRuns 承担「编译画布 DAG 并执行」，
// 这里是「参数 → 直通 Run」与「列画布」。三者分开是因为它们的
// **授权模型不同**：
//
//	agentRuns.TriggerRun   → 按画布授权（已有 canvasId）
//	agentAdHoc.SubmitAdHoc → 按工作区授权（不落画布）
//	agentProjects.List…    → 按工作区授权（还不知道要操作哪个画布）

// agentAdHoc 把工作台直通生成暴露成 agent.AdHocRunner。
//
// 它和 api/runsAdapter.Submit(AdHoc=true) 走的是**同一条链路**
// （engine.Create → CompileAdHoc → ExecuteAdHoc），而不是另写一份：
// 另写一份的表现是「Agent 触发的生成不计量」或「不重试」这类
// 极难发现的偏差。
type agentAdHoc struct {
	engine *exec.Engine
	runs   *runsAdapter
	graph  *graph.Service
}

// SubmitAdHoc 见 agent.AdHocRunner。
func (a *agentAdHoc) SubmitAdHoc(ctx context.Context, canvasID, actor string, params map[string]any) (string, error) {
	canvas, err := a.graph.Get(ctx, canvasID)
	if err != nil {
		return "", err
	}
	wsID := ""
	if ws, werr := a.graph.WorkspaceOf(ctx, canvasID); werr == nil {
		wsID = ws
	}
	dto, err := a.runs.Submit(ctx, api.RunRequest{
		WorkspaceID: wsID,
		CanvasID:    canvasID,
		ProjectID:   canvas.ProjectID,
		Trigger:     "agent",
		ActorID:     actor,
		Params:      params,
		AdHoc:       true,
	})
	if err != nil {
		return "", err
	}
	return dto.ID, nil
}

// agentProjects 把画布列表暴露成 agent.ProjectLister。
type agentProjects struct {
	graph *graph.Service
	auth  api.AuthService
	// defaultWorkspace 解析「当前工作区」。
	// 注入而不是直接查库：工作区的选择规则属于装配层
	//（MCP 客户端不带上下文时取第一个），不是这个适配器该决定的。
	defaultWorkspace func(ctx context.Context) (string, error)
}

// ListProjectsBrief 见 agent.ProjectLister。
//
// 返回的是**精简字段**（id/名称/时间/节点数），不含节点内容：
// Agent 需要它来「找到画布并拿到 id」，不需要看内容。
// 返回完整文档会让一次工具调用带上几 MB 数据，
// 而模型真正需要的信息只有几行。
func (a *agentProjects) ListProjectsBrief(ctx context.Context, keyword string, page, pageSize int) ([]map[string]any, error) {
	if a.auth == nil {
		return nil, platform.NewError(501, platform.CodeNotImplemented, "workspace listing is not wired")
	}
	// 用「当前工作区」而不是「全部工作区」：跨工作区列举会泄露
	// 用户在其他工作区的画布名（INV-10）。
	wsID, err := a.workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	projects, err := a.auth.ListProjects(ctx, wsID)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(projects))
	start := (page - 1) * pageSize
	if start >= len(projects) {
		return out, nil
	}
	end := start + pageSize
	if end > len(projects) {
		end = len(projects)
	}
	for _, p := range projects[start:end] {
		if keyword != "" && !containsFold(p.Name, keyword) {
			continue
		}
		canvases, _ := a.graph.List(ctx, p.ID)
		out = append(out, map[string]any{
			"projectId": p.ID, "name": p.Name,
			"canvases": len(canvases), "updatedAt": p.UpdatedAt,
		})
	}
	return out, nil
}

func (a *agentProjects) workspaceOf(ctx context.Context) (string, error) {
	return a.defaultWorkspace(ctx)
}

// containsFold 是大小写不敏感的子串匹配（中文不受影响）。
//
// 不用 strings.Contains 是为了让「用英文关键词搜中文命名的画布」也能命中一半
// （例如 "demo" 命中 "Demo 项目"）。中文没有大小写，但英文有。
func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}

var (
	_ agent.AdHocRunner   = (*agentAdHoc)(nil)
	_ agent.ProjectLister = (*agentProjects)(nil)
)
