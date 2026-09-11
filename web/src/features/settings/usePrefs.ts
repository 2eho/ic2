import { useCallback, useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/shared/api";
import type { WorkspacePrefs } from "@/shared/api/endpoints";

/**
 * 工作区偏好（服务端权威）。
 *
 * 与「本地偏好」的分工（这一点必须清楚，否则会来回摇摆）：
 *   - **服务端**：主题、语言、默认模型、生成偏好、同步开关 → 多端一致、可备份；
 *   - **本地**：仅「当前选中工作区」「本机桥接器地址/令牌」这类**设备级**信息，
 *     它们换设备本来就该不同。
 *
 * 乐观更新：主题/语言这类切换必须立即生效（等一个往返会明显卡顿）。
 * 失败时回滚并提示——静默回滚会让用户以为「点了没反应」。
 */
export function usePrefs(workspaceId: string, ready: boolean) {
  const qc = useQueryClient();
  const [local, setLocal] = useState<WorkspacePrefs | null>(null);

  const query = useQuery({
    queryKey: ["prefs", workspaceId],
    queryFn: () => api.getPrefs(workspaceId),
    enabled: ready,
    retry: false,
  });

  useEffect(() => {
    if (query.data?.prefs) setLocal(query.data.prefs);
  }, [query.data]);

  const save = useMutation({
    mutationFn: (patch: WorkspacePrefs) => api.updatePrefs(workspaceId, patch),
    onSuccess: (data) => {
      setLocal(data.prefs);
      void qc.invalidateQueries({ queryKey: ["prefs", workspaceId] });
    },
    onError: () => {
      // 回滚到服务端值：否则界面会停留在「看起来成功了」的状态
      void qc.invalidateQueries({ queryKey: ["prefs", workspaceId] });
    },
  });

  const update = useCallback(
    (patch: WorkspacePrefs) => {
      setLocal((prev) => ({ ...(prev ?? {}), ...patch }));
      save.mutate(patch);
    },
    [save],
  );

  return {
    prefs: local ?? query.data?.prefs ?? {},
    secrets: query.data?.secrets ?? {},
    loading: query.isLoading,
    saving: save.isPending,
    error: save.error
      ? ((save.error as { code?: string }).code ?? "internal")
      : null,
    update,
    reload: () => query.refetch(),
  };
}
