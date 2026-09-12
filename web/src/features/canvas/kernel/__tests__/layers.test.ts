import { describe, expect, it } from "vitest";
import {
  isLayerLayoutSettled,
  layerRanks,
  layeredColumnOffsets,
  type LayoutRect,
} from "../geometry";
import { CanvasKernel } from "../kernel";
import type { CanvasDoc, RawEdge, RawNode, ResourceKind } from "../types";

const R = (id: string, x: number, y: number, w = 100, h = 60): LayoutRect => ({
  id,
  x,
  y,
  w,
  h,
});

describe("layerRanks · 拓扑分层", () => {
  it("链条 a→b→c 得到 0/1/2 层", () => {
    const ranks = layerRanks(
      ["a", "b", "c"],
      [
        { from: "a", to: "b" },
        { from: "b", to: "c" },
      ],
    );
    expect(ranks.get("a")).toBe(0);
    expect(ranks.get("b")).toBe(1);
    expect(ranks.get("c")).toBe(2);
  });

  it("菱形 a→b/c→d：b/c 同层，d 取最长路径", () => {
    const ranks = layerRanks(
      ["a", "b", "c", "d"],
      [
        { from: "a", to: "b" },
        { from: "a", to: "c" },
        { from: "b", to: "d" },
        { from: "c", to: "d" },
      ],
    );
    expect(ranks.get("b")).toBe(1);
    expect(ranks.get("c")).toBe(1);
    expect(ranks.get("d")).toBe(2);
  });

  it("最长路径优先：短路 b→d 不能让 d 提前到第 1 层", () => {
    const ranks = layerRanks(
      ["a", "b", "c", "d"],
      [
        { from: "a", to: "b" },
        { from: "b", to: "c" },
        { from: "c", to: "d" },
        { from: "a", to: "d" },
      ],
    );
    expect(ranks.get("d")).toBe(3);
  });

  it("孤立节点落在第 0 层", () => {
    const ranks = layerRanks(["a", "b"], []);
    expect(ranks.get("a")).toBe(0);
    expect(ranks.get("b")).toBe(0);
  });

  it("自环与多重边不影响层号", () => {
    const ranks = layerRanks(
      ["a", "b"],
      [
        { from: "a", to: "a" },
        { from: "a", to: "b" },
        { from: "a", to: "b" },
      ],
    );
    expect(ranks.get("a")).toBe(0);
    expect(ranks.get("b")).toBe(1);
  });

  it("环路不死循环：所有节点都有层号且结果确定", () => {
    const ids = ["a", "b", "c"];
    const edges = [
      { from: "a", to: "b" },
      { from: "b", to: "c" },
      { from: "c", to: "a" },
    ];
    const first = layerRanks(ids, edges);
    const second = layerRanks(ids, edges);
    for (const id of ids) {
      expect(Number.isFinite(first.get(id))).toBe(true);
      expect(first.get(id)).toBe(second.get(id));
    }
  });

  // ATK-29：成环时先做 SCC 收缩再定层，环下游无环节点不得被压进同一层
  it("环上节点同层，环下游的无环节点仍拿到正确层号", () => {
    // 这是上一版的真实缺陷：Kahn 只给「入度能降到 0」的节点定层，
    // 环上节点的入度永远 ≥ 1，于是环下游的节点也一起落进「剩余」集合，
    // 被统一塞进同一层 —— 图里只要有一个环，整张图就被压成一列。
    const ranks = layerRanks(
      ["a", "b", "c", "d"],
      [
        { from: "a", to: "b" },
        { from: "b", to: "a" }, // a<->b 成环
        { from: "b", to: "c" },
        { from: "c", to: "d" },
      ],
    );
    expect(ranks.get("a")).toBe(ranks.get("b")); // 环上互为上下游 → 同层
    expect(ranks.get("c")).toBe(ranks.get("a")! + 1); // c 不受环污染
    expect(ranks.get("d")).toBe(ranks.get("c")! + 1);
  });

  it("环在最左侧时，右侧长链逐层展开（不被压成一列）", () => {
    const ids = ["a", "b", "c", "d", "e", "f"];
    const edges = [
      { from: "a", to: "b" },
      { from: "b", to: "a" },
      { from: "b", to: "c" },
      { from: "c", to: "d" },
      { from: "d", to: "e" },
      { from: "e", to: "f" },
    ];
    const ranks = layerRanks(ids, edges);
    expect(ranks.get("a")).toBe(0);
    expect(ranks.get("b")).toBe(0);
    expect(ranks.get("c")).toBe(1);
    expect(ranks.get("f")).toBe(4);
  });

  it("多个互不相干的环汇入同一节点：汇点取最长路径", () => {
    const ranks = layerRanks(
      ["a", "b", "x", "y", "s"],
      [
        { from: "a", to: "b" },
        { from: "b", to: "a" },
        { from: "x", to: "y" },
        { from: "y", to: "x" },
        { from: "b", to: "s" },
        { from: "y", to: "s" },
      ],
    );
    expect(ranks.get("a")).toBe(0);
    expect(ranks.get("x")).toBe(0);
    expect(ranks.get("s")).toBe(1);
  });

  it("纯环：环上所有节点同层，结果确定", () => {
    const ids = ["a", "b", "c"];
    const edges = [
      { from: "a", to: "b" },
      { from: "b", to: "c" },
      { from: "c", to: "a" },
    ];
    const first = layerRanks(ids, edges);
    expect(first.get("a")).toBe(first.get("b"));
    expect(first.get("b")).toBe(first.get("c"));
    expect([...first]).toEqual([...layerRanks(ids, edges)]);
  });

  it("超长链不爆栈（迭代式 Tarjan）", () => {
    // 递归实现会在这种深度上 RangeError；本函数的实现必须是迭代的。
    const ids = Array.from({ length: 20000 }, (_, i) => `n${i}`);
    const edges = ids
      .slice(1)
      .map((_, i) => ({ from: `n${i}`, to: `n${i + 1}` }));
    const ranks = layerRanks(ids, edges);
    expect(ranks.get("n0")).toBe(0);
    expect(ranks.get("n19999")).toBe(19999);
  });

  it("选区外的端点被忽略（外部连线不污染层号）", () => {
    const ranks = layerRanks(["a", "b"], [{ from: "outside", to: "b" }]);
    expect(ranks.get("a")).toBe(0);
    expect(ranks.get("b")).toBe(0);
  });

  it("空输入返回空表", () => {
    expect(layerRanks([], []).size).toBe(0);
  });
});

