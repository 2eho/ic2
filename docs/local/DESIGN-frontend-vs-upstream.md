# ic2 前端 vs 上游 / 生产 ic — 顶层设计

状态：设计定稿（文档波次）→ 实现按 W1…W5 分 PR  
范围：仅 `/home/box/ic2`；本文件与同目录 GAP / W1 设计只动 `docs/local/`  
对照日期：2026-09-13（Asia/Shanghai）

## 1. 关系

| | 仓库 | 本机 | 公网 |
|--|------|------|------|
| 生产对照（主） | 部署源 v0.18.0 + overlay | `/home/box/infinite-canvas-ic` | `ic.zehh.de5.net` |
| 上游对照（辅） | github.com/basketikun/infinite-canvas @ `d213a74` | `/tmp/basketikun-ic` | — |
| Go 重写魔改 | cnb.cool/context-flow.cloud/ic · mirror github.com/2eho/ic2 | `/home/box/ic2` | `ic2.zehh.de5.net` |

上游 / 生产 `ic` = **本地优先 SPA**（IndexedDB + 前端持 Key 直连渠道）。  
ic2 = **服务端权威**（Workspace / 画布 / 凭据 / 执行在 Go），前端应是「好看的客户端」，不是第二套业务真相。

有意偏离见 `docs/upstream/divergences.md`（DIV-01..10）：凭据不上前端、脚本不上浏览器、插件沙箱等 **禁止为了「更像上游」而回退**。

缺口排序见 `GAP-ic-vs-ic2.md`。W1 落地步骤见 `DESIGN-w1-canvas-shell.md`。

## 2. 目标 / 非目标

| 目标 | 非目标 |
|------|--------|
| 把画布 chrome 密度与手感拉向生产 `ic`（图标胶囊、快捷键真能用、侧栏可插入、底栏停靠感） | 整仓粘贴上游 `web/`；带回 IndexedDB / 明文 Key / `new Function` |
| 数据与副作用只走 ic2 `/api/v1` + 服务端凭据 / Run | 把 WebDAV、本地存储用量、canvas-proxy、GA/赞助商搬进来 |
| 分波可上线、每波可验收；不破坏 DIV-01..10 | 一次改全站；为 UX 改 Go SQL / `.env` / 开放访问模型 |
| 文档与实现路径提示对齐真实 `web/src/features/canvas/*` | 在体验 PR 里「对齐上游」回退 `sqlTime` / `parseSQLTime` |
| 保留 ic2 已强于 ic 的能力（多选对齐、Run 面板、schema 建节点等） | 为视觉统一删掉对齐/分布/分层或 Run 三层语义 |

## 3. 问题陈述

契约对等矩阵宣称大量 done，但 Live QA 体感前端「差」——通常不是缺 API，而是：

1. **交互密度**：上游画布页单文件 3k+ 行堆出来的手感（工具条、右键、快捷键、侧栏）重写后拆模块却「空/顿/绕」。
2. **视觉与信息架构**：Ant/布局/空态/加载不如 `ic` 打磨；ic2 更浅、更疏、更偏表单。
3. **工作流闭环**：生图/参考图/节点工具链路步骤更多或少反馈。
4. **Agent/工作台**：能力挂了，面板与入口不一致导致「找不到」。

同机 sibling SPA 对照（2026-09-13）：

- **`ic`**：更密、更暗的停靠 chrome——图标 Dock、节点上方图标胶囊。
- **`ic2`**：更浅、更疏——文字按钮、pill 顶栏、纵向设置页。

## 4. UX vs API（选定策略）

**禁止**：整仓把上游 `web/` 贴进 ic2（会带回 DIV-02/03/04 安全回退）。  
**采用**：**体验层对齐生产 ic + 数据层只走 ic2 API**。

