import { expect, test, WEB_BASE, assertSpaServed } from './fixtures';

/**
 * ATK-10：插件尝试访问宿主 DOM / Cookie / localStorage 必须被浏览器拦住。
 *
 * 为什么这条必须用真实浏览器：沙箱边界由浏览器实现，任何用 jsdom 或
 * 「检查我们自己的代码逻辑」的验证都是自证。只有真的把插件塞进
 * `sandbox="allow-scripts"` 的 iframe 里，才能确认它拿不到宿主。
 *
 * 早期版本有一条**结构性 bug**：宿主先 `contentDocument.open()` 再写内容。
 * 但 Chromium 对 sandbox（不给 allow-same-origin）的 iframe 直接返回
 * `contentDocument === null`（opaque origin），因此 `doc.open()` 抛
 * `Cannot read properties of null`，宿主脚本中断、iframe 从未被写入，
 * 用例永远拿不到探针结果 → 永远红。正确做法是**让宿主只能通过 postMessage
 * 与沙箱通信**（这也正是真实插件宿主的语义：宿主不该能直接摸沙箱的 DOM）。
 *
 * 另外必须避免 `srcdoc` 的时序坑：用 `document.createElement` 挂载并预先注册
 * message 监听，才能在脚本执行前就位（`setContent` + 静态 iframe 会丢消息）。
 */
test.describe('插件沙箱边界（ATK-10）', () => {
  test('sandbox iframe 无法访问宿主 DOM 与 Cookie', async ({ page }) => {
    await assertSpaServed();
    await page.goto('/');
    // 先种一个真实 cookie：否则「读不到 cookie」可能只是因为根本没有 cookie，
    // 这样断言就退化成恒真（这类「空断言」是最常见的假通过）。
    await page.evaluate(() => {
      document.cookie = 'host_session=HOST-COOKIE-VALUE; path=/';
    });

    const result = await page.evaluate(async () => {
      // 宿主事先放一个哨兵节点与 localStorage 值
      const host = document.createElement('div');
      host.id = 'host-secret';
      host.textContent = 'HOST-MARKER-9f3a';
      document.body.appendChild(host);
      localStorage.setItem('host_ls', 'HOST-LS-VALUE');

      const probe = new Promise<Record<string, string>>((resolve) => {
        window.addEventListener('message', (ev) => {
          if (ev.data && ev.data.__icProbe === true) resolve(ev.data.result as Record<string, string>);
        });
        setTimeout(
          () => resolve({ __timeout: 'NO_RESPONSE' }),
          5000,
        );
      });

      const frame = document.createElement('iframe');
      frame.setAttribute('sandbox', 'allow-scripts');
      // 用 srcdoc 而不是 contentDocument.write：
      // 后者在 sandbox（无 allow-same-origin）下拿不到 contentDocument。
      frame.srcdoc = `<scr${'ipt'}>
        const out = {};
        try { out.cookie = String(parent.document.cookie) || 'EMPTY'; }
        catch (e) { out.cookie = 'BLOCKED:' + e.name; }
        try { out.parentDom = parent.document.getElementById('host-secret').textContent; }
        catch (e) { out.parentDom = 'BLOCKED:' + e.name; }
        try { out.localStorage = String(parent.localStorage.getItem('host_ls')); }
        catch (e) { out.localStorage = 'BLOCKED:' + e.name; }
        try { out.topIsSelf = String(window.top === window); }
        catch (e) { out.topIsSelf = 'BLOCKED:' + e.name; }
        parent.postMessage({ __icProbe: true, result: out }, '*');
      </scr${'ipt'}>`;
      document.body.appendChild(frame);
      return probe;
    });

    expect(result.__timeout, '沙箱脚本完全没有回消息（用例本身失效，不是安全结论）').toBeUndefined();

    // 三条访问路径都必须被浏览器拦住（BLOCKED:*）
    expect(result.cookie).toMatch(/^BLOCKED/);
    expect(result.parentDom).toMatch(/^BLOCKED/);
    expect(result.localStorage).toMatch(/^BLOCKED/);
    // 且不得读到真实值（防止「返回了值但格式恰好匹配」的假绿）
    expect(String(result.cookie)).not.toContain('HOST-COOKIE-VALUE');
    expect(String(result.parentDom)).not.toContain('HOST-MARKER-9f3a');
    expect(String(result.localStorage)).not.toContain('HOST-LS-VALUE');
    // 反向证明探针确实跑起来了：它自己的 origin 是 opaque（不是宿主的 http://）
    expect(result.topIsSelf).toBe('false');
  });

  test('宿主侧协议校验拒绝未声明方法', async ({ page }) => {
    await assertSpaServed();
    await page.goto('/');
    // 用**仓库里的真实协议实现**，而不是在用例里重抄一份允许列表。
    // 抄一份的话，实现改了用例不会红——那测的是用例自己的拷贝，不是被测代码。
    // 通过 e2e 专用挂载点拿真实实现（见 web/e2e/mount.ts 与 vite.config.ts）。
    // 不能用 `/src/...`：e2e 打的是构建产物，产物里没有源码路径；
    // 也不能在用例里重抄一份协议表——那样实现改了用例不会红，测的是拷贝。
    const verdict = await page.evaluate(async () => {
      type Parse = (raw: unknown) => { type: string; method?: string } | null;
      // 用变量路径避免打包器把它当成静态依赖去解析磁盘文件。
      // 挂载点把实现同时放到了 globalThis.__IC__（见 e2e/mount.ts）——
      // 走 globalThis 而不是模块导出，是因为产物里模块导出名可能被重命名。
      const entry = '/e2e-mount.js';
      await import(/* @vite-ignore */ entry).catch(() => null);
      const mod = (window as unknown as { __IC__?: { parsePluginMessage?: Parse } }).__IC__ ?? null;
      if (!mod || typeof mod.parsePluginMessage !== 'function') return { loaded: false as const };
      const parse = mod.parsePluginMessage;
      const call = (method: string) => parse({ type: 'host:call', id: '1', method });
      return {
        loaded: true as const,
        // 已声明方法：应当通过
        allowsGet: call('node.get')?.method === 'node.get',
        // 未声明方法：必须丢弃（返回 null）
        rejectsDelete: call('node.delete') === null,
        rejectsEmpty: call('') === null,
        rejectsProto: call('__proto__') === null,
        rejectsCtor: call('constructor') === null,
        // 形状不对的消息也必须被拒（"尽力解析"会放过畸形输入）
        rejectsNoId: parse({ type: 'host:call', method: 'node.get' }) === null,
        rejectsScalar: parse('node.get') === null,
      };
    });

    expect(verdict.loaded, '未能加载真实协议模块（用例失效，不是安全结论）').toBe(true);
    if (!verdict.loaded) return;

    expect(verdict.allowsGet, '合法方法被误拒（协议校验过严）').toBe(true);
    expect(verdict.rejectsDelete, 'node.delete 未被拦住（越权可写）').toBe(true);
    expect(verdict.rejectsEmpty, '空方法名未被拦住').toBe(true);
    expect(verdict.rejectsProto, '__proto__ 原型污染键必须被拒').toBe(true);
    expect(verdict.rejectsCtor, 'constructor 必须被拒').toBe(true);
    expect(verdict.rejectsNoId, '缺少 id 的消息必须被拒').toBe(true);
    expect(verdict.rejectsScalar, '非对象消息必须被拒').toBe(true);
  });
});

