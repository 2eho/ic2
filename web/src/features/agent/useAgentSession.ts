import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/shared/api";
import { clientId } from "@/shared/client/client-id";
import {
  loadBridgeConfig,
  probeBridge,
  saveBridgeConfig,
  streamTurn,
  type BridgeConfig,
  type BridgeHealth,
} from "@/shared/client/bridge";
import type { AgentItemDTO, AgentTurnDTO } from "@/shared/api/endpoints";

/**
 * Agent 会话状态机。
 *
 * 从 AgentSidebar 抽出的理由：会话状态（当前会话 / 本机桥接状态 / 流式条目）
 * 的逻辑量已经超过视图本身，混在组件里会让「哪些是状态、哪些是渲染」无法分辨，
 * 而这份状态需要被单测覆盖（尤其是「流式条目如何合并进快照」这一段）。
 *
 * 关键不变量（对齐 docs/design/07）：
 *   - **快照为权威**：服务端返回的 turns 覆盖本地流式条目；实时条目只补充未物化部分；
 *   - **多标签隔离**：所有写操作带上 clientId，服务端据此只回给发起标签；
 *   - **断线不丢**：历史查询是唯一真相来源，重连后直接重取，不做本地拼接。
 */

export type BackendKind = "server" | "local";

export interface AgentSessionState {
  sessionId: string | null;
  turns: AgentTurnDTO[];
  /** 本机桥接器的实时条目（快照未物化时展示）。 */
  liveItems: AgentItemDTO[];
  bridge: {
    config: BridgeConfig;
    health: BridgeHealth | null;
    error: string | null;
  };
  backend: BackendKind;
  sending: boolean;
  error: string | null;
}

