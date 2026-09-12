/**
 * 本机 Agent 桥接器客户端（docs/design/07 §3）。
 *
 * 职责边界：**只做转接与展示**。桥接器与本机 CLI 提供能力，
 * 服务端仍是唯一权威（所有写操作仍走标准 op 路径）。
 *
 * 地址与令牌存 localStorage（而不是内存）：用户配一次就好，
 * 且它是「本机桥接器的访问令牌」，不是云端凭据——
 * 即使泄露，攻击者也只能访问用户自己机器上那个只监听 127.0.0.1 的端口。
 * 这一点与「模型 API Key 绝不落浏览器」并不冲突，两者是不同的东西，
 * 但界面上必须把这一区别说清楚，否则用户会以为「既然这个能存，那个也能存」。
 */
export interface BridgeConfig {
  url: string;
  token: string;
}

const KEY = "ic.agent.bridge";

export function loadBridgeConfig(): BridgeConfig {
  try {
    const raw = localStorage.getItem(KEY);
    if (!raw) return { url: "http://127.0.0.1:17371", token: "" };
    const parsed = JSON.parse(raw) as Partial<BridgeConfig>;
    return {
      url: parsed.url || "http://127.0.0.1:17371",
      token: parsed.token || "",
    };
  } catch {
    return { url: "http://127.0.0.1:17371", token: "" };
  }
}

export function saveBridgeConfig(cfg: BridgeConfig): void {
  try {
    localStorage.setItem(KEY, JSON.stringify(cfg));
  } catch {
    /* 隐私模式忽略 */
  }
}

export interface BridgeHealth {
  status: string;
  backends: string[];
  active: boolean;
}

/** 探测桥接器是否可用。 */
export async function probeBridge(
  cfg: BridgeConfig,
  timeoutMs = 3000,
): Promise<BridgeHealth> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  try {
    const res = await fetch(`${cfg.url.replace(/\/$/, "")}/healthz`, {
      headers: { Authorization: `Bearer ${cfg.token}` },
      signal: controller.signal,
    });
    if (res.status === 401) throw new Error("unauthorized");
    if (!res.ok) throw new Error(`bridge_${res.status}`);
    return (await res.json()) as BridgeHealth;
  } finally {
    clearTimeout(timer);
  }
}

export interface BridgeItem {
  kind: string;
  itemId: string;
  payload: Record<string, unknown>;
}

export interface StreamTurnOptions {
  cfg: BridgeConfig;
  turnId: string;
  input: string;
  backend?: "auto" | "codex" | "claude";
  onItem: (item: BridgeItem) => void;
  onDone?: (info: { exitCode: number | null; signal: string | null }) => void;
  onError?: (err: Error) => void;
}

/**
 * 提交一轮输入并流式接收归一化后的 Item。
 *
 * 实现选择 fetch + ReadableStream 而不是 EventSource：
 *   EventSource 无法自定义 header，而令牌**必须**走 header 而不是 URL
 *   （URL 会进日志与 referer）。这是原项目已验证的结论，这里保持一致。
 */
export function streamTurn(opts: StreamTurnOptions): { close: () => void } {
  const controller = new AbortController();
  const base = opts.cfg.url.replace(/\/$/, "");

  void (async () => {
    try {
      const res = await fetch(`${base}/turns`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${opts.cfg.token}`,
        },
        body: JSON.stringify({
          turnId: opts.turnId,
          input: opts.input,
          backend: opts.backend ?? "auto",
        }),
        signal: controller.signal,
      });
      if (res.status === 409) throw new Error("busy");
      if (res.status === 401) throw new Error("unauthorized");
      if (res.status === 422) throw new Error("no_backend");
      if (!res.ok || !res.body) throw new Error(`bridge_${res.status}`);

      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let buffer = "";
      for (;;) {
        const { value, done } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });
        let idx: number;
        while ((idx = buffer.indexOf("\n\n")) >= 0) {
          const frame = buffer.slice(0, idx);
          buffer = buffer.slice(idx + 2);
          const parsed = parseSSEFrame(frame);
          if (!parsed) continue;
          if (parsed.event === "item") opts.onItem(parsed.data as BridgeItem);
          if (parsed.event === "done") {
            opts.onDone?.(
              parsed.data as { exitCode: number | null; signal: string | null },
            );
          }
        }
      }
    } catch (err) {
      if ((err as Error).name === "AbortError") return;
      opts.onError?.(err as Error);
    }
  })();

  return { close: () => controller.abort() };
}

/**
 * 解析一帧 SSE（多行 data 按规范用 \n 拼接）。
 *
 * 导出是为了能脱离 fetch/ReadableStream 单测协议解析——
 * 在 jsdom 里构造真实流会引入环境差异，让「解析逻辑是否对」被环境问题掩盖。
 */
export function parseSSEFrame(
  frame: string,
): { event: string; data: unknown } | null {
  let event = "message";
  const dataLines: string[] = [];
  for (const line of frame.split("\n")) {
    if (line.startsWith(":")) continue; // 心跳注释
    if (line.startsWith("event:")) event = line.slice(6).trim();
    else if (line.startsWith("data:")) dataLines.push(line.slice(5).trim());
  }
  if (dataLines.length === 0) return null;
  try {
    return { event, data: JSON.parse(dataLines.join("\n")) };
  } catch {
    return null;
  }
}
