#!/usr/bin/env node
/**
 * 从上游 clone 抽取「契约面」变化，产出 change-report.json。
 *
 * 设计说明：docs/design/12-legacy-and-upstream.md §3.3 / §3.4
 * 原则：只抽契约面（协议、枚举、边界、存储 schema），不抽实现细节。
 */
import fs from "node:fs";
import path from "node:path";
import { execFileSync } from "node:child_process";

const {
  VERSION = "unknown",
  COMMIT = "",
  PREV_VERSION = "",
  PREV_COMMIT = "",
  REPORT_PATH,
  CLONE_DIR,
} = process.env;

if (!REPORT_PATH || !CLONE_DIR) {
  console.error("REPORT_PATH and CLONE_DIR are required");
  process.exit(2);
}

const read = (rel, fallback = "") => {
  try {
    return fs.readFileSync(path.join(CLONE_DIR, rel), "utf8");
  } catch {
    return fallback;
  }
};

const matchAll = (text, re, group = 1) => [...text.matchAll(re)].map((m) => m[group]);

/** 从 TS 联合类型里取候选字符串字面量。 */
const unionLiterals = (text) => matchAll(text, /"([A-Za-z0-9_:.-]+)"/g);

/** 递归列出源码文件。 */
const listSourceFiles = (dir, exts = [".ts", ".tsx", ".js", ".mjs", ".jsx"], out = []) => {
  let entries = [];
  try {
    entries = fs.readdirSync(dir, { withFileTypes: true });
  } catch {
    return out;
  }
  for (const entry of entries) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      if (["node_modules", "dist", "build", ".git"].includes(entry.name)) continue;
      listSourceFiles(full, exts, out);
    } else if (exts.some((e) => entry.name.endsWith(e))) {
      out.push(full);
    }
  }
  return out;
};

/**
 * 抽取 i18n 文案 key —— 最灵敏的「新功能」探针。
 * 覆盖两种来源：源码里的 i18n.t("a.b.c") 调用，以及 locale 文件里的叶子 key。
 * 用固定长度上限截断，保证 diff 报告体积可控。
 */
