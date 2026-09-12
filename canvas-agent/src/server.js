/**
 * 桥接器 HTTP 服务。
 *
 * 端点（全部要求 Bearer 令牌 + Origin 白名单）：
 *   GET  /healthz          探测桥接器是否在跑（返回可用后端列表）
 *   POST /turns            提交一轮输入，SSE 流回归一化后的 Item
 *   POST /stop             取消当前轮次
 *
 * 安全实现的三条硬约束（每条都对应一个真实攻击面）：
 *   1. 仅绑定 127.0.0.1 —— 否则同网段可直连；
 *   2. 拒绝非白名单 Origin —— 仅校验令牌挡不住「任意网页借浏览器访问本机端口」；
 *   3. 令牌用时间恒定比较 —— 避免逐字节比较的时序侧信道。
 */
import { createServer } from 'node:http';
import { timingSafeEqual } from 'node:crypto';
import { bearerToken, originAllowed } from './config.js';
import { detectBackends, parseLine, startBackend } from './backends.js';

/** 时间恒定比较：长度不同时也走一遍比较，避免用长度泄露信息。 */
function tokenMatches(expected, got) {
  const a = Buffer.from(expected, 'utf8');
  const b = Buffer.from(got.padEnd(expected.length, '\0').slice(0, expected.length), 'utf8');
  if (a.length !== b.length) {
    // 长度不等也要消耗一次比较时间
    timingSafeEqual(a, a);
    return false;
  }
  return timingSafeEqual(a, b) && got.length === expected.length;
}

export function createBridgeServer(cfg, logger = console) {
  const backends = detectBackends(cfg);
  /** 当前活跃轮次：一次只允许一个（本机 CLI 是单会话的）。 */
  let active = null;

  const server = createServer((req, res) => {
    const origin = req.headers.origin;

    // CORS：只回显白名单内的 Origin。不回显 `*`——那等于允许任意站点读响应。
    if (origin && !originAllowed(cfg, origin)) {
      res.writeHead(403, { 'Content-Type': 'application/json; charset=utf-8' });
      res.end(JSON.stringify({ code: 'forbidden', message: 'Origin 不在白名单内' }));
      return;
    }
    if (origin) {
      res.setHeader('Access-Control-Allow-Origin', origin);
      res.setHeader('Vary', 'Origin');
      res.setHeader('Access-Control-Allow-Headers', 'Authorization, Content-Type');
      res.setHeader('Access-Control-Allow-Methods', 'GET, POST, OPTIONS');
    }
    if (req.method === 'OPTIONS') {
      res.writeHead(204);
      res.end();
      return;
    }
    if (!tokenMatches(cfg.token, bearerToken(req))) {
      res.writeHead(401, { 'Content-Type': 'application/json; charset=utf-8' });
      res.end(JSON.stringify({ code: 'unauthorized', message: '令牌缺失或无效' }));
      return;
    }

    const url = new URL(req.url ?? '/', `http://${req.headers.host ?? '127.0.0.1'}`);

    if (req.method === 'GET' && url.pathname === '/healthz') {
      res.writeHead(200, { 'Content-Type': 'application/json; charset=utf-8' });
      res.end(JSON.stringify({ status: 'ok', backends, active: active !== null }));
      return;
    }

    if (req.method === 'POST' && url.pathname === '/stop') {
      if (active) {
        active.stop();
        active = null;
      }
      res.writeHead(200, { 'Content-Type': 'application/json; charset=utf-8' });
      res.end(JSON.stringify({ ok: true }));
      return;
    }

    if (req.method === 'POST' && url.pathname === '/turns') {
      readJSON(req, (err, body) => {
        if (err) {
          res.writeHead(400, { 'Content-Type': 'application/json; charset=utf-8' });
          res.end(JSON.stringify({ code: 'invalid_request', message: err.message }));
          return;
        }
        if (active) {
          // 明确拒绝并发而不是排队：本机 CLI 是单会话的，
          // 排队会让用户以为「发出去没反应」，而拒绝能立刻给出可行动提示。
          res.writeHead(409, { 'Content-Type': 'application/json; charset=utf-8' });
          res.end(JSON.stringify({ code: 'conflict', message: '上一个轮次仍在进行' }));
          return;
        }
        const backend = pickBackend(cfg, backends, body.backend);
        if (!backend) {
          res.writeHead(422, { 'Content-Type': 'application/json; charset=utf-8' });
          res.end(JSON.stringify({
            code: 'invalid_request',
            message: '本机未检测到可用的 Agent 后端（需要 codex 或 claude CLI）',
          }));
          return;
        }
        streamTurn(res, backend, body, logger, (session) => {
          active = session;
        }, () => {
          active = null;
        });
      });
      return;
    }

    res.writeHead(404, { 'Content-Type': 'application/json; charset=utf-8' });
    res.end(JSON.stringify({ code: 'not_found', message: url.pathname }));
  });

  return { server, backends };
}

