import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '@/shared/api';

const SELECTED_KEY = 'ic.workspace.selected';

/**
 * 当前工作区。所有 API 调用都必须带 workspace，避免跨工作区串数据（INV-10）。
 * 选择结果持久化，但**服务端仍会校验归属**——前端选择不是授权依据。
 */
export function useWorkspace(): { workspaceId: string; role: string; ready: boolean } {
  const q = useQuery({ queryKey: ['me'], queryFn: api.me, retry: false });
  return useMemo(() => {
    const list = q.data?.workspaces ?? [];
    if (list.length === 0) return { workspaceId: '', role: '', ready: false };
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

export function setSelectedWorkspace(id: string): void {
  try {
    localStorage.setItem(SELECTED_KEY, id);
  } catch {
    // 忽略
  }
}
