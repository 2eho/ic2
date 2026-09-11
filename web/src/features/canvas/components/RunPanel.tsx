import { useQuery } from "@tanstack/react-query";
import { api } from "@/shared/api";
import type { Run, RunStep } from "@/shared/api/types";
import type { TFn } from "@/app/App";

interface Props {
  t: TFn;
  canvasId: string;
  tick: number;
  onClose: () => void;
  onUseResult: (nodeId: string, assetId: string) => void;
}

/**
 * 运行面板：原项目没有的能力，重写后的核心差异点（docs/design/04 §5.3）。
 * 展示状态、耗时、成本、步骤时间线与失败分类。
 */
export function RunPanel({ t, canvasId, tick, onClose, onUseResult }: Props) {
  const q = useQuery({
    queryKey: ["runs", canvasId, tick],
    queryFn: () => api.listRuns(canvasId, 20),
  });

  return (
    <aside
      className="ic-card"
      style={{
        position: "absolute",
        right: 12,
        top: 12,
        width: 360,
        maxHeight: "70vh",
        overflow: "auto",
        zIndex: 200,
        padding: 12,
      }}
    >
      <div
        style={{
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
        }}
      >
        <strong>{t("canvas.run.title")}</strong>
        <button className="ic-btn ic-btn--ghost" onClick={onClose}>
          ✕
        </button>
      </div>

      {(q.data?.items ?? []).length === 0 && (
        <div className="ic-empty">{t("canvas.run.empty")}</div>
      )}

      {(q.data?.items ?? []).map((run) => (
        <RunCard key={run.id} t={t} run={run} onUseResult={onUseResult} />
      ))}
    </aside>
  );
}

function RunCard({
  t,
  run,
  onUseResult,
}: {
  t: TFn;
  run: Run;
  onUseResult: (nodeId: string, assetId: string) => void;
}) {
  const badge =
    run.status === "succeeded"
      ? "ic-badge--ok"
      : run.status === "failed"
        ? "ic-badge--danger"
        : run.status === "partial"
          ? "ic-badge--warn"
          : "";
  const duration = run.finishedAt
    ? Math.max(
        0,
        (new Date(run.finishedAt).getTime() -
          new Date(run.startedAt).getTime()) /
          1000,
      )
    : Math.max(0, (Date.now() - new Date(run.startedAt).getTime()) / 1000);

  return (
    <div
      style={{
        borderTop: "1px solid var(--ic-border)",
        paddingTop: 8,
        marginTop: 8,
      }}
    >
      <div
        style={{ display: "flex", gap: 6, alignItems: "center", fontSize: 12 }}
      >
        <span className={`ic-badge ${badge}`}>{run.status}</span>
        <span className="ic-mono ic-dim">{run.id.slice(0, 10)}</span>
        <span className="ic-dim">{duration.toFixed(1)}s</span>
        <span className="ic-dim">
          {t("canvas.run.cost")}: {(run.usage.costMicros / 1e6).toFixed(4)}
        </span>
      </div>

      {/* 计量：RunPanel 必须显示 token/张数/秒数与成本（验收要求） */}
      <div className="ic-dim" style={{ fontSize: 11, marginTop: 4 }}>
        {t("canvas.run.tokens")}: {run.usage.textTokensIn}/
        {run.usage.textTokensOut} · {t("canvas.run.images")}: {run.usage.images}
        {run.usage.videoMillis > 0 &&
          ` · video: ${(run.usage.videoMillis / 1000).toFixed(1)}s`}
      </div>

      {run.error && (
        <div className="ic-error" style={{ fontSize: 12, marginTop: 4 }}>
          {t(`errors.${run.error.code}`)}
        </div>
      )}

      <ol style={{ listStyle: "none", margin: "6px 0 0", padding: 0 }}>
        {run.steps.map((s) => (
          <StepRow key={s.id} t={t} step={s} onUseResult={onUseResult} />
        ))}
      </ol>

      <div style={{ display: "flex", gap: 6, marginTop: 6 }}>
        <button
          className="ic-btn"
          style={{ fontSize: 11, padding: "3px 8px" }}
          onClick={() => api.replayRun(run.id)}
        >
          {t("canvas.run.replay")}
        </button>
        {(run.status === "running" || run.status === "pending") && (
          <button
            className="ic-btn ic-btn--danger"
            style={{ fontSize: 11, padding: "3px 8px" }}
            onClick={() => api.cancelRun(run.id)}
          >
            {t("canvas.run.cancel")}
          </button>
        )}
      </div>
    </div>
  );
}

function StepRow({
  t,
  step,
  onUseResult,
}: {
  t: TFn;
  step: RunStep;
  onUseResult: (nodeId: string, assetId: string) => void;
}) {
  const badge =
    step.status === "succeeded"
      ? "ic-badge--ok"
      : step.status === "failed"
        ? "ic-badge--danger"
        : step.status === "running" || step.status === "retrying"
          ? "ic-badge--warn"
          : "";
  return (
    <li style={{ padding: "4px 0", fontSize: 12 }}>
      <div style={{ display: "flex", gap: 6, alignItems: "center" }}>
        <span className={`ic-badge ${badge}`}>{step.status}</span>
        <span className="ic-mono ic-dim">{step.nodeId}</span>
        <span className="ic-dim">{step.kind}</span>
        {step.attempts.length > 1 && (
          <span className="ic-dim ic-badge--warn ic-badge">
            {step.attempts.length} 次尝试
          </span>
        )}
      </div>
      {step.error && (
        <div className="ic-error" style={{ fontSize: 11 }}>
          {t(`errors.${step.error.code}`)}
          {step.error.class && (
            <span className="ic-dim"> · {step.error.class}</span>
          )}
        </div>
      )}
      {step.text && (
        <div className="ic-dim" style={{ fontSize: 11 }}>
          {step.text.slice(0, 120)}
        </div>
      )}
      {(step.outputs ?? []).length > 0 && (
        <div
          style={{ display: "flex", gap: 4, marginTop: 3, flexWrap: "wrap" }}
        >
          {(step.outputs ?? []).map((assetId) => (
            <button
              key={assetId}
              className="ic-btn"
              style={{ fontSize: 10, padding: "2px 6px" }}
              onClick={() => onUseResult(step.nodeId, assetId)}
            >
              {t("canvas.run.useAsPrimary")}
            </button>
          ))}
        </div>
      )}
    </li>
  );
}
