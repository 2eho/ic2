import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { api } from "@/shared/api";
import type { Asset } from "@/shared/api/types";
import type { TFn } from "@/app/App";

interface Props {
  t: TFn;
  workspaceId: string;
  asset: Asset;
  onClose: () => void;
  onSaved: () => void;
}

/**
 * 资产编辑（文档 §7.2）。
 *
 * 只允许改「显示元数据」（标题/标签/来源/备注），不允许改内容字段。
 * 界面上也要体现这一点：图片/尺寸/MIME 是只读展示，
 * 否则用户会以为可以替换内容——那需要重新上传（换 hash）而不是编辑元数据。
 */
export function AssetEditor({
  t,
  workspaceId,
  asset,
  onClose,
  onSaved,
}: Props) {
  const meta = (asset.meta ?? {}) as Record<string, unknown>;
  const [name, setName] = useState(asset.name ?? "");
  const [tags, setTags] = useState(String(meta.tags ?? ""));
  const [note, setNote] = useState(String(meta.note ?? ""));
  const [origin, setOrigin] = useState(
    String(meta.origin ?? asset.origin ?? ""),
  );

  const save = useMutation({
    mutationFn: () =>
      api.updateAsset(asset.id, workspaceId, {
        name,
        meta: { tags, note, origin },
      }),
    onSuccess: onSaved,
  });

  return (
    <div
      style={{
        position: "fixed",
        inset: 0,
        background: "rgba(0,0,0,.35)",
        display: "grid",
        placeItems: "center",
        zIndex: 1000,
      }}
      onClick={onClose}
    >
      <div
        className="ic-card"
        style={{ width: 460, padding: 16 }}
        onClick={(e) => e.stopPropagation()}
      >
        <div
          style={{ display: "flex", alignItems: "center", marginBottom: 10 }}
        >
          <strong style={{ flex: 1 }}>{t("assets.editAsset")}</strong>
          <button className="ic-btn ic-btn--ghost" onClick={onClose}>
            ✕
          </button>
        </div>

        {asset.kind === "image" && (
          <img
            src={api.assetThumbUrl(asset.id, workspaceId, 400)}
            alt=""
            style={{
              width: "100%",
              maxHeight: 200,
              objectFit: "contain",
              marginBottom: 10,
            }}
          />
        )}

        <label style={{ display: "block" }}>
          <span className="ic-dim" style={{ fontSize: 12 }}>
            {t("assets.assetTitle")}
          </span>
          <input
            className="ic-input"
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </label>

        <label style={{ display: "block", marginTop: 8 }}>
          <span className="ic-dim" style={{ fontSize: 12 }}>
            {t("assets.tags")}
          </span>
          <input
            className="ic-input"
            placeholder="用逗号分隔"
            value={tags}
            onChange={(e) => setTags(e.target.value)}
          />
        </label>

        <label style={{ display: "block", marginTop: 8 }}>
          <span className="ic-dim" style={{ fontSize: 12 }}>
            {t("assets.source")}
          </span>
          <input
            className="ic-input"
            value={origin}
            onChange={(e) => setOrigin(e.target.value)}
          />
        </label>

        <label style={{ display: "block", marginTop: 8 }}>
          <span className="ic-dim" style={{ fontSize: 12 }}>
            {t("assets.note")}
          </span>
          <textarea
            className="ic-input"
            style={{ minHeight: 60, fontFamily: "inherit" }}
            value={note}
            onChange={(e) => setNote(e.target.value)}
          />
        </label>

        {/* 只读的内容事实：明确标出「不可编辑」，避免用户以为能替换内容 */}
        <dl
          className="ic-dim ic-mono"
          style={{ fontSize: 11, marginTop: 10, marginBottom: 0 }}
        >
          <div>mime: {asset.mime}</div>
          <div>size: {asset.size}</div>
          <div>hash: {asset.hash.slice(0, 16)}…</div>
          <div className="ic-dim">{t("assets.contentReadonly")}</div>
        </dl>

        <div style={{ display: "flex", gap: 6, marginTop: 12 }}>
          <button
            className="ic-btn ic-btn--primary"
            disabled={save.isPending}
            onClick={() => save.mutate()}
          >
            {t("common.save")}
          </button>
          <button className="ic-btn" onClick={onClose}>
            {t("common.cancel")}
          </button>
        </div>
      </div>
    </div>
  );
}