function pickBackend(cfg, detected, requested) {
  if (requested && requested !== 'auto') return detected.includes(requested) ? requested : null;
  if (cfg.backend && cfg.backend !== 'auto') return detected.includes(cfg.backend) ? cfg.backend : null;
  return detected[0] ?? null;
}

function readJSON(req, cb) {
  let raw = '';
  req.on('data', (c) => {
    raw += c;
    // 上限保护：桥接器只接受简短指令，超长输入说明调用方搞错了端点
    if (raw.length > 1 << 20) {
      cb(new Error('请求体过大'));
      req.destroy();
    }
  });
  req.on('end', () => {
    if (!raw) return cb(null, {});
    try {
      cb(null, JSON.parse(raw));
    } catch (e) {
      cb(new Error('请求体不是合法 JSON'));
    }
  });
}

function streamTurn(res, backend, body, logger, onStart, onEnd) {
  const turnId = body.turnId ?? `turn_${Date.now()}`;
  res.writeHead(200, {
    'Content-Type': 'text/event-stream; charset=utf-8',
    'Cache-Control': 'no-cache, no-transform',
    Connection: 'keep-alive',
    'X-Accel-Buffering': 'no',
  });

  let closed = false;
  const send = (event) => {
    if (closed) return;
    res.write(`event: item\n`);
    res.write(`data: ${JSON.stringify(event)}\n\n`);
  };
  const heartbeat = setInterval(() => {
    if (!closed) res.write(': ping\n\n');
  }, 15_000);

  const session = startBackend(
    backend,
    // 注意：startBackend 需要配置对象；这里直接把命令配置透传
    { codexCommand: body.codexCommand ?? 'codex', claudeCommand: body.claudeCommand ?? 'claude' },
    (line) => {
      for (const item of parseLine(line, backend, { turnId })) send(item);
    },
    (code, signal) => {
      if (closed) return;
      res.write(`event: done\n`);
      res.write(`data: ${JSON.stringify({ turnId, exitCode: code, signal })}\n\n`);
      cleanup();
    },
  );
  onStart(session);

  function cleanup() {
    closed = true;
    clearInterval(heartbeat);
    session.stop();
    onEnd();
    res.end();
  }

  res.on('close', cleanup);
  req_write(session, body);
}

function req_write(session, body) {
  const payload = body.input ?? '';
  // Codex app-server 与 Claude 的输入格式不同，但两者都接受一行 JSON/文本
  session.send(JSON.stringify({ type: 'user_message', text: payload }));
  if (typeof payload === 'string' && payload.length) {
    session.send(payload);
  }
}

/** 启动桥接器。 */
export function startBridge(cfg, logger = console) {
  const { server, backends } = createBridgeServer(cfg, logger);
  server.listen(cfg.port, cfg.host, () => {
    logger.log(`ic-canvas-agent 已启动 http://${cfg.host}:${cfg.port}`);
    logger.log(`检测到的本机后端: ${backends.length ? backends.join(', ') : '(无，需安装 codex 或 claude CLI)'}`);
    logger.log(`Origin 白名单: ${cfg.allowedOrigins.join(', ')}`);
    logger.log('令牌已写入 ~/.ic/agent.json（权限 0600）。请在 IC 的「画布助手 → 本机 Agent」中填入地址与令牌。');
  });

  // 退出时清理子进程，避免孤儿占用本机 CLI 会话
  const shutdown = () => {
    server.close();
    process.exit(0);
  };
  process.on('SIGINT', shutdown);
  process.on('SIGTERM', shutdown);
  return server;
}
