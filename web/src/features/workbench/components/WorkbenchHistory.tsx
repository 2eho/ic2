import { useState } from "react";
import { api } from "@/shared/api";
import type { WorkbenchLog } from "../useWorkbenchLogs";
import type { TFn } from "@/app/App";

interface Props {
  t: TFn;
  logs: WorkbenchLog[];
  workspaceId: string;
  onDelete: (ids: string[]) => void;
  onReuse: (log: WorkbenchLog) => void;
}

/**
 * 历史记录面板（docs/design/10 §6.4 / §6.11）。
 *
 * 三个必须做对的细节：
 *   - 成功/失败计数要**分别**显示（「3 成功 1 失败」比「4 张」有用得多）；
 *   - 单张缩略图要能点开看大图（否则用户只能靠猜哪次是哪次）；
 *   - 「回填参数」要连模型一起回填，不能只回填尺寸——模型变了结果会完全不同。
 */
export function WorkbenchHistory({
  t,
  logs,
  workspaceId,
  onDelete,
  onReuse,
}: Props) {
  const [selected, setSelected] = useState<string[]>([]);

  if (logs.length === 0) {
    return <div className="ic-empty">{t("common.empty")}</div>;
  }

  return (
    <div>
      <div style={{ display: "flex", gap: 6, marginBottom: 6 }}>
        <span className="ic-dim" style={{ fontSize: 12 }}>
          {t("workbench.history")} · {selected.length} {t("workbench.selected")}
        </span>
        <div style={{ flex: 1 }} />
        <button
          className="ic-btn ic-btn--danger"
          style={{ fontSize: 11 }}
          disabled={selected.length === 0}
          onClick={() => {
            onDelete(selected);
            setSelected([]);
          }}
        >
          {t("workbench.deleteSelected")}
        </button>
      </div>

      <div style={{ display: "grid", gap: 8 }}>
        {logs.map((log) => {
          const succeeded = log.results.filter(
            (r) => r.status === "succeeded",
          ).length;
          const failed = log.results.filter(
            (r) => r.status === "failed",
          ).length;
          return (
            <div key={log.id} className="ic-card" style={{ padding: 8 }}>
              <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
                <input
                  type="checkbox"
                  checked={selected.includes(log.id)}
                  onChange={(e) =>
                    setSelected((prev) =>
                      e.target.checked
                        ? [...prev, log.id]
                        : prev.filter((x) => x !== log.id),
                    )
                  }
                />
                <span className="ic-dim" style={{ fontSize: 11 }}>
                  {new Date(log.createdAt).toLocaleString()}
                </span>
                <span
                  className="ic-badge ic-badge--ok"
                  style={{ fontSize: 10 }}
                >
                  ✓{succeeded}
                </span>
                {failed > 0 && (
                  <span
                    className="ic-badge ic-badge--danger"
                    style={{ fontSize: 10 }}
                  >
                    ✕{failed}
                  </span>
                )}
                <span className="ic-dim" style={{ fontSize: 10 }}>
                  {(log.totalMs / 1000).toFixed(1)}s
                </span>
                <div style={{ flex: 1 }} />
                <button
                  className="ic-btn"
                  style={{ fontSize: 10, padding: "2px 6px" }}
                  onClick={() => onReuse(log)}
                >
                  {t("workbench.reuseParams")}
                </button>
              </div>

              <div className="ic-dim" style={{ fontSize: 11, marginTop: 4 }}>
                {log.prompt.slice(0, 80)}
              </div>

              <div
                style={{
                  display: "flex",
                  gap: 4,
                  marginTop: 6,
                  flexWrap: "wrap",
                }}
              >
                {log.results.slice(0, 8).map((r, i) =>
                  r.assetId ? (
                    <a
                      key={r.id}
                      href={api.assetRawUrl(r.assetId, workspaceId)}
                      target="_blank"
                      rel="noreferrer"
                    >
                      <img
                        src={api.assetThumbUrl(r.assetId, workspaceId, 96)}
                        alt={`log-${i}`}
                        style={{
                          width: 48,
                          height: 48,
                          objectFit: "cover",
                          borderRadius: 4,
                        }}
                      />
                    </a>
                  ) : (
                    <div
                      key={r.id}
                      className="ic-badge ic-badge--danger"
                      style={{
                        width: 48,
                        height: 48,
                        display: "grid",
                        placeItems: "center",
                        fontSize: 10,
                      }}
                      title={r.errorCode}
                    >
                      ✕
                    </div>
                  ),
                )}
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}
