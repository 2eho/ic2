import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, newIdempotencyKey } from "@/shared/api";
import type { CanvasKernel } from "../kernel";
import type { RawNode } from "../kernel/types";
import { useWorkspace } from "@/shared/session/workspace";
import type { TFn } from "@/app/App";

interface Props {
  t: TFn;
  kernel: CanvasKernel;
  node: RawNode;
  onCommit: () => void;
  onRun: (nodeIds: string[]) => void;
}

const CAPABILITIES = [
  { value: "image.generate", key: "image.generate" },
  { value: "image.edit", key: "image.edit" },
  { value: "video.generate", key: "video.generate" },
  { value: "text.generate", key: "text.generate" },
  { value: "audio.generate", key: "audio.generate" },
];

/**
 * 生成参数面板（schema 驱动，见 DIV-08）。
 * 参数表单由模型能力推导，新增模型不需要改前端代码。
 */
export function GenerationPanel({ t, kernel, node, onCommit, onRun }: Props) {
  const { workspaceId } = useWorkspace();
  const [busy, setBusy] = useState(false);
  const capability = String(node.spec.capability ?? "image.generate");

  const models = useQuery({
    queryKey: ["models", workspaceId, capability],
    queryFn: () => api.listModels(workspaceId, capability),
    enabled: Boolean(workspaceId),
    retry: false,
  });

  const params = useMemo(
    () => (node.spec.params as Record<string, unknown>) ?? {},
    [node.spec.params],
  );
  const outputCount = Number(node.spec.outputCount ?? 1);

  const set = (patch: Record<string, unknown>) => {
    kernel.dispatch({ type: "set-spec", id: node.id, patch });
    onCommit();
  };
  const setParam = (key: string, value: unknown) =>
    set({ params: { ...params, [key]: value } });

  const run = async () => {
    setBusy(true);
    try {
      await api.createRun(kernel.documentId, [node.id], newIdempotencyKey());
      onRun([node.id]);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div
      style={{
        padding: 8,
        display: "flex",
        flexDirection: "column",
        gap: 6,
        fontSize: 12,
        height: "100%",
        overflow: "auto",
      }}
    >
      <label>
        <span className="ic-dim">{t("workbench.settings")}</span>
        <select
          className="ic-select"
          value={capability}
          onChange={(e) => set({ capability: e.target.value })}
        >
          {CAPABILITIES.map((c) => (
            <option key={c.value} value={c.value}>
              {c.value}
            </option>
          ))}
        </select>
      </label>

      <label>
        <span className="ic-dim">{t("workbench.model")}</span>
        <select
          className="ic-select"
          value={String(node.spec.model ?? "")}
          onChange={(e) => set({ model: e.target.value })}
        >
          <option value="">—</option>
          {(models.data?.items ?? []).map((m) => (
            <option key={m.id} value={m.id}>
              {m.id}
            </option>
          ))}
        </select>
      </label>

      {(capability === "image.generate" || capability === "image.edit") && (
        <>
          <label>
            <span className="ic-dim">{t("workbench.size")}</span>
            <select
              className="ic-select"
              value={String(params.size ?? "1024x1024")}
              onChange={(e) => setParam("size", e.target.value)}
            >
              {[
                "1024x1024",
                "1536x1024",
                "1024x1536",
                "512x512",
                "1792x1024",
              ].map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
            </select>
          </label>
          <label>
            <span className="ic-dim">{t("workbench.quality")}</span>
            <select
              className="ic-select"
              value={String(params.quality ?? "")}
              onChange={(e) => setParam("quality", e.target.value)}
            >
              <option value="">—</option>
              {["low", "medium", "high"].map((q) => (
                <option key={q} value={q}>
                  {q}
                </option>
              ))}
            </select>
          </label>
        </>
      )}

      {capability === "video.generate" && (
        <>
          <label>
            <span className="ic-dim">{t("workbench.seconds")}</span>
            <input
              className="ic-input"
              type="range"
              min={4}
              max={30}
              value={Number(params.seconds ?? 8)}
              onChange={(e) => setParam("seconds", e.target.value)}
            />
            <span className="ic-dim">{String(params.seconds ?? 8)}s</span>
          </label>
          <label>
            <span className="ic-dim">{t("workbench.vquality")}</span>
            <select
              className="ic-select"
              value={String(params.resolution ?? "720p")}
              onChange={(e) => setParam("resolution", e.target.value)}
            >
              {["480p", "720p", "1080p"].map((r) => (
                <option key={r} value={r}>
                  {r}
                </option>
              ))}
            </select>
          </label>
        </>
      )}

      {capability === "audio.generate" && (
        <>
          <label>
            <span className="ic-dim">{t("workbench.model")} · voice</span>
            <input
              className="ic-input"
              value={String(params.audioVoice ?? "alloy")}
              onChange={(e) => setParam("audioVoice", e.target.value)}
            />
          </label>
          <label>
            <span className="ic-dim">format</span>
            <select
              className="ic-select"
              value={String(params.audioFormat ?? "mp3")}
              onChange={(e) => setParam("audioFormat", e.target.value)}
            >
              {["mp3", "wav", "opus", "aac", "flac"].map((f) => (
                <option key={f} value={f}>
                  {f}
                </option>
              ))}
            </select>
          </label>
        </>
      )}

      <label>
        <span className="ic-dim">{t("workbench.count")}（1–15）</span>
        <input
          className="ic-input"
          type="number"
          min={1}
          max={15}
          value={outputCount}
          onChange={(e) => {
            const n = Math.min(15, Math.max(1, Number(e.target.value) || 1));
            set({ outputCount: n });
          }}
        />
      </label>

      <button className="ic-btn ic-btn--primary" disabled={busy} onClick={run}>
        {busy ? t("common.loading") : t("canvas.tool.generate")}
      </button>

      {node.result &&
        node.result.variants &&
        node.result.variants.length > 0 && (
          <div className="ic-dim" style={{ fontSize: 11 }}>
            {t("canvas.run.images")}: {node.result.variants.length}
            {node.result.variants.length > 1 && (
              <div
                style={{
                  display: "flex",
                  gap: 4,
                  marginTop: 4,
                  flexWrap: "wrap",
                }}
              >
                {node.result.variants.map((_v, i) => (
                  <button
                    key={i}
                    className={`ic-btn ${i === (node.result?.primary ?? 0) ? "ic-btn--primary" : ""}`}
                    style={{ padding: "2px 6px", fontSize: 11 }}
                    onClick={() => {
                      kernel.dispatch({
                        type: "set-state",
                        id: node.id,
                        state: node.state,
                        result: { ...node.result!, primary: i },
                      });
                      onCommit();
                    }}
                  >
                    {i + 1}
                  </button>
                ))}
              </div>
            )}
          </div>
        )}
    </div>
  );
}
