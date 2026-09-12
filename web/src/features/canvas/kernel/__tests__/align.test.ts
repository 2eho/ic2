import { describe, expect, it } from "vitest";
import {
  alignOffsets,
  distributeOffsets,
  isAligned,
  type AlignRect,
} from "../geometry";
import { offsetsToMoves } from "../commands";
import { CanvasKernel } from "../kernel";
import type { CanvasDoc, RawNode, Rect } from "../types";

const R = (id: string, x: number, y: number, w = 100, h = 100): AlignRect => ({
  id,
  x,
  y,
  w,
  h,
});

describe("alignOffsets · 向左/右/水平居中", () => {
  const rects = [R("a", 0, 0), R("b", 50, 200), R("c", 300, 400)];

  it("左对齐：全部对齐到最左边缘", () => {
    const out = alignOffsets(rects, "left");
    expect(out.map((o) => o.dx)).toEqual([0, -50, -300]);
    expect(out.every((o) => o.dy === 0)).toBe(true);
  });

  it("右对齐：全部对齐到最右边缘", () => {
    const out = alignOffsets(rects, "right");
    // 最右边缘 = 400，a/b/c 的右边缘分别 100/150/400
    expect(out.map((o) => o.dx)).toEqual([300, 250, 0]);
  });

  it("水平居中：对齐到包围盒中线", () => {
    const out = alignOffsets(rects, "hcenter");
    const mid = 200;
    expect(out.map((o) => o.dx)).toEqual([150, 100, -150]);
    void mid;
  });

  it("上/下/垂直居中", () => {
    const r = [R("a", 0, 0), R("b", 0, 50), R("c", 0, 300)];
    expect(alignOffsets(r, "top").map((o) => o.dy)).toEqual([0, -50, -300]);
    expect(alignOffsets(r, "bottom").map((o) => o.dy)).toEqual([300, 250, 0]);
    expect(alignOffsets(r, "vcenter").map((o) => o.dy)).toEqual([
      150, 100, -150,
    ]);
  });

  it("尺寸不同也能对齐（按边而不是按中心）", () => {
    const r = [R("a", 0, 0, 40, 40), R("b", 100, 100, 200, 80)];
    expect(alignOffsets(r, "left").map((o) => o.dx)).toEqual([0, -100]);
    expect(alignOffsets(r, "top").map((o) => o.dy)).toEqual([0, -100]);
  });
});

describe("alignOffsets · anchor=first", () => {
  const rects = [R("a", 100, 100), R("b", 0, 0), R("c", 300, 300)];

  it("以首个元素为基准，首元素自身位移为 0", () => {
    const out = alignOffsets(rects, "left", "first");
    expect(out[0]).toEqual({ id: "a", dx: 0, dy: 0 });
    expect(out.map((o) => o.dx)).toEqual([0, 100, -200]);
  });
});

describe("alignOffsets · 边界", () => {
  // ATK-27：不足数量的选择、已对齐、NaN 位移都不应该变成「看起来成功」的空操作
  it("少于 2 个元素不产生位移", () => {
    expect(alignOffsets([], "left")).toEqual([]);
    expect(alignOffsets([R("a", 0, 0)], "left")).toEqual([]);
  });

  it("已经对齐时位移为 0（幂等，不会漂移）", () => {
    const out = alignOffsets([R("a", 5, 0), R("b", 5, 80)], "left");
    expect(out.every((o) => o.dx === 0)).toBe(true);
    expect(isAligned([R("a", 5, 0), R("b", 5, 80)], "left")).toBe(true);
  });

  it("非有限值输入不会产出 NaN 位移", () => {
    const out = alignOffsets([R("a", Number.NaN, 0), R("b", 10, 0)], "left");
    expect(out.every((o) => Number.isFinite(o.dx))).toBe(false);
    // 内核层会把这些位移过滤掉（见 offsetsToMoves 单测）
  });
});

describe("distributeOffsets", () => {
  it("水平分布：保持两端不动，中间中心等距", () => {
    const rects = [R("a", 0, 0), R("b", 10, 0), R("c", 1000, 0)];
    const out = distributeOffsets(rects, "horizontal");
    const b = out.find((o) => o.id === "b")!;
    // 中心：a=50，c=1050，step=500 → b 目标中心 550，当前 60
    expect(b.dx).toBe(490);
    expect(out.find((o) => o.id === "a")!.dx).toBe(0);
    expect(out.find((o) => o.id === "c")!.dx).toBe(0);
  });

  it("垂直分布按 y 轴", () => {
    const rects = [R("a", 0, 0), R("b", 0, 10), R("c", 0, 1000)];
    const out = distributeOffsets(rects, "vertical");
    expect(out.find((o) => o.id === "b")!.dy).toBe(490);
    expect(out.every((o) => o.dx === 0)).toBe(true);
  });

  it("乱序输入按坐标排序后计算（不是按数组顺序）", () => {
    const out = distributeOffsets(
      [R("c", 1000, 0), R("a", 0, 0), R("b", 10, 0)],
      "horizontal",
    );
    expect(out.find((o) => o.id === "b")!.dx).toBe(490);
  });

  it("少于 3 个元素不分布", () => {
    expect(
      distributeOffsets([R("a", 0, 0), R("b", 100, 0)], "horizontal"),
    ).toEqual([]);
  });

  it("已经等距时位移全部为 0", () => {
    const out = distributeOffsets(
      [R("a", 0, 0), R("b", 200, 0), R("c", 400, 0)],
      "horizontal",
    );
    expect(out.every((o) => o.dx === 0 && o.dy === 0)).toBe(true);
  });
});

