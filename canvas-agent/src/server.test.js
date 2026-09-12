import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { originAllowed, bearerToken } from './config.js';

// 直接测安全判定的纯函数：这些是「一旦错了就是漏洞」的逻辑，
// 必须在单元层穷举，而不是靠集成测试碰运气。

const cfg = {
  token: 'test-token-value',
  allowedOrigins: ['http://localhost:5173', 'http://127.0.0.1:8080'],
};

test('Origin 白名单：精确匹配', () => {
  assert.equal(originAllowed(cfg, 'http://localhost:5173'), true);
  assert.equal(originAllowed(cfg, 'http://127.0.0.1:8080'), true);
});

test('Origin 白名单：拒绝未登记站点（防 DNS rebinding / CSRF）', () => {
  assert.equal(originAllowed(cfg, 'https://evil.example.com'), false);
  assert.equal(originAllowed(cfg, 'http://localhost:9999'), false);
  // 前缀相似但不相同也要拒绝
  assert.equal(originAllowed(cfg, 'http://localhost:51733'), false);
  assert.equal(originAllowed(cfg, 'http://localhost:5173.evil.com'), false);
});

test('Origin 白名单：缺失 Origin 视为不通过', () => {
  assert.equal(originAllowed(cfg, undefined), false);
  assert.equal(originAllowed(cfg, ''), false);
});

test('令牌解析：只接受 Bearer 前缀（不接受 query 传参）', () => {
  assert.equal(bearerToken({ headers: { authorization: 'Bearer abc123' } }), 'abc123');
  assert.equal(bearerToken({ headers: { authorization: 'bearer abc123' } }), 'abc123');
  assert.equal(bearerToken({ headers: { authorization: 'abc123' } }), '');
  assert.equal(bearerToken({ headers: {} }), '');
});

test('CORS 不回显通配符', async () => {
  // 用真实 HTTP 请求验证：非白名单 Origin 必须 403，且不带 Access-Control-Allow-Origin
  const { createBridgeServer } = await import('./server.js');
  const { server } = createBridgeServer(cfg, { log() {}, error() {} });
  await new Promise((r) => server.listen(0, '127.0.0.1', r));
  const port = server.address().port;
  try {
    const res = await fetch(`http://127.0.0.1:${port}/healthz`, {
      headers: { Origin: 'https://evil.example.com' },
    });
    assert.equal(res.status, 403);
    assert.equal(res.headers.get('access-control-allow-origin'), null, '不得回显非白名单 Origin');
  } finally {
    server.close();
  }
});

test('无令牌访问被拒绝', async () => {
  const { createBridgeServer } = await import('./server.js');
  const { server } = createBridgeServer(cfg, { log() {}, error() {} });
  await new Promise((r) => server.listen(0, '127.0.0.1', r));
  const port = server.address().port;
  try {
    const res = await fetch(`http://127.0.0.1:${port}/healthz`);
    assert.equal(res.status, 401);
  } finally {
    server.close();
  }
});

test('错误令牌被拒绝；正确令牌可访问 healthz', async () => {
  const { createBridgeServer } = await import('./server.js');
  const { server } = createBridgeServer(cfg, { log() {}, error() {} });
  await new Promise((r) => server.listen(0, '127.0.0.1', r));
  const port = server.address().port;
  try {
    const bad = await fetch(`http://127.0.0.1:${port}/healthz`, {
      headers: { Authorization: 'Bearer wrong-token' },
    });
    assert.equal(bad.status, 401);

    const good = await fetch(`http://127.0.0.1:${port}/healthz`, {
      headers: { Authorization: `Bearer ${cfg.token}` },
    });
    assert.equal(good.status, 200);
    const body = await good.json();
    assert.equal(body.status, 'ok');
    assert.ok(Array.isArray(body.backends));
  } finally {
    server.close();
  }
});

test('令牌长度不同也拒绝（防止 pad 截断绕过）', async () => {
  const { createBridgeServer } = await import('./server.js');
  const { server } = createBridgeServer(cfg, { log() {}, error() {} });
  await new Promise((r) => server.listen(0, '127.0.0.1', r));
  const port = server.address().port;
  try {
    for (const token of ['test-token-valu', 'test-token-valueX', '', 'test']) {
      const res = await fetch(`http://127.0.0.1:${port}/healthz`, {
        headers: { Authorization: `Bearer ${token}` },
      });
      assert.equal(res.status, 401, `令牌 "${token}" 不应通过`);
    }
  } finally {
    server.close();
  }
});
