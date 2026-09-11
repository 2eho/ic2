import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/shared/api";
import { useWorkspace } from "@/shared/session/workspace";
import type { AgentItemDTO, AgentTurnDTO } from "@/shared/api/endpoints";
import type { TFn } from "@/app/App";

interface Props {
  t: TFn;
  canvasId: string;
  /** 由画布层提供：把 Agent 的 op 结果应用到内核（审批通过后）。 */
  onAgentOps?: (inverse: unknown) => void;
  onClose: () => void;
}

/**
 * Agent 侧边栏（docs/design/07 §7）。
 *
 * 关键交互语义：
 * - 审批卡片必须展示「工具名 + 影响范围（op 数/节点数）+ 预估成本」；
 * - 批准后才能执行；执行结果带 inverse，支持一键撤销；
 * - 断线重连不重不丢由服务端主键幂等保证，前端只按 items 渲染。
 */
export function AgentSidebar({ t, canvasId, onClose }: Props) {
  const { workspaceId, ready } = useWorkspace();
  const qc = useQueryClient();
  const [sessionId, setSessionId] = useState<string | null>(null);
  const [input, setInput] = useState("");
  const [error, setError] = useState<string | null>(null);

  const session = useQuery({
    queryKey: ["agentSession", sessionId],
    queryFn: () => api.getAgentSession(sessionId!),
    enabled: Boolean(sessionId) && ready,
    retry: false,
  });

  const createSession = useMutation({
    mutationFn: () => api.createAgentSession(workspaceId, canvasId),
    onSuccess: (s) => setSessionId(s.id),
    onError: (e) => setError((e as { code?: string }).code ?? "internal"),
  });

  const sendTurn = useMutation({
    mutationFn: async (text: string) => {
      let sid = sessionId;
      if (!sid) {
        const s = await api.createAgentSession(workspaceId, canvasId);
        sid = s.id;
        setSessionId(sid);
      }
      return api.createAgentTurn(sid, text);
    },
    onSuccess: () => {
      setInput("");
      setError(null);
      void qc.invalidateQueries({ queryKey: ["agentSession"] });
    },
    onError: (e) => setError((e as { code?: string }).code ?? "internal"),
  });

  const approve = useMutation({
    mutationFn: ({ turn, ok }: { turn: AgentTurnDTO; ok: boolean }) =>
      api.approveAgentTurn(sessionId!, turn.id, ok),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["agentSession"] }),
  });

  const turns = session.data?.turns ?? [];

  return (
    <aside
      className="ic-card"
      style={{
        position: "absolute",
        right: 12,
        top: 12,
        width: 400,
        maxHeight: "82vh",
        display: "flex",
        flexDirection: "column",
        zIndex: 300,
      }}
    >
      <header
        style={{
          display: "flex",
          alignItems: "center",
          padding: 10,
          borderBottom: "1px solid var(--ic-border)",
        }}
      >
        <strong style={{ flex: 1 }}>{t("agent.title")}</strong>
        <span className="ic-badge">{t("agent.serverAgent")}</span>
        <button className="ic-btn ic-btn--ghost" onClick={onClose}>
          ✕
        </button>
      </header>

      <div style={{ flex: 1, overflow: "auto", padding: 10 }}>
        {!sessionId && (
          <div className="ic-empty">
            <p>{t("agent.sessions")}</p>
            <button
              className="ic-btn ic-btn--primary"
              disabled={!ready || createSession.isPending}
              onClick={() => createSession.mutate()}
            >
              {t("agent.newSession")}
            </button>
          </div>
        )}

        {turns.map((turn) => (
          <TurnView
            key={turn.id}
            t={t}
            turn={turn}
            onApprove={(ok) => approve.mutate({ turn, ok })}
          />
        ))}
      </div>

      {error && (
        <p className="ic-error" style={{ padding: "0 10px" }}>
          {t(`errors.${error}`)}
        </p>
      )}

      <footer
        style={{
          display: "flex",
          gap: 6,
          padding: 10,
          borderTop: "1px solid var(--ic-border)",
        }}
      >
        <input
          className="ic-input"
          placeholder={t("agent.placeholder")}
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey && input.trim())
              sendTurn.mutate(input.trim());
          }}
        />
        <button
          className="ic-btn ic-btn--primary"
          disabled={!input.trim() || sendTurn.isPending}
          onClick={() => sendTurn.mutate(input.trim())}
        >
          {sendTurn.isPending ? t("agent.sending") : t("common.confirm")}
        </button>
      </footer>
    </aside>
  );
}

