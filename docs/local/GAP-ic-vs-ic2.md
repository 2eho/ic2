# Top frontend gaps — Infinite Canvas (ic) vs ic2

对照日期：2026-09-13（Asia/Shanghai）。只读对照，未改代码。

| | 仓库 | 本机路径 | 说明 |
|--|------|----------|------|
| 生产对照（主） | 部署源 v0.18.0 + 本地 overlay | `/home/box/infinite-canvas-ic` | 用户每天用的 `ic`。相对上游多了 face-guard、若干生图/工具条补丁 |
| 上游对照（辅） | github.com/basketikun/infinite-canvas @ `d213a74` v0.18.0 | `/tmp/basketikun-ic` | 与部署同源 commit；**无** `face-guard` |
| 重写 | cnb.cool/context-flow.cloud/ic | `/home/box/ic2/web` | 服务端权威客户端 |

定位见 `DESIGN-frontend-vs-upstream.md`：复刻交互/布局/文案，**不**复刻 IndexedDB / 前端 Key / `new Function`。有意偏离见 `docs/upstream/divergences.md` DIV-01..10。

本清单按**用户看得见的痛**排序，不是契约对等矩阵勾选。

---

## Executive Top 10

1. **画布节点悬浮工具条「空」** — ic 是节点上方的图标胶囊（可配置 16 项、可开标签）；ic2 是一排 11px 纯文字按钮。每次点一张图都感到「重写没做完」。超分入口是「尚未接入」占位。缺 face-guard / 替换图 / 自由缩放锁 / 「⋯」配置器。
2. **快捷键说明书在撒谎** — 顶栏模态列出 Ctrl/Cmd+C/V，内核 `handleShortcut` 也返回 `copy`/`paste`，但 `CanvasPage` 的 switch **不处理**这两项；也没有 Cmd+G / Shift+G。肌肉记忆每天踩空。模态文案还是写死的中文。
3. **左侧栏不能当工作台用** — 三个 Tab 名字对齐了，但资产 Tab 只渲染 `kind · id前12位 · KB`，不能插入画布、没有缩略图。ic 是分组网格 + hover 插入/删除 + 节点树多选导出 + 预览。
4. **Agent 找不到、打开了也不像** — ic 全局顶栏 Bot、右侧可拖宽停靠、5 个子面板（chat / setup / history / skills / log），主文件 1600 行。ic2 只在画布页一个按钮，浮层卡片，3 个 Tab（chat / tools / skills），无会话历史、无连接引导、无事件日志、无附件作曲器。
5. **工作台少了「从图库/提示词库来」这一步** — 参考栏已有上传/粘贴/拖入/重编号（语义对）。缺资产库点选、提示词库点选、以及 ic 写进 prompt 的「图片1、图片2」编号前缀。视频工作台同样没有库入口。
6. **图像工具条缺三件用户天天用的、超分是假门** — 相对生产 ic：无换图、无自由缩放锁、无 face-guard；超分对话框明确说未接入。蒙版/裁剪/分割/反推/多角度对话框在，但工具条手感把它们衬得像后台菜单。
7. **手机导航是假的** — `AppShell` 里有 `drawerOpen`，但触发按钮包在 `display:none` 里。ic 有左侧 Drawer，6 个带图标入口。窄屏只能靠顶栏 `flexWrap` 挤文字链。
8. **设置页是一长条卡片，不是配置中心** — ic 6 个 Tab（渠道 / 本地代理 / 偏好 / 提示词源 / WebDAV / 本地存储）+ 渠道抽屉编辑器。ic2 纵向堆：偏好、直连、脚本、渠道表单、提示词源、导入导出（**组件挂了两次**）、插件、限额 JSON。深度在渠道/模型选择，信息架构没有。
9. **画布底栏 / 连线落点创建菜单没有「停靠感」** — ic 底部图标 Dock（选择/平移切换、五类节点、外观、插件、上传）+ 连到空白处弹出 `ConnectionCreateMenu`。ic2 底栏是文字按钮；右键空白能建节点（这点更好），但拖线到空白没有「选类型」菜单。
10. **壳层视觉与文案密度** — 顶栏无图标、主题是 🌙/☀️、快捷键/同步文案中英混写。i18n 叶键约 347 vs ic 约 530。空态/间距/动效未打磨。这是「看起来像内部工具」而不是「少一个 API」。

---

## 方法与度量

