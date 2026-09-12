import { expect, test, assertSpaServed } from './fixtures';

/**
 * ATK-18：5000 节点画布视口操作 ≥55 FPS。
 *
 * 这条用例早期是**结构性假绿**：它 `import('/src/features/canvas/kernel/index.ts')`
 * 并期望导出一个 `createKernel` 工厂，但
 *   a. e2e 打的是构建产物，产物里没有 `/src/*` 路径；
 *   b. 内核根本没有导出 `createKernel`（只有 `CanvasKernel` 类）。
 * 两个原因叠加 → 永远走 test 的跳过分支，在 CI 里表现为「跳过 = 通过」。
 * 现在改为通过 e2e 挂载点拿真实实现（web/e2e/mount.ts），并断言用例**确实跑起来了**。
 *
 * 设计要点（也是最容易写成「假通过」的地方）：
 *   - 不测「渲染 5000 个 DOM 节点」的耗时，而是测**内核在 5000 节点下的操作吞吐**；
 *   - 用 `requestAnimationFrame` 之间的间隔计算帧时间，而不是 `Date.now()` 差值——
 *     后者会把浏览器节流算进去，得到漂亮的假数字；
 *   - 预算常量来自 kernel/budget.ts（与 CI 门禁同一真源），不在这里硬编码。
 */
test.describe('画布性能预算（ATK-18）', () => {
  test('5000 节点下视口操作帧时间满足预算', async ({ page }) => {
    await assertSpaServed();
    await page.goto('/');
    // 断言挂载点可用：否则下面的 `unavailable` 分支会把「基础设施没搭好」
    // 伪装成「内核不支持」，从而长期跳过。
    const mounted = await page.evaluate(async () => {
      const entry = '/e2e-mount.js';
      await import(/* @vite-ignore */ entry).catch(() => null);
      const ic = (window as unknown as { __IC__?: { createKernel?: unknown } }).__IC__;
      return typeof ic?.createKernel === 'function';
    });
    expect(mounted, 'e2e 挂载点不可用：请以 E2E_MOUNT=1 构建前端产物').toBe(true);

    const perf = await page.evaluate(async () => {
      const ic = (window as unknown as {
        __IC__: {
          createKernel: (o: {
            nodes: Array<{ id: string; rect: { x: number; y: number; w: number; h: number } }>;
          }) => {
            dispatch: (cmd: unknown) => unknown;
            dispose?: () => void;
          };
          budget: { MIN_FPS: number; EDIT_FEEDBACK_MS: number };
        };
      }).__IC__;

      const NODES = 5000;
      const nodes = Array.from({ length: NODES }, (_, i) => ({
        id: `n_${i}`,
        rect: { x: (i % 100) * 340, y: Math.floor(i / 100) * 260, w: 320, h: 220 },
      }));

      const kernel = ic.createKernel({ nodes });
      // 预热，避免把首次 JIT 编译算进帧时间
      for (let i = 0; i < 20; i += 1) {
        kernel.dispatch({ type: 'pan', dx: 1, dy: 1 });
      }

      const frameTimes: number[] = [];
      let last = performance.now();
      await new Promise<void>((resolve) => {
        let frames = 0;
        const tick = () => {
          const now = performance.now();
          frameTimes.push(now - last);
          last = now;
          // 每帧做一次视口操作（模拟持续拖拽）
          kernel.dispatch({ type: 'pan', dx: 2, dy: 1 });
          frames += 1;
          if (frames < 120) requestAnimationFrame(tick);
          else resolve();
        };
        requestAnimationFrame(tick);
      });
      kernel.dispose?.();

      // 去掉最慢的 10% 再取平均：单次 GC/调度抖动不代表内核性能，
      // 但持续性的低帧率会被保留下来（这是我们要抓的）。
      const sorted = [...frameTimes].sort((a, b) => a - b);
      const kept = sorted.slice(0, Math.max(1, Math.floor(sorted.length * 0.9)));
      const avg = kept.reduce((s, v) => s + v, 0) / kept.length;
      const fps = 1000 / avg;

      return {
        fps,
        nodeCount: NODES,
        frames: frameTimes.length,
        minFps: ic.budget.MIN_FPS,
        editFeedbackMs: ic.budget.EDIT_FEEDBACK_MS,
      };
    });

    // 先断言「确实在测 5000 节点、确实采到了帧」，避免空跑出高帧率
    expect(perf.nodeCount).toBe(5000);
    expect(perf.frames, '未采集到足够帧数（用例没真正跑）').toBeGreaterThanOrEqual(100);

    const minFps = perf.minFps ?? 55;
    expect(
      perf.fps,
      `5000 节点下平均帧率 ${perf.fps.toFixed(1)} FPS，低于预算 ${minFps}`,
    ).toBeGreaterThanOrEqual(minFps);

    // 单次视口操作的本地反馈延迟也必须满足预算（拖拽手感的下限）
    const avgFrameMs = 1000 / perf.fps;
    expect(
      avgFrameMs,
      `平均帧时间 ${avgFrameMs.toFixed(2)}ms 超过单次编辑反馈预算 ${perf.editFeedbackMs}ms`,
    ).toBeLessThanOrEqual(perf.editFeedbackMs * 2);
  });

  test('产物体积未超预算', async ({ page }) => {
    // 首屏体积是「打开画布 < 1.5s」的最强相关因子；按 gzip 后衡量。
    // 早期断言是 `html.length > 0`——恒真，等于没测。
    const res = await page.goto('/');
    expect(res?.status()).toBeLessThan(400);

    // 收集页面实际加载的 JS 体积（用浏览器真实的资源计时，而不是自己拼路径）
    const sizes = await page.evaluate(() =>
      performance
        .getEntriesByType('resource')
        .filter((e) => (e as PerformanceResourceTiming).initiatorType === 'script')
        .map((e) => ({
          name: new URL((e as PerformanceResourceTiming).name).pathname,
          bytes: (e as PerformanceResourceTiming).encodedBodySize,
        })),
    );
    const total = sizes.reduce((s, x) => s + x.bytes, 0);
    expect(sizes.length, '页面没有加载任何脚本（用例没真正跑）').toBeGreaterThan(0);
    // 未压缩体积上限；gzip 后的门禁由 scripts/perf-budget.mjs 在构建产物上校验。
    // 这里用宽松上限是为了抓「误把整库打进首屏」这类数量级错误。
    expect(total, `首屏脚本体积 ${(total / 1024).toFixed(0)}KB 超过上限`).toBeLessThan(2_500_000);

    // e2e 挂载点**不应**出现在生产产物里（它是 e2e 专用，不该让用户下载）
    expect(
      sizes.some((s) => s.name.includes('e2e-mount')),
      'e2e-mount.js 被生产页面加载了（e2e 专用产物泄漏到生产）',
    ).toBe(false);
  });
});
