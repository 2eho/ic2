# 10 · 功能对等矩阵（复刻清单）

> 本文是**重写的验收基线**。逐项对照 `github.com/basketikun/infinite-canvas` v0.18.0 的真实实现，
> 每一条都给出：原实现位置 → 重写落位 → 验收方式 → 状态。
> 「状态」取值：`todo` / `wip` / `done` / `dropped`（明确不做，必须写理由）。
>
> 纪律：**任何功能只有在矩阵里出现并且有验收方式，才算「已复刻」**。
> 原仓库更新时，先改本文档，再改代码。

## 0. 原项目实现盘点（事实基线）

| 维度 | 事实 |
| --- | --- |
| 版本 | v0.18.0（MIT），38 个版本记录 |
| 形态 | 纯前端 SPA，`Dockerfile` 只启 nginx 托管静态产物 |
| 前端栈 | Vite 7 + React 19 + TS + React Router 7 + Ant Design 6 + Tailwind 4 + Zustand 5 + TanStack Query 5 + i18next |
| 持久化 | localForage / IndexedDB（`infinite-canvas` 库：`app_state` / `image_files` / `media_files` / `image_generation_logs` / `video_generation_logs` / `prompt_cache`） |
| 后端 | 无。可选 `canvas-proxy`（纯 CORS 转发，132 行） |
| 本地 Agent | `canvas-agent`（独立 npm 包 `@basketikun/canvas-agent@0.6.0`，Express + SSE + MCP + Codex app-server JSON-RPC 桥） |
| 代码规模 | 244 个 ts/tsx，约 6.5 万行（含 lock 文件）；`web/src` 173 个文件 |
| 巨型文件 | `pages/canvas/project.tsx` 3384 行、`components/agent/local-agent-panel.tsx` 1603 行、`services/api/model-plugin.ts` 993 行、`canvas-node.tsx` 960 行、`services/api/image.ts` 921 行 |
| 插件 | 6 个官方插件（markdown / svg / html / panorama / sticky-note / template）+ SDK + registry；运行时 `Blob URL + import()` 同源执行 |
| 提示词 | 7 个内置来源（`yukkcat/image-prompts` 的 dist/sources/*.json），浏览器直连 + IndexedDB 缓存 + 定时拉取 |
| 同步 | WebDAV（4 个域：canvas / assets / image-workbench / video-workbench），manifest + files/ 结构，按 `updatedAt` 合并 |
| i18n | zh-CN / en-US，各 657 行 |

**结论**：这不是一个「半成品」，功能面相当完整。重写的价值在于架构天花板，不在于功能缺失。
因此本文档的定位是**防止重写过程中的功能回退**。

## 1. 页面与信息架构

| # | 功能 | 原实现 | 重写落位 | 验收 | 状态 |
| --- | --- | --- | --- | --- | --- |
| 1.1 | 首页（介绍 + 提示词展示墙 + 入口） | `pages/home/index.tsx` 120 行 | `features/home/HomePage.tsx` | 首页可打开；点封面打开大图预览（`shared/components/ImageLightbox`，ESC/遮罩关闭、锁滚动、加载失败有兜底链接） | done |
| 1.2 | 我的画布（项目卡片列表） | `pages/canvas/index.tsx` 130 行 | `features/canvas/ProjectList` | 新建/重命名/复制/删除/导入/导出 | done |
| 1.3 | 画布编辑器 | `pages/canvas/project.tsx` 3384 行 | `features/canvas/*` 拆 20+ 模块 | 见第 3 节 | done |
| 1.4 | 生图工作台 | `pages/image/index.tsx` 903 行 | `features/workbench/WorkbenchPage`（mode=image） | 见第 6 节（6.1–6.7 全部 done） | done |
| 1.5 | 视频创作台 | `pages/video/index.tsx` 801 行 | `features/workbench/WorkbenchPage`（mode=video） | 见第 6 节（6.8–6.12 全部 done） | done |
| 1.6 | 提示词库 | `pages/prompts/index.tsx` + `components/prompts/*` | `features/prompts` | 分类/标签/搜索/详情/复制/存资产 | done |
| 1.7 | 我的素材 | `pages/assets/index.tsx` 563 行 | `features/assets` | 见第 7 节 | done |
| 1.8 | 配置中心 | `pages/config/index.tsx` + `components/layout/app-config-modal.tsx` | `features/settings` | 见第 8 节 | done |
| 1.9 | 顶栏导航 + 移动端抽屉 | `components/layout/app-top-nav.tsx` / `mobile-nav-drawer.tsx` | `app/Shell` | 导航可用，画布页隐藏顶栏 | done |
| 1.10 | Agent 侧边栏（全局） | `components/agent/agent-panel.tsx` + 12 个子组件 | `features/agent/AgentSidebar` + `BridgePanel` + `SkillsPanel` | 见第 9 节（9.11 已覆盖） | done |
| 1.11 | 404 页 | `pages/not-found` | `app/NotFound` | 任意未知路由展示 404 | done |
| 1.12 | 快捷键说明弹窗 | `canvas-top-bar.tsx` 内 `Shortcut` | `features/canvas/ShortcutsModal` | 内容与原项目 15 条一致 | done |

## 2. 画布数据模型（对等）

| # | 字段/能力 | 原实现 | 重写落位 | 验收 | 状态 |
| --- | --- | --- | --- | --- | --- |
| 2.1 | 节点类型 image/text/video/audio/config/group | `types/canvas.ts` `CanvasNodeType` | 判别联合 `NodeSpec`（见 02-domain-model） | 六种节点均可创建与渲染 | done |
| 2.2 | 插件节点类型 `<pluginId>:<name>` | `CanvasNodeTypeId = CanvasNodeType \| (string & {})` | 同上 + manifest 校验 | 未安装插件时展示占位与安装提示 | done |
| 2.3 | 节点状态 idle/success/loading/error | `CanvasNodeStatus` | `NodeState` 状态机 | 非法转移被服务端拒绝 | done |
| 2.4 | 多图结果（`images[]` + `primaryImageId`） | 节点内数组 + 主图指针 | `State.Result.Variants[] + Primary` | 批量生成后折叠展示、切换主图、单张重试 | done |
| 2.5 | 多文本结果（`texts[]` + `primaryTextId`） | 同上 | 同上 | 批量文本可展开切换 | done |
| 2.6 | group 归属（`groupId`） | 扁平 metadata 字段 | `Node.ParentID` + 服务端校验不成环 | 打组/解散/拖入拖出 | done |
| 2.7 | 连线仅记 from/to | `CanvasConnection` | `Edge{From,To,Kind}` 显式 Port | 类型不匹配拒绝连线 | done |
| 2.8 | 视口 x/y/k | `ViewportTransform` | `Viewport` | 打开画布恢复视口 | done |
| 2.9 | 背景模式 lines/dots/blank | `CanvasBackgroundMode` | `CanvasSettings.Background` | 三种背景可切换 | done |
| 2.10 | 图片信息开关 `showImageInfo` | project 字段 | `CanvasSettings.ImageInfo` | 开关状态持久化 | done |
| 2.11 | 助手会话随画布保存 | `chatSessions` / `activeChatId` | `AgentSession`（服务端，与 canvasId 绑定）+ `/export` 附带只读快照 | 会话持久化成立；导出含会话快照，非文本条目显式标 `redacted`（工具参数可能含凭据/内网地址） | done |
| 2.12 | 旧数据迁移 | 无（AGENTS.md 声明不兼容） | `legacy/` 转换器 + `POST /canvases/import` | 原用户一键导入历史画布与图片 | done |

### 2.13 原项目 metadata 字段 → 重写落位映射（逐字段，防漏）

`CanvasNodeMetadata` 共 40+ 可选字段，必须全部有归宿：

| 原字段 | 重写落位 |
| --- | --- |
| `content` | `Spec.text`(prompt 节点) / `Spec.assetId`(媒体节点) |
| `composerContent` | `Spec.promptTemplate`（generation 节点） |
| `prompt` / `status` / `errorDetails` | `Spec.prompt` / `State.Status` / `State.Error` |
| `fontSize` | `Spec.fontSize` |
| `generationMode` / `generationType` | `Spec.capability` / `Spec.editMode` |
| `model` / `reasoningEffort` | `Spec.model` / `Spec.reasoningEffort` |
| `size` / `quality` / `background` / `count` | `Spec.params.*`（schema 校验） |
| `textCount` / `texts` / `primaryTextId` | `State.Result`（见 2.5） |
| `seconds` / `vquality` / `generateAudio` / `watermark` / `videoMode` | `Spec.params.*` |
| `audioVoice` / `audioFormat` / `audioSpeed` / `audioInstructions` | `Spec.params.*` |
| `references` | `Edge` + `Run.Inputs`（不再存 URL 数组） |
| `naturalWidth` / `naturalHeight` / `bytes` / `mimeType` | `Asset.Meta` |
| `freeResize` | `Spec.freeResize` |
| `images` / `primaryImageId` | `State.Result` |
| `storageKey` | **删除**（资产统一 `assetId`） |
| `durationMs` | `Asset.Meta.DurationMs` |
| `videoTaskId` / `videoTaskProvider` | `Attempt.RemoteTask{ID,Provider}`（进程重启可续查） |
| `groupId` | `Node.ParentID` |
| `interactive` | `Spec.interactive`（插件节点） |

**验收**：写一个 `legacy-mapping.spec.ts`，遍历原项目 `CanvasNodeMetadata` 的每个 key，断言存在映射或显式标注 `dropped`。

## 3. 画布交互（对等）

| # | 功能 | 原实现 | 验收 | 状态 |
| --- | --- | --- | --- | --- |
| 3.1 | 无限画布平移/缩放（滚轮 + 滑杆 + 重置） | `infinite-canvas.tsx` | 缩放范围 0.05–5，指针为锚点 | done |
| 3.2 | 空格/Ctrl 临时切换 选择 ⇄ 移动 | 同上 | 按住生效，松开还原 | done |
| 3.3 | 框选（Ctrl/Cmd + 拖拽），Shift 追加 | `handleGlobalPointerMove` | 交集判定；**Shift 追加与反选由内核持有语义**（`CanvasKernel.setSelection(sel,{additive})` / `toggleSelection`，`CanvasSurface` 只传命中集合与修饰键）。曾出现「状态机产出 `additive`、内核 `select` 分支丢弃它」的假绿（Shift 点击静默覆盖选区，多选堆不出来、多选工具栏入口失效），已修并补回归用例（见 `kernel/__tests__/kernel.test.ts`） | done |
| 3.4 | 节点拖拽（多选联动、组内成员跟随） | `handleNodeMouseDown` + rAF | 拖组时成员一起移动 | done |
| 3.5 | 节点八向缩放 + 图片等比锁定 | `canvas-node.tsx` resize | `freeResize=false` 时保持原始比例 | done |
| 3.6 | 连线拖拽创建 + 连线校验 | `normalizeConnection` | 配置节点之间禁止连线（原项目明确报错） | done |
| 3.7 | 从连线末端拖到空白 → 创建节点菜单 | `ConnectionCreateMenu` | `kernel/interaction.commitConnect` + `CanvasSurface.ConnectCreateMenu` | 端口拖拽（屏幕像素命中，缩放后判定一致）；落点空白弹菜单，只列类型兼容的节点，创建后自动连线 | done |
| 3.8 | 画布右键 → 节点创建菜单 | `NodeCreateMenu` | 列出内置 + 已启用插件节点 | done |
| 3.9 | 节点右键菜单（复制/打组/解散/删除/截视频帧） | `CanvasNodeContextMenu` | 5 项齐备，条件显示 | done |
| 3.10 | 悬停工具栏（信息/删除/重试/存资产/下载/编辑文字/生图/字号） | `canvas-node-hover-toolbar.tsx` | 按节点类型条件渲染 | done |
| 3.11 | 图片快捷工具（复制提示词/反推/替换/锁比例/蒙版/裁剪/切图/放大/超分/多角度/查看） | `canvas-image-toolbar-tools.tsx` | 11 项齐备，可自定义显示 + 显示文字开关，配置存 localStorage | done |
| 3.12 | 底部 Dock 工具栏（工具/撤销重做/六种节点/插件节点/上传/外观/删除/清空） | `canvas-toolbar.tsx` 378 行 | 全部入口可用 | done |
| 3.13 | 外观面板（主题 亮/暗 + 网格样式 + 图片信息） | 同上 | 三项均生效并持久化 | done |
| 3.14 | 小地图（开关 + 拖拽定位） | `canvas-mini-map.tsx` | 开关常驻，点击跳转视口 | done |
| 3.15 | 缩放控件（滑杆/百分比/重置） | `canvas-zoom-controls.tsx` | 联动视口 | done |
| 3.16 | 左侧面板（画布元素/资产/提示词 三 Tab，可拖宽） | `canvas-side-panel.tsx` 610 行 | 宽度持久化到 localStorage | done |
| 3.17 | 元素列表（类型筛选/搜索/组树形展开/定位/预览/批量选择导出） | `CanvasNodesTab` | 组内子节点可折叠；批量导出 zip | done |
| 3.18 | 多选工具栏（打组/解散） | `canvas-selection-toolbar.tsx` | 虚线选区 + 工具栏 | done |
| 3.29 | **一键对齐 / 等间距分布**（本仓新增，原项目无） | 原项目仅拖拽时 `alignmentGuides` 吸附辅助线，无「对多选节点一键对齐」入口 | `kernel/geometry.ts` `alignOffsets` / `distributeOffsets`（纯函数）+ `CanvasKernel.alignSelection` / `distributeSelection` + `SelectionToolbar` | 六向对齐（左/水平居中/右/顶/垂直居中/底）与水平/垂直等距：位移为绝对目标坐标（重复点击幂等，不漂移）；已对齐时按钮置灰；少于此数（对齐 <2、分布 <3）不产生 op；只读画布不生效；对齐可被一次 Ctrl+Z 整体还原；`readOnly` / NaN 输入被丢弃（见 `kernel/__tests__/align.test.ts` 23 例） | done |
| 3.30 | **分层成列 / 按层对齐**（本仓新增，原项目无） | 原项目既无批量对齐入口，也无「按连线层级整理」能力 | `kernel/geometry.ts` `layerRanks` / `layeredColumnOffsets`（纯函数）+ `CanvasKernel.layoutByLayers` / `isLayeredAlready` + `SelectionToolbar` / 右键菜单 | 按连线拓扑层级成列：**同一层的节点对齐到同一列（x 轴相同）**，列间距按该层最宽节点 + 间距，列内按原顺序自上而下铺开；用最长路径定层，连线不会倒退；成环时**先做强连通分量（SCC）收缩再定层**（迭代式 Tarjan，不递归不爆栈）：环上节点互为上下游故共享同一层，环下游的无环节点仍拿到正确层号。曾出现「Kahn 只给定层到入度归零的节点」导致**图里只要有一个环，整个下游子图被压进同一列**的缺陷，已修（见 `kernel/__tests__/layers.test.ts`「环上节点同层，环下游的无环节点仍拿到正确层号」等 5 例）；位移为绝对坐标（重复点击幂等）；不足 2 个不产生 op；只读画布不生效；整理可被一次 Ctrl+Z 整体还原（见 `kernel/__tests__/layers.test.ts` 26 例）。**不做**：连线交叉优化 / 自动改道 / 删除节点（那是 Dagre 级全图重排，另议） | done |
| 3.19 | 撤销/重做（50 步，含视口/背景/助手会话） | `historyRef` 180ms 防抖合并 | 组合键 Ctrl+Z / Ctrl+Shift+Z / Ctrl+Y | done |
| 3.20 | 复制/粘贴（内部剪贴板 + 系统剪贴板图片/文本） | `pasteSystemClipboard` | 粘贴节点、粘贴图片文件、粘贴文本 | done |
| 3.21 | 全选（Ctrl+A）、Esc 清空选择并关浮层 | `handleKeyDown` | 与文档一致 | done |
| 3.22 | 拖入图片/视频/音频到画布 | `handleDrop` | 多文件 40px 错位排布 | done |
| 3.23 | 上传替换节点（选中节点时上传即替换） | `handleImageInputChange` | 首个替换，其余新建 | done |
| 3.24 | 节点内容编辑（文本 textarea、双击） | `canvas-node.tsx` | 文本可直接编辑；图片/视频双击进面板 | done |
| 3.25 | 节点标题双击重命名 | 同上 | 空值回退原名 | done |
| 3.26 | 视口裁剪（可见节点才渲染） | `visibleNodes` padding 280 | 5000 节点 ≥55 FPS | done |
| 3.27 | 关联高亮（邻接节点与连线高亮） | `relatedHighlight` | 悬停/单选时高亮上下游 | done |
| 3.28 | 动画聚焦节点（450ms easeOutCubic） | `focusNode` | 侧栏点击定位并缩放 | done |

## 4. 生成与执行（对等）

| # | 功能 | 原实现 | 重写落位 | 验收 | 状态 |
| --- | --- | --- | --- | --- | --- |
| 4.1 | 文本生成（`/v1/responses` + SSE 流式） | `image.ts requestImageQuestion` | `provider/adapter/openai` | 流式增量渲染 | done |
| 4.2 | 生图（`/v1/images/generations`） | `requestGeneration` | 同上 | 单张/多张 | done |
| 4.3 | 图生图/编辑（`/v1/images/edits` multipart，多图用 `image[]`） | `requestEdit` | 同上 | 多参考图不因重复 `image` 字段被拒 | done |
| 4.4 | 视频生成（`POST /v1/videos` → 轮询 → `/content`） | `video.ts` | provider + `exec` 异步任务 | 刷新/重启后仍能续查 | done |
| 4.5 | 音频生成（`/v1/audio/speech`，返回 Blob） | `audio.ts` | 同上 | mp3/wav/opus 等格式 | done |
| 4.6 | Gemini 文本（`streamGenerateContent?alt=sse`） | `requestGeminiStreamingResponse` | gemini adapter | 流式 | done |
| 4.7 | Gemini 生图（`generateContent` + `generationConfig.imageConfig`） | `requestGeminiImagesOnce` | 同上 | aspectRatio + imageSize 正确 | done |
| 4.8 | Gemini 视频（`predictLongRunning` + 轮询 operation） | `createGeminiVideoTask` | 同上 | 任务名持久化可续查 | done |
| 4.9 | Gemini TTS（`responseModalities:["AUDIO"]` + `speechConfig`） | model-plugin gemini 音频模板 | 同上 | 返回 base64 PCM 可播放 | done |
| 4.10 | 模型列表拉取（OpenAI `/v1/models`、Gemini `/v1beta/models`） | `fetchImageModels` | `provider` | 拉取后按关键词猜能力 | done |
| 4.11 | 自定义渠道 + 每模型能力（image/video/text/audio） | `use-config-store.ts` 496 行 | `providers` 表 | 多渠道、按能力选模型 | done |
| 4.12 | 自定义调用脚本 | `model-plugin.ts` `runModelPlugin` | `internal/sandbox` + `internal/provider/adapter/script` | 脚本产出请求描述，出口在服务端：凭据由平台注入（脚本不能设 Authorization）、只能请求渠道 baseUrl 下的路径、`responsePath` 决定结果位置 | done |
| 4.13 | 脚本模板（OpenAI/Gemini × image/video/audio/text 共 8 个） | `getPluginTemplates()` | 同结构模板 | 逐个可运行 | done |
| 4.14 | 脚本编辑器 UI | `scriptEditor` i18n 有大段文案 | `features/settings/ScriptEditor.tsx` | 三步向导（映射 → 请求预览 → 确认保存）；保存前做静态检查：未定义变量、白名单外函数、被拒写法 | done |
| 4.15 | 失败分类与提示（401/403/429/404/502/503、HTML 错误页、超时、取消） | `readStatusError` / `readApiErrorMessage` | `DomainError.Code` + i18n | 错误码稳定枚举 | done |
| 4.16 | 取消生成 | `AbortController` | context 级联取消 | 取消后节点回到 idle | done |
| 4.17 | 参考图编号注入提示词（"参考图片编号：图片1、图片2…"） | `buildImageReferencePromptText` | 结构化 `ComposedPrompt.Inputs` | 不再依赖文本约定 | done |
| 4.18 | 上游文本按「文本N」分块编号 | `buildNodeGenerationContext` | 同上，端口 `Order` 决定顺序 | 多段文本不错位 | done |
| 4.19 | 自定义脚本沙箱化 | `new Function` 在浏览器执行（安全缺陷） | `internal/sandbox`（受限解释器，**不引第三方**） | 无 eval/Function/原型链、无循环与递归（语法即拒绝）、宿主函数白名单、步数+墙钟+输出字节上限；ATK 用例实测逃逸手法全部被拒 | done |
| 4.20 | 本地代理开关（绕 CORS） | `withLocalProxy` + `canvas-proxy` | 服务端同源转发（无需代理） | 前端不再需要代理 | dropped |
| 4.21 | 本地直连模式（保留兼容） | 默认行为 | `workspace.Prefs.Direct` + `features/settings/DirectConnectPanel` | 默认关闭；开启必须同时给 `acknowledgedAt`（服务端校验，UI 绕不过）；地址必须是回环（否则等于服务端代任意地址发请求）；生效范围默认仅 image.generate | done |
| 4.22 | 生成中断标记（刷新后 loading → error「已中断」） | `resetInterruptedGeneration` | 服务端 Run 状态收敛 | 刷新后状态准确 | done |
| 4.23 | Run 记录/重放/对比（原项目无） | — | 见 05-execution-engine | RunPanel 可用 | done |
| 4.24 | 计量与成本（原项目无） | — | `Usage` 微元 | RunPanel 展示 token/张数/秒/成本 | done |

## 5. 画布内图像工具（原项目最重的一段，逐项复刻）

| # | 工具 | 原实现 | 验收 | 状态 |
| --- | --- | --- | --- | --- |
| 5.1 | 覆盖蒙版局部编辑（手绘遮罩 → 生成遮罩标注图 → 作为参考图 2 走 edit） | `canvas-node-mask-edit-dialog.tsx` 438 行 + `maskEditImageNode` | `dialogs/generative.tsx` `MaskDialog`（蒙版上传为真实资产 → 图片2） | 画笔/擦除/笔刷/撤销/仅导出；确认时蒙版落库成素材并连到 edit 节点的 ref 端口；空蒙版显式拒绝（否则会花一次费用做无意义重绘） | done |
| 5.2 | 裁剪（自由/固定/原图三种比例，缩放拖动） | `canvas-node-crop-dialog.tsx` + `cropDataUrl` | 生成新节点并连线 | done |
| 5.3 | 切图（行列 + 自定义横竖切线 + 撤销重做） | `canvas-node-split-dialog.tsx` 299 行 + `splitDataUrl` | 按原网格排列到右侧 | done |
| 5.4 | 放大（目标边长 ≤4096，最近邻/双线性/高清插值） | `canvas-node-upscale-dialog.tsx` + `upscaleDataUrl` | 三种算法结果可区分 | done |
| 5.5 | AI 超分 | 原项目**未实现**（弹窗提示「暂未实现」） | `openai.imageUpscale`（走 edits 端点）+ `adapter/script` 可自接超分渠道 | 真的发请求并产出结果；无源图时**发请求前**报错；参考图按序号上传（`ref1.png`） | done |
| 5.6 | AI 多角度（水平/俯仰/镜头距离/广角 → 生成编辑提示词） | `canvas-node-angle-dialog.tsx` + `buildAnglePrompt` | 文案与角度标签一致 | done |
| 5.7 | 视频截帧（首帧/尾帧/当前帧 → 图片节点） | `canvas-video-frame.ts` | 生成节点并连线，避让已有节点 | done |
| 5.8 | 反推提示词（图片 → 文本节点 + 配置节点 + 连线） | `createImageReversePromptNodes` | 三节点布局与提示词文案一致 | done |
| 5.9 | 复制提示词 | `copyImagePrompt` | 复制到剪贴板并提示 | done |
| 5.10 | 图片查看大图 / 图片详情 | `handleNodeViewImage` / `CanvasNodeInfoModal` | 信息视图 + JSON 视图（base64 折叠） | done |
| 5.11 | 多图组展开/收起/设为主图/复制/下载/删除/单张重试 | `setBatchPrimary` 等 6 个回调 | 全部可用 | done |
| 5.12 | 文本多结果展开/设为主文本 | `CanvasNodeText` | 可用 | done |

> 5.1–5.7 全部是**浏览器端 Canvas API 图像处理**，纯函数、可单测。
> 重写时统一放 `features/canvas/tools/*`，与 UI 解耦，并补 vitest。
> 一个已知坑：蒙版编辑在**缩放期间**渲染面板会导致崩溃（原 CHANGELOG v0.16.0 修复），
> 重写后由 `isNodeResizing` 门控，写进回归用例。

## 6. 工作台（生图 / 视频）

| # | 功能 | 原实现 | 验收 | 状态 |
| --- | --- | --- | --- | --- |
| 6.1 | 生图：提示词 + 参考图（上传/剪切板/拖入/排序/编号角标/删除） | `pages/image/index.tsx` | `workbench/components/ReferenceBar` + `renumber` | 角标编号恒等于提交顺序（单测穷举交换/删除后的重编号） | done |
| 6.2 | 生图：参数面板（模型/尺寸/质量/张数 1–10/背景） | `ImageSettingsPanel` | `workbench/components/ImageSettingsPanel` | 档位×比例组合出合法尺寸，手工微调自动对齐 16 倍数并回显实际提交值 | done |
| 6.3 | 生图：并发生成 N 张 + 单张失败独立重试 | `Promise.allSettled` + `runGenerationSlot` | `workbench/generate.ts` 并发单张 Run | 部分失败不影响成功项；失败项独立重试不重复扣费（幂等键） | done |
| 6.4 | 生图：历史记录（卡片/缩略图/成功失败计数/耗时/勾选/批量删/点击回填参数） | `LogPanel` / `LogCard` | `workbench/useWorkbenchLogs` + `WorkbenchHistory` | 成功/失败分别计数；回填参数含模型；单张失败可定位到 errorCode | done |
| 6.5 | 生图：结果操作（存资产/加为参考/下载） | `ResultImageCard` | `workbench/components/ResultGrid` | 每张结果独立操作：下载 / 加为参考 / 单张重试 | done |
| 6.6 | 生图：从我的资产插入、从提示词库引入 | `AssetPickerModal` / `PromptSelectDialog` | 资产/提示词检索接口 + 参考图栏复用 | 检索走服务端；插入即成为参考图 | done |
| 6.7 | 生图：移动端抽屉式历史与设置 | `Drawer` | 历史/设置面板可折叠 | 小屏下设置与历史默认收起，主区域占满宽度 | done |
| 6.8 | 视频：提示词 + 参考图/视频/音频 | `pages/video/index.tsx` | 同 `ReferenceBar`（上限 3） | 三类参考按类型上传，超 2 张自动切全能参考 | done |
| 6.9 | 视频：参数（清晰度 480/720/1080、比例 6 种+auto、时长 4–30 滑杆、首尾帧/全能参考、生成声音、水印） | `VideoSettingsPanel` | `workbench/components/VideoSettingsPanel` | 清晰度作用于短边、边长取偶数；参考图 >2 张自动切全能参考并说明 | done |
| 6.10 | 视频：任务进度与轮询、失败原因 | 同上 | 时长与状态实时 | done |
| 6.11 | 视频：历史记录与资产沉淀 | 同上 | 与生图共用 `useWorkbenchLogs`（按 kind 隔离） | 可用 | done |
| 6.12 | 两个工作台的「Agent 命令」入口（`imageCommand` / `videoCommand` 响应 Agent 调用） | `use-workbench-agent-store.ts` | Agent 经 MCP/工具表调用同一直通生成接口 | Agent 可驱动工作台（与画布同一执行引擎） | done |
| 6.13 | 工作台与画布共用执行引擎 | 原项目各自直连（重复代码） | 统一 `exec` | done |

## 7. 素材库

| # | 功能 | 原实现 | 验收 | 状态 |
| --- | --- | --- | --- | --- |
| 7.1 | 三种类型 text/image/video，列表卡片 + 分页 + 类型筛选 + 关键词搜索 | `pages/assets/index.tsx` | 可用 | done |
| 7.2 | 新建/编辑（标题、封面、标签、来源、备注、正文/图片） | 同上 | `asset.Service.UpdateMeta` + `features/assets/AssetEditor` | 可改显示元数据；内容字段只读（改了会破坏内容寻址不变量） | done |
| 7.3 | 删除、复制文本、下载（读本地 blob 而非预览地址） | 同上 + CHANGELOG v0.18.0 修复 | 中文/空格/书名号标题可下载 | done |
| 7.4 | 打包导出 zip / 导入 zip（含媒体文件） | `asset-transfer.ts` | `shared/zip`（自研 store 模式）+ `features/assets/transfer` | 往返一致（同内容两次导出字节相同）；zip slip / zip bomb 有防护；缺文件与跳过条目显式上报 | done |
| 7.5 | 与画布互操作（画布存资产、资产插入画布） | 双向 | 画布侧「存为素材」+ 素材侧「复制 ID」 | 双向可用；画布只存 assetId，不再把 dataURL 塞进节点 | done |
| 7.6 | 引用计数与 GC | 前端 `cleanupImages` 遍历 IndexedDB | 服务端 `asset_refs` + 延迟回收 | done |

## 8. 配置与同步

| # | 功能 | 原实现 | 验收 | 状态 |
| --- | --- | --- | --- | --- |
| 8.1 | 渠道 CRUD（名称/协议 openai\|gemini/BaseURL/APIKey/模型列表） | `app-config-modal.tsx` | 多渠道路由 | done |
| 8.2 | 模型选择器（拉取列表 + 手动增加 + 已有/新获取分栏 + 全选） | `model-select-modal.tsx` | `features/settings/ModelSelectModal` | 已有/新获取分栏；能力显式落库（关键词表演进不漂移） | done |
| 8.3 | 每模型能力标注 + 关键词猜测 | `guessCapability` | 可覆盖 | done |
| 8.4 | 默认模型四类（image/video/text/audio） | `config.preferences` | 节点可覆盖默认 | done |
| 8.5 | 生成偏好（画布默认张数、音频声音/格式/语速/指令、系统提示词） | 同上 | `workspaces.settings.prefs.generation` + `PrefsPanel` | 服务端存储（多端一致、可备份）；系统提示词随请求附加 | done |
| 8.6 | 界面语言（zh-CN / en-US） | `i18n` | 切换即时生效并持久化 | done |
| 8.7 | 主题（亮/暗） | `use-theme-store` | 全局生效 | done |
| 8.8 | 配置导入/导出（JSON，含 Key 与 WebDAV 凭据 + 安全提示） | `config-file.ts` | `workspace.Service.Export/Import` + `ConfigTransfer` | 往返一致；**默认导出不含凭据**，含凭据需显式选择且带 warning 字段 | done |
| 8.9 | URL 参数导入凭据（`?baseUrl=&apiKey=`，导入后从地址栏清除） | `client-root-init.tsx` | `features/settings/url-import.ts` | 读取即清除（replaceState，后退不回带密钥 URL）；需用户确认后才写入 | done |
| 8.10 | 提示词来源 CRUD + 启用开关 + 立即拉取 + 定时拉取（30m/1h/6h/24h） | `config-prompt-sources.tsx` + `use-prompt-source-scheduler.ts` | 失败保留旧缓存 | done |
| 8.11 | 本地代理页（命令提示 + 地址 + 测试连接） | `config-local-proxy.tsx` | 见 4.20（改由服务端承担） | dropped |
| 8.12 | IndexedDB 用量统计（按对象仓库估算 + 浏览器配额） | `config-local-storage.tsx` | 改为服务端存储用量统计 | done |
| 8.13 | WebDAV 测试连接 + 同步 + 分域进度 + 上次同步时间 | `webdav-sync.ts` + `app-sync.ts` | — | **dropped**：DIV-06 已改用服务端权威同步；保留「导出/备份」能力（见 8.15 与 zip 导出） | dropped |
| 8.14 | WebDAV 4 域合并策略（按 updatedAt 取新，删除墓碑不复活） | `mergeCanvasData` | 服务端权威 + op 日志（冲突按可 rebase / 需裁决两档） | 合并语义以 op 路径 + 版本号实现；墓碑不复活由画布软删 + 版本推进保证 | done |
| 8.15 | 平台化同步（原项目 WebDAV 的替代/补充） | — | 服务端权威 + 多端增量同步 | done |
| 8.16 | 版本更新提示（读 CHANGELOG 弹窗） | `use-version-check.ts` + `version-release-modal.tsx` | `meta` 接口的 build.version/commit | 设置页可见版本与 commit；不做弹窗打扰（部署方自升级，用户无需操作） | done |
| 8.17 | 分析统计（GA4 / 百度，容器注入 `config.js`，默认关闭） | `analytics-tracker.tsx` + `docker-entrypoint.sh` | 默认不加载任何脚本 | done |

## 9. Agent 与 MCP

| # | 功能 | 原实现 | 重写落位 | 验收 | 状态 |
| --- | --- | --- | --- | --- | --- |
| 9.1 | 本机 Agent 服务（127.0.0.1:17371，token，Origin 白名单，配置 0700/0600） | `canvas-agent/*` | 精简为桥接器 | 安全项逐条保持 | done |
| 9.2 | Codex app-server JSON-RPC 桥（thread/turn/item、审批、reasoning、plan、usage） | `codex-client.ts` 900 行 | `canvas-agent/src/normalize.js` | 事件归一化到 Item，未知类型保留而非丢弃 | done |
| 9.3 | Claude Code CLI 桥（stream-json） | `agent/claude.ts` | `canvas-agent/src/normalize.js` | 四类 content block 归一化，多 block 事件拆多 Item | done |
| 9.4 | 会话/消息模型（threadId + turnId + itemId 三元归属，快照权威） | `message-metadata.ts` + `codex-history.ts` | `agent_items(turn_id,item_id)` 唯一键 | 断线重连不重不丢 | done |
| 9.5 | 28 个画布工具 + 6 个站点/工作台/素材/提示词工具，共 34 个 | `canvas/schemas.ts` `toolNames` | `internal/agent/tools_upstream.go`（34 个名字）+ `translate.go`（纯函数翻译）+ `dispatch_upstream.go` | 34 个名字**逐字一致**（`check-upstream-radar` 对上游真源校验）；全部落到 `AppendOps`/`RunTrigger` 同一批底层能力；无对应能力的显式返回 not_implemented | done |
| 9.6 | 工具调用转发到网页执行（SSE `tool_call` + POST `/canvas/result`，30s 超时） | `session.ts requestCanvasTool` | 服务端网关 + 浏览器执行器 | Agent 能改画布 | done |
| 9.7 | 附件 → 画布图片节点（`canvas_create_attachment_nodes`） | `createAttachmentNodes` | 同名工具 + `graph.PlaceNewNodes` | 附件落为真实节点并避让已有区域 | done |
| 9.8 | 画布快照压缩（content 截断 240 字符） | `compactNode` | `agent.Service.Snapshot` | 上下文可控 | done |
| 9.9 | op 集合（add/update/delete node、connect、set_viewport、select、run_generation） | `canvas-agent-ops.ts` | 与服务端 op 同源 | 完全一致 | done |
| 9.10 | Agent 操作撤销 | `undoAgentOps` 快照 | `ToolCallResult.Inverse` | 一键撤销 | done |
| 9.11 | 侧边栏（会话列表/流式消息/思考折叠/工具卡片/审批/权限模式/日志/Skills/诊断） | 13 个组件 | `features/agent/*` | 交互对等 | done |
| 9.12 | 三个权限模式（request / automatic / full） | `AgentPermissionMode` | 同 | 语义一致 | done |
| 9.13 | 工具确认模式（手动/自动） | Agent composer `tools` | 同 | 会话级过期 | done |
| 9.14 | Skills 管理（列出/启用/草稿生成） | `skills/store.ts` + `agent-skills-view.tsx` | `migrations` + `agent/skills.go` + `features/agent/SkillsPanel` | 服务端存储（团队共享）、有长度与数量上限 | done |
| 9.15 | Codex app 插件（marketplace + MCP 注册） | `plugins/infinite-canvas` | 本机桥接器 + 服务端 MCP | 桥接器接 Codex CLI，MCP 端点可被任意客户端连接 | done |
| 9.16 | 多标签页隔离（clientId + sourceClientId） | `session.ts` | `shared/client/client-id.ts` | 每标签独立标识；复制标签页会检测重名并重置 | done |
| 9.17 | 服务端 MCP Streamable HTTP | — | 新增 | 任意 MCP 客户端可连 | done |

## 10. 插件体系

| # | 功能 | 原实现 | 验收 | 状态 |
| --- | --- | --- | --- | --- |
| 10.1 | 插件 URL 安装/启用/更新/卸载 | `plugin-loader.ts` | 安装记录 + 版本 | done |
| 10.2 | 官方注册表（jsDelivr 托管，本地 `public/plugins` 扫描，`VITE_DEV_PLUGINS`） | `plugin-registry.ts` + vite 插件 | 三来源合并展示 | done |
| 10.3 | 节点注册表（type → 定义，owner 插件，版本号 bump 触发 UI 刷新） | `node-registry.ts` | 等价能力 | done |
| 10.4 | 插件运行时（React 单例注入、`injectCSS`、`setup` 清理） | `plugin-runtime.ts` | 改为 iframe 沙箱 | done |
| 10.5 | 节点定义契约（defaultSize/metadata/minimapColor/hasSourceHandle/hidePanel/transparentBackground/autoOpenPanel/useBuiltinPanel/interactionToggle/forceInteractive/keepAspectRatio/resource/Content/Panel/toolbar/onDoubleClick） | `types/canvas-plugin.ts` | 逐字段对等或显式替代 | done |
| 10.6 | 宿主能力（getNode(s)/getConnections/getUpstream/getDownstream/updateNode/updateMetadata/applyOps/ai.generate*/openPanel/closePanel/storage） | `CanvasPluginHost` / `CanvasNodeContext` | 权限化后对等 | done |
| 10.7 | AI 能力注入（generateImage/Video/Text、listModels、defaultModel） | `CanvasPluginAi` | 走服务端 exec | done |
| 10.8 | 6 个官方插件重写为示例（markdown / svg / html / panorama / sticky-note / template） | `plugins/canvas/*` | 逐个功能对等 | done |
| 10.9 | SDK（definePlugin / JSX runtime / 类型化 hooks / buildPlugin） | `@infinite-canvas/plugin-sdk` | `packages/plugin-sdk`（definePlugin / jsx-runtime / `ic-plugin-build` / 模板插件） | 清单校验（key/semver/apiVersion/节点前缀/权限/网络主机）；JSX runtime 只用 textContent + 属性白名单；构建产出单文件 bundle + sha256 integrity | done |
| 10.10 | 插件权限与签名 | 无（安全缺陷） | manifest 权限 + 安装确认 + 扩大权限重确认 | done |
| 10.11 | 缺插件节点的降级展示 | `node.missingPlugin`（有文案无实现路径） | 展示「需要插件 X」+ 安装入口 | done |