describe("layeredColumnOffsets · 同层同列", () => {
  const ranks = new Map([
    ["a", 0],
    ["b", 1],
    ["c", 1],
    ["d", 2],
  ]);

  it("同层节点 x 相同，且列从左到右按层号递增", () => {
    const rects = [
      R("a", 500, 500),
      R("b", 0, 0),
      R("c", 900, 300),
      R("d", 1200, 700),
    ];
    const offs = layeredColumnOffsets(rects, ranks);
    const at = (id: string) => {
      const r = rects.find((x) => x.id === id)!;
      const o = offs.find((x) => x.id === id)!;
      return { x: r.x + o.dx, y: r.y + o.dy, w: r.w };
    };
    const a = at("a");
    const b = at("b");
    const c = at("c");
    const d = at("d");
    expect(b.x).toBe(c.x); // 同层同列
    expect(a.x).toBeLessThan(b.x);
    expect(b.x).toBeLessThan(d.x);
    void a;
  });

  it("列间距用「最宽节点 + columnGap」，列之间不横向重叠", () => {
    const rects = [R("a", 0, 0, 300), R("b", 0, 0, 100)];
    const ranks2 = new Map([
      ["a", 0],
      ["b", 1],
    ]);
    const offs = layeredColumnOffsets(rects, ranks2, { columnGap: 50 });
    const xa = rects[0].x + offs[0].dx;
    const xb = rects[1].x + offs[1].dx;
    expect(xb).toBe(xa + 300 + 50);
  });

  it("列内保持原有上下顺序并铺开（不再重叠）", () => {
    const rects = [R("b", 0, 400), R("c", 0, 100), R("a", 0, 0, 100, 60)];
    const sameRank = new Map([
      ["a", 0],
      ["b", 0],
      ["c", 0],
    ]);
    const offs = layeredColumnOffsets(rects, sameRank, { rowGap: 0 });
    const ys = ["c", "b"].map((id) => {
      const r = rects.find((x) => x.id === id)!;
      return r.y + offs.find((o) => o.id === id)!.dy;
    });
    // c 原 y=100 在 b 原 y=400 上方，整理后仍在上方且不重叠
    expect(ys[0]).toBeLessThan(ys[1]);
  });

  it("重复执行幂等（已排列好再算一次位移为 0）", () => {
    const rects = [R("a", 0, 0), R("b", 400, 0)];
    const ranks2 = new Map([
      ["a", 0],
      ["b", 1],
    ]);
    const first = layeredColumnOffsets(rects, ranks2);
    const moved = rects.map((r) => {
      const o = first.find((x) => x.id === r.id)!;
      return { ...r, x: r.x + o.dx, y: r.y + o.dy };
    });
    const second = layeredColumnOffsets(moved, ranks2);
    expect(
      second.every((o) => Math.abs(o.dx) < 1e-9 && Math.abs(o.dy) < 1e-9),
    ).toBe(true);
    expect(isLayerLayoutSettled(moved, ranks2)).toBe(true);
  });

  it("整体锚点不变：选区左上角不被推走", () => {
    const rects = [R("a", 1000, 2000), R("b", 5000, 300)];
    const offs = layeredColumnOffsets(
      rects,
      new Map([
        ["a", 0],
        ["b", 1],
      ]),
    );
    const minX = Math.min(
      ...rects.map((r) => r.x + offs.find((o) => o.id === r.id)!.dx),
    );
    const minY = Math.min(
      ...rects.map((r) => r.y + offs.find((o) => o.id === r.id)!.dy),
    );
    expect(minX).toBe(1000);
    expect(minY).toBe(300);
  });

  it("少于 2 个矩形不产生位移", () => {
    expect(layeredColumnOffsets([R("a", 0, 0)], new Map([["a", 0]]))).toEqual(
      [],
    );
    expect(layeredColumnOffsets([], new Map())).toEqual([]);
  });

  it("center 模式：列内水平居中", () => {
    const rects = [R("a", 0, 0, 200), R("b", 0, 100, 100)];
    const same = new Map([
      ["a", 0],
      ["b", 0],
    ]);
    const offs = layeredColumnOffsets(rects, same, { columnAlign: "center" });
    const xa = rects[0].x + offs[0].dx;
    const xb = rects[1].x + offs[1].dx;
    // b 宽 100，居中于宽 200 的列 → 偏移 50
    expect(xb - xa).toBe(50);
  });
});

