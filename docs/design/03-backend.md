# 03 · Go 后端设计

## 1. 进程与装配

```
cmd/ic-server/main.go
  ├─ 加载配置（env + yaml），校验必填
  ├─ 初始化 platform：logger / tracer / db / redis / blobstore
  ├─ 运行迁移（可关闭，默认开启）
  ├─ 装配内部模块：identity → asset → graph → provider → exec → plugin → agent
  ├─ 启动 HTTP server（含 SSE hub）
  ├─ 启动 worker pool（若 ENABLE_WORKER=true）
  └─ 等待 SIGTERM → 停止接流 → 等待在途 Run（超时上限）→ 关闭连接池
```

单机模式通过 `IC_MODE=standalone` 使用 SQLite + 本地 FS + 内存队列，
其余代码路径完全一致。

## 2. 模块职责

### 2.1 `internal/graph`

画布文档的领域核心，不依赖 HTTP。

```go
type Service interface {
    Create(ctx, projectID, name string) (*Canvas, error)
    Get(ctx, canvasID string, version int64) (*CanvasDocument, error)
    ApplyOps(ctx, canvasID string, baseVersion int64, ops []Op) (ApplyResult, error)
    Subscribe(ctx, canvasID string) (<-chan Event, func(), error)
}
```

Op 就是原项目 `CanvasAgentOp` 的**严格化版本**，每个 op 都有明确的类型与校验：

```go
type Op interface{ Kind() OpKind }

type AddNodeOp   struct{ Node Node }
type RemoveNodeOp struct{ NodeID string; Cascade bool }
type MoveNodeOp  struct{ ID string; Rect Rect }
type ResizeNodeOp struct{ ID string; Rect Rect; KeepAspect bool }
type SetSpecOp   struct{ ID string; SpecPatch json.RawMessage }  // 按节点类型 schema 校验
type AddEdgeOp   struct{ Edge Edge }
type RemoveEdgeOp struct{ ID string }
type GroupOp     struct{ NodeIDs []string; GroupID string }
type SetViewportOp struct{ Viewport Viewport }

type ApplyResult struct {
    Version    int64
    Applied    []Op
    Rejected   []OpError
    Inverse    []Op     // 供前端 undo / 服务端回滚
}
```

校验规则（服务端权威）：

- 端口类型必须匹配（`image` 不能连到声明 `text` 的端口）。
- `Required` 输入端口在生成前必须满足（编辑器允许暂存，执行时拒绝）。
- 分组不允许循环包含。
- 单入端口重复接线时按「替换」语义处理并返回 warning。
- 每条 op 都记录 `actor_id`，为审计与协同埋点。

**版本与冲突**：`baseVersion` 用于乐观并发。
若 `baseVersion != current`，进入三种策略：

1. 提交的 op 集合与当前 op 日志可交换（如移动不同节点）→ 自动 rebase 后应用，返回新版本。
2. 存在语义冲突（同一节点被同时改 spec）→ 返回 `409 conflict`，携带服务端权威文档；
   前端展示「你的改动与他人冲突」并让用户选择保留哪一版。
3. 显式 `force=true` 用于「我需要覆盖」的场景，写审计日志。

> 后续升级到 CRDT 时，op 语义可直接作为 CRDT 操作，`canvas_ops` 日志即 op-based CRDT 的底层。

### 2.2 `internal/exec`

职责：把「图的一部分」编译成 DAG，然后可靠地跑完。

```go
type Compiler interface {
    Compile(doc *CanvasDocument, targets []string) (*Plan, error)
}

type Plan struct {
    Steps  []PlanStep   // 拓扑序
    Inputs map[string][]ResolvedInput
}

type Scheduler interface {
    Submit(ctx, run *Run, plan *Plan) error
    Cancel(ctx, runID string) error
    Retry(ctx, runID string, stepID string) error
}
```

- 编译期做「输入解析」：把上游 `prompt.text`、`image.assetId` 解析成具体值，
  写入 `Run.Params`/`Run.Inputs`，保证重放时不受画布后续修改影响。
- 执行期做「能力路由」：`capability=image.generation` → 取该 workspace 可用凭据 → 按 priority + 健康度选择。
- 重试策略：`transient`（网络/429/5xx）指数退避，最多 N 次；`permanent`（4xx 语义错误）不重试。
  超时、重试次数、并发上限**必须可配置**，并在 UI 里可见（原项目 AGENTS.md 也有这条纪律）。
