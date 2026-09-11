import { NavLink, Outlet, useLocation } from 'react-router-dom';
import { useState } from 'react';
import type { Session } from '@/shared/api';
import type { Locale } from '@/shared/i18n';
import type { TFn } from './App';

interface Props {
  t: TFn;
  session: Session;
  onLogout: () => void;
  locale: Locale;
  theme: 'light' | 'dark';
  onLocaleChange: (l: Locale) => void;
  onThemeChange: (t: 'light' | 'dark') => void;
}

const NAV = [
  { to: '/', key: 'nav.home', exact: true },
  { to: '/projects', key: 'nav.canvases' },
  { to: '/workbench/image', key: 'nav.image' },
  { to: '/workbench/video', key: 'nav.video' },
  { to: '/prompts', key: 'nav.prompts' },
  { to: '/assets', key: 'nav.assets' },
  { to: '/settings', key: 'nav.settings' },
];

export function AppShell({ t, session, onLogout, locale, theme, onLocaleChange, onThemeChange }: Props) {
  const location = useLocation();
  // 画布页隐藏顶栏，把整屏让给画布（见 docs/design/10 §1.9）
  const isCanvas = location.pathname.startsWith('/canvas/');
  const [drawerOpen, setDrawerOpen] = useState(false);

  if (isCanvas) {
    return <Outlet />;
  }

  return (
    <div style={{ minHeight: '100%', display: 'flex', flexDirection: 'column' }}>
      <header
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 16,
          padding: '10px 16px',
          borderBottom: '1px solid var(--ic-border)',
          background: 'var(--ic-surface)',
          position: 'sticky',
          top: 0,
          zIndex: 20,
        }}
      >
        <strong style={{ fontSize: 16, letterSpacing: 0.2 }}>{t('common.appName')}</strong>
        <nav style={{ display: 'flex', gap: 4, flex: 1, flexWrap: 'wrap' }} className="ic-topnav">
          {NAV.map((n) => (
            <NavLink
              key={n.to}
              to={n.to}
              end={n.exact}
              style={({ isActive }) => ({
                padding: '6px 10px',
                borderRadius: 8,
                textDecoration: 'none',
                color: isActive ? 'var(--ic-accent)' : 'var(--ic-text-dim)',
                background: isActive ? 'var(--ic-accent-soft)' : 'transparent',
                fontSize: 13,
              })}
            >
              {t(n.key)}
            </NavLink>
          ))}
        </nav>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <select
            className="ic-select"
            style={{ width: 'auto' }}
            value={locale}
            onChange={(e) => onLocaleChange(e.target.value as Locale)}
            aria-label={t('settings.language')}
          >
            <option value="zh-CN">简体中文</option>
            <option value="en-US">English</option>
          </select>
          <button
            className="ic-btn ic-btn--ghost"
            onClick={() => onThemeChange(theme === 'light' ? 'dark' : 'light')}
            title={t('settings.theme')}
          >
            {theme === 'light' ? '🌙' : '☀️'}
          </button>
          <span className="ic-dim" style={{ fontSize: 13 }}>
            {session.user.name}
          </span>
          <button className="ic-btn" onClick={onLogout}>
            {t('auth.logout')}
          </button>
        </div>
      </header>
      <div style={{ display: 'none' }} aria-hidden>
        <button onClick={() => setDrawerOpen(!drawerOpen)}>{t('nav.home')}</button>
      </div>
      <main style={{ flex: 1, minHeight: 0 }}>
        <Outlet />
      </main>
    </div>
  );
}
