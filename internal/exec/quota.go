package exec

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/context-flow/ic/internal/platform"
)

// 限额与用量（docs/design/05 §4）。
//
// 原项目因为前端直连上游，**无法做任何限额与风控**——这是重写要拿回来的能力之一。
//
// 时区语义（ATK-17）是本文件的重点：
//   - 「日限额」的「日」必须有一个明确定义的边界，否则跨时区/跨夏令时会重复或跳过；
//   - 这里统一用「工作区配置的时区」在 UTC 时间轴上算出自然日区间 [start, end)，
//     落库与比较都用 UTC 瞬时，从而不受服务器本地时区与 DST 影响；
//   - 判据是「该自然日内的累计成本 + 本次预估 ≤ 日额度」，
//     而不是「按 24 小时滚动窗口」——后者会让用户在额度恢复时间上无法预期。
//
// 边界定义集中在 DayBounds，并被测试穷举（含跨月、跨年、DST 切换日）。

// Quota 是一个工作区的额度配置。
type Quota struct {
	WorkspaceID string
	// DailyMicros 日额度（微元），0 表示不限。
	DailyMicros int64
	// MonthlyMicros 月额度，0 表示不限。
	MonthlyMicros int64
	// PerRunMicros 单次运行上限，0 表示不限。
	PerRunMicros int64
	// Timezone 用于计算自然日/月的时区名（IANA，例如 Asia/Shanghai）。
	Timezone string
}

// QuotaStore 持久化额度与用量。
type QuotaStore interface {
	Quota(ctx context.Context, wsID string) (Quota, error)
	// UsageSince 返回 [since, until) 区间内已入账的成本（微元）。
	UsageSince(ctx context.Context, wsID string, since, until time.Time) (int64, error)
}

// SQLQuotaStore 是 QuotaStore 的 SQL 实现：额度来自配置表，用量从 runs 聚合。
type SQLQuotaStore struct {
	db *sql.DB
	// defaults 是未显式配置工作区时的默认额度（0 = 不限）。
	defaults Quota
}

// NewSQLQuotaStore 构造限额存储。
func NewSQLQuotaStore(db *sql.DB, defaults Quota) *SQLQuotaStore {
	return &SQLQuotaStore{db: db, defaults: defaults}
}

// Quota 见 QuotaStore。
func (s *SQLQuotaStore) Quota(ctx context.Context, wsID string) (Quota, error) {
	q := s.defaults
	q.WorkspaceID = wsID
	if q.Timezone == "" {
		q.Timezone = "UTC"
	}
	if s.db == nil {
		return q, nil
	}
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(settings, '{}') FROM workspaces WHERE id = ?`, wsID).Scan(&raw)
	if err != nil {
		if err == sql.ErrNoRows {
			return q, nil
		}
		return q, platform.AsError(err)
	}
	applyQuotaSettings(&q, raw)
	if q.Timezone == "" {
		q.Timezone = "UTC"
	}
	return q, nil
}

// UsageSince 见 QuotaStore。
//
// 只统计**已入账**的运行（succeeded/partial）：失败或取消的运行不计费——
// 这与上游「失败不扣费」的普遍语义一致，也避免用户因为一次网络抖动而损失额度。
func (s *SQLQuotaStore) UsageSince(ctx context.Context, wsID string, since, until time.Time) (int64, error) {
	if s.db == nil {
		return 0, nil
	}
	var total sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT SUM(CAST(COALESCE(json_extract(usage, '$.costMicros'), 0) AS INTEGER))
		FROM runs
		WHERE workspace_id = ? AND status IN ('succeeded', 'partial')
		  AND started_at >= ? AND started_at < ?`,
		wsID, since.UTC().Format(time.RFC3339Nano), until.UTC().Format(time.RFC3339Nano)).Scan(&total)
	if err != nil {
		return 0, platform.AsError(err)
	}
	if !total.Valid {
		return 0, nil
	}
	return total.Int64, nil
}

// QuotaDecision 是限额判定结果。
type QuotaDecision struct {
	Allowed bool
	// Reason 是拒绝原因（稳定 code）。
	Reason string
	// RemainingMicros 剩余额度（不限时为 -1）。
	RemainingMicros int64
	// WindowEnd 当前窗口结束时刻（用于提示「何时恢复」）。
	WindowEnd time.Time
	// Scope 是被触发的限额范围：daily | monthly | per_run。
	Scope string
}

