/**
 * API 客户端：唯一与后端通信的出口。
 * 契约真源：contracts/openapi.yaml（见 docs/design/08 §6 契约先行）。
 */

export interface ApiErrorBody {
  code: string;
  message: string;
  details?: Record<string, unknown>;
  traceId?: string;
}

export class ApiFailure extends Error {
  readonly code: string;
  readonly status: number;
  readonly details?: Record<string, unknown>;
  readonly traceId?: string;

  constructor(status: number, body: ApiErrorBody) {
    super(body.message);
    this.name = "ApiFailure";
    this.status = status;
    this.code = body.code;
    this.details = body.details;
    this.traceId = body.traceId;
  }
}

let tokenProvider: () => string | null = () => null;

/** 由 app 层注入 token 来源（内存），避免散落的 localStorage 访问。 */
export function setTokenProvider(fn: () => string | null): void {
  tokenProvider = fn;
}

export interface RequestOptions {
  method?: "GET" | "POST" | "PATCH" | "DELETE";
  body?: unknown;
  signal?: AbortSignal;
  idempotencyKey?: string;
  formData?: FormData;
}

export async function request<T>(
  path: string,
  opts: RequestOptions = {},
): Promise<T> {
  const headers: Record<string, string> = {};
  const token = tokenProvider();
  if (token) headers["Authorization"] = `Bearer ${token}`;
  if (opts.idempotencyKey) headers["Idempotency-Key"] = opts.idempotencyKey;

  let body: BodyInit | undefined;
  if (opts.formData) {
    body = opts.formData;
  } else if (opts.body !== undefined) {
    headers["Content-Type"] = "application/json";
    body = JSON.stringify(opts.body);
  }

  const res = await fetch(path, {
    method: opts.method ?? (body ? "POST" : "GET"),
    headers,
    body,
    signal: opts.signal,
    credentials: "same-origin",
  });

  if (res.status === 204) return undefined as T;

  const text = await res.text();
  const parsed = text ? safeJson(text) : undefined;

  if (!res.ok) {
    const err = (parsed ?? {}) as Partial<ApiErrorBody>;
    throw new ApiFailure(res.status, {
      code: err.code ?? "unknown",
      message: err.message ?? `HTTP ${res.status}`,
      details: err.details,
      traceId: err.traceId ?? res.headers.get("X-Trace-Id") ?? undefined,
    });
  }
  return parsed as T;
}

function safeJson(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return undefined;
  }
}

/** 可重试判定：网络异常、5xx、429。 */
export function isRetryable(err: unknown): boolean {
  if (err instanceof ApiFailure) return err.status >= 500 || err.status === 429;
  return true;
}

/** 生成幂等键（同一用户操作重试时复用，避免重复扣费，INV-2/INV-3）。 */
export function newIdempotencyKey(): string {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto)
    return crypto.randomUUID();
  return `idem_${Date.now()}_${Math.random().toString(36).slice(2)}`;
}
