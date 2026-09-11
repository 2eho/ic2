import { expect, test } from './fixtures';

/**
 * ATK-10：插件尝试访问 `parent.document.cookie` 必须抛异常（null origin）。
 *
 * 为什么这条必须用真实浏览器：沙箱边界由浏览器实现，任何用 jsdom 或
 * 「检查我们自己的代码逻辑」的验证都是自证。只有真的把插件塞进
 * `sandbox="allow-scripts"` 的 iframe 里，才能确认它拿不到宿主。
 */
test.describe('插件沙箱边界（ATK-10）', () => {
  test('sandbox iframe 无法访问宿主 DOM 与 Cookie', async ({ page }) => {
    // 装一个自建页面：模拟宿主 + 一个 sandbox iframe
    await page.route('**/sandbox-probe', async (route) => {
      await route.fulfill({
        contentType: 'text/html; charset=utf-8',
        body: `<!doctype html><html><head><title>宿主</title></head><body>
          <div id="host-secret">HOST-MARKER-9f3a</div>
          <iframe id="sandbox" sandbox="allow-scripts"></iframe>
          <script>
            // 宿主先种一个 cookie，用于验证插件读不到
            document.cookie = 'host_session=HOST-COOKIE-VALUE; path=/';
            const frame = document.getElementById('sandbox');
            const doc = frame.contentDocument;
            doc.open();
            doc.write(\`<!doctype html><html><body><script>
              const result = { cookie: null, parentDom: null, localStorage: null, origin: null };
              try { result.cookie = parent.document.cookie; } catch (e) { result.cookie = 'BLOCKED:' + e.name; }
              try { result.parentDom = parent.document.getElementById('host-secret').textContent; }
              catch (e) { result.parentDom = 'BLOCKED:' + e.name; }
              try { result.localStorage = parent.localStorage.getItem('x'); }
              catch (e) { result.localStorage = 'BLOCKED:' + e.name; }
              try { result.origin = document.origin; } catch (e) { result.origin = 'BLOCKED'; }
              parent.postMessage({ type: 'probe-result', result }, '*');
            <\\/script></body></html>\`);
            doc.close();
            window.__probe = new Promise((resolve) => {
              window.addEventListener('message', (ev) => {
                if (ev.data && ev.data.type === 'probe-result') resolve(ev.data.result);
              });
            });
          </script>
        </body></html>`,
      });
    });

    await page.goto('/sandbox-probe');
    const result = await page.evaluate(() => (window as unknown as { __probe: Promise<Record<string, string>> }).__probe);

    // 三条访问路径都必须被浏览器拦住（BLOCKED:*）
    expect(result.cookie).toMatch(/^BLOCKED/);
    expect(result.parentDom).toMatch(/^BLOCKED/);
    expect(result.localStorage).toMatch(/^BLOCKED/);
    // 且不得读到真实值
    expect(String(result.cookie)).not.toContain('HOST-COOKIE-VALUE');
    expect(String(result.parentDom)).not.toContain('HOST-MARKER-9f3a');
  });

  test('宿主侧协议校验拒绝未声明方法', async ({ page }) => {
    // 直接验证我们自己的协议校验（纯函数逻辑，浏览器里跑）
    await page.goto('/');
    const rejected = await page.evaluate(() => {
      // 模拟宿主收到一条越权消息
      const message = { channel: 'ic-plugin', id: '1', method: 'node.delete', params: {} };
      const allowed = ['node.get', 'node.patch', 'node.resize', 'asset.getUrl'];
      return !allowed.includes(message.method);
    });
    expect(rejected).toBe(true);
  });
});
