import { useCallback, useEffect, useState } from 'react';
import { Navigate, Route, Routes } from 'react-router-dom';
import { AppShell } from './AppShell';
import { useSession } from './useSession';
import { LoginPage } from '@/features/auth/LoginPage';
import { HomePage } from '@/features/home/HomePage';
import { ProjectListPage } from '@/features/canvas/ProjectListPage';
import { CanvasPage } from '@/features/canvas/CanvasPage';
import { WorkbenchPage } from '@/features/workbench/WorkbenchPage';
import { AssetsPage } from '@/features/assets/AssetsPage';
import { PromptsPage } from '@/features/prompts/PromptsPage';
import { SettingsPage } from '@/features/settings/SettingsPage';
import { NotFoundPage } from './NotFoundPage';
import { translate, type Locale } from '@/shared/i18n';

export function App() {
  const { session, loading, logout } = useSession();
  const [locale, setLocale] = useState<Locale>('zh-CN');
  const [theme, setTheme] = useState<'light' | 'dark'>('light');
  const t = useCallback((key: string, vars?: Record<string, string | number>) => translate(locale, key, vars), [locale]);

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
  }, [theme]);

  useEffect(() => {
    document.documentElement.lang = locale;
  }, [locale]);

  if (loading) {
    return <div className="ic-empty">{t('common.loading')}</div>;
  }

  return (
    <Routes>
      <Route
        path="/login"
        element={session ? <Navigate to="/" replace /> : <LoginPage t={t} />}
      />
      <Route
        element={
          session ? (
            <AppShell t={t} session={session} onLogout={logout} locale={locale} theme={theme}
              onLocaleChange={setLocale} onThemeChange={setTheme} />
          ) : (
            <Navigate to="/login" replace />
          )
        }
      >
        <Route path="/" element={<HomePage t={t} />} />
        <Route path="/projects" element={<ProjectListPage t={t} />} />
        <Route path="/canvas/:canvasId" element={<CanvasPage t={t} locale={locale} />} />
        <Route path="/workbench/image" element={<WorkbenchPage t={t} mode="image" />} />
        <Route path="/workbench/video" element={<WorkbenchPage t={t} mode="video" />} />
        <Route path="/assets" element={<AssetsPage t={t} />} />
        <Route path="/prompts" element={<PromptsPage t={t} />} />
        <Route path="/settings/*" element={<SettingsPage t={t} />} />
        <Route path="*" element={<NotFoundPage t={t} />} />
      </Route>
    </Routes>
  );
}

export type TFn = (key: string, vars?: Record<string, string | number>) => string;