### 导航 / 路由 / 移动端

| | ic (`navigation-tools` + `router.tsx`) | ic2 (`AppShell` + `App.tsx`) |
|--|--|--|
| 顶栏入口 | canvas / image / video / prompts / assets / config（图标+下划线） | home / canvases / image / video / prompts / assets / settings（纯文字 pill） |
| 路由 | `/` `/image` `/video` `/assets` `/prompts` `/canvas` `/canvas/:id` `/config` | `/login` `/` `/projects` `/canvas/:id` `/workbench/image` `/workbench/video` `/assets` `/prompts` `/settings/*` |
| 画布时顶栏 | `/canvas/:id` 隐藏全站 header，画布自带 top-bar | 同：`/canvas/` 整壳隐藏，只留画布 top-bar |
| Agent 入口 | 顶栏 Bot，全站可开 | **仅画布 top-bar** 一个「Agent」按钮 |
| 移动端 | `MobileNavDrawer` 左侧 280 Drawer | `drawerOpen` 状态在，按钮 `display:none` |
| 右侧动作 | 文档 / 配置 / 中英 / 动画主题 / 版本 / GitHub | 语言 select / emoji 主题 / 用户名 / 登出 |

ic2 多出来的 `/login`、`/projects` 是服务端会话与多画布列表，**不是缺口**（见有意偏离）。缺的是图标导航、真·移动抽屉、全局 Agent。

### 画布编辑器 chrome

| 表面 | ic | ic2 |
|--|--|--|
| 侧栏 Tab | canvas / assets / prompts（可拖宽、动画进出） | nodes / assets / prompts（可拖宽） |
| 侧栏节点 | 类型图标、状态色、分组折叠、多选、导出、预览、定位 | 纯文本 `type + title`，点击定位 |
| 侧栏资产 | 分类网格缩略图，hover 插入/删除 | `kind · id · KB`，**不能插入** |
| 侧栏提示词 | 按来源分组、详情、复制/插入文本节点 | 标题+50 字预览，点击插入 prompt 节点（有） |
| 多选工具条 | 仅 成组 / 解组，贴在选区上方 | **更强**：六向对齐 + 分布 + 分层 + 锚点 + 成组 + 删除（底部居中） |
| 节点悬浮条 | 图标胶囊 + 可配置显隐/标签；图像 12 动作 + 4 基础 | 11px 文字按钮；`IMAGE_TOOLS` 9 项 + info/copy/retry/save/download/delete |
| 图像工具 ID | copyPrompt, reversePrompt, **replace**, **resize**, mask, **faceGuard**, crop, split, upscale, superResolve, angle, view | view, mask, crop, split, upscale, superRes, multiAngle, reversePrompt, videoFrame |
| 右键（节点） | 视频首/中/尾帧、成组/解组、复制、删除 | 复制、成组/解组、对齐/分布/分层、生成、抽帧（单点）、存资产、删除 |
| 右键（空白） | 独立 `NodeCreateMenu` | 按 `NODE_SCHEMAS` 列出可建类型（覆盖更全） |
| 连线落空白 | `ConnectionCreateMenu`（文/图/视频/音频/配置） | 内核有 `commit-connect-blank`，**无类型选择菜单** |
| 快捷键文件 | `pages/canvas/project.tsx` 处理函数 + `canvas-top-bar` 模态 + `lib/keyboard-event.ts`（IME） | `CanvasPage` + `kernel.handleShortcut` + `CanvasTopBar` 模态 + `interaction.ts`（空格/Esc） |
| 快捷键实际 | Z/Y/A/G/Shift+G/C/V/Delete/Esc + 系统剪贴板图/文 | Z/Y/A/Delete/Esc；**C/V 声明未接线**；无 G |
| 底栏 Dock | 图标：选择/平移、撤销重做、五类节点、插件、上传、外观、删/清空 | 文字：撤销重做、按 schema 建节点、上传（含替换）、外观、删除 |

生产 ic 相对上游多出的本地 overlay（不要和「上游没有」搞混）：

- `canvas-node-face-guard-dialog.tsx`、`lib/face-guard.ts`
- `canvas-image-toolbar-tools.tsx` / hover-toolbar / image workbench 有对应补丁

### 工作台 image / video

