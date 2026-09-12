# 04 · React 前端设计

## 1. 目标与约束

| 约束 | 说明 |
| --- | --- |
| 视口操作零 React 重渲染 | 平移/缩放/框选不能触发节点树 re-render |
| 万级元素可用 | 视口裁剪 + 分层 + 惰性挂载 |
| 插件节点可共存 | 第三方节点渲染不污染主包，不阻塞主线程 |
| 服务端为权威 | 本地只做乐观更新，永不成为「唯一真源」 |
| 无密钥 | 前端不保存任何模型凭据 |

## 2. 画布内核（framework-agnostic）

`src/features/canvas/kernel/` 是纯 TS 模块，**不 import react**。

```ts
type Kernel = {
  viewport: ViewportController;   // 世界坐标 ↔ 屏幕坐标
  scene: SceneGraph;              // 节点/边/索引
  hit: HitTester;                 // 点/框命中测试
  render: Renderer;               // 分层渲染调度
  interact: InteractionMachine;   // 手势状态机
  commands: CommandBus;           // 命令 → op
  undo: UndoStack;
};
```

### 2.1 渲染分层

```
<canvas-host>  position: relative; overflow: hidden
  ├─ layer-grid      : CSS background（点阵/网格）+ 单一 transform
  ├─ layer-edges     : 一个 <svg>，统一 transform，path 复用
  ├─ layer-nodes     : 节点容器，每个节点一个绝对定位 div（transform: translate3d）
  ├─ layer-overlay   : 选中框、连线预览、对齐辅助线（SVG）
  ├─ layer-widgets   : 小地图、缩放控件、工具栏（不随视口变换）
  └─ layer-plugins   : 插件节点宿主（iframe，仅在可见+可交互时挂载）
```

- 视口变更只写**一个** CSS 变量 / transform 到一个 wrapper，浏览器在合成层完成平移缩放，React 不参与。
- 节点使用 `transform: translate3d(x,y,0)` + 固定 `width/height`，避免 layout。
- 视口裁剪：`SceneGraph` 维护 R-tree（或简单网格分桶），只把可见节点交给 React 渲染。
- 缩放很大或很小时降级：低于阈值只渲染节点色块占位，高于阈值才挂载内容组件。

### 2.2 交互状态机

用显式状态机替代原项目散落在 `project.tsx` 里的大量 `if (dragging && ...)` 分支。

```
states: idle | panning | marquee | dragging-node | resizing-node
      | connecting | snapping | editing-text | plugin-interacting

events: pointerdown/move/up, key, wheel, dblclick, drop, blur

guards: 命中目标类型、修饰键、节点锁定状态、插件是否交互态
```

每个转移返回一个 `Command` 列表（如 `MoveNodes(ids, delta)`），由 `CommandBus` 统一：
应用 → 推 undo → 批量提交服务端。

### 2.3 坐标与几何

- 世界坐标统一 `float64`，屏幕坐标用 `devicePixelRatio` 校正。
- 吸附：网格吸附、节点边缘对齐、等间距提示，全部在内核计算（不依赖 DOM 测量）。
- 原项目 `lib/canvas/canvas-node-geometry.ts` 的逻辑迁入 `kernel/geometry.ts` 并补测试。

## 3. 状态分层

| 层 | 工具 | 内容 | 生命周期 |
| --- | --- | --- | --- |
| 服务端状态 | TanStack Query | 项目列表、画布详情、资产、运行、提示词 | 随缓存策略 |
| 画布文档状态 | Zustand slice + 内核订阅 | 节点/边/版本，与内核双向同步 | 打开画布期间 |
| 交互/UI 状态 | Zustand slice | 选区、面板开合、悬浮工具栏、上下文菜单 | 会话级 |
| 本地偏好 | `localStorage`（仅小配置）+ 服务端同步 | 主题、语言、面板宽度 | 持久 |

**明确禁止**：把画布文档放进 React `useState` 或 Context。
（原项目 3384 行的 `project.tsx` 正是这样长出来的。）

### 3.1 写入路径

```ts
// 统一入口
dispatch(command: Command): void {
  const ops = toOps(command);           // 命令 → 领域 op
  kernel.applyLocal(ops);               // 乐观应用，立即反馈
  undo.push(command);
  queue.push(ops);                      // rAF 合并
  flush();                              // POST /canvases/{id}/ops
}
```

失败回滚：`flush` 返回 409 时，用返回的权威文档做一次 re-sync，并把冲突提示给用户。

### 3.2 读取路径（事件驱动）

SSE 客户端把事件分派到对应 slice：

| 事件 | 处理 |
| --- | --- |
| `canvas.op` | 若是本端提交的回声 → 忽略（按 actorId + 临时 id 匹配）；否则应用到内核 |
| `run.step` | 更新对应节点的 `State`，驱动节点上的进度/错误 UI |
| `run.step.delta` | 追加到流式文本节点，用 `requestAnimationFrame` 合并 |
| `asset.created` | 预取缩略图，写入 Query 缓存 |
| `presence` | 更新协作光标（低频，可丢） |

## 4. 功能模块落位

