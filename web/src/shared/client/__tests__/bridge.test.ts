import { afterEach, describe, expect, it, vi } from "vitest";
import {
  loadBridgeConfig,
  parseSSEFrame,
  saveBridgeConfig,
  streamTurn,
  type BridgeConfig,
} from "../bridge";

/**
 * 本机桥接器客户端的协议解析与错误语义。
 *
 * 重点验证两条安全/可靠性约束：
 *   1. 令牌走 Authorization 头，**绝不**出现在 URL（URL 会进日志与 referer）；
 *   2. SSE 帧解析要能正确拼接多行 data，且不因心跳注释帧出错。
 */
describe("桥接器客户端", () => {
  afterEach(() => {
    localStorage.clear();
    vi.restoreAllMocks();
  });

  it("默认配置指向 127.0.0.1（不指向任意主机）", () => {
    const cfg = loadBridgeConfig();
    expect(cfg.url).toContain("127.0.0.1");
    expect(cfg.token).toBe("");
  });

  it("配置往返一致", () => {
    saveBridgeConfig({ url: "http://127.0.0.1:9999", token: "tk_1" });
    expect(loadBridgeConfig()).toEqual({
      url: "http://127.0.0.1:9999",
      token: "tk_1",
    });
  });

  it("损坏的配置不抛错，回落到默认值", () => {
    localStorage.setItem("ic.agent.bridge", "{not json");
    expect(() => loadBridgeConfig()).not.toThrow();
    expect(loadBridgeConfig().url).toContain("127.0.0.1");
  });

  it("令牌走 Authorization 头，不出现在 URL", async () => {
    const cfg: BridgeConfig = {
      url: "http://127.0.0.1:17371",
      token: "SECRET-TOKEN",
    };
    const calls: Array<{ url: string; headers: Record<string, string> }> = [];
    vi.stubGlobal("fetch", async (url: string, init?: RequestInit) => {
      calls.push({
        url,
        headers: (init?.headers ?? {}) as Record<string, string>,
      });
      // 返回一个立即结束的流
      return new Response(new ReadableStream({ start: (c) => c.close() }), {
        status: 200,
      });
    });

    const handle = streamTurn({
      cfg,
      turnId: "t1",
      input: "hi",
      onItem: () => {},
    });
    await new Promise((r) => setTimeout(r, 20));
    handle.close();

    expect(calls.length).toBeGreaterThan(0);
    const call = calls[0];
    expect(call.url).not.toContain("SECRET-TOKEN");
    expect(call.url).not.toContain("token=");
    expect(call.headers.Authorization).toBe("Bearer SECRET-TOKEN");
  });

  it("解析 SSE 帧：item 与 done 事件，忽略心跳注释", () => {
    const item = parseSSEFrame(
      'event: item\ndata: {"kind":"agent_message","itemId":"m1","payload":{"text":"你好"}}',
    );
    expect(item?.event).toBe("item");
    expect((item?.data as { payload: { text: string } }).payload.text).toBe(
      "你好",
    );

    const done = parseSSEFrame(
      'event: done\ndata: {"exitCode":0,"signal":null}',
    );
    expect(done?.event).toBe("done");
    expect((done?.data as { exitCode: number }).exitCode).toBe(0);

    // 心跳注释帧不产生事件（否则会被当成空消息插入 UI）
    expect(parseSSEFrame(": ping")).toBeNull();
    // 无 data 的帧同样忽略
    expect(parseSSEFrame("event: item")).toBeNull();
    // 无法解析的 data 不能抛错（服务端不应发出，但客户端不能因此崩溃）
    expect(() => parseSSEFrame("event: item\ndata: {broken")).not.toThrow();
    expect(parseSSEFrame("event: item\ndata: {broken")).toBeNull();
  });

  it("多行 data 按规范拼接后解析", () => {
    const frame = [
      "event: item",
      'data: {"kind":"agent_message","itemId":"m2",',
      'data: "payload":{"text":"split"}}',
    ].join("\n");
    const parsed = parseSSEFrame(frame);
    expect(parsed).not.toBeNull();
    expect((parsed!.data as { itemId: string }).itemId).toBe("m2");
    expect((parsed!.data as { payload: { text: string } }).payload.text).toBe(
      "split",
    );
  });

  it("桥接器未启动时上报可识别的错误（不静默）", async () => {
    const cfg: BridgeConfig = { url: "http://127.0.0.1:17371", token: "t" };
    vi.stubGlobal("fetch", async () => {
      throw new TypeError("Failed to fetch");
    });
    const errors: Error[] = [];
    const handle = streamTurn({
      cfg,
      turnId: "t",
      input: "i",
      onItem: () => {},
      onError: (e) => errors.push(e),
    });
    await new Promise((r) => setTimeout(r, 30));
    handle.close();
    expect(errors).toHaveLength(1);
    expect(errors[0].message).toContain("fetch");
  });

  it("409（上一轮未结束）映射为 busy，便于 UI 给出明确提示", async () => {
    const cfg: BridgeConfig = { url: "http://127.0.0.1:17371", token: "t" };
    vi.stubGlobal("fetch", async () => new Response("{}", { status: 409 }));
    const errors: Error[] = [];
    const handle = streamTurn({
      cfg,
      turnId: "t",
      input: "i",
      onItem: () => {},
      onError: (e) => errors.push(e),
    });
    await new Promise((r) => setTimeout(r, 30));
    handle.close();
    expect(errors[0].message).toBe("busy");
  });
});
