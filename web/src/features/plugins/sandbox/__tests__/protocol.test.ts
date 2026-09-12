import { describe, expect, it } from "vitest";
import {
  buildSandboxDocument,
  hasPermission,
  parsePluginMessage,
  pluginCSP,
  requiredPermission,
  sandboxAttrs,
} from "../protocol";

describe("protocol", () => {
  it("拒绝非法消息（必须丢弃而不是尽力解析）", () => {
    expect(parsePluginMessage(null)).toBeNull();
    expect(parsePluginMessage("string")).toBeNull();
    expect(parsePluginMessage({ type: "unknown" })).toBeNull();
    // 未知方法必须拒绝：白名单语义
    expect(
      parsePluginMessage({
        type: "host:call",
        id: "x",
        method: "process.exec",
      }),
    ).toBeNull();
    // 缺字段
    expect(
      parsePluginMessage({ type: "host:call", method: "node.get" }),
    ).toBeNull();
  });

  it("接受合法消息", () => {
    expect(parsePluginMessage({ type: "plugin:ready" })).toEqual({
      type: "plugin:ready",
    });
    const call = parsePluginMessage({
      type: "host:call",
      id: "1",
      method: "node.get",
    });
    expect(call).toEqual({
      type: "host:call",
      id: "1",
      method: "node.get",
      params: undefined,
    });
  });

  it("resize 消息必须是有限数", () => {
    expect(
      parsePluginMessage({
        type: "plugin:resize",
        width: Number.NaN,
        height: 10,
      }),
    ).toBeNull();
    expect(
      parsePluginMessage({ type: "plugin:resize", width: 100, height: 200 }),
    ).toEqual({
      type: "plugin:resize",
      width: 100,
      height: 200,
    });
  });

  it("日志级别回落到 info", () => {
    const m = parsePluginMessage({
      type: "plugin:log",
      level: "debug",
      message: "x",
    });
    expect(m).toEqual({ type: "plugin:log", level: "info", message: "x" });
  });

  it("方法 → 权限映射完整", () => {
    expect(requiredPermission("asset.getUrl")).toBe("asset.read");
    expect(requiredPermission("node.patch")).toBe("node.write");
    expect(requiredPermission("host.toast")).toBeNull();
  });

  it("权限判定支持 ai.generate 的子能力", () => {
    expect(hasPermission(["ai.generate:image"], "ai.generate", "image")).toBe(
      true,
    );
    expect(hasPermission(["ai.generate:image"], "ai.generate", "video")).toBe(
      false,
    );
    expect(hasPermission(["ai.generate"], "ai.generate", "video")).toBe(true);
    expect(hasPermission([], "node.read")).toBe(false);
    expect(hasPermission([], null)).toBe(true);
  });

  // INV-6：sandbox 属性绝不能包含 allow-same-origin
  it("sandbox 不含 allow-same-origin", () => {
    const attrs = sandboxAttrs();
    expect(attrs).not.toContain("allow-same-origin");
    expect(attrs).toContain("allow-scripts");
  });

  it("CSP 默认禁止一切外部连接，且不放开 unsafe-eval", () => {
    const csp = pluginCSP([]);
    expect(csp).toContain("connect-src 'none'");
    expect(csp).not.toContain("unsafe-eval");
    expect(csp).toContain("default-src 'none'");
  });

  it("CSP 按 allowlist 放开 https 主机", () => {
    const csp = pluginCSP(["cdn.jsdelivr.net"]);
    expect(csp).toContain("https://cdn.jsdelivr.net");
    expect(csp).not.toContain("connect-src *");
  });

  it("sandbox 文档包含 CSP、桥接与 bundle，且不带 allow-same-origin", () => {
    const doc = buildSandboxDocument("console.log(1);", []);
    expect(doc).toContain("Content-Security-Policy");
    expect(doc).toContain("icHost");
    expect(doc).toContain("console.log(1);");
    expect(doc).not.toContain("allow-same-origin");
  });
});

/**
 * ATK-10 补充：协议校验必须拒绝**原型链键**。
 *
 * 真实缺陷（实测确认）：旧实现用 `msg.method in METHOD_PERMISSION` 判白名单，
 * 而 `"__proto__" in {}` 为 true（来自 Object.prototype）。于是
 * `method: "__proto__"` / `"constructor"` / `"hasOwnProperty"` 全部被当成
 * 合法调用放行，权限判定也退化成「无需权限」——插件可以提交任意未声明的
 * 原型链键并绕过权限检查，与白名单语义（未声明一律拒绝）冲突。
 */
describe('ATK-10 协议校验拒绝原型链键', () => {
  const call = (method: string) =>
    parsePluginMessage({ type: 'host:call', id: '1', method });

  it('__proto__ / constructor / hasOwnProperty 都必须被拒', () => {
    for (const evil of ['__proto__', 'constructor', 'hasOwnProperty', 'toString', 'valueOf']) {
      expect(call(evil), `${evil} 被当作合法方法放行`).toBeNull();
    }
  });

  it('已声明的方法仍然通过（不能把校验做成恒拒）', () => {
    expect(call('node.get')).toEqual({
      type: 'host:call',
      id: '1',
      method: 'node.get',
      params: undefined,
    });
  });

  it('requiredPermission 对原型链键返回 null（语义即"无需权限"）', () => {
    // 若返回 Object.prototype 之类的对象，调用方会把它当权限用，
    // 于是「无需权限」被误判为「已授权」。
    expect(requiredPermission('__proto__' as never)).toBeNull();
    expect(requiredPermission('constructor' as never)).toBeNull();
    // 真实方法的权限不能被误伤
    expect(requiredPermission('node.get')).toBe('node.read');
    expect(requiredPermission('node.patch')).toBe('node.write');
    expect(requiredPermission('host.toast')).toBeNull();
  });

  it('空字符串与非字符串方法名被拒', () => {
    expect(call('')).toBeNull();
    expect(parsePluginMessage({ type: 'host:call', id: '1', method: 123 })).toBeNull();
    expect(parsePluginMessage({ type: 'host:call', id: '1', method: null })).toBeNull();
  });
});
