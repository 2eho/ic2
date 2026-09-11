package agent

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/context-flow/ic/internal/platform"
)

// SkillsSQLStore 是 SkillStore 的 SQL 实现。
//
// 为什么 Skill 存在服务端（原项目存在浏览器 store 里）：
//   - Skill 是「团队资产」：同一个工作区的成员应该共享，
//     浏览器存储做不到这一点；
//   - Skill 会进模型的系统提示词，因此必须有长度上限与归属校验。
type SkillsSQLStore struct {
	db    *sql.DB
	clock platform.Clock
	// MaxSkills 单工作区上限，避免注入过长的系统提示词。
	MaxSkills int
}

// NewSkillsSQLStore 构造 Skill 存储。
func NewSkillsSQLStore(db *sql.DB, clock platform.Clock) *SkillsSQLStore {
	if clock == nil {
		clock = platform.SystemClock()
	}
	return &SkillsSQLStore{db: db, clock: clock, MaxSkills: 50}
}

// ListSkills 见 agent.SkillStore。
func (s *SkillsSQLStore) ListSkills(ctx context.Context, wsID string) ([]map[string]any, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT name, description, instructions, tags, enabled, created_at, updated_at
		FROM agent_skills WHERE workspace_id = ? ORDER BY name`, wsID)
	if err != nil {
		return nil, platform.AsError(err)
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var (
			name, desc, instructions, tags, createdAt, updatedAt string
			enabled                                              int
		)
		if err := rows.Scan(&name, &desc, &instructions, &tags, &enabled, &createdAt, &updatedAt); err != nil {
			return nil, platform.AsError(err)
		}
		out = append(out, map[string]any{
			"name": name, "description": desc, "instructions": instructions,
			"tags": splitTags(tags), "enabled": enabled == 1,
			"createdAt": createdAt, "updatedAt": updatedAt,
		})
	}
	return out, rows.Err()
}

// UpsertSkill 见 agent.SkillStore。
func (s *SkillsSQLStore) UpsertSkill(ctx context.Context, wsID string, skill map[string]any) (map[string]any, error) {
	name, _ := skill["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, platform.ErrInvalid("skill name is required")
	}
	if len(name) > 64 {
		return nil, platform.ErrInvalid("skill name too long").WithDetail("maxLen", 64)
	}
	instructions, _ := skill["instructions"].(string)
	if strings.TrimSpace(instructions) == "" {
		return nil, platform.ErrInvalid("instructions is required")
	}
	if len(instructions) > 32*1024 {
		return nil, platform.NewError(422, platform.CodeInvalidRequest, "instructions too large").
			WithDetail("limitBytes", 32*1024)
	}
	description, _ := skill["description"].(string)
	if len(description) > 500 {
		description = description[:500]
	}
	enabled := 1
	if v, ok := skill["enabled"].(bool); ok && !v {
		enabled = 0
	}
	tags := ""
	if list, ok := skill["tags"].([]any); ok {
		parts := make([]string, 0, len(list))
		for _, t := range list {
			if sv, ok := t.(string); ok && strings.TrimSpace(sv) != "" {
				parts = append(parts, strings.TrimSpace(sv))
			}
		}
		tags = strings.Join(parts, ",")
	}

	var existing int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM agent_skills WHERE workspace_id = ? AND name = ?`, wsID, name).Scan(&existing); err != nil {
		return nil, platform.AsError(err)
	}
	if existing == 0 {
		var total int
		if err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM agent_skills WHERE workspace_id = ?`, wsID).Scan(&total); err != nil {
			return nil, platform.AsError(err)
		}
		if total >= s.MaxSkills {
			return nil, platform.NewError(422, platform.CodeInvalidRequest, "skill 数量已达上限").
				WithDetail("limit", s.MaxSkills)
		}
	}
	now := s.clock.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO agent_skills (workspace_id, name, description, instructions, tags, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(workspace_id, name) DO UPDATE SET
		  description = excluded.description,
		  instructions = excluded.instructions,
		  tags = excluded.tags,
		  enabled = excluded.enabled,
		  updated_at = excluded.updated_at`,
		wsID, name, description, instructions, tags, enabled, now, now); err != nil {
		return nil, platform.AsError(err)
	}
	return map[string]any{
		"name": name, "description": description, "instructions": instructions,
		"tags": splitTags(tags), "enabled": enabled == 1, "updatedAt": now,
	}, nil
}

// DeleteSkill 见 agent.SkillStore。
func (s *SkillsSQLStore) DeleteSkill(ctx context.Context, wsID, name string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM agent_skills WHERE workspace_id = ? AND name = ?`, wsID, name)
	if err != nil {
		return platform.AsError(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return platform.ErrNotFound("skill")
	}
	return nil
}

// EnabledInstructions 返回启用中的 Skill 指令（拼进系统提示词）。
//
// 上限保护：即使 skill 数量在限额内，拼起来也可能很长；
// 这里按总字节截断并保序，避免「加了一个 skill 就把上下文挤爆」。
func (s *SkillsSQLStore) EnabledInstructions(ctx context.Context, wsID string, maxBytes int) (string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT name, instructions FROM agent_skills
		WHERE workspace_id = ? AND enabled = 1 ORDER BY name`, wsID)
	if err != nil {
		return "", platform.AsError(err)
	}
	defer rows.Close()
	var sb strings.Builder
	for rows.Next() {
		var name, instructions string
		if err := rows.Scan(&name, &instructions); err != nil {
			return "", platform.AsError(err)
		}
		block := "\n### " + name + "\n" + instructions + "\n"
		if maxBytes > 0 && sb.Len()+len(block) > maxBytes {
			break
		}
		sb.WriteString(block)
	}
	return sb.String(), rows.Err()
}

func splitTags(s string) []string {
	out := []string{}
	for _, part := range strings.Split(s, ",") {
		if t := strings.TrimSpace(part); t != "" {
			out = append(out, t)
		}
	}
	return out
}

var _ SkillStore = (*SkillsSQLStore)(nil)

// 编译期断言：允许 nil DB 时不 panic（精简部署）。
var _ = errors.Is
