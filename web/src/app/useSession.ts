import { useCallback, useEffect, useState } from 'react';
import { api, ApiFailure, setTokenProvider, type Session } from '@/shared/api';
import { getStoredToken, setStoredToken } from './session';

export interface SessionState {
  session: Session | null;
  loading: boolean;
  error: string | null;
  login: (email: string, password: string) => Promise<void>;
  register: (email: string, name: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
}

/**
 * 会话管理：优先用已存 token 拉 /me 校验，失败则视为未登录。
 * 刻意不做「静默续期」——过期即明确提示重新登录，避免隐藏状态。
 */
export function useSession(): SessionState {
  const [session, setSession] = useState<Session | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const token = getStoredToken();
    if (!token) {
      setLoading(false);
      return;
    }
    setTokenProvider(() => token);
    api
      .me()
      .then((me) => {
        setSession({
          token,
          user: me.user,
          workspaces: me.workspaces,
          expiresAt: '',
        });
      })
      .catch(() => {
        setStoredToken(null);
        setSession(null);
      })
      .finally(() => setLoading(false));
  }, []);

  const login = useCallback(async (email: string, password: string) => {
    setError(null);
    try {
      const s = await api.login(email, password);
      setStoredToken(s.token);
      setTokenProvider(() => s.token);
      setSession(s);
    } catch (e) {
      setError(e instanceof ApiFailure ? e.code : 'unknown');
      throw e;
    }
  }, []);

  const register = useCallback(async (email: string, name: string, password: string) => {
    setError(null);
    try {
      const s = await api.register(email, name, password);
      setStoredToken(s.token);
      setTokenProvider(() => s.token);
      setSession(s);
    } catch (e) {
      setError(e instanceof ApiFailure ? e.code : 'unknown');
      throw e;
    }
  }, []);

  const logout = useCallback(async () => {
    try {
      await api.logout();
    } catch {
      // 登出失败也要清本地态，避免卡在「看似已登录」
    }
    setStoredToken(null);
    setTokenProvider(() => null);
    setSession(null);
  }, []);

  return { session, loading, error, login, register, logout };
}