| | ic `pages/image` 905 行 + `pages/video` 801 行 | ic2 `WorkbenchPage` 431 行 + 子组件 |
|--|--|--|
| 参考栏 | 上传 / 剪贴板 / 拖入 / 左右排序 / 资产库点选 / 编号角标 | 上传 / 剪贴板 / 拖入 / 左右排序 / 编号角标（`order` 与提交序强制一致，有单测） |
| 提示词库 | `PromptSelectDialog` | 无 |
| 资产库 | `AssetPickerModal` | 无 |
| 编号写进 prompt | `imageReferenceLabel` →「参考图片编号：图片1、图片2」 | 角标有，**不写进 prompt 前缀** |
| 结果 | 下载 / 存资产 / **结果加为参考** | `ResultGrid`：下载 / 存资产 / **加为参考** / 单张重试（有） |
| 历史 | Drawer + 预览 Modal + 回填（含参考图） | 页内面板：成功/失败分计、缩略图、回填模型+参数、多选删（有） |
| Agent 驱使 | `useWorkbenchAgentStore` 可远程下发生图 | 无工作台 Agent 命令总线 |

工作台缺口主要是**入口密度**（库、提示词、编号前缀），不是「没有历史」。

### Agent

| | ic | ic2 |
|--|--|--|
| 出现位置 | 全站顶栏 + 画布右侧停靠 | 仅画布浮层 |
| 子面板 | chat / **setup** / **history** / skills / **log** | chat / tools / skills |
| 连接 | `AgentConnectView`、URL bootstrap、协议握手、MCP 启动态 | 下拉 server/local + `BridgePanel` |
| 聊天 | 时间线、用量条、附件（最多 6）、画布引用预览、审批 | 回合列表 + 审批卡片（工具名/范围/成本） |
| 代码量 | `components/agent` ≈ 5050 行（`local-agent-panel.tsx` 1603） | `features/agent` ≈ 865 行 |

### 设置

| | ic | ic2 |
|--|--|--|
| 形态 | 6 Tab + 渠道抽屉 + 脚本全屏编辑 | 单页纵向 section，无 Tab |
| 面板 | 渠道、本地代理、偏好（默认模型/生图张数/音频）、提示词源、WebDAV、本地存储用量 | 偏好、直连模式、脚本编辑器、渠道+凭据表单、提示词源、导入导出、插件、限额 |
| 深度 | 渠道抽屉改模型/格式/Key（明文在浏览器，DIV-02） | 渠道表单 + `ModelSelectModal`；Key 只提交一次、回显掩码 |
| 怪味 | — | `ConfigTransfer` 与 `ModelSelectModal` **各挂了两遍**（`SettingsPage.tsx`） |

ic2 面板「个数」不少（约 8 块），但**没有分层**，渠道编辑远浅于 ic 抽屉。WebDAV / 本地存储 / 本地代理是 ic 才有——后两者不该抄。

### 行数 / 文件数（粗）

不含测试。ic 的 `web/src` 合计 174 文件 / ~33.7k 行；ic2 `web/src` 120 文件 / ~21.5k 行。

| 范围 | ic | ic2 |
|--|--|--|
| 画布页入口 | `pages/canvas/project.tsx` **3416** + `index.tsx` 130 | `CanvasPage.tsx` **222** |
| 画布 UI 组件 | `components/canvas` 40 文件 / **8186** | `features/canvas/components` 14 文件 / **3340** |
| 画布内核/lib | `lib/canvas` 1370 + stores 218 | `kernel` 16 文件 / **3121**（ic2 把几何/撤销/对齐沉到内核，这是优点） |
| 画布工具对话框 | 多个独立 dialog（mask/crop/split/upscale/angle/face-guard） | `tools/` 8 文件 / 1408 |
| features/canvas 合计 | pages+components+lib+stores ≈ **13.6k** | 无测试 **8748**（内核占 1/3） |
| 工作台 | image 905 + video 801 | WorkbenchPage 431 + ReferenceBar/History/Settings |
| Agent UI | ~5050 | ~865 |
| 设置 UI | config 相关 ~1200 | settings 1633（含直连/脚本/导入，更深在安全侧） |

结论：ic2 画布**内核并不瘦**；瘦的是 chrome（project.tsx 那 3k 行堆出来的手感还没搬完）。

### i18n（zh-CN）

粗算叶子键（`key: 标量`）：

| | 文件 | 行 | 叶键（约） |
|--|--|--|--|
| ic | `web/src/i18n/locales/zh-CN.ts` | 679 | **530** |
| ic2 | `web/src/shared/i18n/zh-CN.ts` | 403 | **347** |

