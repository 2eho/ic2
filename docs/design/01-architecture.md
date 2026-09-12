# 01 · 整体架构

## 1. 系统上下文

```
                    ┌───────────────────────────────────────────────┐
                    │                  Browser                      │
                    │  React 19 + Vite + TS                         │
                    │  ┌───────────────┐  ┌─────────────────────┐   │
                    │  │ Canvas Kernel │  │ Query/Store 数据层   │   │
                    │  │ (DOM + SVG)   │  │ (SSE 事件 → 缓存)    │   │
                    │  └───────────────┘  └─────────────────────┘   │
                    │  ┌───────────────┐  ┌─────────────────────┐   │
                    │  │ Plugin Host   │  │ Agent Sidebar       │   │
                    │  │ (iframe 沙箱) │  │ (会话 / 工具确认)    │   │
                    │  └───────────────┘  └─────────────────────┘   │
                    └───────┬──────────────────┬────────────────────┘
                            │ REST + SSE       │ localhost HTTP
                            ▼                  ▼
        ┌───────────────────────────────┐  ┌────────────────────────────┐
        │        IC Server (Go)         │  │  Canvas Agent (本机,可选)   │
        │  ┌─────────────────────────┐  │  │  MCP Server (stdio)        │
        │  │ api      HTTP/SSE/文件   │  │  │  Codex / Claude 适配        │
        │  ├─────────────────────────┤  │  └────────────────────────────┘
        │  │ graph    画布文档/同步    │  │
        │  ├─────────────────────────┤  │
        │  │ exec     工作流/运行/队列 │  │
        │  ├─────────────────────────┤  │
        │  │ provider 模型适配/路由    │  │
        │  ├─────────────────────────┤  │
        │  │ asset    资产存储/转码    │  │
        │  ├─────────────────────────┤  │
        │  │ plugin   插件注册/清单    │  │
        │  ├─────────────────────────┤  │
        │  │ identity 用户/工作区/权限 │  │
        │  └─────────────────────────┘  │
        └──────┬───────────┬────────────┘
               │           │
        ┌──────▼───┐  ┌────▼─────┐  ┌──────────────┐
        │ Postgres │  │  Redis   │  │ S3/FS/MinIO  │
        │ 领域数据  │  │队列/事件  │  │ 资产 Blob     │
        └──────────┘  └──────────┘  └──────────────┘
               │
               ▼
        ┌────────────────────────────────────────┐
        │ 外部 Provider: OpenAI / Gemini / 中转站  │
        └────────────────────────────────────────┘
```

依赖方向（严格单向，靠 import 规则与 CI lint 约束）：

```
api ──▶ graph ──▶ 无
      ├▶ exec ──▶ graph, provider, asset
      ├▶ provider ──▶ (仅标准库 + HTTP client)
      ├▶ asset ──▶ (存储抽象)
      ├▶ plugin ──▶ graph
      └▶ identity ──▶ 无
```

## 2. 后端分层

```
cmd/ic-server/           进程入口、依赖装配、优雅退出
internal/api/            HTTP 路由、请求/响应 DTO、SSE、鉴权中间件
internal/graph/          画布文档领域模型、op 应用、版本、快照、冲突合并
internal/exec/           工作流编译（图 → DAG）、调度、Run/Step 状态机、重试、取消
internal/provider/       能力枚举、Provider 注册表、适配器（openai/gemini/script）
internal/asset/          内容寻址存储、元数据、缩略图、引用计数 GC
internal/plugin/         插件清单校验、版本、权限、注册表分发
internal/agent/          MCP Server、Agent 会话编排、工具审批网关
internal/identity/       用户、工作区、成员、角色、API Key
internal/eventbus/       领域事件发布/订阅（进程内 + Redis）
internal/platform/       配置、日志、追踪、错误、ID、HTTP client、DB、Redis
pkg/icclient/            对外可复用的 Go 客户端（同时也给 e2e 测试用）
```

分层规则：

- Handler 不写业务：只做 DTO ↔ 领域对象转换、错误映射、鉴权校验。
- 领域对象不依赖 HTTP 类型；不允许 `internal/graph` import `internal/api`。
- 跨模块协作走**接口**，具体实现由 `cmd/ic-server` 装配注入。
- 副作用（DB / Redis / HTTP / 文件）统一通过 platform 层的包装，便于测试替换。