- 取消：context 级联，Provider 调用携带 `context.Context`，HTTP 请求即时中断。
- 成本：每次 attempt 写入 tokens / 数量 / 时长，聚合到 Run，供 UI 与限额使用。

### 2.3 `internal/provider`

```go
type Adapter interface {
    ID() string
    Capabilities() []Capability
    Invoke(ctx context.Context, req Request) (Response, error)
    Stream(ctx context.Context, req Request) (Stream, error) // 文本流式
    Poll(ctx context.Context, taskID string) (TaskStatus, error) // 异步视频
}
```

内置适配器：

| Adapter | 覆盖 |
| --- | --- |
| `openai` | `/v1/images/generations`、`/v1/images/edits`、`/v1/responses`、`/v1/videos`、`/v1/audio/*`、`/v1/models` |
| `gemini` | `generateContent` / `predictLongRunning`，含 `generationConfig.imageConfig` |
| `script` | 用户自定义脚本（沙箱执行，见下） |

**统一能力枚举**（避免原项目内部散落的字符串判断）：

```go
type Capability string
const (
  CapImageGenerate Capability = "image.generate"
  CapImageEdit     Capability = "image.edit"
  CapImageUpscale  Capability = "image.upscale"
  CapVideoGenerate Capability = "video.generate"
  CapTextGenerate  Capability = "text.generate"
  CapTextTool      Capability = "text.tools"      // 支持 function calling
  CapAudioGenerate Capability = "audio.generate"
  CapModelList     Capability = "model.list"
)
```

**自定义脚本的演进**：原项目让用户在前端 Codemirror 里写 JS 字符串并 `eval` 执行。
重写改为：

1. **默认模板化**：UI 提供常见中转站模板（字段映射表单），覆盖 90% 场景，不写代码。
2. **高级脚本**：可选 JS 脚本，在服务端隔离沙箱（`goja` 或独立进程 + `--no-network` 之外的白名单）执行，
   仅允许通过注入的 `http` 能力发起请求，禁止文件系统与进程访问。
3. 脚本有显式版本与参数 schema，调用记录里标注脚本版本，便于排障。

### 2.4 `internal/asset`

- 写入：流式接收 → 分片算 hash → 命中则复用（秒传）→ 未命中写 Blob 存储 → 落元数据。
- 读取：`GET /assets/{id}/raw` 支持 `Range`、`ETag`、`Cache-Control: immutable`。
  （原项目 blob URL 一刷新就换，且无法跨设备；重写后资产 URL 稳定可缓存。）
- 缩略图：图片生成多档 webp；视频抽首帧；异步任务。
- GC：引用计数归零 + 超过保留期 → 回收 Blob，先标记后删除，可恢复窗口 7 天。

### 2.5 `internal/api`

路由风格（OpenAPI 3.1 为唯一真源）：

```
POST   /api/v1/auth/login | /logout | /refresh
GET    /api/v1/me

GET    /api/v1/workspaces
POST   /api/v1/workspaces
GET    /api/v1/workspaces/{wid}/members

GET    /api/v1/workspaces/{wid}/projects
POST   /api/v1/workspaces/{wid}/projects
GET    /api/v1/projects/{pid}
PATCH  /api/v1/projects/{pid}
DELETE /api/v1/projects/{pid}

POST   /api/v1/projects/{pid}/canvases
GET    /api/v1/canvases/{cid}
PATCH  /api/v1/canvases/{cid}                 # 元数据
POST   /api/v1/canvases/{cid}/ops             # 批量 op 提交（幂等键）
GET    /api/v1/canvases/{cid}/ops?since=seq   # 增量拉取
POST   /api/v1/canvases/{cid}/import          # 导入（含旧版迁移）
GET    /api/v1/canvases/{cid}/export          # 导出 JSON / zip
GET    /api/v1/canvases/{cid}/events          # SSE 多路复用通道

POST   /api/v1/canvases/{cid}/runs            # 触发运行
GET    /api/v1/runs/{rid}
POST   /api/v1/runs/{rid}/cancel
POST   /api/v1/runs/{rid}/replay
GET    /api/v1/canvases/{cid}/runs?limit=&cursor=

POST   /api/v1/workspaces/{wid}/assets        # 上传（支持分片）
GET    /api/v1/assets/{aid}
GET    /api/v1/assets/{aid}/raw
GET    /api/v1/assets/{aid}/thumb?w=
DELETE /api/v1/assets/{aid}

GET    /api/v1/workspaces/{wid}/providers
POST   /api/v1/workspaces/{wid}/providers
POST   /api/v1/workspaces/{wid}/providers/{pid}/credentials
POST   /api/v1/workspaces/{wid}/providers/{pid}/test    # 连通性测试
GET    /api/v1/workspaces/{wid}/models?capability=

GET    /api/v1/workspaces/{wid}/prompt-sources
POST   /api/v1/workspaces/{wid}/prompt-sources/{sid}/sync
GET    /api/v1/prompts?q=&tags=&cursor=

GET    /api/v1/workspaces/{wid}/plugins
POST   /api/v1/workspaces/{wid}/plugins/{key}/enable

POST   /api/v1/agent/sessions
POST   /api/v1/agent/sessions/{sid}/turns
POST   /api/v1/agent/sessions/{sid}/turns/{tid}/approve
GET    /api/v1/agent/sessions/{sid}/history
```