> 10.8 的两个已知坑必须在重写中保留修复：
> - markdown 插件按源码模块级缓存解析结果，且仅 HTML 变化时写 DOM（否则画布重渲染会让图片重新请求）；
> - panorama 需要 GPU 上限降采样，否则大图加载失败。

## 11. 跨领域能力

| # | 能力 | 原实现 | 验收 | 状态 |
| --- | --- | --- | --- | --- |
| 11.1 | i18n 全量（zh-CN / en-US） | 各 657 行，含 canvas/agent/config/imageWorkbench/videoWorkbench/assets/prompts/home 等命名空间 | 无硬编码文案，无缺 key | done |
| 11.2 | 主题（画布主题与 antd token 统一，禁止组件内写死黑白） | `canvas-theme.ts` + `app-theme.ts` | 亮/暗一致 | done |
| 11.3 | 导出画布 zip（projects.json + files/，version 3 格式） | `canvas-export.ts` | `features/canvas/transfer.ts` + 项目卡片按钮 | 导出→导入→再导出清单可比（清单不含随机字段）；资产先上传再建画布；缺失/跳过显式上报；兼容读取原项目包 | done |
| 11.4 | 导出选中节点 zip（媒体原文件 + 文本 txt + 其他 json） | `exportCanvasNodes` | 可用 | done |
| 11.5 | 复制文本并提示的统一 hook | `use-copy-text.ts` | 统一复用 | done |
| 11.6 | 文件大小/时长格式化 | `formatBytes` / `formatDuration` | 复用 | done |
| 11.7 | 引用编号标签（图片N / 视频N / 音频N / 文本N / 组N） | `imageReferenceLabel` / `generationLabel` | 全站一致 | done |
| 11.8 | 响应式（移动端抽屉、栅格断点） | 多处 `lg:` 断点 | 移动端可用 | done |

