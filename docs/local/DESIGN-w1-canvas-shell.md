# W1 画布壳 — 图标胶囊悬浮条 + Cmd/Ctrl+C/V/G

状态：设计定稿 → 下一 PR 实现（**本文档波次不改代码**）  
父文档：`DESIGN-frontend-vs-upstream.md` · 缺口：`GAP-ic-vs-ic2.md` Top #1 / #2  
对照日期：2026-09-13（Asia/Shanghai）

## 1. 一句话

把「选中一张图」最高频路径上的两个谎言拆掉：悬浮工具条从 11px 纯文字改成接近生产 `ic` 的**图标胶囊**；顶栏快捷键说明书里的 **Cmd/Ctrl+C/V**（以及缺失的 **G / Shift+G**）接到已有内核能力上。

## 2. 目标 / 非目标

| 目标 | 非目标（本波不做） |
|------|-------------------|
| `NodeHoverToolbar` 视觉 = 图标 + tooltip 胶囊（可贴顶、圆角、浅分隔） | face-guard / 换图 / 自由缩放锁 / 超分真接入（→ W2） |
| Cmd/Ctrl+C → 复制选中节点；V → 粘贴（内部剪贴板优先，否则系统图片走上传原语） | 侧栏资产缩略图网格 + 插入（→ W1-2） |
| Cmd/Ctrl+G 成组；Shift+G（或 Cmd/Ctrl+Shift+G，与实现择一并写进模态）解组 | Agent 五 Tab、全局 Bot 入口（→ W4） |
| `CanvasTopBar` SHORTCUTS 全部 `t()`，条目与真实行为一致 | 底栏改图标 Dock、连线落空白类型菜单（→ W1-2 / W5） |
| 补齐 kernel 快捷键单测 | 设置改 Tab、WebDAV、Key 回浏览器、改 `internal/` SQL / `.env` |
| 保留多选 `SelectionToolbar` 对齐/分布/分层（已强于 ic） | 回退或「简化」掉对齐工具条 |
| | 为「像上游」改 SQLite `updated_at` 扫描（脚枪已修，勿碰） |

## 3. 现状（只读核实）

### 3.1 悬浮条空

- 文件：`web/src/features/canvas/components/NodeHoverToolbar.tsx`
- `ToolBtn` 只渲染 `label` 文字，`fontSize: 11`；工具来自 `imageToolsFor(node)` + info/copyPrompt/retry/save/download/delete。
- 注册表：`web/src/features/canvas/tools/registry.ts` 已有 `IMAGE_TOOLS`、`hiddenTools()` / `setToolHidden`（localStorage `ic.canvas.hiddenTools`），**W1 可接「⋯」显隐，不必新工具 ID**。
- 对照：`/home/box/infinite-canvas-ic/.../canvas-node-hover-toolbar.tsx`（图标胶囊 + 可配置）。

### 3.2 快捷键说明书撒谎

- `kernel.handleShortcut`（`web/src/features/canvas/kernel/kernel.ts`）在 mod+c / mod+v 时已返回 `"copy"` / `"paste"`，但返回联合类型**没有** `"group"` / `"ungroup"`，且 **没有** `g` 分支。
- `CanvasPage.tsx` 的 `switch (action)` 只处理 `undo|redo|select-all|delete|escape`，`copy`/`paste` 落入 `default` → **按了没反应**。
- 成组/解组 op 与 UI **已存在**：`kernel/commands.ts` 的 `group` / `ungroup` / `duplicate-nodes`；`kernel.duplicateNodes`；`SelectionToolbar.tsx` / `CanvasToolbar.tsx` 按钮已 dispatch。缺的是键盘接线。
- `CanvasTopBar.tsx` 的 `SHORTCUTS` 写死中文，且列出「Ctrl/Cmd + C / V」——与行为不符；**无 G**。

### 3.3 相关但本波少动

