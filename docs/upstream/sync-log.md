# 上游同步记录

上游：`https://github.com/basketikun/infinite-canvas`（MIT）
机制：`scripts/upstream-sync.sh` + `scripts/report-upstream.mjs`，由 `.cnb.yml` 每周一
03:00 UTC 巡检，也可手动触发。**只产出报告，不自动改代码**（见 `docs/design/12` §3、§4）。

记录纪律：每次同步写清「上游版本/commit → 契约面变化 → 处理结论」，结论必须落到
具体条目（`docs/design/10-parity-matrix.md` 或 `docs/upstream/divergences.md`），
不接受「已关注」这类没有落点的结论。

## 2026-09-12 · 首次分析基线 v0.18.0

- 上游版本：`v0.18.0`，commit `d213a74`（2026-09-07 发布）
- 巡检结论：`baseline`（首次运行，无上一轮基线可比）
- 上游形态事实：244 个 ts/tsx、`web/src` 173 个文件、38 个版本记录、6 个官方插件、
  34 个 MCP 工具、2 个入口（`main` 与 `plugins-dist` 分支）
- 契约面基线（`upstream/baseline-probes.json`，已归档，不入库）：
  i18n key 162 个、节点类型 6 个、op 类型 8 个、MCP 工具 34 个、
  Provider 端点 3 个、边界常量 9 个、插件 manifest 字段 45 个、语言构成 4 项

### 「上游是不是改 Go 了？」

这个问题**只能靠事实核对，不能靠读文字记录**——上游 CHANGELOG 里两种说法都有：

| 位置 | 原文 | 读起来像 |
| --- | --- | --- |
| v0.0.4 | 「`/api/*` 由 Next.js 代理到内部 Go 服务」 | 曾经有 Go 服务端 |
| v0.4.0 | 「移除后端，项目定位为个人画布工具」 | 后端已被删除 |
| v0.5.0 | 「前端从 Next.js 迁移到 Vite，项目改为静态前端构建」 | 只剩静态前端 |

实测（v0.18.0，`d213a74`）：

```
.ts 131 / .tsx 111 / .mjs 11 / .js 2
.go 0 / .py 0 / .rs 0 / .java 0
Dockerfile 末段：FROM nginx:1.27-alpine，只 COPY web/dist
```

结论：**上游现在是纯 TypeScript 静态 SPA，没有 Go**。历史上 v0.0.x 期间确实有过一个
Go 服务端，但 v0.4.0 已删除，`find . -name '*.go'` 与 `find . -name 'go.mod'` 均为空。
本仓的 Go 服务端是本次重写**新增**的，不是从上游继承的。

为了让这个结论以后可自动核对（而不是每次重新翻 CHANGELOG），
`report-upstream.mjs` 新增了 `languages` 探针：上游一旦引入 Go 或任何非 TS 服务端，
巡检会把它报成契约面变化；`check-upstream-radar.mjs` 用夹具（含一个 `main.go`）
验证这个探针**真的能认出 Go**。

处理结论：

1. **契约面已逐项对照本仓**（下表），无未处理项。
2. 本仓在这次对照中补了两个**从未存在过的产物**：本文件与 `divergences.md`。
   在此之前「上游定时同步」这条能力只有脚本、没有产物，`make upstream-radar`
   一直红着——也就是说这条链路从建立到被修好之前，**没有任何一次真的记录过**。
3. 补齐产物时连带查出 **3 个真实缺陷**（都在「上游同步」这条链路上，都不是文档问题）：