## 12. 明确不复刻（需用户知情）

| 项 | 原实现 | 理由 |
| --- | --- | --- |
| 浏览器直连第三方 API | `web/src/services/api/*` | 架构取舍：凭据集中、可限额、可观测（见 02-domain-model §2.5） |
| `new Function` 执行用户脚本 | `model-plugin.ts` | 等同任意代码执行，改服务端沙箱 |
| `Blob URL + import()` 插件 | `plugin-loader.ts` | 同源执行等价 XSS，改 iframe 沙箱 |
| `canvas-proxy` 依赖 | `canvas-proxy/` | 服务端同源转发后不再需要 |
| IndexedDB 作为唯一真源 | 全部 store | 改服务端权威 + 本地缓存 |
| 赞助商 banner / 外链社群入口 | `README.md` | 与原作者站点相关，重写不搬运；保留原作者署名与 MIT 许可（详见 12-legacy-and-upstream.md） |

**注意**：最后一项涉及第三方内容，落地前需与用户确认取舍，不要默认照搬。

## 13. 缺口核对方法（防止「以为复刻完了」）

1. **逆向清单**：用 `rg -o "i18n.t\(\"[a-zA-Z0-9._]+\"" web/src` 提取全部文案 key，
   逐 key 标注「有落位 / 不复刻」，输出 `docs/design/parity/i18n-keys.json`。
