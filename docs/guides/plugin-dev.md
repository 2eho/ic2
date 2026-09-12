# 插件开发指南

## 最小插件

```
my-plugin/
  plugin.json
  index.js
```

```json
{
  "key": "com.example.hello",
  "name": "Hello 插件",
  "version": "1.0.0",
  "apiVersion": "^1.0.0",
  "entry": "index.js",
  "permissions": ["node.read", "node.write"],
  "nodes": [
    {
      "type": "com.example.hello:greeting",
      "title": "问候",
      "defaultSize": { "width": 320, "height": 200 },
      "minSize": { "width": 160, "height": 120 },
      "configSchema": {
        "type": "object",
        "properties": { "text": { "type": "string" } }
      },
      "ports": {
        "inputs": [{ "id": "in", "name": "输入", "kind": "text" }],
        "outputs": [{ "id": "out", "name": "输出", "kind": "text" }]
      }
    }
  ]
}
```

```js
// index.js —— 运行在 sandbox iframe 中
icHost.onInit(function (node, theme) {
  document.body.innerHTML =
    '<div style="padding:12px;font-family:system-ui">' +
    '<strong>' + node.title + '</strong>' +
    '<p>' + (node.config.text || '点击编辑') + '</p>' +
    '</div>';
});

// 能力调用（必须在 manifest 里声明权限）
function saveText(text) {
  return icHost.call('node.patch', { config: { text: text } });
}
```

## 安装

在「配置中心 → 插件」填入 manifest URL，宿主会：

1. 拉取并**严格校验**清单（key/版本/入口路径/节点命名空间/端口类型/schema 结构）；
2. 展示权限清单（含 `ai.generate` 会高亮，因为它产生真实费用）；
3. 你确认后才写入安装记录。

**更新时若权限扩大，必须重新确认**（服务端返回 409 并列出新增权限）。

## 安全边界（不可绕过）

| 约束 | 实现 |
| --- | --- |
| 无法访问宿主 DOM/Cookie/localStorage | `sandbox="allow-scripts"`，**不加** `allow-same-origin` |
| 无法调用未声明能力 | 宿主逐条校验方法 → 权限映射，未声明即拒绝并记审计 |
| 无法发起未声明网络请求 | 默认 CSP `connect-src 'none'`；声明 `network` 后按 allowlist 放开 |
| 崩溃不影响主页面 | iframe 隔离；宿主只停用该节点 |
| 无法访问文件系统/进程 | 能力表里根本没有这些方法 |

## 能力与权限对照

| 方法 | 所需权限 |
| --- | --- |
| `node.get` / `graph.upstream` / `graph.downstream` | `node.read` |
| `node.patch` / `node.resize` / `node.emit` | `node.write` |
| `graph.query` | `graph.query` |
| `asset.getUrl` | `asset.read` |
| `asset.upload` | `asset.write` |
| `ai.generate` | `ai.generate` 或 `ai.generate:<capability>` |
| `storage.get/set/remove` | `storage`（有配额） |
| `host.toast` | 无需权限 |

## 已知的两个坑（重写中保留修复）

1. **Markdown**：解析结果要按源码缓存，且只在 HTML 变化时写 DOM；
   否则画布重渲染会让图片重新请求。
2. **全景**：大图需要按 GPU 上限降采样，否则加载失败。