## 3. 前端分层

```
src/app/            路由、Provider、全局错误边界
src/features/canvas/     画布特性：内核 + 组件 + 交互 + 面板
    kernel/          渲染内核（不依赖 React）：视口、命中测试、渲染器、命令总线
    components/      节点外壳、连线、小地图、工具栏（React）
    hooks/           视口/选择/剪贴板/快捷键等交互 hook
src/features/workflow/   执行编排视图：运行面板、步骤时间线、成本
src/features/assets/     资产库
src/features/prompts/    提示词库
src/features/settings/   渠道、凭据、偏好
src/features/agent/      侧边栏会话、工具确认
src/features/plugins/    插件管理
src/shared/          api 客户端、SSE 客户端、UI 基础组件、i18n、工具
```

核心约束：**画布内核不认识 React**。
内核暴露命令式 API（`viewport.set`、`hitTest`、`render`），React 只负责「把节点转成 DOM 子树」与「把 UI 事件转成内核命令」。这样视口变换、框选、缩放不会触发整棵 React 树重渲染。

## 4. 数据流

### 4.1 编辑（写）

```
用户操作
  → 内核生成领域 op（add_node / move_node / connect / set_meta ...）
  → 本地乐观应用 + 压入 undo 栈
  → 批量合并（rAF 节流）后 POST /canvases/{id}/ops
  → 服务端校验 + 应用 + 版本号 +1
  → 发布 domain event → SSE 广播回所有在线客户端
  → 前端用权威版本做一次校正（仅在冲突时生效）
```

### 4.2 生成（执行）

```
用户在节点触发生成
  → POST /canvases/{id}/runs         （携带目标节点 + 参数快照）
  → exec 编译子图 → 创建 Run + Step
  → 入队（Redis Stream / 内存队列）
  → worker 执行：取上游输入 → provider 调用 → 结果资产入库
  → 每步状态通过 SSE 事件推送：run.started / step.delta / step.succeeded / run.finished
  → 前端把结果资产挂到节点（节点只保存 asset_id）
```

关键点：**同一次生成的结果以不可变 Run 记录**，节点保存的是「引用了哪个 Run 产出的哪个 Asset」，因此可以随时回看、对比、重放。

## 5. 部署形态

| 形态 | 组成 | 适用 |
| --- | --- | --- |
| 单机 Docker | 一个进程（内嵌 SQLite + 本地 FS） | 个人自部署 |
| 标准部署 | ic-server ×N + Postgres + Redis + S3 | 团队/生产 |
| 前后端一体 | 前端静态产物由 Go 服务托管，`/api` 同源 | 简化部署，避免 CORS |

单机模式不是「阉割版」：`internal/platform` 对 DB 与 Blob 都以接口暴露，SQLite/本地 FS 是一等实现。

## 6. 技术选型

| 层 | 选型 | 理由 |
| --- | --- | --- |
| 后端语言 | Go 1.24+ | 并发模型适合长时任务与 SSE 多路复用，部署单体二进制 |
| HTTP | `net/http` + 自定义中间件 | 标准库路由已支持 method+path 模式，减少依赖 |
| DB 访问 | `sqlc` + `pgx` / `database/sql` | 显式 SQL，类型安全，避免 ORM 隐式行为 |
| 迁移 | `golang-migrate` | 与 SQL 文件共存，CI 可校验 |
| 队列 | Redis Stream（集群）/ 内存队列（单机） | 轻量、有 ack、可观测 lag |
| 实时 | SSE（单连接多路复用） | 见 05 章 |
| 对象存储 | S3 兼容（MinIO）/ 本地 FS | 接口隔离 |
| 前端 | React 19 + TypeScript + Vite + Tailwind | 与原作者技术栈一致，降低迁移成本 |
| 前端数据 | TanStack Query + Zustand | 服务端状态与 UI 状态分离 |
| 契约 | OpenAPI 3.1 → `openapi-typescript` | 单一真源 |
| 可观测 | OpenTelemetry（trace/metric）+ `log/slog` | 端到端 trace_id |
