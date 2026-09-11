package exec_test

import (
	"context"
	"testing"
	"time"

	"github.com/context-flow/ic/internal/exec"
	"github.com/context-flow/ic/internal/platform"
)

// fakeQuotaStore 是确定性替身：解耦清单要求限额存储有 ≥2 个实现。
type fakeQuotaStore struct {
	q     exec.Quota
	usage map[string]int64 // "2026-03-01" → micros
	calls []string
}

func (f *fakeQuotaStore) Quota(context.Context, string) (exec.Quota, error) { return f.q, nil }

func (f *fakeQuotaStore) UsageSince(_ context.Context, _ string, since, until time.Time) (int64, error) {
	// 用「自然日数量」做粗粒度匹配：测试只关心窗口边界是否正确，
	// 因此把用量挂在「窗口起始日的本地日期」上。
	key := since.UTC().Format("2006-01-02")
	f.calls = append(f.calls, key+"→"+until.UTC().Format("2006-01-02"))
	return f.usage[key], nil
}

// ATK-17：日限额边界——跨时区跨日不得重复也不得跳过。
//
// 这条用例的价值在于把一个模糊需求（"按天限流"）变成可证伪命题：
// 给定同一 UTC 瞬时、不同工作区时区，窗口边界必须落在各自的本地零点。
func TestATK17DailyQuotaRespectsWorkspaceTimezone(t *testing.T) {
	cases := []struct {
		name     string
		tz       string
		now      string // RFC3339 UTC
		wantFrom string // 期望的窗口起点（UTC）
		wantTo   string // 期望的窗口终点（UTC）
	}{
		{
			name: "上海 UTC+8：UTC 16:00 已跨到次日",
			tz:   "Asia/Shanghai",
			// UTC 2026-03-10T16:30Z == 上海 2026-03-11 00:30
			now:      "2026-03-10T16:30:00Z",
			wantFrom: "2026-03-10T16:00:00Z",
			wantTo:   "2026-03-11T16:00:00Z",
		},
		{
			name: "上海 UTC+8：UTC 15:59 仍属当日",
			tz:   "Asia/Shanghai",
			now:  "2026-03-10T15:59:59Z",
			// 上海的 2026-03-10 23:59:59 → 窗口 [03-09T16:00Z, 03-10T16:00Z)
			wantFrom: "2026-03-09T16:00:00Z",
			wantTo:   "2026-03-10T16:00:00Z",
		},
		{
			name:     "UTC：窗口就是 UTC 自然日",
			tz:       "UTC",
			now:      "2026-03-10T00:00:01Z",
			wantFrom: "2026-03-10T00:00:00Z",
			wantTo:   "2026-03-11T00:00:00Z",
		},
		{
			name: "洛杉矶 DST 切换日（2026-03-08 起用 PDT，UTC-7）",
			tz:   "America/Los_Angeles",
			now:  "2026-03-08T12:00:00Z",
			// 当天本地 00:00 是 PST(UTC-8) → 08:00Z；次日本地 00:00 是 PDT(UTC-7) → 07:00Z
			// 用「构造本地零点」而不是「减 24 小时」才能得到正确结果
			wantFrom: "2026-03-08T08:00:00Z",
			wantTo:   "2026-03-09T07:00:00Z",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			now, err := time.Parse(time.RFC3339, c.now)
			if err != nil {
				t.Fatal(err)
			}
			loc, err := time.LoadLocation(c.tz)
			if err != nil {
				t.Skipf("时区数据不可用: %v", err)
			}
			from, to := exec.DayBounds(now, loc)
			if got := from.Format(time.RFC3339); got != c.wantFrom {
				t.Fatalf("窗口起点错误: got %s want %s", got, c.wantFrom)
			}
			if got := to.Format(time.RFC3339); got != c.wantTo {
				t.Fatalf("窗口终点错误: got %s want %s", got, c.wantTo)
			}
			if !to.After(from) {
				t.Fatal("窗口终点必须晚于起点")
			}
			// 窗口必须包含 now
			if now.Before(from) || !now.Before(to) {
				t.Fatalf("now %s 不在窗口 [%s, %s) 内", now, from, to)
			}
		})
	}
}

