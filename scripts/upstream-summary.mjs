#!/usr/bin/env node
// 渲染 upstream/change-report.json 的摘要。
//
// 为什么单独成脚本：CI 的 crontab / web_trigger 原本用 `node -e "..."` 内联渲染，
// 而平台的 YAML 解析会把块标量按 C 风格转义处理（换行被压平、反斜杠被吃掉），
// 内联脚本在平台上会变形。把渲染逻辑放进 .mjs 后，YAML 只需一行无转义命令。
//
// 用法：
//   node scripts/upstream-summary.mjs            # 摘要
//   node scripts/upstream-summary.mjs --full     # 摘要 + 完整报告
// 退出码始终为 0：巡检产物是报告，不是门禁结论。
import fs from 'node:fs';

const REPORT = process.env.REPORT_PATH || 'upstream/change-report.json';
const PREVIEW = 20;
const full = process.argv.includes('--full');

if (!fs.existsSync(REPORT)) {
  console.log(`无报告（上游未变化）：${REPORT}`);
  process.exit(0);
}

const report = JSON.parse(fs.readFileSync(REPORT, 'utf8'));
const from = report.from || {};
const to = report.to || {};
const probes = report.probes || {};

console.log('---- upstream change-report 摘要 ----');
console.log('verdict:', report.verdict);
console.log('from   :', from.version, from.commit);
console.log('to     :', to.version, to.commit);

if (probes.baseline) {
  console.log('基线首次建立，无逐项差异');
} else {
  for (const [key, value] of Object.entries(probes)) {
    if (Array.isArray(value) && value.length) {
      const shown = value.slice(0, PREVIEW);
      const suffix = value.length > shown.length ? ` …（共 ${value.length} 项）` : '';
      console.log(`${key} =>`, JSON.stringify(shown) + suffix);
    }
  }
}

if (Array.isArray(report.changelog) && report.changelog.length) {
  console.log(`changelog: ${report.changelog.length} 条`);
}
if (full) {
  console.log('---- 完整报告 ----');
  console.log(JSON.stringify(report, null, 2));
}
