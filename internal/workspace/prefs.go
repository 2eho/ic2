// Package workspace 管理「工作区级偏好」：界面语言、主题、默认模型、
// 生成偏好、同步配置等。
//
// 为什么这些偏好要上服务端（原项目全在浏览器）：
//  1. **多端一致**：用户在办公室和家里打开应看到同一套设置；
//  2. **可备份**：浏览器被清理 = 设置全丢；
//  3. **安全分类**：偏好里有一部分是**秘密**（额外凭据等），必须 at-rest 加密，
//     不能与普通偏好像同等对待。
//
// 设计要点：偏好是一个 JSON 文档（`workspaces.settings`），但**分两段**——
//
//	普通段（prefs）明文存，便于运维直接用 SQL 排查；
//	秘密段（secrets）用主密钥加密后存 base64，读接口只回掩码。
//
// 这样「凭据只进不出」（INV-5）在偏好这一层也成立，而不只在 provider_credentials 里成立。
package workspace

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/context-flow/ic/internal/platform"
)

// 边界：偏好文档与秘密文档的大小上限。
//
// 没有上限的 JSON 字段会变成「隐形数据库」——用户往里塞任意东西，
// 之后导出/备份/迁移都变得不可预测。
const (
	MaxPrefsBytes   = 64 * 1024
	MaxSecretsBytes = 16 * 1024
)

// Service 读写工作区偏好。
type Service struct {
	db     *sql.DB
	secret []byte
	clock  platform.Clock
}

// New 构造偏好服务。
func New(db *sql.DB, secret []byte, clock platform.Clock) *Service {
	if clock == nil {
		clock = platform.SystemClock()
	}
	return &Service{db: db, secret: secret, clock: clock}
}

// Prefs 是普通偏好（明文，可导出 JSON）。
type Prefs struct {
	Theme         string           `json:"theme,omitempty"`
	Locale        string           `json:"locale,omitempty"`
	DefaultModels *ModelDefaults   `json:"defaultModels,omitempty"`
	Generation    *GenerationPrefs `json:"generation,omitempty"`
	Sync          *SyncPrefs       `json:"sync,omitempty"`
	UI            map[string]any   `json:"ui,omitempty"`
}

// ModelDefaults 是四类默认模型。
type ModelDefaults struct {
	Image string `json:"image,omitempty"`
	Video string `json:"video,omitempty"`
	Text  string `json:"text,omitempty"`
	Audio string `json:"audio,omitempty"`
}

// GenerationPrefs 是生成偏好。
type GenerationPrefs struct {
	ImageCount        int    `json:"imageCount,omitempty"`
	AudioVoice        string `json:"audioVoice,omitempty"`
	AudioFormat       string `json:"audioFormat,omitempty"`
	AudioSpeed        string `json:"audioSpeed,omitempty"`
	AudioInstructions string `json:"audioInstructions,omitempty"`
	SystemPrompt      string `json:"systemPrompt,omitempty"`
}

// SyncPrefs 是同步偏好（docs/design/12 DIV-06：服务端权威，
// 这里只保留「导出/备份」相关开关与上次时间）。
type SyncPrefs struct {
	Enabled      bool   `json:"enabled"`
	LastSyncedAt string `json:"lastSyncedAt,omitempty"`
	ExportZip    bool   `json:"exportZip"`
}

// Secrets 是秘密段（加密存储，读接口只回掩码）。
type Secrets struct {
	// Extra 是「非模型类」的凭据（例如自建中转的额外 token）。
	// 模型类凭据仍走 provider_credentials，两者不重叠。
	Extra map[string]string `json:"extra,omitempty"`
}

// snapshot 是 settings JSON 的完整形状。
//
// prefs / secrets 分在两个键下：这样即便有人手工改了数据库，
// 也不会把密文与明文混在一起（结构上不可能误读）。
type snapshot struct {
	Version   int             `json:"v"`
	Prefs     json.RawMessage `json:"prefs,omitempty"`
	Secrets   string          `json:"secrets,omitempty"` // base64(AES-GCM)
	Quota     json.RawMessage `json:"quota,omitempty"`   // 限额配置（由 exec 读取）
	UpdatedAt string          `json:"updatedAt,omitempty"`
}

