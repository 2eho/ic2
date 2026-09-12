import type { SceneGraph } from "./scene";
import type { Op } from "./types";

/**
 * 场景级 op 的本地应用（与 `internal/graph/op.go` 语义保持一致的子集）。
 *
 * 从 `kernel.ts` 拆出的理由：这是一段「op → 场景」的纯搬运，
 * 不依赖选区/undo/视口，放在内核里只会让内核文件膨胀（800 行硬上限）。
 * 返回 `false` 表示这条 op 不属于场景层（`set_viewport` / `set_settings`），
 * 由内核自己处理——这样本函数保持无副作用之外的确定行为，便于单测。
 */
export function applySceneOp(scene: SceneGraph, op: Op): boolean {
  switch (op.kind) {
    case "add_node":
      scene.addNode(op.node);
      return true;
    case "remove_node": {
      const n = scene.removeNode(op.id);
      if (n) for (const e of scene.edgesOf(op.id)) scene.removeEdge(e.id);
      return true;
    }
    case "move_node": {
      const n = scene.getNode(op.id);
      if (!n) return true;
      scene.updateNode({
        ...n,
        rect: {
          ...n.rect,
          x: op.delta ? n.rect.x + op.x : op.x,
          y: op.delta ? n.rect.y + op.y : op.y,
        },
      });
      return true;
    }
    case "resize_node": {
      const n = scene.getNode(op.id);
      if (!n) return true;
      scene.updateNode({ ...n, rect: { ...n.rect, w: op.w, h: op.h } });
      return true;
    }
    case "set_title": {
      const n = scene.getNode(op.id);
      if (!n) return true;
      scene.updateNode({ ...n, title: op.title || n.title });
      return true;
    }
    case "set_spec": {
      const n = scene.getNode(op.id);
      if (!n) return true;
      const spec = { ...n.spec };
      for (const k of op.unset ?? []) delete spec[k];
      for (const [k, v] of Object.entries(op.patch ?? {})) spec[k] = v;
      scene.updateNode({ ...n, spec });
      return true;
    }
    case "set_state": {
      const n = scene.getNode(op.id);
      if (!n) return true;
      scene.updateNode({
        ...n,
        state: op.state,
        result: op.result ?? (op.state === "idle" ? undefined : n.result),
        error: op.error ?? (op.state === "idle" ? null : n.error),
      });
      return true;
    }
    case "add_edge": {
      const from = scene.getNode(op.edge.from.nodeId);
      const to = scene.getNode(op.edge.to.nodeId);
      if (!from || !to) return true;
      // 单入端口替换语义（与服务端一致）
      const targetPort = to.ports.inputs.find(
        (p) => p.id === op.edge.to.portId,
      );
      if (targetPort && !targetPort.multiple) {
        for (const e of scene.upstreamOf(to.id)) {
          if (e.to.portId === op.edge.to.portId) scene.removeEdge(e.id);
        }
      }
      scene.addEdge(op.edge);
      return true;
    }
    case "remove_edge":
      scene.removeEdge(op.id);
      return true;
    case "group": {
      for (const id of op.nodeIds) {
        const n = scene.getNode(id);
        if (n) scene.updateNode({ ...n, parentId: op.groupId });
      }
      return true;
    }
    case "ungroup": {
      for (const n of scene.childrenOf(op.groupId)) {
        scene.updateNode({ ...n, parentId: undefined });
      }
      return true;
    }
    case "set_parent": {
      const n = scene.getNode(op.id);
      if (n) scene.updateNode({ ...n, parentId: op.parentId });
      return true;
    }
    default:
      // set_viewport / set_settings：不属于场景层，交回内核
      return false;
  }
}