| 文件 | 角色 | W1 态度 |
|------|------|---------|
| `components/SelectionToolbar.tsx` | 多选对齐/成组 | 保持；G 快捷键应复用同一 dispatch 语义 |
| `components/CanvasToolbar.tsx` | 底栏；含 upload / group / ungroup | paste 无内部剪贴板时可复用 upload 原语；不改 Dock 视觉 |
| `kernel/interaction.ts` | 空格平移、框选、Esc | 一般不动；勿破坏 IME/编辑态短路（`handleShortcut` 已跳过 input/textarea） |
| `kernel/commands.ts` | 命令 → op | 只读确认；通常无需改类型 |
| `components/CanvasSurface.tsx` / `NodeShell.tsx` | 挂载悬浮条 | 若胶囊定位需微调 top/left，可小改；不做大重构 |

## 4. 实现步骤（给下一代码 PR）

### Step A — 图标胶囊 UI

1. 在 `NodeHoverToolbar.tsx` 为每个动作提供 **icon + `title`/`aria-label`（文案仍 `t()`）**；默认只显示图标，hover 用原生 tooltip 即可（W1 不必上 Ant Tooltip 依赖膨胀）。
2. 容器样式靠拢 ic：更高一点的胶囊（约 h-10~12 量级）、圆角、轻微 `gap`、危险项（删除）视觉分隔；**不要**引入明文 Key 或新网络调用。
3. 图标来源优先：现有设计系统 / 简单 SVG inline / 已有依赖里的图标集；禁止为 W1 新拉大型图标库除非仓库已有。
4. （可选同 PR）「⋯」打开显隐列表，调用已有 `hiddenTools` / `setToolHidden`；未做也不阻塞合并。
5. **不要**在本步加入 `replace` / `resize` / `faceGuard` / 真超分——注册表新 ID 留给 W2。

### Step B — 复制 / 粘贴接线

1. 在 `CanvasKernel` 侧（或 Page 持有的模块级状态）维护**内部剪贴板**：`copy` 时序列化当前选中 node ids（或深拷贝所需最小字段）；优先调用已有 `duplicateNodes(ids, offset)` 作为 paste 的默认实现（与右键「复制」体感一致：粘贴即偏移副本）。
2. `CanvasPage.tsx` `switch` 增加：
   - `case "copy"`：写入内部剪贴板（若无选中则 no-op）。
   - `case "paste"`：若有内部剪贴板 → `duplicateNodes` + `sync.flush()`；若无，则尝试系统剪贴板图片 → 复用 `CanvasToolbar` 上传/建 image 节点原语（抽一小函数避免循环依赖）。
3. 注意：浏览器对 `navigator.clipboard.read` 有权限限制；失败时静默或 toast，**不要**抛到白屏。
4. 编辑态（input/textarea/contentEditable）继续让 `handleShortcut` 返回 `null`，避免拦截系统 C/V。

### Step C — 成组 / 解组快捷键

1. 扩展 `handleShortcut` 返回类型：`"group" | "ungroup"`。
2. 约定（写进模态与单测，二选一后锁定）：
   - **推荐**：`mod+g` → group；`mod+shift+g` → ungroup（与常见设计工具接近，且说明书好写）。
   - 若对齐生产 ic 的裸 `Shift+G`，须确认不与输入冲突，并在模态写清楚。
3. `CanvasPage` switch：
   - `group`：选中 ≥2 且非仅 group 时，走与 `SelectionToolbar` 相同逻辑（`createNode("group")` + `dispatch({ type: "group", ... })`）+ flush。
   - `ungroup`：选中为 group（或选中子节点所属 group）时 `dispatch({ type: "ungroup", groupId })` + flush。
4. 抽 `groupSelection(kernel)` / `ungroupSelection(kernel)` 共享函数，避免 Toolbar / Page / 快捷键三处复制（可放 `kernel` 旁小模块或 kernel 方法）。

### Step D — 快捷键模态 i18n

1. 删除 `CanvasTopBar.tsx` 内写死中文的 `SHORTCUTS` 数组。
2. 改为 `t("canvas.shortcut.*")` 键；`zh-CN.ts` / `en-US.ts` 成对添加；现有键集单测必须绿。
3. 条目**仅列出已接线行为**；C/V/G 在 Step B/C 合并后才写入。同步状态「同步中…」等残留硬编码中文一并收进 i18n（若同文件有）。

## 5. 文件清单