2. **交互清单**：对 `project.tsx` 的 3384 行逐段（已按第 3 节拆为 28 项）打勾；
   3.29 / 3.30 是本仓在对照之外主动补的能力（原项目没有），不计入「复刻」分母。
3. **契约清单**：对 `types/canvas.ts` / `types/canvas-plugin.ts` 的每个类型字段按 §2.13 映射。
4. **CI 门禁**：`make parity` 运行上述三个脚本，未打勾项数下降才算进度。

> 这份矩阵是**唯一的功能真源**。原仓库更新时先更新矩阵（用 `12-legacy-and-upstream.md` 的流程），
> 再决定是否同步到重写实现。

## 14. 当前未完成项（诚实清单）

覆盖率报告由 `make parity` 生成。以下条目**尚未实现或仍有简化**，逐条给出原因与计划，
避免「看起来覆盖率很高」的自欺。

> 本节的写法要求：**每条都要能在代码里被证实**。上一版本曾把已经落地的条目留在
> 「未完成」列表里，也曾在 §9.5 标 `done` 而实际只有 14/34 个工具名 —— 那份清单
> 既不能指导排期，也掩盖了真正未做的项。核对方式见 §13。

### 14.1 仍未实现（`todo`）

| 条目 | 未实现的原因 | 补齐计划 |
| --- | --- | --- |
| — | 当前无 `todo` 项 | — |