/**
 * 同源托管的直接验证：探针页面与宿主**同源**时，沙箱仍然隔离。
 * 这一条防的是「隔离只是因为跨域」这一误解——即使同源，sandbox 也必须生效。
 */
test.describe('沙箱隔离与同源无关（ATK-10 补充）', () => {
  test('同源下 sandbox 依然隔离 DOM 与存储', async ({ page }) => {
    await assertSpaServed();
    await page.goto(WEB_BASE + '/');
    const result = await page.evaluate(async () => {
      const probe = new Promise<Record<string, string>>((resolve) => {
        window.addEventListener('message', (ev) => {
          if (ev.data && ev.data.__p2 === true) resolve(ev.data.result as Record<string, string>);
        });
        setTimeout(() => resolve({ __timeout: 'NO_RESPONSE' }), 5000);
      });
      const f = document.createElement('iframe');
      f.setAttribute('sandbox', 'allow-scripts');
      f.srcdoc = `<scr${'ipt'}>
        const out = {};
        // 用 location.origin：document.origin 已被规范移除，在 Chromium 上返回
        // undefined，用它断言会得到「undefined ≠ null」的假失败。
        try { out.origin = String(location.origin); } catch (e) { out.origin = 'THROWS:' + e.name; }
        try { out.href = parent.location.href; } catch (e) { out.href = 'BLOCKED:' + e.name; }
        parent.postMessage({ __p2: true, result: out }, '*');
      </scr${'ipt'}>`;
      document.body.appendChild(f);
      return probe;
    });
    expect(result.__timeout).toBeUndefined();
    // 沙箱文档的 origin 必须是 "null"（opaque origin），不是宿主 origin。
    // 这正是「同源也应隔离」的机制：sandbox 不给 allow-same-origin 时，
    // 浏览器把文档放进一个不透明来源，于是所有跨文档访问都被拒。
    expect(result.origin).toBe('null');
    // 且确实读不到宿主的 location（不是「没试」）
    expect(String(result.href)).toMatch(/^BLOCKED/);
  });
});
