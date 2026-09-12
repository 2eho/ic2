# @ic/plugin-sdk

IC 画布插件 SDK（对应 `docs/design/10-parity-matrix.md` §10.9）。

插件运行在 `sandbox="allow-scripts"`（**无** `allow-same-origin`）的 iframe 里，
是 null origin 的不可信代码。SDK 的存在是把这个边界表达清楚，而不是隐藏它。

## 最小插件

```json
// plugin.json
{
  "key": "com.example.markdown",
  "name": "Markdown 预览",
  "version": "1.0.0",
  "apiVersion": "1.0.0",
  "entry": "index.js",
  "permissions": ["node.read", "node.write"],
  "nodes": [
    { "type": "com.example.markdown:preview", "title": "Markdown", "ports": { "inputs": [], "outputs": [] } }
  ]
}
```

```js
import { definePlugin, onInit, host } from "@ic/plugin-sdk";

export default definePlugin({
  manifest: /* 与 plugin.json 同源，可由构建期注入 */ manifest,
  render(node, api) {
    api.toast("已就绪");
  },
});

onInit(async (node) => {
  const api = host();
  const cfg = node.config ?? {};
  document.body.textContent = String(cfg.text ?? "（空）");
});
```

## 构建

```
npx ic-plugin-build .        # 产出 dist/index.js 与 dist/plugin.json（含 sha256）
```

## 边界（很重要）

- **脚本看不到任何宿主对象**。`window.parent` / `document.cookie` 都不可达；
  唯一通道是 `window.icHost`，由宿主注入。
- **所有能力都要声明权限**，宿主逐次校验；未声明的调用被拒绝并写审计日志。
- **不要用 `innerHTML`**：插件本身就是不可信代码，SDK 的 `renderToDOM`
  只用 `textContent` 与属性白名单。
- **渲染失败不能拖垮宿主**：宿主只停用该节点并提示，画布继续可用。