const i18nKeys = () => {
  const keys = new Set();
  const sources = [
    ...listSourceFiles(path.join(CLONE_DIR, "web/src")),
    ...listSourceFiles(path.join(CLONE_DIR, "canvas-agent/src")),
  ];
  for (const file of sources) {
    const text = read(path.relative(CLONE_DIR, file));
    for (const m of text.matchAll(/i18n\.t\(\s*[`"']([A-Za-z0-9_]+(?:\.[A-Za-z0-9_]+)+)/g)) keys.add(m[1]);
    // 模板字符串前缀：i18n.t(`a.b.${x}`) → a.b.*
    for (const m of text.matchAll(/i18n\.t\(\s*`([A-Za-z0-9_.]+)\$\{/g)) keys.add(`${m[1]}*`);
  }
  return [...keys].sort();
};

/**
 * CHANGELOG 只取**本次变化涉及的那一段**，不是文件开头 40 行。
 *
 * 这里踩过一个真实的坑：原实现固定截取文件开头 40 行，于是 v0.18.0 已经发布、
 * 上游「未变化」时，判定仍然是 `security-fix`——因为那 40 行里恰好有几条历史
 * 安全修复（Agent 令牌脱敏 / 配置目录权限）。后果是**每次巡检都产出一条安全告警**，
 * 而安全告警一旦常态化就会被忽略，等于没有告警。
 *
 * 现在的口径：取 `## Unreleased`（若有内容）到 `to.version` 所在小节的内容。
 * 上游未变化时这段为空 → verdict 落回 noise。
 */
const changelog = (() => {
  const lines = read("CHANGELOG.md").split("\n");
  // 小节头形如 `## v0.18.0 - 2026-09-07`；Unreleased 也算一个小节。
  const heads = [];
  lines.forEach((line, i) => {
    const m = line.match(/^##\s+(.+?)\s*$/);
    if (m) heads.push({ title: m[1].trim(), line: i });
  });
  const wanted = heads.filter(
    (h) => /^unreleased/i.test(h.title) || (VERSION && h.title.startsWith(VERSION)),
  );
  const picked = wanted.length ? wanted : heads.slice(0, 1);
  const out = [];
  for (const h of picked) {
    const next = heads.find((x) => x.line > h.line);
    const body = lines.slice(h.line + 1, next ? next.line : lines.length);
    for (const line of body) {
      const m = line.match(/^\+\s*\[(新增|调整|修复|优化)\]\s*(.+)$/);
      if (m) out.push({ tag: m[1], text: m[2].trim() });
    }
  }
  return out.slice(0, 40);
})();

const probes = {
  i18nKeys: i18nKeys(),
  nodeTypes: unionLiterals(read("web/src/types/canvas.ts").split("export enum CanvasNodeType")[1]?.split("}")[0] || ""),
  opTypes: matchAll(read("web/src/lib/canvas/canvas-agent-ops.ts"), /type: "([a-z_]+)"/g),
  tools: matchAll(read("canvas-agent/src/canvas/schemas.ts"), /^\s*"([a-z_]+)",$/gm),
  endpoints: [
    ...new Set([
      ...matchAll(read("web/src/services/api/image.ts"), /"(\/v1[^"]*|\/v1beta[^"]*)"/g),
      ...matchAll(read("web/src/services/api/video.ts"), /`\$\{[^}]*\}(\/v1[^`]*|\/v1beta[^`]*)`/g),
      ...matchAll(read("web/src/services/api/audio.ts"), /"(\/audio\/[^"]*)"/g),
    ]),
  ].sort(),
  limits: (() => {
    // 边界常量是「静默行为变化」的高危面：抽取所有全大写数字常量，逐条比对。
    const files = [
      "web/src/services/api/image.ts",
      "web/src/services/api/video.ts",
      "web/src/lib/media-size.ts",
      "web/src/lib/canvas/canvas-image-data.ts",
      "web/src/constant/canvas.ts",
    ];
    const limits = {};
    for (const file of files) {
      for (const m of read(file).matchAll(/const ([A-Z][A-Z0-9_]+)\s*=\s*(\d+)\s*;/g)) limits[m[1]] = Number(m[2]);
    }
    return limits;
  })(),
  pluginFields: unionLiterals(read("plugins/canvas/sdk/src/types.ts")),
  /**
   * 上游**语言构成**探针。
   *
   * 加这一项的起因是一个具体的疑问：「原仓库是不是改 Go 了？」
   * 这个问题没法靠读 CHANGELOG 回答——上游 v0.0.4 的 CHANGELOG 里确实出现过
   * 「内部 Go 服务」，v0.4.0 又写着「移除后端」。也就是说，只看文字记录会得出
   * 相反的结论。所以这里把语言构成变成**可核对的事实**：
   * 一旦上游真的引入 Go（或任何非 TS 服务端），巡检必须报出来。
   *
   * 只统计源码扩展名，不扫 node_modules（否则数量会被依赖淹没）。
   */
  languages: (() => {
    const exts = [".ts", ".tsx", ".js", ".mjs", ".go", ".py", ".rs", ".java", ".rb", ".php"];
    const counts = {};
    const walkDir = (dir) => {
      let entries = [];
      try {
        entries = fs.readdirSync(dir, { withFileTypes: true });
      } catch {
        return;
      }
      for (const entry of entries) {
        const full = path.join(dir, entry.name);
        if (entry.isDirectory()) {
          if (["node_modules", "dist", "build", ".git", ".next", "coverage"].includes(entry.name)) continue;
          walkDir(full);
        } else {
          const ext = path.extname(entry.name);
          if (exts.includes(ext)) counts[ext] = (counts[ext] || 0) + 1;
        }
      }
    };
    walkDir(CLONE_DIR);
    // 输出成 `go=0` 这种可 diff 的键值，避免报告里出现对象导致比对逻辑走偏。
    return Object.entries(counts)
      .filter(([, n]) => n > 0)
      .map(([ext, n]) => `${ext.replace(".", "")}=${n}`)
      .sort();
  })(),
};

const priorPath = path.join(path.dirname(REPORT_PATH), "baseline-probes.json");
const prior = fs.existsSync(priorPath) ? JSON.parse(fs.readFileSync(priorPath, "utf8")) : null;

const diffSets = (a = [], b = []) => {
  const sb = new Set(b);
  const sa = new Set(a);
  return { added: a.filter((x) => !sb.has(x)), removed: b.filter((x) => !sa.has(x)) };
};

const changed = prior
  ? {
      i18nKeysAdded: diffSets(probes.i18nKeys, prior.i18nKeys).added,
      i18nKeysRemoved: diffSets(probes.i18nKeys, prior.i18nKeys).removed,
      nodeTypesAdded: diffSets(probes.nodeTypes, prior.nodeTypes).added,
      nodeTypesRemoved: diffSets(probes.nodeTypes, prior.nodeTypes).removed,
      opTypesAdded: diffSets(probes.opTypes, prior.opTypes).added,
      toolsAdded: diffSets(probes.tools, prior.tools).added,
      toolsRemoved: diffSets(probes.tools, prior.tools).removed,
      pluginFieldsAdded: diffSets(probes.pluginFields, prior.pluginFields).added,
      endpointsAdded: diffSets(probes.endpoints, prior.endpoints).added,
      endpointsRemoved: diffSets(probes.endpoints, prior.endpoints).removed,
      // 语言构成变化（尤其是「上游引入 Go 服务端」）是架构级信号，必须报出来。
      languagesAdded: diffSets(probes.languages, prior.languages).added,
      languagesRemoved: diffSets(probes.languages, prior.languages).removed,
      limitsChanged: Object.entries(probes.limits)
        .filter(([k, v]) => prior.limits?.[k] !== undefined && String(prior.limits[k]) !== String(v))
        .map(([name, to]) => ({ name, from: prior.limits[name], to })),
    }
  : { baseline: true };

/**
 * 安全判定只在「上游真的动了」时才生效。
 *
 * 与 changelog 的截取范围是同一类问题：上游停更时，CHANGELOG 里那些历史安全修复
 * 仍然存在，于是每次巡检都报 security-fix。安全告警一旦常态化就不会有人看，
 * 这条判定也就等于没有。判据收紧为「版本或 commit 发生变化」。
 */
const upstreamMoved = Boolean(VERSION) && Boolean(PREV_VERSION) && (VERSION !== PREV_VERSION || COMMIT !== PREV_COMMIT);
const securityRelevant =
  upstreamMoved && changelog.some((x) => /安全|漏洞|凭据|密钥|权限|脱敏|XSS|注入/.test(x.text));
const contractTouched = [
  changed.i18nKeysAdded,
  changed.nodeTypesAdded,
  changed.opTypesAdded,
  changed.toolsAdded,
  changed.pluginFieldsAdded,
  changed.endpointsAdded,
  changed.limitsChanged,
  changed.languagesAdded,
].some((arr) => Array.isArray(arr) && arr.length > 0);

// 首次运行没有基线可比，任何判定都是误报，单独归类为 baseline。
const isBaseline = !prior;
const verdict = isBaseline ? "baseline" : securityRelevant ? "security-fix" : contractTouched ? "contract-change" : "noise";

/**
 * 把契约面变化翻译成**可执行的改写队列**。
 *
 * 为什么要有这一步：原来的产物只说「上游变了什么」，而这只回答了半个问题。
 * 一份报告如果读完还需要人再去想「那我们要改哪里」，它的实际结局就是被跳过
 * ——上一轮的 `docs/upstream/` 长期没被写过一个字，正是这个原因。
 *
 * 所以每条变化都必须给出三件事：
 *   1. **落点**：本仓哪个文件是这个契约面的对应位置；
 *   2. **动作**：具体要做什么（不是「关注一下」）；
 *   3. **验收**：改完怎么证明（跑哪条门禁/哪条用例）。
 *
 * 拿不到落点的变化**也要列出来**（action 里写明「需人工判断落点」）：
 * 静默丢弃会让「没落在队列里」等价于「没发生」。
 */
const LANDING = {
  i18nKeysAdded: {
    where: "web/src/shared/i18n/{zh-CN,en-US}.ts",
    action: "对照上游新增文案，补充我们对应的 key（注意命名空间不同，见 DIV-07）",
    verify: "node scripts/check-i18n-dupkeys.mjs && cd web && npm run lint",
  },
  nodeTypesAdded: {
    where: "internal/graph/spec.go + web/src/features/canvas/kernel/schema.ts",
    action: "评估新节点类型：需要就加 schema（前后端两处），不需要就写进 divergences.md 并说明",
    verify: "node scripts/check-node-schema.mjs && go test ./internal/graph/",
  },
  opTypesAdded: {
    where: "internal/graph/op.go + contracts/ops.schema.json",
    action: "新增 op kind 必须同时改契约与 apply 分派（rebase/重放也要能处理）",
    verify: "make gen-check && go test ./internal/graph/",
  },
  toolsAdded: {
    where: "internal/agent/tools_upstream.go（UpstreamToolNames）",
    action: "把新工具名加入上游清单，并在 dispatch_upstream.go 里给出落位；无对应能力时返回 not_implemented",
    verify: "go test ./internal/agent/ -run TestUpstreamToolNamesAllRegistered",
  },
  toolsRemoved: {
    where: "internal/agent/tools_upstream.go",
    action: "上游删除的工具名：从清单里移除，并在 divergences.md 记录（客户端可能仍在调用）",
    verify: "go test ./internal/agent/",
  },
  pluginFieldsAdded: {
    where: "internal/plugin/manifest.go + packages/plugin-sdk/src/index.d.ts",
    action: "插件清单新增字段要与宿主校验、SDK 类型两处同步",
    verify: "go test ./internal/plugin/ && node --test packages/plugin-sdk/src/sdk.test.js",
  },
  endpointsAdded: {
    where: "internal/provider/adapter/*.go",
    action: "上游新增的 Provider 端点：在适配器里实现或登记为不支持（不要静默忽略）",
    verify: "go test ./internal/provider/...",
  },
  limitsChanged: {
    where: "internal/graph/limits.go + docs/design/11",
    action: "边界常量变化：改代码真源，再跑 check-boundaries 同步文档",
    verify: "node scripts/check-boundaries.mjs",
  },
  languagesAdded: {
    where: "docs/upstream/sync-log.md",
    action: "上游引入新语言（尤其 Go）是架构级信号：记录结论并评估是否影响我们的形态判断",
    verify: "人工确认（写入 sync-log）",
  },
};

function buildQueue(changed) {
  const queue = [];
  for (const [key, value] of Object.entries(changed)) {
    if (!Array.isArray(value) || value.length === 0) continue;
    if (key.endsWith("Removed") && key !== "toolsRemoved") continue;
    const landing = LANDING[key] ?? {
      where: "（需人工判断落点）",
      action: "契约面变化但未登记落点：请在 docs/upstream/sync-log.md 记录并补充本表",
      verify: "人工确认",
    };
    queue.push({
      probe: key,
      items: value.slice(0, 50),
      truncated: value.length > 50,
      where: landing.where,
      action: landing.action,
      verify: landing.verify,
    });
  }
  return queue;
}

const queue = buildQueue(changed);

fs.writeFileSync(REPORT_PATH, JSON.stringify({
  from: { version: PREV_VERSION, commit: PREV_COMMIT },
  to: { version: VERSION, commit: COMMIT },
  changelog,
  probes: changed,
  verdict,
  // 改写队列：每条都有落点、动作、验收
  rewriteQueue: queue,
  // 一句话结论，供 CI 日志与 Issue 标题直接用
  summary: verdict === "baseline"
    ? "首次建立基线，无未处理项"
    : verdict === "noise"
      ? "上游契约面无变化"
      : `上游契约面有 ${queue.length} 类变化，待改写到本仓`,
}, null, 2));
fs.writeFileSync(priorPath, JSON.stringify(probes, null, 2));

console.log(`verdict=${verdict}`);
