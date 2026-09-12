import { test } from "node:test";
import assert from "node:assert/strict";
import { definePlugin, validateManifest, host, HOST_METHODS, PERMISSIONS, API_VERSION } from "./index.js";
import { renderToDOM, jsx } from "./jsx-runtime.js";

// renderToDOM 需要真实 DOM。这里用 jsdom 而不是自己写一个替身：
// 「注入的 <script> 有没有被执行」这类断言只有在真实 DOM 上才成立。
import { createRequire } from "node:module";
const require = createRequire(import.meta.url);
const { JSDOM } = require("/workspace/web/node_modules/jsdom");
const dom = new JSDOM("<!doctype html><html><body></body></html>");
globalThis.document = dom.window.document;
globalThis.Document = dom.window.Document;
globalThis.Event = dom.window.Event;

const baseManifest = () => ({
  key: "com.example.t",
  name: "T",
  version: "1.0.0",
  apiVersion: "1.0.0",
  entry: "index.js",
  permissions: ["node.read"],
  nodes: [{ type: "com.example.t:main", title: "Main", ports: { inputs: [], outputs: [] } }],
});

test("definePlugin 校验清单并在合法时返回 spec", () => {
  const spec = definePlugin({ manifest: baseManifest(), render: () => {} });
  assert.equal(spec.manifest.key, "com.example.t");
});

// 下面每一条都对应一次真实会发生的失败。这些必须在**开发期**报出来：
// 同样的错误在宿主里表现为「运行时被拒绝」，而那时作者已经走了很远。
test("拒绝 key 格式错误", () => {
  const m = { ...baseManifest(), key: "hello" };
  assert.throws(() => validateManifest(m), /key/);
});

test("拒绝非 semver 版本", () => {
  assert.throws(() => validateManifest({ ...baseManifest(), version: "v1" }), /semver/);
});

test("拒绝 apiVersion 主版本不匹配", () => {
  assert.throws(
    () => validateManifest({ ...baseManifest(), apiVersion: "2.0.0" }),
    /主版本/,
  );
  assert.doesNotThrow(() =>
    validateManifest({ ...baseManifest(), apiVersion: API_VERSION.replace(/^\d+/, (n) => n) }),
  );
});

test("拒绝节点 type 缺少插件前缀", () => {
  const m = baseManifest();
  m.nodes = [{ type: "main", title: "Main", ports: { inputs: [], outputs: [] } }];
  assert.throws(() => validateManifest(m), /必须以.*开头/);
});

test("拒绝未知权限", () => {
  assert.throws(
    () => validateManifest({ ...baseManifest(), permissions: ["root"] }),
    /未知权限/,
  );
});

test("声明网络主机但缺少 network 权限时拒绝", () => {
  assert.throws(
    () => validateManifest({ ...baseManifest(), network: ["api.example.com"] }),
    /network/,
  );
  assert.doesNotThrow(() =>
    validateManifest({
      ...baseManifest(),
      permissions: ["node.read", "network"],
      network: ["api.example.com"],
    }),
  );
});

test("网络主机不接受通配与路径", () => {
  for (const host of ["*", "https://a/b", "a b"]) {
    assert.throws(
      () =>
        validateManifest({
          ...baseManifest(),
          permissions: ["network"],
          network: [host],
        }),
      /精确主机名/,
    );
  }
});

test("拒绝没有节点的清单", () => {
  assert.throws(() => validateManifest({ ...baseManifest(), nodes: [] }), /至少声明一个节点/);
});

test("host() 在没有桥时给出可行动的报错", () => {
  // 「直接在浏览器打开 bundle」是非常常见的误会，报错必须说清这一点
  assert.throws(() => host(), /沙箱 iframe/);
});

test("host() 把调用转发到 icHost 桥", async () => {
  const calls = [];
  globalThis.icHost = {
    call: async (method, params) => {
      calls.push({ method, params });
      return "ok";
    },
  };
  const api = host();
  assert.equal(await api.getNode(), "ok");
  await api.patch({ a: 1 });
  await api.resize(200, 100);
  await api.generate("image.generate", { prompt: "x" });
  assert.deepEqual(
    calls.map((c) => c.method),
    ["node.get", "node.patch", "node.resize", "ai.generate"],
  );
  assert.deepEqual(calls[3].params, { capability: "image.generate", prompt: "x" });
  delete globalThis.icHost;
});

test("host() 只暴露白名单方法（不存在任意调用入口）", () => {
  globalThis.icHost = { call: async () => null };
  const api = host();
  // 关键：没有任何「直接透传 method 字符串」的通用方法。
  // 有的话，SDK 就成了绕过宿主白名单的方便入口。
  const keys = Object.keys(api);
  assert.ok(!keys.includes("call"));
  assert.ok(!keys.includes("invoke"));
  delete globalThis.icHost;
});

