import { useEffect, useMemo, useState } from "react";
import { api } from "@/shared/api";
import type { TFn } from "@/app/App";

/**
 * 自定义调用脚本编辑器（4.14）。
 *
 * 三步向导，而不是「一个大文本框 + 保存」：
 *
 *   1. **映射**：脚本内容（带可用变量与宿主函数清单）
 *   2. **请求预览**：确认脚本产出的请求形状（不发请求）
 *   3. **凭据与保存**
 *
 * 顺序是刻意的。上一版的 parity 矩阵把这一项标 todo 的理由是
 * 「先把『脚本能安全跑』做出来，再做 UI」——现在沙箱已经落地
 * （internal/sandbox：无 eval / 无循环 / 步数与超时上限），
 * 所以这一步才有意义。反过来先做 UI，就是给一个不可用的功能做界面。
 *
 * 编辑器里有两条**必须做**的静态检查，它们在保存前就拦住最常见的失败：
 *   - 引用了不存在的变量（服务端会报「未定义的变量」，但那时用户已经在等结果了）
 *   - 调用了白名单外的函数（例如 `fetch`），这是被明确拒绝的能力，不是笔误
 */
export interface ScriptDraft {
  capability: string;
  script: string;
}

interface Props {
  t: TFn;
  workspaceId: string;
  providerId: string;
  initial?: ScriptDraft;
  onClose: () => void;
  onSaved: () => void;
}

/** 脚本可读的环境变量（与 internal/provider/adapter/script 注入的一致）。 */
export const SCRIPT_ENV_VARS = [
  { name: "capability", desc: "当前能力，如 image.generate" },
  { name: "model", desc: "模型 ID" },
  { name: "prompt", desc: "已组装的提示词（含参考素材编号）" },
  { name: "count", desc: "期望结果数量" },
  { name: "params", desc: "参数对象（不含 script 自身）" },
  { name: "inputs", desc: "输入元信息数组：kind/label/assetId/hasData" },
  { name: "baseUrl", desc: "渠道 baseUrl（脚本只能请求它下面的路径）" },
];

/** 宿主函数白名单（与 internal/sandbox hostFuncTable 一致）。 */
export const SCRIPT_HOST_FUNCS = [
  "string",
  "number",
  "len",
  "join",
  "trim",
  "slice",
  "upper",
  "lower",
  "replace",
  "default",
  "has",
  "toast",
  "log",
];

/** 被明确拒绝的写法（不是笔误，是能力边界）。 */
const FORBIDDEN = [
  "eval",
  "Function",
  "__proto__",
  "constructor",
  "fetch",
  "require",
  "import",
  "process",
  "globalThis",
  "window",
  "setTimeout",
  "for ",
  "while ",
  "class ",
  "new ",
  "await ",
];

/**
 * 静态检查脚本。
 *
 * 只做**能确定**的判断：未声明的标识符、白名单外的调用、被拒绝的关键字。
 * 不做类型推断——一个会误报的检查比没有检查更糟，用户会开始忽略红色提示。
 */
