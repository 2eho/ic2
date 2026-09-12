import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/shared/api";
import type { CanvasKernel } from "../kernel";
import { useKernelVersion } from "../hooks/useKernel";
import { useWorkspace } from "@/shared/session/workspace";
import type { TFn } from "@/app/App";
import type { Locale } from "@/shared/i18n";

interface Props {
  t: TFn;
  locale: Locale;
  kernel: CanvasKernel;
  tab: "nodes" | "assets" | "prompts";
  onTabChange: (tab: "nodes" | "assets" | "prompts") => void;
  onLocate: (nodeId: string) => void;
}

const WIDTH_KEY = "ic.canvas.sidePanelWidth";

/** 左侧面板：画布元素 / 资产 / 提示词 三 Tab，宽度持久化（对齐 §3.16–3.17）。 */
export function SidePanel({ t, kernel, tab, onTabChange, onLocate }: Props) {
  const [width, setWidth] = useState(() =>
    Number(localStorage.getItem(WIDTH_KEY) ?? 260),
  );
  const [filter, setFilter] = useState("");
  const [typeFilter, setTypeFilter] = useState<string>("all");
  const dragging = useRef(false);
  useKernelVersion(kernel); // 订阅内核变化以刷新列表

  useEffect(() => {
    const onMove = (e: MouseEvent) => {
      if (!dragging.current) return;
      const w = Math.min(520, Math.max(200, e.clientX));
      setWidth(w);
      localStorage.setItem(WIDTH_KEY, String(w));
    };
    const onUp = () => {
      dragging.current = false;
    };
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
    return () => {
      window.removeEventListener("mousemove", onMove);
      window.removeEventListener("mouseup", onUp);
    };
  }, []);

  const nodes = kernel.scene.allNodes().filter((n) => {
    if (typeFilter !== "all" && n.type !== typeFilter) return false;
    if (!filter) return true;
    const q = filter.toLowerCase();
    return n.title.toLowerCase().includes(q) || n.id.toLowerCase().includes(q);
  });

  return (
    <div
      style={{
        position: "relative",
        width,
        borderRight: "1px solid var(--ic-border)",
        background: "var(--ic-surface)",
        display: "flex",
        flexDirection: "column",
      }}
    >
      <div
        style={{ display: "flex", borderBottom: "1px solid var(--ic-border)" }}
      >
        {(
          [
            ["nodes", t("canvas.nodes")],
            ["assets", t("assets.title")],
            ["prompts", t("prompts.title")],
          ] as const
        ).map(([key, label]) => (
          <button
            key={key}
            className="ic-btn ic-btn--ghost"
            style={{
              flex: 1,
              borderRadius: 0,
              color: tab === key ? "var(--ic-accent)" : "var(--ic-text-dim)",
            }}
            onClick={() => onTabChange(key)}
          >
            {label}
          </button>
        ))}
      </div>

      <div style={{ padding: 8, display: "flex", gap: 6 }}>
        <input
          className="ic-input"
          placeholder={t("common.search")}
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        />
        {tab === "nodes" && (
          <select
            className="ic-select"
            style={{ width: 90 }}
            value={typeFilter}
            onChange={(e) => setTypeFilter(e.target.value)}
          >
            {[
              "all",
              "prompt",
              "image",
              "video",
              "audio",
              "generation",
              "group",
            ].map((v) => (
              <option key={v} value={v}>
                {v === "all" ? t("assets.kind.all") : v}
              </option>
            ))}
          </select>
        )}
      </div>

      <div style={{ flex: 1, overflow: "auto", padding: "0 8px 8px" }}>
        {tab === "nodes" &&
          (nodes.length === 0 ? (
            <div className="ic-empty">{t("common.empty")}</div>
          ) : (
            <ul style={{ listStyle: "none", margin: 0, padding: 0 }}>
              {nodes.map((n) => (
                <li key={n.id}>
                  <button
                    className="ic-btn ic-btn--ghost"
                    style={{
                      width: "100%",
                      justifyContent: "flex-start",
                      fontSize: 12,
                      padding: "5px 8px",
                    }}
                    onClick={() => {
                      kernel.setSelection({ nodes: [n.id], edges: [] });
                      onLocate(n.id);
                    }}
                  >
                    <span
                      style={{ color: "var(--ic-text-dim)", marginRight: 6 }}
                    >
                      {n.type}
                    </span>
                    <span
                      style={{
                        overflow: "hidden",
                        textOverflow: "ellipsis",
                        whiteSpace: "nowrap",
                      }}
                    >
                      {n.title}
                    </span>
                    {n.parentId && (
                      <span className="ic-dim" style={{ marginLeft: 6 }}>
                        ↳
                      </span>
                    )}
                  </button>
                </li>
              ))}
            </ul>
          ))}
        {tab === "assets" && <AssetList t={t} />}
        {tab === "prompts" && (
          <PromptList
            t={t}
            filter={filter}
            onInsert={(text) => {
              const host = document.querySelector(
                "[data-canvas-host]",
              ) as HTMLElement | null;
              const rect = host?.getBoundingClientRect();
              const center = kernel.viewport.toWorld({
                x: (rect?.width ?? 800) / 2,
                y: (rect?.height ?? 600) / 2,
              });
              const node = kernel.createNode("prompt", center);
              if (node)
                kernel.dispatch({
                  type: "set-spec",
                  id: node.id,
                  patch: { text },
                });
            }}
          />
        )}
      </div>

      {/* 可拖宽的把手 */}
      <div
        style={{
          position: "absolute",
          right: -3,
          top: 0,
          bottom: 0,
          width: 6,
          cursor: "col-resize",
        }}
        onMouseDown={() => {
          dragging.current = true;
        }}
      />
    </div>
  );
}

