# 02 · 领域模型与数据模型

## 1. 领域全景

```
User ─┬─ Workspace ─┬─ Project ─── Canvas ─── CanvasDoc(nodes, edges, groups)
      │             ├─ ProviderCredential
      │             ├─ AssetFolder ── Asset
      │             ├─ PromptLibrary ── Prompt
      │             ├─ PluginInstall
      │             └─ AgentSession
      └─ Membership(role)
Run ── Step ── Attempt ── ProviderCallLog
```

与原项目的最大差异：**Workspace 成为所有资源的归属边界**，画布项目不再孤立存在于浏览器。

## 2. 核心实体

### 2.1 画布文档（CanvasDocument）

画布的权威数据结构。与原项目不同，**节点不再用一个扁平 metadata 袋装所有东西**。

```go
type CanvasDocument struct {
    ID        string
    ProjectID string
    Version   int64          // 单调递增，每次 op 提交 +1
    Viewport  Viewport       // 服务端存"最后编辑者视口"，仅作恢复用
    Settings  CanvasSettings // 背景、网格、吸附、只读
    Nodes     map[string]Node
    Edges     map[string]Edge
    UpdatedAt time.Time
}

type Node struct {
    ID       string
    Type     NodeTypeID     // "image" | "text" | ... | "plugin:xxx/yyy"
    Title    string
    Rect     Rect           // x,y,w,h 世界坐标
    Z        int
    ParentID string         // 分组归属
    Ports    Ports          // 输入/输出端口声明
    Spec     NodeSpec       // 类型化配置（联合类型，不用任意 map）
    State    NodeState      // 运行态：idle/running/succeeded/failed + 最近 Run
}

type Rect struct{ X, Y, W, H float64 }

type Ports struct {
    Inputs  []Port
    Outputs []Port
}

type Port struct {
    ID       string
    Name     string
    Kind     ResourceKind // text | image | video | audio | file | json
    Multiple bool         // 是否允许多条入边
    Required bool
}
```

节点类型化配置（判别联合，Go 侧 union，TS 侧 discriminated union）：

| NodeType | Spec | 说明 |
| --- | --- | --- |
| `prompt` | `{ text, variables[] }` | 原「文本节点」升级：支持 `{{var}}` 变量与 `@ref` 引用 |
| `image` | `{ assetId?, fit, freeResize, annotations[] }` | 只存资产引用 |
| `video` | `{ assetId?, poster, loop, controls }` | |
| `audio` | `{ assetId?, duration }` | |
| `group` | `{ collapsed, tint }` | 布局容器，不作为执行单元 |
| `generation` | `{ capability, providerId, model, params, outputCount }` | 原「生成配置节点」标准化 |
| `run` | `{ runId }` | 工作流运行的输入锚点 |
| `plugin:*` | `{ pluginId, schemaVersion, config }` | 由插件清单声明 schema |

> 设计决策：原项目 `CanvasNodeMetadata` 是一个 40+ 可选字段的扁平袋，读取处到处做 `metadata?.xxx` 判断。
> 重写后每个节点类型的配置是**判别联合**，TS 侧类型收窄天然成立，服务端可直接做 schema 校验。

### 2.2 连线（Edge）

```go
type Edge struct {
    ID         string
    From       Endpoint  // {NodeID, PortID}
    To         Endpoint
    Kind       ResourceKind
    CreatedBy  string
    CreatedAt  time.Time
}
```

原项目连线只记 `fromNodeId/toNodeId`，端口靠隐式约定；重写后显式声明 Port，
使「一个节点多个输入端口」与「类型不匹配校验」成为可能。

### 2.3 运行（Run）

```go
type Run struct {
    ID          string
    WorkspaceID string
    CanvasID    string
    Trigger     RunTrigger    // manual | replay | schedule | agent
    TargetNodes []string
    Status      RunStatus     // pending|running|succeeded|failed|canceled|partial
    Params      RunParams     // 参数快照，保证可重放
    Inputs      []RunInput    // 上游内容快照（文本/资产 hash）
    Steps       []Step
    Usage       Usage         // token / 图片张数 / 视频秒数 / 估算成本
    Error       *RunError
    StartedAt   time.Time
    FinishedAt  *time.Time
}

type Step struct {
    ID         string
    RunID      string
    NodeID     string
    Kind       StepKind       // generate | transform | fetch | agent
    Status     StepStatus
    DependsOn  []string       // DAG 依赖（Step ID）
    Attempts   []Attempt
    Outputs    []AssetRef     // 产出的资产
    StartedAt  time.Time
    FinishedAt *time.Time
}

type Attempt struct {
    Index        int
    ProviderID   string
    ModelID      string
    RequestID    string       // 幂等键
    Status       AttemptStatus
    HTTPStatus   int
    LatencyMs    int
    Error        *ProviderError
    TokensIn     int
    TokensOut    int
    CostMicros   int64        // 用整数避免浮点误差
}
```

