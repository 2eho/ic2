# 12 · 原仓库剥离与上游同步

> 回应 Issue 的两条要求：
> 1. 「复刻重写完成就把原仓库剔除」；
> 2. 「以后原仓库的更新内容也要定时拉取，然后更新成重写的内容」。
>
> 这两件事都需要**机制**，不能靠记忆。本文给出可执行流程与自动化配置。

## 1. 法律与署名（先解决，再动手）

原项目 `infinite-canvas` 为 **MIT License**。重写是「复刻」而非「引用依赖」，
但功能语义、数据结构、提示词文案、i18n 文案都可能构成衍生。必须做三件事：

1. **保留许可**：仓库根目录 `LICENSE` 保留原 MIT 全文，并在 `NOTICE` 中写明：
   ```
   本项目的功能设计参考自 basketikun/infinite-canvas (MIT License)，
   原著作权归原作者所有。本项目为独立重写实现。
   ```
2. **保留署名**：README「致谢」区块注明原作者与原仓库地址；`docs/design/00-overview.md` 已注明。
3. **UI 标识处理**：原项目 AGENTS.md 要求「二次开发与 PR 请保留原作者信息和前端页面标识」。
   重写后前端标识改为本项目自有品牌 + 致谢页链接原项目，**不复制**原项目的赞助商 banner、
   社群推广、统计脚本（这些与原作者商业关系绑定，不属于代码功能）。

> ⚠️ 需要用户确认：是否保留「原项目标识」在界面上。默认方案为「自有品牌 + 致谢」。
> 在用户确认前，不把原项目 logo、`canvas.best` 文档域、赞助商内容搬进重写产物。

## 2. 「剔除原仓库」的正确含义

Issue 说「剔除原仓库」。理解有两种，设计上分别处理：

| 解释 | 做法 | 采纳 |
| --- | --- | --- |
| A. 删除本地 clone / 不 vendor 原代码 | 重写仓库中**不含**原项目任何源码文件；原项目仅作为「参考镜像」存在于 CI 缓存 | ✅ 采用 |
| B. 断绝对上游的任何追踪 | 不再拉取上游更新 | ❌ 与「定时拉取更新」矛盾，不采用 |

因此：
- 重写仓库 `main` 中**没有任何** `web/`、`canvas-agent/`、`canvas-proxy/`、`plugins/` 原代码。
- 引入阶段完成后删除任何临时 `third_party/infinite-canvas/` 目录（不提交到 git）。
- 原项目以「上游参考」形式存在：`upstream/` 目录（gitignore）+ CI 定时任务。

## 3. 上游追踪机制

### 3.1 目录与文件

```
upstream/                       # gitignore，不入库
  infinite-canvas/              # 上游 clone（浅克隆 main）
  snapshot.json                 # 上次分析的上游版本与 commit
docs/upstream/
  sync-log.md                   # 每次同步的结论（人可读）
  divergences.md                # 有意的偏离清单（见 §5）
```

`upstream/snapshot.json` 结构：

```json
{
  "repo": "https://github.com/basketikun/infinite-canvas",
  "version": "v0.18.0",
  "commit": "…",
  "analyzedAt": "2026-09-12T00:00:00Z",
  "openItems": [
    { "id": "ATK-23", "kind": "upstream-change", "title": "…", "status": "pending" }
  ]
}
```

> 注意：`snapshot.json` 若包含时间戳，**不要**把「分析时间」写进需要人读的文档标题，
> 符合原项目 AGENTS.md 的「文档不要写过期日期」纪律。

### 3.2 定时任务（.cnb.yml）

```yaml
# 每周一 03:00（UTC）拉取上游变更，产出差异报告并发起 Issue
upstream-watch:
  <<: *only-default-branch
  schedule:
    - cron: "0 3 * * 1"
  stages:
    - name: sync-upstream
      script: bash scripts/upstream-sync.sh
    - name: report
      script: node scripts/report-upstream.mjs
```

`scripts/upstream-sync.sh` 职责：

1. 浅克隆或 `git fetch --depth 1 origin main` 到 `upstream/infinite-canvas`。
2. 读出 `VERSION` 与最新 commit；与 `snapshot.json` 比对：
   - 版本与 commit 都未变 → 直接退出（幂等，不产生噪音）。
3. 变更时执行**结构化抽取**（见 3.3），产出 `upstream/change-report.json`。
4. 若报告非空，更新 `snapshot.json` 并让 CI 失败/发起 Issue 触发人工处理。
5. **不自动改重写代码**。上游更新只生成报告与待办，由人（或 NPC）按 §4 处理。

