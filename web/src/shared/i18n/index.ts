import { zhCN, type Dict } from "./zh-CN";
import { enUS } from "./en-US";

export type Locale = "zh-CN" | "en-US";

const dicts: Record<Locale, Dict> = { "zh-CN": zhCN, "en-US": enUS };

/** 按点分路径读取文案，缺失时回落到 key 本身（便于发现漏配）。 */
export function translate(
  locale: Locale,
  path: string,
  vars?: Record<string, string | number>,
): string {
  const dict = dicts[locale] ?? zhCN;
  const parts = path.split(".");
  let cur: unknown = dict;
  for (const p of parts) {
    if (
      cur &&
      typeof cur === "object" &&
      p in (cur as Record<string, unknown>)
    ) {
      cur = (cur as Record<string, unknown>)[p];
    } else {
      return path;
    }
  }
  if (typeof cur !== "string") return path;
  if (!vars) return cur;
  return cur.replace(/\{(\w+)\}/g, (_, k: string) =>
    String(vars[k] ?? `{${k}}`),
  );
}

export { zhCN, enUS };
export type { Dict };
