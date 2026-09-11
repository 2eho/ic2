// Package prompt 实现服务端提示词库：来源管理、拉取归一化、全文检索。
//
// 与原项目的关键差异（DIV-01）：原项目在浏览器里直连 7 个 GitHub raw 地址并缓存到
// IndexedDB，这带来三个问题——每个用户的浏览器都要发起外网请求、可用性依赖上游 CORS、
// 离线用户拿不到内容。重写后统一由服务端拉取与存储，浏览器只查服务端。
package prompt

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/context-flow/ic/internal/api"
	"github.com/context-flow/ic/internal/platform"
)

// 边界：单个来源的条目上限与单条提示词长度，避免一个畸形来源把库撑爆。
const (
	MaxPromptsPerSync    = 20_000
	MaxPromptContentLen  = 32 * 1024
	MaxPromptTagCount    = 32
	MaxPromptSearchLimit = 200
)

// Options 构造参数。
type Options struct {
	DB     *sql.DB
	Guard  *platform.NetGuard
	Client platform.HTTPDoer
	Clock  platform.Clock
	IDs    platform.IDGen
}

// Service 实现 api.PromptService。
type Service struct {
	db     *sql.DB
	guard  *platform.NetGuard
	client platform.HTTPDoer
	clock  platform.Clock
	ids    platform.IDGen
}

// New 构造提示词服务。
func New(o Options) *Service {
	if o.Clock == nil {
		o.Clock = platform.SystemClock()
	}
	if o.IDs == nil {
		o.IDs = platform.DefaultIDGen()
	}
	if o.Guard == nil {
		o.Guard = platform.NewNetGuard()
	}
	return &Service{db: o.DB, guard: o.Guard, client: o.Client, clock: o.Clock, ids: o.IDs}
}

// ListSources 见 api.PromptService。
func (s *Service) ListSources(ctx context.Context, wsID string) ([]api.PromptSourceDTO, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, url, format, refresh_interval, enabled, status, last_synced_at,
		       (SELECT COUNT(*) FROM prompts p WHERE p.source_id = ps.id) AS cnt
		FROM prompt_sources ps WHERE workspace_id = ? ORDER BY created_at`, wsID)
	if err != nil {
		return nil, platform.AsError(err)
	}
	defer rows.Close()
	out := []api.PromptSourceDTO{}
	for rows.Next() {
		var (
			id, name, url, format, interval, status string
			enabled                                 int
			lastSynced                              sql.NullString
			count                                   int
		)
		if err := rows.Scan(&id, &name, &url, &format, &interval, &enabled, &status, &lastSynced, &count); err != nil {
			return nil, platform.AsError(err)
		}
		dto := api.PromptSourceDTO{
			ID: id, Name: name, URL: url, Format: format, RefreshInterval: interval,
			Enabled: enabled == 1, Status: status, Count: count,
		}
		if lastSynced.Valid && lastSynced.String != "" {
			t := parseTime(lastSynced.String)
			dto.LastSyncedAt = &t
		}
		out = append(out, dto)
	}
	return out, rows.Err()
}

// CreateSource 见 api.PromptService。
func (s *Service) CreateSource(ctx context.Context, wsID string, in api.PromptSourceInput) (*api.PromptSourceDTO, error) {
	if wsID == "" {
		return nil, platform.ErrInvalid("workspaceId is required")
	}
	if strings.TrimSpace(in.URL) == "" {
		return nil, platform.ErrInvalid("url is required")
	}
	// 建来源时先校验 URL：把「配了一个内网地址」这种错误在保存时就挡住，
	// 而不是等定时任务执行时才发现（那时用户已经离开配置页了）。
	if err := s.guard.CheckURL(ctx, in.URL); err != nil {
		return nil, err
	}
	format := in.Format
	if format == "" {
		format = "auto"
	}
	interval := in.RefreshInterval
	if interval == "" {
		interval = "6h"
	}
	enabled := 1
	if in.Enabled != nil && !*in.Enabled {
		enabled = 0
	}
	id := s.ids.NewID("psrc")
	now := fmtTime(s.clock.Now())
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO prompt_sources (id, workspace_id, name, url, format, refresh_interval, enabled, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'idle', ?)`,
		id, wsID, firstNonEmpty(in.Name, in.URL), in.URL, format, interval, enabled, now); err != nil {
		return nil, platform.AsError(err)
	}
	return &api.PromptSourceDTO{
		ID: id, Name: firstNonEmpty(in.Name, in.URL), URL: in.URL, Format: format,
		RefreshInterval: interval, Enabled: enabled == 1, Status: "idle",
	}, nil
}