### 3.3 抽取什么（只抽「可核对的事实」，不抽代码）

| 抽取项 | 来源 | 用途 |
| --- | --- | --- |
| `VERSION` / CHANGELOG | `VERSION`、`CHANGELOG.md` | 版本级变更摘要（按 `[新增]/[调整]/[修复]/[优化]` 分类） |
| 新增/删除/改名的源文件 | `git diff --name-status` | 判断是新功能还是重构 |
| i18n key 集合 | `web/src/i18n/locales/zh-CN.ts` | 新增文案 = 新功能信号（最灵敏的探针） |
| 节点类型集合 | `web/src/types/canvas.ts` 的 `CanvasNodeType` | 节点模型是否变化 |
| op 类型集合 | `web/src/lib/canvas/canvas-agent-ops.ts` | 协议是否变化（影响服务端 op 定义） |
| 工具名集合 | `canvas-agent/src/canvas/schemas.ts` 的 `toolNames` | MCP 工具表变化 |
| 插件 manifest 字段 | `plugins/canvas/sdk/src/types.ts` | 插件契约变化 |
| 请求路径常量 | `rg -o '"/v1[^"]*"|"/v1beta[^"]*"'` | Provider 协议变化 |
| 默认/边界常量 | `IMAGE_MAX_EDGE`、`VIDEO_SECONDS_MIN` 等 | 边界值变化（必须同步到 §2.1） |
| 存储 schema | `stores/*` 的 persist key / IndexedDB store 名 | 迁移影响 |

**为什么用这些探针**：它们是**契约面**而非实现细节。实现的等价重写不需要跟随，
但契约变化必须让重写跟上。

### 3.4 差异报告结构

`upstream/change-report.json`：

```json
{
  "from": { "version": "v0.18.0", "commit": "…" },
  "to":   { "version": "v0.19.0", "commit": "…" },
  "changelog": [{ "tag": "新增", "text": "…" }],
  "probes": {
    "i18nKeysAdded": ["canvas.xxx.yyy"],
    "i18nKeysRemoved": [],
    "nodeTypesAdded": [],
    "opTypesAdded": ["resize_group"],
    "toolsAdded": ["canvas_resize_group"],
    "pluginFieldsAdded": ["configVersion"],
    "endpointsAdded": ["/v1/videos/{id}/cancel"],
    "limitsChanged": [{ "name": "VIDEO_SECONDS_MAX", "from": 30, "to": 60 }]
  },
  "filesTouched": { "web/src/services/api/video.ts": 120, "…": 0 },
  "verdict": "contract-change"
}
```

`verdict` 取值：

- `noise`：仅内部重构/格式化/文档 → 只记 `sync-log.md`。
- `contract-change`：契约面变化 → 必须更新 `10-parity-matrix.md` 与相关模块设计。
- `security-fix`：CHANGELOG 含安全修复或 diff 触及鉴权/凭据/沙箱 → **立即**处理，不走周礼。
- `breaking`：数据结构/协议破坏性变化 → 评估是否同步；若重写已规避该问题，记入 `divergences.md`。

## 4. 上游同步处理流程（人 + NPC）

```
CI 产出 change-report
  → 建 Issue（标签 upstream-sync），正文贴 report 摘要 + 待办清单
  → NPC 读取 report 与上游 diff
  → 分类处理：
      ① 契约变化   → 改 contracts/ → make gen → 改实现 → 补对抗用例
      ② 边界值变化 → 改 config/limits.go + 02/11 文档 + 测试
      ③ 新增功能   → 加 10-parity-matrix 条目（状态 todo）→ 排期
      ④ 安全修复   → 对照 INV-1..10 检查是否已免疫；未免疫则立刻修
      ⑤ 纯实现重构 → 记录并忽略
  → 更新 docs/upstream/sync-log.md 与 snapshot.json
  → 关闭 Issue
```

**红线**：

- 不直接 `git merge` / `cherry-pick` 上游代码到重写仓库。
- 不因上游新增功能就自动复制其实现（重写要保持自己的架构约束）。
- 不因为「上游没做」就跳过边界分析。

## 5. 有意偏离清单（divergences）

任何「重写与上游行为不同」的地方都必须登记，避免以后被当成 bug。

