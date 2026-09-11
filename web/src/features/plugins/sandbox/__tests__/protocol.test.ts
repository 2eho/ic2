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
