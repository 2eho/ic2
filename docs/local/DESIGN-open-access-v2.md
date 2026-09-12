# ic2 开放访问完善 — 顶层设计 v2

状态：待定稿 → 实现  
范围：仅 `/home/box/ic2` 本机部署；不回灌上游公开仓除非另说  
前提：`IC_OPEN_ACCESS=true` 已上线（`/auth/open` + 前端自动进）

## 1. 目标

| 目标 | 非目标 |
|------|--------|
| 打开 URL 即用，零账号密码心智 | 做成公网多租户 SaaS |
| 开放模式下 UI/API 行为一致、无登录闪屏 | 关掉服务端会话模型（仍要 session，只是自动发） |
| 边界清晰：谁能进、什么不能误开 | 改 VLESS / 其它站点鉴权 |
| 可测、可回滚（关 `IC_OPEN_ACCESS` 即回登录墙） | 把本机补丁默认同步进 cnb 上游 main |

## 2. 现状（已落地）

- 配置：`IC_OPEN_ACCESS`、meta.`features.openAccess`
- 引导：`u_open_local` / `ws_open_default` / `pj_open_default`
- API：`GET|POST /api/v1/auth/open` → 长寿命 cookie（~10y）
- 前端：meta → 无 token 则 auto open；logout 再 open
- 注册：`.env` 已 `IC_ALLOW_REGISTRATION=false`

## 3. 完善范围（本轮要做）

### P0 — 体验与安全必做

1. **隐藏登出入口**  
   `openAccess` 时 AppShell 不展示「退出」；`logout` API 仍保留给调试，UI 不诱导「登出→空白」。

2. **消灭登录闪屏 / 死胡同**  
   - `openAccess` 时路由永不落到 `<LoginPage>`（含直接访问 `/login` → 重定向 `/` 并继续 bootstrap）。  
   - loading 态覆盖 meta+open 全程，失败时展示可重试错误页，不跳登录。

3. **会话发放收敛**  
   - 每次刷新都 `POST /auth/open` 会堆积 sessions 表 → 改为：**优先复用未过期 cookie/sessionStorage token**（已有）；仅失效时再 open。  
   - 增加服务端：**同一 open 用户限制活跃 session 上限**（例如 20），超额删最旧（防表膨胀）。  
   - TTL：保持长寿命，但文档写明「等同大门钥匙」；可选环境变量 `IC_OPEN_SESSION_DAYS`（默认 3650）便于以后收紧。

4. **Cookie 与跨站**  
   - 维持 `Secure + HttpOnly + SameSite=Lax`（HTTPS 隧道）。  
   - 前端同时存 token（现有 sessionStorage）供 Bearer；两边任一有效即可。

### P1 — 产品完整度

5. **默认着陆**  
   open 成功后若仅有默认项目、无画布，可保留 Home；不强制建空画布（避免吵）。若 Home 空态差，补一句「从项目进入」。

6. **设置页文案**  
   open 模式下账号区显示「开放访问（无私密登录）」而非邮箱密码表单；禁止露出引导用户密码。

7. **运维开关**  
   `DEPLOY.md` / `OPS.md`：如何关开放访问（改 `.env` + restart）；风险一句：持有 URL ≈ 全权。

### P2 — 工程质量

8. **单测**  
   - `EnsureOpenAccessBootstrap` 幂等  
   - `OpenAccess` 关旗 → 404；开旗 → 有 token  
   - session 上限裁剪（若实现）

9. **契约**  
   OpenAPI 已改则保持与路由一致；CI `make check` 本地能过的子集跑通（至少 `go test` 相关包）。

## 4. 架构与解耦

```
浏览器                    ic-server
  |                         |
  | GET /api/v1/meta        | features.openAccess
  |------------------------>|
  |                         |
  | [无有效 token]          |
  | POST /api/v1/auth/open  | OpenAccess flag gate
  |------------------------>| EnsureBootstrap → issueSessionTTL
  | Set-Cookie + JSON       | (+ prune old sessions)
  |                         |
  | 业务 API + Bearer/Cookie| 与普通登录同一 principal 路径
```

- **身份域**（identity）：bootstrap / open / prune — 不渗入 graph/exec。  
- **API 域**：薄 handler + meta 旗。  
- **前端**：`useSession` 唯一会话真相；App 路由只认 `session|openAccess+loading`。  
- **配置**：仅环境变量；不写死进镜像默认 true（上游默认 false，本机 `.env` true）。

## 5. 对抗推演（边界 / 死角）

| ID | 攻击/失效面 | 期望 | 对策 |
|----|-------------|------|------|
| A1 | URL 泄露 | 等同共享整站（含后续填的模型 Key） | 文档明示；隧道不公网宣传；不关 SSRF 默认 |
| A2 | 关 `IC_OPEN_ACCESS` 后旧 cookie | 应 401，回登录墙 | Authenticate 照常校验 session；前端 meta 变 false 后走登录 |
| A3 | `/login` 深链 | 不出现注册墙死循环 | open 时 Navigate→`/` |
| A4 | logout 清空后卡死 | 自动再 open | 已有；再藏 UI 防误触 |
| A5 | sessions 表膨胀 | 磁盘/查询变慢 | 上限 prune |
| A6 | 引导邮箱 `open@local` 不合 Register 正则 | 不影响（直插 SQL） | 保持；勿走 Register 路径建引导用户 |
| A7 | 多标签并发 open | 多个有效 token | 可接受；prune 限制总量 |
| A8 | 本机补丁误 push 上游 | 公开仓默认开放访问 | **禁止默认 push**；补丁仅本机 / 私有分支 |
| A9 | update.sh 覆盖 `.env` | 丢开关 | update 不改 `.env`；验收保留 |
| A10 | 与真实多用户混用 | 数据全进 Open 工作区 | 开放模式假定单人私用；不做隔离增强 |

## 6. 验收标准

1. 无痕窗口打开 `https://ic2.zehh.de5.net/`：无登录页、进入主界面。  
2. 直接访问 `/login`：不停留登录页。  
3. 无「退出」按钮（或点了仍留在已登录态）。  
4. `IC_OPEN_ACCESS=false` + restart：恢复登录墙；`/auth/open` → 404。  
5. 相关 `go test` 通过；公网 `/` `/api/v1/meta` 仍 200。  
6. 不改动 ic/canvas/studio/edit/slinx/VLESS。

## 7. 实现分工建议

- 云端 **Claude Fable 5.1**（`claude-fable-5-1`）：适合出 PR 的代码完善。  
  **阻塞**：当前 Cursor 已连接 SCM 仅为 GitHub，**看不到** `cnb.cool/context-flow.cloud/ic`。要 Fable 云端改，需：把本机分支推到已连接的 GitHub 仓，或连接可用的 Git 托管后再 launch。  
- 否则：**本机按本设计直接改**（效果相同，模型非 Fable）。

## 8. 回滚

```bash
# /home/box/ic2/.env
IC_OPEN_ACCESS=false
# 可选恢复注册
# IC_ALLOW_REGISTRATION=true
/home/box/ic2/start.sh --restart
```
