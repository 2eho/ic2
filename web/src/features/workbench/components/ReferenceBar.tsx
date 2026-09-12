import { useCallback, useEffect, useRef } from "react";
import { api } from "@/shared/api";
import type { TFn } from "@/app/App";

export interface ReferenceItem {
  id: string;
  name: string;
  assetId: string;
  mime: string;
  /** 上传顺序的序号，用于在提示词里标注「图片N」。 */
  order: number;
}

interface Props {
  t: TFn;
  workspaceId: string;
  items: ReferenceItem[];
  onChange: (items: ReferenceItem[]) => void;
  /** 视频工作台允许的参考数量上限（含图/视频/音频混排）。 */
  max?: number;
}

/**
 * 参考图栏（对齐 docs/design/10 §6.1）。
 *
 * 复刻的关键语义（原项目里这三条是靠手工维护 index 的，经常错位）：
 *   - **编号即提交顺序**：角标显示的编号必须与提交给上游的顺序一致，
 *     否则用户在提示词里写「图片2」会指向另一张图；
 *   - 支持上传 / 剪贴板粘贴 / 拖入三种入口，行为必须一致；
 *   - 排序后要重编号，不能只换位置不换编号。
 */
export function ReferenceBar({
  t,
  workspaceId,
  items,
  onChange,
  max = 7,
}: Props) {
  const fileRef = useRef<HTMLInputElement>(null);
  const dragDepth = useRef(0);

  const addFiles = useCallback(
    async (files: FileList | File[]) => {
      const list = Array.from(files).filter((f) => f.type.startsWith("image/"));
      if (list.length === 0) return;
      const room = Math.max(0, max - items.length);
      const accepted = list.slice(0, room);
      const uploaded = await Promise.all(
        accepted.map(async (file) => {
          const asset = await api.uploadAsset(workspaceId, file, file.name);
          return {
            id: asset.id,
            name: file.name,
            assetId: asset.id,
            mime: file.type,
            order: 0,
          };
        }),
      );
      onChange(renumber([...items, ...uploaded]));
    },
    [items, max, onChange, workspaceId],
  );

  const pasteFromClipboard = useCallback(async () => {
    try {
      const clipboard = await navigator.clipboard.read();
      const blobs: File[] = [];
      for (const item of clipboard) {
        const type = item.types.find((ty) => ty.startsWith("image/"));
        if (!type) continue;
        const blob = await item.getType(type);
        // File 需要名字：桥接器与上游都会用到，纯 Blob 会让文件名变成 undefined
        blobs.push(
          new File([blob], `clipboard-${blobs.length + 1}.png`, { type }),
        );
      }
      if (blobs.length === 0) return;
      await addFiles(blobs);
    } catch {
      // 剪贴板不可用（未授权/非 https）：静默忽略而不是弹错——
      // 用户点一下没反应比看到一条「无法访问剪贴板」更容易理解成环境限制。
    }
  }, [addFiles]);

  useEffect(() => {
    const onPaste = (e: ClipboardEvent) => {
      const files = Array.from(e.clipboardData?.files ?? []);
      if (files.length) void addFiles(files);
    };
    window.addEventListener("paste", onPaste);
    return () => window.removeEventListener("paste", onPaste);
  }, [addFiles]);

  const move = (index: number, delta: number) => {
    const next = [...items];
    const target = index + delta;
    if (target < 0 || target >= next.length) return;
    [next[index], next[target]] = [next[target], next[index]];
    onChange(renumber(next));
  };

  const remove = (index: number) =>
    onChange(renumber(items.filter((_, i) => i !== index)));

  return (
    <div
      onDragEnter={(e) => {
        e.preventDefault();
        dragDepth.current += 1;
      }}
      onDragOver={(e) => e.preventDefault()}
      onDragLeave={() => {
        dragDepth.current -= 1;
      }}
      onDrop={(e) => {
        e.preventDefault();
        dragDepth.current = 0;
        if (e.dataTransfer?.files?.length) void addFiles(e.dataTransfer.files);
      }}
      style={{
        border: "1px dashed var(--ic-border)",
        borderRadius: 8,
        padding: 8,
      }}
    >
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 6,
          marginBottom: 6,
        }}
      >
        <span className="ic-dim" style={{ fontSize: 12 }}>
          {t("workbench.references")} {items.length}/{max}
        </span>
        <div style={{ flex: 1 }} />
        <button
          className="ic-btn"
          style={{ fontSize: 11 }}
          onClick={() => fileRef.current?.click()}
        >
          {t("common.upload")}
        </button>
        <button
          className="ic-btn"
          style={{ fontSize: 11 }}
          onClick={() => void pasteFromClipboard()}
        >
          {t("workbench.pasteImage")}
        </button>
        <input
          ref={fileRef}
          type="file"
          accept="image/*"
          multiple
          hidden
          onChange={(e) => {
            if (e.target.files) void addFiles(e.target.files);
            e.target.value = "";
          }}
        />
      </div>

      {items.length === 0 ? (
        <p className="ic-dim" style={{ fontSize: 11, margin: 0 }}>
          {t("workbench.dropHint")}
        </p>
      ) : (
        <div style={{ display: "flex", gap: 6, flexWrap: "wrap" }}>
          {items.map((item, index) => (
            <div
              key={item.id}
              style={{
                position: "relative",
                width: 72,
                height: 72,
                borderRadius: 6,
                overflow: "hidden",
              }}
            >
              <img
                src={api.assetThumbUrl(item.assetId, workspaceId, 144)}
                alt={item.name}
                style={{ width: "100%", height: "100%", objectFit: "cover" }}
              />
              {/* 编号角标：必须等于提交顺序 */}
              <span
                className="ic-badge"
                style={{
                  position: "absolute",
                  left: 2,
                  top: 2,
                  fontSize: 10,
                  background: "rgba(0,0,0,.6)",
                  color: "#fff",
                }}
                title={item.name}
              >
                {index + 1}
              </span>
              <div
                style={{
                  position: "absolute",
                  right: 2,
                  bottom: 2,
                  display: "flex",
                  gap: 2,
                }}
              >
                <button
                  className="ic-btn ic-btn--ghost"
                  style={{ fontSize: 9, padding: "0 3px" }}
                  title={t("workbench.moveLeft")}
                  onClick={() => move(index, -1)}
                >
                  ‹
                </button>
                <button
                  className="ic-btn ic-btn--ghost"
                  style={{ fontSize: 9, padding: "0 3px" }}
                  title={t("workbench.moveRight")}
                  onClick={() => move(index, 1)}
                >
                  ›
                </button>
                <button
                  className="ic-btn ic-btn--danger"
                  style={{ fontSize: 9, padding: "0 3px" }}
                  title={t("common.delete")}
                  onClick={() => remove(index)}
                >
                  ✕
                </button>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

/**
 * 重编号：order 必须与数组下标一致。
 *
 * 单独抽成函数是为了能被单测穷举——「编号错位」这类缺陷在界面上
 * 只表现为「图片2 变成了图片3」，肉眼很难发现。见 __tests__/reference.test.ts。
 */
export function renumber(items: ReferenceItem[]): ReferenceItem[] {
  return items.map((item, index) => ({ ...item, order: index }));
}
