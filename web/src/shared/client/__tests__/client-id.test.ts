import { afterEach, describe, expect, it, vi } from "vitest";
import { clientId, clientIdAsync, resetClientIdForTest } from "../client-id";

/**
 * 多标签隔离（ATK-16 同族的会话隔离要求）。
 *
 * 这里刻意用真实 sessionStorage（vitest 的 jsdom/node 环境提供），
 * 而不是 mock：我们要验证的正是「存储语义」带来的隔离，
 * mock 掉存储等于把被测行为一起 mock 掉了。
 */
describe("clientId（多标签隔离）", () => {
  afterEach(() => {
    resetClientIdForTest();
    vi.restoreAllMocks();
  });

  it("首次调用生成 id 并持久化到 sessionStorage", () => {
    const id = clientId();
    expect(id).toMatch(/^cli_/);
    expect(sessionStorage.getItem("ic.clientId")).toBe(id);
  });

  it("同一标签内重复调用返回同一 id（刷新不应换身份）", () => {
    const a = clientId();
    const b = clientId();
    expect(a).toBe(b);
  });

  it("已存在的 sessionStorage 值被复用（同一标签刷新场景）", () => {
    resetClientIdForTest("cli_existing");
    expect(clientId()).toBe("cli_existing");
  });

  it("不同标签（不同 sessionStorage 内容）得到不同 id", () => {
    resetClientIdForTest("cli_tab_a");
    const a = clientId();
    resetClientIdForTest("cli_tab_b");
    const b = clientId();
    expect(a).not.toBe(b);
  });

  it("sessionStorage 不可用时退化为内存内唯一（不抛错）", () => {
    const original = Object.getOwnPropertyDescriptor(window, "sessionStorage");
    Object.defineProperty(window, "sessionStorage", {
      configurable: true,
      get() {
        throw new Error("blocked");
      },
    });
    resetClientIdForTest();
    expect(() => clientId()).not.toThrow();
    expect(clientId()).toMatch(/^cli_/);
    if (original) Object.defineProperty(window, "sessionStorage", original);
  });

  it("BroadcastChannel 不可用时 clientIdAsync 仍返回可用 id", async () => {
    const saved = globalThis.BroadcastChannel;
    // @ts-expect-error 测试环境刻意移除
    delete globalThis.BroadcastChannel;
    const id = await clientIdAsync(1);
    expect(id).toMatch(/^cli_/);
    globalThis.BroadcastChannel = saved;
  });

  it("无冲突时 clientIdAsync 保持原 id", async () => {
    resetClientIdForTest("cli_no_conflict");
    const id = await clientIdAsync(10);
    expect(id).toBe("cli_no_conflict");
  });
});
