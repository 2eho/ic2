package exec

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/context-flow/ic/internal/platform"
	"github.com/context-flow/ic/internal/provider"
)

// SQLStore 是运行持久化的 SQL 实现。
//
// 为什么持久化是必须的（而不是「内存够用」）：
// docs/design/13 §4.2 的故障演练要求「Run 运行中 kill -9 服务端后重启，任务恢复或明确失败」。
// 内存实现做不到这一点——重启即丢失，用户会看到一个永远停在 running 的 Run。
//
// 写入策略：每次状态变化立即落库（Run/Step/Attempt 都是小对象），
// 不做写合并。理由是可观测性优先：崩溃后能看到的最后状态越新，用户越不容易误解。
type SQLStore struct {
	db      *sql.DB
	dialect string
}

// NewSQLStore 构造 SQL 存储。
func NewSQLStore(db *sql.DB, dialect string) *SQLStore {
	return &SQLStore{db: db, dialect: dialect}
}

// SaveRun 见 Store。
func (s *SQLStore) SaveRun(ctx context.Context, run *Run) error {
	if run == nil {
		return platform.ErrInvalid("run is nil")
	}
	targets, err := json.Marshal(orEmptySlice(run.TargetNodes))
	if err != nil {
		return platform.AsError(err)
	}
	params, err := json.Marshal(orEmptyMap(run.Params))
	if err != nil {
		return platform.AsError(err)
	}
	usage, err := json.Marshal(usageToMap(run.Usage))
	if err != nil {
		return platform.AsError(err)
	}
	var errJSON any
	if run.Error != nil {
		raw, _ := json.Marshal(run.Error)
		errJSON = string(raw)
	}
	var idem any
	if run.IdempotencyKey != "" {
		idem = run.IdempotencyKey
	}
	var finished any
	if run.FinishedAt != nil {
		finished = fmtTime(*run.FinishedAt)
	}

	// 幂等键唯一约束：用 UPSERT 让并发重复提交在 DB 层收敛（INV-2）。
	// SQLite 与 Postgres 的 UPSERT 语法一致（ON CONFLICT），因此这里不需要分支。
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO runs (id, workspace_id, canvas_id, trigger, status, targets, params, usage, error,
		                  actor_id, idempotency_key, started_at, finished_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
		  status = excluded.status,
		  targets = excluded.targets,
		  params = excluded.params,
		  usage = excluded.usage,
		  error = excluded.error,
		  finished_at = excluded.finished_at`,
		run.ID, run.WorkspaceID, run.CanvasID, run.Trigger, string(run.Status),
		string(targets), string(params), string(usage), errJSON,
		run.ActorID, idem, fmtTime(run.StartedAt), finished)
	if err != nil {
		if isUniqueViolation(err) && run.IdempotencyKey != "" {
			return platform.NewError(409, platform.CodeConflict, "duplicate idempotency key")
		}
		return platform.AsError(err)
	}
	// 步骤随 Run 一起落库：避免「Run 状态新但步骤缺失」的中间态。
	for _, st := range run.Steps {
		if err := s.SaveStep(ctx, run.ID, st); err != nil {
			return err
		}
	}
	return nil
}

// LoadRun 见 Store。
func (s *SQLStore) LoadRun(ctx context.Context, runID string) (*Run, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, workspace_id, canvas_id, trigger, status, targets, params, usage, error,
		       actor_id, COALESCE(idempotency_key, ''), started_at, finished_at
		FROM runs WHERE id = ?`, runID)
	run, err := scanRun(row.Scan)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, platform.ErrNotFound("run")
		}
		return nil, platform.AsError(err)
	}
	steps, err := s.loadSteps(ctx, runID)
	if err != nil {
		return nil, err
	}
	run.Steps = steps
	run.recomputeUsage()
	return run, nil
}

