import { useState } from "react";
import { useWorkspace } from "@/shared/session/workspace";
import type { AgentItemDTO, AgentTurnDTO } from "@/shared/api/endpoints";
import { useAgentSession, type BackendKind } from "./useAgentSession";
import { BridgePanel } from "./BridgePanel";
import { SkillsPanel } from "./SkillsPanel";
import type { TFn } from "@/app/App";

interface Props {
  t: TFn;
  canvasId: string;
  /** 由画布层提供：把 Agent 的 op 结果应用到内核（审批通过后）。 */
  onAgentOps?: (inverse: unknown) => void;
  onClose: () => void;
}

type Tab = "chat" | "tools" | "skills";

/**
 * Agent 侧边栏（docs/design/07 §7）。
 *
 * 关键交互语义：
 * - 审批卡片必须展示「工具名 + 影响范围（op 数/节点数）+ 预估成本」；
 *   只显示「要执行工具」等于让用户盲签；
 * - 断线重连不重不丢由服务端主键幂等保证，前端只按 items 渲染；
 * - 多标签隔离：写操作带 clientId，服务端据此只回给发起标签。
 */
export function AgentSidebar({ t, canvasId, onClose }: Props) {
  const { workspaceId, ready } = useWorkspace();
  const { state, actions } = useAgentSession(canvasId, workspaceId, ready);
  const [input, setInput] = useState("");
  const [tab, setTab] = useState<Tab>("chat");

  const send = () => {
    const text = input.trim();
    if (!text || state.sending) return;
    setInput("");
    actions.send(text);
  };

  return (
    <aside
      className="ic-card"
      style={{
        position: "absolute",
        right: 12,
        top: 12,
        width: 420,
        maxHeight: "84vh",
        display: "flex",
        flexDirection: "column",
        zIndex: 300,
      }}
    >
      <header
        style={{
          display: "flex",
          alignItems: "center",
          gap: 6,
          padding: 10,
          borderBottom: "1px solid var(--ic-border)",
        }}
      >
        <strong style={{ flex: 1 }}>{t("agent.title")}</strong>
        <select
          className="ic-select"
          style={{ width: "auto", fontSize: 11 }}
          value={state.backend}
          onChange={(e) => actions.setBackend(e.target.value as BackendKind)}
          title={t("agent.permissionMode")}
        >
          <option value="server">{t("agent.serverAgent")}</option>
          <option value="local">{t("agent.localAgent")}</option>
        </select>
        <button className="ic-btn ic-btn--ghost" onClick={onClose}>
          ✕
        </button>
      </header>

      <nav
        style={{
          display: "flex",
          gap: 4,
          padding: "6px 8px",
          borderBottom: "1px solid var(--ic-border)",
        }}
      >
        {(
          [
            ["chat", t("agent.title")],
            ["tools", t("agent.tools")],
            ["skills", t("agent.skills")],
          ] as Array<[Tab, string]>
        ).map(([key, label]) => (
          <button
            key={key}
            className={tab === key ? "ic-btn ic-btn--primary" : "ic-btn"}
            style={{ fontSize: 11, padding: "3px 8px" }}
            onClick={() => setTab(key)}
          >
            {label}
          </button>
        ))}
        <div style={{ flex: 1 }} />
        <span
          className="ic-dim ic-mono"
          style={{ fontSize: 10 }}
          title={t("agent.clientIsolation")}
        >
          {state.clientId.slice(0, 12)}
        </span>
      </nav>

      <div style={{ flex: 1, overflow: "auto", padding: 10 }}>
        {tab === "chat" && (
          <>
            {!state.sessionId && state.turns.length === 0 && (
              <div className="ic-empty">
                <button
                  className="ic-btn ic-btn--primary"
                  disabled={!ready}
                  onClick={actions.createSession}
                >
                  {t("agent.newSession")}
                </button>
              </div>
            )}
            {state.turns.map((turn) => (
              <TurnView
                key={turn.id}
                t={t}
                turn={turn}
                onApprove={(ok) => actions.approve(turn, ok)}
              />
            ))}
          </>
        )}

        {tab === "tools" && (
          <>
            <BridgePanel
              t={t}
              config={state.bridgeConfig}
              health={state.bridgeHealth}
              error={state.bridgeError}
              onSave={actions.updateBridge}
              onProbe={actions.probeBridge}
            />
            <ToolList t={t} />
          </>
        )}

        {tab === "skills" && (
          <SkillsPanel t={t} workspaceId={workspaceId} ready={ready} />
        )}
      </div>

      {state.error && (
        <p className="ic-error" style={{ padding: "0 10px", fontSize: 12 }}>
          {t(
            state.error.startsWith("errors.") ||
              state.error.startsWith("agent.")
              ? state.error
              : `errors.${state.error}`,
          )}
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
            if (e.key === "Enter" && !e.shiftKey) send();
          }}
        />
        <button
          className="ic-btn ic-btn--primary"
          disabled={!input.trim() || state.sending}
          onClick={send}
        >
          {state.sending ? t("agent.sending") : t("common.confirm")}
        </button>
      </footer>
    </aside>
  );
}

