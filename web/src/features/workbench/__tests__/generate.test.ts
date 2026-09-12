import { describe, expect, it } from "vitest";
import { renumber, type ReferenceItem } from "../components/ReferenceBar";
import { buildReferencePrefix } from "../generate";

/**
 * 参考图编号与提示词注入（验收口径见 docs/design/10 §6.1：「编号与提交顺序一致」）。
 *
 * 为什么这条必须单独测：编号错位的表现是「提示词里的『图片2』指向了第 3 张」，
 * 界面上完全看不出来（角标、缩略图、提示词各自都对），只有结果不对。
 * 这是最典型的「静默错误」。
 */
function ref(assetId: string, order: number): ReferenceItem {
  return { id: assetId, name: assetId, assetId, mime: "image/png", order };
}

describe("参考图编号", () => {
  it("renumber 让 order 严格等于数组下标", () => {
    const items = [ref("a", 5), ref("b", 2), ref("c", 9)];
    expect(renumber(items).map((i) => i.order)).toEqual([0, 1, 2]);
  });

  it("交换顺序后编号跟着变（不是只换位置）", () => {
    const items = renumber([ref("a", 0), ref("b", 1), ref("c", 2)]);
    // 模拟把第 3 个移到第 1 个
    const moved = [items[2], items[0], items[1]];
    const after = renumber(moved);
    expect(after.map((i) => i.assetId)).toEqual(["c", "a", "b"]);
    expect(after.map((i) => i.order)).toEqual([0, 1, 2]);
  });

  it("删除中间项后其余项重编号（不留空洞）", () => {
    const items = renumber([ref("a", 0), ref("b", 1), ref("c", 2)]);
    const after = renumber(items.filter((i) => i.assetId !== "b"));
    expect(after.map((i) => i.order)).toEqual([0, 1]);
  });

  it("空列表不报错", () => {
    expect(renumber([])).toEqual([]);
  });
});

describe("提示词参考图注入（4.17）", () => {
  const refs = [ref("a", 0), ref("b", 1), ref("c", 2)];

  it("无参考图时原样返回（不注入空编号行）", () => {
    expect(buildReferencePrefix("画一只猫", [])).toBe("画一只猫");
  });

  it("编号从 1 开始，用顿号连接，与 UI 角标一致", () => {
    const out = buildReferencePrefix("画一只猫", refs);
    expect(out).toContain("图片1、图片2、图片3");
    expect(out).toContain("画一只猫");
    // 顺序必须与 UI 一致：第 1 个是 a，第 3 个是 c
    expect(out.indexOf("图片1")).toBeLessThan(out.indexOf("图片3"));
  });

  it("多张参考图的编号连续无跳号", () => {
    const many = Array.from({ length: 7 }, (_, i) => ref(`r${i}`, i));
    const out = buildReferencePrefix("x", many);
    for (let i = 1; i <= 7; i += 1) {
      expect(out, `缺少 图片${i}`).toContain(`图片${i}`);
    }
    expect(out).not.toContain("图片8");
  });
});
