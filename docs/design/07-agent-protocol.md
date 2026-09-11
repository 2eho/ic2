# 07 · Agent 接入设计

## 1. 原方案回顾

原项目 `canvas-agent/` 的思路是对的，也踩出了不少经验：

- 浏览器不能直接起本地进程，所以引入本机 Node 服务。
- 本机服务做两件事：**MCP Server**（给终端 Codex/Claude 用）和 **会话网关**（给网页侧边栏用）。
- 网页侧主动连本机（SSE + HTTP POST），本机只监听 `127.0.0.1` + token + Origin 白名单。
- 已经沉淀的坑：token 不能进 URL（会进日志/Referer/历史），配置目录权限要收紧，日志要脱敏，
  Agent 消息必须按 `threadId/turnId/itemId` 归属，协议版本与消息存储版本要分开管理。

重写保留这些结论，但把**编排逻辑上移到服务端**，本地 Agent 退化为「本机能力执行器」。

## 2. 架构

```
┌────────────── 浏览器 ──────────────┐
│ Agent Sidebar                       │
│  ├─ 会话 UI（消息 / 工具调用 / 审批） │
│  └─ 画布工具执行器（唯一能改画布的地方）│
└──────┬───────────────────┬──────────┘
       │ SSE 事件           │ 工具结果
       ▼                   ▲
┌──────────────────────────────────────┐
│      IC Server · internal/agent       │
│  ├─ Session/Turn/Item 领域模型         │
│  ├─ Backend 适配：codex / claude / http│
│  ├─ MCP Server（stdio 供本机 Agent 连）│
│  └─ 工具审批网关 + 审计                │
└──────┬───────────────────────────────┘
       │ HTTP（内网/回环，mTLS 可选）
       ▼
┌──────────────────────────┐
│ Canvas Agent（本机，可选） │
│  ├─ Codex app-server 桥接  │
│  ├─ Claude Code 桥接       │
│  └─ 本机文件/附件能力       │
└──────────────────────────┘
```

两种部署形态：

| 形态 | 说明 |
| --- | --- |
| A. 服务端承载 Agent | 服务端可直接调用云端模型做 Agent（无需本地），工具执行仍在浏览器 |
| B. 本机 Agent 承载 | 用户想用自己的 Codex/Claude Code 与本机文件能力，服务端转发到本机 Agent |

形态 A 是新增能力：原项目完全没有服务端，因此 Agent 必须依赖本机进程。
形态 B 保留原体验。两者对前端是**同一套会话 UI 与同一套工具协议**。

## 3. 会话领域模型（落实原项目的经验教训）

```go
type Item struct {
    ID       string    // itemId，Agent 侧稳定 ID
    TurnID   string
    Seq      int
    Kind     ItemKind  // agent_message | reasoning | tool_call | tool_result | file_change | error
    Payload  json.RawMessage
    Source   ItemSource // live（实时事件物化） | snapshot（历史快照）
    CreatedAt time.Time
}
```

规则（直接来自原项目 AGENTS.md 的沉淀，写进 schema 与测试）：

1. Item 主键 `(turn_id, item_id)`，**唯一约束**保证实时流与历史快照合并幂等。
2. 实时事件只能**补充未物化**的 turn；一旦该 turn 的历史快照到达，快照为权威。
3. 协议版本（`agent_protocol_version`）与消息存储版本（`agent_store_version`）独立管理。
4. 存储升级必须：先备份 → 迁移 → 未知版本/损坏清单/冲突备份时**拒绝覆盖**，不静默裁剪历史。

## 4. 工具协议

工具定义与画布领域 op 同源（见 `02-domain-model.md`）。
服务端把工具暴露给 Agent，Agent 调用的结果是「一堆 op」，
由**浏览器**执行（形态 A/B 都一样），因为它持有画布的内核与 undo 栈。

