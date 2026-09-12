# 08 · 基础设施与工程规范

## 1. 配置

单一配置源，优先级：`命令行参数 > 环境变量 > config.yaml > 默认值`。

```
IC_MODE=standalone|cluster
IC_LISTEN=:8080
IC_PUBLIC_URL=https://ic.example.com
IC_DB_DSN=postgres://...
IC_REDIS_URL=redis://...
IC_BLOB_DRIVER=fs|s3
IC_BLOB_FS_ROOT=/var/lib/ic/assets
IC_S3_ENDPOINT= / IC_S3_BUCKET= / IC_S3_REGION=
IC_SECRET_KEY=<32B base64>          # 凭据加密主密钥
IC_ENABLE_WORKER=true
IC_WORKER_CONCURRENCY=8
IC_SESSION_COOKIE_SECURE=true
IC_ALLOW_REGISTRATION=false
IC_PLUGIN_REGISTRY=https://registry.example.com/index.json
IC_AGENT_LOCAL_ALLOWED=true          # 是否允许连接本机 Agent
IC_LOG_LEVEL=info / IC_LOG_FORMAT=json
```

启动时做**配置校验**（必填项、互斥项、密钥长度），失败即退出并打印可读原因。

## 2. 部署

### 2.1 单机（推荐给个人用户）

```yaml
# docker-compose.yml
services:
  ic:
    image: registry.example.com/ic:latest
    ports: ["8080:8080"]
    environment:
      IC_MODE: standalone
      IC_DB_DSN: file:/data/ic.db
      IC_BLOB_DRIVER: fs
      IC_BLOB_FS_ROOT: /data/assets
      IC_SECRET_KEY: ${IC_SECRET_KEY:?required}
    volumes: ["./data:/data"]
    restart: unless-stopped
```

一个容器跑完，前端静态产物由 Go 服务托管（`//go:embed`），`/api` 同源，无 CORS。

### 2.2 集群

```
LB / Ingress
   ├─ ic-server ×N     （无状态，SSE 通过 Redis Pub/Sub 跨实例扇出）
   ├─ ic-worker ×N     （执行 Run，可独立扩缩）
   └─ Postgres / Redis / S3(MinIO)
```

- `ic-server` 与 `ic-worker` 是**同一个二进制**，用 `IC_ROLE=api|worker|all` 区分。
- SSE 跨实例：`eventbus` 同时实现「进程内」与「Redis Pub/Sub」两种，接口一致。
- 健康检查：`/healthz`（存活）、`/readyz`（依赖就绪）、`/metrics`（Prometheus）。

### 2.3 构建

- 多阶段 Dockerfile：`golang:1.24` 构建 → `node:22` 构建前端 → `gcr.io/distroless/static` 运行。
- 版本注入：`-ldflags "-X main.version=..."`，`/api/v1/meta` 暴露版本与 commit。
- 提供 `make dev`（前后端热重载）、`make test`、`make lint`、`make gen`。

## 3. 可观测

| 维度 | 方案 |
| --- | --- |
| 日志 | `slog` JSON，含 `trace_id/workspace_id/run_id/step_id`，统一脱敏 |
| 追踪 | OpenTelemetry：HTTP → graph → exec → provider 全链路 span |
| 指标 | 请求 P50/P95/P99、SSE 连接数、队列深度/延迟、Run 成功率、Provider 错误率与延迟、资产存储量 |
| 告警 | 队列积压、Provider 错误率突增、磁盘水位、SSE 连接泄漏 |
| 审计 | `audit_logs`：凭据变更、成员变更、导出、Agent 工具调用、插件安装 |

前端：错误边界上报（含 trace_id 回显），方便「用户报错 → 用 trace 定位后端」。

## 4. 安全清单

- [ ] 凭据加密存储，接口只回显掩码；前端永不接触明文 Key。
- [ ] 所有写接口校验 workspace 成员角色。
- [ ] 文件名/路径不进 Blob 存储路径（用 hash 命名），杜绝路径穿越。
- [ ] 上传做 MIME 嗅探与大小限制，SVG 走静态化处理或单独域展示。
- [ ] SSE 与文件下载接口做速率限制与连接数上限。
- [ ] CSP：`default-src 'self'`，插件 iframe 无 `allow-same-origin`。
- [ ] SSRF：自定义脚本与 Provider Base URL 禁止访问私网段（可配置白名单，供内网自部署放开）。
- [ ] 依赖审计：Go `govulncheck`、前端 `npm audit` 进 CI。
- [ ] 备份：DB 每日快照 + Blob 增量；提供 `ic export` / `ic import` 全量搬运。

## 5. 测试策略

| 层 | 工具 | 覆盖目标 |
| --- | --- | --- |
| Go 单测 | `go test` | graph op 校验 / DAG 编译 / 重试 / 计量 |
| Go 集成 | `testcontainers` | DB + Redis + MinIO 真实依赖 |
| Provider 契约 | `httptest` + 录制样本 | 解析与错误分类 |
| 前端内核 | `vitest` | 几何、命中测试、状态机迁移 |
| 前端组件 | `vitest` + Testing Library | 关键面板 |
| E2E | `playwright` | 建画布 → 连节点 → 运行 → 查看结果 → Agent 操作 |
| 性能 | `k6` / 自研脚本 | SSE 500 并发连接、画布 5000 节点滚动 |

性能预算（写进 CI 门禁）：

- 画布视口操作 ≥ 55 FPS（5000 节点场景）。
- 打开画布（1000 节点）首屏可交互 < 1.5s。
- 编辑 op 的本地反馈延迟 < 16ms。

## 6. 工程规范

- **契约先行**：改 API 必须先改 `contracts/openapi.yaml`，`make gen` 生成 Go DTO 校验与 TS 客户端，CI 校验生成结果已提交。
- **迁移**：DB 变更一律写 `migrations/NNNN_xxx.up.sql` / `down.sql`，CI 校验可回滚。
- **错误码**：新增 `code` 必须同时在 `contracts/errors.yaml` 声明并附 i18n key。
- **提交规范**：Conventional Commits；PR 必须包含「为什么 / 改了什么 / 怎么验证」。
- **代码规模纪律**：单文件 > 500 行必须在 PR 里说明；`features/*` 不允许跨目录深层 import。
- **文档即代码**：本文档目录随架构演进同步更新，ADR（架构决策记录）放 `docs/adr/`。

## 7. 目录总览

```
ic/
  cmd/ic-server/
  cmd/ic-cli/                # 运维 CLI：迁移、导入导出、密钥轮换
  internal/{api,graph,exec,provider,asset,plugin,agent,identity,eventbus,platform}/
  contracts/{openapi.yaml,events.yaml,errors.yaml,plugin-api.ts,ops.schema.json}
  migrations/
  web/                       # React 前端
    src/{app,features,shared}/
  canvas-agent/              # 本机桥接器（保留独立发布）
  deploy/{docker-compose.yml,Dockerfile,k8s/}
  docs/{design,adr,guides}/
  testdata/workflows/
  Makefile
```
