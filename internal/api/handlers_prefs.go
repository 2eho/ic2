package api

import (
	"net/http"

	"github.com/context-flow/ic/internal/platform"
)

// 工作区偏好的 HTTP 面。
//
// 权限分级（这是本文件最重要的一点）：
//   - 读偏好：ActReadContent（任何人都要知道界面该用什么主题）；
//   - 写偏好：ActWriteContent（改默认模型属于日常配置）；
//   - 写/导出**凭据**：ActManageConfig（凭据能花钱、能访问外部系统）；
//   - 含凭据导出：ActManageConfig + 显式开关（默认导出不含凭据）。
//
// 之所以把「读偏好」放在最低档：偏好不含秘密（秘密走单独字段且只回掩码），
// 而 view（只读成员）也需要正确的界面语言与主题。

func (h *handlers) getWorkspacePrefs(w http.ResponseWriter, r *http.Request) {
	if h.deps.Prefs == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "prefs not configured"))
		return
	}
	p, err := h.requireWorkspace(r, r.PathValue("wid"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	prefs, masked, err := h.deps.Prefs.ReadPrefs(r.Context(), p.WorkspaceID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"prefs": prefs, "secrets": masked})
}

func (h *handlers) updateWorkspacePrefs(w http.ResponseWriter, r *http.Request) {
	if h.deps.Prefs == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "prefs not configured"))
		return
	}
	p, err := h.requireWorkspace(r, r.PathValue("wid"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := h.requireAction(r, p, ActWriteContent); err != nil {
		writeError(w, r, err)
		return
	}
	var in struct {
		Prefs map[string]any `json:"prefs"`
		// Secrets 是「额外凭据」：写入即加密，读取只回掩码（INV-5）。
		Secrets map[string]string `json:"secrets,omitempty"`
	}
	if err := decodeBody(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	if len(in.Secrets) > 0 {
		// 写凭据需要更高角色：editor 不该能改团队的凭据
		if err := h.requireAction(r, p, ActManageConfig); err != nil {
			writeError(w, r, err)
			return
		}
		if err := h.deps.Prefs.PutSecrets(r.Context(), p.WorkspaceID, in.Secrets); err != nil {
			writeError(w, r, err)
			return
		}
	}
	prefs, err := h.deps.Prefs.UpdatePrefs(r.Context(), p.WorkspaceID, in.Prefs)
	if err != nil {
		writeError(w, r, err)
		return
	}
	_, masked, err := h.deps.Prefs.ReadPrefs(r.Context(), p.WorkspaceID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"prefs": prefs, "secrets": masked})
}

// exportWorkspacePrefs 导出配置。`includeSecrets=true` 需要 ActManageConfig。
//
// 默认不含凭据：导出文件经常被贴进聊天群或存网盘，
// 把凭据写进去等于主动泄露。要含凭据必须显式要求，且界面上会警告。
func (h *handlers) exportWorkspacePrefs(w http.ResponseWriter, r *http.Request) {
	if h.deps.Prefs == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "prefs not configured"))
		return
	}
	p, err := h.requireWorkspace(r, r.PathValue("wid"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	includeSecrets := r.URL.Query().Get("includeSecrets") == "true"
	var payload map[string]any
	if includeSecrets {
		if err := h.requireAction(r, p, ActManageConfig); err != nil {
			writeError(w, r, err)
			return
		}
		payload, err = h.deps.Prefs.ExportPrefsWithSecrets(r.Context(), p.WorkspaceID)
	} else {
		payload, err = h.deps.Prefs.ExportPrefs(r.Context(), p.WorkspaceID)
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

// importWorkspacePrefs 导入配置。合并语义；含凭据时同样需要 ActManageConfig。
func (h *handlers) importWorkspacePrefs(w http.ResponseWriter, r *http.Request) {
	if h.deps.Prefs == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "prefs not configured"))
		return
	}
	p, err := h.requireWorkspace(r, r.PathValue("wid"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := h.requireAction(r, p, ActWriteContent); err != nil {
		writeError(w, r, err)
		return
	}
	var in map[string]any
	if err := decodeBody(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	if _, hasSecrets := in["secrets"]; hasSecrets {
		// 导入含凭据的配置需要更高角色（否则 editor 能借导入提权拿到凭据）
		if err := h.requireAction(r, p, ActManageConfig); err != nil {
			writeError(w, r, err)
			return
		}
	}
	prefs, err := h.deps.Prefs.ImportPrefs(r.Context(), p.WorkspaceID, in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"prefs": prefs})
}
