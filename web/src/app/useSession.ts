import { useCallback, useEffect, useRef, useState } from "react";
import { api, ApiFailure, setTokenProvider, type Session } from "@/shared/api";
import { getStoredToken, setStoredToken } from "./session";

export interface SessionState {
  session: Session | null;
  loading: boolean;
  error: string | null;
  openAccess: boolean;
  login: (email: string, password: string) => Promise<void>;
  register: (email: string, name: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
}

/**
 * 会话管理：优先用已存 token 拉 /me 校验；无 token 时若 meta.features.openAccess
 * 则自动 POST /auth/open，永不展示登录墙。
 */
export function useSession(): SessionState {
  const [session, setSession] = useState<Session | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [openAccess, setOpenAccess] = useState(false);
  const openAccessRef = useRef(false);

  const applySession = useCallback((s: Session) => {
    setStoredToken(s.token);
    setTokenProvider(() => s.token);
    setSession(s);
  }, []);

  const openSession = useCallback(async (): Promise<Session> => {
    const s = await api.openAccess();
    applySession(s);
    return s;
  }, [applySession]);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const meta = await api.meta();
        const oa = Boolean(meta.features?.openAccess);
        if (cancelled) return;
        setOpenAccess(oa);
        openAccessRef.current = oa;

        const token = getStoredToken();
        if (token) {
          setTokenProvider(() => token);
          try {
            const me = await api.me();
            if (cancelled) return;
            setSession({
              token,
              user: me.user,
              workspaces: me.workspaces,
              expiresAt: "",
            });
            return;
          } catch {
            setStoredToken(null);
            setTokenProvider(() => null);
            if (!oa) {
              setSession(null);
              return;
            }
          }
        }

        if (oa) {
          await openSession();
        }
      } catch (e) {
        if (!cancelled) {
          setError(e instanceof ApiFailure ? e.code : "unknown");
        }
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [openSession]);

  const login = useCallback(
    async (email: string, password: string) => {
      setError(null);
      try {
        const s = await api.login(email, password);
        applySession(s);
      } catch (e) {
        setError(e instanceof ApiFailure ? e.code : "unknown");
        throw e;
      }
    },
    [applySession],
  );

  const register = useCallback(
    async (email: string, name: string, password: string) => {
      setError(null);
      try {
        const s = await api.register(email, name, password);
        applySession(s);
      } catch (e) {
        setError(e instanceof ApiFailure ? e.code : "unknown");
        throw e;
      }
    },
    [applySession],
  );

  const logout = useCallback(async () => {
    try {
      await api.logout();
    } catch {
      // 登出失败也要清本地态
    }
    setStoredToken(null);
    setTokenProvider(() => null);
    if (openAccessRef.current) {
      try {
        await openSession();
        return;
      } catch {
        // fall through to clear
      }
    }
    setSession(null);
  }, [openSession]);

  return { session, loading, error, openAccess, login, register, logout };
}
