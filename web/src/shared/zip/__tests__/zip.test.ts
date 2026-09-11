import { describe, expect, it } from "vitest";
import { ZIP_LIMITS, crc32, isSafeEntryName, readZip, writeZip } from "../zip";

/**
 * ZIP 读写测试。
 *
 * 重点不是「能压能解」，而是两条安全边界与一条一致性要求：
 *   - zip slip：条目名含 `..` / 绝对路径必须被拒（历史漏洞，可用于覆盖任意文件）；
 *   - zip bomb：解压总量受限；
 *   - 往返一致：同一内容两次导出字节完全相同（11.3 的验收方式）。
 */
const enc = (s: string) => new TextEncoder().encode(s);
const dec = (b: Uint8Array) => new TextDecoder().decode(b);

describe("CRC32", () => {
  it("与标准测试向量一致", () => {
    expect(crc32(enc("123456789"))).toBe(0xcbf43926);
    expect(crc32(new Uint8Array(0))).toBe(0);
  });
});

describe("写入与读取往返", () => {
  it("单个文本文件往返一致", () => {
    const zip = writeZip([{ name: "a.txt", data: enc("hello 世界") }]);
    const { entries } = readZip(zip);
    expect(entries.size).toBe(1);
    expect(dec(entries.get("a.txt")!)).toBe("hello 世界");
  });

  it("多文件与二进制内容往返一致", () => {
    const binary = new Uint8Array(256);
    for (let i = 0; i < 256; i += 1) binary[i] = i;
    const zip = writeZip([
      { name: "a.txt", data: enc("a") },
      { name: "dir/b.bin", data: binary },
      { name: "dir/c.json", data: enc('{"x":1}') },
    ]);
    const { entries } = readZip(zip);
    expect(entries.size).toBe(3);
    expect(entries.get("dir/b.bin")).toEqual(binary);
    expect(dec(entries.get("dir/c.json")!)).toBe('{"x":1}');
  });

  it("相同输入产生完全相同的字节", () => {
    const a = writeZip([{ name: "x.txt", data: enc("same") }]);
    const b = writeZip([{ name: "x.txt", data: enc("same") }]);
    expect(a).toEqual(b);
  });

  it("空 zip 可读且条目数为 0", () => {
    const { entries } = readZip(writeZip([]));
    expect(entries.size).toBe(0);
  });

  it("条目顺序稳定", () => {
    const zip = writeZip([
      { name: "1.txt", data: enc("1") },
      { name: "2.txt", data: enc("2") },
      { name: "3.txt", data: enc("3") },
    ]);
    expect([...readZip(zip).entries.keys()]).toEqual([
      "1.txt",
      "2.txt",
      "3.txt",
    ]);
  });

  it("空内容文件往返一致（size 为 0 的边界）", () => {
    const zip = writeZip([{ name: "empty.txt", data: new Uint8Array(0) }]);
    const { entries } = readZip(zip);
    expect(entries.get("empty.txt")!.length).toBe(0);
  });
});

describe("zip slip 防护", () => {
  it("拒绝 .. 路径段", () => {
    expect(isSafeEntryName("../etc/passwd")).toBe(false);
    expect(isSafeEntryName("a/../../etc/passwd")).toBe(false);
    expect(isSafeEntryName("..")).toBe(false);
  });

  it("拒绝绝对路径与盘符", () => {
    expect(isSafeEntryName("/etc/passwd")).toBe(false);
    expect(isSafeEntryName("C:/Windows/system32")).toBe(false);
    expect(isSafeEntryName("\\\\server\\share")).toBe(false);
    expect(isSafeEntryName("a\\b")).toBe(false);
  });

  it("拒绝超长名字", () => {
    expect(isSafeEntryName("x".repeat(ZIP_LIMITS.maxNameLength + 1))).toBe(
      false,
    );
  });

  it("接受正常相对路径", () => {
    expect(isSafeEntryName("a.txt")).toBe(true);
    expect(isSafeEntryName("dir/sub/file.png")).toBe(true);
    expect(isSafeEntryName("带空格 与中文/文件.txt")).toBe(true);
    expect(isSafeEntryName("a..b")).toBe(true); // 名字含 .. 但不是路径段
  });

  it("读取时拒绝不安全条目并上报原因（不静默丢弃）", () => {
    const zip = writeZip([
      { name: "../evil.sh", data: enc("rm -rf /") },
      { name: "good.txt", data: enc("ok") },
    ]);
    const { entries, rejected } = readZip(zip);
    expect(entries.has("../evil.sh")).toBe(false);
    expect(entries.has("good.txt")).toBe(true);
    expect(rejected).toHaveLength(1);
    expect(rejected[0]!.reason).toBe("unsafe_path");
    expect(rejected[0]!.name).toBe("../evil.sh");
  });
});

describe("健壮性", () => {
  it("非 zip 数据抛出明确错误", () => {
    expect(() => readZip(enc("this is not a zip file at all"))).toThrow(
      /invalid_zip/,
    );
  });

  it("截断的 zip 抛出明确错误", () => {
    const zip = writeZip([{ name: "a.txt", data: enc("hello") }]);
    expect(() => readZip(zip.subarray(0, zip.length - 10))).toThrow();
  });

  it("空文件名被拒绝", () => {
    expect(isSafeEntryName("")).toBe(false);
  });
});

describe("体积上限", () => {
  it("上限是有限值（防止退化成「无上限」）", () => {
    expect(ZIP_LIMITS.maxEntries).toBeGreaterThan(0);
    expect(ZIP_LIMITS.maxEntries).toBeLessThanOrEqual(10000);
    expect(ZIP_LIMITS.maxTotalBytes).toBeGreaterThan(0);
    expect(ZIP_LIMITS.maxTotalBytes).toBeLessThanOrEqual(
      2 * 1024 * 1024 * 1024,
    );
  });
});