// CheckQuota 判定一次预估成本是否被允许（ATK-17）。
//
// 调用时机在**派发上游之前**：超限必须在上游被调用前拒绝，
// 否则「限额」只是事后告知，用户照样被扣费。
func CheckQuota(ctx context.Context, store QuotaStore, wsID string, estimatedMicros int64, now time.Time) (QuotaDecision, error) {
	q, err := store.Quota(ctx, wsID)
	if err != nil {
		return QuotaDecision{}, err
	}
	loc, err := time.LoadLocation(q.Timezone)
	if err != nil {
		// 时区名非法时退回 UTC 而不是报错：限额是保护性能力，
		// 一个拼错的时区名不应该让整个生成链路不可用。
		loc = time.UTC
	}

	if q.PerRunMicros > 0 && estimatedMicros > q.PerRunMicros {
		return QuotaDecision{
			Allowed: false, Reason: platform.CodeQuotaExceeded, Scope: "per_run",
			RemainingMicros: q.PerRunMicros - estimatedMicros,
		}, nil
	}

	dayStart, dayEnd := DayBounds(now, loc)
	if q.DailyMicros > 0 {
		used, err := store.UsageSince(ctx, wsID, dayStart, dayEnd)
		if err != nil {
			return QuotaDecision{}, err
		}
		if used+estimatedMicros > q.DailyMicros {
			return QuotaDecision{
				Allowed: false, Reason: platform.CodeQuotaExceeded, Scope: "daily",
				RemainingMicros: q.DailyMicros - used, WindowEnd: dayEnd,
			}, nil
		}
	}

	monthStart, monthEnd := MonthBounds(now, loc)
	if q.MonthlyMicros > 0 {
		used, err := store.UsageSince(ctx, wsID, monthStart, monthEnd)
		if err != nil {
			return QuotaDecision{}, err
		}
		if used+estimatedMicros > q.MonthlyMicros {
			return QuotaDecision{
				Allowed: false, Reason: platform.CodeQuotaExceeded, Scope: "monthly",
				RemainingMicros: q.MonthlyMicros - used, WindowEnd: monthEnd,
			}, nil
		}
	}

	remaining := int64(-1)
	if q.DailyMicros > 0 {
		used, _ := store.UsageSince(ctx, wsID, dayStart, dayEnd)
		remaining = q.DailyMicros - used
	}
	return QuotaDecision{Allowed: true, RemainingMicros: remaining, WindowEnd: dayEnd}, nil
}

// DayBounds 返回 loc 时区下 now 所在自然日的 [start, end)（UTC 瞬时）。
//
// 用「构造本地零点再转 UTC」而不是「用 now 减小时数」：
// 后者在 DST 切换日会算错（那天只有 23 或 25 小时）。
func DayBounds(now time.Time, loc *time.Location) (time.Time, time.Time) {
	if loc == nil {
		loc = time.UTC
	}
	local := now.In(loc)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	return start.UTC(), start.AddDate(0, 0, 1).UTC()
}

// MonthBounds 返回 loc 时区下 now 所在自然月的 [start, end)（UTC 瞬时）。
// AddDate(0, 1, 0) 会自动处理 28/29/30/31 天的月份，不需要逐个判断。
func MonthBounds(now time.Time, loc *time.Location) (time.Time, time.Time) {
	if loc == nil {
		loc = time.UTC
	}
	local := now.In(loc)
	start := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, loc)
	return start.UTC(), start.AddDate(0, 1, 0).UTC()
}

// applyQuotaSettings 从工作区 settings JSON 中读取额度字段。
// 解析失败时保持默认值（不因为一段坏 JSON 让整个工作区不可用）。
func applyQuotaSettings(q *Quota, raw string) {
	type settings struct {
		Quota *struct {
			DailyMicros   *int64  `json:"dailyMicros"`
			MonthlyMicros *int64  `json:"monthlyMicros"`
			PerRunMicros  *int64  `json:"perRunMicros"`
			Timezone      *string `json:"timezone"`
		} `json:"quota"`
	}
	var s settings
	if jsonUnmarshal(raw, &s) != nil || s.Quota == nil {
		return
	}
	if s.Quota.DailyMicros != nil {
		q.DailyMicros = *s.Quota.DailyMicros
	}
	if s.Quota.MonthlyMicros != nil {
		q.MonthlyMicros = *s.Quota.MonthlyMicros
	}
	if s.Quota.PerRunMicros != nil {
		q.PerRunMicros = *s.Quota.PerRunMicros
	}
	if s.Quota.Timezone != nil && *s.Quota.Timezone != "" {
		q.Timezone = *s.Quota.Timezone
	}
}

// FormatWindowEnd 生成「额度何时恢复」的可读文案（UTC ISO8601，前端负责本地化）。
func FormatWindowEnd(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

var _ = fmt.Sprintf