ic 多在 `canvas.*`（工具/快捷键/侧栏）、`config.*`、`agent.runtime.*`、`apiErrors.*`、`imageWorkbench.*` / `videoWorkbench.*`。ic2 纪律更好（en-US 键集单测强制对齐，错误走 `errors.<code>`，DIV-07）。缺的是画布/工作台交互文案，不是框架。

---

## 分区对照表

严重度：S = 每天踩、W1 该动；M = 主路径多一步或找不到；L = 锦上添花或可后置。

| 区 | ic 有 | ic2 有 | 缺口 | 严重度 |
|--|--|--|--|--|
| 顶栏导航 | 6 图标项 + 下划线 | 7 文字项（多 Home/画布列表） | 无图标、无全局 Agent | M |
| 移动抽屉 | 真 Drawer | `display:none` 桩 | 窄屏不可用 | S |
| 画布顶栏 | 标题编辑、撤销菜单、快捷键模态、Agent 状态灯 | 返回、版本/节点数、同步徽章、Agent/Run/快捷键/导出 | 快捷键模态中英混写；无标题就地编辑手感 | M |
| 侧栏 Tab | 3 | 3 | Tab 齐，内容不齐 | — |
| 侧栏节点 | 树/状态/多选导出/预览 | 文本列表+定位 | 缺导出/预览/状态 | M |
| 侧栏资产 | 缩略图网格 + 插入 | 文本清单 | **不能插入** | S |
| 侧栏提示词 | 分组/详情/复制 | 插入 prompt 节点 | 缺详情与分组 | M |
| 多选工具条 | 成组/解组 | 对齐/分布/分层/成组/删除 | ic2 **超前**，不要回退 | — |
| 悬浮工具条 | 图标胶囊 + 16 项可配置 | 文字按钮 + 9+6 | 手感空；缺 replace/resize/faceGuard；超分假门 | S |
| 右键节点 | 三帧抽取 + 复制删除 | 更长（对齐/生成/存资产） | 三帧 vs 单点抽帧 | L |
| 右键空白 | 创建菜单 | schema 全类型创建 | ic2 不弱 | — |
| 连线落空白 | 类型选择菜单 | 无 UI | 少一步「拖出来就建」 | M |
| 快捷键 | C/V/G/Shift+G 真能用 + IME 防护 | 声明 C/V，未接线；无 G | 说明书撒谎 | S |
| 底栏 | 图标 Dock + 选择/平移 | 文字 Dock | 无平移拨杆、无图标 | M |
| 工作台参考栏 | 库+粘贴+拖+排序+编号前缀 | 粘贴+拖+排序+编号角标 | 无资产库/提示词库/前缀 | M |
| 工作台历史 | Drawer+大图+回填 | 页内面板+回填+分计 | 形态不同，功能接近 | L |
| 工作台结果 | 下/存/转参考 | 下/存/转参考/单张重试 | ic2 单张重试更好 | — |
| Agent 入口 | 全站 | 仅画布 | 工作台/首页找不到 | S |
| Agent 子面板 | 5 | 3 | 无 setup/history/log | S |
| 设置 IA | 6 Tab + 抽屉 | 8 section 长页（两处重复挂载） | 找不到、重复 | M |
| 设置安全 | Key 在 localStorage | 掩码 + 服务端加密 | **不要抄 ic** | — |
| i18n | ~530 叶键 | ~347 叶键，键集测试更严 | 画布/Agent 文案薄 | L |

---

## 有意偏离（禁止为了「更像 ic」而抄）

完整表在 `docs/upstream/divergences.md`。前端体验波次里**明确不要搬**的：