/** 工具清单：让用户先看到「Agent 能做什么」，再决定要不要用它。 */
function ToolList({ t }: { t: TFn }) {
  // 工具表由服务端生成（与 op schema 同源），这里展示的是它的静态镜像。
  // 之所以不请求接口：工具表在会话内不变，多一次请求只会让面板闪烁。
  const tools: Array<{
    name: string;
    scope: "read" | "write";
    costs?: boolean;
  }> = [
    { name: "canvas.get_state", scope: "read" },
    { name: "canvas.export_snapshot", scope: "read" },
    { name: "canvas.apply_ops", scope: "write" },
    { name: "canvas.create_text_node", scope: "write" },
    { name: "canvas.create_attachment_nodes", scope: "write" },
    { name: "canvas.create_generation_flow", scope: "write", costs: true },
    { name: "canvas.run_generation", scope: "write", costs: true },
    { name: "assets.search", scope: "read" },
    { name: "prompts.search", scope: "read" },
    { name: "runs.list", scope: "read" },
    { name: "runs.get", scope: "read" },
    { name: "skills.list", scope: "read" },
    { name: "skills.save", scope: "write" },
  ];
  return (
    <ul style={{ listStyle: "none", padding: 0, margin: "8px 0 0" }}>
      {tools.map((tool) => (
        <li
          key={tool.name}
          style={{
            display: "flex",
            alignItems: "center",
            gap: 6,
            fontSize: 11,
            padding: "4px 0",
            borderTop: "1px solid var(--ic-border)",
          }}
        >
          <code className="ic-mono" style={{ flex: 1 }}>
            {tool.name}
          </code>
          <span
            className={`ic-badge ${tool.scope === "write" ? "ic-badge--warn" : ""}`}
            style={{ fontSize: 10 }}
          >
            {tool.scope}
          </span>
          {tool.costs && (
            <span
              className="ic-badge ic-badge--danger"
              style={{ fontSize: 10 }}
            >
              ¥
            </span>
          )}
        </li>
      ))}
      <li className="ic-dim" style={{ fontSize: 11, paddingTop: 6 }}>
        {t("agent.toolConfirmHint", { ops: 0, nodes: 0 }).split("，")[0]}
      </li>
    </ul>
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

      {turn.input && (
        <p className="ic-dim" style={{ fontSize: 12, margin: "2px 0" }}>
          你：{turn.input}
        </p>
      )}
      {turn.items.map((item) => (
        <ItemView key={item.id} t={t} item={item} />
      ))}
      {turn.error && (
        <p className="ic-error" style={{ fontSize: 12 }}>
          {turn.error.code}
        </p>
      )}

      {/* 审批卡片：影响范围与成本必须可见（不能只显示"要执行工具"） */}
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
          <div className="ic-dim" style={{ fontSize: 11 }}>
            {turn.pending.estCostMicros > 0
              ? `预估成本 ¥${(turn.pending.estCostMicros / 1e6).toFixed(4)}`
              : "预估成本：未知（取决于模型价格表）"}
          </div>
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
          {payload.incomplete === true && (
            <span
              className="ic-badge ic-badge--warn"
              style={{ fontSize: 10, marginLeft: 4 }}
            >
              未完整
            </span>
          )}
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
          className={`ic-badge ${
            status === "ok"
              ? "ic-badge--ok"
              : status === "denied"
                ? "ic-badge--warn"
                : "ic-badge--danger"
          }`}
          style={{ fontSize: 11, margin: "4px 0" }}
        >
          {t("agent.toolResult")}: {status}
        </div>
      );
    }
    case "file_change":
      return (
        <div className="ic-dim ic-mono" style={{ fontSize: 11 }}>
          {String(payload.path ?? "")} {String(payload.summary ?? "")}
        </div>
      );
    case "error":
      return (
        <p className="ic-error" style={{ fontSize: 12 }}>
          {String(
            payload.message ??
              t(`errors.${String(payload.code ?? "internal")}`),
          )}
        </p>
      );
    default:
      return null;
  }
}
