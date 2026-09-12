#!/usr/bin/env node
// 上游雷达的**自检**：不能只看它「跑完了」，要看它「有没有真的在看」。
//
// 起因：docs/design/12 §7 的落地检查单写了
//   - `docs/upstream/sync-log.md` 有首次分析记录
//   - `docs/upstream/divergences.md` 已建立（含 DIV-01..DIV-10）
// 而仓库里 `docs/upstream/` **整个目录都不存在**。也就是说：
// 「上游定时同步」这条能力只有脚本、没有产物，`make parity`/`make drill`
// 这类门禁自然也不会发现——文档里的检查单没人执行过。
//
// 本脚本把那份检查单变成可执行的：
//   1. docs/upstream/{sync-log,divergences}.md 必须存在，且内容不是占位；
//   2. divergences 必须覆盖 docs/design/12 §5 声明的 DIV-01..DIV-10；
//   3. 雷达脚本必须可运行，且能从**离线夹具**中抽出契约面
//      （不能依赖网络：CI/自托管 Runner 未必有外网）。
import { readFileSync, existsSync, mkdtempSync, writeFileSync, mkdirSync, rmSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const problems = [];
const DIVERGENCE_DOC = 'docs/design/12-legacy-and-upstream.md';

// ------------------------------------------------------------- 0) 产物必须真的会被提交
// 这里覆盖一个**实际发生过**的缺陷：`.gitignore` 里的 `upstream/` 会匹配任意层级的
// 同名目录，于是 `docs/upstream/`（本应随代码一起发布的同步记录）被静默忽略。
// 后果极隐蔽：文件在本地存在、门禁在本地绿、`git status` 却干净——
// 本地检查读的是文件系统，而发布读的是 git。所以门禁必须**问 git**，不能只看文件在不在。
const tracked = (file) => {
  const r = spawnSync('git', ['ls-files', '--error-unmatch', file], { encoding: 'utf8' });
  return r.status === 0;
};
const ignored = (file) => {
  const r = spawnSync('git', ['check-ignore', '-q', file], { encoding: 'utf8' });
  return r.status === 0;
};

// ------------------------------------------------------------- 1) 产物文件存在且非占位
for (const f of ['docs/upstream/sync-log.md', 'docs/upstream/divergences.md']) {
  if (!existsSync(f)) {
    problems.push(`缺少 ${f}（docs/design/12 §7 的落地检查项）`);
    continue;
  }
  if (ignored(f)) {
    problems.push(
      `${f} 被 .gitignore 忽略：文件在本地存在但永远不会被提交。` +
        '检查 .gitignore 是否把 `upstream/` 写成了非锚定的形式（应为 `/upstream/`）',
    );
  } else if (!tracked(f)) {
    problems.push(`${f} 未被 git 跟踪（记得 git add，否则发布物里没有它）`);
  }
  const body = readFileSync(f, 'utf8');
  // 占位文件的判据：没有表格行、没有日期锚点、字数过少
  const meaningful = body.split('\n').filter((l) => l.trim() && !l.trim().startsWith('#')).length;
  if (meaningful < 5) {
    problems.push(`${f} 内容过少（${meaningful} 行有效内容），疑似占位文件`);
  }
  // 只有**记录型**文档需要日期；divergences 是清单型文档，它的可追溯性体现在
  // 「每条偏离都对应设计文档 §5 的声明」（下方会双向校验编号），不需要日期。
  const needsDate = f.endsWith('sync-log.md');
  if (needsDate && !/\d{4}-\d{2}-\d{2}/.test(body)) {
    problems.push(`${f} 没有任何日期记录（同步记录必须可追溯时间）`);
  }
}

// ------------------------------------------------------------- 2) DIV 清单覆盖
const divDoc = existsSync(DIVERGENCE_DOC) ? readFileSync(DIVERGENCE_DOC, 'utf8') : '';
const declaredDivs = [...divDoc.matchAll(/^\|\s*(DIV-\d+)\s*\|/gm)].map((m) => m[1]);
if (declaredDivs.length === 0) {
  problems.push(`${DIVERGENCE_DOC} 中未解析出 DIV-\d+ 清单（设计文档被改动？）`);
}
if (existsSync('docs/upstream/divergences.md')) {
  const body = readFileSync('docs/upstream/divergences.md', 'utf8');
  for (const id of declaredDivs) {
    if (!body.includes(id)) {
      problems.push(`docs/upstream/divergences.md 缺少设计文档声明的 ${id}`);
    }
  }
  // 反向：产物里不该出现设计文档没声明的编号（编号漂移）
  for (const m of body.matchAll(/\bDIV-(\d+)\b/g)) {
    const id = `DIV-${m[1]}`;
    if (!declaredDivs.includes(id)) {
      problems.push(`docs/upstream/divergences.md 引用了未在 ${DIVERGENCE_DOC} 声明的 ${id}`);
    }
  }
}
console.log(`DIV 清单：设计文档声明 ${declaredDivs.length} 项`);

// ------------------------------------------------------------- 3) 雷达必须能离线工作
// 造一个「上游夹具」，验证探针真的能抽出内容。用夹具而不是真克隆：
//   1) CI/自托管 Runner 未必有外网；
//   2) 真克隆会让门禁依赖 GitHub 可用性，变成不稳定门禁。
const fixture = mkdtempSync(join(tmpdir(), 'ic-upstream-'));
try {
  const w = (rel, content) => {
    const p = join(fixture, rel);
    mkdirSync(join(p, '..'), { recursive: true });
    writeFileSync(p, content);
  };
  // 夹具必须**照上游真实形状**造：探针的正则是按真实文件写的，
  // 夹具形状不对会让「探针失效」与「夹具不对」无法区分。
  // 这里刻意用真实上游的两种来源：
  //   - i18n：源码里的 i18n.t("a.b.c") 调用（而不是 locale 字典条目）
  //   - 节点类型：`export enum CanvasNodeType { ... }` 块
  w(
    'web/src/types/canvas.ts',
    'export enum CanvasNodeType {\n  Prompt = "prompt",\n  Image = "image",\n  Video = "video",\n}\n',
  );
  w(
    'web/src/pages/canvas/project.tsx',
    'i18n.t("canvas.addNode");\ni18n.t("canvas.deleteNode");\n',
  );
  w('VERSION', 'v0.0.0-fixture\n');
  // 语言探针的夹具：上游是 TS 单栈。这里刻意放一个 `.go` 文件，
  // 验证探针**能识别出 Go**——否则「上游没改 Go」这个结论就没有证据。
  w('cmd/server/main.go', 'package main\n\nfunc main() {}\n');
  w(
    'CHANGELOG.md',
    [
      '# CHANGELOG',
      '',
      '## Unreleased',
      '',
      '## v0.0.0-fixture - 2026-01-01',
      '',
      '+ [修复] 历史遗留的凭据脱敏问题（上游未变化时不该被报成本次 security-fix）。',
      '',
      '## v0.0.0-older - 2025-01-01',
      '',
      '+ [修复] 更早的一条凭据泄露修复（用来验证判定只看「本次版本小节」，'
        + '而不是文件开头若干行）。',
      '',
    ].join('\n'),
  );

  const r = spawnSync(
    'node',
    ['scripts/report-upstream.mjs'],
    {
      encoding: 'utf8',
      env: {
        ...process.env,
        VERSION: 'v0.0.0-fixture',
        COMMIT: 'fixture',
        PREV_VERSION: 'v0.0.0-fixture',
        PREV_COMMIT: 'fixture',
        REPORT_PATH: join(fixture, 'report.json'),
        CLONE_DIR: fixture,
      },
    },
  );
  if (r.status !== 0) {
    problems.push(`report-upstream.mjs 在夹具上执行失败（退出码 ${r.status}）：${r.stderr?.slice(0, 200)}`);
  } else {
    // 注意：report.json 的 `probes` 字段是**变更集**（added/removed），
    // 原始探针值写在同目录的 baseline-probes.json。读错文件会让断言恒假。
    const rawPath = join(fixture, 'baseline-probes.json');
    const report = JSON.parse(readFileSync(join(fixture, 'report.json'), 'utf8'));
    if (!existsSync(rawPath)) {
      problems.push('report-upstream.mjs 未写出 baseline-probes.json（下次巡检没有基线可比）');
    } else {
      const raw = JSON.parse(readFileSync(rawPath, 'utf8'));
      // 夹具里放了 i18n.t(...) 调用与 CanvasNodeType 枚举，必须被抽出来——
      // 抽不出来说明探针正则已失效，雷达会「什么都没发现」却报告成功。
      for (const [key, why] of [
        ['i18nKeys', 'i18n key（最灵敏的新功能探针）'],
        ['nodeTypes', '节点类型枚举'],
        ['languages', '上游语言构成（用于回答「上游是否引入 Go 服务端」这类问题）'],
      ]) {
        if (!Array.isArray(raw[key]) || raw[key].length === 0) {
          problems.push(`探针未从夹具中抽出 ${key}（${why}）：正则可能已失效，雷达会静默漏报`);
        }
      }
      // 语言探针必须真的能认出 Go：夹具里放了 main.go，抽不出 go=1 就说明探针失效，
      // 「上游是否改 Go」这个问题就只能靠读 CHANGELOG 猜（CHANGELOG 里两种说法都有）。
      if (!raw.languages?.includes('go=1')) {
        problems.push(
          `语言探针未识别夹具里的 Go 文件（实得 ${JSON.stringify(raw.languages)}）：` +
            '这样「上游是否引入 Go 服务端」无法被事实核对',
        );
      }
    }
    // 首次运行没有基线，判定为 baseline 是正确的（不是「变化」）
    if (report.verdict !== 'baseline') {
      problems.push(`无基线时 verdict=${report.verdict}，期望 baseline（否则会把首跑误报成变更）`);
    }
    // 报告必须带**改写队列**：只说「上游变了什么」而没给落点与验收，
    // 结局就是没人跟进（上一轮 docs/upstream/ 长期为空正是这个原因）。
    if (!Array.isArray(report.rewriteQueue)) {
      problems.push('报告缺少 rewriteQueue 字段：契约面变化必须翻译成「落点 + 动作 + 验收」');
    }
    if (typeof report.summary !== 'string' || !report.summary) {
      problems.push('报告缺少 summary 字段：CI 日志与 Issue 标题需要它');
    }

    // 造一次**真的契约面变化**，验证队列里每条都带落点/动作/验收。
    // 只测 baseline 与 noise 是不够的：这两条路径的 queue 都是空的，
    // 队列逻辑坏了也测不出来（上一轮「产物存在但内容是空壳」正是这类问题）。
    mkdirSync(join(fixture, 'canvas-agent/src/canvas'), { recursive: true });
    writeFileSync(
      join(fixture, 'canvas-agent/src/canvas/schemas.ts'),
      [
        'export const toolNames = [',
        '    "canvas_get_state",',
        '    "brand_new_tool",',
        '] as const;',
      ].join('\n'),
    );
    const r3 = spawnSync('node', ['scripts/report-upstream.mjs'], {
      encoding: 'utf8',
      env: {
        ...process.env,
        VERSION: 'v0.0.1-fixture',
        COMMIT: 'fixture2',
        PREV_VERSION: 'v0.0.0-fixture',
        PREV_COMMIT: 'fixture',
        REPORT_PATH: join(fixture, 'report3.json'),
        CLONE_DIR: fixture,
      },
    });
    if (r3.status !== 0) {
      problems.push(`契约面变化时 report-upstream.mjs 失败: ${r3.stderr?.slice(0, 200)}`);
    } else {
      const report3 = JSON.parse(readFileSync(join(fixture, 'report3.json'), 'utf8'));
      if (report3.verdict === 'noise') {
        problems.push('上游工具清单新增了一项，但判定为 noise（diff 逻辑漏了 tools）');
      }
      const q = report3.rewriteQueue ?? [];
      if (q.length === 0) {
        problems.push('检出契约面变化但 rewriteQueue 为空：变化会被静默丢弃');
      }
      const toolEntry = q.find((x) => x.probe === 'toolsAdded');
      if (!toolEntry) {
        problems.push(`rewriteQueue 缺少 toolsAdded 条目（实得 ${q.map((x) => x.probe).join(',')}）`);
      } else {
        for (const field of ['where', 'action', 'verify']) {
          if (!toolEntry[field] || /需人工判断落点/.test(toolEntry[field])) {
            problems.push(`toolsAdded 队列条目的 ${field} 未登记落点/动作/验收`);
          }
        }
        if (!toolEntry.items.includes('brand_new_tool')) {
          problems.push('队列条目未列出具体变化项（items 为空或内容不对）');
        }
      }
    }
  }

  // 第二次运行（有基线、上游未变）必须判定为 noise：否则每次巡检都会产生假告警。
  //
  // 这里覆盖的是一个**实际发生过**的缺陷：原实现把 CHANGELOG 固定截取开头 40 行、
  // 且安全判定不检查「上游是否真的动了」，于是上游停更时 verdict 恒为 security-fix。
  // 夹具的 CHANGELOG 里**故意保留**一条含「凭据/密钥」的历史修复，
  // 用来把「上游没动就不该报安全告警」这条规则钉死（否则用例会恒绿）。
  const r2 = spawnSync('node', ['scripts/report-upstream.mjs'], {
    encoding: 'utf8',
    env: {
      ...process.env,
      VERSION: 'v0.0.0-fixture',
      COMMIT: 'fixture',
      PREV_VERSION: 'v0.0.0-fixture',
      PREV_COMMIT: 'fixture',
      REPORT_PATH: join(fixture, 'report2.json'),
      CLONE_DIR: fixture,
    },
  });
  if (r2.status === 0) {
    const report2 = JSON.parse(readFileSync(join(fixture, 'report2.json'), 'utf8'));
    if (report2.verdict !== 'noise') {
      problems.push(
        `上游未变化时 verdict=${report2.verdict}，期望 noise（假告警会让巡检被忽略）`,
      );
    }
    const c = report2.probes ?? {};
    const noisy = Object.entries(c).some(
      ([, v]) => Array.isArray(v) && v.length > 0,
    );
    if (noisy) {
      problems.push('上游未变化时仍报告出变更项（diff 逻辑有误，会产生假告警）');
    }
  }
} finally {
  rmSync(fixture, { recursive: true, force: true });
}

// ------------------------------------------------------------- 4) 本仓的上游工具清单必须与上游真源一致
//
// 这是 9.5 的**防复发**措施。上一轮的缺陷是：矩阵标 done、实际缺 20 个工具名，
// 而没有任何门禁能发现——因为「清单是否正确」只存在于人的脑子里。
//
// 判据只能是上游的真源文件（`canvas-agent/src/canvas/schemas.ts` 的 toolNames）。
// 有网络时对真上游校验；没有网络时退化为「与仓库内已归档的清单比对」，
// 并**显式标注为降级**而不是静默通过。
const upstreamRepo = process.env.UPSTREAM_DIR || 'upstream/infinite-canvas';
const schemasPath = join(upstreamRepo, 'canvas-agent/src/canvas/schemas.ts');
if (existsSync(schemasPath)) {
  const text = readFileSync(schemasPath, 'utf8');
  const block = text.split('export const toolNames')[1]?.split(']')[0] ?? '';
  const upstreamNames = [...block.matchAll(/"([a-z_]+)"/g)].map((m) => m[1]);
  const ourFile = 'internal/agent/tools_upstream.go';
  const our = readFileSync(ourFile, 'utf8');
  const ourBlock = our.split('func UpstreamToolNames() []string {')[1]?.split('}', 1)[0] ?? '';
  const ourNames = [...ourBlock.matchAll(/"([a-z_]+)"/g)].map((m) => m[1]);
  if (upstreamNames.length === 0) {
    problems.push(`无法从 ${schemasPath} 解析出 toolNames（上游结构变了？探针需要更新）`);
  } else {
    const missing = upstreamNames.filter((n) => !ourNames.includes(n));
    const extra = ourNames.filter((n) => !upstreamNames.includes(n));
    if (missing.length) {
      problems.push(
        `本仓上游工具清单缺少 ${missing.length} 个工具名：${missing.join(', ')}` +
          '（MCP 客户端按名调用，缺名字就是「这个能力用不了」且没有任何报错）',
      );
    }
    if (extra.length) {
      problems.push(`本仓上游工具清单有上游不存在的名字：${extra.join(', ')}（多出的名字没有依据）`);
    }
    if (missing.length === 0 && extra.length === 0) {
      console.log(`上游工具名逐字一致（${upstreamNames.length} 个）`);
    }
  }
} else {
  console.log(
    `未找到上游镜像（${schemasPath}）：工具名一致性检查**已降级**。` +
      '执行 `make upstream-watch` 拉取上游镜像后可完整校验。',
  );
}

console.log('上游雷达自检');
console.log('─'.repeat(56));
if (problems.length) {
  console.error(`发现 ${problems.length} 处问题：`);
  for (const p of problems) console.error('  - ' + p);
  process.exit(1);
}
console.log('雷达产物齐备且可从离线夹具抽取契约面');
