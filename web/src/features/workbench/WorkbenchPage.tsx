import { useCallback, useMemo, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { api, ApiFailure } from "@/shared/api";
import { useWorkspace } from "@/shared/session/workspace";
import {
  ReferenceBar,
  renumber,
  type ReferenceItem,
} from "./components/ReferenceBar";
import {
  ImageSettingsPanel,
  defaultImageParams,
  type ImageParams,
} from "./components/ImageSettingsPanel";
import {
  VideoSettingsPanel,
  defaultVideoParams,
  type VideoParams,
} from "./components/VideoSettingsPanel";
import { generateOne, pollRun } from "./generate";
import { useWorkbenchLogs, type WorkbenchLog } from "./useWorkbenchLogs";
import { WorkbenchHistory } from "./components/WorkbenchHistory";
import { ResultGrid, type ResultSlot } from "./components/ResultGrid";
import type { TFn } from "@/app/App";

interface Props {
  t: TFn;
  mode: "image" | "video";
}

/**
 * 工作台（生图 / 视频）。
 *
 * 与画布**共用执行引擎**（对齐 docs/design/10 §6.13）：这里不落画布，
 * 走 `/workspaces/{id}/generate` 直通生成，但重试策略、错误分类、
 * 计量口径、限额判定与画布完全相同——差别只有「计划怎么来」。
 *
 * 生成策略（6.3）：客户端并发发起 N 个单张 Run，每张独立状态、独立重试。
 * 不用 outputCount 一次生成 N 张的原因写在 generate.ts 顶部。
 */
export function WorkbenchPage({ t, mode }: Props) {
  const { workspaceId, ready } = useWorkspace();
  const isImage = mode === "image";

  const [prompt, setPrompt] = useState("");
  const [references, setReferences] = useState<ReferenceItem[]>([]);
  const [imageParams, setImageParams] =
    useState<ImageParams>(defaultImageParams);
  const [videoParams, setVideoParams] =
    useState<VideoParams>(defaultVideoParams);
  const [slots, setSlots] = useState<ResultSlot[]>([]);
  const [running, setRunning] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [showHistory, setShowHistory] = useState(false);
  const [showSettings, setShowSettings] = useState(true);

  const { logs, addLog, updateLog, removeLogs } = useWorkbenchLogs(mode);

  const models = useQuery({
    queryKey: ["models", workspaceId, mode],
    queryFn: () =>
      api.listModels(
        workspaceId,
        isImage ? "image.generate" : "video.generate",
      ),
    enabled: ready,
    retry: false,
  });

  const modelOptions = useMemo(
    () =>
      (models.data?.items ?? []).map((m) => ({
        id: m.id,
        providerId: m.providerId,
      })),
    [models.data],
  );

  const count = isImage ? imageParams.count : 1;

  /** 只重试一张（6.3）：失败项独立，不影响已成功的项。 */
  const retrySlot = useCallback(
    async (log: WorkbenchLog, index: number) => {
      const slot = log.results[index];
      if (!slot || running) return;
      setSlots((prev) =>
        prev.map((s) => (s.index === index ? { ...s, status: "pending" } : s)),
      );
      const result = await generateOne(
        {
          workspaceId,
          prompt: log.prompt,
          model: log.model,
          params: log.params,
          references: log.references.map((assetId, i) => ({
            id: `${assetId}-${i}`,
            name: `ref-${i + 1}`,
            assetId,
            mime: "image/png",
            order: i,
          })),
        },
        index,
      );
      setSlots((prev) =>
        prev.map((s) =>
          s.index === index
            ? {
                ...s,
                status: result.status,
                ...(result.assetId ? { assetId: result.assetId } : {}),
                durationMs: result.durationMs,
              }
            : s,
        ),
      );
      const nextResults = log.results.map((r, i) =>
        i === index
          ? {
              ...r,
              status: result.status as "pending" | "succeeded" | "failed",
              ...(result.assetId ? { assetId: result.assetId } : {}),
              durationMs: result.durationMs,
            }
          : r,
      );
      updateLog(log.id, { results: nextResults });
    },
    [running, updateLog, workspaceId],
  );

  const generate = useMutation({
    mutationFn: async () => {
      const text = prompt.trim();
      if (!text)
        throw Object.assign(new Error("prompt"), { code: "invalid_request" });

      const params = isImage
        ? {
            size: imageParams.size,
            quality: imageParams.quality,
            background: imageParams.background,
            count: String(imageParams.count),
          }
        : {
            size: videoParams.size,
            resolution: videoParams.resolution,
            ratio: videoParams.ratio,
            seconds: videoParams.seconds,
            mode:
              videoParams.ratio && references.length > 2
                ? "reference"
                : videoParams.mode,
            generateAudio: String(videoParams.generateAudio),
            watermark: String(videoParams.watermark),
          };

      const logId = `log_${Date.now().toString(36)}`;
      const initial: ResultSlot[] = Array.from({ length: count }, (_, i) => ({
        index: i,
        status: "pending",
      }));
      setSlots(initial);
      const started = Date.now();
      addLog({
        id: logId,
        createdAt: started,
        kind: mode,
        prompt: text,
        model: isImage ? imageParams.model : videoParams.model,
        params,
        references: references.map((r) => r.assetId),
        results: initial.map((s) => ({
          id: `r-${s.index}`,
          status: "pending",
          durationMs: 0,
        })),
        totalMs: 0,
      });

      // 并发发起，逐张回填：用户能看到「第 2 张先出来」而不是等全部完成
      await Promise.all(
        initial.map(async (slot) => {
          const result = await generateOne(
            {
              workspaceId,
              prompt: text,
              model: isImage ? imageParams.model : videoParams.model,
              params,
              references,
            },
            slot.index,
          );
          setSlots((prev) =>
            prev.map((s) =>
              s.index === slot.index
                ? {
                    ...s,
                    status: result.status,
                    ...(result.assetId ? { assetId: result.assetId } : {}),
                    errorCode: result.errorCode,
                    durationMs: result.durationMs,
                  }
                : s,
            ),
          );
          // 每张完成就落一次历史：中途关页面也不会丢掉已完成的记录
          updateLog(logId, {
            totalMs: Date.now() - started,
          });
        }),
      );

      const finished = await Promise.all(
        initial.map(async (slot) => {
          const r = await generateOne(
            {
              workspaceId,
              prompt: text,
              model: isImage ? imageParams.model : videoParams.model,
              params,
              references,
            },
            slot.index,
          );
          return r;
        }),
      ).catch(() => [] as Awaited<ReturnType<typeof generateOne>>[]);
      void finished;
      return logId;
    },
    onSuccess: () => setError(null),
    onError: (e) => setError(e instanceof ApiFailure ? e.code : "internal"),
    onSettled: () => setRunning(false),
  });

  const runGenerate = () => {
    if (!prompt.trim() || running) return;
    setRunning(true);
    generate.mutate();
  };

  return (
    <div
      style={{
        padding: 16,
        maxWidth: 1400,
        margin: "0 auto",
        display: "grid",
        gap: 12,
      }}
    >
      <header style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <h1 style={{ fontSize: 18, margin: 0, flex: 1 }}>
          {isImage ? t("workbench.image") : t("workbench.video")}
        </h1>
        <button
          className="ic-btn"
          style={{ fontSize: 12 }}
          onClick={() => setShowSettings((v) => !v)}
        >
          {t("workbench.settings")}
        </button>
        <button
          className="ic-btn"
          style={{ fontSize: 12 }}
          onClick={() => setShowHistory((v) => !v)}
        >
          {t("workbench.history")} ({logs.length})
        </button>
      </header>

      <div
        style={{
          display: "grid",
          gridTemplateColumns: showSettings
            ? "minmax(0, 1fr) 340px"
            : "minmax(0, 1fr)",
          gap: 12,
          alignItems: "start",
        }}
      >
        <div style={{ display: "grid", gap: 10 }}>
          <textarea
            className="ic-input"
            style={{ minHeight: 90, fontFamily: "inherit" }}
            placeholder={t("workbench.promptPlaceholder")}
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
          />

          <ReferenceBar
            t={t}
            workspaceId={workspaceId}
            items={references}
            onChange={(items) => setReferences(renumber(items))}
            max={isImage ? 7 : 3}
          />

          <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
            <button
              className="ic-btn ic-btn--primary"
              disabled={!prompt.trim() || running || !ready}
              onClick={runGenerate}
            >
              {running
                ? t("workbench.generating")
                : isImage
                  ? t("workbench.generateCount", { n: count })
                  : t("workbench.generate")}
            </button>
            <button
              className="ic-btn"
              disabled={running}
              onClick={() => {
                setPrompt("");
                setReferences([]);
                setSlots([]);
              }}
            >
              {t("workbench.clear")}
            </button>
            {error && <span className="ic-error">{t(`errors.${error}`)}</span>}
          </div>

          <ResultGrid
            t={t}
            workspaceId={workspaceId}
            slots={slots}
            onAddAsReference={(assetId) => {
              setReferences((prev) =>
                renumber([
                  ...prev,
                  {
                    id: `${assetId}-${prev.length}`,
                    name: `result-${prev.length + 1}`,
                    assetId,
                    mime: "image/png",
                    order: prev.length,
                  },
                ]),
              );
            }}
            onRetry={(index) => {
              const latest = logs[0];
              if (latest) void retrySlot(latest, index);
            }}
            onReuseParams={(params) => {
              if (isImage)
                setImageParams((prev) => ({
                  ...prev,
                  ...(params as Partial<ImageParams>),
                }));
              else
                setVideoParams((prev) => ({
                  ...prev,
                  ...(params as Partial<VideoParams>),
                }));
            }}
          />

          {showHistory && (
            <WorkbenchHistory
              t={t}
              logs={logs}
              workspaceId={workspaceId}
              onDelete={(ids) => removeLogs(ids)}
              onReuse={(log) => {
                setPrompt(log.prompt);
                setReferences(
                  log.references.map((assetId, i) => ({
                    id: `${assetId}-${i}`,
                    name: `ref-${i + 1}`,
                    assetId,
                    mime: "image/png",
                    order: i,
                  })),
                );
                if (isImage) {
                  setImageParams((prev) => ({
                    ...prev,
                    size: String(log.params.size ?? prev.size),
                    quality: String(log.params.quality ?? prev.quality),
                    count: Number(log.params.count ?? prev.count) || prev.count,
                    model: log.model || prev.model,
                  }));
                } else {
                  setVideoParams((prev) => ({
                    ...prev,
                    size: String(log.params.size ?? prev.size),
                    seconds: String(log.params.seconds ?? prev.seconds),
                    model: log.model || prev.model,
                  }));
                }
                setShowHistory(false);
              }}
            />
          )}
        </div>

        {showSettings && (
          <aside className="ic-card" style={{ padding: 12 }}>
            {isImage ? (
              <ImageSettingsPanel
                t={t}
                params={imageParams}
                onChange={(patch) =>
                  setImageParams((prev) => ({ ...prev, ...patch }))
                }
                models={modelOptions}
              />
            ) : (
              <VideoSettingsPanel
                t={t}
                params={videoParams}
                onChange={(patch) =>
                  setVideoParams((prev) => ({ ...prev, ...patch }))
                }
                models={modelOptions}
                referenceCount={references.length}
              />
            )}
          </aside>
        )}
      </div>
    </div>
  );
}

export { pollRun };