// Read 返回偏好（明文）与秘密的掩码。
func (s *Service) Read(ctx context.Context, wsID string) (Prefs, map[string]string, error) {
	snap, err := s.load(ctx, wsID)
	if err != nil {
		return Prefs{}, nil, err
	}
	var prefs Prefs
	if len(snap.Prefs) > 0 {
		// 解析失败不回错：一个坏掉的偏好不该让整个配置页打不开。
		// 调用方拿到零值（等于「全部用默认」），比一片报错有用。
		_ = json.Unmarshal(snap.Prefs, &prefs)
	}
	masked := map[string]string{}
	if snap.Secrets != "" {
		if plain, err := s.open(snap.Secrets); err == nil {
			var sec Secrets
			if json.Unmarshal(plain, &sec) == nil {
				for k, v := range sec.Extra {
					masked[k] = platform.Mask(v)
				}
			}
		}
	}
	return prefs, masked, nil
}

// Update 合并写入普通偏好（不触碰秘密段）。
//
// 合并而不是整体替换：前端可能只改了主题，整体替换会把其他端刚设置的
// 默认模型抹掉——多端场景下这种「丢设置」极难排查。
func (s *Service) Update(ctx context.Context, wsID string, patch Prefs) (Prefs, error) {
	snap, err := s.load(ctx, wsID)
	if err != nil {
		return Prefs{}, err
	}
	merged := map[string]any{}
	if len(snap.Prefs) > 0 {
		_ = json.Unmarshal(snap.Prefs, &merged)
	}
	patchRaw, err := json.Marshal(patch)
	if err != nil {
		return Prefs{}, platform.AsError(err)
	}
	var patchMap map[string]any
	if err := json.Unmarshal(patchRaw, &patchMap); err != nil {
		return Prefs{}, platform.AsError(err)
	}
	for k, v := range patchMap {
		merged[k] = v
	}
	raw, err := json.Marshal(merged)
	if err != nil {
		return Prefs{}, platform.AsError(err)
	}
	if len(raw) > MaxPrefsBytes {
		return Prefs{}, platform.NewError(422, platform.CodeInvalidRequest, "偏好文档过大").
			WithDetail("limitBytes", MaxPrefsBytes).WithDetail("actualBytes", len(raw))
	}
	snap.Prefs = raw
	if err := s.save(ctx, wsID, snap); err != nil {
		return Prefs{}, err
	}
	var out Prefs
	if err := json.Unmarshal(raw, &out); err != nil {
		return Prefs{}, platform.AsError(err)
	}
	return out, nil
}

// PutSecrets 写入秘密段（加密）。空值表示**删除**该条，而不是存空串。
func (s *Service) PutSecrets(ctx context.Context, wsID string, values map[string]string) error {
	snap, err := s.load(ctx, wsID)
	if err != nil {
		return err
	}
	extra := map[string]string{}
	if snap.Secrets != "" {
		if plain, err := s.open(snap.Secrets); err == nil {
			var sec Secrets
			if json.Unmarshal(plain, &sec) == nil && sec.Extra != nil {
				extra = sec.Extra
			}
		}
	}
	for k, v := range values {
		// 显式空值 = 删除：这让「清空某个凭据」有明确表达，
		// 而不是把空串加密存进去（读出来会是一个看似存在的空凭据）。
		if strings.TrimSpace(v) == "" {
			delete(extra, k)
			continue
		}
		extra[k] = v
	}
	raw, err := json.Marshal(Secrets{Extra: extra})
	if err != nil {
		return platform.AsError(err)
	}
	if len(raw) > MaxSecretsBytes {
		return platform.NewError(422, platform.CodeInvalidRequest, "凭据文档过大").
			WithDetail("limitBytes", MaxSecretsBytes)
	}
	sealed, err := s.seal(raw)
	if err != nil {
		return err
	}
	snap.Secrets = sealed
	return s.save(ctx, wsID, snap)
}

// Export 导出为可分享的 JSON（**不含秘密**）。
//
// 导出文件经常被贴到聊天群或存到网盘，把凭据写进去等于主动泄露。
// 需要完整备份（含凭据）时用 ExportWithSecrets，那个方法在 handler 层有更强权限校验。
func (s *Service) Export(ctx context.Context, wsID string) (map[string]any, error) {
	prefs, _, err := s.Read(ctx, wsID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"app":        "ic",
		"version":    1,
		"exportedAt": s.clock.Now().UTC().Format(time.RFC3339),
		"prefs":      prefs,
		"secrets": map[string]any{
			"included": false,
			"reason":   "导出文件可能被分享，凭据不写入；如需完整备份请选择「含凭据导出」并妥善保管",
		},
	}, nil
}

