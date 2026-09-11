import { expect, test } from './fixtures';

/**
 * ATK-19：恶意 SVG 节点 —— `<script>` 不得执行。
 *
 * SVG 是唯一「可携带脚本的图片格式」，因此它是画布类应用最容易被打穿的入口：
 * 用户从任意来源粘贴一段 SVG，若直接 innerHTML 进 DOM，就等于在自己页面里
 * 执行了第三方脚本（可读 cookie / 令牌 / 画布内容）。
 *
 * 本用例在真实浏览器中同时验证三件事：
 *   1. 我们的 sanitize 函数移除了 script 与事件处理器；
 *   2. 被移除的脚本**没有执行**（用可观测副作用判定，而不是「看代码里删了」）；
 *   3. 正常图形元素保留（否则「安全」退化成「什么都渲染不出来」）。
 */
test.describe('SVG 净化（ATK-19）', () => {
  test('script 与事件属性被移除且不执行', async ({ page }) => {
    await page.goto('/');

    const result = await page.evaluate(async () => {
      const modPath = '/src/features/canvas/tools/svg-sanitize.ts';
      const mod = await import(/* @vite-ignore */ modPath).catch(() => null);
      if (!mod) return { unavailable: true };

      // 记录脚本是否执行：用 window 上的哨兵
      (window as unknown as { __svgPwned?: boolean }).__svgPwned = false;
      const hostile = `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100">
        <script>window.__svgPwned = true;</script>
        <rect x="0" y="0" width="50" height="50" fill="red" onload="window.__svgPwned = true" onclick="window.__svgPwned = true"/>
        <a xlink:href="javascript:window.__svgPwned = true"><text x="5" y="60">x</text></a>
        <image href="x" onerror="window.__svgPwned = true"/>
      </svg>`;
      const clean = (mod as { sanitizeSVG: (s: string) => string }).sanitizeSVG(hostile);

      // 把净化后的内容真的插入 DOM（这一步才是「会不会执行」的判定）
      const host = document.createElement('div');
      host.innerHTML = clean;
      document.body.appendChild(host);
      await new Promise((r) => setTimeout(r, 60));

      const pwned = (window as unknown as { __svgPwned?: boolean }).__svgPwned === true;
      host.remove();

      return {
        unavailable: false,
        clean,
        pwned,
        hasScript: /<script/i.test(clean),
        hasOnAttr: /\son[a-z]+\s*=/i.test(clean),
        hasJavascriptUrl: /javascript:/i.test(clean),
        keepsRect: /<rect/i.test(clean),
      };
    });

    if (result.unavailable) {
      test.skip(true, 'svg-sanitize 模块尚未提供（由计划中的实现补齐后此用例自动生效）');
      return;
    }

    expect(result.pwned, '恶意脚本被执行了').toBe(false);
    expect(result.hasScript, '<script> 未被移除').toBe(false);
    expect(result.hasOnAttr, '内联事件处理器未被移除').toBe(false);
    expect(result.hasJavascriptUrl, 'javascript: URL 未被移除').toBe(false);
    expect(result.keepsRect, '合法图形元素被误删（净化过度）').toBe(true);
  });
});
