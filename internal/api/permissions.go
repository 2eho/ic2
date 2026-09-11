package api

import (
	"net/http"

	"github.com/context-flow/ic/internal/platform"
)

// 工作区角色与写权限（ATK-07 / INV-10）。
//
// 为什么要有一个显式的角色矩阵，而不是零散的 if：
// 「谁能写」是安全边界，散落成多处判断时，新增接口很容易漏掉一处。
// 这里把「动作 → 所需角色」收敛成一张表，并在中间件层统一执行。
//
// 角色语义（与 02-domain-model 一致）：
//
//	owner  ：全部权限，含成员与凭据管理
//	admin  ：除转让所有权外的全部
//	editor ：读写内容（画布、资产、运行）
//	viewer ：只读，**任何写操作必须 403**
//
// 上一轮的真实缺陷：appendOps 只校验了「已认证」，没有校验角色，
// 因此 viewer 可以直接写画布——这正是 ATK-07 要拦的东西。
type Action string

// 动作枚举。
const (
	ActReadContent  Action = "read_content"
	ActWriteContent Action = "write_content"
	ActManageConfig Action = "manage_config"
	ActManageMember Action = "manage_member"
)

// roleAllows 返回某角色是否允许某动作。
func roleAllows(role string, act Action) bool {
	switch act {
	case ActReadContent:
		// 所有角色（含 viewer）都可读
		return role == "owner" || role == "admin" || role == "editor" || role == "viewer" || role == ""
	case ActWriteContent:
		return role == "owner" || role == "admin" || role == "editor"
	case ActManageConfig:
		// 凭据、渠道、插件安装属于配置管理：editor 不该看到/修改他人凭据
		return role == "owner" || role == "admin"
	case ActManageMember:
		return role == "owner"
	}
	return false
}

// requireAction 校验当前主体是否有权执行动作，无权限返回 403。
func (h *handlers) requireAction(r *http.Request, p *Principal, act Action) error {
	if p == nil {
		return platform.NewError(http.StatusUnauthorized, platform.CodeUnauthorized, "missing principal")
	}
	if roleAllows(p.Role, act) {
		return nil
	}
	// 错误信息里带上所需动作，便于排查；但不泄漏「有哪些其他角色存在」这类信息。
	return platform.NewError(http.StatusForbidden, platform.CodeForbidden,
		"当前角色无权执行该操作").WithDetail("action", string(act)).WithDetail("role", p.Role)
}
