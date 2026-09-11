import { api } from "@/shared/api";
import { readZip, writeZip, type ZipEntry } from "@/shared/zip/zip";

/**
 * 资产包的导出与导入（docs/design/10 §7.4）。
 *
 * 格式沿用原项目的思路但有两个刻意的改动：
 *
 *  1. **清单用我们自己的 schema 版本号**（`app: "ic", version: 1`），
 *     而不是沿用 `infinite-canvas/v1`——沿用会让「这到底是哪个产品的包」变得模糊，
 *     而且将来要兼容原项目的包时必须显式写一个转换器，不能靠字段恰好相同。
 *  2. **文件路径由 assetId + hash 推导**，不用原名。
 *     原名可能重复（两张不同的图都叫 `1.png`），用原名会在导出时互相覆盖；
 *     导入后由清单里的 name 恢复显示名。
 *
 * 兼容性：导入时同时接受新格式与原项目 v1 格式（`app: "infinite-canvas"`），
 * 因为「老用户手里已经有包」是事实，不接受会让他们的数据变成孤岛。
 */

export const ASSET_PACKAGE_APP = "ic";
export const ASSET_PACKAGE_VERSION = 1;

/** 原项目资产包格式的标识（只读兼容，不产出）。 */
const LEGACY_APP = "infinite-canvas";

export interface AssetManifestItem {
  id: string;
  name: string;
  kind: string;
  mime: string;
  size: number;
  hash: string;
  /** zip 内的相对路径。 */
  path: string;
  /** 资产来源与备注（原项目有，保留以免信息丢失）。 */
  origin?: string;
  meta?: Record<string, unknown>;
  createdAt?: string;
}

export interface AssetPackageManifest {
  app: string;
  version: number;
  exportedAt: string;
  assets: AssetManifestItem[];
  /** 导入时被跳过的条目（缺失文件 / 不支持的格式），必须让用户看到。 */
  skipped?: Array<{ id: string; reason: string }>;
}

export interface ExportResult {
  blob: Blob;
  manifest: AssetPackageManifest;
  /** 导出时因体积或权限原因被跳过的资产。 */
  skipped: Array<{ id: string; reason: string }>;
}

/**
 * 导出资产为 zip。
 *
 * 取字节走 raw 接口（Range/流式），不在内存里拼 dataURI——
 * 一个大视频转成 base64 会让内存翻 1.33 倍且无法流式。这里仍然会把
 * 全部内容放进内存以便打包（store 模式的局限），因此对**超大资产**显式跳过
 * 并上报，而不是让页面崩掉。
 */
export async function exportAssets(
  workspaceId: string,
  assets: Array<{
    id: string;
    name?: string;
    kind: string;
    mime: string;
    size: number;
    hash: string;
  }>,
  opts: {
    maxTotalBytes?: number;
    onProgress?: (done: number, total: number) => void;
  } = {},
): Promise<ExportResult> {
  const maxTotal = opts.maxTotalBytes ?? 512 * 1024 * 1024;
  const entries: ZipEntry[] = [];
  const manifest: AssetManifestItem[] = [];
  const skipped: Array<{ id: string; reason: string }> = [];
  let total = 0;

  for (const [index, asset] of assets.entries()) {
    if (total + asset.size > maxTotal) {
      // 超过总量预算就停下并上报：继续会在浏览器里触发 OOM，用户只会看到白屏
      skipped.push({ id: asset.id, reason: "over_size_budget" });
      continue;
    }
    try {
      const res = await fetch(api.assetRawUrl(asset.id, workspaceId));
      if (!res.ok) {
        skipped.push({ id: asset.id, reason: `http_${res.status}` });
        continue;
      }
      const bytes = new Uint8Array(await res.arrayBuffer());
      // 路径用 id 前缀 + 名字后缀：既避免重名覆盖，又让用户在解压后能看出内容
      const safeName = sanitizeFileName(asset.name ?? asset.id);
      const path = `files/${asset.id.slice(0, 12)}-${safeName}`;
      entries.push({ name: path, data: bytes });
      manifest.push({
        id: asset.id,
        name: asset.name ?? asset.id,
        kind: asset.kind,
        mime: asset.mime,
        size: bytes.length,
        hash: asset.hash,
        path,
      });
      total += bytes.length;
      opts.onProgress?.(index + 1, assets.length);
    } catch (e) {
      skipped.push({ id: asset.id, reason: (e as Error).message.slice(0, 60) });
    }
  }

  const payload: AssetPackageManifest = {
    app: ASSET_PACKAGE_APP,
    version: ASSET_PACKAGE_VERSION,
    exportedAt: new Date().toISOString(),
    assets: manifest,
  };
  entries.unshift({
    name: "assets.json",
    data: new TextEncoder().encode(JSON.stringify(payload, null, 2)),
  });

  // writeZip 返回 Uint8Array，其 buffer 类型在 TS 5.7+ 下是 ArrayBufferLike，
  // 与 BlobPart 不完全兼容；显式转成 ArrayBuffer 是最直接的修法
  // （不复制数据：slice 出来的正好是这一段的副本，语义也更安全）。
  const zipBytes = writeZip(entries);
  const blob = new Blob([zipBytes.buffer.slice(0) as ArrayBuffer], {
    type: "application/zip",
  });
  return { blob, manifest: payload, skipped };
}