```
上游 / ic UI 模式（对照物）     ic2
  交互/布局/文案/动效        →  复刻或借鉴
  IndexedDB / 前端 Key       →  不复刻（走 /api/v1 + 服务端凭据）
  canvas-proxy CORS          →  不需要（同源）
  WebDAV / 本地代理 Tab      →  不搬（DIV-06 + 衍生）
```

判定口诀：

- **UX 缺口**（工具条长什么样、快捷键接没接、侧栏能不能插）→ 改 `web/src/features/*`。
- **API / 契约已齐** → 不要再开「补后端」票，除非真缺字段（本清单默认不缺）。
- **安全边界**（Key、脚本、插件、Run 语义）→ 永远按 DIV，不按视觉。

## 5. 波次 W0–W5（含 `web/src` 路径提示）

每波：对照 `/home/box/infinite-canvas-ic`（或 `/tmp/basketikun-ic`）路径 → 在 ic2 对应 `features/*` 改 → 不新增前端持有的明文 Key → 不改 `internal/` / `cmd/` / deploy / `.env`（除非独立运维票）。

| 波次 | 主题 | 成功标准 | 主要路径提示（ic2） |
|------|------|----------|---------------------|
| **W0** | 基线录屏/清单 | Top 缺口已写入 `GAP-ic-vs-ic2.md`；Live QA 视觉与 canvases 500 脚枪已登记 | 本文 + GAP（文档 only） |
| **W1** | 画布壳 · 第一刀 | 节点悬浮条 = 图标胶囊；Cmd/Ctrl+C/V/G（及 Shift+G）真能用；快捷键模态走 i18n 且与行为一致 | 详见 `DESIGN-w1-canvas-shell.md`：`components/NodeHoverToolbar.tsx`、`CanvasPage.tsx`、`kernel/kernel.ts`（`handleShortcut`）、`kernel/commands.ts`（`duplicate-nodes` / `group` / `ungroup`）、`components/CanvasTopBar.tsx`、`tools/registry.ts`（`hiddenTools`）、`components/CanvasToolbar.tsx`（upload 原语）、`shared/i18n/{zh-CN,en-US}.ts` |
| **W1-2** | 画布壳 · 侧栏资产 | 资产 Tab 缩略图网格 + 点击插入画布 | `components/SidePanel.tsx`；`shared/api` 的 `listAssets` / upload；内核 `createNode` + `set-spec.assetId` |
| **W2** | 节点图像工具 | 蒙版/裁剪/反推/多角度主路径少一步；换图 / 自由缩放锁 / face-guard / 超分真接入按优先级 | `tools/registry.ts`、`tools/ImageToolDialog.tsx`、`tools/dialogs/*`、`NodeHoverToolbar.tsx`；对照 ic `canvas-image-toolbar-tools.tsx` / `canvas-node-*-dialog.tsx`。**新工具协议放本波，不塞进 W1** |
| **W3** | 工作台 | 参考栏可从资产库/提示词库点选；「图片1、图片2」可写进 prompt 前缀 | `features/workbench/*`（`WorkbenchPage`、ReferenceBar、History、Settings）；对照 ic `pages/image` / `pages/video` |
| **W4** | Agent 面板 | 入口不迷路；setup / history / log 至少有可用等价物；权限确认清晰 | `features/agent/*`、`CanvasPage.tsx` 的 `AgentSidebar` 挂载；全局入口若做则碰 `AppShell`（单独说明） |
| **W5** | 视觉统一 + 壳 | 间距/字体/空态/暗色；顶栏图标化；真·移动 Drawer；设置 IA（去重双挂载） | `AppShell`、设置 `features/settings/*`、`components/CanvasToolbar.tsx`（底栏 Dock）、主题 token / CSS 变量 |

### W1 对照物（只读）

