import { useCallback, useEffect, useRef } from "react";
import { api, ApiFailure, newIdempotencyKey } from "@/shared/api";
import type { CanvasDoc, Op } from "../kernel/types";
import type { CanvasKernel } from "../kernel";
import type { TFn } from "@/app/App";

interface Options {
  canvasId: string;
  kernel: CanvasKernel | null;
  t: TFn;
  onState: (s: "idle" | "syncing" | "error" | "conflict" | "offline") => void;
}

export interface CanvasSync {
  flush: () => void;
  flushViewport: () => void;
  applyRemoteOp: (actor: string, op: Op) => void;
  conflictDoc: CanvasDoc | null;
}

/**
 * 写入路径（docs/design/04 §3.1）：
 *   命令 → 内核乐观应用 → rAF 合并 → POST /ops → 版本推进 / 冲突处理
 *
 * 关键不变式：
 * - 本地 op 与请求一一对应，失败时不清空队列（避免静默丢改动）；
 * - 冲突（409）时用服务端权威文档 re-sync，并明确告知用户。
 */
export function useCanvasSync({
  canvasId,
  kernel,
  t,
  onState,
}: Options): CanvasSync {
  const flushing = useRef(false);
  const failed = useRef<Op[]>([]);
  const conflictRef = useRef<CanvasDoc | null>(null);
  const rafRef = useRef<number | null>(null);

  const doFlush = useCallback(async () => {
    if (!kernel || flushing.current) return;
    const { baseVersion, ops } = kernel.submitPayload();
    if (ops.length === 0) return;
    flushing.current = true;
    onState("syncing");
    try {
      const res = await api.appendOps(
        canvasId,
        baseVersion,
        ops,
        newIdempotencyKey(),
      );
      kernel.commitVersion(res.version);
      // 服务端自动 rebase 时以权威文档为准（本地与在途 op 已合并）
      if (res.rebased && res.document) {
        kernel.applyAuthoritative(res.document);
      }
      failed.current = [];
      onState("idle");
    } catch (e) {
      const err = e as ApiFailure;
      if (err.status === 409) {
        // 语义冲突：拉权威文档重新同步，不静默覆盖
        try {
          const auth = await api.getCanvas(canvasId);
          kernel.applyAuthoritative(auth);
          conflictRef.current = auth;
        } catch {
          onState("offline");
          return;
        }
        onState("conflict");
        return;
      }
      // 网络类错误：保留 op 以便恢复后重发（不丢改动）
      failed.current = [...failed.current, ...ops];
      onState("error");
    } finally {
      flushing.current = false;
    }
  }, [kernel, canvasId, onState]);

  /** rAF 合并提交，避免拖拽时每个 pointermove 都发请求。 */
  const flush = useCallback(() => {
    if (rafRef.current !== null) return;
    rafRef.current = requestAnimationFrame(() => {
      rafRef.current = null;
      void doFlush();
    });
  }, [doFlush]);

  const flushViewport = useCallback(() => {
    if (!kernel) return;
    kernel.dispatch({
      type: "set-viewport",
      viewport: kernel.viewport.current,
    });
    flush();
  }, [kernel, flush]);

  const applyRemoteOp = useCallback(
    (actor: string, op: Op) => {
      if (!kernel) return;
      // 本端 actor 的回声忽略：本地已乐观应用（见 docs/design/04 §3.2）
      if (actor && actor === kernel.localActor) return;
      kernel.applyRemote(op);
    },
    [kernel],
  );

  // 网络恢复后重发失败的 op（断网 30s 恢复不丢不重，ATK-12）
  useEffect(() => {
    const onOnline = () => {
      onState("idle");
      flush();
    };
    const onOffline = () => onState("offline");
    window.addEventListener("online", onOnline);
    window.addEventListener("offline", onOffline);
    return () => {
      window.removeEventListener("online", onOnline);
      window.removeEventListener("offline", onOffline);
    };
  }, [flush, onState]);

  useEffect(() => {
    if (rafRef.current !== null) {
      cancelAnimationFrame(rafRef.current);
      rafRef.current = null;
    }
    void t;
  }, [t]);

  return {
    flush,
    flushViewport,
    applyRemoteOp,
    conflictDoc: conflictRef.current,
  };
}