| ID | 不要抄的东西 | 原因 |
|--|--|--|
| DIV-01 | IndexedDB 当唯一真源、`localforage` 生图日志库、设置里的「本地存储用量」面板当产品能力 | 数据在 Workspace；浏览器只缓存 |
| DIV-02 | 渠道抽屉里明文 API Key、localStorage 持 Key、前端直连第三方 | 设置页只提交一次、只回显掩码 |
| DIV-03 | 浏览器 `new Function` 跑模型调用脚本 | 脚本编辑器可以长得像 ic，执行必须在服务端沙箱 |
| DIV-04 | `Blob URL + import()` 插件进主页面 | 继续 iframe sandbox |
| DIV-05 | 把 Run/Step/Attempt 收成一次 `await` | ic2 的 Run 面板是优点 |
| DIV-06 | WebDAV 四域同步 UI | 服务端权威 + op 日志已经替代 |
| DIV-07 | 从 `error.message` 正则猜失败原因 | 继续 `errors.<code>` |
| DIV-08 | 每种模型一座专用表单组件 | 继续 schema 驱动 |
| DIV-09 | `primaryImageId` + 子节点双真相 | 继续 Variants |
| DIV-10 | 赞助商 banner、社群入口、GA4/百度统计、顶栏 GitHub 营销链 | 顶栏可以有文档入口，不要外链推广与统计脚本 |
| （衍生） | `canvas-proxy` / 「本地代理」Tab | ic2 同源 API，不需要 CORS 代理 |
| （衍生） | 登录墙拆掉、把画布做成纯本地 SPA | 开放访问是运维开关，不是把会话模型改回去 |
| （衍生） | 为对齐 ic 把 Key 放进 URL 并自动写入 | `PrefsPanel` 已做成「确认后导入」；保持 |

face-guard：生产 ic 有、上游 clone 无。要做就当 **W2 图像工具**，走服务端/已有资产管线，不要把 `public/mediapipe` 当「必须复刻的前端本地推理」。

---

## ic2 已经强于 ic、不要回退的点

- 多选对齐 / 等距 / 分层布局（ic 只有成组）。
- 空白右键按 schema 建节点；内核 `handleShortcut` 分层（只是还没接完）。
- 工作台单张独立重试 + 历史成功/失败分计。
- Run 面板、同步冲突徽章、服务端限额可见。
- 设置里的直连模式确认、URL 凭据确认导入、插件沙箱管理。

---

## 建议的 W1 第一刀（一个 PR 体量）

**标题：** 画布节点悬浮工具条：图标胶囊 + 把说明书上的 C/V/G 接上。

**为什么是第一刀：** Top 10 的 #1 和 #2 都发生在「选中一张图」这条最高频路径；不新增工具协议、不碰凭据、不搬 Agent。对齐 `DESIGN-frontend-vs-upstream.md` 的 W1「画布壳」，但把范围收成可一周内合并的一块。侧栏插入、底栏图标 Dock 放 W1-2。

**改哪些文件（预期）：**

1. `web/src/features/canvas/components/NodeHoverToolbar.tsx`  
   文字按钮 → 图标 + tooltip 胶囊（位置贴节点顶边，参考 ic `canvas-node-hover-toolbar.tsx` 的 h-12 / 圆角 / 分隔）。工具集合仍走现有 `imageToolsFor` + info/copy/retry/save/download/delete。用已有 `hiddenTools()` 做「⋯」显隐，不必先做 face-guard。
2. `web/src/features/canvas/CanvasPage.tsx`  
   `handleShortcut` 的 `copy` / `paste` 接到 `kernel.duplicateNodes`；paste 无内部剪贴板时走系统剪贴板图片（`CanvasToolbar.upload` 已有上传原语）。
3. `web/src/features/canvas/kernel/kernel.ts`  
   `handleShortcut` 增加 `g` / `shift+g` → `group` / `ungroup`（ops 已在）。
4. `web/src/features/canvas/components/CanvasTopBar.tsx` + `shared/i18n/{zh-CN,en-US}.ts`  
   `SHORTCUTS` 数组改 `t()`，删死中文；条目与真实行为一致（接上之后再写 C/V/G）。

**不要在这一 PR 做：** face-guard、超分真接入、侧栏资产网格、Agent 五 Tab、设置改 Tab、WebDAV、任何 Key 回浏览器。

**验收（人眼 + 已有 vitest）：**

- 选中图像节点：工具条是图标胶囊，不是一排 11px 字。
- Ctrl/Cmd+C 再 V：节点复制偏移出现；Ctrl/Cmd+G 成组，Shift+G 解组。
- 快捷键模态全部走 i18n，且没有「列出但按了没反应」的项。
- `kernel` 快捷键单测补 copy/paste/group；不新增前端明文 Key。

**W1-2（下一刀，不在本 PR）：** `SidePanel` 资产 Tab 改缩略图网格 + 点击插入（API 已有 `listAssets` / `createNode` + `set-spec.assetId`）。这是 Top 10 #3，仍然是画布壳。

