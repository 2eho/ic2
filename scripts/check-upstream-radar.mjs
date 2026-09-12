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

// ------------------------------------------------------------- 1) 产物文件存在且非占位
for (const f of ['docs/upstream/sync-log.md', 'docs/upstream/divergences.md']) {
  if (!existsSync(f)) {
    problems.push(`缺少 ${f}（docs/design/12 §7 的落地检查项）`);
    continue;
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
      ]) {
        if (!Array.isArray(raw[key]) || raw[key].length === 0) {
          problems.push(`探针未从夹具中抽出 ${key}（${why}）：正则可能已失效，雷达会静默漏报`);
        }
      }
    }
    // 首次运行没有基线，判定为 baseline 是正确的（不是「变化」）
    if (report.verdict !== 'baseline') {
      problems.push(`无基线时 verdict=${report.verdict}，期望 baseline（否则会把首跑误报成变更）`);
    }
  }

  // 第二次运行（有基线、上游未变）必须判定为 no-signal：否则每次巡检都会产生假告警。
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

console.log('上游雷达自检');
console.log('─'.repeat(56));
if (problems.length) {
  console.error(`发现 ${problems.length} 处问题：`);
  for (const p of problems) console.error('  - ' + p);
  process.exit(1);
}
console.log('雷达产物齐备且可从离线夹具抽取契约面');
