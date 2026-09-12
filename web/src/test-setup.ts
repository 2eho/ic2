/**
 * 单测环境准备。
 *
 * 这里的每一项都对应一个「jsdom 与真实浏览器不一致」的坑，
 * 不做处理会让测试因为环境差异而红/绿失真：
 *   - jsdom 没有 BroadcastChannel（多标签隔离需要）；
 *   - jsdom 的 fetch 在部分版本下缺失；
 *   - AbortController 的 abort 语义在 jsdom 下与浏览器有细微差异。
 */
import { vi } from "vitest";

// BroadcastChannel：提供一个最小的进程内实现。
// 不用 mock 成「永远不响应」——那会把「复制标签检测」测成永远无冲突。
if (typeof globalThis.BroadcastChannel === "undefined") {
  class InMemoryBroadcastChannel {
    name: string;
    onmessage: ((ev: MessageEvent) => void) | null = null;
    private static channels = new Map<string, Set<InMemoryBroadcastChannel>>();

    constructor(name: string) {
      this.name = name;
      const set = InMemoryBroadcastChannel.channels.get(name) ?? new Set();
      set.add(this);
      InMemoryBroadcastChannel.channels.set(name, set);
    }

    postMessage(data: unknown): void {
      const set = InMemoryBroadcastChannel.channels.get(this.name);
      if (!set) return;
      for (const ch of set) {
        if (ch === this) continue; // 不回声给自身，与浏览器行为一致
        queueMicrotask(() => ch.onmessage?.({ data } as MessageEvent));
      }
    }

    close(): void {
      InMemoryBroadcastChannel.channels.get(this.name)?.delete(this);
    }
  }
  // @ts-expect-error 补环境
  globalThis.BroadcastChannel = InMemoryBroadcastChannel;
}

// fetch：jsdom 未实现时补一个会抛错的实现，让「未 mock 却发请求」的测试立刻失败，
// 而不是静默走到超时。
if (typeof globalThis.fetch === "undefined") {
  globalThis.fetch = vi.fn(() =>
    Promise.reject(new Error("fetch 未在测试中 mock")),
  ) as unknown as typeof fetch;
}
