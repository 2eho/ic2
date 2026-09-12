// 模板插件：把所有插件都需要处理的四件事写出来。
import { definePlugin, host, onInit, requestResize } from "@ic/plugin-sdk";

export default definePlugin({
  manifest: {
    key: "com.example.hello",
    name: "Hello 插件（模板）",
    version: "1.0.0",
    apiVersion: "1.0.0",
    entry: "index.js",
    permissions: ["node.read", "node.write"],
    nodes: [
      {
        type: "com.example.hello:greeting",
        title: "问候",
        ports: { inputs: [], outputs: [] },
      },
    ],
  },
  render() {
    // 渲染由 onInit 接管（SDK 的 render 只是规范入口）
  },
});

onInit(async (node) => {
  const api = host();
  let cfg = node.config ?? {};

  const root = document.createElement("div");
  root.style.cssText = "padding:8px;font:13px system-ui";
  const input = document.createElement("input");
  input.value = String(cfg.text ?? "你好");
  input.style.cssText = "width:100%;margin-bottom:6px";
  const save = document.createElement("button");
  save.textContent = "保存";

  save.addEventListener("click", async () => {
    // 写入走宿主 op 路径：因此有校验、有撤销、有审计。
    // 直接改 localDOM 是常见的错误做法 —— 刷新后内容就没了。
    cfg = { ...cfg, text: input.value };
    await api.patch(cfg);
    api.toast("已保存");
  });

  root.append(input, save);
  document.body.replaceChildren(root);

  // 内容尺寸变化后必须主动上报，否则宿主不知道 iframe 该多高，
  // 表现为「内容被裁掉一半」——这是插件里最常见的体验问题。
  requestResize(260, 140);
});
