/**
 * 画布内核入口。
 *
 * 硬约束（docs/design/01 §3）：本目录**不 import react**。
 * 通过 `npm run lint` 的依赖检查脚本强制（scripts/check-kernel-purity.mjs）。
 */
export * from './types';
export * from './viewport';
export * from './geometry';
export * from './scene';
export * from './commands';
export * from './undo';
export * from './interaction';
export { CanvasKernel } from './kernel';
export * from './schema';