// ExportWithSecrets 导出含明文凭据的 JSON，供「我就是要完整备份」的场景。
//
// 单独一个方法而不是 Export 的参数：调用点必须显式选择，
// 并且能在 handler 层要求更高角色（ActManageConfig）。
func (s *Service) ExportWithSecrets(ctx context.Context, wsID string) (map[string]any, error) {
	prefs, _, err := s.Read(ctx, wsID)
	if err != nil {
		return nil, err
	}
	snap, err := s.load(ctx, wsID)
	if err != nil {
		return nil, err
	}
	plain := map[string]string{}
	if snap.Secrets != "" {
		if raw, err := s.open(snap.Secrets); err == nil {
			var sec Secrets
			if json.Unmarshal(raw, &sec) == nil && sec.Extra != nil {
				plain = sec.Extra
			}
		}
	}
	return map[string]any{
		"app":        "ic",
		"version":    1,
		"exportedAt": s.clock.Now().UTC().Format(time.RFC3339),
		"prefs":      prefs,
		"secrets":    plain,
		"warning":    "本文件包含明文凭据，请勿分享或提交到版本库",
	}, nil
}

// Import 导入配置（合并语义，不删除未提及的字段）。
func (s *Service) Import(ctx context.Context, wsID string, payload map[string]any) (Prefs, error) {
	app, _ := payload["app"].(string)
	if app != "" && app != "ic" {
		// 明确拒绝而不是「尽力而为」：把别的产品的配置当自己的读进去，
		// 只会产生一堆看不懂的默认值，用户还以为是导入成功了。
		return Prefs{}, platform.ErrInvalid("不是 IC 的配置文件（app=" + app + "）")
	}
	prefsRaw, ok := payload["prefs"]
	if !ok {
		return Prefs{}, platform.ErrInvalid("配置文件缺少 prefs")
	}
	raw, err := json.Marshal(prefsRaw)
	if err != nil {
		return Prefs{}, platform.AsError(err)
	}
	var patch Prefs
	if err := json.Unmarshal(raw, &patch); err != nil {
		return Prefs{}, platform.ErrInvalid("prefs 字段结构不正确")
	}
	updated, err := s.Update(ctx, wsID, patch)
	if err != nil {
		return Prefs{}, err
	}
	if secrets, ok := payload["secrets"].(map[string]any); ok {
		values := map[string]string{}
		for k, v := range secrets {
			if sv, ok := v.(string); ok {
				values[k] = sv
			}
		}
		if len(values) > 0 {
			if err := s.PutSecrets(ctx, wsID, values); err != nil {
				return updated, err
			}
		}
	}
	return updated, nil
}

func (s *Service) load(ctx context.Context, wsID string) (snapshot, error) {
	var raw string
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(settings, '{}') FROM workspaces WHERE id = ? AND deleted_at IS NULL`, wsID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		// 已删除与不存在返回同一个错误（不泄露存在性，INV-10）
		return snapshot{}, platform.ErrNotFound("workspace")
	}
	if err != nil {
		return snapshot{}, platform.AsError(err)
	}
	var snap snapshot
	if err := json.Unmarshal([]byte(raw), &snap); err != nil || snap.Prefs == nil {
		// 老数据的 settings 可能直接是 prefs（没有 v/prefs 包装）：兼容读取。
		// 不这样做的话，第一次升级会让所有老工作区的设置「消失」。
		snap = snapshot{Version: 1, Prefs: json.RawMessage(raw)}
	}
	return snap, nil
}

func (s *Service) save(ctx context.Context, wsID string, snap snapshot) error {
	snap.Version = 1
	snap.UpdatedAt = s.clock.Now().UTC().Format(time.RFC3339)
	raw, err := json.Marshal(snap)
	if err != nil {
		return platform.AsError(err)
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE workspaces SET settings = ? WHERE id = ? AND deleted_at IS NULL`, string(raw), wsID)
	if err != nil {
		return platform.AsError(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return platform.ErrNotFound("workspace")
	}
	return nil
}

func (s *Service) seal(plain []byte) (string, error) {
	if len(s.secret) == 0 {
		return "", platform.NewError(500, platform.CodeInternal, "未配置主密钥，无法存储凭据")
	}
	sealed, err := platform.SealSecret(s.secret, string(plain))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func (s *Service) open(encoded string) ([]byte, error) {
	if len(s.secret) == 0 {
		return nil, platform.NewError(500, platform.CodeInternal, "未配置主密钥，无法读取凭据")
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, platform.NewError(500, platform.CodeInternal, "凭据密文格式不正确")
	}
	plain, err := platform.OpenSecret(s.secret, raw)
	if err != nil {
		return nil, err
	}
	return []byte(plain), nil
}