// FindByIdempotencyKey 见 Store。
func (s *SQLStore) FindByIdempotencyKey(ctx context.Context, wsID, key string) (*Run, error) {
	if key == "" {
		return nil, nil
	}
	var id string
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM runs WHERE workspace_id = ? AND idempotency_key = ?`, wsID, key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, platform.AsError(err)
	}
	// 幂等重放必须返回**完整**的 Run（含当前步骤状态），否则前端会看到一个空壳。
	return s.LoadRun(ctx, id)
}

// ListRuns 见 Store。
func (s *SQLStore) ListRuns(ctx context.Context, wsID, canvasID string, limit int, cursor string) ([]*Run, string, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	args := []any{wsID}
	where := "workspace_id = ?"
	if canvasID != "" {
		where += " AND canvas_id = ?"
		args = append(args, canvasID)
	}
	if cursor != "" {
		where += " AND started_at < ?"
		args = append(args, cursor)
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, workspace_id, canvas_id, trigger, status, targets, params, usage, error,
		       actor_id, COALESCE(idempotency_key, ''), started_at, finished_at
		FROM runs WHERE `+where+` ORDER BY started_at DESC, id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, "", platform.AsError(err)
	}
	defer rows.Close()
	out := make([]*Run, 0, limit)
	for rows.Next() {
		run, err := scanRun(rows.Scan)
		if err != nil {
			return nil, "", platform.AsError(err)
		}
		out = append(out, run)
	}
	if err := rows.Err(); err != nil {
		return nil, "", platform.AsError(err)
	}
	var next string
	if len(out) > limit {
		next = fmtTime(out[limit-1].StartedAt)
		out = out[:limit]
	}
	// 列表接口的步骤按需懒加载：列表视图不需要 attempt 明细，避免 N+1 查询。
	return out, next, nil
}

// SaveStep 见 Store。
func (s *SQLStore) SaveStep(ctx context.Context, runID string, step *Step) error {
	if step == nil {
		return nil
	}
	depends, _ := json.Marshal(orEmptySlice(step.DependsOn))
	outputs, _ := json.Marshal(outputsToMaps(step.Outputs))
	var errJSON any
	if step.Error != nil {
		raw, _ := json.Marshal(step.Error)
		errJSON = string(raw)
	}
	var finished any
	if step.FinishedAt != nil {
		finished = fmtTime(*step.FinishedAt)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO run_steps (id, run_id, node_id, kind, status, depends_on, outputs, text, error, started_at, finished_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
		  status = excluded.status,
		  outputs = excluded.outputs,
		  text = excluded.text,
		  error = excluded.error,
		  finished_at = excluded.finished_at`,
		step.ID, runID, step.NodeID, string(step.Kind), string(step.Status),
		string(depends), string(outputs), step.Text, errJSON, fmtTime(step.StartedAt), finished)
	if err != nil {
		return platform.AsError(err)
	}
	for i := range step.Attempts {
		if err := s.SaveAttempt(ctx, runID, step.ID, &step.Attempts[i]); err != nil {
			return err
		}
	}
	return nil
}