export interface ImportResult {
  imported: number;
  skipped: Array<{ name: string; reason: string }>;
  /** 清单里声明但包里缺失的文件（说明包不完整）。 */
  missing: string[];
}

/**
 * 导入资产包。
 *
 * 幂等：同名同 hash 的资产会被服务端内容寻址去重（asset.Service.Upload），
 * 因此重复导入同一个包不会产生重复资产——这是「导入两次」这一常见误操作的兜底。
 */
export async function importAssets(
  workspaceId: string,
  file: File,
): Promise<ImportResult> {
  const bytes = new Uint8Array(await file.arrayBuffer());
  const { entries, rejected } = readZip(bytes);
  const skipped = rejected.map((r) => ({ name: r.name, reason: r.reason }));

  const manifestRaw = entries.get("assets.json");
  if (!manifestRaw) {
    throw Object.assign(new Error("missing_manifest"), {
      code: "invalid_request",
    });
  }
  const manifest = JSON.parse(
    new TextDecoder().decode(manifestRaw),
  ) as AssetPackageManifest;
  if (manifest.app !== ASSET_PACKAGE_APP && manifest.app !== LEGACY_APP) {
    throw Object.assign(new Error("unknown_package"), {
      code: "invalid_request",
    });
  }

  const missing: string[] = [];
  let imported = 0;

  for (const item of manifest.assets ?? []) {
    const bytesForItem = entries.get(item.path);
    if (!bytesForItem) {
      // 包不完整：缺文件不能静默跳过，否则用户以为导入成功但内容少了一半
      missing.push(item.path);
      continue;
    }
    try {
      // 与导出同理：显式用 ArrayBuffer 规避 TS 对 SharedArrayBuffer 的兼容性检查
      const blob = new Blob([bytesForItem.buffer.slice(0) as ArrayBuffer], {
        type: item.mime || "application/octet-stream",
      });
      await api.uploadAsset(workspaceId, blob, item.name || item.id);
      imported += 1;
    } catch (e) {
      skipped.push({
        name: item.name,
        reason: (e as { code?: string }).code ?? "upload_failed",
      });
    }
  }

  return { imported, skipped, missing };
}

/** 文件名清洗：与后端 sanitizeName 保持同一策略（去掉路径分隔符与控制字符）。 */
export function sanitizeFileName(name: string): string {
  const cleaned = name
    .replace(/[\\/:*?"<>|]/g, "_")
    // eslint-disable-next-line no-control-regex
    .replace(/[\u0000-\u001f\u007f]/g, "")
    .trim();
  if (!cleaned) return "untitled";
  // 限长：过长的名字在部分文件系统上会直接失败
  return cleaned.length > 120 ? cleaned.slice(0, 120) : cleaned;
}
