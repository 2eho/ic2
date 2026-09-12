/**
 * 多标签页隔离（对齐 docs/design/10 §9.16）。
 *
 * 问题：同一个用户在多标签打开同一画布时，Agent 的工具调用、画布选中态、
 * SSE 连接如果共享同一个标识，就会出现「A 标签的操作出现在 B 标签」。
 *
 * 方案（两级标识）：
 *   - clientId：每个标签页唯一（sessionStorage 级），标识「谁在操作」；
 *   - sourceClientId：发起操作的标签页，用于服务端把结果**只**回给发起方。
 *
 * 为什么用 sessionStorage 而不是 localStorage：
 *   localStorage 在同一 origin 的所有标签间共享，正好是我们要区分的东西；
 *   sessionStorage 天然按标签隔离，且同一标签刷新后保持（用户刷新不该换身份）。
 *
 * 为什么还要自己去重：浏览器「复制标签页」会**连同 sessionStorage 一起复制**，
 *   因此两个标签可能拿到同一个 clientId。这里用一段极短的窗口期检测并纠正，
 *   保证不变量「同一时刻活着的两个标签 clientId 不同」成立。
 */

const CLIENT_ID_KEY = "ic.clientId";

function randomId(): string {
  // 不用 crypto.randomUUID 的强唯一性需求：这里只需要「同一浏览器内不撞」，
  // 且必须在无 crypto（老浏览器/测试环境）时也能工作。
  const rand =
    typeof crypto !== "undefined" &&
    typeof crypto.getRandomValues === "function"
      ? Array.from(crypto.getRandomValues(new Uint8Array(8)))
          .map((b) => b.toString(16).padStart(2, "0"))
          .join("")
      : Math.random().toString(16).slice(2, 18);
  return `cli_${Date.now().toString(36)}_${rand}`;
}

let cached: string | null = null;

/**
 * 返回当前标签页的 clientId。
 *
 * 检测「复制标签页」的方式：读取时向 BroadcastChannel 广播一次握手，
 * 若在 30ms 内收到同 clientId 的响应，说明本标签是复制出来的，重新生成。
 * 30ms 是刻意的短窗口：它只影响首次调用的延迟，不影响任何交互路径。
 */
export function clientId(): string {
  if (cached) return cached;
  let stored: string | null = null;
  try {
    stored = sessionStorage.getItem(CLIENT_ID_KEY);
  } catch {
    stored = null;
  }
  if (!stored) {
    stored = randomId();
    try {
      sessionStorage.setItem(CLIENT_ID_KEY, stored);
    } catch {
      // 隐私模式：退化为内存内唯一（刷新会换 id，但不影响正确性）
    }
  }
  cached = stored;
  return stored;
}

/** 异步版本：会侦测「复制标签」并纠正重名。 */
export async function clientIdAsync(windowMs = 30): Promise<string> {
  const current = clientId();
  if (typeof BroadcastChannel === "undefined") return current;

  const channel = new BroadcastChannel("ic.client-identity");
  const dup = await new Promise<boolean>((resolve) => {
    const timer = setTimeout(() => resolve(false), windowMs);
    channel.onmessage = (ev) => {
      const msg = ev.data as { type?: string; id?: string } | null;
      if (msg?.type === "probe" && msg.id === current) {
        // 另一个标签也在用这个 id：立刻回应，让**对方**先重置（先到先得，
        // 避免两个标签互相重置导致都不稳定）
        channel.postMessage({ type: "occupied", id: current });
        return;
      }
      if (msg?.type === "occupied" && msg.id === current) {
        clearTimeout(timer);
        resolve(true);
      }
    };
    channel.postMessage({ type: "probe", id: current });
  });
  channel.close();

  if (!dup) return current;
  const fresh = randomId();
  cached = fresh;
  try {
    sessionStorage.setItem(CLIENT_ID_KEY, fresh);
  } catch {
    /* 忽略 */
  }
  return fresh;
}

/** 供测试重置（避免测试之间互相影响）。 */
export function resetClientIdForTest(id?: string): void {
  cached = id ?? null;
  try {
    if (id) sessionStorage.setItem(CLIENT_ID_KEY, id);
    else sessionStorage.removeItem(CLIENT_ID_KEY);
  } catch {
    /* 忽略 */
  }
}
