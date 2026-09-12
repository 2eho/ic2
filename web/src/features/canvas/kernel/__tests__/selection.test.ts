import { describe, expect, it } from "vitest";
import {
  applyAdditive,
  toggleSelectionNodes,
  unionSelection,
} from "../selection";

/**
 * ATK-30：多选的入口本身不能被静默降级。
 *
 * 这些用例盯的是「多选能不能堆出来」——如果 additivity 退化，
 * 多选工具栏（一键对齐 / 等间距 / 分层成列）就永远只对 1 个节点可见，
 * 用户到不了功能，却看不出哪里报错。
 */
describe("选区合并 · unionSelection", () => {
  it("空选区并入命中 → 等于命中", () => {
    expect(
      unionSelection({ nodes: [], edges: [] }, { nodes: ["a"], edges: [] }),
    ).toEqual({ nodes: ["a"], edges: [] });
  });

  it("重复 id 去重（同一节点被命中两次不应出现两遍）", () => {
    const r = unionSelection(
      { nodes: ["a"], edges: [] },
      { nodes: ["a", "a", "b"], edges: [] },
    );
    expect(r.nodes).toEqual(["a", "b"]);
  });

  it("连线并入同样去重", () => {
    const r = unionSelection(
      { nodes: [], edges: ["e1"] },
      { nodes: [], edges: ["e1", "e2"] },
    );
    expect(r.edges).toEqual(["e1", "e2"]);
  });

  it("不修改入参（纯函数）", () => {
    const base = { nodes: ["a"], edges: [] as string[] };
    unionSelection(base, { nodes: ["b"], edges: [] });
    expect(base.nodes).toEqual(["a"]);
  });
});

describe("选区合并 · toggleSelectionNodes", () => {
  it("已选中 → 反选移除", () => {
    const r = toggleSelectionNodes(
      { nodes: ["a", "b"], edges: [] },
      { nodes: ["a"], edges: [] },
    );
    expect(r.nodes).toEqual(["b"]);
  });

  it("未选中 → 追加", () => {
    const r = toggleSelectionNodes(
      { nodes: ["a"], edges: [] },
      { nodes: ["b"], edges: [] },
    );
    expect(r.nodes).toEqual(["a", "b"]);
  });

  it("反选节点不影响已选连线", () => {
    const r = toggleSelectionNodes(
      { nodes: ["a"], edges: ["e1"] },
      { nodes: ["a"], edges: [] },
    );
    expect(r.nodes).toEqual([]);
    expect(r.edges).toEqual(["e1"]);
  });
});

describe("选区合并 · applyAdditive（Shift 点击的最终语义）", () => {
  it("命中项全部已选中 → 反选", () => {
    const r = applyAdditive(
      { nodes: ["a", "b"], edges: [] },
      { nodes: ["a"], edges: [] },
    );
    expect(r.nodes).toEqual(["b"]);
  });

  it("命中项部分未选中 → 并入（不能因为「已有一个」就整批反选）", () => {
    const r = applyAdditive(
      { nodes: ["a"], edges: [] },
      { nodes: ["a", "b"], edges: [] },
    );
    expect(r.nodes).toEqual(["a", "b"]);
  });

  it("空命中（Shift 点空白）不得清空已有选区", () => {
    const r = applyAdditive(
      { nodes: ["a", "b"], edges: [] },
      { nodes: [], edges: [] },
    );
    expect(r.nodes).toEqual(["a", "b"]);
  });
});
