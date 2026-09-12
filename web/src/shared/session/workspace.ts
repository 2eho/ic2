import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/shared/api";

const SELECTED_KEY = "ic.workspace.selected";

/**
 * 当前工作区。所有 API 调用都必须带 workspace，避免跨工作区串数据（INV-10）。
 * 选择结果持久化，但**服务端仍会校验归属**——前端选择不是授权依据。
 *
 * 落位说明：本模块从 `features/settings/useWorkspace` 迁到 `shared/session`。
 * 它提供的是「会话上下文」，被 canvas / agent / workbench / assets / prompts 共同依赖；
 * 放在 settings 特性下会迫使所有特性跨目录依赖 settings，
 * 这正是 `scripts/check-features-boundary.mjs` 要拦住的耦合形态。
 */
export function useWorkspace(): {
  workspaceId: string;
  role: string;
  ready: boolean;
} {
  const q = useQuery({ queryKey: ["me"], queryFn: api.me, retry: false });
  return useMemo(() => {
    const list = q.data?.workspaces ?? [];
    if (list.length === 0) return { workspaceId: "", role: "", ready: false };
    let stored: string | null = null;
    try {
      stored = localStorage.getItem(SELECTED_KEY);
    } catch {
      stored = null;
    }
    const picked = list.find((w) => w.id === stored) ?? list[0];
    return { workspaceId: picked.id, role: picked.role, ready: true };
  }, [q.data]);
}

/** 切换当前工作区（写入本地偏好；服务端仍会校验归属）。 */
export function setSelectedWorkspace(id: string): void {
  try {
    localStorage.setItem(SELECTED_KEY, id);
  } catch {
    // 隐私模式下 localStorage 不可用，退回默认（第一个）工作区
  }
}