export function useAgentSession(
  canvasId: string,
  workspaceId: string,
  ready: boolean,
) {
  const qc = useQueryClient();
  const [sessionId, setSessionId] = useState<string | null>(null);
  const [backend, setBackend] = useState<BackendKind>("server");
  const [liveItems, setLiveItems] = useState<AgentItemDTO[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [bridgeConfig, setBridgeConfig] = useState<BridgeConfig>(() =>
    loadBridgeConfig(),
  );
  const [bridgeHealth, setBridgeHealth] = useState<BridgeHealth | null>(null);
  const [bridgeError, setBridgeError] = useState<string | null>(null);
  const streamRef = useRef<{ close: () => void } | null>(null);

  const session = useQuery({
    queryKey: ["agentSession", sessionId],
    queryFn: () => api.getAgentSession(sessionId!),
    enabled: Boolean(sessionId) && ready,
    retry: false,
  });

  // 快照到达时清空实时条目：快照是权威，实时条目只是「快照还没到」时的补充。
  // 若不清理，会出现同一句话显示两遍（一遍来自流、一遍来自快照）。
  useEffect(() => {
    if (session.dataUpdatedAt) setLiveItems([]);
  }, [session.dataUpdatedAt]);

  useEffect(() => () => streamRef.current?.close(), []);

  const createSession = useMutation({
    mutationFn: () =>
      api.createAgentSession(
        workspaceId,
        canvasId,
        backend === "local" ? "codex" : "http",
      ),
    onSuccess: (s) => {
      setSessionId(s.id);
      setError(null);
    },
    onError: (e) => setError(codeOf(e)),
  });

  const ensureSession = useCallback(async (): Promise<string> => {
    if (sessionId) return sessionId;
    const s = await api.createAgentSession(
      workspaceId,
      canvasId,
      backend === "local" ? "codex" : "http",
    );
    setSessionId(s.id);
    return s.id;
  }, [sessionId, workspaceId, canvasId, backend]);

  const sendTurn = useMutation({
    mutationFn: async (text: string) => {
      const sid = await ensureSession();
      if (backend === "local") {
        await sendViaBridge(sid, text);
        return null;
      }
      return api.createAgentTurn(sid, text);
    },
    onSuccess: () => {
      setError(null);
      void qc.invalidateQueries({ queryKey: ["agentSession"] });
    },
    onError: (e) => setError(codeOf(e)),
  });

  /** 走本机桥接器：把归一化后的条目直接渲染为 liveItems，并在结束时回取快照。 */
  const sendViaBridge = useCallback(
    async (sid: string, text: string) => {
      const turnId = `local_${Date.now()}`;
      await new Promise<void>((resolve, reject) => {
        streamRef.current = streamTurn({
          cfg: bridgeConfig,
          turnId,
          input: text,
          backend: "auto",
          onItem: (item) => {
            setLiveItems((prev) => [
              ...prev,
              {
                id: item.itemId,
                turnId,
                seq: prev.length,
                kind: item.kind as AgentItemDTO["kind"],
                payload: item.payload,
                source: "live",
              },
            ]);
          },
          onError: (err) => reject(mapBridgeError(err)),
          onDone: () => resolve(),
        });
      }).finally(() => {
        streamRef.current = null;
      });
      // 本机轮次同样落库：服务端记录会话与轮次，保证「刷新后还能看到历史」。
      try {
        await api.createAgentTurn(sid, text);
      } catch {
        // 落库失败不影响本次结果展示（下次快照会覆盖）
      }
      void qc.invalidateQueries({ queryKey: ["agentSession"] });
    },
    [bridgeConfig, qc],
  );

  const approve = useMutation({
    mutationFn: ({ turn, ok }: { turn: AgentTurnDTO; ok: boolean }) =>
      api.approveAgentTurn(sessionId!, turn.id, ok),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["agentSession"] }),
  });

  const probe = useCallback(async () => {
    try {
      const health = await probeBridge(bridgeConfig);
      setBridgeHealth(health);
      setBridgeError(health.backends.length === 0 ? "no_backend" : null);
    } catch (e) {
      setBridgeHealth(null);
      setBridgeError(mapBridgeError(e as Error).message);
    }
  }, [bridgeConfig]);

  const updateBridge = useCallback((cfg: BridgeConfig) => {
    saveBridgeConfig(cfg);
    setBridgeConfig(cfg);
    setBridgeHealth(null);
    setBridgeError(null);
  }, []);

  const turns = useMemo(() => {
    const fromServer = session.data?.turns ?? [];
    if (liveItems.length === 0) return fromServer;
    // 流式条目在没有服务端轮次时以「临时轮次」呈现：让用户立刻看到反馈，
    // 而不是等落库完成（那会有几百毫秒的空白，体验上像「没反应」）。
    const materialized = new Set(
      fromServer.flatMap((t) => t.items.map((i) => i.id)),
    );
    const pending = liveItems.filter((i) => !materialized.has(i.id));
    if (pending.length === 0) return fromServer;
    return [
      ...fromServer,
      {
        id: "live",
        seq: fromServer.length,
        status: "running",
        input: "",
        items: pending,
        usage: {},
      } as AgentTurnDTO,
    ];
  }, [session.data, liveItems]);

  return {
    state: {
      sessionId,
      turns,
      liveItems,
      backend,
      sending: sendTurn.isPending,
      error,
      bridgeConfig,
      bridgeHealth,
      bridgeError,
      clientId: clientId(),
    },
    actions: {
      createSession: () => createSession.mutate(),
      send: (text: string) => sendTurn.mutate(text),
      approve: (turn: AgentTurnDTO, ok: boolean) =>
        approve.mutate({ turn, ok }),
      setBackend,
      probeBridge: probe,
      updateBridge,
      refresh: () => qc.invalidateQueries({ queryKey: ["agentSession"] }),
    },
  };
}

function codeOf(e: unknown): string {
  const code = (e as { code?: string }).code;
  return code ?? "internal";
}

/** 把桥接器错误映射成稳定的 i18n code，而不是把英文 message 直接给用户看。 */
function mapBridgeError(err: Error): Error {
  const map: Record<string, string> = {
    unauthorized: "agent.bridgeUnauthorized",
    busy: "agent.bridgeBusy",
    no_backend: "agent.bridgeNoBackend",
  };
  const mapped = map[err.message];
  if (mapped) return Object.assign(new Error(mapped), { code: mapped });
  return Object.assign(new Error("agent.bridgeUnreachable"), {
    code: "agent.bridgeUnreachable",
  });
}
