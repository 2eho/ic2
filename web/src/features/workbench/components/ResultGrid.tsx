import { api } from "@/shared/api";
import type { TFn } from "@/app/App";

export interface ResultSlot {
  index: number;
  status: "pending" | "succeeded" | "failed";
  assetId?: string;
  errorCode?: string;
  durationMs?: number;
}

interface Props {
  t: TFn;
  workspaceId: string;
  slots: ResultSlot[];
  onAddAsReference: (assetId: string) => void;
  onRetry: (index: number) => void;
  onReuseParams: (params: Record<string, unknown>) => void;
}

/**
 * 结果网格（docs/design/10 §6.5）。
 *
 * 关键语义：**每张结果独立**。失败的那张单独显示错误码与重试按钮，
 * 成功的照常展示。把所有结果合成一个「批次状态」会让用户以为整体失败，
 * 从而放弃已经拿到手的结果。
 */
export function ResultGrid({
  t,
  workspaceId,
  slots,
  onAddAsReference,
  onRetry,
}: Props) {
  if (slots.length === 0) return null;

  return (
    <div
      style={{
        display: "grid",
        gridTemplateColumns: "repeat(auto-fill, minmax(200px, 1fr))",
        gap: 10,
      }}
    >
      {slots.map((slot) => (
        <div key={slot.index} className="ic-card" style={{ padding: 8 }}>
          {slot.status === "pending" && (
            <div className="ic-empty" style={{ height: 160, fontSize: 12 }}>
              {t("workbench.generating")}
            </div>
          )}

          {slot.status === "succeeded" && slot.assetId && (
            <>
              <img
                src={api.assetThumbUrl(slot.assetId, workspaceId, 400)}
                alt={`result-${slot.index + 1}`}
                style={{
                  width: "100%",
                  height: 160,
                  objectFit: "cover",
                  borderRadius: 6,
                }}
              />
              <div
                style={{
                  display: "flex",
                  gap: 4,
                  marginTop: 6,
                  flexWrap: "wrap",
                }}
              >
                <a
                  className="ic-btn"
                  style={{
                    fontSize: 11,
                    padding: "3px 8px",
                    textDecoration: "none",
                  }}
                  href={api.assetRawUrl(slot.assetId, workspaceId)}
                  download
                >
                  {t("common.download")}
                </a>
                <button
                  className="ic-btn"
                  style={{ fontSize: 11, padding: "3px 8px" }}
                  onClick={() => onAddAsReference(slot.assetId!)}
                >
                  {t("workbench.addAsReference")}
                </button>
              </div>
            </>
          )}

          {slot.status === "failed" && (
            <div className="ic-empty" style={{ height: 160, fontSize: 12 }}>
              <p className="ic-error">
                {t(`errors.${slot.errorCode ?? "internal"}`)}
              </p>
              {/* 单张重试：不影响其他已完成的结果 */}
              <button
                className="ic-btn"
                style={{ fontSize: 11 }}
                onClick={() => onRetry(slot.index)}
              >
                {t("common.retry")}
              </button>
            </div>
          )}

          <div className="ic-dim" style={{ fontSize: 10, marginTop: 4 }}>
            #{slot.index + 1}
            {slot.durationMs
              ? ` · ${(slot.durationMs / 1000).toFixed(1)}s`
              : ""}
          </div>
        </div>
      ))}
    </div>
  );
}
