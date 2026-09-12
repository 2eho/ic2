import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/shared/api";
import { useWorkspace } from "@/shared/session/workspace";
import { exportCanvas, importCanvasPackage } from "./transfer";
import type { TFn } from "@/app/App";

/** 我的画布：项目卡片列表（对齐 docs/design/10 §1.2）。 */
export function ProjectListPage({ t }: { t: TFn }) {
  const { workspaceId, ready } = useWorkspace();
  const qc = useQueryClient();
  const [name, setName] = useState("");

  const projects = useQuery({
    queryKey: ["projects", workspaceId],
    queryFn: () => api.listProjects(workspaceId),
    enabled: ready,
  });
  const [notice, setNotice] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const importRef = useRef<HTMLInputElement>(null);
  const [importTarget, setImportTarget] = useState<string>("");

  const doImport = useMutation({
    mutationFn: (input: { projectId: string; file: File }) =>
      importCanvasPackage(input.projectId, workspaceId, input.file),
    onSuccess: (result) => {
      // 缺失与被跳过必须显式告知（静默成功 = 用户以为数据全在）
      const parts = [
        t("canvas.importDone", { n: result.assetCount }),
      ];
      if (result.legacy) parts.push(t("canvas.importLegacy"));
      if (result.missing.length)
        parts.push(t("canvas.importMissing", { n: result.missing.length }));
      if (result.skipped.length)
        parts.push(t("canvas.importSkipped", { n: result.skipped.length }));
      setNotice(parts.join(" · "));
      if (result.canvasId) {
        window.location.href = `/canvas/${result.canvasId}`;
        return;
      }
      void qc.invalidateQueries({ queryKey: ["projects", workspaceId] });
    },
    onError: (e) => setError((e as { code?: string }).code ?? "internal"),
  });

  const create = useMutation({
    mutationFn: () =>
      api.createProject(workspaceId, name || t("canvas.projectName")),
    onSuccess: () => {
      setName("");
      void qc.invalidateQueries({ queryKey: ["projects", workspaceId] });
    },
  });

  return (
    <div style={{ padding: 24, maxWidth: 1200, margin: "0 auto" }}>
      <h1 style={{ fontSize: 20, marginTop: 0 }}>{t("nav.canvases")}</h1>

      <div style={{ display: "flex", gap: 8, marginBottom: 20 }}>
        <input
          className="ic-input"
          placeholder={t("canvas.projectName")}
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
        <button
          className="ic-btn ic-btn--primary"
          onClick={() => create.mutate()}
          disabled={!ready || create.isPending}
        >
          {t("canvas.newCanvas")}
        </button>
      </div>

      {!ready && <div className="ic-empty">{t("common.loading")}</div>}
      {projects.data?.items.length === 0 && (
        <div className="ic-empty">{t("common.empty")}</div>
      )}

      <input
        ref={importRef}
        type="file"
        accept=".zip,application/zip"
        hidden
        onChange={(e) => {
          const f = e.target.files?.[0];
          if (f && importTarget) doImport.mutate({ projectId: importTarget, file: f });
          e.target.value = "";
        }}
      />
      {notice && (
        <p className="ic-dim" style={{ fontSize: 12 }}>
          {notice}
        </p>
      )}
      {error && <p className="ic-error">{t(`errors.${error}`)}</p>}

      <div
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(auto-fill, minmax(240px, 1fr))",
          gap: 12,
        }}
      >
        {(projects.data?.items ?? []).map((p) => (
          <ProjectCard
            key={p.id}
            t={t}
            projectId={p.id}
            name={p.name}
            description={p.description ?? ""}
            canvasCount={p.canvasCount}
            onImport={() => {
              setImportTarget(p.id);
              // 先设 state 再点 input：直接点会读到上一次的 importTarget
              // （React 状态更新是异步的），表现为「导入到了上一个项目」。
              requestAnimationFrame(() => importRef.current?.click());
            }}
          />
        ))}
      </div>
    </div>
  );
}

function ProjectCard({
  t,
  projectId,
  name,
  description,
  canvasCount,
  onImport,
}: {
  t: TFn;
  projectId: string;
  name: string;
  description: string;
  canvasCount: number;
  onImport: () => void;
}) {
  const { workspaceId } = useWorkspace();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [exporting, setExporting] = useState(false);

  /** 导出该项目的第一个画布为 zip（11.3）。 */
  const doExport = async () => {
    setExporting(true);
    setError(null);
    try {
      const list = await api.listCanvases(projectId);
      const first = list.items[0];
      if (!first) {
        setError("no_canvas");
        return;
      }
      const result = await exportCanvas(first.id, workspaceId);
      const url = URL.createObjectURL(result.blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `ic-canvas-${name}-${Date.now()}.zip`;
      a.click();
      // 释放对象 URL：不释放会让每导出一次就多占一份内存，
      // 而画布包可能有几百 MB。
      URL.revokeObjectURL(url);
      if (result.skipped.length > 0) {
        setError(`skipped_${result.skipped.length}`);
      }
    } catch (e) {
      setError((e as { code?: string }).code ?? "internal");
    } finally {
      setExporting(false);
    }
  };

  /** 打开项目：已有画布则直接进入最近一个，否则先创建（对齐原项目交互）。 */
  const openCanvas = async () => {
    setBusy(true);
    setError(null);
    try {
      const list = await api.listCanvases(projectId);
      const first = list.items[0];
      if (first) {
        window.location.href = `/canvas/${first.id}`;
        return;
      }
      const created = await api.createCanvas(projectId, t("canvas.untitled"));
      window.location.href = `/canvas/${created.canvas.id}`;
    } catch (e) {
      setError((e as { code?: string }).code ?? "internal");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div
      className="ic-card"
      style={{ padding: 16, display: "flex", flexDirection: "column", gap: 8 }}
    >
      <strong>{name}</strong>
      {description && (
        <span className="ic-dim" style={{ fontSize: 12 }}>
          {description}
        </span>
      )}
      <span className="ic-badge">
        {canvasCount} {t("nav.canvases")}
      </span>
      {error && <span className="ic-error">{t(`errors.${error}`)}</span>}
      <div style={{ display: "flex", gap: 6, flexWrap: "wrap" }}>
        <button
          className="ic-btn ic-btn--primary"
          disabled={busy}
          onClick={openCanvas}
        >
          {busy ? t("common.loading") : t("canvas.newCanvas")}
        </button>
        <button
          className="ic-btn"
          disabled={exporting}
          title={t("canvas.exportZip")}
          onClick={doExport}
        >
          {exporting ? t("common.loading") : t("canvas.exportZip")}
        </button>
        <button className="ic-btn" onClick={onImport}>
          {t("canvas.importZip")}
        </button>
      </div>
    </div>
  );
}