// SaveAttempt 见 Store。request_id 唯一约束保证「同一 request_id 上游只计费一次」（INV-3）。
func (s *SQLStore) SaveAttempt(ctx context.Context, _ string, stepID string, a *Attempt) error {
	if a == nil {
		return nil
	}
	var errJSON any
	if a.Error != nil {
		raw, _ := json.Marshal(a.Error)
		errJSON = string(raw)
	}
	var remoteTask, remoteProvider any
	if a.RemoteTask != nil {
		remoteTask = a.RemoteTask.ID
		remoteProvider = a.RemoteTask.Provider
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO run_attempts (id, step_id, idx, provider_id, model_id, request_id, status, http_status,
		                          latency_ms, tokens_in, tokens_out, images, video_millis, audio_millis,
		                          cost_micros, remote_task_id, remote_provider, error, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(request_id) DO UPDATE SET
		  status = excluded.status,
		  http_status = excluded.http_status,
		  latency_ms = excluded.latency_ms,
		  tokens_in = excluded.tokens_in,
		  tokens_out = excluded.tokens_out,
		  images = excluded.images,
		  video_millis = excluded.video_millis,
		  audio_millis = excluded.audio_millis,
		  cost_micros = excluded.cost_micros,
		  remote_task_id = excluded.remote_task_id,
		  remote_provider = excluded.remote_provider,
		  error = excluded.error`,
		attemptID(stepID, a.Index), stepID, a.Index, a.ProviderID, a.ModelID, a.RequestID, string(a.Status),
		a.HTTPStatus, a.Latency.Milliseconds(), a.Usage.TextTokensIn, a.Usage.TextTokensOut,
		a.Usage.Images, a.Usage.VideoMillis, a.Usage.AudioMillis,
		a.Usage.CostMicros, remoteTask, remoteProvider, errJSON, fmtTime(time.Now().UTC()))
	if err != nil {
		return platform.AsError(err)
	}
	return nil
}

// ListResumableRuns 见 Store：返回有未完成异步任务或处于 running 的运行。
func (s *SQLStore) ListResumableRuns(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT r.id FROM runs r
		LEFT JOIN run_steps st ON st.run_id = r.id
		LEFT JOIN run_attempts a ON a.step_id = st.id
		WHERE r.status IN ('pending', 'running')
		   OR (a.remote_task_id IS NOT NULL AND a.status = 'running')
		ORDER BY r.started_at`)
	if err != nil {
		return nil, platform.AsError(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, platform.AsError(err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *SQLStore) loadSteps(ctx context.Context, runID string) ([]*Step, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, node_id, kind, status, depends_on, outputs, text, error, started_at, finished_at
		FROM run_steps WHERE run_id = ? ORDER BY started_at, id`, runID)
	if err != nil {
		return nil, platform.AsError(err)
	}
	defer rows.Close()
	var steps []*Step
	byID := map[string]*Step{}
	order := []string{}
	for rows.Next() {
		var (
			id, nodeID, kind, status, dependsRaw, outputsRaw, text string
			errRaw                                                 sql.NullString
			startedAt                                              string
			finishedAt                                             sql.NullString
		)
		if err := rows.Scan(&id, &nodeID, &kind, &status, &dependsRaw, &outputsRaw, &text, &errRaw, &startedAt, &finishedAt); err != nil {
			return nil, platform.AsError(err)
		}
		st := &Step{
			ID: id, NodeID: nodeID, Kind: StepKind(kind), Status: StepStatus(status),
			Text: text, StartedAt: parseTime(startedAt),
		}
		_ = json.Unmarshal([]byte(dependsRaw), &st.DependsOn)
		var outs []map[string]string
		_ = json.Unmarshal([]byte(outputsRaw), &outs)
		for _, o := range outs {
			st.Outputs = append(st.Outputs, OutputAsset{AssetID: o["assetId"], Kind: o["kind"], MIME: o["mime"]})
		}
		if errRaw.Valid && errRaw.String != "" {
			var pe provider.ProviderError
			if json.Unmarshal([]byte(errRaw.String), &pe) == nil {
				st.Error = &pe
			}
		}
		if finishedAt.Valid {
			t := parseTime(finishedAt.String)
			st.FinishedAt = &t
		}
		byID[id] = st
		order = append(order, id)
	}
	if err := rows.Err(); err != nil {
		return nil, platform.AsError(err)
	}
	// 尝试明细单独加载（避免 JOIN 造成步骤行重复）
	for _, id := range order {
		attempts, err := s.loadAttempts(ctx, id)
		if err != nil {
			return nil, err
		}
		byID[id].Attempts = attempts
	}
	for _, id := range order {
		steps = append(steps, byID[id])
	}
	return steps, nil
}

func (s *SQLStore) loadAttempts(ctx context.Context, stepID string) ([]Attempt, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT idx, provider_id, model_id, request_id, status, http_status, latency_ms,
		       tokens_in, tokens_out, images, video_millis, audio_millis,
		       cost_micros, remote_task_id, remote_provider, error
		FROM run_attempts WHERE step_id = ? ORDER BY idx`, stepID)
	if err != nil {
		return nil, platform.AsError(err)
	}
	defer rows.Close()
	var out []Attempt
	for rows.Next() {
		var (
			a                                      Attempt
			status, providerID, modelID, requestID string
			remoteTask, remoteProvider, errRaw     sql.NullString
		)
		var latencyMs int64
		if err := rows.Scan(&a.Index, &providerID, &modelID, &requestID, &status, &a.HTTPStatus, &latencyMs,
			&a.Usage.TextTokensIn, &a.Usage.TextTokensOut, &a.Usage.Images, &a.Usage.VideoMillis, &a.Usage.AudioMillis,
			&a.Usage.CostMicros, &remoteTask, &remoteProvider, &errRaw); err != nil {
			return nil, platform.AsError(err)
		}
		a.ProviderID, a.ModelID, a.RequestID, a.Status = providerID, modelID, requestID, StepStatus(status)
		a.Latency = time.Duration(latencyMs) * time.Millisecond
		if remoteTask.Valid {
			a.RemoteTask = &provider.RemoteTask{ID: remoteTask.String, Provider: remoteProvider.String}
		}
		if errRaw.Valid && errRaw.String != "" {
			var pe provider.ProviderError
			if json.Unmarshal([]byte(errRaw.String), &pe) == nil {
				a.Error = &pe
			}
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// scanRun 从一行扫描 Run（列顺序与各查询保持一致，集中在此处便于同时改）。
func scanRun(scan func(...any) error) (*Run, error) {
	var (
		run                            Run
		status, targets, params, usage string
		canvasID, trigger, actorID     string
		startedAt, idemKey             string
		errRaw, finishedAt             sql.NullString
	)
	if err := scan(&run.ID, &run.WorkspaceID, &canvasID, &trigger, &status, &targets, &params, &usage, &errRaw,
		&actorID, &idemKey, &startedAt, &finishedAt); err != nil {
		return nil, err
	}
	run.CanvasID, run.Trigger, run.Status = canvasID, trigger, RunStatus(status)
	run.ActorID, run.IdempotencyKey = actorID, idemKey
	_ = json.Unmarshal([]byte(targets), &run.TargetNodes)
	_ = json.Unmarshal([]byte(params), &run.Params)
	applyUsageMap(&run.Usage, usage)
	if errRaw.Valid && errRaw.String != "" {
		var pe provider.ProviderError
		if json.Unmarshal([]byte(errRaw.String), &pe) == nil {
			run.Error = &pe
		}
	}
	run.StartedAt = parseTime(startedAt)
	if finishedAt.Valid && finishedAt.String != "" {
		t := parseTime(finishedAt.String)
		run.FinishedAt = &t
	}
	return &run, nil
}

func attemptID(stepID string, idx int) string { return fmt.Sprintf("%s#%d", stepID, idx) }

func fmtTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func orEmptySlice(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func orEmptyMap(v map[string]any) map[string]any {
	if v == nil {
		return map[string]any{}
	}
	return v
}

func outputsToMaps(outs []OutputAsset) []map[string]string {
	out := make([]map[string]string, 0, len(outs))
	for _, o := range outs {
		out = append(out, map[string]string{"assetId": o.AssetID, "kind": o.Kind, "mime": o.MIME})
	}
	return out
}

func usageToMap(u provider.Usage) map[string]int64 {
	return map[string]int64{
		"textTokensIn": u.TextTokensIn, "textTokensOut": u.TextTokensOut,
		"images": u.Images, "videoMillis": u.VideoMillis, "audioMillis": u.AudioMillis,
		"costMicros": u.CostMicros,
	}
}

func applyUsageMap(u *provider.Usage, raw string) {
	if raw == "" {
		return
	}
	var m map[string]int64
	if json.Unmarshal([]byte(raw), &m) != nil {
		return
	}
	u.TextTokensIn = m["textTokensIn"]
	u.TextTokensOut = m["textTokensOut"]
	u.Images = m["images"]
	u.VideoMillis = m["videoMillis"]
	u.AudioMillis = m["audioMillis"]
	u.CostMicros = m["costMicros"]
}

// isUniqueViolation 判定唯一约束冲突。SQLite 与 Postgres 的文案不同，
// 但都包含 "unique"，够用且不需要引入驱动特定错误类型。
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "unique")
}
