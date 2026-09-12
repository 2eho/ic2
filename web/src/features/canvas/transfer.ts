import { api } from "@/shared/api";
import { readZip, writeZip, type ZipEntry } from "@/shared/zip/zip";

/**
 * 画布包的导出与导入（11.3）。
 *
 * 格式沿用「一个 zip + 一份清单」的形状，但清单是我们自己的 schema
 * （`projects.json` + `files/`），而不是原项目 version 3 的字节级复刻：
 *
 *   - 原项目把画布 JSON 与图片 dataURL 混在一起，一个大画布导出后是几百 MB
 *     的单个 JSON，任何编辑器都打不开，也无法只取其中一张图；
 *   - 这里把二进制内容拆到 `files/`，清单只存**引用**（assetId + 路径 + hash）。
 *
 * 兼容性：导入时同时接受原项目 version 3 包（`kind: "safe-canvas-export"` /
 * 无 kind 但有 `projects` 数组）。老用户手里已经有包，不接受会让他们的数据
 * 变成孤岛——这与素材包导入器是同一条纪律（见 features/assets/transfer.ts）。
 *
 * 「往返一致」是验收口径：导出 → 导入 → 再导出，两份清单必须相等
 * （时间戳与随机 id 除外）。因此导出格式里不能有随内容变化而变化的字段。
 */

export const CANVAS_PACKAGE_KIND = "ic-canvas-export";
export const CANVAS_PACKAGE_VERSION = 4;

/** 原项目 version 3 的结构标识（只读兼容，不产出）。 */
const LEGACY_KINDS = ["safe-canvas-export", "infinite-canvas-export"];

export interface CanvasManifestAsset {
  assetId: string;
  name: string;
  mime: string;
  /** zip 内的相对路径。 */
  path: string;
  hash: string;
  size: number;
}

export interface CanvasManifest {
  kind: string;
  version: number;
  exportedAt: string;
  /** 画布文档（与 REST /export 返回的结构一致）。 */
  canvas: Record<string, unknown>;
  /** 画布引用的资产清单。 */
  assets: CanvasManifestAsset[];
  /** 助手会话只读快照（2.11）。缺失时表示不含会话。 */
  agentSessions?: unknown[];
  /** 导出时被跳过的内容（必须显式上报，否则用户以为数据全在）。 */
  skipped?: Array<{ id: string; reason: string }>;
}

export interface CanvasExportResult {
  blob: Blob;
  manifest: CanvasManifest;
  skipped: Array<{ id: string; reason: string }>;
}

/** 从画布文档里收集所有引用的 assetId（节点 spec/result 与 run 输出）。 */
export function collectAssetIds(canvas: Record<string, unknown>): string[] {
  const ids = new Set<string>();
  const add = (v: unknown) => {
    if (typeof v === "string" && /^as_[A-Za-z0-9_-]+$/.test(v)) ids.add(v);
  };
  const nodes = (canvas.nodes ?? {}) as Record<string, Record<string, unknown>>;
  const list = Array.isArray(nodes) ? nodes : Object.values(nodes);
  for (const node of list) {
    const spec = (node?.spec ?? {}) as Record<string, unknown>;
    add(spec.assetId);
    add(spec.sourceAssetId);
    const result = (node?.result ?? {}) as Record<string, unknown>;
    const variants = (result.variants ?? []) as Array<Record<string, unknown>>;
    for (const v of variants) add(v.assetId);
  }
  return [...ids];
}

/**
 * 导出画布为 zip。
 *
 * 资产取字节走 raw 接口。超过总量预算时**跳过并上报**而不是继续：
 * 继续会在浏览器里触发 OOM，用户只会看到白屏，而白屏不会告诉他原因是
 * 「这张画布引用了 2GB 素材」。
 */
export async function exportCanvas(
  canvasId: string,
  workspaceId: string,
  opts: { maxTotalBytes?: number; onProgress?: (done: number, total: number) => void } = {},
): Promise<CanvasExportResult> {
  const raw = (await api.exportCanvas(canvasId)) as Record<string, unknown>;
  // REST 的 /export 返回 { version, kind, canvas, agentSessions? }；
  // 兼容「直接返回画布文档」的形态，避免后端结构调整时前端静默拿到空包。
  const canvas = ((raw.canvas ?? raw) as Record<string, unknown>) ?? {};
  const agentSessions = raw.agentSessions as unknown[] | undefined;

  const maxTotal = opts.maxTotalBytes ?? 512 * 1024 * 1024;
  const assetIds = collectAssetIds(canvas);
  const entries: ZipEntry[] = [];
  const assets: CanvasManifestAsset[] = [];
  const skipped: Array<{ id: string; reason: string }> = [];
  let total = 0;

  for (const [index, assetId] of assetIds.entries()) {
    try {
      const meta = await api.getAsset(assetId, workspaceId);
      if (total + meta.size > maxTotal) {
        skipped.push({ id: assetId, reason: "over_size_budget" });
        continue;
      }
      const res = await fetch(api.assetRawUrl(assetId, workspaceId));
      if (!res.ok) {
        skipped.push({ id: assetId, reason: `http_${res.status}` });
        continue;
      }
      const bytes = new Uint8Array(await res.arrayBuffer());
      const path = `files/${assetId}.${extFor(meta.mime, meta.name)}`;
      entries.push({ name: path, data: bytes });
      assets.push({
        assetId,
        name: meta.name ?? assetId,
        mime: meta.mime,
        path,
        hash: meta.hash,
        size: bytes.length,
      });
      total += bytes.length;
      opts.onProgress?.(index + 1, assetIds.length);
    } catch (e) {
      skipped.push({ id: assetId, reason: (e as Error).message.slice(0, 60) });
    }
  }

  const manifest: CanvasManifest = {
    kind: CANVAS_PACKAGE_KIND,
    version: CANVAS_PACKAGE_VERSION,
    exportedAt: new Date().toISOString(),
    canvas,
    assets,
  };
  // 会话快照是**可选**的：没有 Agent 会话时省略该字段而不是写空数组，
  // 这样「往返一致」的比较不会被一个空数组的差异破坏。
  if (agentSessions && agentSessions.length > 0) {
    manifest.agentSessions = agentSessions;
  }

  // 清单放最后：解压工具里读到的顺序是 files 在前、清单在后，
  // 用户能先看到内容再看到索引（没有功能意义，但对人工排查更友好）。
  entries.push({
    name: "projects.json",
    data: new TextEncoder().encode(JSON.stringify(manifest, null, 2)),
  });

  const zipBytes = writeZip(entries);
  const blob = new Blob([zipBytes.buffer.slice(0) as ArrayBuffer], {
    type: "application/zip",
  });
  return { blob, manifest, skipped };
}