function TurnView({
  t,
  turn,
  onApprove,
}: {
  t: TFn;
  turn: AgentTurnDTO;
  onApprove: (ok: boolean) => void;
}) {
  return (
    <div
      style={{
        borderBottom: "1px solid var(--ic-border)",
        paddingBottom: 10,
        marginBottom: 10,
      }}
    >
      <div
        style={{
          fontSize: 12,
          display: "flex",
          gap: 6,
          alignItems: "center",
          marginBottom: 6,
        }}
      >
        <span className="ic-badge">#{turn.seq}</span>
        <span
          className={`ic-badge ${
            turn.status === "succeeded"
              ? "ic-badge--ok"
              : turn.status === "failed"
                ? "ic-badge--danger"
                : turn.status === "awaiting_approval"
                  ? "ic-badge--warn"
                  : ""
          }`}
        >
          {turn.status}
        </span>
      </div>

      {turn.items.map((item) => (
        <ItemView key={item.id} t={t} item={item} />
      ))}

      {/* 审批卡片：影响范围必须可见（不能只显示"要执行工具"） */}
      {turn.pending && (
        <div
          className="ic-card"
          style={{ padding: 10, marginTop: 8, borderColor: "var(--ic-warn)" }}
        >
          <strong style={{ fontSize: 13 }}>{t("agent.toolConfirm")}</strong>
          <div className="ic-mono" style={{ fontSize: 11, marginTop: 4 }}>
            {turn.pending.tool}
          </div>
          <div className="ic-dim" style={{ fontSize: 12 }}>
            {t("agent.toolConfirmHint", {
              ops: turn.pending.opCount,
              nodes: turn.pending.nodeIds?.length ?? 0,
            })}
          </div>
          {turn.pending.estCostMicros === 0 && (
            <div className="ic-dim" style={{ fontSize: 11 }}>
              预估成本：未知（取决于模型价格表）
            </div>
          )}
          <div style={{ display: "flex", gap: 6, marginTop: 8 }}>
            <button
              className="ic-btn ic-btn--primary"
              onClick={() => onApprove(true)}
            >
              {t("agent.approve")}
            </button>
            <button
              className="ic-btn ic-btn--danger"
              onClick={() => onApprove(false)}
            >
              {t("agent.deny")}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

function ItemView({ t, item }: { t: TFn; item: AgentItemDTO }) {
  const payload = (item.payload ?? {}) as Record<string, unknown>;
  switch (item.kind) {
    case "agent_message":
      return (
        <p style={{ fontSize: 13, margin: "4px 0", whiteSpace: "pre-wrap" }}>
          {String(payload.text ?? "")}
        </p>
      );
    case "reasoning":
      return (
        <details style={{ fontSize: 12, color: "var(--ic-text-dim)" }}>
          <summary>{t("agent.reasoning")}</summary>
          <p style={{ whiteSpace: "pre-wrap" }}>{String(payload.text ?? "")}</p>
        </details>
      );
    case "tool_call":
      return (
        <div
          className="ic-badge"
          style={{ fontSize: 11, margin: "4px 0", display: "inline-flex" }}
        >
          {t("agent.toolCall")}: {String(payload.name ?? "")}
        </div>
      );
    case "tool_result": {
      const status = String(payload.status ?? "");
      return (
        <div
          className={`ic-badge ${status === "ok" ? "ic-badge--ok" : status === "denied" ? "ic-badge--warn" : "ic-badge--danger"}`}
          style={{ fontSize: 11, margin: "4px 0" }}
        >
          {t("agent.toolResult")}: {status}
        </div>
      );
    }
    case "error":
      return (
        <p className="ic-error" style={{ fontSize: 12 }}>
          {t(`errors.${String(payload.code ?? "internal")}`)}
        </p>
      );
    default:
      return null;
  }
}
