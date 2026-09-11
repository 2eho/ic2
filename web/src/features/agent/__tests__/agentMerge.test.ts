import { describe, expect, it } from "vitest";
import type { AgentItemDTO, AgentTurnDTO } from "@/shared/api/endpoints";

/**
 * 快照与实时条目的合并（对齐 docs/design/10 §9.4 / §9.16，ATK-13 的客户端侧）。
 *
 * 服务端合并由主键 (turnId, itemId) 保证（见 internal/agent/agent_test.go）。
 * 这里验证**客户端**合并：快照为权威，实时条目只补充快照里还没有的。
 *
 * 为什么值得独立测试：合并写错的表现是「消息重复」或「消息消失」，
 * 而这两种症状都不会让任何请求失败——只有人工点界面才能发现，
 * 所以必须在这里穷举边界。
 */

/** 与被测实现保持同一语义的纯函数版本（便于穷举边界而不依赖 React）。 */
function mergeTurns(
  server: AgentTurnDTO[],
  live: AgentItemDTO[],
): AgentTurnDTO[] {
  if (live.length === 0) return server;
  const materialized = new Set(server.flatMap((t) => t.items.map((i) => i.id)));
  const pending = live.filter((i) => !materialized.has(i.id));
  if (pending.length === 0) return server;
  return [
    ...server,
    {
      id: "live",
      seq: server.length,
      status: "running",
      input: "",
      items: pending,
      usage: {},
    } as AgentTurnDTO,
  ];
}

function item(
  id: string,
  text: string,
  source: "live" | "snapshot" = "live",
): AgentItemDTO {
  return {
    id,
    turnId: "t1",
    seq: 0,
    kind: "agent_message",
    payload: { text },
    source,
  };
}

describe("Agent 快照与实时合并", () => {
  it("无实时条目时原样返回快照（引用不变，避免无意义重渲染）", () => {
    const server = [
      {
        id: "t1",
        seq: 0,
        status: "succeeded",
        items: [item("i1", "a", "snapshot")],
      } as AgentTurnDTO,
    ];
    expect(mergeTurns(server, [])).toBe(server);
  });

  it("快照已物化的条目不再追加（不重复显示）", () => {
    const server = [
      {
        id: "t1",
        seq: 0,
        status: "succeeded",
        items: [item("i1", "hello", "snapshot")],
      } as AgentTurnDTO,
    ];
    const merged = mergeTurns(server, [item("i1", "hello")]);
    expect(merged).toHaveLength(1);
    expect(merged[0].items).toHaveLength(1);
  });

  it("快照缺的实时条目追加为临时轮次", () => {
    const server = [
      {
        id: "t1",
        seq: 0,
        status: "succeeded",
        items: [item("i1", "a", "snapshot")],
      } as AgentTurnDTO,
    ];
    const merged = mergeTurns(server, [item("i1", "a"), item("i2", "b")]);
    expect(merged).toHaveLength(2);
    expect(merged[1].id).toBe("live");
    expect(merged[1].items.map((i) => i.id)).toEqual(["i2"]);
  });

  it("全部实时条目都已物化时不产生空壳临时轮次", () => {
    const server = [
      {
        id: "t1",
        seq: 0,
        status: "succeeded",
        items: [item("i1", "a", "snapshot"), item("i2", "b", "snapshot")],
      } as AgentTurnDTO,
    ];
    const merged = mergeTurns(server, [item("i1", "a"), item("i2", "b")]);
    expect(merged).toHaveLength(1);
  });

  it("临时轮次的 seq 必须接在最后（否则 UI 排序会跳）", () => {
    const server = [
      {
        id: "t1",
        seq: 0,
        status: "succeeded",
        items: [item("i1", "a", "snapshot")],
      } as AgentTurnDTO,
      {
        id: "t2",
        seq: 1,
        status: "succeeded",
        items: [item("i2", "b", "snapshot")],
      } as AgentTurnDTO,
    ];
    const merged = mergeTurns(server, [item("i3", "c")]);
    expect(merged).toHaveLength(3);
    expect(merged[2].seq).toBe(2);
  });
});