function AssetList({ t }: { t: TFn }) {
  const { workspaceId } = useWorkspace();
  const [kind, setKind] = useState("");
  const q = useQuery({
    queryKey: ["assets", workspaceId, kind],
    queryFn: () => api.listAssets(workspaceId, kind, 30),
    enabled: Boolean(workspaceId),
  });
  return (
    <div>
      <select
        className="ic-select"
        value={kind}
        onChange={(e) => setKind(e.target.value)}
        style={{ marginBottom: 8 }}
      >
        {["", "image", "video", "audio", "text"].map((k) => (
          <option key={k} value={k}>
            {k || t("assets.kind.all")}
          </option>
        ))}
      </select>
      {(q.data?.items ?? []).map((a) => (
        <div
          key={a.id}
          className="ic-dim ic-mono"
          style={{
            fontSize: 11,
            padding: "4px 0",
            borderBottom: "1px solid var(--ic-border)",
          }}
        >
          {a.kind} · {a.id.slice(0, 12)} · {Math.round(a.size / 1024)}KB
        </div>
      ))}
      {(q.data?.items ?? []).length === 0 && (
        <div className="ic-empty">{t("common.empty")}</div>
      )}
    </div>
  );
}

function PromptList({
  t,
  filter,
  onInsert,
}: {
  t: TFn;
  filter: string;
  onInsert: (text: string) => void;
}) {
  const { workspaceId } = useWorkspace();
  const q = useQuery({
    queryKey: ["prompts", workspaceId, filter],
    queryFn: () => api.searchPrompts(workspaceId, filter, [], 30),
    enabled: Boolean(workspaceId),
  });
  return (
    <div>
      {(q.data?.items ?? []).map((p) => (
        <button
          key={p.id}
          className="ic-btn ic-btn--ghost"
          style={{
            display: "block",
            width: "100%",
            textAlign: "left",
            fontSize: 12,
            padding: "6px 4px",
            borderBottom: "1px solid var(--ic-border)",
          }}
          onClick={() => onInsert(p.content)}
        >
          <strong>{p.title}</strong>
          <div className="ic-dim">{p.content.slice(0, 50)}…</div>
        </button>
      ))}
      {(q.data?.items ?? []).length === 0 && (
        <div className="ic-empty">{t("common.empty")}</div>
      )}
    </div>
  );
}
