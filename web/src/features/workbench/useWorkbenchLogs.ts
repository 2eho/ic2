import { useCallback, useEffect, useState } from "react";

/**
 * 工作台历史记录（本地持久化）。
 *
 * 为什么存在浏览器而不是服务端：
 *   历史记录是「一个人的操作回放」，与工作区协作无关；存服务端会让
 *   「我昨天试的那 20 次」出现在同事的列表里（原项目也没做共享）。
 *   真正的产物（图片/视频）已经在服务端资产库里，这里只存**参数与结果索引**，
 *   因此体积小、丢失代价低。
 *
 * 但有两个不能丢的东西（因此落 localStorage 而不是内存）：
 *   - 参数快照：用户要能「回填参数」重试；
 *   - 失败原因：用户要能区分「上游限流」和「参数错误」。
 */
export interface WorkbenchLog {
  id: string;
  createdAt: number;
  kind: "image" | "video";
  prompt: string;
  model: string;
  params: Record<string, unknown>;
  references: string[];
  /** 每个结果的独立状态：部分失败不能整条标失败（6.3 的核心要求）。 */
  results: Array<{
    id: string;
    status: "pending" | "succeeded" | "failed";
    assetId?: string;
    errorCode?: string;
    durationMs: number;
    width?: number;
    height?: number;
    bytes?: number;
  }>;
  totalMs: number;
}

const KEY = "ic.workbench.logs";
const MAX_LOGS = 50;

export function useWorkbenchLogs(kind: "image" | "video") {
  const [logs, setLogs] = useState<WorkbenchLog[]>([]);

  const reload = useCallback(() => {
    try {
      const raw = localStorage.getItem(KEY);
      if (!raw) {
        setLogs([]);
        return;
      }
      const all = JSON.parse(raw) as WorkbenchLog[];
      setLogs(all.filter((l) => l.kind === kind).slice(0, MAX_LOGS));
    } catch {
      // 历史损坏不应影响当前操作：清空即可（产物在服务端资产库里）
      setLogs([]);
    }
  }, [kind]);

  useEffect(() => {
    reload();
  }, [reload]);

  const persist = useCallback(
    (next: WorkbenchLog[]) => {
      try {
        const raw = localStorage.getItem(KEY);
        const others = raw
          ? (JSON.parse(raw) as WorkbenchLog[]).filter((l) => l.kind !== kind)
          : [];
        // 保留两个工作台各自的记录，且各自截断到上限
        localStorage.setItem(
          KEY,
          JSON.stringify([...next, ...others].slice(0, MAX_LOGS * 2)),
        );
      } catch {
        // 配额满：放弃持久化，但本次会话内仍可看到（状态已在内存里）
      }
    },
    [kind],
  );

  const addLog = useCallback(
    (log: WorkbenchLog) => {
      setLogs((prev) => {
        const next = [log, ...prev].slice(0, MAX_LOGS);
        persist(next);
        return next;
      });
    },
    [persist],
  );

  const updateLog = useCallback(
    (id: string, patch: Partial<WorkbenchLog>) => {
      setLogs((prev) => {
        const next = prev.map((l) => (l.id === id ? { ...l, ...patch } : l));
        persist(next);
        return next;
      });
    },
    [persist],
  );

  const removeLogs = useCallback(
    (ids: string[]) => {
      setLogs((prev) => {
        const next = prev.filter((l) => !ids.includes(l.id));
        persist(next);
        return next;
      });
    },
    [persist],
  );

  return { logs, addLog, updateLog, removeLogs, reload };
}
