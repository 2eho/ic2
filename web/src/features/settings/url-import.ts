/**
 * URL 参数导入凭据（对齐 docs/design/10 §8.9）。
 *
 * 场景：服务商给一个「一键配置」链接，用户点开就自动配好渠道。
 *
 * 两条必须做对的安全细节（原项目踩过第一条）：
 *   1. **导入后立刻从地址栏清除参数**。把 `?apiKey=sk-xxx` 留在 URL 里，
 *      它会进浏览器历史、进 referer、被截图发出去——一次泄露就是永久的；
 *   2. **不落 localStorage**。参数只交给调用方（写入服务端凭据），
 *      本模块自己不存任何东西。
 *
 * 用 history.replaceState 而不是 pushState：push 会让「后退」回到带密钥的 URL。
 */

export interface UrlCredentialParams {
  baseUrl: string;
  apiKey: string;
  name?: string;
  model?: string;
}

/** 从当前地址解析凭据参数（不修改地址栏）。 */
export function parseCredentialParams(
  search: string,
): UrlCredentialParams | null {
  const params = new URLSearchParams(
    search.startsWith("?") ? search : `?${search}`,
  );
  const apiKey = params.get("apiKey") ?? params.get("api_key") ?? "";
  const baseUrl = params.get("baseUrl") ?? params.get("base_url") ?? "";
  if (!apiKey && !baseUrl) return null;
  const out: UrlCredentialParams = { baseUrl, apiKey };
  const name = params.get("name");
  const model = params.get("model");
  if (name) out.name = name;
  if (model) out.model = model;
  return out;
}

/** 已知的凭据参数名（清理时全部移除）。 */
const CREDENTIAL_KEYS = [
  "apiKey",
  "api_key",
  "baseUrl",
  "base_url",
  "token",
  "access_token",
];

/**
 * 清除地址栏中的凭据参数，返回是否发生了清理。
 *
 * 用 replaceState：pushState 会让「后退」回到带密钥的 URL，
 * 那样「清除」就没有意义了。
 */
export function stripCredentialParams(win: Window = window): boolean {
  const url = new URL(win.location.href);
  let removed = false;
  for (const key of CREDENTIAL_KEYS) {
    if (url.searchParams.has(key)) {
      url.searchParams.delete(key);
      removed = true;
    }
  }
  if (removed) {
    win.history.replaceState(
      {},
      "",
      url.pathname + (url.search ? url.search : "") + url.hash,
    );
  }
  return removed;
}

/**
 * 完整的「读取并清除」流程：一次性把参数取出并从地址栏抹掉。
 *
 * 返回 null 表示地址栏没有凭据参数（正常访问）。
 */
export function consumeCredentialParams(
  win: Window = window,
): UrlCredentialParams | null {
  const parsed = parseCredentialParams(win.location.search);
  if (!parsed) return null;
  stripCredentialParams(win);
  return parsed;
}