| 原项目位置 | 重写后位置 | 说明 |
| --- | --- | --- |
| `pages/canvas/project.tsx`（3384 行） | `features/canvas/{CanvasPage,NodesLayer,EdgesLayer,Toolbar,Panels,...}` | 按职责拆成 20+ 个模块 |
| `components/canvas/canvas-node.tsx`（960 行） | `features/canvas/components/NodeShell.tsx` + 每种类型一个 `renderers/*` | 节点外壳与内容分离 |
| `lib/canvas/*` | `features/canvas/kernel/*` | 去 React 化 + 补单测 |
| `stores/canvas/*` | `features/canvas/store/*` | slice 化 |
| `services/api/*`（image 921 行 / video 428 行） | **删除**，改为 `shared/api/generated/*` | 生成逻辑下沉到后端 |
| `stores/use-config-store.ts`（496 行） | `features/settings/*` + 服务端 Provider | 凭据不再在前端 |
| `components/agent/*` | `features/agent/*` | 会话走服务端 Agent 网关 |
| `pages/{image,video}` | 保留为「快速生成」入口，走后端统一 API | 与画布共用执行引擎 |

## 5. 关键组件设计

### 5.1 NodeShell

```
NodeShell
  ├─ 顶栏：图标 + 标题（可编辑）+ 状态徽标 + 悬浮工具条
  ├─ 内容：由 NodeRendererRegistry 根据 node.type 决定
  └─ 端口锚点：根据 node.Ports 渲染，位置由内核计算
```

- 状态徽标订阅 `node.State`（idle/running/succeeded/failed），不订阅整个文档。
- 使用 `useSyncExternalStore` 订阅内核的细粒度 selector，避免全树重渲染。
- 悬浮工具条由节点类型 + 能力声明（`nodeCapabilities(type)`）生成，不再是巨型 switch。

### 5.2 GenerationPanel

统一原项目的 `canvas-config-composer`、`canvas-node-prompt-panel`、
`canvas-image-settings-popover`、`canvas-video-settings-popover`、`canvas-audio-settings-popover`：

```
GenerationPanel
  ├─ InputBar       提示词 + @ 引用（引用列表由上游连线推导，不是手工维护）
  ├─ ModelPicker    按 capability 过滤，来自 GET /models，带健康度
  ├─ ParamForm      由 Provider 的 params schema 驱动（JSON Schema → 表单）
  ├─ ReferenceBar   上游资源预览、排序、移除
  └─ RunActions     生成 / 批量 / 取消 / 重放
```

参数表单**由 schema 生成**，新增模型参数不需要改前端代码，避免原项目每种能力写一套弹层的重复。

### 5.3 RunPanel（新增）

原项目没有的能力，重写后的核心差异点：

- 运行列表（按画布）：状态、耗时、成本、触发方式。
- 步骤时间线：每个节点的开始/结束/重试/错误详情。
- 差异对比：同一节点的多次运行结果并排比较，一键「以这次为准」更新节点。
- 重放：用相同参数与输入重跑，用于复现问题或换模型重试。

### 5.4 引用系统（`@` mention）

原项目用 `canvas-resource-mention-textarea.tsx` + `canvas-prompt-chip-input.tsx` 两个大组件实现，
并在发送时按顺序编号「图片1/文本1」，历史上出现过「多段文本靠空行分隔无法对应」的 bug（见 CHANGELOG）。

重写方案：

- 引用是**结构化 token**，不是文本约定：`{ type: "ref", nodeId, portId, resourceKind }`。
- 存储为 `ContentBlock[]`，渲染为 chip，序列化给后端时**同时**给出结构与渲染文本。
- 后端在编译 DAG 时按端口读取真实资源，不依赖编号文本，从根本上消除错位问题。

### 5.5 资产与提示词

- 资产库：虚拟滚动网格、标签过滤、批量操作、拖入画布。
- 提示词库：服务端搜索（不再浏览器直连 7 个仓库），支持变量填充后插入画布。

## 6. 插件节点宿主

每个插件节点渲染在一个 `<iframe sandbox="allow-scripts">` 中（无 `allow-same-origin`，保证无法访问父页面），
通过 `postMessage` 与宿主通信；宿主实现能力代理：

```ts
// 宿主 → 插件
{ type: "plugin:init", node, theme, size, scale }
{ type: "plugin:update", patch }
{ type: "plugin:theme", theme }
// 插件 → 宿主（能力调用，需在 manifest 权限内）
{ type: "host:call", id, method: "node.patch" | "graph.query" | "ai.generate" | "storage.get", params }
{ type: "host:event", name, payload }
```

性能：iframe 只在节点可见且非纯展示时挂载；滚动出视口即卸载。
相比原项目「Blob URL + dynamic import」的方案，隔离更强、崩溃不会拖垮主页面。

详见 `06-plugin-sdk.md`。

## 7. 前端工程规范

- 目录即边界：`features/*` 之间不互相 import 内部文件，只通过 `features/*/index.ts` 暴露。
- 禁止跨 feature 直接改 store；跨 feature 协作走事件或 Query 失效。
- 组件文件 > 300 行视为重构信号；`project.tsx` 那种规模在 review 阶段直接拒绝。
- 所有内核模块必须有单测（`vitest`），画布关键交互有 e2e（`playwright`）。
- 文案统一 i18n key，按后端错误 `code` 映射，不再手写中文错误串。
