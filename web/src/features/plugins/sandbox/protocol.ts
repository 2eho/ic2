/**
 * 插件宿主 ↔ 插件 iframe 的消息协议。
 *
 * 安全模型（INV-6）：
 * - iframe 使用 sandbox="allow-scripts"，**不加** allow-same-origin，插件处于 null origin；
 * - 插件无法访问宿主 DOM / Cookie / localStorage；
 * - 宿主对每条消息做 schema 校验，未知方法一律拒绝（白名单语义）。
 *
 * 该文件是协议的单一真源，服务端 internal/plugin/capability.go 的方法表与之对应。
 */

export type HostMethod =
  | "node.get"
  | "node.patch"
  | "node.resize"
  | "node.emit"
  | "graph.upstream"
  | "graph.downstream"
  | "graph.query"
  | "asset.getUrl"
  | "asset.upload"
  | "ai.generate"
  | "storage.get"
  | "storage.set"
  | "storage.remove"
  | "host.toast";

export type Permission =
  | "node.read"
  | "node.write"
  | "asset.read"
  | "asset.write"
  | "ai.generate"
  | "storage"
  | "network"
  | "graph.query";

/** 宿主 → 插件 */
export type HostToPlugin =
  | {
      type: "plugin:init";
      node: PluginNodeSnapshot;
      theme: "light" | "dark";
      width: number;
      height: number;
    }
  | { type: "plugin:update"; patch: Record<string, unknown> }
  | { type: "plugin:theme"; theme: "light" | "dark" }
  | { type: "plugin:result"; id: string; ok: true; value: unknown }
  | {
      type: "plugin:result";
      id: string;
      ok: false;
      error: { code: string; message: string };
    }
  | { type: "plugin:disable"; reason: string };

/** 插件 → 宿主 */
export type PluginToHost =
  | { type: "plugin:ready" }
  | {
      type: "host:call";
      id: string;
      method: HostMethod;
      params?: Record<string, unknown>;
    }
  | { type: "host:event"; name: string; payload?: unknown }
  | { type: "plugin:resize"; width: number; height: number }
  | { type: "plugin:log"; level: "info" | "warn" | "error"; message: string };

export interface PluginNodeSnapshot {
  id: string;
  type: string;
  title: string;
  rect: { x: number; y: number; w: number; h: number };
  config: Record<string, unknown>;
  schemaVersion: number;
}

const METHOD_PERMISSION: Record<HostMethod, Permission | null> = {
  "node.get": "node.read",
  "node.patch": "node.write",
  "node.resize": "node.write",
  "node.emit": "node.write",
  "graph.upstream": "node.read",
  "graph.downstream": "node.read",
  "graph.query": "graph.query",
  "asset.getUrl": "asset.read",
  "asset.upload": "asset.write",
  "ai.generate": "ai.generate",
  "storage.get": "storage",
  "storage.set": "storage",
  "storage.remove": "storage",
  "host.toast": null,
};

/** 校验消息结构。返回 null 表示不是合法协议消息（必须丢弃，不能"尽力解析"）。 */
export function parsePluginMessage(raw: unknown): PluginToHost | null {
  if (typeof raw !== "object" || raw === null) return null;
  const msg = raw as Record<string, unknown>;
  switch (msg.type) {
    case "plugin:ready":
      return { type: "plugin:ready" };
    case "host:call": {
      if (typeof msg.id !== "string" || typeof msg.method !== "string")
        return null;
      // 必须用 own-property 判定，不能用 `in`。
      //
      // 真实缺陷（实测）：`"__proto__" in METHOD_PERMISSION` 为 **true**，
      // `"constructor"` / `"hasOwnProperty"` / `"toString"` 同理——它们都来自
      // Object.prototype。于是宿主会把 `method: "__proto__"` 当作合法调用放行，
      // 一路走到 `METHOD_PERMISSION[method]`（拿到 Object.prototype 而非权限串），
      // 再由 `hasPermission` 判定为「无需权限」。
      // 结果是插件可以提交**任意未声明的原型链键**并绕过权限检查，
      // 与白名单语义（INV-6：未声明一律拒绝）直接冲突。
      //
      // 两道防线：先要求是自有属性，再要求值是字符串（原型链键拿不到字符串）。
      if (!Object.prototype.hasOwnProperty.call(METHOD_PERMISSION, msg.method)) return null;
      const required = METHOD_PERMISSION[msg.method as HostMethod];
      if (required !== null && typeof required !== "string") return null;
      const params =
        typeof msg.params === "object" && msg.params !== null
          ? (msg.params as Record<string, unknown>)
          : undefined;
      return {
        type: "host:call",
        id: msg.id,
        method: msg.method as HostMethod,
        params,
      };
    }
    case "host:event": {
      if (typeof msg.name !== "string") return null;
      return { type: "host:event", name: msg.name, payload: msg.payload };
    }
    case "plugin:resize": {
      if (typeof msg.width !== "number" || typeof msg.height !== "number")
        return null;
      if (!Number.isFinite(msg.width) || !Number.isFinite(msg.height))
        return null;
      return { type: "plugin:resize", width: msg.width, height: msg.height };
    }
    case "plugin:log": {
      const level =
        msg.level === "warn" || msg.level === "error" ? msg.level : "info";
      return { type: "plugin:log", level, message: String(msg.message ?? "") };
    }
    default:
      return null;
  }
}