function edge(id: string, from: string, to: string): RawEdge {
  const kind: ResourceKind = "text";
  return {
    id,
    from: { nodeId: from, portId: "out" },
    to: { nodeId: to, portId: "in" },
    kind,
    createdAt: new Date().toISOString(),
  };
}

function node(id: string, x: number, y: number): RawNode {
  return {
    id,
    type: "image",
    title: id,
    rect: { x, y, w: 100, h: 60 },
    z: 1,
    ports: { inputs: [], outputs: [] },
    spec: {},
    state: "idle",
  };
}

function docWith(nodes: RawNode[], edges: RawEdge[] = []): CanvasDoc {
  const nm: Record<string, RawNode> = {};
  for (const n of nodes) nm[n.id] = n;
  const em: Record<string, RawEdge> = {};
  for (const e of edges) em[e.id] = e;
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
    nodes: nm,
    edges: em,
    updatedAt: new Date().toISOString(),
  };
}

describe("CanvasKernel 分层成列", () => {
  // ATK-29：按层同列，一条命令、一次撤销可还原
  it("链条三层被整理成三列，同层同 x，可一次撤销", () => {
    const kernel = new CanvasKernel(
      docWith(
        [node("a", 500, 500), node("b", 0, 0), node("c", 900, 300)],
        [edge("e1", "a", "b"), edge("e2", "b", "c")],
      ),
    );
    const ops = kernel.layoutByLayers(["a", "b", "c"]);
    expect(ops.length).toBeGreaterThan(0);
    const a = kernel.scene.getNode("a")!.rect;
    const b = kernel.scene.getNode("b")!.rect;
    const c = kernel.scene.getNode("c")!.rect;
    expect(a.x).toBeLessThan(b.x);
    expect(b.x).toBeLessThan(c.x);
    expect(kernel.pendingCount).toBe(ops.length);

    expect(kernel.undoOnce()).toBe(true);
    expect(kernel.scene.getNode("b")!.rect.x).toBe(0);
    expect(kernel.scene.getNode("c")!.rect.x).toBe(900);
  });

  it("同一层的多个节点共用同一 x", () => {
    const kernel = new CanvasKernel(
      docWith(
        [node("a", 0, 0), node("b", 10, 300), node("c", 800, 700)],
        [edge("e1", "a", "b"), edge("e2", "a", "c")],
      ),
    );
    kernel.layoutByLayers(["a", "b", "c"]);
    const bx = kernel.scene.getNode("b")!.rect.x;
    const cx = kernel.scene.getNode("c")!.rect.x;
    expect(bx).toBe(cx);
  });

  it("重复整理幂等（第二次不产生 op）", () => {
    const kernel = new CanvasKernel(
      docWith([node("a", 0, 0), node("b", 400, 0)], [edge("e1", "a", "b")]),
    );
    expect(kernel.layoutByLayers(["a", "b"]).length).toBeGreaterThan(0);
    expect(kernel.layoutByLayers(["a", "b"])).toEqual([]);
    expect(kernel.isLayeredAlready(["a", "b"])).toBe(true);
  });

  it("只读画布不产生任何改动", () => {
    const doc = docWith(
      [node("a", 0, 0), node("b", 400, 0)],
      [edge("e1", "a", "b")],
    );
    doc.settings.readOnly = true;
    const kernel = new CanvasKernel(doc);
    expect(kernel.layoutByLayers(["a", "b"])).toEqual([]);
    expect(kernel.scene.getNode("b")!.rect.x).toBe(400);
  });

  it("单选 / 空选不产生 op", () => {
    const kernel = new CanvasKernel(docWith([node("a", 0, 0)]));
    expect(kernel.layoutByLayers(["a"])).toEqual([]);
    expect(kernel.layoutByLayers([])).toEqual([]);
    expect(kernel.isLayeredAlready(["a"])).toBe(true);
  });

  it("默认作用于当前选区", () => {
    const kernel = new CanvasKernel(
      docWith([node("a", 0, 0), node("b", 400, 0)], [edge("e1", "a", "b")]),
    );
    kernel.setSelection({ nodes: ["a", "b"], edges: [] });
    expect(kernel.layoutByLayers().length).toBeGreaterThan(0);
  });
});
