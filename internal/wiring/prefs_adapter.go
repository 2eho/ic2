package wiring

import (
	"context"
	"encoding/json"

	"github.com/context-flow/ic/internal/platform"
	"github.com/context-flow/ic/internal/workspace"
)

// prefsAdapter 把 internal/workspace.Service 适配为 api.PrefsService。
//
// 适配层做两件事：
//  1. 把强类型 Prefs 转成 map（HTTP 层不该依赖领域包的类型，避免耦合）；
//  2. 请求体是自由 map 时，用「先 JSON 往返再解析」保持字段校验在领域层完成——
//     如果在这里做字段白名单，领域层新增一个字段就必须同步改两处。
type prefsAdapter struct{ svc *workspace.Service }

// NewPrefsAdapter 构造适配器。
func NewPrefsAdapter(svc *workspace.Service) *prefsAdapter { return &prefsAdapter{svc: svc} }

// ReadPrefs 见 api.PrefsService。
func (a *prefsAdapter) ReadPrefs(ctx context.Context, wsID string) (any, map[string]string, error) {
	prefs, masked, err := a.svc.Read(ctx, wsID)
	if err != nil {
		return nil, nil, err
	}
	return toMap(prefs), masked, nil
}

// UpdatePrefs 见 api.PrefsService。
func (a *prefsAdapter) UpdatePrefs(ctx context.Context, wsID string, patch map[string]any) (any, error) {
	if patch == nil {
		patch = map[string]any{}
	}
	raw, err := json.Marshal(patch)
	if err != nil {
		return nil, platform.AsError(err)
	}
	var typed workspace.Prefs
	if err := json.Unmarshal(raw, &typed); err != nil {
		return nil, platform.ErrInvalid("偏好字段结构不正确: " + platform.Redact(err.Error()))
	}
	updated, err := a.svc.Update(ctx, wsID, typed)
	if err != nil {
		return nil, err
	}
	return toMap(updated), nil
}

// PutSecrets 见 api.PrefsService。
func (a *prefsAdapter) PutSecrets(ctx context.Context, wsID string, values map[string]string) error {
	return a.svc.PutSecrets(ctx, wsID, values)
}

// ExportPrefs 见 api.PrefsService。
func (a *prefsAdapter) ExportPrefs(ctx context.Context, wsID string) (map[string]any, error) {
	return a.svc.Export(ctx, wsID)
}

// ExportPrefsWithSecrets 见 api.PrefsService。
func (a *prefsAdapter) ExportPrefsWithSecrets(ctx context.Context, wsID string) (map[string]any, error) {
	return a.svc.ExportWithSecrets(ctx, wsID)
}

// ImportPrefs 见 api.PrefsService。
func (a *prefsAdapter) ImportPrefs(ctx context.Context, wsID string, payload map[string]any) (any, error) {
	prefs, err := a.svc.Import(ctx, wsID, payload)
	if err != nil {
		return nil, err
	}
	return toMap(prefs), nil
}

func toMap(v any) map[string]any {
	raw, err := json.Marshal(v)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}
	}
	return out
}
