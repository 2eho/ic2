import { useSyncExternalStore } from "react";
import type { CanvasKernel } from "../kernel";
import type { Selection } from "../kernel/types";

/** 订阅内核的粗粒度版本号：内核变更时返回自增快照，驱动 React 重渲染。 */
export function useKernelVersion(kernel: CanvasKernel): number {
  return useSyncExternalStore(
    (cb) => kernel.subscribe(cb),
    () => kernel.currentVersion,
  );
}

/** 订阅选择集。 */
export function useKernelSelection(kernel: CanvasKernel): Selection {
  return useSyncExternalStore(
    (cb) => kernel.subscribe(cb),
    () => kernel.currentSelection,
    () => kernel.currentSelection,
  );
}

/** 订阅单个节点的快照（避免整棵树重渲染）。 */
export function useKernelNode(kernel: CanvasKernel, nodeId: string) {
  return useSyncExternalStore(
    (cb) => kernel.subscribe(cb),
    () => kernel.scene.getNode(nodeId),
  );
}

/** 订阅视口（只用于需要显示数值的组件，如缩放百分比）。 */
export function useKernelViewport(kernel: CanvasKernel) {
  return useSyncExternalStore(
    (cb) => kernel.subscribeViewport(() => cb()),
    () => kernel.viewport.current,
    () => kernel.viewport.current,
  );
}
