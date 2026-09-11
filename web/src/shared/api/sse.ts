/**
 * SSE 客户端：单连接多路复用（docs/design/03 §2.6）。
 * 关键点：Last-Event-ID 续传、慢消费者背压、服务端 reconnect 事件后重连。
 */
import { setTokenProvider } from "./client";

export interface CanvasEvent {
  type: string;
  seq: number;
  version: number;
  actor: string;
  op?: unknown;
  at: string;
}

export interface SSEOptions {
  canvasId: string;
  onEvent: (ev: CanvasEvent) => void;
  onError?: (err: Event) => void;
  onReconnect?: () => void;
  /** 服务端每 15s 发心跳；超过该时间未收到任何帧视为断流。 */
  staleAfterMs?: number;
}

/**
 * 连接画布事件流。返回显式 disconnect，调用方必须在 unmount 时调用，避免连接泄漏。
 */
export function connectCanvasEvents(opts: SSEOptions): {
  disconnect: () => void;
  lastEventId: () => number;
} {
  const url = `/api/v1/canvases/${encodeURIComponent(opts.canvasId)}/events`;
  let source: EventSource | null = null;
  let lastId = 0;
  let closed = false;
  let staleTimer: ReturnType<typeof setTimeout> | null = null;
  const staleAfter = opts.staleAfterMs ?? 45_000;

  const armStaleTimer = () => {
    if (staleTimer) clearTimeout(staleTimer);
    staleTimer = setTimeout(() => {
      // 心跳缺失：主动重连而不是静默等待（避免「假在线」）
      reconnect();
    }, staleAfter);
  };

  const handle = (ev: MessageEvent) => {
    armStaleTimer();
    if (ev.lastEventId) lastId = Number(ev.lastEventId) || lastId;
    if (!ev.data) return;
    try {
      const data = JSON.parse(ev.data) as Partial<CanvasEvent>;
      opts.onEvent({
        type: ev.type,
        seq: data.seq ?? lastId,
        version: data.version ?? 0,
        actor: data.actor ?? "",
        op: data.op,
        at: data.at ?? "",
      });
    } catch {
      // 忽略无法解析的帧（服务端不应发出，但客户端不能因此崩溃）
    }
  };

  const connect = () => {
    if (closed) return;
    // EventSource 无法自定义 header，因此 token 走 cookie（服务端同时支持 Bearer 与 cookie）
    const q = lastId ? `?lastEventId=${lastId}` : "";
    source = new EventSource(url + q);
    source.addEventListener("canvas.op", handle as EventListener);
    source.addEventListener("run.step", handle as EventListener);
    source.addEventListener("run.step.delta", handle as EventListener);
    source.addEventListener("asset.created", handle as EventListener);
    source.addEventListener("presence", handle as EventListener);
    source.addEventListener("reconnect", () => {
      reconnect();
    });
    source.addEventListener("message", handle as EventListener);
    source.onerror = (err) => {
      opts.onError?.(err);
      // 浏览器会自动重连；为避免风暴，这里交给 EventSource
    };
    armStaleTimer();
  };

  const reconnect = () => {
    if (closed) return;
    source?.close();
    source = null;
    opts.onReconnect?.();
    connect();
  };

  connect();

  return {
    disconnect: () => {
      closed = true;
      if (staleTimer) clearTimeout(staleTimer);
      source?.close();
      source = null;
    },
    lastEventId: () => lastId,
  };
}

/** 供测试注入 token（SSE 走 cookie 时通常不需要）。 */
export { setTokenProvider };
