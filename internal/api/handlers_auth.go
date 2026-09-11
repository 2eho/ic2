package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/context-flow/ic/internal/platform"
)

type loginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type registerReq struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
}

func (h *handlers) login(w http.ResponseWriter, r *http.Request) {
	var in loginReq
	if err := decodeBody(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	if in.Email == "" || in.Password == "" {
		writeError(w, r, platform.ErrInvalid("email and password are required"))
		return
	}
	if h.deps.Auth == nil {
		writeError(w, r, platform.NewError(http.StatusNotImplemented, platform.CodeNotImplemented, "auth is not configured"))
		return
	}
	sess, err := h.deps.Auth.Login(r.Context(), strings.TrimSpace(in.Email), in.Password)
	if err != nil {
		writeError(w, r, err)
		return
	}
	setSessionCookie(w, h.deps.Config, sess.Token, sess.ExpiresAt)
	writeJSON(w, http.StatusOK, sess)
}

func (h *handlers) register(w http.ResponseWriter, r *http.Request) {
	if !h.deps.Config.AllowRegistration {
		writeError(w, r, platform.NewError(http.StatusForbidden, platform.CodeForbidden, "registration is disabled"))
		return
	}
	var in registerReq
	if err := decodeBody(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	sess, err := h.deps.Auth.Register(r.Context(), strings.TrimSpace(in.Email), in.Name, in.Password)
	if err != nil {
		writeError(w, r, err)
		return
	}
	setSessionCookie(w, h.deps.Config, sess.Token, sess.ExpiresAt)
	writeJSON(w, http.StatusCreated, sess)
}

func (h *handlers) logout(w http.ResponseWriter, r *http.Request) {
	if tok := bearerOrCookie(r); tok != "" && h.deps.Auth != nil {
		_ = h.deps.Auth.Logout(r.Context(), tok)
	}
	http.SetCookie(w, &http.Cookie{Name: "ic_session", Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: h.deps.Config.SessionCookieSecure, SameSite: http.SameSiteLaxMode})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *handlers) me(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	ws, err := h.deps.Auth.ListWorkspaces(r.Context(), p.UserID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user":       UserDTO{ID: p.UserID, Email: p.Email, Name: p.Name},
		"workspaces": ws,
	})
}

func (h *handlers) listWorkspaces(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	ws, err := h.deps.Auth.ListWorkspaces(r.Context(), p.UserID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": ws})
}

func (h *handlers) createWorkspace(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := decodeBody(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	ws, err := h.deps.Auth.CreateWorkspace(r.Context(), p.UserID, in.Name)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, ws)
}

func (h *handlers) listProjects(w http.ResponseWriter, r *http.Request) {
	if _, err := h.requireWorkspace(r, r.PathValue("wid")); err != nil {
		writeError(w, r, err)
		return
	}
	items, err := h.deps.Auth.ListProjects(r.Context(), r.PathValue("wid"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *handlers) createProject(w http.ResponseWriter, r *http.Request) {
	pr, err := h.requireWorkspace(r, r.PathValue("wid"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	if pr == nil {
		writeError(w, r, platform.NewError(http.StatusInternalServerError, platform.CodeInternal, "workspace scope missing"))
		return
	}
	var in struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if derr := decodeBody(r, &in); derr != nil {
		writeError(w, r, derr)
		return
	}
	_ = in
	pj, cerr := h.deps.Auth.CreateProject(r.Context(), r.PathValue("wid"), in.Name, in.Description)
	if cerr != nil {
		h.logf(r, "create project failed", "err", cerr.Error())
		writeError(w, r, cerr)
		return
	}
	if pj == nil {
		h.logf(r, "create project returned nil")
		writeError(w, r, platform.NewError(500, platform.CodeInternal, "project nil"))
		return
	}
	if pj == nil {
		writeError(w, r, platform.NewError(500, platform.CodeInternal, "project is nil after create"))
		return
	}
	// writeJSON 里若有问题会 panic，这里显式先行序列化以定位。
	if _, merr := json.Marshal(pj); merr != nil {
		writeError(w, r, platform.NewError(500, platform.CodeInternal, "encode failed"))
		return
	}
	writeJSON(w, http.StatusCreated, pj)
}

// principal 解出当前主体；未认证返回 401。
func (h *handlers) principal(r *http.Request) (*Principal, error) {
	if h.deps.Auth == nil {
		// 未配置认证时为本地单用户模式（standalone 首启），固定本地主体。
		return &Principal{UserID: "local", Email: "local@ic", Name: "Local User", Role: "owner"}, nil
	}
	tok := bearerOrCookie(r)
	if tok == "" {
		return nil, platform.NewError(http.StatusUnauthorized, platform.CodeUnauthorized, "missing credentials")
	}
	p, err := h.deps.Auth.Authenticate(r.Context(), tok)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// requireWorkspace 校验路径中的 workspace 属于当前主体（INV-10 / ATK-08）。
func (h *handlers) requireWorkspace(r *http.Request, wsID string) (*Principal, error) {
	p, err := h.principal(r)
	if err != nil {
		if de, ok := err.(*platform.DomainError); ok && de == nil {
			h.logf(r, "DBG requireWorkspace: typed nil DomainError from principal")
			return nil, platform.NewError(http.StatusUnauthorized, platform.CodeUnauthorized, "unauthenticated")
		}
		return nil, err
	}
	if p == nil {
		return nil, platform.NewError(http.StatusUnauthorized, platform.CodeUnauthorized, "missing principal")
	}
	if h.deps.Auth == nil {
		if wsID == "" || wsID == "local" || wsID == "default" {
			return p, nil
		}
		return &Principal{UserID: p.UserID, Email: p.Email, Name: p.Name, Role: "owner"}, nil
	}
	ws, lerr := h.deps.Auth.ListWorkspaces(r.Context(), p.UserID)
	if lerr != nil {
		return nil, lerr
	}
	for _, x := range ws {
		if x.ID == wsID {
			p.WorkspaceID = x.ID
			p.Role = x.Role
			return p, nil
		}
	}
	// 不泄露工作区是否存在（INV-10 / ATK-08）：统一返回 404。
	return nil, platform.ErrNotFound("workspace")
}

func bearerOrCookie(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		if strings.HasPrefix(strings.ToLower(h), "bearer ") {
			return strings.TrimSpace(h[7:])
		}
	}
	if c, err := r.Cookie("ic_session"); err == nil {
		return c.Value
	}
	return ""
}

func setSessionCookie(w http.ResponseWriter, cfg platform.Config, token string, exp time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: "ic_session", Value: token, Path: "/", Expires: exp,
		HttpOnly: true, Secure: cfg.SessionCookieSecure, SameSite: http.SameSiteLaxMode,
	})
}

func decodeBody(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 8<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return platform.NewError(http.StatusBadRequest, platform.CodeInvalidRequest, "invalid request body: "+err.Error())
	}
	return nil
}