test("HOST_METHODS 与 PERMISSIONS 与宿主协议一致（防镜像漂移）", () => {
  // 这两个列表是**镜像**：真源在 web/src/features/plugins/sandbox/protocol.ts。
  // 这里断言它们的规模与关键项，漂移时至少会红一次。
  assert.equal(HOST_METHODS.length, 14);
  assert.ok(HOST_METHODS.includes("node.patch"));
  assert.ok(!HOST_METHODS.includes("__proto__"));
  assert.ok(!HOST_METHODS.includes("constructor"));
  assert.equal(PERMISSIONS.length, 8);
});

// ---------------------------------------------------------------- JSX runtime

test("renderToDOM 只用 textContent 写文本", () => {
  const el = renderToDOM(jsx("div", null, "<script>alert(1)</script>"));
  assert.equal(el.textContent, "<script>alert(1)</script>");
  // 关键断言：注入的标记没有变成元素
  assert.equal(el.children.length, 0);
  assert.equal(el.querySelector("script"), null);
});

test("renderToDOM 拒绝非白名单属性", () => {
  const el = renderToDOM(
    jsx("a", { href: "javascript:alert(1)", src: "x.png", "data-id": "1" }),
  );
  // href 不在白名单 → 不落 DOM；src 与 data-* 在白名单 → 保留
  assert.equal(el.getAttribute("href"), null);
  assert.equal(el.getAttribute("src"), "x.png");
  assert.equal(el.getAttribute("data-id"), "1");
});

test("renderToDOM 用 setProperty 写样式", () => {
  const el = renderToDOM(jsx("div", { style: { backgroundColor: "red" } }));
  assert.equal(el.style.backgroundColor, "red");
});

test("renderToDOM 绑定事件而不是写属性", () => {
  let fired = 0;
  const el = renderToDOM(jsx("button", { onClick: () => fired++ }));
  el.dispatchEvent(new Event("click"));
  assert.equal(fired, 1);
  assert.equal(el.getAttribute("onclick"), null);
});

test("renderToDOM 渲染子节点与列表", () => {
  const el = renderToDOM(jsx("ul", null, [jsx("li", null, "a"), jsx("li", null, "b")]));
  assert.equal(el.children.length, 2);
  assert.equal(el.textContent, "ab");
});

// ---------------------------------------------------------------- 构建脚本

test("构建脚本产出单文件 bundle 与含 sha256 的清单", async () => {
  const { execFileSync } = await import("node:child_process");
  const { mkdtempSync, writeFileSync, readFileSync, rmSync } = await import("node:fs");
  const { tmpdir } = await import("node:os");
  const { join } = await import("node:path");

  const dir = mkdtempSync(join(tmpdir(), "ic-plugin-"));
  try {
    writeFileSync(
      join(dir, "plugin.json"),
      JSON.stringify({
        ...baseManifest(),
        entry: "index.js",
      }),
    );
    writeFileSync(
      join(dir, "index.js"),
      `import { definePlugin } from "@ic/plugin-sdk";
export default definePlugin({ manifest: { key: "com.example.t", name: "T", version: "1.0.0", apiVersion: "1.0.0", entry: "index.js", permissions: [], nodes: [{ type: "com.example.t:main", title: "M", ports: { inputs: [], outputs: [] } }] }, render() {} });
`,
    );
    execFileSync("node", ["bin/build.js", dir], { cwd: process.cwd() });
    const bundle = readFileSync(join(dir, "dist", "index.js"), "utf8");
    assert.ok(!bundle.includes("import "), "bundle 不应残留 import");
    assert.ok(bundle.includes("definePlugin"), "SDK 应被内联");
    const out = JSON.parse(readFileSync(join(dir, "dist", "plugin.json"), "utf8"));
    assert.equal(out.entry, "index.js");
    assert.match(out.integrity.sha256, /^[0-9a-f]{64}$/);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("构建脚本拒绝未声明的第三方依赖", async () => {
  const { execFileSync } = await import("node:child_process");
  const { mkdtempSync, writeFileSync, rmSync } = await import("node:fs");
  const { tmpdir } = await import("node:os");
  const { join } = await import("node:path");

  const dir = mkdtempSync(join(tmpdir(), "ic-plugin-"));
  try {
    writeFileSync(join(dir, "plugin.json"), JSON.stringify(baseManifest()));
    writeFileSync(join(dir, "index.js"), `import x from "lodash";\nexport default x;\n`);
    assert.throws(
      () => execFileSync("node", ["bin/build.js", dir], { cwd: process.cwd(), stdio: "pipe" }),
      // 报错必须是可行动的（说清「为什么」和「怎么办」），
      // 否则作者会以为是构建工具坏了
      /自包含|打包器/,
    );
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