| ic / 上游 | ic2 |
|-----------|-----|
| `components/canvas/canvas-node-hover-toolbar.tsx` | `features/canvas/components/NodeHoverToolbar.tsx` |
| `components/canvas/canvas-selection-toolbar.tsx` | `features/canvas/components/SelectionToolbar.tsx`（ic2 已更强，W1 **不要回退**） |
| `components/canvas/canvas-toolbar.tsx` | `features/canvas/components/CanvasToolbar.tsx` |
| `pages/canvas/project.tsx` 快捷键分支 | `CanvasPage.tsx` switch + `kernel.handleShortcut` |
| `lib/canvas` 命令 | `kernel/commands.ts` + `kernel/interaction.ts`（空格/Esc/平移；W1 一般不动 interaction） |

## 6. 对抗

| 风险 | 对策 |
|------|------|
| 为像上游把 Key 放回 localStorage | 硬拦；设置页只掩码（DIV-02） |
| 大段复制上游组件导致双数据源 | 强制 props/API 适配层，禁止直读 IndexedDB（DIV-01） |
| 浏览器 `new Function` / Blob 插件进主页 | 脚本服务端沙箱；插件 iframe sandbox（DIV-03/04） |
| 一次改全站失控 | 严格按波次；每波可上线验收；W1 只做胶囊+快捷键 |
| 为 UX「简化」删掉 Run / Variants / 对齐工具条 | 禁止；见 GAP「ic2 已强于 ic」 |
| 体验 PR 误改 SQLite 时间扫描「对齐别的写法」 | canvases list 500 已修（`c73fade`：string scan + `parseSQLTime` / `sqlTime`）；**脚枪类，勿再踩** |
| 与开放访问混谈 | 开放访问是运维开关（`DESIGN-open-access-v2.md`）；前端体验是另一条线 |
| Fable / 云端只能改已连接 SCM | 改动落本机 `/home/box/ic2`；文档/代码 PR 推 `github` remote（`2eho/ic2`） |

## 7. 验收（跨波次）

1. **人眼**：同机开 `ic` 与 `ic2` 画布，选中图像节点——ic2 悬浮条不再是一排 11px 纯文字（W1 起）。
2. **快捷键**：说明书上有的组合键，按下去必须有对应行为；没有的项不得写进模态（W1 起强制）。
3. **安全**：Network / Application 无明文模型 Key；无前端执行用户脚本；插件仍在 sandbox（全程）。
4. **回归**：`GET /projects/{pid}/canvases` 200；不重引入 `time.Time` 直接 Scan TEXT（全程）。
5. **单测**：波次触及的 `kernel` / 组件纯函数测试绿色；i18n en-US/zh-CN 键集仍对齐。
6. **范围**：体验 PR 默认只动 `web/src`（本设计文档波次则只动 `docs/local/`）；不提交 `data/`、`.env`、secrets。

## 8. 滚动与发布

```
W0 文档（本 PR） → W1 代码 PR → W1-2 → W2 → W3 → W4 → W5
         │
         └─ 每波：分支 → 实现 → vitest/相关包 → 人眼对照 ic → PR 中文摘要
```

- **回滚**：单波独立 PR；回滚只 revert 该波，不影响开放访问或 SQL 时间修复。
- **远程**：实现可推 `github`（`https://github.com/2eho/ic2`）开 PR → `main`；不 force-push；不改 VPS/egress。
- **默认拍板**：无额外输入时从 **W1 画布壳** 开干（见 `DESIGN-w1-canvas-shell.md`）。

## 9. 与「开放访问 v2」关系

正交。开放访问已可用；体验线不依赖再改登录。可并行；痛点在画布时优先 W1。

## 10. 文档索引

| 文档 | 用途 |
|------|------|
| `GAP-ic-vs-ic2.md` | Top 10 + 严重度 + 有意非抄 + Live QA |
| `DESIGN-w1-canvas-shell.md` | W1 唯一实现设计（步骤/文件/测试/范围外） |
| `DESIGN-open-access-v2.md` | 开放访问（本线勿混入） |
| `docs/upstream/divergences.md` | DIV-01..10 权威表 |