| # | 缺陷 | 表现 | 修法 |
| --- | --- | --- | --- |
| 1 | 巡检判定**恒报安全告警** | `verdict` 在「上游未变化」时也是 `security-fix`：CHANGELOG 固定截取开头 40 行、安全判定不检查上游是否真的动了。上游 v0.17.0 那几条 Agent 令牌脱敏修复永远落在窗口里 → 每次巡检都告警 → 告警必然被忽略 | 只取「本次版本小节」的 CHANGELOG；安全判定加 `upstreamMoved` 前置条件；`check-upstream-radar` 用含历史凭据条目的夹具钉住 |
| 2 | `.gitignore` 的 `upstream/` 是**非锚定**规则 | 它同时匹配 `docs/upstream/` → 本文件与 `divergences.md` 写完**永远不会被提交**。更隐蔽的是：文件在本地存在、门禁在本地绿（门禁读文件系统）、`git status` 干净 | 改为 `/upstream/`；`check-upstream-radar` 改为**问 git**（`check-ignore` + `ls-files`），不再只看文件在不在 |
| 3 | 「上游是否改 Go」**无法被核对** | 只能靠翻 CHANGELOG，而 CHANGELOG 里「有内部 Go 服务」与「移除后端」两种说法都有 | 新增 `languages` 探针；夹具放一个 `main.go`，验证探针真的能认出 Go（否则这条结论没有证据） |

三个缺陷都配了**能真正失败**的回归用例，并实测「改回缺陷实现 → 用例变红」。

### 契约面对照（v0.18.0 → 本仓）

| 契约面 | 上游 | 本仓 | 结论 |
| --- | --- | --- | --- |
| i18n key | 162（`i18n.t` 调用点） | 305（字典叶子 key + 代码字面量） | 命名空间不同，属 DIV-07/08 的范围；**无遗漏同名 key** |
| 节点类型 | image / text / config / video / audio / group（6） | prompt / image / video / audio / group / generation / run（7） | 映射关系见 `10-parity-matrix.md` §2.13；`text`→`prompt`、`config`→`generation`（+ `run` 为一等执行记录，DIV-05） |
| op 类型 | add_node / update_node / delete_node / delete_connections / connect_nodes / set_viewport / select_nodes / run_generation（8） | 14 个（`add_node`…`set_parent`） | 上游 8 个全部有等价或更强替代（`update_node`→`set_title`/`set_spec`/`set_state`/`move_node`/`resize_node`；`connect_nodes`→`add_edge`；`delete_*`→`remove_*`；`select_nodes` 不进文档，改为前端本地状态）；多出 `group`/`ungroup`/`set_parent`/`set_settings` 四项 |
| MCP 工具 | 34（`canvas-agent/src/canvas/schemas.ts`） | 14（`internal/agent/tools.go`） | **未对等**，见下方「待处理」第 1 条 |
| Provider 端点 | `/v1`、`/v1beta`、`/audio/speech` | provider adapter 内同族端点 | 由 `contracts/openapi.yaml` 声明，`make gen-check` 双向校验 |
| 边界常量 | 9 个（见 `12` §3.3） | 全部有落位或显式说明 | 其中 `IMAGE_MAX_EDGE` 上游 3840 / 本仓 4096 是**刻意放宽**（4096 是上游自己的超分上限，两处取值不统一），已在此登记为需固化的差异说明 |

### 待处理（已进迭代队列，不是「已关注」）

| # | 项 | 落点 | 状态 |
| --- | --- | --- | --- |
| 1 | MCP 工具面 34 → 14 的缺口（含 `workbench_*`、`canvas_list_projects`、`site_navigate`、`assets_list`/`assets_add`、`generation_get_status` 等） | `10-parity-matrix.md` §9.5 目前标 `done` 但只覆盖 14 个，属**过度声明**，需改回 `wip` 并排期 | 待改 |
| 2 | `IMAGE_MAX_EDGE` 上游 3840 / 本仓 4096 的口径差异 | 把「上游 IMAGE_MAX_EDGE=3840、MAX_UPSCALE_LONG_EDGE=4096」写进 `docs/design/11` §2.1；本仓 UI 不提供 3840 以外的比例组合，因此实际不可达 | 待补说明 |

## 下一次巡检要看什么

1. 上游是否把 `IMAGE_MAX_EDGE` 与超分上限统一（若统一，本仓可收紧到同一数值）。
2. 上游是否新增 op 类型或节点类型（本仓 `contracts/ops.schema.json` 需同步评估）。
3. 上游 CHANGELOG 里出现安全/凭据类修复时，判定升级为 `security-fix`，按
   `docs/design/12` §4 的 ④ 处理（对照 INV-1..10 检查本仓是否已免疫）。
