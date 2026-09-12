import { expect, test, apiCall, WEB_BASE } from './fixtures';

/**
 * ATK-19：恶意 SVG —— `<script>` 不得执行。
 *
 * 早期版本的这条用例是**结构性假绿**：
 *   1. 它 import `/src/features/canvas/tools/svg-sanitize.ts`，但**该模块在仓库里
 *      根本不存在**，于是永远走 `test.skip`（在 CI 里表现为「通过」）；
 *   2. 即使模块存在，它测的也是「净化一个字符串」，而不是「资产能不能被当成
 *      可执行文档打开」——后者才是真实攻击面。
 *
 * 真实攻击面（已实测确认）：SVG 是唯一可携带脚本的图片格式。
 * 用户 A 上传 `xss.svg`（Content-Type: image/svg+xml）→ 同源地址
 * `GET /api/v1/assets/{id}/raw` **原样**返回，无 `nosniff`、无 CSP →
 * 把该地址发给用户 B，B 打开就在**应用同源下执行了 A 的脚本**，
 * 可读 cookie / localStorage（含会话令牌）/ 以 B 的身份调 API。
 *
 * 因此本用例断言的是**服务端的响应契约**，不是前端某个函数的返回值：
 *   - 可执行类型（SVG）必须以不可执行的 Content-Type + nosniff 下发；
 *   - 其余类型也不得让浏览器「嗅探成 HTML」；
 *   - 并且真的在浏览器里打开该地址，确认脚本没有执行。
 */
test.describe('恶意 SVG 不下发为可执行文档（ATK-19）', () => {
  const HOSTILE_SVG = `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100">
    <script>window.name='PWNED-TOP-LEVEL'</script>
    <rect x="0" y="0" width="50" height="50" fill="red" onclick="window.name='PWNED-CLICK'"/>
  </svg>`;

  async function uploadSvg(session: { token: string; workspaceId: string }, body: string) {
    // 走 FormData 真实上传（不经任何 mock），拿服务端真实判定
    const form = new FormData();
    form.append(
      'file',
      new Blob([body], { type: 'image/svg+xml' }),
      'hostile.svg',
    );
    const res = await fetch(
      `${WEB_BASE}/api/v1/workspaces/${session.workspaceId}/assets`,
      { method: 'POST', headers: { Authorization: `Bearer ${session.token}` }, body: form },
    );
    expect(res.status, '上传恶意 SVG 本身应当被接受（内容不可判断意图）').toBe(201);
    return (await res.json()) as { id: string; mime: string };
  }

  test('raw 资产不得以可执行的 SVG/HTML 类型下发，且带 nosniff', async ({ session }) => {
    const asset = await uploadSvg(session, HOSTILE_SVG);
    const url = `${WEB_BASE}/api/v1/assets/${asset.id}/raw?workspaceId=${session.workspaceId}`;

    const res = await fetch(url);
    expect(res.status).toBe(200);
    const ct = (res.headers.get('content-type') ?? '').toLowerCase();

    // 1) 不得是可执行文档类型
    expect(ct, `SVG 被以 ${ct} 下发，可在同源下执行脚本`).not.toContain('image/svg+xml');
    expect(ct).not.toContain('text/html');
    expect(ct).not.toContain('application/xhtml');

    // 2) 必须显式禁止嗅探：没有 nosniff 时，text/plain 也会被 IE/部分场景嗅探成 HTML
    expect(
      (res.headers.get('x-content-type-options') ?? '').toLowerCase(),
      '缺少 X-Content-Type-Options: nosniff',
    ).toBe('nosniff');

    // 3) 响应体**字节原样**下发是可以接受的（不是下载器，不该改写用户内容），
    //    关键是不被当作文档执行——这不取决于字节，而取决于上面的类型与 nosniff。
    //    这里只断言「字节确实是用户的 SVG」（证明我们确实在做类型降级而不是把内容吞了），
    //    以及内容不会被内联渲染成可交互文档。
    const text = await res.text();
    expect(text).toContain('<svg');
    expect(
      (res.headers.get('content-security-policy') ?? '').toLowerCase(),
      '降级类型时缺少 CSP sandbox（缺少时仍可能被内联渲染）',
    ).toContain('sandbox');
  });

  test('在真实浏览器里打开该地址不会执行脚本', async ({ page, session }) => {
    const asset = await uploadSvg(session, HOSTILE_SVG);
    const url = `${WEB_BASE}/api/v1/assets/${asset.id}/raw?workspaceId=${session.workspaceId}`;

    await page.goto(url, { waitUntil: 'domcontentloaded' });
    await page.waitForTimeout(300);

    // 判据必须是**可观测副作用**，而不是「看响应里的字符串」：
    // 脚本若真能跑，window.name 会被改。
    const name = await page.evaluate(() => window.name);
    expect(name, 'SVG 内的脚本在应用同源下执行了（存储型 XSS）').toBe('');

    // 同时验证「不只是这一次请求恰好被拒」：确认页面是以纯文本（不可执行文档）
    // 呈现的。用 document.contentType 而不是 content() 文本匹配——
    // 后者会因为「纯文本里当然包含这段字面量」而产生假失败。
    const contentType = await page.evaluate(() => document.contentType);
    expect(contentType, `页面以 ${contentType} 渲染，脚本有机会执行`).not.toContain('svg');
    expect(contentType).not.toContain('html');
  });

  test('伪装成 image/png 的 HTML 也不得被嗅探为文档', async ({ session }) => {
    // 同一类攻击的另一条路径：把 HTML 声明成图片，靠浏览器嗅探执行。
    const form = new FormData();
    form.append(
      'file',
      new Blob(['<html><body><script>window.name="PWNED-PNG"</script></body></html>'], {
        type: 'image/png',
      }),
      'evil.png',
    );
    const up = await fetch(
      `${WEB_BASE}/api/v1/workspaces/${session.workspaceId}/assets`,
      { method: 'POST', headers: { Authorization: `Bearer ${session.token}` }, body: form },
    );
    const asset = (await up.json()) as { id: string };
    const res = await fetch(
      `${WEB_BASE}/api/v1/assets/${asset.id}/raw?workspaceId=${session.workspaceId}`,
    );
    const ct = (res.headers.get('content-type') ?? '').toLowerCase();
    expect(ct).not.toContain('text/html');
    expect((res.headers.get('x-content-type-options') ?? '').toLowerCase()).toBe('nosniff');
  });

  test('上传接口对声明的 MIME 与实际内容不一致时不谎报类型', async ({ session }) => {
    // 服务端应当以**内容嗅探结果**（或安全兜底类型）为准，而不是原样采信客户端声明。
    // 否则「声明成什么就下发给什么」等于把 XSS 的开关交给上传者。
    const form = new FormData();
    form.append(
      'file',
      new Blob(['not actually an svg at all'], { type: 'image/svg+xml' }),
      'lying.svg',
    );
    const up = await fetch(
      `${WEB_BASE}/api/v1/workspaces/${session.workspaceId}/assets`,
      { method: 'POST', headers: { Authorization: `Bearer ${session.token}` }, body: form },
    );
    const dto = (await up.json()) as { id: string; mime: string };
    const meta = await apiCall<{ mime: string }>(`/api/v1/assets/${dto.id}?workspaceId=${session.workspaceId}`, {
      token: session.token,
    });
    expect(
      meta.data.mime,
      '服务端原样采信了客户端声明的 image/svg+xml（等于把可执行类型交给上传者选择）',
    ).not.toBe('image/svg+xml');
  });
});