**为什么要有 Run**：原项目生成失败后只能「重试」，没有历史；用户无法对比两次生成、无法知道花了多少钱、Agent 也无法引用「上一次运行的结果」。Run 让执行成为可追溯资产。

状态机（后端强约束，非法转移直接报错）：

```
Run:  pending → running → { succeeded | failed | canceled }
                             running → partial（部分步骤失败但兼容模式允许）

Step: pending → ready → running → { succeeded | failed | skipped | canceled }
                             running → retrying → running
```

### 2.4 资产（Asset）

```go
type Asset struct {
    ID          string
    WorkspaceID string
    Kind        AssetKind     // image | video | audio | text | json | file
    Hash        string        // sha256，内容寻址
    Size        int64
    MIME        string
    Meta        AssetMeta     // 宽高/时长/EXIF 脱敏/主色
    Origin      AssetOrigin   // upload | generated | imported | derived
    SourceRef   *SourceRef    // 生成来源：RunID/StepID/模型
    Thumbnails  []ThumbRef
    CreatedAt   time.Time
}
```

关键点：

- **内容寻址**：同一张图在两个画布、被两个人上传，物理只存一份。
- **引用计数**：`asset_refs(asset_id, ref_type, ref_id)` 表，删除画布不立即删 Blob，GC 异步清理。
  （原项目在前端做 `cleanupImages`，逻辑正确但无法跨设备，且要遍历 IndexedDB 全量对比。）
- **派生关系**：裁剪、放大、拆分的产物记录 `derived_from`，形成血缘，便于「回到原图」。

### 2.5 Provider 与凭据

```go
type Provider struct {
    ID          string          // "openai" | "gemini" | "custom-xxx"
    Kind        ProviderKind    // builtin | custom_script
    Capabilities []Capability   // image.generation | image.edit | video.* | text.* | audio.*
    BaseURL     string
    AuthKind    AuthKind        // bearer | header | query | none
    Script      *ScriptSpec     // 自定义调用脚本（js/go template）
}

type ProviderCredential struct {
    ID          string
    WorkspaceID string
    ProviderID  string
    Name        string
    SecretRef   string     // 指向密钥存储的引用，不落明文
    Priority    int        // 路由优先级
    Limits      Limits     // 并发上限 / QPS / 日额度
    Enabled     bool
}
```

**安全设计**：API Key 不再返回到前端，前端也永远拿不到。
- 存储：`secretRef` 指向 DB 中 AES-GCM 加密后的密文（key 来自 KMS / 环境变量），或外部 vault。
- 回显：接口只返回 `sk-...last4` 形式的掩码。
- 使用：仅 exec 层在调用 Provider 前解密，且日志脱敏（`http` 层统一 redact）。

这解决了原项目最大的安全短板：Key 在浏览器 localStorage 且前端直连第三方，
任何一次 XSS 就是密钥泄露，也无法防止用户误把 Key 分享给中转站。
若用户坚持本地直连模式，保留一个显式的 `local-direct` 模式，但默认关闭。

### 2.6 提示词（Prompt）

```go
type PromptSource struct {  // 来源仓库
    ID, Name, URL, Format, RefreshInterval string
    Enabled bool
    LastSyncedAt *time.Time
    Status  SyncStatus
}

type Prompt struct {
    ID          string
    SourceID    string
    ExternalID  string
    Title       string
    Tags        []string
    Content     string      // 提示词正文，含变量占位
    Variables   []PromptVar // 从正文解析出的变量
    CoverRef    *AssetRef
    Results     []AssetRef
    Hash        string      // 去重
}
```

原项目由**浏览器直连** 7 个提示词仓库并缓存到 IndexedDB。
重写后由服务端定期抓取 + 归一化 + 全文检索（Postgres `tsvector` 或 `pg_trgm`），
前端只查询。好处：抓取失败重试、跨端一致、支持变量解析与去重。

### 2.7 Agent 会话

