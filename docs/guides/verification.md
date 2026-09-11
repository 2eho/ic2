# 核验方法

本文说明「怎么证明它是对的」，对应 `docs/design/13-verification-and-iteration.md` 的三层核验。

## 一键核验

```bash
make check      # lint + test + boundaries + parity + sec + drill
```

拆开跑：

```bash
make lint        # go vet + staticcheck + 内核纯度 + schema 一致性 + tsc
make test        # Go 单测 + 前端 vitest
make boundaries  # 边界常量：代码 ↔ 文档 一致性
make parity      # 对等矩阵覆盖率报告
make sec         # 凭据脱敏 / SSRF / 插件权限 / IDOR / 仓库卫生
make drill       # 故障演练子集
make e2e         # 端到端主链路
```

## L1 设计核验（覆盖率与死角）

```bash
make parity
```

输出示例（当前状态）：

```
条目总数   : 158
done       : 116
wip        : 0
todo       : 41
dropped    : 1（不计入分母）
覆盖率     : 74.05%  (done / (total - dropped))
```

- 覆盖率分母排除 `dropped`（明确不做且写了理由的项）；
- **未完成项在 `10-parity-matrix.md` §14 逐条列出并写明原因**，
  避免「看起来很高」的自欺；
- 门槛对照见 `docs/design/09-roadmap.md`：当前处于 M3 末（65%）与 M4 末（80%）之间。

## L2 实现核验（不变量与对抗用例）

每个不变量都有对应的可执行用例：

| 不变量 | 覆盖用例 |
| --- | --- |
| INV-1 op 日志重放 = 快照 | `internal/graph/replay_test.go` `TestATK21ReplayEqualsSnapshot` |
| INV-2 幂等键只生效一次 | `internal/exec/engine_test.go` `TestATK02IdempotentRunCreation` |
| INV-3 上游只计费一次 | `internal/provider/transport_test.go` + `TestRequestIDIsStable` |
| INV-4 引用窗口内 Blob 可读 | `internal/asset/service_test.go` `TestGCKeepsReferencedAsset` |
| INV-5 凭据明文永不出现 | `internal/platform/redact_test.go` `TestRedactCredentialLeak` |
| INV-6 插件拿不到宿主 origin | `internal/plugin/manifest_test.go` `TestSandboxNeverAllowsSameOrigin` + 前端 `protocol.test.ts` |
| INV-7 agent_items 合并幂等 | `internal/agent/agent_test.go` `TestATK13LiveAndSnapshotMerge` |
| INV-8 写操作可归属 | `internal/graph/replay_test.go` `TestActorRequired` |
| INV-9 成本用整数微元 | `internal/provider/transport_test.go` `TestUsageAddIsInteger` |
| INV-10 跨工作区不可读 | `internal/asset/service_test.go` `TestCrossWorkspaceAssetIsolation` |

对抗用例（ATK-01..22）落地位置见 `docs/design/11-boundary-and-adversarial.md` §3。

## L3 上线核验（故障演练与性能）

```bash
make drill
```

覆盖：
- 时钟回拨（`TestDrillClockSkew`）——验证时间异常会被暴露而不是被掩盖；
- 非法配置启动期拒绝（`TestDrillInvalidConfigRejectedAtStartup`）；
- 密钥长度非法启动期拒绝（`TestDrillSecretKeyValidation`）；
- DB 不可用快速失败（`TestDrillDBUnavailableFailsFast`）；
- 重定向 SSRF 每跳重新校验（`TestDrillRedirectRevalidation`）；
- 多行日志脱敏（`TestDrillRedactMultiline`）；
- 服务端重启后异步任务续查（`TestATK15ResumeAsyncTaskAfterRestart`）；
- 并发冲突 rebase / 409（`TestVersionConflictDetected`）；
- GC 与引用竞态（`TestGCKeepsReferencedAsset`）。

性能预算在 `docs/design/13 §3.3`。当前可自动化的部分：

| 指标 | 预算 | 验证方式 |
| --- | --- | --- |
| 5000 节点视口查询 | 每次 < 2ms | `web/src/features/canvas/kernel/__tests__/scene.test.ts` |
| 单批 op 上限 | 1000 | `internal/graph/op_test.go` `TestOpBatchSizeLimit` |
| 提示词上限 | 32KB | `internal/graph/spec.go` + 边界常量校验 |

## 「可上线」的定义

一个功能算可上线，当且仅当（`docs/design/13 §5.3`）：

1. 在对等矩阵中状态为 `done`（或有明确 `dropped` 理由）；
2. 涉及的不变量有测试证明；
3. 新增边界有对抗用例且全绿；
4. 性能预算未退化；
5. 错误有稳定 code 与 i18n 文案；
6. 有可观测（日志字段、指标、trace）；
7. 文档已同步；
8. 故障演练未暴露该模块的不可恢复路径。

任一不满足 → 状态为 `wip`。**这条用来对抗「差不多就行了」。**

## 本轮迭代中被测试捕获的真实缺陷

核验不是走过场——以下都是测试先红、再修代码的：

1. `applyResize` 的参数形状写成 `{x,y}` 而实现读 `{dx,dy}` → 缩放恒为 NaN；
2. 撤销栈用时间窗合并导致两次独立点击被并成一步（改为手势标识）；
3. 手写 `includes('n')` 判定缩放方向在有歧义，改为显式方向映射；
4. 软删资产在 `Get` 里被 `deleted_at IS NULL` 过滤 → 打开旧画布会破图；
5. GC 只查「无引用」而漏了「跨工作区共享同一 hash」的情况，会误删他人在用的 Blob；
6. sqlite `max_open_conns=1` 下行迭代期间嵌套查询自锁（Agent 会话加载）；
7. `AsDomainError` 返回 typed-nil 被直接当 error 返回 → 接口 500 且日志空指针；
8. `requireWorkspace` 匹配失败路径返回 `(nil, nil)` → 500 而不是 404；
9. 主密钥解析只校验「能解码」不校验长度 → 错误被推迟到第一次加密；
10. 插件节点类型正则拒绝多层点分段 key → 6 个内置插件全部无法安装。