export function lintScript(src: string): { errors: string[]; calls: string[] } {
  const errors: string[] = [];
  const calls: string[] = [];
  const stripped = src
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .replace(/\/\/[^\n]*/g, "")
    .replace(/'[^']*'|"[^"]*"|`[^`]*`/g, '""');

  for (const word of FORBIDDEN) {
    const re = new RegExp(
      word.trim() === word
        ? `\\b${word.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\b`
        : word.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"),
    );
    if (re.test(stripped)) {
      errors.push(`不支持 ${word.trim()}：沙箱只提供受限映射能力`);
    }
  }

  const declared = new Set([
    ...SCRIPT_ENV_VARS.map((v) => v.name),
    ...SCRIPT_HOST_FUNCS,
    "var",
    "if",
    "else",
    "return",
    "true",
    "false",
    "null",
    "undefined",
    "typeof",
    "in",
    "out",
  ]);
  // 局部变量声明
  for (const m of stripped.matchAll(/\bvar\s+([A-Za-z_$][\w$]*)/g)) {
    declared.add(m[1]);
  }

  for (const m of stripped.matchAll(/([A-Za-z_$][\w$]*)\s*\(/g)) {
    const name = m[1];
    if (["if"].includes(name)) continue;
    calls.push(name);
    if (!SCRIPT_HOST_FUNCS.includes(name)) {
      errors.push(`函数 ${name}() 不在白名单内，可用：${SCRIPT_HOST_FUNCS.join(" / ")}`);
    }
  }

  for (const m of stripped.matchAll(/(^|[^.\w$])([A-Za-z_$][\w$]*)/g)) {
    const name = m[2];
    if (declared.has(name)) continue;
    // 属性名（`a.b` 里的 b）不在此列：上面的模式已经排除了紧跟在 `.` 后的标识符
    errors.push(`未定义的变量 ${name}`);
  }

  return { errors: [...new Set(errors)], calls: [...new Set(calls)] };
}

const DEFAULT_SCRIPT = `// 把统一请求映射成你的渠道协议。
// 注意：脚本只描述请求，不发请求 —— 出口在服务端（SSRF 守卫、超时、计量都还在）。
return {
  method: "POST",
  path: "/v1/generate",
  headers: { "x-model": model },
  body: {
    prompt: prompt,
    n: count,
    size: default(params.size, "1024x1024"),
  },
  // 结果在响应里的位置
  responsePath: "data[0].url",
};`;

export function ScriptEditor({
  t,
  workspaceId,
  providerId,
  initial,
  onClose,
  onSaved,
}: Props) {
  const [step, setStep] = useState(1);
  const [script, setScript] = useState(initial?.script ?? DEFAULT_SCRIPT);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const lint = useMemo(() => lintScript(script), [script]);
  const blocking = lint.errors.length > 0;

  useEffect(() => {
    setError(null);
  }, [step]);

  const save = async () => {
    setSaving(true);
    setError(null);
    try {
      // 脚本随渠道配置保存（provider_credentials.limits.script 是它的落位）。
      await api.createCredential(workspaceId, providerId, {
        name: `${t("settings.scriptEditor")}-${Date.now()}`,
        secret: "",
        priority: 0,
        limits: { script },
      });
      onSaved();
    } catch (e) {
      setError((e as { code?: string }).code ?? "internal");
    } finally {
      setSaving(false);
    }
  };

  return (
    <div
      style={{
        position: "fixed",
        inset: 0,
        background: "rgba(0,0,0,.35)",
        display: "grid",
        placeItems: "center",
        zIndex: 1500,
      }}
      onClick={onClose}
    >
      <div
        className="ic-card"
        style={{ width: 720, maxWidth: "94vw", padding: 18 }}
        onClick={(e) => e.stopPropagation()}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <strong style={{ flex: 1 }}>{t("settings.scriptEditor")}</strong>
          <span className="ic-badge" style={{ fontSize: 10 }}>
            {step}/3 {t(`settings.scriptStep.${step}`)}
          </span>
          <button className="ic-btn ic-btn--ghost" onClick={onClose}>
            ✕
          </button>
        </div>

        {step === 1 && (
          <div style={{ marginTop: 12 }}>
            <div style={{ display: "flex", gap: 12 }}>
              <div style={{ flex: 1 }}>
                <textarea
                  className="ic-input ic-mono"
                  spellCheck={false}
                  style={{ width: "100%", height: 300, fontSize: 12 }}
                  value={script}
                  onChange={(e) => setScript(e.target.value)}
                />
              </div>
              <div style={{ width: 220, fontSize: 11 }}>
                <div className="ic-dim">{t("settings.scriptVars")}</div>
                <ul style={{ paddingLeft: 16, margin: "4px 0 10px" }}>
                  {SCRIPT_ENV_VARS.map((v) => (
                    <li key={v.name} title={v.desc}>
                      <code>{v.name}</code>
                    </li>
                  ))}
                </ul>
                <div className="ic-dim">{t("settings.scriptFuncs")}</div>
                <ul style={{ paddingLeft: 16, margin: "4px 0 0" }}>
                  {SCRIPT_HOST_FUNCS.map((f) => (
                    <li key={f}>
                      <code>{f}()</code>
                    </li>
                  ))}
                </ul>
              </div>
            </div>
            {lint.errors.length > 0 && (
              <ul className="ic-error" style={{ fontSize: 12 }}>
                {lint.errors.slice(0, 6).map((e) => (
                  <li key={e}>{e}</li>
                ))}
              </ul>
            )}
          </div>
        )}

        {step === 2 && (
          <div style={{ marginTop: 12 }}>
            <p className="ic-dim" style={{ fontSize: 12 }}>
              {t("settings.scriptPreviewHint")}
            </p>
            <pre
              className="ic-mono"
              style={{
                background: "var(--ic-surface-2)",
                padding: 12,
                borderRadius: 8,
                fontSize: 12,
                maxHeight: 320,
                overflow: "auto",
              }}
            >
              {JSON.stringify(
                {
                  method: "POST",
                  path: "（由脚本决定，仅允许渠道 baseUrl 下的路径）",
                  headers: { "x-model": "（脚本产出）" },
                  responsePath: "（脚本产出）",
                  calledFunctions: lint.calls,
                  scriptBytes: new TextEncoder().encode(script).length,
                },
                null,
                2,
              )}
            </pre>
          </div>
        )}

        {step === 3 && (
          <div style={{ marginTop: 12, fontSize: 13 }}>
            <p>{t("settings.scriptSaveHint")}</p>
            <ul className="ic-dim" style={{ fontSize: 12 }}>
              <li>{t("settings.scriptRisk1")}</li>
              <li>{t("settings.scriptRisk2")}</li>
              <li>{t("settings.scriptRisk3")}</li>
            </ul>
          </div>
        )}

        {error && (
          <p className="ic-error" style={{ fontSize: 12 }}>
            {t(`errors.${error}`) !== `errors.${error}`
              ? t(`errors.${error}`)
              : error}
          </p>
        )}

        <div style={{ display: "flex", gap: 6, marginTop: 14 }}>
          <button
            className="ic-btn"
            disabled={step === 1}
            onClick={() => setStep((s) => s - 1)}
          >
            {t("common.cancel")}
          </button>
          <div style={{ flex: 1 }} />
          {step < 3 ? (
            <button
              className="ic-btn ic-btn--primary"
              disabled={blocking}
              onClick={() => setStep((s) => s + 1)}
            >
              {t("settings.scriptNext")}
            </button>
          ) : (
            <button
              className="ic-btn ic-btn--primary"
              disabled={saving || blocking}
              onClick={save}
            >
              {t("common.save")}
            </button>
          )}
        </div>
      </div>
    </div>
  );
}