```ts
type ToolDef = {
  name: string;                 // canvas.apply_ops
  description: string;
  inputSchema: JSONSchema;      // 由领域 op 的 schema 生成
  approval: "auto" | "confirm" | "forbidden";
  scope: "read" | "write";
};

type ToolCallResult = {
  callId: string;
  status: "ok" | "denied" | "error";
  applied?: { ops: number; version: number };
  inverse?: Op[];               // 支持撤销 Agent 的一次批量操作
  error?: { code: string; message: string };
};
```

首批工具（对齐并扩展原项目的 6 个）：

| 工具 | 权限 | 审批 |
| --- | --- | --- |
| `canvas.get_state` | read | auto |
| `canvas.get_selection` | read | auto |
| `canvas.export_snapshot` | read | auto |
| `canvas.apply_ops` | write | confirm |
| `canvas.create_text_node` | write | auto |
| `canvas.create_generation_flow` | write | **confirm**（会触发真实费用） |
| `canvas.run_generation` | write+cost | **confirm** |
| `assets.search` | read | auto |
| `prompts.search` | read | auto |
| `runs.list` / `runs.get` | read | auto |

审批策略：

- `confirm` 的工具在侧边栏展示「将要执行的 op 列表 + 影响节点数 + 预估成本」，用户确认后执行。
- 每次批量写保留 `inverse`，提供「撤销这次 Agent 操作」按钮。
- 可配置「本会话自动放行 `canvas.apply_ops`」，但**必须**由用户显式开启且随会话过期。

## 5. 与 MCP 的关系

- 服务端暴露 **MCP Server**（Streamable HTTP），任何 MCP 客户端都能连：
  `ic mcp --server https://ic.example.com --workspace ws_1 --key <api_key>`
- 本机 `canvas-agent` 提供 stdio MCP，把工具调用转发到服务端（兼容原有 `codex mcp add` 用法）。
- MCP 工具列表与 REST 工具表由同一份 schema 生成，避免两套定义漂移。

## 6. 安全边界

| 事项 | 做法 |
| --- | --- |
| 本机服务监听 | 仅 `127.0.0.1`，随机端口，token 认证 |
| Token 传递 | 走 `Authorization` 头 / fragment，**禁止** query string |
| Origin | 首次连接后固定，之后其他 Origin 复用需用户重置 |
| 配置存储 | `~/.ic/` 目录 `0700`，配置文件 `0600` |
| 日志 | 脱敏 API Key、Token、Data URL；日志文件权限收紧 |
| 服务端 → 本机 | 只允许用户自己配置的本机地址，禁止服务端主动探测内网 |
| 模型凭据 | 本机 Agent 用自己的 Codex/Claude 登录态，服务端不保存、不中转 |
| 附件 | 单请求大小上限（默认 30MB），图片落临时文件后即删 |
| 审计 | 每次工具调用记录：会话、工具、op 摘要、审批人、结果 |

## 7. 前端侧边栏

```
AgentSidebar
  ├─ SessionList      会话列表（按画布过滤）
  ├─ Transcript       消息流：流式 markdown、思考折叠、工具调用卡片、错误
  ├─ ToolApprovalCard 审批卡片：op 列表 / 成本预估 / 一键撤销
  ├─ Composer         输入 + 附件（图片/画布选区）+ 引用选择节点
  └─ ConnStatus       连接状态：服务端 Agent / 本机 Agent / 断开与重连
```

原项目侧边栏把「Codex app-server 事件」直接映射到 UI，
重写后统一为 `Item` 模型，后端负责不同 Backend（Codex/Claude/HTTP）的事件归一化，
前端只认 `ItemKind`，不再为每个 Agent 写一套渲染逻辑。

## 8. 落地顺序

1. 服务端 `internal/agent` 会话模型 + 工具协议 + SSE 事件（形态 A，先用 HTTP 模型做 Agent）。
2. 前端侧边栏（复用现有 UI 设计）+ 工具审批 + 撤销。
3. 本机 `canvas-agent` 精简为桥接器（Codex app-server / Claude Code）+ stdio MCP 转发。
4. 扩展工具集（资产检索、提示词、运行历史）。
5. MCP Streamable HTTP 对外开放 + API Key 鉴权。