// SyncSource 见 api.PromptService：拉取 → 归一化 → upsert → 记账。
//
// 失败语义（对齐 docs/design/10 §8.10）：**失败保留旧缓存**。
// 网络抖动不应该让用户突然搜不到任何提示词。
func (s *Service) SyncSource(ctx context.Context, wsID, sourceID string) (*api.SyncResultDTO, error) {
	var name, url, format string
	if err := s.db.QueryRowContext(ctx,
		`SELECT name, url, format FROM prompt_sources WHERE id = ? AND workspace_id = ?`,
		sourceID, wsID).Scan(&name, &url, &format); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, platform.ErrNotFound("prompt source")
		}
		return nil, platform.AsError(err)
	}
	_ = name

	items, err := s.fetch(ctx, url, format)
	if err != nil {
		// 记录失败原因（脱敏后）但**不删除**已有条目。
		_, _ = s.db.ExecContext(ctx,
			`UPDATE prompt_sources SET status = 'error', last_error = ? WHERE id = ?`,
			platform.Redact(err.Error()), sourceID)
		return &api.SyncResultDTO{
			SourceID: sourceID, Status: "error",
			Error: &api.ErrDTO{Code: codeOf(err), Message: platform.Redact(err.Error())},
		}, nil
	}

	added, updated, total := 0, 0, 0
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		if item.ExternalID == "" || item.Content == "" {
			continue // 缺 id 或内容的条目直接跳过：无法建立稳定引用
		}
		if seen[item.ExternalID] {
			continue
		}
		seen[item.ExternalID] = true
		content := truncateBytes(item.Content, MaxPromptContentLen)
		hash := hashOf(content)
		var existingID, existingHash string
		err := s.db.QueryRowContext(ctx,
			`SELECT id, hash FROM prompts WHERE source_id = ? AND external_id = ?`,
			sourceID, item.ExternalID).Scan(&existingID, &existingHash)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if _, err := s.db.ExecContext(ctx, `
				INSERT INTO prompts (id, source_id, workspace_id, external_id, title, tags, content, variables, cover_url, hash, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				s.ids.NewID("pr"), sourceID, wsID, item.ExternalID,
				truncateBytes(item.Title, 300), strings.Join(limitTags(item.Tags), ","),
				content, marshalStrings(item.Variables), item.CoverURL, hash, fmtTime(s.clock.Now()), fmtTime(s.clock.Now())); err != nil {
				return nil, platform.AsError(err)
			}
			added++
		case err != nil:
			return nil, platform.AsError(err)
		default:
			// 只有内容 hash 变化才更新：避免每次拉取都刷新 updated_at，
			// 让「最近更新」排序失去意义，也让同步变成无意义写放大。
			if existingHash != hash {
				if _, err := s.db.ExecContext(ctx, `
					UPDATE prompts SET title = ?, tags = ?, content = ?, variables = ?, cover_url = ?, hash = ?, updated_at = ?
					WHERE id = ?`,
					truncateBytes(item.Title, 300), strings.Join(limitTags(item.Tags), ","),
					content, marshalStrings(item.Variables), item.CoverURL, hash, fmtTime(s.clock.Now()), existingID); err != nil {
					return nil, platform.AsError(err)
				}
				updated++
			}
		}
		total++
	}

	now := fmtTime(s.clock.Now())
	if _, err := s.db.ExecContext(ctx,
		`UPDATE prompt_sources SET status = 'ok', last_error = NULL, last_synced_at = ? WHERE id = ?`, now, sourceID); err != nil {
		return nil, platform.AsError(err)
	}
	_, _ = s.db.ExecContext(ctx, `
		INSERT INTO prompt_sync_logs (id, source_id, status, added, updated, removed, total, created_at)
		VALUES (?, ?, 'ok', ?, ?, 0, ?, ?)`,
		s.ids.NewID("psl"), sourceID, added, updated, total, now)

	return &api.SyncResultDTO{SourceID: sourceID, Added: added, Updated: updated, Total: total, Status: "ok"}, nil
}

// Search 见 api.PromptService：服务端检索（LIKE + 关键词切分）。
//
// 为什么不用 FTS：standalone（SQLite）与 cluster（Postgres）需要两套 FTS 语法，
// 而本场景规模是「每工作区几万条以内」，LIKE + 索引足够，且行为在两种库上一致。
// 这是有意识的取舍，收益是「同一份代码在两种部署形态下结果一致」。
func (s *Service) Search(ctx context.Context, wsID, q string, tags []string, limit int, cursor string) ([]api.PromptDTO, string, error) {
	if limit <= 0 || limit > MaxPromptSearchLimit {
		limit = 20
	}
	args := []any{}
	where := "1=1"
	if wsID != "" {
		where += " AND workspace_id = ?"
		args = append(args, wsID)
	}
	if q = strings.TrimSpace(q); q != "" {
		// 多关键词：全部命中（AND），顺序无关。把 % 与 _ 转义，
		// 否则用户搜 "50%" 会变成通配符匹配全部。
		for _, kw := range strings.Fields(q) {
			where += ` AND (title LIKE ? ESCAPE '\' OR content LIKE ? ESCAPE '\' OR tags LIKE ? ESCAPE '\')`
			pat := "%" + escapeLike(kw) + "%"
			args = append(args, pat, pat, pat)
		}
	}
	for _, tag := range tags {
		if tag = strings.TrimSpace(tag); tag != "" {
			where += ` AND (',' || tags || ',') LIKE ?`
			args = append(args, "%,"+escapeLike(tag)+",%")
		}
	}
	_ = cursor
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, source_id, external_id, title, tags, content, variables, cover_url, hash
		FROM prompts WHERE `+where+`
		ORDER BY updated_at DESC, id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, "", platform.AsError(err)
	}
	defer rows.Close()
	out := []api.PromptDTO{}
	for rows.Next() {
		var id, sourceID, externalID, title, tagsRaw, content, variables, coverURL, hash string
		if err := rows.Scan(&id, &sourceID, &externalID, &title, &tagsRaw, &content, &variables, &coverURL, &hash); err != nil {
			return nil, "", platform.AsError(err)
		}
		out = append(out, api.PromptDTO{
			ID: id, SourceID: sourceID, ExternalID: externalID, Title: title,
			Tags: splitCSV(tagsRaw), Content: content,
			Variables: unmarshalStrings(variables), CoverURL: coverURL, Hash: hash,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, "", platform.AsError(err)
	}
	var next string
	if len(out) > limit {
		out = out[:limit]
		next = out[len(out)-1].ID
	}
	return out, next, nil
}

// promptItem 是归一化后的提示词条目（与来源格式解耦）。
type promptItem struct {
	ExternalID string
	Title      string
	Content    string
	Tags       []string
	Variables  []string
	CoverURL   string
}

// fetch 拉取并归一化。
func (s *Service) fetch(ctx context.Context, url, format string) ([]promptItem, error) {
	if s.client == nil {
		return nil, platform.NewError(501, platform.CodeNotImplemented, "http client is not configured")
	}
	if err := s.guard.CheckURL(ctx, url); err != nil {
		return nil, err
	}
	req, err := newRequest(ctx, "GET", url)
	if err != nil {
		return nil, platform.AsError(err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, platform.AsError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, platform.NewError(502, platform.CodeUpstreamInvalid,
			fmt.Sprintf("上游返回 %d", resp.StatusCode))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, platform.AsError(err)
	}
	items, err := normalize(body, format)
	if err != nil {
		return nil, err
	}
	if len(items) > MaxPromptsPerSync {
		items = items[:MaxPromptsPerSync]
	}
	return items, nil
}

// Normalize 暴露归一化函数供单测与导入器复用。
func Normalize(body []byte, format string) ([]PromptItem, error) {
	items, err := normalize(body, format)
	if err != nil {
		return nil, err
	}
	out := make([]PromptItem, 0, len(items))
	for _, it := range items {
		out = append(out, PromptItem{
			ExternalID: it.ExternalID, Title: it.Title, Content: it.Content,
			Tags: it.Tags, Variables: it.Variables, CoverURL: it.CoverURL,
		})
	}
	return out, nil
}

// PromptItem 是导出的归一化条目（供测试与其他包复用）。
type PromptItem struct {
	ExternalID string
	Title      string
	Content    string
	Tags       []string
	Variables  []string
	CoverURL   string
}

// normalize 把多种来源格式归一化。
//
// 支持三种真实存在的形状（依据原项目 7 个内置来源的实际结构）：
//  1. 数组：[{id,title,content,tags}]
//  2. 包装对象：{prompts:[...]} / {items:[...]} / {data:[...]} / {list:[...]}
//  3. 带分组：{groups:[{name,prompts:[...]}]}（组名作为补充分类标签）
func normalize(body []byte, format string) ([]promptItem, error) {
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, platform.NewError(502, platform.CodeUpstreamInvalid, "来源内容不是合法 JSON")
	}
	var out []promptItem
	switch v := root.(type) {
	case []any:
		for _, raw := range v {
			if item, ok := itemFromMap(raw, nil); ok {
				out = append(out, item)
			}
		}
	case map[string]any:
		handled := false
		for _, key := range []string{"prompts", "items", "data", "list"} {
			if arr, ok := v[key].([]any); ok {
				for _, raw := range arr {
					if item, ok := itemFromMap(raw, nil); ok {
						out = append(out, item)
					}
				}
				handled = true
			}
		}
		if groups, ok := v["groups"].([]any); ok {
			for _, g := range groups {
				gm, ok := g.(map[string]any)
				if !ok {
					continue
				}
				groupName := str(gm["name"])
				arr, ok := gm["prompts"].([]any)
				if !ok {
					continue
				}
				for _, raw := range arr {
					var extra []string
					if groupName != "" {
						extra = []string{groupName}
					}
					if item, ok := itemFromMap(raw, extra); ok {
						out = append(out, item)
					}
				}
			}
			handled = true
		}
		if !handled {
			return nil, platform.NewError(502, platform.CodeUpstreamInvalid, "来源 JSON 结构无法识别")
		}
	default:
		return nil, platform.NewError(502, platform.CodeUpstreamInvalid, "来源 JSON 结构无法识别")
	}
	_ = format
	if out == nil {
		out = []promptItem{}
	}
	return out, nil
}

func itemFromMap(raw any, extraTags []string) (promptItem, bool) {
	m, ok := raw.(map[string]any)
	if !ok {
		return promptItem{}, false
	}
	content := firstOf(m, "content", "prompt", "text", "positive", "description")
	if strings.TrimSpace(content) == "" {
		return promptItem{}, false
	}
	title := firstOf(m, "title", "name", "label", "id")
	externalID := firstOf(m, "id", "uuid", "key")
	if externalID == "" {
		externalID = hashOf(content)[:16]
	}
	if title == "" {
		title = truncateBytes(content, 40)
	}
	tags := append([]string{}, extraTags...)
	switch t := m["tags"].(type) {
	case []any:
		for _, x := range t {
			if sv := str(x); sv != "" {
				tags = append(tags, sv)
			}
		}
	case string:
		tags = append(tags, splitCSV(t)...)
	}
	for _, k := range []string{"category", "group", "type"} {
		if sv := str(m[k]); sv != "" {
			tags = append(tags, sv)
		}
	}
	return promptItem{
		ExternalID: externalID, Title: title, Content: content,
		Tags:      limitTags(tags),
		Variables: detectVariables(content),
		CoverURL:  firstOf(m, "coverUrl", "cover", "image", "thumbnail"),
	}, true
}

// varPattern 匹配 {{var}} 与 {var} 两种占位写法（原项目两种都用）。
var varPattern = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_.\p{Han}-]{1,40})\s*\}\}|\{\s*([A-Za-z0-9_.\p{Han}-]{1,40})\s*\}`)

// detectVariables 提取占位变量名（去重、保序）。
func detectVariables(content string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, m := range varPattern.FindAllStringSubmatch(content, 64) {
		name := m[1]
		if name == "" {
			name = m[2]
		}
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func firstOf(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if sv := str(m[k]); sv != "" {
			return sv
		}
	}
	return ""
}

func str(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case float64:
		return strings.TrimSpace(fmt.Sprint(t))
	case bool:
		return ""
	case nil:
		return ""
	default:
		return ""
	}
}

func limitTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	seen := map[string]bool{}
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, truncateBytes(t, 64))
		if len(out) >= MaxPromptTagCount {
			break
		}
	}
	sort.Strings(out)
	return out
}

// escapeLike 转义 LIKE 通配符，避免用户输入被当作模式匹配。
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

func truncateBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	// 按 rune 边界截断，避免切出半个 UTF-8 字符（中文场景很容易触发）。
	runes := []rune(s)
	for len(string(runes)) > max && len(runes) > 0 {
		runes = runes[:len(runes)-1]
	}
	return string(runes)
}

func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func splitCSV(s string) []string {
	out := []string{}
	for _, part := range strings.Split(s, ",") {
		if t := strings.TrimSpace(part); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func marshalStrings(v []string) string {
	if v == nil {
		v = []string{}
	}
	raw, _ := json.Marshal(v)
	return string(raw)
}

func unmarshalStrings(raw string) []string {
	var out []string
	if raw == "" {
		return nil
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func fmtTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(s string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func codeOf(err error) string {
	if de := platform.AsDomainError(err); de != nil {
		return de.Code
	}
	return platform.CodeInternal
}
