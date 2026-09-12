import { describe, expect, it } from "vitest";
import { applySelection, coalesceOps, commandToOps } from "../commands";
import type { Op } from "../types";

describe("commandToOps", () => {
  it("移动命令展开为 delta 移动 op", () => {
    const ops = commandToOps({
      type: "move-nodes",
      ids: ["a", "b"],
      dx: 10,
      dy: -5,
    });
    expect(ops).toHaveLength(2);
    expect(ops[0]).toEqual({
      kind: "move_node",
      id: "a",
      x: 10,
      y: -5,
      delta: true,
    });
  });

  it("删除命令默认级联", () => {
    expect(commandToOps({ type: "delete-nodes", ids: ["x"] })[0]).toEqual({
      kind: "remove_node",
      id: "x",
      cascade: true,
    });
  });

  it("复制命令由调用方展开", () => {
    expect(
      commandToOps({
        type: "duplicate-nodes",
        ids: ["a"],
        offsetX: 20,
        offsetY: 20,
        newIds: {},
      }),
    ).toEqual([]);
  });
});

describe("coalesceOps", () => {
  it("同一节点多次 delta 移动合并", () => {
    const out = coalesceOps([
      { kind: "move_node", id: "a", x: 1, y: 1, delta: true },
      { kind: "move_node", id: "a", x: 2, y: 3, delta: true },
      { kind: "move_node", id: "b", x: 5, y: 0, delta: true },
    ]);
    expect(out.find((o) => o.kind === "move_node" && o.id === "a")).toEqual({
      kind: "move_node",
      id: "a",
      x: 3,
      y: 4,
      delta: true,
    });
    expect(out.filter((o) => o.kind === "move_node")).toHaveLength(2);
  });

  it("视口 op 只保留最后一个", () => {
    const vps = coalesceOps([
      { kind: "set_viewport", viewport: { x: 1, y: 1, k: 1 } },
      { kind: "set_viewport", viewport: { x: 2, y: 2, k: 2 } },
    ]).filter((o) => o.kind === "set_viewport");
    expect(vps).toHaveLength(1);
    expect(vps[0]).toEqual({
      kind: "set_viewport",
      viewport: { x: 2, y: 2, k: 2 },
    });
  });

  it("零位移移动被丢弃", () => {
    expect(
      coalesceOps([{ kind: "move_node", id: "a", x: 0, y: 0, delta: true }]),
    ).toEqual([]);
  });

  it("缩放以后者覆盖前者", () => {
    const out = coalesceOps([
      { kind: "resize_node", id: "a", w: 100, h: 100 },
      { kind: "resize_node", id: "a", w: 200, h: 200 },
    ] as Op[]).filter((o) => o.kind === "resize_node");
    expect(out).toHaveLength(1);
    expect(out[0]).toEqual({ kind: "resize_node", id: "a", w: 200, h: 200 });
  });
});

describe("applySelection", () => {
  it("非追加模式替换选择", () => {
    expect(
      applySelection({ nodes: ["old"], edges: [] }, { nodes: ["new"] }, false)
        .nodes,
    ).toEqual(["new"]);
  });

  it("追加模式切换选中状态", () => {
    let sel = applySelection(
      { nodes: ["a"], edges: [] },
      { nodes: ["b"] },
      true,
    );
    expect(sel.nodes.sort()).toEqual(["a", "b"]);
    sel = applySelection(sel, { nodes: ["a"] }, true);
    expect(sel.nodes).toEqual(["b"]);
  });
});
