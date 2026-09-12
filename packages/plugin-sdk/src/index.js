/**
 * IC 插件 SDK 的运行时部分（10.9）。
 *
 * 设计上有一条硬约束：**这个文件不许出现任何 DOM 之外的宿主能力**。
 * 插件运行在 `sandbox="allow-scripts"`（无 allow-same-origin）的 iframe 里，
 * 它唯一的对外通道是 `window.icHost` —— 由宿主注入的 postMessage 桥。
 * 所以 SDK 不能假设自己有 fetch-with-cookies、localStorage、indexedDB……
 *
 * 与协议文件的关系：`web/src/features/plugins/sandbox/protocol.ts` 是协议的
 * 单一真源（宿主与插件共用），但它是 TS 且属于应用。SDK 面向第三方作者，
 * 必须能独立安装与引用，因此这里**只镜像类型**，而运行时行为（白名单判定）
 * 由宿主执行 —— 插件侧做权限检查是没有意义的（它拦不住自己）。
 */

/** 宿主方法白名单。与宿主 protocol.ts 的 HostMethod 一致。 */
export const HOST_METHODS = [
  "node.get",
  "node.patch",
  "node.resize",
  "node.emit",
  "graph.upstream",
  "graph.downstream",
  "graph.query",
  "asset.getUrl",
  "asset.upload",
  "ai.generate",
  "storage.get",
  "storage.set",
  "storage.remove",
  "host.toast",
];

/** 权限名。插件清单里声明，宿主逐次校验。 */
export const PERMISSIONS = [
  "node.read",
  "node.write",
  "asset.read",
  "asset.write",
  "ai.generate",
  "storage",
  "network",
  "graph.query",
];

/** 协议版本。清单里的 apiVersion 必须与之主版本一致。 */
export const API_VERSION = "1.0.0";

/**
 * 定义插件。
 *
 * 这是 SDK 存在的唯一入口。它做两件事：登记渲染函数、以及**在开发期
 * 校验声明的权限与方法是否匹配**——写错的清单在宿主里表现为运行时被拒绝，
 * 而那时插件作者已经走了很远。提前报错是 SDK 最划算的价值。
 */
export function definePlugin(spec) {
  if (!spec || typeof spec !== "object") {
    throw new Error("definePlugin: spec 必填");
  }
  const { manifest, render } = spec;
  if (!manifest || typeof manifest !== "object") {
    throw new Error("definePlugin: manifest 必填");
  }
  if (typeof render !== "function") {
    throw new Error("definePlugin: render 必须是函数");
  }
  validateManifest(manifest);
  return { manifest, render };
}

/** 校验清单的最小完整性。只做**能确定**的判断，不做猜测。 */
export function validateManifest(manifest) {
  const fail = (msg) => {
    throw new Error("invalid manifest: " + msg);
  };
  if (typeof manifest.key !== "string" || !/^[a-z0-9]+(\.[a-z0-9-]+)+$/.test(manifest.key)) {
    fail("key 必须形如 com.example.plugin");
  }
  if (typeof manifest.version !== "string" || !/^\d+\.\d+\.\d+/.test(manifest.version)) {
    fail("version 必须是 semver");
  }
  if (typeof manifest.apiVersion !== "string") {
    fail("apiVersion 必填");
  }
  if (manifest.apiVersion.split(".")[0] !== API_VERSION.split(".")[0]) {
    fail(
      `apiVersion 主版本必须是 ${API_VERSION.split(".")[0]}，实际 ${manifest.apiVersion}`,
    );
  }
  if (!Array.isArray(manifest.nodes) || manifest.nodes.length === 0) {
    fail("至少声明一个节点");
  }
  for (const node of manifest.nodes) {
    if (typeof node.type !== "string" || !node.type.startsWith(`${manifest.key}:`)) {
      fail(`节点 type 必须以 ${manifest.key}: 开头，实际 ${node.type}`);
    }
  }
  const declared = new Set(manifest.permissions || []);
  for (const p of declared) {
    if (!PERMISSIONS.includes(p)) {
      fail(`未知权限 ${p}`);
    }
  }
  // 声明网络能力必须同时声明 network 权限（与宿主 Validate 同一条规则）
  if ((manifest.network || []).length > 0 && !declared.has("network")) {
    fail("声明了 network 主机但缺少 network 权限");
  }
  if ((manifest.network || []).some((h) => h === "*" || h.includes("/") || h.includes(" "))) {
    fail("network 必须是精确主机名，不接受通配或路径");
  }
  return true;
}

/**
 * 宿主 API 的调用入口。
 *
 * 只做「把调用转发到 icHost 桥」，不做任何权限判断 —— 权限由宿主判定。
 * 插件侧再加一层检查会给人一种「已经安全了」的错觉，而它实际上
 * 只是在自己身上加了个可以自己删掉的锁。
 */
export function host() {
  const bridge = globalThis.icHost;
  if (!bridge) {
    throw new Error(
      "icHost 不可用：插件必须在宿主注入的沙箱 iframe 中运行（不要直接打开 bundle）",
    );
  }
  return {
    /** 读取本节点的快照与 config。 */
    getNode: () => bridge.call("node.get"),
    /** 写入 config（会走宿主的 op 路径，因此有校验与撤销）。 */
    patch: (config) => bridge.call("node.patch", { config }),
    /** 请求宿主调整节点尺寸。 */
    resize: (width, height) => bridge.call("node.resize", { width, height }),
    /** 向某个输出端口发出资源。 */
    emit: (portId, resource) => bridge.call("node.emit", { portId, resource }),
    /** 画布查询（需要 graph.query 权限）。 */
    query: (params) => bridge.call("graph.query", params),
    /** 取资产的可访问 URL（需要 asset.read）。 */
    assetUrl: (assetId) => bridge.call("asset.getUrl", { assetId }),
    /** 上传资产（需要 asset.write）。 */
    upload: (blob, name) => bridge.call("asset.upload", { blob, name }),
    /** 调用模型（需要 ai.generate:<capability>）。 */
    generate: (capability, params) =>
      bridge.call("ai.generate", { capability, ...params }),
    /** 键值存储（需要 storage；作用域限于本插件）。 */
    storage: {
      get: (key) => bridge.call("storage.get", { key }),
      set: (key, value) => bridge.call("storage.set", { key, value }),
      remove: (key) => bridge.call("storage.remove", { key }),
    },
    /** 提示（无需权限，但宿主会限流）。 */
    toast: (message) => bridge.call("host.toast", { message }),
  };
}

/** 注册初始化回调（宿主在 iframe 就绪后调用一次）。 */
export function onInit(fn) {
  const bridge = globalThis.icHost;
  if (!bridge) {
    throw new Error("icHost 不可用");
  }
  bridge.onInit(fn);
}

/** 请求宿主调整 iframe 高度（内容变化后调用）。 */
export function requestResize(width, height) {
  const bridge = globalThis.icHost;
  if (!bridge) return;
  bridge.resize(width, height);
}
