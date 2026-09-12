/** 会话 token 存取。刻意不用 localStorage：只在内存 + sessionStorage。 */
const KEY = "ic.session.token";

let memoryToken: string | null = null;

export function getStoredToken(): string | null {
  if (memoryToken) return memoryToken;
  try {
    memoryToken = sessionStorage.getItem(KEY);
  } catch {
    memoryToken = null;
  }
  return memoryToken;
}

export function setStoredToken(token: string | null): void {
  memoryToken = token;
  try {
    if (token) sessionStorage.setItem(KEY, token);
    else sessionStorage.removeItem(KEY);
  } catch {
    // 隐私模式下 sessionStorage 可能不可用，内存兜底
  }
}
