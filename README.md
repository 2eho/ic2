# IC · 无限画布

面向 AI 创作的工作台：无限画布 + 节点连线 + 多模型生成 + 可追溯执行 + 沙箱插件 + Agent 协作。

React 前端 + Go 后端。**服务端权威**：数据、凭据、执行记录都在服务端，
浏览器不保存任何模型密钥。

## 快速开始

```bash
# 单机（推荐）
export IC_SECRET_KEY=$(openssl rand -base64 32)
docker compose -f deploy/docker-compose.yml up -d
# 打开 http://localhost:8080
```

从源码跑：

```bash
make build           # 构建后端
make web-build       # 构建前端
make run             # 本地启动（开发模式）

# 前端热重载 + 后端
make run
make dev
```

## 这是什么

一个把「AI 生成」当**工程问题**而非「按钮回调」来做的重写：

| 维度 | 做法 |
| --- | --- |
| 执行 | 生成 → `Run/Step/Attempt` 三层，可追溯、可重放、可对比 |
| 画布 | 自研内核（不依赖 React 渲染），视口操作零重渲染，5000 节点 ≥55FPS 目标 |
| 资产 | 内容寻址（sha256 去重）+ 引用计数 + 两阶段 GC |
| 凭据 | 服务端 AES-GCM 加密，接口只回显掩码，前端永不接触明文 |
| 插件 | iframe 沙箱（无 `allow-same-origin`）+ 权限声明 + 权限扩大重确认 |
| Agent | 会话/工具/审批网关，写操作与用户编辑走**同一条**校验路径 |
| 契约 | op 语义、错误码、边界常量各有唯一真源，CI 校验不漂移 |

## 文档

| 想了解 | 看 |
| --- | --- |
| 全貌与取舍 | [`docs/design/00-overview.md`](docs/design/00-overview.md) |
| 架构与选型 | [`docs/design/01-architecture.md`](docs/design/01-architecture.md) |
| 功能范围（唯一真源） | [`docs/design/10-parity-matrix.md`](docs/design/10-parity-matrix.md) |
| 边界与对抗 | [`docs/design/11-boundary-and-adversarial.md`](docs/design/11-boundary-and-adversarial.md) |
| 怎么核验 | [`docs/guides/verification.md`](docs/guides/verification.md) |
| 为什么这样选 | [`docs/guides/architecture-decisions.md`](docs/guides/architecture-decisions.md) |
| 部署 | [`docs/guides/deploy.md`](docs/guides/deploy.md) |
| 插件开发 | [`docs/guides/plugin-dev.md`](docs/guides/plugin-dev.md) |

## 目录

```
cmd/ic-server/          进程入口
internal/
  api/                  HTTP 路由、SSE、鉴权中间件
  graph/                画布文档领域核心（op 校验、版本、rebase、重放）
  exec/                 图 → DAG 编译、调度、重试、计量、续查
  provider/             能力枚举 + 传输层 + adatper（openai / gemini）
  asset/                内容寻址存储、缩略图、引用计数、GC
  plugin/               清单校验、权限模型、注册表与分发
  agent/                会话模型、工具协议、审批网关、MCP
  legacy/               旧数据字段级迁移
  identity/             用户、工作区、会话、API Key
  platform/             配置、日志脱敏、SSRF、加密、时钟/ID、迁移
contracts/              OpenAPI / 事件 / 错误码契约
migrations/             数据库迁移
web/src/
  features/canvas/kernel/   画布内核（不 import react）
  features/*/               各功能模块
  shared/                   API 客户端、SSE、i18n
deploy/                 Dockerfile 与 compose
docs/                   design / guides / upstream
scripts/                CI 校验脚本
```

## 核验

```bash
make check    # lint + test + boundaries + parity + sec + drill
```

当前对等矩阵覆盖率 **74.05%**。未完成项在
[`docs/design/10-parity-matrix.md`](docs/design/10-parity-matrix.md) §14 逐条列出原因。

核验不是走过场：本轮迭代中被测试捕获并修复的真实缺陷有 10 个
（含缩放参数形状错位、撤销合并粒度过粗、软删资产导致破图、
GC 误删跨工作区共享 Blob、sqlite 连接池自锁、typed-nil 导致 500 等），
清单见 [`docs/guides/verification.md`](docs/guides/verification.md) 末节。

## 许可与致谢

MIT License（见 [`LICENSE`](LICENSE)）。

本项目是对 [`basketikun/infinite-canvas`](https://github.com/basketikun/infinite-canvas)
（v0.18.0，MIT）的**独立重写**：功能设计参考其已验证的产品形态，
但架构完全重做。仓库中不含上游任何源码；上游仅作为「契约面对照物」由 CI 定时巡检
（见 [`docs/design/12-legacy-and-upstream.md`](docs/design/12-legacy-and-upstream.md)）。

原项目著作权归原作者所有，详见 [`NOTICE`](NOTICE)。
