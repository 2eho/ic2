# 05 · 执行引擎设计

## 1. 为什么要有执行引擎

原项目里「生成」= 组件内的一次 `await generateImage()`：
没有任务实体、没有依赖编排、不能并行、不能取消干净、失败后只有整段重试、没有成本记录。

重写后把生成统一抽象为 **工作流运行（Run）**，画布上的所有生成按钮都只是一个触发器。

## 2. 编译：图 → DAG

### 2.1 编译规则

对一个目标节点集合 `T`：

1. 反向遍历：从 `T` 沿入边向上收集所有「执行型节点」（`generation`、`run`），
   遇到 `prompt`/`image`/`video`/`audio` 等**资源节点**时停止收集，转为步骤的输入。
2. 每个执行型节点生成一个 `Step`，其 `DependsOn` 由节点间路径推导。
3. `group` 节点不产生步骤，只做输入解析时展开（对齐原项目「组节点可整体引用」的语义）。
4. 校验：环检测、必填端口、类型匹配、参数 schema、模型可用性。
   校验失败**在提交前**返回 `422`，附可点击的节点定位信息。

### 2.2 输入解析

```
ResolvedInput {
  kind:   text | image | video | audio | json
  value:  string | AssetRef
  label:  string            // 用于提示词中的引用说明
  origin: { nodeId, portId }
}
```

关键：**解析结果快照进 Run**。
即使之后用户改了上游文本节点，重放这次 Run 仍然用当时的输入。

### 2.3 提示词组装

对齐原项目经验（上游文本按「文本N」分块编号，避免与提示词错位），但改为结构化：

```go
type ComposedPrompt struct {
    Template  string            // 用户写的提示词
    Variables map[string]string // {{var}} 填充值
    Inputs    []ResolvedInput   // 有序输入
}
```

组装顺序由端口 `Order` 决定（前端 ReferenceBar 可拖拽调整），
不再依赖「空行分隔」这种脆弱约定。

## 3. 调度

### 3.1 队列模型

| 模式 | 实现 | 场景 |
| --- | --- | --- |
| standalone | 进程内 `chan` + 有界 worker pool | 个人部署 |
| cluster | Redis Stream + consumer group | 多实例 |

- 任务粒度：`Step`（不是 Run），便于并行与部分成功。
- 优先级：交互式运行 > 批量运行 > 定时同步（提示词抓取、缩略图）。
- 并发控制：按 `provider_credential` 维度限流（原项目只能用户自己控制，容易打爆中转站）。
- 公平性：按 workspace 维度限制在途 Step，防止一个工作区饿死其他工作区。

### 3.2 状态机与重试

```
Step: pending → ready → running ─┬→ succeeded
                                 ├→ retrying → running
                                 ├→ failed        （不可重试错误 / 超过上限）
                                 ├→ skipped        （依赖失败 + 策略为 skip）
                                 └→ canceled
```

重试分类：

| 类别 | 判定 | 策略 |
| --- | --- | --- |
| transient | 网络错误、超时、429、5xx | 指数退避 + 抖动，默认最多 3 次（可配置） |
| rate_limited | 429 且带 Retry-After | 尊重响应头，退避到该时刻 |
| permanent | 400/401/403/404/422 | 不重试，直接失败并给出可读原因 |
| content_policy | 内容审核拒绝 | 不重试，UI 单独提示 |

> 所有边界值（超时、次数、并发上限）集中在 `config/limits.go`，
> 通过 API 暴露给前端展示，并支持工作区级覆盖 —— 不做静默的经验值。

### 3.3 幂等

- 客户端提交 Run 携带 `Idempotency-Key`；服务端 24h 内去重。
- 每次 `Attempt` 生成 `request_id` 传给 Provider（支持时作为 `X-Request-Id`），
  避免「服务端超时但 Provider 已扣费」导致的重复扣费。
- 异步任务（视频）：`videoTaskId` 持久化到 Attempt，进程重启后继续轮询
  （对齐原项目「刷新后继续查询任务状态」的能力）。

### 3.4 取消

- `POST /runs/{id}/cancel` → 设置 Run 状态为 canceling → 向所有在途 Step 的 context 发 cancel。
- 已成功步骤保留结果；未开始的置为 canceled。
- Provider 支持取消时透传（HTTP 断开），不支持时只停止本地等待并记录 `orphaned`。

## 4. 计量与成本

```go
type Usage struct {
    TextTokensIn   int64
    TextTokensOut  int64
    Images         int64
    VideoSeconds   float64
    AudioSeconds   float64
    CostMicros     int64   // 整数微元，避免浮点
}
```

- 价格表由 Provider 定义（`pricing` 字段，可被工作区覆盖）。
- 每次 Attempt 记录计量，聚合到 Step / Run / 日维度。
- 限额：工作区可设日/月额度与单次上限，超限拒绝并给明确提示。
  （原项目因为前端直连，无法做任何限额与风控。）
- 展示：RunPanel 显示「本次消耗」，设置页显示「今日用量」。

## 5. 执行结果回写画布

执行完成不等于「改了画布」。回写走标准 op 路径：

```
Step 成功
  → 产出 Asset（已入库）
  → exec 发出 step.succeeded 事件（含 assetIds）
  → graph 服务按预声明的「回写目标」（节点 ID + 端口）生成 op
  → 走 /canvases/{id}/ops 同一套校验与广播
```

好处：运行产物、手工编辑、Agent 修改**走完全相同的写入路径**，
不存在「某条路径绕过了校验」的隐患（原项目 Agent 操作与实际编辑是两套逻辑）。

## 6. 多结果与变体

原项目「批量生成多张图片时先展示为图片组节点，支持叠卡预览、展开查看全部结果并设置主图」。

重写方案：不再用「组节点 + 主图指针」这种隐式约定，而是：

```go
type GenerationResult struct {
    NodeID   string
    Variants []AssetRef      // 全部结果
    Primary  int             // 主结果索引
}
```

节点 `State.Result` 直接持有变体集合，UI 折叠展示，切换主结果只是改 `Primary`。
批量生成的节点不复制、不建父子关系，避免了原项目 `isBatchRoot/batchRootId/batchChildIds` 这类补丁字段。

## 7. 可测试性

- Provider 适配器对 HTTP 层做接口隔离，用 `httptest` 录制/回放真实响应样本。
- 编译器（图 → DAG）是纯函数，单测覆盖：环、多入端口、缺失输入、组展开、跨类型。
- 调度器用注入的时钟与假队列做确定性测试（重试/超时/取消）。
- 端到端：`testdata/workflows/` 放若干画布 JSON + 期望的 Plan 快照，CI 比对防止回归。
