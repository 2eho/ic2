package exec

import (
	"context"
	"testing"
	"time"

	"github.com/context-flow/ic/internal/graph"
	"github.com/context-flow/ic/internal/platform"
	"github.com/context-flow/ic/internal/provider"
)

type asyncAdapter struct {
	fakeAdapter
	polls     int32
	pollState string
}

func (a *asyncAdapter) Invoke(ctx context.Context, cred provider.Credential, req provider.Request) (provider.Response, error) {
	return provider.Response{
		RemoteTask: &provider.RemoteTask{ID: "task_1", Provider: "openai", Status: "queued"},
	}, nil
}

func (a *asyncAdapter) Poll(context.Context, provider.Credential, string) (provider.RemoteTask, error) {
	a.polls++
	if a.pollState == "" {
		a.pollState = "succeeded"
	}
	return provider.RemoteTask{ID: "task_1", Provider: "openai", Status: a.pollState, Progress: 100}, nil
}

// ATK-15：视频任务运行中 kill 服务端再启动，任务应继续轮询并完成。
func TestATK15ResumeAsyncTaskAfterRestart(t *testing.T) {
	adapter := &asyncAdapter{fakeAdapter: fakeAdapter{id: "openai"}}
	e, store, _, _ := engineWith(adapter, provider.Pricing{})
	ctx := context.Background()

	// 第一次运行：产出 pending 的异步任务（模拟进程在轮询前被杀）
	doc := genDoc()
	doc.Nodes["g_1"] = func() graph.Node { n := doc.Nodes["g_1"]; n.Spec["capability"] = "video.generate"; return n }()
	run, _ := e.Create(ctx, RunRequest{WorkspaceID: "ws_1", CanvasID: "cv_1", TargetNodes: []string{"g_1"}, ActorID: "u_1"})

	// 手工构造「已提交上游、等待轮询」的状态
	step := &Step{ID: "st_g_1", NodeID: "g_1", Kind: StepGenerate, Status: StepRunning,
		Attempts: []Attempt{{
			Index: 1, ProviderID: "openai", ModelID: "sora-2", RequestID: "req_1",
			Status: StepRunning, RemoteTask: &provider.RemoteTask{ID: "task_1", Provider: "openai", Status: "queued"},
		}},
		StartedAt: time.Now().UTC(),
	}
	run.Steps = []*Step{step}
	run.Status = RunRunning
	if err := store.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	store.SaveStep(ctx, run.ID, step)

	// 重启后恢复
	reloaded, err := store.LoadRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Resume(ctx, reloaded); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if adapter.polls == 0 {
		t.Fatal("应继续轮询异步任务")
	}
	if reloaded.Status != RunSucceeded {
		t.Fatalf("恢复后状态=%s", reloaded.Status)
	}
	if len(reloaded.Steps) != 1 || reloaded.Steps[0].Status != StepSucceeded {
		t.Fatalf("步骤未收敛: %+v", reloaded.Steps[0].Status)
	}
}

// 无异步任务的「运行中」运行在重启后必须收敛，不能永久挂起。
func TestResumeConvergesInterruptedRun(t *testing.T) {
	adapter := &fakeAdapter{id: "openai"}
	e, store, _, _ := engineWith(adapter, provider.Pricing{})
	ctx := context.Background()
	run, _ := e.Create(ctx, RunRequest{WorkspaceID: "ws_1", CanvasID: "cv_1", ActorID: "u_1"})
	run.Status = RunRunning
	run.Steps = []*Step{{ID: "st_1", NodeID: "g_1", Kind: StepGenerate, Status: StepRunning, StartedAt: time.Now().UTC()}}
	store.SaveRun(ctx, run)
	store.SaveStep(ctx, run.ID, run.Steps[0])

	reloaded, _ := store.LoadRun(ctx, run.ID)
	if err := e.Resume(ctx, reloaded); err != nil {
		t.Fatal(err)
	}
	if reloaded.Status == RunRunning {
		t.Fatal("中断的运行必须收敛到终态")
	}
	if reloaded.Steps[0].Error == nil || reloaded.Steps[0].Error.Code != platform.CodeInterrupted {
		t.Fatalf("应给出中断原因: %+v", reloaded.Steps[0].Error)
	}
}

func TestResumeAll(t *testing.T) {
	adapter := &fakeAdapter{id: "openai"}
	e, store, _, _ := engineWith(adapter, provider.Pricing{})
	ctx := context.Background()
	run, _ := e.Create(ctx, RunRequest{WorkspaceID: "ws_1", CanvasID: "cv_1", ActorID: "u_1"})
	run.Status = RunRunning
	run.Steps = []*Step{{ID: "st_1", NodeID: "g_1", Status: StepRunning, StartedAt: time.Now().UTC()}}
	store.SaveRun(ctx, run)
	store.SaveStep(ctx, run.ID, run.Steps[0])

	n, err := e.ResumeAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("应恢复 1 个运行，实际 %d", n)
	}
}
