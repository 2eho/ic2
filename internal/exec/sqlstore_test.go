package exec_test

import (
	"context"
	"testing"
	"time"

	"github.com/context-flow/ic/internal/exec"
	"github.com/context-flow/ic/internal/platform"
	"github.com/context-flow/ic/internal/provider"
	"github.com/context-flow/ic/migrations"
)

// ATK-03（落库侧）：同一 request_id 的重复 attempt 只能存在一条记录。
//
// 为什么这条必须独立于传输层测试：即使上游幂等头被忽略，
// 服务端也必须保证「同一次上游调用只记一次账」——
// 否则重试会把成本翻倍写进用量统计，用户看到的账单与上游账单不一致。
func TestATK03RequestIDUniqueInStore(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	store := exec.NewSQLStore(db.DB, db.Dialect)

	run := &exec.Run{
		ID: "run_atk03", WorkspaceID: "ws_1", Trigger: "manual", Status: exec.RunRunning,
		StartedAt: time.Now().UTC(),
	}
	step := &exec.Step{ID: "step_1", NodeID: "n_1", Kind: exec.StepGenerate, Status: exec.StepRunning, StartedAt: time.Now().UTC()}
	run.Steps = []*exec.Step{step}
	if err := store.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}

	// 第一次 attempt（超时但上游已计费）
	a1 := &exec.Attempt{
		Index: 0, ProviderID: "openai", ModelID: "gpt-image-1",
		RequestID: "run_atk03:step_1", Status: exec.StepFailed,
		Usage: provider.Usage{Images: 1, CostMicros: 40_000},
	}
	if err := store.SaveAttempt(ctx, run.ID, step.ID, a1); err != nil {
		t.Fatal(err)
	}
	// 第二次 attempt（重试）必须携带同一 request_id
	a2 := &exec.Attempt{
		Index: 1, ProviderID: "openai", ModelID: "gpt-image-1",
		RequestID: "run_atk03:step_1", Status: exec.StepSucceeded,
		Usage: provider.Usage{Images: 1, CostMicros: 40_000},
	}
	if err := store.SaveAttempt(ctx, run.ID, step.ID, a2); err != nil {
		t.Fatalf("重复 request_id 应被 upsert 吸收而不是报错: %v", err)
	}

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM run_attempts WHERE request_id = ?`, "run_atk03:step_1").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("同一 request_id 应只有一条记录（否则重复计费），实际 %d 条", n)
	}
	// 状态应更新为最后一次
	var status string
	if err := db.QueryRow(`SELECT status FROM run_attempts WHERE request_id = ?`, "run_atk03:step_1").Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != string(exec.StepSucceeded) {
		t.Fatalf("重试后状态应更新，实际 %s", status)
	}
}

// 幂等键在 DB 层收敛：并发提交不会产生两条 Run（INV-2）。
func TestATK02IdempotencyKeyUniqueInSQLStore(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	store := exec.NewSQLStore(db.DB, db.Dialect)

	base := exec.Run{
		ID: "run_a", WorkspaceID: "ws_1", Trigger: "manual", Status: exec.RunPending,
		IdempotencyKey: "same-key", StartedAt: time.Now().UTC(),
	}
	if err := store.SaveRun(ctx, &base); err != nil {
		t.Fatal(err)
	}
	// 另一个 Run 用同一 key：必须被唯一约束拒绝
	dup := base
	dup.ID = "run_b"
	err := store.SaveRun(ctx, &dup)
	if err == nil {
		t.Fatal("同一 (workspace, idempotency_key) 的第二条 Run 应被拒绝")
	}
	if de := platform.AsDomainError(err); de.Code != platform.CodeConflict {
		t.Fatalf("期望 conflict，实际 %s", de.Code)
	}
	// 按 key 查应返回完整 Run（含步骤），而不是空壳
	found, err := store.FindByIdempotencyKey(ctx, "ws_1", "same-key")
	if err != nil {
		t.Fatal(err)
	}
	if found == nil || found.ID != "run_a" {
		t.Fatalf("按幂等键查询结果不正确: %+v", found)
	}
}

// 重启后可续跑：ListResumableRuns 必须捞出 running/pending 与未完成的异步任务。
func TestATK15ResumableRunsFromSQLStore(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	store := exec.NewSQLStore(db.DB, db.Dialect)

	running := &exec.Run{ID: "run_running", WorkspaceID: "ws_1", Trigger: "manual", Status: exec.RunRunning, StartedAt: time.Now().UTC()}
	step := &exec.Step{ID: "step_async", NodeID: "n_1", Kind: exec.StepGenerate, Status: exec.StepRunning, StartedAt: time.Now().UTC()}
	running.Steps = []*exec.Step{step}
	if err := store.SaveRun(ctx, running); err != nil {
		t.Fatal(err)
	}
	// 带远程异步任务（视频）的 attempt：即使 Run 已是 succeeded，也要能续查
	done := &exec.Run{ID: "run_done", WorkspaceID: "ws_1", Trigger: "manual", Status: exec.RunSucceeded, StartedAt: time.Now().UTC()}
	dstep := &exec.Step{ID: "step_video", NodeID: "n_2", Kind: exec.StepGenerate, Status: exec.StepRunning, StartedAt: time.Now().UTC()}
	done.Steps = []*exec.Step{dstep}
	if err := store.SaveRun(ctx, done); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAttempt(ctx, done.ID, dstep.ID, &exec.Attempt{
		Index: 0, ProviderID: "openai", ModelID: "veo", RequestID: "run_done:step_video",
		Status: exec.StepRunning, RemoteTask: &provider.RemoteTask{ID: "task_123", Provider: "openai"},
	}); err != nil {
		t.Fatal(err)
	}

	ids, err := store.ListResumableRuns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !containsStr(ids, "run_running") {
		t.Fatal("处于 running 的 Run 应可恢复")
	}
	if !containsStr(ids, "run_done") {
		t.Fatal("有未完成异步任务的 Run 应可恢复（进程重启后继续轮询）")
	}
}

// 步骤与 attempt 的往返读写必须保真（否则恢复后执行会丢失上下文）。
func TestSQLStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	store := exec.NewSQLStore(db.DB, db.Dialect)

	finished := time.Now().UTC().Truncate(time.Millisecond)
	run := &exec.Run{
		ID: "run_rt", WorkspaceID: "ws_1", CanvasID: "cv_1", Trigger: "manual",
		Status: exec.RunPartial, TargetNodes: []string{"n_1", "n_2"},
		Params:  map[string]any{"size": "1024x1024", "count": float64(2)},
		ActorID: "u_1", StartedAt: finished.Add(-time.Second), FinishedAt: &finished,
	}
	step := &exec.Step{
		ID: "step_rt", NodeID: "n_1", Kind: exec.StepGenerate, Status: exec.StepSucceeded,
		DependsOn: []string{"s0"},
		Outputs:   []exec.OutputAsset{{AssetID: "as_1", Kind: "image", MIME: "image/png"}},
		Text:      "hello", StartedAt: finished.Add(-time.Second), FinishedAt: &finished,
		Attempts: []exec.Attempt{{
			Index: 0, ProviderID: "openai", ModelID: "gpt-image-1", RequestID: "run_rt:step_rt",
			Status: exec.StepSucceeded, HTTPStatus: 200, Latency: 1200 * time.Millisecond,
			Usage: provider.Usage{Images: 2, CostMicros: 80_000, TextTokensIn: 10, TextTokensOut: 20},
		}},
	}
	run.Steps = []*exec.Step{step}
	if err := store.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}

	got, err := store.LoadRun(ctx, "run_rt")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != exec.RunPartial {
		t.Fatalf("状态丢失: %s", got.Status)
	}
	if len(got.TargetNodes) != 2 || got.TargetNodes[1] != "n_2" {
		t.Fatalf("目标节点丢失: %v", got.TargetNodes)
	}
	if got.Params["size"] != "1024x1024" {
		t.Fatalf("参数丢失: %v", got.Params)
	}
	if len(got.Steps) != 1 {
		t.Fatalf("步骤数不符: %d", len(got.Steps))
	}
	st := got.Steps[0]
	if st.Text != "hello" || len(st.Outputs) != 1 || st.Outputs[0].AssetID != "as_1" {
		t.Fatalf("步骤内容丢失: %+v", st)
	}
	if len(st.Attempts) != 1 || st.Attempts[0].Usage.CostMicros != 80_000 {
		t.Fatalf("attempt 计量丢失: %+v", st.Attempts)
	}
	if st.Attempts[0].Latency != 1200*time.Millisecond {
		t.Fatalf("耗时精度丢失: %v", st.Attempts[0].Latency)
	}
	// 聚合计量必须能从 attempts 重算出来（INV-9）
	if got.Usage.CostMicros != 80_000 || got.Usage.Images != 2 {
		t.Fatalf("聚合计量不符: %+v", got.Usage)
	}
}

// 列表接口按工作区分页，且不跨工作区泄漏。
func TestSQLStoreListIsolation(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	store := exec.NewSQLStore(db.DB, db.Dialect)

	for i, ws := range []string{"ws_a", "ws_a", "ws_b"} {
		r := &exec.Run{
			ID: "run_" + ws + itoa(i), WorkspaceID: ws, Trigger: "manual", Status: exec.RunSucceeded,
			StartedAt: time.Now().UTC().Add(time.Duration(i) * time.Second),
		}
		if err := store.SaveRun(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	got, _, err := store.ListRuns(ctx, "ws_a", "", 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("ws_a 应只有 2 个 Run，实际 %d", len(got))
	}
	for _, r := range got {
		if r.WorkspaceID != "ws_a" {
			t.Fatalf("列表泄漏了其他工作区的 Run: %s", r.WorkspaceID)
		}
	}
}

func newTestDB(t *testing.T) *platform.DB {
	t.Helper()
	cfg := platform.Defaults()
	cfg.DBDriver = "sqlite"
	cfg.DBDSN = "file:" + t.TempDir() + "/exec.db"
	cfg.AllowInsecureDevKey = true
	db, err := platform.OpenDB(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background(), migrations.FS, "."); err != nil {
		t.Fatal(err)
	}
	return db
}

func containsStr(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf []byte
	for v > 0 {
		buf = append([]byte{byte('0' + v%10)}, buf...)
		v /= 10
	}
	return string(buf)
}