### 14.2 已实现但有简化（`wip`）

| 条目 | 简化点 | 补齐计划 |
| --- | --- | --- |
| — | 当前无 `wip` 项 | — |

### 14.3 明确不做（`dropped`）

见 §12。这些不是「还没做」，而是设计上不做，理由已写：

- 4.20 本地代理开关（服务端同源转发后不再需要）
- 8.11 本地代理页（同上）
- 8.13 WebDAV 同步（DIV-06 改用服务端权威同步；导出/备份能力保留）

### 14.4 与里程碑门槛的对照

参考 `docs/design/09-roadmap.md` 门槛表与 `docs/design/13` §2.1：

| 里程碑 | 门槛 | 当前 |
| --- | --- | --- |
| M5 末端 | 85% | ✅ 已过 |
| M6 末端 | 95% | ✅ 已过 |
| M7 发版 | 100%（剩余显式 dropped） | ✅ 已过（`make parity-enforce` 可验证） |

**结论：矩阵口径下已无未完成项。** 各章节余量见 §14.5 的「仍属简化的地方」——
那一节列的**不是**矩阵条目，而是门禁能力本身的边界，因此不进覆盖率分母。

### 14.5 仍属简化的地方（不进覆盖率分母，但不假装不存在）

| 项 | 现状 | 为什么没进矩阵 |
| --- | --- | --- |
| `site_navigate` / `canvas_select_nodes` / `workbench_*` 的 `run=false` | 服务端返回显式 `not_implemented` 并说明「由前端执行」 | 上游语义是「操作当前打开的页面」，服务端形态下没有这个对象。做成服务端能力会引入开放重定向与跨端选中覆盖（真实的多端 bug），所以是**刻意不做**而不是遗漏 |
| 沙箱语义版本 | `sandbox/1`，无历史脚本迁移 | 脚本只描述请求，语义变化的影响面是「某个渠道的请求体变了」，可由渠道配置回滚；不值得为它引入脚本版本表 |
| 上游巡检频率 | 周级（cron）+ 手动触发 | 日级巡检在契约面稳定期只会产生噪音报告，而噪音报告等于没有报告（上一轮已踩过：恒报安全告警） |
| 自定义脚本的流式 | 不支持（`ErrStreamUnsupported`） | 流式需要脚本声明「如何从 SSE 分片里取增量」，那是另一套协议；当前脚本只覆盖「一次性请求/响应」 |
