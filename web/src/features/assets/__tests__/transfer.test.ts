import { describe, expect, it } from "vitest";
import { ASSET_PACKAGE_APP, sanitizeFileName } from "../transfer";
import { readZip, writeZip } from "@/shared/zip/zip";

/**
 * 资产包格式与文件名清洗。
 *
 * 这里不 mock fetch 测完整导入导出（那会变成测自己的 mock），
 * 而是验证两条最容易出错、且与网络无关的逻辑：
 *   - 文件名清洗（中文/空格/书名号必须保留，路径字符必须替换）；
 *   - 清单结构能被自己读回来（往返一致）。
 */
describe("文件名清洗", () => {
  it("保留中文、空格、书名号（原项目 v0.18.0 修过这个 bug）", () => {
    expect(sanitizeFileName("《我的图》 第二版.png")).toBe(
      "《我的图》 第二版.png",
    );
    expect(sanitizeFileName("中文 名字.png")).toBe("中文 名字.png");
  });

  it("替换路径分隔符与非法字符", () => {
    expect(sanitizeFileName("a/b\\c.txt")).toBe("a_b_c.txt");
    expect(sanitizeFileName("a:b*c?.txt")).toBe("a_b_c_.txt");
    expect(sanitizeFileName('a|b"c<d>e.txt')).toBe("a_b_c_d_e.txt");
  });

  it("移除控制字符", () => {
    expect(sanitizeFileName("a\u0000b\u001fc.txt")).toBe("abc.txt");
  });

  it("空名字回落到 untitled", () => {
    expect(sanitizeFileName("")).toBe("untitled");
    expect(sanitizeFileName("   ")).toBe("untitled");
  });

  it("超长名字被截断", () => {
    const long = "x".repeat(300) + ".png";
    expect(sanitizeFileName(long).length).toBeLessThanOrEqual(120);
  });
});

describe("资产包清单往返", () => {
  it("清单可以写入 zip 并原样读回", () => {
    const manifest = {
      app: ASSET_PACKAGE_APP,
      version: 1,
      exportedAt: "2026-01-01T00:00:00.000Z",
      assets: [
        {
          id: "as_1",
          name: "图片.png",
          kind: "image",
          mime: "image/png",
          size: 3,
          hash: "abc",
          path: "files/as_1-图片.png",
        },
      ],
    };
    const zip = writeZip([
      {
        name: "assets.json",
        data: new TextEncoder().encode(JSON.stringify(manifest)),
      },
      { name: "files/as_1-图片.png", data: new Uint8Array([1, 2, 3]) },
    ]);
    const { entries } = readZip(zip);
    const back = JSON.parse(
      new TextDecoder().decode(entries.get("assets.json")!),
    );
    expect(back.app).toBe(ASSET_PACKAGE_APP);
    expect(back.assets).toHaveLength(1);
    expect(back.assets[0].name).toBe("图片.png");
    expect(entries.get("files/as_1-图片.png")).toEqual(
      new Uint8Array([1, 2, 3]),
    );
  });

  it("只写清单不写文件时导入方应能识别 missing", () => {
    const manifest = {
      app: ASSET_PACKAGE_APP,
      version: 1,
      exportedAt: "2026-01-01T00:00:00.000Z",
      assets: [
        {
          id: "as_1",
          name: "a.png",
          kind: "image",
          mime: "image/png",
          size: 1,
          hash: "h",
          path: "files/as_1-a.png",
        },
      ],
    };
    const zip = writeZip([
      {
        name: "assets.json",
        data: new TextEncoder().encode(JSON.stringify(manifest)),
      },
    ]);
    const { entries } = readZip(zip);
    const back = JSON.parse(
      new TextDecoder().decode(entries.get("assets.json")!),
    );
    expect(entries.has(back.assets[0].path)).toBe(false);
  });
});