约定：

- 列表统一游标分页 `?limit=&cursor=`，不返回 total（避免大表 count）。
- 写操作支持 `Idempotency-Key` 头，服务端 24h 内去重。
- 错误统一 `{ code, message, details, traceId }`，`code` 为稳定枚举。
- 所有响应带 `X-Trace-Id`。

### 2.6 实时通道（SSE 多路复用）

只开**一条** SSE 连接承载所有事件（原项目机制分散：页面用 SSE 连本地 Agent，其余靠轮询/本地状态）。

```
GET /api/v1/canvases/{cid}/events
Accept: text/event-stream

event: canvas.op            data: {"seq":42,"op":{...},"actor":"u_1"}
event: run.step             data: {"runId":"r_1","stepId":"s_2","status":"running"}
event: run.step.delta       data: {"stepId":"s_2","chunk":"text..."}   # 文本流
event: asset.created        data: {"assetId":"a_9","kind":"image"}
event: presence             data: {"userId":"u_2","cursor":{...}}
event: plugin.event         data: {"plugin":"xxx","name":"evt","payload":{}}

id: 1042                    # 断线重连用 Last-Event-ID 续传
retry: 3000
```

实现要点：

- 每个连接一个 `Hub` 订阅，channel 缓冲 + 背压策略（慢消费者丢弃 `presence` 等可丢失事件，保留 `canvas.op`/`run.*`）。
- 心跳：15s 注释帧 `:hb`，避免中间代理超时。
- 事件按 `canvas_id` 分区，仅在订阅了该画布的连接上投递。
- 断线重连：`Last-Event-ID` 对应 `canvas_ops.seq`，服务端重放缺失事件。

## 3. 鉴权与权限

- 认证：Session Cookie（HttpOnly + SameSite=Lax）优先；API 场景用 `Authorization: Bearer <api_key>`。
- 授权：以 Workspace 为边界，角色 `owner > admin > editor > viewer`。
  - `viewer`：只读画布与资产。
  - `editor`：编辑画布、运行工作流、上传资产。
  - `admin`：管理 Provider 凭据、成员、插件。
  - `owner`：删除工作区、转移所有权。
- 中间件链：`TraceID → Recover → Logger → Auth → WorkspaceScope → RateLimit → Handler`。
- 敏感操作（改凭据、删除工作区、导出全量数据）写 `audit_logs`。

## 4. 错误与可观测

```go
type DomainError struct {
    Code    string   // 稳定枚举，前端按 code 做本地化
    Message string   // 面向用户的英文兜底
    Details map[string]any
    Cause   error
}
```

- 前端 i18n 按 `code` 映射文案，不解析 message。
- `log/slog` 结构化日志，字段含 `trace_id / workspace_id / run_id / provider_id`。
- 指标：请求 P99、SSE 连接数、队列深度、Run 成功率、Provider 各模型延迟与错误率。
- 日志脱敏：HTTP 层统一 redact `authorization`、`api_key`、`x-api-key`、base64 图片串。

## 5. Go 包结构示例

```
internal/graph/
  doc.go            领域文档类型
  op.go             op 定义与校验
  service.go        用例编排
  store.go          DB 读写（sqlc 生成 + 手写查询）
  rebase.go         冲突 rebase
  event.go          领域事件

internal/exec/
  plan.go           编译结果类型
  compiler.go       图 → DAG
  scheduler.go      调度
  worker.go         执行循环
  retry.go          重试策略
  usage.go          计量

internal/provider/
  capability.go     能力枚举
  registry.go       Provider 注册表与路由
  adapter/
    openai/…
    gemini/…
    script/…
  transport.go      HTTP 客户端、超时、重试、脱敏
```