| ID | 维度 | 上游行为 | 重写行为 | 理由 |
| --- | --- | --- | --- | --- |
| DIV-01 | 数据归属 | 浏览器 IndexedDB | 服务端 Workspace | 多端/协作/备份 |
| DIV-02 | 凭据 | 前端 localStorage + 直连 | 服务端加密存储 | 安全（INV-5） |
| DIV-03 | 自定义脚本 | 浏览器 `new Function` | 服务端沙箱 | 安全 |
| DIV-04 | 插件运行 | 同源 Blob import | iframe sandbox | 安全（INV-6） |
| DIV-05 | 生成 | 一次性 await | Run/Step/Attempt | 可追溯、可限额、可重放 |
| DIV-06 | 同步 | WebDAV 时间戳合并 | 服务端权威 + 版本号 | 一致性（避免时钟回拨攻击） |
| DIV-07 | 错误 | 前端从 message 猜语义 | 稳定 `code` 枚举 + i18n | 可本地化、可测试 |
| DIV-08 | 参数表单 | 每种能力一套弹层 | schema 驱动 | 新增模型零改前端 |
| DIV-09 | 多结果 | `images[]` + 主图指针 | 变体集合 + Primary | 去掉父子里程碑补丁字段 |
| DIV-10 | 品牌与统计 | 赞助商/统计脚本 | 无（致谢替代） | 商业关系不随代码继承 |

`divergences.md` 是**发布物的一部分**：迁移指南要告诉老用户「哪些地方不一样」。

## 6. 迁移旧数据（必须做，否则老用户流失）

虽然原项目 AGENTS.md 声明「不需要兼容旧数据」，但重写意味着**已有真实用户**，所以本期保留一次性导入。

### 6.1 导入路径

```
浏览器打开新前端 → 检测 IndexedDB('infinite-canvas')
  ├─ 无数据 → 正常进入
  └─ 有数据 → 展示「发现本地画布 N 个、素材 M 个」导入向导
       ├─ 逐个 Blob 上传（分片 + 断点续传 + 进度）
       ├─ 画布 JSON 走 migrateLegacyCanvas（旧 schema → 新 schema）
       ├─ POST /canvases/import（服务端校验后入库）
       └─ 完成后本地数据保留 30 天（只写「已导入」标记，不删除）
```

### 6.2 旧 → 新 schema 映射

按 `10-parity-matrix.md` §2.13 的字段映射表执行，关键转换：

| 旧 | 新 |
| --- | --- |
| `node.metadata.content`（blob: URL） | 上传得 `assetId`，写入 `Spec.assetId` |
| `node.metadata.storageKey`（`image:<id>` / `video:<id>`） | 上传对应 Blob 后丢弃 key |
| `node.metadata.images[]` | `State.Result.Variants[].assetId` |
| `metadata.primaryImageId` | `State.Result.Primary`（转索引） |
| `metadata.groupId` | `Node.ParentID` |
| `metadata.references[]`（storageKey 数组） | 反查节点 → 生成 `Edge` |
| `metadata.videoTaskId` | `Attempt.RemoteTask`（若仍可查则允许续查，否则标失败） |
| `metadata.status = loading` | `State.Status = failed` + `code=interrupted` |
| `project.chatSessions` | 导入为「历史会话（只读）」的 `AgentSession`，不保证可继续对话 |
| `project.viewport` / `backgroundMode` / `showImageInfo` | `CanvasDocument.Viewport` / `Settings` |

### 6.3 导入的对抗要求

- 导入器必须**幂等**：重复导入不产生重复画布（用「源 projectId + 内容 hash」做去重键）。
- 单个 Blob 丢失时**不阻断**整体导入：记录缺失清单，节点上标 `missing-asset`。
- 导入中断可续：每完成一个画布就落一次进度。
- 导入前后做数量核对：`旧节点数 == 新节点数`，不一致时报告而非静默成功。
- 旧数据不做破坏性删除（原项目已有用户，误删不可恢复）。

## 7. 落地检查单

- [ ] `LICENSE` + `NOTICE` 就位，README 有致谢区块
- [ ] `.gitignore` 覆盖 `upstream/`、原项目构建产物
- [ ] 仓库内不存在原项目源码（`rg -l "basketikun" --glob '!docs/**' --glob '!LICENSE' --glob '!NOTICE'` 无业务命中）
- [ ] `.cnb.yml` 有 `upstream-watch` 定时任务
- [ ] `scripts/upstream-sync.sh` + `scripts/report-upstream.mjs` 可独立运行
- [ ] `docs/upstream/sync-log.md` 有首次分析记录
- [ ] `docs/upstream/divergences.md` 已建立（含 DIV-01..DIV-10）
- [ ] 导入器有 e2e：玩具旧数据 → 导入 → 断言与预期一致
- [ ] 品牌/logo/文档域/统计脚本已替换，无残留外链
- [ ] 「原项目界面标识是否保留」已向用户确认