describe("offsetsToMoves", () => {
  const rects: Record<string, Rect> = {
    a: { x: 0, y: 0, w: 100, h: 100 },
    b: { x: 50, y: 200, w: 100, h: 100 },
  };
  const rectOf = (id: string) => rects[id];

  it("位移转绝对坐标（不是增量）", () => {
    expect(offsetsToMoves([{ id: "b", dx: -50, dy: 0 }], rectOf)).toEqual([
      { kind: "move_node", id: "b", x: 0, y: 200 },
    ]);
  });

  it("零位移与未知 id 被跳过（不产生垃圾 op）", () => {
    expect(
      offsetsToMoves(
        [
          { id: "a", dx: 0, dy: 0 },
          { id: "ghost", dx: 10, dy: 10 },
        ],
        rectOf,
      ),
    ).toEqual([]);
  });

  it("非有限位移被丢弃（不把 NaN 发给服务端）", () => {
    expect(
      offsetsToMoves([{ id: "a", dx: Number.NaN, dy: 0 }], rectOf),
    ).toEqual([]);
  });
});

function docWith(nodes: RawNode[]): CanvasDoc {
  const map: Record<string, RawNode> = {};
  for (const n of nodes) map[n.id] = n;
  return {
    id: "c1",
    projectId: "p1",
    version: 1,
    viewport: { x: 0, y: 0, k: 1 },
    settings: {
      background: "dots",
      imageInfo: false,
      gridSnap: false,
      readOnly: false,
      freeResize: true,
    },
    nodes: map,
    edges: {},
    updatedAt: new Date().toISOString(),
  };
}

function node(id: string, x: number, y: number): RawNode {
  return {
    id,
    type: "image",
    title: id,
    rect: { x, y, w: 100, h: 100 },
    z: 1,
    ports: { inputs: [], outputs: [] },
    spec: {},
    state: "idle",
  };
}

describe("CanvasKernel 一键对齐", () => {
  // ATK-28：对齐必须是单条命令，一次 Ctrl+Z 整体还原（而不是 N 条散装移动）
  it("左对齐后节点坐标一致，且可通过 undo 还原", () => {
    const kernel = new CanvasKernel(
      docWith([node("a", 0, 0), node("b", 50, 200), node("c", 300, 400)]),
    );
    const ops = kernel.alignSelection(["a", "b", "c"], "left");
    expect(ops.length).toBe(2); // a 本来就在左边缘，无需移动
    expect(kernel.scene.getNode("b")!.rect.x).toBe(0);
    expect(kernel.scene.getNode("c")!.rect.x).toBe(0);
    expect(kernel.pendingCount).toBe(2);

    expect(kernel.undoOnce()).toBe(true);
    expect(kernel.scene.getNode("b")!.rect.x).toBe(50);
    expect(kernel.scene.getNode("c")!.rect.x).toBe(300);
  });

  // ATK-27：重复点击幂等（绝对坐标而非增量）、只读不生效、位移含 NaN 被丢弃
  it("重复对齐是幂等的（第二次不产生 op）", () => {
    const kernel = new CanvasKernel(
      docWith([node("a", 0, 0), node("b", 50, 200)]),
    );
    expect(kernel.alignSelection(["a", "b"], "top").length).toBe(1);
    expect(kernel.alignSelection(["a", "b"], "top").length).toBe(0);
  });

  it("只读到 settings.readOnly 时不产生任何改动", () => {
    const doc = docWith([node("a", 0, 0), node("b", 50, 200)]);
    doc.settings.readOnly = true;
    const kernel = new CanvasKernel(doc);
    expect(kernel.alignSelection(["a", "b"], "left")).toEqual([]);
    expect(kernel.scene.getNode("b")!.rect.x).toBe(50);
  });

  it("单选 / 空选不产生 op", () => {
    const kernel = new CanvasKernel(docWith([node("a", 0, 0)]));
    expect(kernel.alignSelection(["a"], "left")).toEqual([]);
    expect(kernel.alignSelection([], "left")).toEqual([]);
  });

  it("分布保持两端不动（3 个以上才生效）", () => {
    const kernel = new CanvasKernel(
      docWith([node("a", 0, 0), node("b", 10, 0), node("c", 1000, 0)]),
    );
    expect(kernel.distributeSelection(["a", "b"], "horizontal")).toEqual([]);
    const ops = kernel.distributeSelection(["a", "b", "c"], "horizontal");
    expect(ops.length).toBe(1);
    expect(kernel.scene.getNode("a")!.rect.x).toBe(0);
    expect(kernel.scene.getNode("c")!.rect.x).toBe(1000);
    expect(kernel.scene.getNode("b")!.rect.x).toBe(500);
  });

  it("rectsOf 过滤已不存在的 id", () => {
    const kernel = new CanvasKernel(docWith([node("a", 0, 0)]));
    expect(kernel.rectsOf(["a", "ghost"])).toEqual([
      { id: "a", x: 0, y: 0, w: 100, h: 100 },
    ]);
  });
});
