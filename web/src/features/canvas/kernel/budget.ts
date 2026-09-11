/**
 * 画布内核性能预算（唯一真源）。
 *
 * 这些常量同时被三处使用，因此必须只有一处定义：
 *   - 内核实现（视口裁剪的 padding、命中测试的提前返回阈值）；
 *   - `scripts/perf-budget.mjs`（CI 门禁校验常量存在）；
 *   - `web/e2e/perf.spec.ts`（ATK-18 用真实浏览器断言）。
 *
 * 改动任何数值都必须同步 `docs/design/13-verification-and-iteration.md` §3.3，
 * 否则 check-boundaries 会红——「文档写 55、代码写别的」这类漂移必须被拦住。
 */

/** 视口操作最低帧率（5000 节点场景，ATK-18）。 */
export const MIN_FPS = 55;

/** 视口裁剪外扩像素：预渲染屏幕外一圈，避免快速平移时出现空白。 */
export const VIEWPORT_PAD = 280;

/** 单次命中测试的最长耗时（毫秒）。超过说明退化成了线性全扫描。 */
export const MAX_HIT_TEST_MS = 2;

/** 打开 1000 节点画布到可交互的目标耗时（毫秒）。 */
export const OPEN_CANVAS_MS = 1500;

/** 本地编辑反馈延迟（拖拽节点时从指针事件到画面更新的目标，毫秒）。 */
export const EDIT_FEEDBACK_MS = 16;

/** 撤销栈深度（与原项目一致，交互心智不退化）。 */
export const UNDO_LIMIT = 50;
