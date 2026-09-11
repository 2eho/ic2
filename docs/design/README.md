# IC 重写 · 顶层设计索引

本目录存放 `context-flow.cloud/ic` 的顶层设计文档。

| 文件 | 内容 |
| --- | --- |
| `00-overview.md` | 目标、范围、非目标、设计原则、分期 |
| `01-architecture.md` | 整体架构、分层、部署形态、技术选型 |
| `02-domain-model.md` | 领域模型与数据模型（Graph / Run / Asset / Workspace） |
| `03-backend.md` | Go 后端：模块划分、包结构、API、实时通道、任务系统 |
| `04-frontend.md` | React 前端：渲染内核、状态分层、编辑器设计 |
| `05-execution-engine.md` | 执行引擎：DAG 调度、Provider 适配、成本与限流 |
| `06-plugin-sdk.md` | 插件系统与 SDK 契约 |
| `07-agent-protocol.md` | Agent Bridge / MCP 协议与安全边界 |
| `08-infra.md` | 部署、可观测、配置、密钥、测试与工程规范 |
| `09-roadmap.md` | 里程碑与验收标准 |

> 本设计针对 `github.com/basketikun/infinite-canvas`（v0.18.0，MIT）做**重写**，不是改造。
> 目标是拿到可长期演进的架构：AI 工作流的执行、资产、协作都成为一等公民。