// 跨月与跨年边界。
func TestATK17MonthBoundsAcrossYearBoundary(t *testing.T) {
	loc := time.UTC
	from, to := exec.MonthBounds(time.Date(2026, 12, 31, 23, 30, 0, 0, time.UTC), loc)
	if from.Format(time.RFC3339) != "2026-12-01T00:00:00Z" {
		t.Fatalf("12 月起止错误: %s", from)
	}
	if to.Format(time.RFC3339) != "2027-01-01T00:00:00Z" {
		t.Fatalf("跨年终点错误: %s", to)
	}
	// 2 月（非闰年）
	f2, t2 := exec.MonthBounds(time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC), loc)
	if f2.Format("2006-01-02") != "2026-02-01" || t2.Format("2006-01-02") != "2026-03-01" {
		t.Fatalf("2 月边界错误: %s → %s", f2, t2)
	}
	// 闰年 2 月
	f3, t3 := exec.MonthBounds(time.Date(2028, 2, 15, 0, 0, 0, 0, time.UTC), loc)
	if t3.Sub(f3) != 29*24*time.Hour {
		t.Fatalf("闰年 2 月应为 29 天，实际 %v", t3.Sub(f3))
	}
}

// 额度判定：不超限放行、超限拒绝且给出恢复时间与剩余额度。
func TestATK17QuotaDecision(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	dayStart, _ := exec.DayBounds(now, time.UTC)
	key := dayStart.Format("2006-01-02")

	store := &fakeQuotaStore{
		q:     exec.Quota{DailyMicros: 1_000_000, MonthlyMicros: 10_000_000, Timezone: "UTC"},
		usage: map[string]int64{key: 900_000},
	}

	// 未超限：900k + 50k ≤ 1M
	dec, err := exec.CheckQuota(ctx, store, "ws_1", 50_000, now)
	if err != nil {
		t.Fatal(err)
	}
	if !dec.Allowed {
		t.Fatalf("未超限却被拒绝: %+v", dec)
	}
	if dec.RemainingMicros != 100_000 {
		t.Fatalf("剩余额度错误: %d", dec.RemainingMicros)
	}

	// 超限：900k + 200k > 1M
	dec2, err := exec.CheckQuota(ctx, store, "ws_1", 200_000, now)
	if err != nil {
		t.Fatal(err)
	}
	if dec2.Allowed {
		t.Fatal("超限应被拒绝")
	}
	if dec2.Reason != platform.CodeQuotaExceeded {
		t.Fatalf("期望 code=%s，实际 %s", platform.CodeQuotaExceeded, dec2.Reason)
	}
	if dec2.Scope != "daily" {
		t.Fatalf("期望触发 daily 限额，实际 %s", dec2.Scope)
	}
	if dec2.WindowEnd.IsZero() {
		t.Fatal("拒绝时必须给出窗口恢复时间（否则用户无法预期何时可再生成）")
	}

	// 恰好等于额度：应放行（判据是 > 而不是 >=）
	dec3, _ := exec.CheckQuota(ctx, store, "ws_1", 100_000, now)
	if !dec3.Allowed {
		t.Fatal("恰好用满额度应放行（判据为严格大于）")
	}
}

// 单次上限与不限额度。
func TestATK17PerRunAndUnlimited(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()

	// 单次上限
	store := &fakeQuotaStore{q: exec.Quota{PerRunMicros: 1000, Timezone: "UTC"}, usage: map[string]int64{}}
	dec, _ := exec.CheckQuota(ctx, store, "ws", 1001, now)
	if dec.Allowed || dec.Scope != "per_run" {
		t.Fatalf("单次超限应被拒绝且 scope=per_run，实际 %+v", dec)
	}

	// 全部为 0 = 不限
	unlimited := &fakeQuotaStore{q: exec.Quota{Timezone: "UTC"}, usage: map[string]int64{}}
	dec2, _ := exec.CheckQuota(ctx, unlimited, "ws", 1<<40, now)
	if !dec2.Allowed {
		t.Fatal("额度为 0 表示不限，不应拒绝")
	}
	if dec2.RemainingMicros != -1 {
		t.Fatalf("不限时剩余额度应为 -1，实际 %d", dec2.RemainingMicros)
	}
}

// 非法时区名必须降级为 UTC 而不是报错：限额是保护性能力，
// 一个拼错的时区名不应让整个生成链路不可用。
func TestATK17InvalidTimezoneFallsBackToUTC(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	store := &fakeQuotaStore{
		q:     exec.Quota{DailyMicros: 100, Timezone: "Not/AZone"},
		usage: map[string]int64{},
	}
	dec, err := exec.CheckQuota(ctx, store, "ws", 10, now)
	if err != nil {
		t.Fatalf("非法时区不应导致错误: %v", err)
	}
	if !dec.Allowed {
		t.Fatal("非法时区下降级为 UTC，未超限应放行")
	}
	// 窗口必须仍是合法的 UTC 自然日
	want := time.Date(2026, 3, 11, 0, 0, 0, 0, time.UTC)
	if !dec.WindowEnd.Equal(want) {
		t.Fatalf("降级后窗口终点应为 %s，实际 %s", want, dec.WindowEnd)
	}
}
