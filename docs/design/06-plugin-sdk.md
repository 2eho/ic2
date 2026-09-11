# 06 · 插件系统设计

## 1. 原方案的问题

| 现状 | 问题 |
| --- | --- |
| 通过 URL 拉取 JS 文本，`new Blob` + `import()` 执行 | 任意代码在主页面同源执行，等同于 XSS；无法做权限边界 |
| 插件与宿主共享 React 实例（`runtime.React`） | 插件崩溃/内存泄漏会污染主页面 |
| SDK 类型是宿主类型的「镜像文件」 | 双份真源，宿主改了插件类型不一定跟随 |
| 无签名、无版本约束、无权限声明 | 无法审核，也无法安全地做官方插件分发 |
| 插件配置直接写进节点 `metadata` 扁平袋 | 无 schema，无迁移机制 |

## 2. 目标

1. 插件**无法**访问宿主 DOM、Cookie、其他插件数据。
2. 插件能力**显式声明**，用户安装时可见，运行时强制。
3. 插件可独立构建、独立版本、独立升级，且类型与宿主契约同源。
4. 插件节点可序列化（画布导出后仍可被其他实例加载）。
5. 官方插件有审核与签名，第三方插件默认需用户显式信任。

## 3. 插件包格式

```
my-plugin/
  plugin.json          # 清单
  dist/index.js        # ESM bundle（浏览器可加载）
  dist/style.css       # 可选
  dist/*.wasm          # 可选
```

```json
{
  "key": "com.example.panorama",
  "name": "3D 全景",
  "version": "1.2.0",
  "apiVersion": "^1.0.0",
  "author": "…",
  "homepage": "…",
  "entry": "dist/index.js",
  "style": "dist/style.css",
  "permissions": ["node.read", "node.write", "asset.read", "ai.generate:image", "storage"],
  "nodes": [
    {
      "type": "com.example.panorama:viewer",
      "title": "全景查看器",
      "defaultSize": { "width": 640, "height": 360 },
      "ports": { "inputs": [{ "id": "in", "kind": "image" }], "outputs": [] },
      "configSchema": { "type": "object", "properties": { "fov": { "type": "number", "default": 75 } } },
      "minSize": { "width": 320, "height": 200 }
    }
  ],
  "integrity": { "sha256": "…" },
  "signature": "…" 
}
```

- `configSchema` 是 JSON Schema：**画布导出时携带 configSchema 快照**，
  使插件未安装时也能显示「此节点需要插件 X」，而不是渲染成空白。
- `integrity` 用于校验分发内容；官方仓库额外带 `signature`（ed25519）。

## 4. 运行时架构

```
              ┌─────────────── 主页面（宿主） ────────────────┐
              │  PluginRuntime                                │
              │   ├─ 注册表（节点类型 → 插件）                  │
              │   ├─ 权限校验器                                 │
              │   ├─ 能力路由器（AI / 资产 / 存储 / 图查询）      │
              │   └─ 生命周期（load / enable / disable / 卸载）   │
              └───────────────┬───────────────────────────────┘
                              │ postMessage（结构化 + 校验）
              ┌───────────────▼───────────────┐
              │  <iframe sandbox="allow-scripts">  │
              │  插件 bundle（自带 React / 或纯 DOM）│
              └────────────────────────────────┘
```

要点：

- `sandbox="allow-scripts"` 且**不加** `allow-same-origin` → 插件处于独立 null origin，
  无法读取宿主 Cookie / localStorage / DOM。
- 通信只走 `postMessage`，宿主侧对每条消息做 schema 校验（`zod`），拒绝未知方法。
- 资源加载：插件 bundle 由服务端代理分发（`/api/v1/plugins/{key}/{version}/index.js`），
  携带 `ETag`，可缓存；同时让鉴权与审计成立。

## 5. 插件 API（宿主能力）

按权限分组，未声明权限的方法调用直接拒绝。

```ts
// 只读图查询（权限：node.read）
interface GraphAPI {
  getNode(id: string): NodeSnapshot | null;
  getUpstream(portId: string): NodeSnapshot[];
  getDownstream(): NodeSnapshot[];
}

// 写（权限：node.write）——只能写自己这个节点，或自己创建的节点
interface NodeAPI {
  patch(config: unknown, schemaVersion: number): void;   // 按 configSchema 校验
  resize(w: number, h: number): void;
  emit(outputPortId: string, resource: Resource): void;
  toast(msg: string): void;
}

// 资产（权限：asset.read）
interface AssetAPI {
  getURL(assetId: string): Promise<string>;   // 宿主签发短时 URL
  upload(blob: Blob, name: string): Promise<AssetRef>;
}

// AI（权限：ai.generate:<capability>）
interface AIAPI {
  generate(req: { capability: string; prompt: string; refs?: AssetRef[]; params?: object }): Promise<{ assets: AssetRef[] }>;
}

// 存储（权限：storage）——按 pluginKey 命名空间隔离，有配额
interface StorageAPI {
  get<T>(key: string): Promise<T | null>;
  set(key: string, value: unknown): Promise<void>;
  remove(key: string): Promise<void>;
}
```

明确**不提供**：直接网络请求、文件系统、宿主 DOM、其他插件数据、凭据读取。
需要联网的插件（如原项目 Markdown 插件从 CDN 加载 marked）改为：
在 manifest 里声明 `network: ["cdn.jsdelivr.net"]`，走宿主代理并记录审计。

## 6. 与画布数据的交互

- 插件的 `config` **必须**是 JSON 可序列化且通过 `configSchema` 校验的数据。
  （原项目允许插件往 `metadata` 塞任意字段，导出后无法校验。）
- 插件声明 `configVersion`，升级时宿主执行 `migrate(old, new)` 函数（在 iframe 内执行）。
- 插件节点输出的资源类型由 `ports.outputs[].kind` 声明，参与编译器的类型校验。

## 7. 分发与治理

| 渠道 | 说明 |
| --- | --- |
| 官方仓库 | 服务端维护 registry（原项目是 GitHub 分支 + jsDelivr），带签名与审核 |
| 自建/第三方 | 用户填 manifest URL，宿主拉取后展示权限清单，需二次确认 |
| 私有部署 | 支持从内网 registry 拉取，支持 `IC_PLUGIN_REGISTRY` 环境变量 |

安全措施：

- 安装时展示：作者、版本、权限列表、内容 hash。权限含 `ai.generate` 时必须高亮提示。
- 更新时若权限扩大，必须重新确认。
- 插件事件与 AI 调用写审计日志（哪个插件调了什么）。
- 提供「禁用所有插件」的应急开关。

## 8. SDK 与类型同源

原项目 SDK 的类型是手工维护的镜像，容易漂移。重写后：

1. 契约定义在一处（`contracts/plugin-api.ts`），由 JSON Schema / TS 类型共同生成。
2. 生成 Go 侧校验代码（用于服务端校验 manifest）与 TS 侧 SDK 类型。
3. SDK 提供 `definePlugin` + `createNodeRenderer`，并把构建脚本（esbuild）作为依赖，
   插件作者只写业务代码（与原项目 `buildPlugin(import.meta.url)` 的体验保持一致）。
4. SDK 版本与 `apiVersion` 解耦：SDK 可以频繁发版，`apiVersion` 只在破坏性变更时提升。

## 9. 迁移路径

原项目插件（Markdown / SVG / HTML / 全景 / 便利贴）在本设计下重写为官方插件，
作为 SDK 的示例与回归用例。旧插件（Blob URL 形式）**不支持**直接运行，
但可以在导入器中识别并提示「该节点需要新版插件 X」。
