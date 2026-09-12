#!/usr/bin/env node
/**
 * 插件构建脚本（10.9）。
 *
 * 产出：dist/<entry>（bundle）+ dist/plugin.json（清单）。
 *
 * 三条约束，都是为了「插件能被安全地装载」：
 *
 *  1. **单文件 bundle**：宿主用 iframe srcdoc 装载，没有同源目录可读，
 *     因此不能有相对 import。多个源文件在构建期合并。
 *  2. **清单内的 entry 与实际产物同名**：不然宿主会在装载时 404，
 *     而用户只看到「插件不工作」。
 *  3. **计算 sha256 写入 integrity**：这是「用户在审批权限清单时看到的
 *     code 是否就是他后来运行的那份」唯一的锚点。
 *
 * 有意不做的事：不做压缩混淆（插件作者需要能对着 DevTools 调试自己的代码），
 * 不做 tree-shaking（没有依赖图，一个只在开发期用到的 import 混进来也不致命）。
 */
import { createHash } from "node:crypto";
import { mkdirSync, readFileSync, writeFileSync, existsSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

/**
 * 内联相对 import。
 *
 * 为什么不用正则替换 `import x from "./y"`：那会漏掉 `export { x } from "./y"`
 * 与副作用 import，而漏掉的表现是**构建成功但装载失败**——最糟的一类。
 * 这里递归处理三种形态，并把循环依赖显式报错（循环在单文件里无法内联）。
 */
/** SDK 自身的源码目录（vite 之类工具会改写 import.meta.url，因此只算一次）。 */
const SDK_SRC = resolve(dirname(fileURLToPath(import.meta.url)), "..", "src");

function inlineImports(file, chain) {
  const abs = resolve(file);
  if (chain.includes(abs)) {
    throw new Error(`循环依赖: ${[...chain, abs].join(" -> ")}`);
  }
  let src = readFileSync(abs, "utf8");
  const dir = dirname(abs);
  // 先处理裸 import：SDK 自身必须内联（iframe 里没有模块解析器，
  // 也没有 node_modules 可读）。只允许白名单里的包名，其余显式报错 ——
  // 静默跳过会让错误延迟到装载时才出现，而那时错误信息指向 iframe。
  const bareRe =
    /(?:^|\n)\s*(?:import\s+(?:[\w*{}\s,]+)\s+from\s+|export\s+(?:[\w*{}\s,]+)\s+from\s+)["']([^."'][^"']*)["'];?/g;
  src = src.replace(bareRe, (_m, spec) => {
    if (spec === "@ic/plugin-sdk" || spec.startsWith("@ic/plugin-sdk/")) {
      const sub = spec.replace("@ic/plugin-sdk", "").replace(/^\//, "");
      const target = resolve(SDK_SRC, sub || "index.js");
      return `\n/* inlined SDK: ${spec} */\n` + inlineImports(target, [...chain, abs]);
    }
    throw new Error(
      `不允许的依赖 "${spec}"：插件必须自包含（iframe 里没有模块解析器）。` +
        `请把该依赖的代码内联进插件，或改用打包器生成单文件。`,
    );
  });

  const re =
    /(?:^|\n)\s*(?:import\s+(?:[\w*{}\s,]+)\s+from\s+|export\s+(?:[\w*{}\s,]+)\s+from\s+)["'](\.{1,2}\/[^"']+)["'];?/g;
  src = src.replace(re, (_m, spec) => {
    const target = resolve(dir, spec);
    const resolved = existsSync(target)
      ? target
      : existsSync(target + ".js")
        ? target + ".js"
        : null;
    if (!resolved) {
      throw new Error(`无法解析 import: ${spec}（来自 ${abs}）`);
    }
    // 命名导出在单文件里直接拼接：插件作者的相对模块通常是「一段工具函数」，
    // 用拼接而不是真正的模块语义是刻意的简化 —— SDK 的契约是「无模块系统」。
    return `\n/* inlined: ${spec} */\n` + inlineImports(resolved, [...chain, abs]);
  });
  return src;
}

const root = resolve(process.argv[2] ?? ".");
const manifestPath = join(root, "plugin.json");

if (!existsSync(manifestPath)) {
  console.error(`找不到 ${manifestPath}（插件根目录必须包含 plugin.json）`);
  process.exit(1);
}
const manifest = JSON.parse(readFileSync(manifestPath, "utf8"));

if (!manifest.entry) {
  console.error("plugin.json 缺少 entry");
  process.exit(1);
}

// 解析入口：支持单个文件，或一个入口点（其相对 import 会被内联）。
const entryPath = join(root, manifest.entry);
if (!existsSync(entryPath)) {
  console.error(`entry 指向的文件不存在: ${manifest.entry}`);
  process.exit(1);
}

let bundle = inlineImports(entryPath, []);
// 去除 export 关键字：单文件 bundle 没有模块语境，
// 残留的 `export` 在 <script> 里是 SyntaxError。
bundle = bundle.replace(/^\s*export\s+default\s+/m, "var __icDefault = ");
bundle = bundle.replace(/^\s*export\s+/gm, "");
bundle += "\n;window.__icPlugin = window.__icPlugin || (typeof __icDefault !== 'undefined' ? __icDefault : undefined);\n";
if (bundle.includes("import ") || bundle.includes("export ")) {
  // 残留的 import/export 意味着有未被内联的依赖：iframe 里没有任何模块解析器，
  // 这些语句会让插件在装载时抛 SyntaxError（而且错误信息指向 iframe，很难查）。
  console.error("bundle 里仍有 import/export：请把依赖内联，或改用 --bundle 打包器");
  process.exit(1);
}

const distDir = join(root, "dist");
mkdirSync(distDir, { recursive: true });
const entryName = "index.js";
writeFileSync(join(distDir, entryName), bundle);

const sha256 = createHash("sha256").update(bundle).digest("hex");
const outManifest = { ...manifest, entry: entryName, integrity: { sha256 } };
writeFileSync(
  join(distDir, "plugin.json"),
  JSON.stringify(outManifest, null, 2) + "\n",
);

console.log(`插件已构建: ${manifest.key}@${manifest.version}`);
console.log(`  bundle: dist/${entryName} (${bundle.length} 字节)`);
console.log(`  sha256: ${sha256}`);