/** 判定方法所需权限；未声明即为越权（宿主必须拒绝并审计）。 */
export function requiredPermission(method: HostMethod): Permission | null {
  // 同样必须排除原型链：`METHOD_PERMISSION["__proto__"]` 会返回 Object.prototype，
  // 而 `?? null` 不会把它变成 null（它非 null/undefined），于是调用方会拿到一个
  // 对象当权限用。返回 null 的语义是「无需权限」，因此这里绝不能误判。
  if (!Object.prototype.hasOwnProperty.call(METHOD_PERMISSION, method)) return null;
  return METHOD_PERMISSION[method] ?? null;
}

export function hasPermission(
  declared: string[],
  need: Permission | null,
  arg?: string,
): boolean {
  if (need === null) return true;
  if (need === "ai.generate" && arg) {
    return (
      declared.includes(`ai.generate:${arg}`) ||
      declared.includes("ai.generate")
    );
  }
  return declared.includes(need);
}

/** 生成 iframe 的 sandbox 属性（绝不加 allow-same-origin）。 */
export function sandboxAttrs(): string {
  return "allow-scripts";
}

/** 生成插件文档的 CSP（默认无外部连接）。 */
export function pluginCSP(allowedHosts: string[]): string {
  const connect =
    allowedHosts.length > 0
      ? allowedHosts.map((h) => `https://${h}`).join(" ")
      : "'none'";
  return `default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; connect-src ${connect}`;
}

/**
 * 生成 iframe 的 srcDoc：把插件 bundle 包进一个最小的、带 CSP 的 HTML 外壳。
 * 插件代码自身无法修改 CSP（iframe 的 CSP 由外层文档设置）。
 */
export function buildSandboxDocument(
  bundle: string,
  allowedHosts: string[],
): string {
  const csp = pluginCSP(allowedHosts);
  return `<!doctype html>
<html><head>
<meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="${csp}">
<style>html,body{margin:0;padding:0;height:100%;overflow:hidden;font-family:system-ui,sans-serif}</style>
</head><body>
<script>
(function(){
  "use strict";
  var send = function (msg) { parent.postMessage(msg, "*"); };
  // 便捷 API：插件可直接用 window.icHost
  window.icHost = {
    call: function (method, params) {
      return new Promise(function (resolve, reject) {
        var id = "c" + Math.random().toString(36).slice(2);
        window.__icPending = window.__icPending || {};
        window.__icPending[id] = { resolve: resolve, reject: reject };
        send({ type: "host:call", id: id, method: method, params: params || {} });
      });
    },
    onInit: function (fn) { window.__icOnInit = fn; },
    resize: function (w, h) { send({ type: "plugin:resize", width: w, height: h }); },
    toast: function (message) { return window.icHost.call("host.toast", { message: message }); }
  };
  window.addEventListener("message", function (ev) {
    var msg = ev.data || {};
    if (msg.type === "plugin:init" && typeof window.__icOnInit === "function") { window.__icOnInit(msg.node, msg.theme); }
    if (msg.type === "plugin:result") {
      var p = (window.__icPending || {})[msg.id];
      if (!p) return;
      delete window.__icPending[msg.id];
      if (msg.ok) p.resolve(msg.value); else p.reject(msg.error);
    }
  });
})();
<\/script>
<script>${bundle}<\/script>
</body></html>`;
}
