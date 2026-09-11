/**
 * 极简 ZIP 读写（store 模式，无压缩）。
 *
 * 为什么不引 fflate / jszip：
 *   1. 体积——这个功能整体只值几 KB，引一个 20KB+ 的库不划算；
 *   2. 可控——导出格式是我们的契约（原项目 v3 格式），需要能逐字节验证；
 *   3. 安全——解压外部 zip 是经典攻击面（zip slip / zip bomb），
 *      自己实现可以内置这两道防护，而不是依赖库的默认行为。
 *
 * **不支持压缩**是刻意的取舍：资产以图片/视频为主，本身已是压缩格式，
 * 再 deflate 一次通常只能省 1–2%，却要多实现一个 inflate（几百行 + 攻击面）。
 * 需要压缩时由用户在导出后自行处理。
 *
 * 安全防护（两者都有单测）：
 *   - zip slip：条目名含 `..` 或绝对路径一律拒绝，且解压方只能按名字查表，
 *     不直接落盘（本模块返回 Map，由调用方决定怎么用）；
 *   - zip bomb：限制解压后总大小与条目数（见 LIMITS）。
 */

const LOCAL_HEADER_SIG = 0x04034b50;
const CENTRAL_HEADER_SIG = 0x02014b50;
const EOCD_SIG = 0x06054b50;

/** 解压上限：条目数与总字节数。 */
export const ZIP_LIMITS = {
  maxEntries: 2000,
  maxTotalBytes: 512 * 1024 * 1024,
  maxNameLength: 512,
};

export interface ZipEntry {
  name: string;
  data: Uint8Array;
}

/**
 * CRC32（ZIP 必需）。用查表法，表在模块加载时生成一次。
 */
const CRC_TABLE = (() => {
  const table = new Uint32Array(256);
  for (let i = 0; i < 256; i += 1) {
    let c = i;
    for (let k = 0; k < 8; k += 1) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    table[i] = c >>> 0;
  }
  return table;
})();

export function crc32(data: Uint8Array): number {
  let c = 0xffffffff;
  for (let i = 0; i < data.length; i += 1)
    c = CRC_TABLE[(c ^ data[i]) & 0xff] ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
}

/**
 * 生成 zip 字节流。
 *
 * 时间戳统一用 1980-01-01（ZIP 的最小可表示时间）：
 *   用真实时间会让**同样的内容每次导出字节不同**，从而无法做
 *   「导出→导入→再导出」的往返一致性断言（那是 11.3 的验收方式）。
 */
export function writeZip(entries: ZipEntry[]): Uint8Array {
  const chunks: Uint8Array[] = [];
  const central: Uint8Array[] = [];
  let offset = 0;

  for (const entry of entries) {
    const nameBytes = new TextEncoder().encode(entry.name);
    const crc = crc32(entry.data);
    const size = entry.data.length;

    const local = new Uint8Array(30 + nameBytes.length);
    const lv = new DataView(local.buffer);
    lv.setUint32(0, LOCAL_HEADER_SIG, true);
    lv.setUint16(4, 20, true); // version needed
    lv.setUint16(6, 0, true); // flags
    lv.setUint16(8, 0, true); // method = store
    lv.setUint16(10, 0, true); // time
    lv.setUint16(12, 0x21, true); // date = 1980-01-01
    lv.setUint32(14, crc, true);
    lv.setUint32(18, size, true);
    lv.setUint32(22, size, true);
    lv.setUint16(26, nameBytes.length, true);
    lv.setUint16(28, 0, true);
    local.set(nameBytes, 30);

    chunks.push(local, entry.data);

    const cd = new Uint8Array(46 + nameBytes.length);
    const cv = new DataView(cd.buffer);
    cv.setUint32(0, CENTRAL_HEADER_SIG, true);
    cv.setUint16(4, 20, true);
    cv.setUint16(6, 20, true);
    cv.setUint16(8, 0, true);
    cv.setUint16(10, 0, true);
    cv.setUint16(12, 0, true);
    cv.setUint16(14, 0x21, true);
    cv.setUint32(16, crc, true);
    cv.setUint32(20, size, true);
    cv.setUint32(24, size, true);
    cv.setUint16(28, nameBytes.length, true);
    cv.setUint32(42, offset, true);
    cd.set(nameBytes, 46);
    central.push(cd);

    offset += local.length + size;
  }

  const centralSize = central.reduce((s, c) => s + c.length, 0);
  const eocd = new Uint8Array(22);
  const ev = new DataView(eocd.buffer);
  ev.setUint32(0, EOCD_SIG, true);
  ev.setUint16(8, entries.length, true);
  ev.setUint16(10, entries.length, true);
  ev.setUint32(12, centralSize, true);
  ev.setUint32(16, offset, true);

  const total =
    chunks.reduce((s, c) => s + c.length, 0) + centralSize + eocd.length;
  const out = new Uint8Array(total);
  let pos = 0;
  for (const c of chunks) {
    out.set(c, pos);
    pos += c.length;
  }
  for (const c of central) {
    out.set(c, pos);
    pos += c.length;
  }
  out.set(eocd, pos);
  return out;
}

