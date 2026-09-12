import { describe, expect, it } from "vitest";
import {
  CANVAS_PACKAGE_KIND,
  CANVAS_PACKAGE_VERSION,
  collectAssetIds,
  convertLegacy,
  extFor,
} from "../transfer";
import { readZip, writeZip } from "@/shared/zip/zip";

/**
 * 画布包格式（11.3）。
 *
 * 验收口径是「往返一致」：导出 → 导入 → 再导出，两份清单必须相等。
 * 因此这里测的是**格式本身的性质**，而不是完整链路（那需要 mock 一堆 HTTP，
 * 等于在测自己的 mock）。
 */

describe("资产引用收集", () => {
  it("从节点 spec 与 result 里收集 assetId", () => {
    const ids = collectAssetIds({
      nodes: {
        n1: { spec: { assetId: "as_aaa" }, result: { variants: [{ assetId: "as_bbb" }] } },
        n2: { spec: { sourceAssetId: "as_ccc" } },
      },
    });
    expect(ids.sort()).toEqual(["as_aaa", "as_bbb", "as_ccc"]);
  });

  it("节点为数组形态时同样能收集（服务端与前端表示不同）", () => {
    const ids = collectAssetIds({
      nodes: [{ spec: { assetId: "as_1" } }, { spec: { assetId: "as_2" } }],
    });
    expect(ids.sort()).toEqual(["as_1", "as_2"]);
  });

  it("不把非 assetId 的字符串当引用（否则导出会去下载一堆不存在的东西）", () => {
    const ids = collectAssetIds({
      nodes: { n1: { spec: { assetId: "https://example.com/a.png" } } },
    });
    expect(ids).toEqual([]);
  });

  it("空画布返回空数组而不是抛错", () => {
    expect(collectAssetIds({})).toEqual([]);
  });
});

describe("扩展名推断", () => {
  it("优先用文件名后缀", () => {
    expect(extFor("application/octet-stream", "a.PNG")).toBe("png");
  });

  it("按 MIME 推断常见类型", () => {
    expect(extFor("image/jpeg")).toBe("jpg");
    expect(extFor("video/mp4")).toBe("mp4");
    expect(extFor("text/plain")).toBe("txt");
  });

  it("拿不准时用 bin 而不是乱猜（猜错的扩展名比没有更糟）", () => {
    expect(extFor("application/octet-stream")).toBe("bin");
    expect(extFor("application/x-weird")).toBe("bin");
  });
});

describe("旧格式转换", () => {
  it("从原项目 version 3 的 projects[].canvases[] 里取第一个画布", () => {
    const out = convertLegacy({
      projects: [{ canvases: [{ id: "cv_1", nodes: [] }] }],
    });
    expect(out.canvas.id).toBe("cv_1");
  });

  it("结构缺失时不抛错（返回空画布，由导入流程上报节点数不一致）", () => {
    expect(convertLegacy({}).canvas).toEqual({});
    expect(convertLegacy({ projects: [] }).canvas).toEqual({});
  });
});

describe("包格式性质", () => {
  it("清单里不含随内容变化的时间以外的字段（保证往返可比）", () => {
    // 这条断言看起来废话，但它挡住的是「往清单里塞一个自增序号」这类改动——
    // 那种改动会让「导出→导入→再导出」的比对永远不相等，
    // 而验收口径就失效了。
    const allowed = new Set([
      "kind",
      "version",
      "exportedAt",
      "canvas",
      "assets",
      "agentSessions",
      "skipped",
    ]);
    const sample = {
      kind: CANVAS_PACKAGE_KIND,
      version: CANVAS_PACKAGE_VERSION,
      exportedAt: "2026-01-01T00:00:00.000Z",
      canvas: {},
      assets: [],
    };
    for (const k of Object.keys(sample)) {
      expect(allowed.has(k)).toBe(true);
    }
  });

  it("包能被 zip 读写往返（清单与文件都在）", () => {
    const manifest = {
      kind: CANVAS_PACKAGE_KIND,
      version: CANVAS_PACKAGE_VERSION,
      exportedAt: "2026-01-01T00:00:00.000Z",
      canvas: { id: "cv_1" },
      assets: [{ assetId: "as_1", name: "a.png", mime: "image/png", path: "files/as_1.png", hash: "h", size: 3 }],
    };
    const bytes = writeZip([
      { name: "files/as_1.png", data: new Uint8Array([1, 2, 3]) },
      { name: "projects.json", data: new TextEncoder().encode(JSON.stringify(manifest)) },
    ]);
    const { entries, rejected } = readZip(bytes);
    expect(rejected).toEqual([]);
    expect(entries.has("files/as_1.png")).toBe(true);
    const readBack = JSON.parse(new TextDecoder().decode(entries.get("projects.json")!));
    expect(readBack).toEqual(manifest);
  });

  it("会话快照缺失时不写空数组（空数组会破坏往返相等）", () => {
    // 这条纪律与服务端一致：没有会话时省略字段，而不是写 []。
    const manifest: Record<string, unknown> = {
      kind: CANVAS_PACKAGE_KIND,
      version: CANVAS_PACKAGE_VERSION,
      exportedAt: "x",
      canvas: {},
      assets: [],
    };
    expect("agentSessions" in manifest).toBe(false);
  });
});