```go
type AgentSession struct {
    ID          string
    WorkspaceID string
    CanvasID    string
    Backend     AgentBackend  // codex | claude_code | remote_http
    ThreadID    string
    Title       string
    Turns       []Turn
}

type Turn struct {
    ID        string
    Seq       int
    Input     TurnInput       // 文本 + 附件资产引用
    Items     []Item          // agent_message / reasoning / tool_call / tool_result / error
    Status    TurnStatus
    Usage     Usage
    CreatedAt time.Time
}
```

原项目已经踩过坑并在 AGENTS.md 沉淀了规则：「消息必须同时按 `threadId`、`turnId`、`itemId` 归属；
实时事件只用于补充未物化的 turn，历史快照成为权威后不得重复合并」。
重写直接把这套语义**建模到数据结构里**：Turn 是聚合根，Item 以 `(turnId, itemId)` 唯一，
实时事件与历史快照都写入同一张 `agent_items` 表，用 `source` 字段标记来源，合并策略由主键保证幂等。

## 3. 数据库表（Postgres）

```
users(id, email, name, avatar_ref, created_at)
workspaces(id, name, slug, owner_id, plan, settings, created_at)
workspace_members(workspace_id, user_id, role, created_at)   -- role: owner|admin|editor|viewer

projects(id, workspace_id, name, description, cover_asset_id, visibility, created_by, created_at, updated_at, deleted_at)
canvases(id, project_id, name, version, settings, thumb_asset_id, created_at, updated_at)
canvas_docs(canvas_id, version, doc jsonb, created_at)         -- 增量快照，保留最近 N 个
canvas_ops(id, canvas_id, seq, actor_id, op jsonb, created_at) -- op 日志，用于回溯与协同
canvas_presence(canvas_id, user_id, cursor, selection, updated_at)

assets(id, workspace_id, kind, hash, size, mime, meta, origin, source_run_id, created_at)
asset_refs(asset_id, ref_type, ref_id, created_at)
asset_derivations(child_asset_id, parent_asset_id, op)

providers(id, workspace_id, kind, name, capabilities, base_url, auth_kind, script, enabled)
provider_credentials(id, workspace_id, provider_id, name, secret_ref, priority, limits, enabled)

runs(id, workspace_id, canvas_id, trigger, status, params, inputs, usage, error, started_at, finished_at)
run_steps(id, run_id, node_id, kind, status, depends_on, started_at, finished_at)
run_attempts(id, step_id, idx, provider_id, model_id, request_id, status, http_status, latency_ms, tokens_in, tokens_out, cost_micros, error)

prompt_sources(...)   prompts(...)   prompt_sync_logs(...)

plugins(id, workspace_id, plugin_key, version, manifest, permissions, enabled, installed_by)
plugin_installs(...)

agent_sessions(id, workspace_id, canvas_id, backend, thread_id, title, created_at)
agent_turns(id, session_id, seq, status, usage, created_at)
agent_items(id, turn_id, item_id, seq, kind, payload, source, created_at)

api_keys(id, workspace_id, user_id, name, hash, scopes, last_used_at, expires_at)
audit_logs(id, workspace_id, actor_id, action, target_type, target_id, detail, ip, created_at)
```

索引要点：

- `canvas_ops(canvas_id, seq)` 唯一索引，保证 op 顺序与幂等。
- `assets(workspace_id, hash)` 唯一索引，内容寻址去重。
- `asset_refs(ref_type, ref_id)` 用于反查与 GC。
- `run_attempts(request_id)` 唯一索引，保证 Provider 调用幂等。
- `prompts` 建 GIN 索引在 `to_tsvector(content)` 与 `tags`。

## 4. 与原项目的数据迁移

原项目数据在浏览器 IndexedDB（`infinite-canvas:canvas_store` / `asset_store` / `image_files` / `media_files`）。

迁移策略：**前端一次性导入器** + **服务端兼容导入端点**。

1. 前端在首次登录后检测本地旧数据，提示「导入到工作区」。
2. 前端在浏览器内把 Blob 逐个上传到 `POST /assets`（保留进度与断点）。
3. 前端把画布 JSON 用旧 → 新 schema 转换器转换（`migrateLegacyCanvas`），
   图片/视频节点由 `content/storageKey` 映射到 `assetId`。
4. 转换后的文档 `POST /canvases/import`，服务端校验并写入。
5. 导入完成后在本地保留旧数据 30 天（只标记不再使用），不做破坏性删除。

旧 schema 的兼容代码**只存在于一个 `legacy/` 目录**，业务代码不感知。
（原项目 AGENTS.md 写「项目尚未上线，不需要兼容旧数据」，但重写意味着已有用户，所以本期明确保留一次迁移。）
