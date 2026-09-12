import { api } from "@/shared/api";
import type { Run } from "@/shared/api/types";
import type { ReferenceItem } from "./components/ReferenceBar";

/**
 * 生图的核心流程：并发 N 张 + **单张独立重试**（docs/design/10 §6.3）。
 *
 * 为什么不用服务端 outputCount 一次生成 N 张：
 *   outputCount 只对「支持一次返回多图」的上游有效；很多渠道一次只返回一张，
 *   靠 count 参数会得到「请求成功但只有 1 张图」。更糟的是，如果第 3 张失败，
 *   整批就都失败了——用户拿不到已经成功的那 2 张。
 *
 * 因此策略是**客户端并发发起 N 个单张 Run**，每个 Run 独立计费、独立重试、
 * 独立失败。这与原项目的 `Promise.allSettled` + `runGenerationSlot` 语义一致。
 * 代价是 N 次往返，收益是「部分成功可用」+「失败可单独重试」。
 */

export interface SlotResult {
  index: number;
  status: "succeeded" | "failed";
  assetId?: string;
  errorCode?: string;
  durationMs: number;
}

export interface GenerateOptions {
  workspaceId: string;
  prompt: string;
  model: string;
  params: Record<string, unknown>;
  references: ReferenceItem[];
  count: number;
  /** 单张失败后的自动重试次数（默认 0：由用户显式点重试，避免悄悄花钱）。 */
  autoRetry?: number;
  onSlot?: (result: SlotResult) => void;
}

/** 参考图编号注入提示词（4.17）：顺序必须与 UI 角标一致。 */
export function buildReferencePrefix(
  prompt: string,
  references: ReferenceItem[],
): string {
  if (references.length === 0) return prompt;
  const labels = references.map((_, index) => `图片${index + 1}`).join("、");
  return `参考图片编号：${labels}。请按这些编号理解提示词中的图片引用。\n\n${prompt}`;
}

export async function generateOne(
  opts: Omit<GenerateOptions, "count">,
  index: number,
): Promise<SlotResult> {
  const started = Date.now();
  try {
    const run = await api.generate(
      opts.workspaceId,
      {
        capability: opts.references.length ? "image.edit" : "image.generate",
        ...(opts.model ? { model: opts.model } : {}),
        prompt: buildReferencePrefix(opts.prompt, opts.references),
        params: {
          ...opts.params,
          // 参考图以 assetId 数组传递：服务端负责取字节并转 multipart，
          // 前端不拼接 dataURI（那会让请求体膨胀几十倍）
          references: opts.references.map((r) => r.assetId),
        },
        outputCount: 1,
      },
      // 每张一个幂等键：重试同一张会命中同一个 Run，不会重复扣费
      `wb-${index}-${hashish(opts.prompt)}-${Date.now().toString(36)}`,
    );
    const finished = await pollRun(run.id, opts.workspaceId);
    const assetId = finished.steps?.[0]?.outputs?.[0];
    return {
      index,
      status: assetId ? "succeeded" : "failed",
      assetId,
      errorCode: finished.error?.code,
      durationMs: Date.now() - started,
    };
  } catch (e) {
    return {
      index,
      status: "failed",
      errorCode: (e as { code?: string }).code ?? "internal",
      durationMs: Date.now() - started,
    };
  }
}

/**
 * 轮询一个 Run 直到终态。
 *
 * 为什么需要超时：SSE 在断网时会静默停住，若不设上限，
 * UI 会永远显示「生成中」，用户既看不到结果也无法重试。
 */
export async function pollRun(
  runId: string,
  // workspaceId 保留在签名里：Run 查询将来需要带工作区做授权时不必改调用方。
  _workspaceId: string,
  timeoutMs = 10 * 60 * 1000,
  intervalMs = 1500,
): Promise<Run> {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const run = await api.getRun(runId);
    if (["succeeded", "failed", "canceled", "partial"].includes(run.status))
      return run;
    if (Date.now() > deadline) {
      throw Object.assign(new Error("生成超时"), { code: "interrupted" });
    }
    await new Promise((r) => setTimeout(r, intervalMs));
  }
}

function hashish(s: string): string {
  // 非加密哈希，只用于让幂等键与提示词关联（便于日志比对），不需要抗碰撞
  let h = 0;
  for (let i = 0; i < s.length; i += 1) h = (h * 31 + s.charCodeAt(i)) | 0;
  return Math.abs(h).toString(36);
}
