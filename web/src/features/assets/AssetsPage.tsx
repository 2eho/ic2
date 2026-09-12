import { useMemo, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiFailure } from "@/shared/api";
import { useWorkspace } from "@/shared/session/workspace";
import { exportAssets, importAssets } from "./transfer";
import { AssetEditor } from "./AssetEditor";
import type { Asset } from "@/shared/api/types";
import type { TFn } from "@/app/App";

const KINDS = ["image", "video", "audio", "text", "json", "file"] as const;

/**
 * 我的素材（docs/design/10 §7）。
 *
 * 三个刻意的设计：
 *   1. **搜索在客户端做**：资产列表是游标分页的，把关键词下推到 SQL 需要
 *      额外索引与迁移；单页 60 条时客户端过滤足够快（<1ms）。
 *      注意这与「提示词检索」不同——那里是几万条，必须走服务端。
 *   2. **下载读原始字节**而不是缩略图地址：原项目 v0.18.0 修过这个 bug
 *      （下载下来是预览图）。这里用 raw 接口，天然不带缩略图参数。
 *   3. **删除是软删 + 引用计数**：服务端在引用归零后由 GC 回收（7.6），
 *      因此删掉一个仍在画布上使用的资产不会立刻丢数据。
 */
export function AssetsPage({ t }: { t: TFn }) {
  const { workspaceId, ready } = useWorkspace();
  const qc = useQueryClient();
  const [kind, setKind] = useState("");
  const [query, setQuery] = useState("");
  const [editing, setEditing] = useState<Asset | null>(null);
  const [selected, setSelected] = useState<string[]>([]);
  const [notice, setNotice] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);
  const zipRef = useRef<HTMLInputElement>(null);

  const list = useQuery({
    queryKey: ["assets", workspaceId, kind],
    queryFn: () => api.listAssets(workspaceId, kind, 60),
    enabled: ready,
  });

  const items = useMemo(() => {
    const all = list.data?.items ?? [];
    const q = query.trim().toLowerCase();
    if (!q) return all;
    return all.filter((a) => (a.name ?? "").toLowerCase().includes(q));
  }, [list.data, query]);

  const invalidate = () =>
    qc.invalidateQueries({ queryKey: ["assets", workspaceId] });

  const upload = useMutation({
    mutationFn: (files: FileList) =>
      Promise.all(
        Array.from(files).map((f) => api.uploadAsset(workspaceId, f, f.name)),
      ),
    onSuccess: (uploaded) => {
      setNotice(t("assets.uploaded", { n: uploaded.length }));
      invalidate();
    },
    onError: (e) => setError(codeOf(e)),
  });

  const remove = useMutation({
    mutationFn: (ids: string[]) =>
      Promise.all(ids.map((id) => api.deleteAsset(id, workspaceId))),
    onSuccess: () => {
      setSelected([]);
      invalidate();
    },
    onError: (e) => setError(codeOf(e)),
  });

  const doExport = useMutation({
    mutationFn: async (ids: string[]) => {
      const targets = items.filter((a) => ids.includes(a.id));
      const result = await exportAssets(workspaceId, targets);
      downloadBlob(result.blob, `ic-assets-${Date.now()}.zip`);
      return result.skipped;
    },
    onSuccess: (skipped) =>
      setNotice(
        skipped.length
          ? t("assets.exportSkipped", { n: skipped.length })
          : t("assets.exported"),
      ),
    onError: (e) => setError(codeOf(e)),
  });

  const doImport = useMutation({
    mutationFn: (file: File) => importAssets(workspaceId, file),
    onSuccess: (result) => {
      // 缺失与被跳过都必须显式告知：静默成功会让用户以为数据全在
      const parts = [t("assets.imported", { n: result.imported })];
      if (result.missing.length)
        parts.push(t("assets.importMissing", { n: result.missing.length }));
      if (result.skipped.length)
        parts.push(t("assets.importSkipped", { n: result.skipped.length }));
      setNotice(parts.join(" · "));
      invalidate();
    },
    onError: (e) => setError(codeOf(e)),
  });

  return (
    <div style={{ padding: 20, maxWidth: 1400, margin: "0 auto" }}>
      <header
        style={{
          display: "flex",
          gap: 8,
          alignItems: "center",
          flexWrap: "wrap",
          marginBottom: 12,
        }}
      >
        <h1 style={{ fontSize: 18, margin: 0, marginRight: 12 }}>
          {t("assets.title")}
        </h1>
        <input
          className="ic-input"
          style={{ width: 200 }}
          placeholder={t("common.search")}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
        <select
          className="ic-select"
          style={{ width: 130 }}
          value={kind}
          onChange={(e) => setKind(e.target.value)}
        >
          <option value="">{t("assets.kind.all")}</option>
          {KINDS.map((k) => (
            <option key={k} value={k}>
              {t(`assets.kind.${k}`) !== `assets.kind.${k}`
                ? t(`assets.kind.${k}`)
                : k}
            </option>
          ))}
        </select>
        <button
          className="ic-btn ic-btn--primary"
          onClick={() => fileRef.current?.click()}
        >
          {t("common.upload")}
        </button>
        <button className="ic-btn" onClick={() => zipRef.current?.click()}>
          {t("assets.importZip")}
        </button>
        <button
          className="ic-btn"
          disabled={items.length === 0}
          onClick={() =>
            doExport.mutate(selected.length ? selected : items.map((a) => a.id))
          }
        >
          {t("assets.exportZip")}{" "}
          {selected.length > 0 && `(${selected.length})`}
        </button>
        <button
          className="ic-btn ic-btn--danger"
          disabled={selected.length === 0}
          onClick={() => remove.mutate(selected)}
        >
          {t("workbench.deleteSelected")}
        </button>
        <input
          ref={fileRef}
          type="file"
          multiple
          hidden
          onChange={(e) => {
            if (e.target.files) upload.mutate(e.target.files);
            e.target.value = "";
          }}
        />
        <input
          ref={zipRef}
          type="file"
          accept=".zip,application/zip"
          hidden
          onChange={(e) => {
            const f = e.target.files?.[0];
            if (f) doImport.mutate(f);
            e.target.value = "";
          }}
        />
      </header>

      {notice && (
        <p className="ic-dim" style={{ fontSize: 12 }}>
          {notice}
        </p>
      )}
      {error && <p className="ic-error">{t(`errors.${error}`)}</p>}
      {items.length === 0 && (
        <div className="ic-empty">{t("common.empty")}</div>
      )}

      <div
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(auto-fill, minmax(180px, 1fr))",
          gap: 12,
        }}
      >
        {items.map((asset) => (
          <div key={asset.id} className="ic-card" style={{ padding: 8 }}>
            <label
              style={{
                display: "flex",
                alignItems: "center",
                gap: 6,
                marginBottom: 4,
              }}
            >
              <input
                type="checkbox"
                checked={selected.includes(asset.id)}
                onChange={(e) =>
                  setSelected((prev) =>
                    e.target.checked
                      ? [...prev, asset.id]
                      : prev.filter((x) => x !== asset.id),
                  )
                }
              />
              <span className="ic-badge" style={{ fontSize: 10 }}>
                {asset.kind}
              </span>
            </label>

            {asset.kind === "image" ? (
              <img
                src={api.assetThumbUrl(asset.id, workspaceId, 320)}
                alt={asset.name}
                style={{
                  width: "100%",
                  height: 130,
                  objectFit: "cover",
                  borderRadius: 6,
                }}
              />
            ) : asset.kind === "video" ? (
              <video
                src={api.assetRawUrl(asset.id, workspaceId)}
                style={{
                  width: "100%",
                  height: 130,
                  borderRadius: 6,
                  background: "#000",
                }}
                muted
              />
            ) : (
              <div className="ic-empty" style={{ height: 130, fontSize: 12 }}>
                {asset.mime || asset.kind}
              </div>
            )}

            <div
              style={{
                fontSize: 12,
                marginTop: 6,
                overflow: "hidden",
                textOverflow: "ellipsis",
                whiteSpace: "nowrap",
              }}
              title={asset.name}
            >
              {asset.name || asset.id.slice(0, 12)}
            </div>
            <div className="ic-dim ic-mono" style={{ fontSize: 10 }}>
              {formatBytes(asset.size)} · {asset.mime}
            </div>

            <div
              style={{
                display: "flex",
                gap: 4,
                marginTop: 6,
                flexWrap: "wrap",
              }}
            >
              {/* 下载走 raw：不带缩略图参数，保证下载到的是原始字节 */}
              <a
                className="ic-btn"
                style={{
                  fontSize: 10,
                  padding: "2px 6px",
                  textDecoration: "none",
                }}
                href={api.assetRawUrl(asset.id, workspaceId)}
                download={asset.name || true}
              >
                {t("common.download")}
              </a>
              <button
                className="ic-btn"
                style={{ fontSize: 10, padding: "2px 6px" }}
                onClick={() => setEditing(asset)}
              >
                {t("assets.editAsset")}
              </button>
              <button
                className="ic-btn"
                style={{ fontSize: 10, padding: "2px 6px" }}
                onClick={() => {
                  // 与画布互操作（7.5）：复制 assetId，在画布中「粘贴资产」即可插入
                  void navigator.clipboard?.writeText(asset.id);
                  setNotice(t("assets.copiedId"));
                }}
              >
                {t("assets.copyId")}
              </button>
              <button
                className="ic-btn ic-btn--danger"
                style={{ fontSize: 10, padding: "2px 6px" }}
                onClick={() => remove.mutate([asset.id])}
              >
                {t("common.delete")}
              </button>
            </div>
          </div>
        ))}
      </div>

      {editing && (
        <AssetEditor
          t={t}
          workspaceId={workspaceId}
          asset={editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            invalidate();
            setEditing(null);
          }}
        />
      )}
    </div>
  );
}

function codeOf(e: unknown): string {
  return e instanceof ApiFailure
    ? e.code
    : ((e as { code?: string }).code ?? "internal");
}

function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB"];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${value >= 10 || unit === 0 ? value.toFixed(0) : value.toFixed(1)} ${units[unit]}`;
}

function downloadBlob(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  a.click();
  // 立刻回收：不释放会在大批量导出时累积内存
  setTimeout(() => URL.revokeObjectURL(url), 0);
}
