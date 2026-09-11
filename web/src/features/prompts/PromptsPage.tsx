import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '@/shared/api';
import { useWorkspace } from '@/features/settings/useWorkspace';
import type { TFn } from '@/app/App';

/** 提示词库：服务端检索 + 复制（不再浏览器直连 7 个仓库，见 DIV-01）。 */
export function PromptsPage({ t }: { t: TFn }) {
  const { workspaceId, ready } = useWorkspace();
  const [q, setQ] = useState('');
  const [tags, setTags] = useState('');
  const [copiedId, setCopiedId] = useState<string | null>(null);

  const list = useQuery({
    queryKey: ['prompts', workspaceId, q, tags],
    queryFn: () => api.searchPrompts(workspaceId, q, tags ? tags.split(',').map((s) => s.trim()) : [], 60),
    enabled: ready,
    retry: false,
  });

  const copy = async (id: string, content: string) => {
    try {
      await navigator.clipboard.writeText(content);
      setCopiedId(id);
      setTimeout(() => setCopiedId(null), 1500);
    } catch {
      // 剪贴板不可用
    }
  };

  return (
    <div style={{ padding: 24, maxWidth: 1200, margin: '0 auto' }}>
      <h1 style={{ fontSize: 20, marginTop: 0 }}>{t('prompts.title')}</h1>
      <div style={{ display: 'flex', gap: 8, marginBottom: 16 }}>
        <input className="ic-input" placeholder={t('prompts.searchPlaceholder')} value={q} onChange={(e) => setQ(e.target.value)} />
        <input className="ic-input" style={{ width: 220 }} placeholder={t('prompts.tags')} value={tags} onChange={(e) => setTags(e.target.value)} />
      </div>

      {(list.data?.items ?? []).length === 0 && (
        <div className="ic-empty">
          {t('common.empty')}
          <p className="ic-dim" style={{ fontSize: 12 }}>在「配置中心 → 提示词来源」添加来源后点「立即拉取」。</p>
        </div>
      )}

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(240px, 1fr))', gap: 12 }}>
        {(list.data?.items ?? []).map((p) => (
          <div key={p.id} className="ic-card" style={{ padding: 12 }}>
            <strong style={{ fontSize: 13 }}>{p.title}</strong>
            <p className="ic-dim" style={{ fontSize: 12, minHeight: 48 }}>{p.content.slice(0, 90)}</p>
            {p.tags.length > 0 && (
              <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap', marginBottom: 6 }}>
                {p.tags.slice(0, 4).map((tag) => (
                  <span key={tag} className="ic-badge" style={{ fontSize: 10 }}>{tag}</span>
                ))}
              </div>
            )}
            <button className="ic-btn" style={{ fontSize: 12 }} onClick={() => copy(p.id, p.content)}>
              {copiedId === p.id ? t('common.copySuccess') : t('prompts.copyPrompt')}
            </button>
          </div>
        ))}
      </div>
    </div>
  );
}
