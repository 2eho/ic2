/**
 * 拓扑分层（「每一层在同一列」的层号计算）。
 *
 * 从 `geometry.ts` 拆出：SCC + 最长路径定层是一段自成一体的图算法，
 * 与「矩形几何」不是一类东西；放在这里也能让 `geometry.ts` 的职责更清晰。
 * 纯函数：不 import react、不碰 DOM、不持有状态。
 */

/**
 * 计算每个节点的拓扑层级（rank）。
 *
 * 规则（确定性 + 容错，画布上的连线可能成环）：
 * - 无入边的节点是第 0 层；
 * - 其余节点 = 所有上游节点层级的最大值 + 1（最长路径，保证连线不会倒退）；
 * - **成环时先做强连通分量（SCC）收缩**：同一个环上的节点互为上下游、
 *   无法定序，因此它们共享同一层；收缩后的图必为 DAG，再按最长路径定层。
 *   这样「环下游的无环节点」仍能拿到正确层号，而不会被一起推进第 0 层。
 *
 * 为什么不能只做 Kahn + 把剩余节点统一甩到最后一层：
 *   Kahn 只会给「入度能降到 0」的节点定层。环上节点入度永远 ≥1，
 *   而环下游的节点因为前驱从未出队，入度也永远降不到 0 ——
 *   于是**整个下游子图**都会落进「剩余」集合，被一起塞进同一层。
 *   实测后果：图里只要有一个环，整张图就被压成一列，分层功能当场失效。
 *
 * `edges` 只需要 `from/to` 的节点 id；不在 `ids` 里的端点会被忽略，
 * 这样「只对选区做分层」时外部连线不会污染层号。
 */
export function layerRanks(
  ids: string[],
  edges: { from: string; to: string }[],
): Map<string, number> {
  const rank = new Map<string, number>();
  const known = new Set(ids);
  if (ids.length === 0) return rank;

  // 邻接表：去重 + 去掉自环（自环不产生跨分量依赖）、忽略区外端点
  const adj = new Map<string, string[]>();
  for (const id of ids) adj.set(id, []);
  for (const e of edges) {
    if (!known.has(e.from) || !known.has(e.to) || e.from === e.to) continue;
    adj.get(e.from)!.push(e.to);
  }

  // 1) Tarjan 求强连通分量（迭代实现，避免超大图递归爆栈）
  const comp = new Map<string, number>(); // id -> 分量号
  const compNode: string[] = []; // 分量号 -> 代表节点 id（用于稳定排序）
  {
    const index = new Map<string, number>();
    const low = new Map<string, number>();
    const onStack = new Set<string>();
    const stack: string[] = [];
    let counter = 0;
    let compCount = 0;

    for (const root of ids) {
      if (index.has(root)) continue;
      // 显式栈：每个帧记录节点与「下一个待访问的邻居下标」
      const frames: { node: string; next: number }[] = [
        { node: root, next: 0 },
      ];
      index.set(root, counter);
      low.set(root, counter);
      counter += 1;
      stack.push(root);
      onStack.add(root);

      while (frames.length > 0) {
        const frame = frames[frames.length - 1];
        const outs = adj.get(frame.node)!;
        if (frame.next < outs.length) {
          const to = outs[frame.next];
          frame.next += 1;
          if (!index.has(to)) {
            index.set(to, counter);
            low.set(to, counter);
            counter += 1;
            stack.push(to);
            onStack.add(to);
            frames.push({ node: to, next: 0 });
          } else if (onStack.has(to)) {
            low.set(frame.node, Math.min(low.get(frame.node)!, index.get(to)!));
          }
          continue;
        }
        // 该节点的邻居都访问完了：回溯
        frames.pop();
        if (frames.length > 0) {
          const parent = frames[frames.length - 1].node;
          low.set(parent, Math.min(low.get(parent)!, low.get(frame.node)!));
        }
        if (low.get(frame.node) === index.get(frame.node)) {
          // 弹出一个完整分量
          let member: string | undefined;
          let minId: string | undefined;
          do {
            member = stack.pop()!;
            onStack.delete(member);
            comp.set(member, compCount);
            if (minId === undefined || member < minId) minId = member;
          } while (member !== frame.node);
          compNode[compCount] = minId!;
          compCount += 1;
        }
      }
    }
  }

  // 2) 收缩后建 DAG（同一分量内的边丢弃），去重
  const dagAdj = new Map<number, Set<number>>();
  const dagIndeg = new Map<number, number>();
  for (let c = 0; c < compNode.length; c += 1) {
    dagAdj.set(c, new Set());
    dagIndeg.set(c, 0);
  }
  for (const id of ids) {
    const from = comp.get(id)!;
    for (const to of adj.get(id)!) {
      const tc = comp.get(to)!;
      if (from === tc) continue;
      const outs = dagAdj.get(from)!;
      if (outs.has(tc)) continue;
      outs.add(tc);
      dagIndeg.set(tc, (dagIndeg.get(tc) ?? 0) + 1);
    }
  }

  // 3) 在 DAG 上按最长路径定层：只有「所有前驱都已定层」的分量才能定层。
  const dagRank = new Map<number, number>();
  const frontier: number[] = [];
  for (let c = 0; c < compNode.length; c += 1) {
    if ((dagIndeg.get(c) ?? 0) === 0) {
      dagRank.set(c, 0);
      frontier.push(c);
    }
  }
  // 分量号按代表节点 id 排序，保证 frontier 迭代顺序确定
  frontier.sort((a, b) => (compNode[a] < compNode[b] ? -1 : 1));
  let queue = [...frontier];
  while (queue.length > 0) {
    const next: number[] = [];
    for (const c of queue) {
      const base = dagRank.get(c)!;
      for (const tc of dagAdj.get(c)!) {
        const d = (dagIndeg.get(tc) ?? 0) - 1;
        dagIndeg.set(tc, d);
        if (d === 0) {
          dagRank.set(tc, base + 1);
          next.push(tc);
        }
      }
    }
    next.sort((a, b) => (compNode[a] < compNode[b] ? -1 : 1));
    queue = next;
  }

  for (const id of ids) {
    rank.set(id, dagRank.get(comp.get(id)!) ?? 0);
  }
  return rank;
}
