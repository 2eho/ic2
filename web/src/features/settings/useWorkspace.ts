/**
 * @deprecated 已迁移到 `@/shared/session/workspace`。
 *
 * 保留本文件是**迁移窗口**，不是长期入口：新代码一律用 shared 版本。
 * `make lint` 的特性边界检查会拦住新增的跨特性引用。
 */
export { useWorkspace, setSelectedWorkspace } from "@/shared/session/workspace";