| 路径 | 动作 |
|------|------|
| `web/src/features/canvas/components/NodeHoverToolbar.tsx` | 图标胶囊 UI |
| `web/src/features/canvas/CanvasPage.tsx` | switch 接 copy/paste/group/ungroup |
| `web/src/features/canvas/kernel/kernel.ts` | `handleShortcut` + 可选 clipboard/group helpers |
| `web/src/features/canvas/kernel/commands.ts` | 通常只读；确认 duplicate/group op |
| `web/src/features/canvas/components/CanvasTopBar.tsx` | SHORTCUTS → i18n |
| `web/src/features/canvas/components/CanvasToolbar.tsx` | 抽出 upload/group 共享（若需要） |
| `web/src/features/canvas/components/SelectionToolbar.tsx` | 可选改为调用共享 group helper |
| `web/src/features/canvas/tools/registry.ts` | 可选接「⋯」；不新增工具 ID |
| `web/src/shared/i18n/zh-CN.ts` | 快捷键与 tooltip 文案 |
| `web/src/shared/i18n/en-US.ts` | 同上 |
| `web/src/features/canvas/kernel/__tests__/kernel.test.ts` | 扩展快捷键断言 |
| （可选）`web/src/features/canvas/__tests__/*` | Page 级纯函数若可测则补 |

**禁止改动：** `internal/`、`cmd/`、`deploy/`、`.env`、`data/`、开放访问逻辑、SQL 时间字段。

## 6. 测试计划

### 单测

- `handleShortcut`：mod+c/v → copy/paste；mod+g → group；mod+shift+g → ungroup；在 TEXTAREA 内 → null。
- `duplicateNodes` 已有覆盖则补「copy 再 paste 后 id 映射 / 偏移」。
- group/ungroup 快捷键路径与 button 路径产生相同 op 形状（可对 `dispatch` 结果或 pending ops 断言）。
- i18n 键集：en-US / zh-CN 对齐测试不挂。

### 人眼 / 手动

1. 打开一画布，选中图像节点：悬浮条为图标胶囊，hover 有中文（及切 en 后英文）tooltip。
2. Cmd/Ctrl+C → V：出现偏移副本；可连续 V（若设计为保留剪贴板）。
3. 多选两节点 Cmd/Ctrl+G：成组；再解组快捷键恢复。
4. 打开顶栏快捷键模态：无「列出但按了没反应」项；无写死中文漏网。
5. 在 prompt 节点编辑文字时 Cmd+C 不触发画布复制（系统选区复制仍可用）。
6. 回归：画布列表页仍 200（不碰 store）；多选对齐条仍在。

## 7. 验收标准（W1 Done）

- [ ] 选中图像节点时工具条是图标胶囊，不是一排 11px 字。
- [ ] Cmd/Ctrl+C / V 复制粘贴节点生效；Cmd/Ctrl+G（及文档约定的解组键）生效。
- [ ] 快捷键模态全部走 i18n，且与真实行为一致。
- [ ] 相关 vitest 绿；无新前端明文 Key；无 `data/` / `.env` 进 PR。
- [ ] DIV-01..10 无回退；未改 `internal/graph/sqlstore.go` 时间逻辑。

## 8. 范围外（明确甩给后续波次）

| 项 | 波次 |
|----|------|
| face-guard / replace / resize / 超分真接入 | W2 |
| 侧栏资产网格 + 插入 | W1-2 |
| 底栏图标 Dock、ConnectionCreateMenu | W1-2 / W5 |
| Agent setup/history/log、全局入口 | W4 |
| 设置 Tab 化、去重双挂载、移动 Drawer | W5 |
| 开放访问 / 登录墙 | 见 `DESIGN-open-access-v2.md` |
| SQLite TEXT 时间 Scan 脚枪 | 已修于 `c73fade`；登记于 GAP，本波不碰 |

## 9. 滚动

1. 本 PR：仅文档（GAP 刷新 + 本设计 + 父设计展开）。
2. 下一 PR：按本文 Step A→D 改 `web/src`，标题建议「fix(canvas): W1 图标胶囊悬浮条 + C/V/G 快捷键」。
3. 再下一刀：W1-2 `SidePanel` 资产插入。