/** 条目名校验：拒绝 zip slip 与超长名。 */
export function isSafeEntryName(name: string): boolean {
  if (!name || name.length > ZIP_LIMITS.maxNameLength) return false;
  // 绝对路径（含 Windows 盘符与 UNC）
  if (name.startsWith("/") || name.startsWith("\\")) return false;
  if (/^[a-zA-Z]:/.test(name)) return false;
  if (name.includes("\\")) return false;
  // 任一路径段为 .. 即拒绝（不做「规范化后允许」——那容易被绕过）
  if (name.split("/").some((seg) => seg === "..")) return false;
  return true;
}

export interface ReadZipResult {
  entries: Map<string, Uint8Array>;
  /** 被拒绝的条目名与原因（必须上报给用户，不能静默丢弃）。 */
  rejected: Array<{ name: string; reason: string }>;
}

/** 读取 zip。条目名不安全时记入 rejected，不抛错（部分可用优于全不可用）。 */
export function readZip(bytes: Uint8Array): ReadZipResult {
  const entries = new Map<string, Uint8Array>();
  const rejected: Array<{ name: string; reason: string }> = [];
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);

  // 从尾部找 EOCD（可能有注释，因此向前搜索最多 64KB）
  let eocd = -1;
  const minEocd = Math.max(0, bytes.length - 22 - 0xffff);
  for (let i = bytes.length - 22; i >= minEocd; i -= 1) {
    if (view.getUint32(i, true) === EOCD_SIG) {
      eocd = i;
      break;
    }
  }
  if (eocd < 0) throw new Error("invalid_zip: 找不到 EOCD");

  const count = view.getUint16(eocd + 10, true);
  const centralOffset = view.getUint32(eocd + 16, true);
  if (count > ZIP_LIMITS.maxEntries) {
    throw new Error(
      `invalid_zip: 条目数 ${count} 超出上限 ${ZIP_LIMITS.maxEntries}`,
    );
  }

  let total = 0;
  let pos = centralOffset;
  for (let i = 0; i < count; i += 1) {
    if (
      pos + 46 > bytes.length ||
      view.getUint32(pos, true) !== CENTRAL_HEADER_SIG
    ) {
      throw new Error("invalid_zip: 中央目录损坏");
    }
    const method = view.getUint16(pos + 10, true);
    const size = view.getUint32(pos + 24, true);
    const nameLen = view.getUint16(pos + 28, true);
    const extraLen = view.getUint16(pos + 30, true);
    const commentLen = view.getUint16(pos + 32, true);
    const localOffset = view.getUint32(pos + 42, true);
    const name = new TextDecoder().decode(
      bytes.subarray(pos + 46, pos + 46 + nameLen),
    );
    pos += 46 + nameLen + extraLen + commentLen;

    if (method !== 0) {
      rejected.push({ name, reason: "unsupported_compression" });
      continue;
    }
    if (!isSafeEntryName(name)) {
      // zip slip：不入表，但记下来让调用方提示用户
      rejected.push({ name, reason: "unsafe_path" });
      continue;
    }
    total += size;
    if (total > ZIP_LIMITS.maxTotalBytes) {
      throw new Error(
        `invalid_zip: 解压总大小超出上限 ${ZIP_LIMITS.maxTotalBytes}`,
      );
    }
    // 本地头：定位真实数据起点（不能假设 localOffset + 30 + nameLen，要读实际长度）
    if (view.getUint32(localOffset, true) !== LOCAL_HEADER_SIG) {
      throw new Error("invalid_zip: 本地头损坏");
    }
    const localNameLen = view.getUint16(localOffset + 26, true);
    const localExtraLen = view.getUint16(localOffset + 28, true);
    const dataStart = localOffset + 30 + localNameLen + localExtraLen;
    entries.set(name, bytes.subarray(dataStart, dataStart + size));
  }

  return { entries, rejected };
}

/** 目录项（以 / 结尾）在业务上无意义，遍历时过滤掉。 */
export function isDirectoryEntry(name: string): boolean {
  return name.endsWith("/");
}