export interface CanvasImportResult {
  canvasId: string;
  assetCount: number;
  /** 清单声明但包里缺失的文件（说明包不完整）。 */
  missing: string[];
  /** 因格式或体积被跳过的条目。 */
  skipped: Array<{ name: string; reason: string }>;
  /** 导入的包是否为原项目格式（需要在界面上说明「这是老格式」）。 */
  legacy: boolean;
}

/**
 * 导入画布包。
 *
 * 幂等：服务端按 `sourceProjectId + contentHash` 去重（handlers_graph.go），
 * 因此重复导入同一个包不会产生重复画布 —— 这是「导入两次」这一常见误操作的兜底。
 *
 * 缺失与被跳过都必须显式上报：静默成功会让用户以为数据全在，
 * 这是数据迁移里最危险的一类错觉。
 */
export async function importCanvasPackage(
  projectId: string,
  workspaceId: string,
  file: File,
): Promise<CanvasImportResult> {
  const bytes = new Uint8Array(await file.arrayBuffer());
  const { entries, rejected } = readZip(bytes);
  const skipped = rejected.map((r) => ({ name: r.name, reason: r.reason }));

  const manifestRaw = entries.get("projects.json") ?? entries.get("manifest.json");
  if (!manifestRaw) {
    throw Object.assign(new Error("missing_manifest"), { code: "invalid_request" });
  }
  const manifest = JSON.parse(new TextDecoder().decode(manifestRaw)) as CanvasManifest & {
    kind?: string;
    projects?: unknown[];
  };

  const legacy =
    (manifest.kind !== undefined && LEGACY_KINDS.includes(manifest.kind)) ||
    (manifest.kind === undefined && Array.isArray(manifest.projects)) ||
    (manifest.version !== undefined && manifest.version < CANVAS_PACKAGE_VERSION);

  // 先上传资产（画布文档里的 assetId 指向它们）。
  // 必须在上传画布**之前**做完：否则画布先入库、资产后失败，
  // 用户拿到一个「节点全在、图全是空的」画布，且没有任何报错。
  const missing: string[] = [];
  let assetCount = 0;
  for (const item of manifest.assets ?? []) {
    const data = entries.get(item.path);
    if (!data) {
      missing.push(item.path);
      continue;
    }
    try {
      const blob = new Blob([data.buffer.slice(0) as ArrayBuffer], {
        type: item.mime || "application/octet-stream",
      });
      await api.uploadAsset(workspaceId, blob, item.name || item.assetId);
      assetCount += 1;
    } catch (e) {
      skipped.push({
        name: item.name || item.assetId,
        reason: (e as { code?: string }).code ?? "upload_failed",
      });
    }
  }

  const payload = legacy
    ? convertLegacy(manifest as unknown as Record<string, unknown>)
    : manifest;
  const created = await api.importCanvas(projectId, {
    name: String((payload.canvas?.title as string) ?? undefined) || undefined,
    nodes: (payload.canvas?.nodes ?? []) as never[],
    edges: (payload.canvas?.edges ?? []) as never[],
    viewport: payload.canvas?.viewport as never,
    settings: payload.canvas?.settings as never,
  } as never);
  const createdAny = created as unknown as { canvasId?: string; id?: string };
  return {
    canvasId: createdAny.canvasId ?? createdAny.id ?? "",
    assetCount,
    missing,
    skipped,
    legacy,
  };
}

/** convertLegacy 把原项目 version 3 的包转成我们的 payload 形状。 */
export function convertLegacy(manifest: Record<string, unknown>): {
  canvas: Record<string, unknown>;
} {
  // 原项目的项目数组里第一个即目标画布（一次导出通常只含一个）。
  const projects = (manifest.projects ?? []) as Array<Record<string, unknown>>;
  const first = projects[0] ?? {};
  const canvases = (first.canvases ?? []) as Array<Record<string, unknown>>;
  const canvas = canvases[0] ?? manifest.canvas ?? {};
  return { canvas: canvas as Record<string, unknown> };
}

/**
 * extFor 按 MIME 与文件名推断扩展名（只影响包内路径的可读性）。
 *
 * 不追求完备：拿不准时用 `bin`，因为**猜错的扩展名比没有扩展名更糟**——
 * 用户会双击一个 `png` 却发现打不开。
 */
export function extFor(mime: string, name?: string): string {
  if (name && /\.[a-z0-9]{1,5}$/i.test(name)) return name.split(".").pop()!.toLowerCase();
  switch (mime) {
    case "image/png":
      return "png";
    case "image/jpeg":
      return "jpg";
    case "image/webp":
      return "webp";
    case "image/gif":
      return "gif";
    case "video/mp4":
      return "mp4";
    case "audio/mpeg":
      return "mp3";
    case "text/plain":
      return "txt";
  }
  return "bin";
}
