import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '@/shared/api';
import { useWorkspace } from '@/features/settings/useWorkspace';
import type { TFn } from '@/app/App';

/** 我的画布：项目卡片列表（对齐 docs/design/10 §1.2）。 */
export function ProjectListPage({ t }: { t: TFn }) {
  const { workspaceId, ready } = useWorkspace();
  const qc = useQueryClient();
  const [name, setName] = useState('');

  const projects = useQuery({
    queryKey: ['projects', workspaceId],
    queryFn: () => api.listProjects(workspaceId),
    enabled: ready,
  });

  const create = useMutation({
    mutationFn: () => api.createProject(workspaceId, name || t('canvas.projectName')),
    onSuccess: () => {
      setName('');
      void qc.invalidateQueries({ queryKey: ['projects', workspaceId] });
    },
  });

  return (
    <div style={{ padding: 24, maxWidth: 1200, margin: '0 auto' }}>
      <h1 style={{ fontSize: 20, marginTop: 0 }}>{t('nav.canvases')}</h1>

      <div style={{ display: 'flex', gap: 8, marginBottom: 20 }}>
        <input className="ic-input" placeholder={t('canvas.projectName')} value={name} onChange={(e) => setName(e.target.value)} />
        <button className="ic-btn ic-btn--primary" onClick={() => create.mutate()} disabled={!ready || create.isPending}>
          {t('canvas.newCanvas')}
        </button>
      </div>

      {!ready && <div className="ic-empty">{t('common.loading')}</div>}
      {projects.data?.items.length === 0 && <div className="ic-empty">{t('common.empty')}</div>}

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(240px, 1fr))', gap: 12 }}>
        {(projects.data?.items ?? []).map((p) => (
          <ProjectCard key={p.id} t={t} projectId={p.id} name={p.name} description={p.description ?? ''} canvasCount={p.canvasCount} />
        ))}
      </div>
    </div>
  );
}

function ProjectCard({
  t, projectId, name, description, canvasCount,
}: { t: TFn; projectId: string; name: string; description: string; canvasCount: number }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  /** 打开项目：已有画布则直接进入最近一个，否则先创建（对齐原项目交互）。 */
  const openCanvas = async () => {
    setBusy(true);
    setError(null);
    try {
      const list = await api.listCanvases(projectId);
      const first = list.items[0];
      if (first) {
        window.location.href = `/canvas/${first.id}`;
        return;
      }
      const created = await api.createCanvas(projectId, t('canvas.untitled'));
      window.location.href = `/canvas/${created.canvas.id}`;
    } catch (e) {
      setError((e as { code?: string }).code ?? 'internal');
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="ic-card" style={{ padding: 16, display: 'flex', flexDirection: 'column', gap: 8 }}>
      <strong>{name}</strong>
      {description && <span className="ic-dim" style={{ fontSize: 12 }}>{description}</span>}
      <span className="ic-badge">{canvasCount} {t('nav.canvases')}</span>
      {error && <span className="ic-error">{t(`errors.${error}`)}</span>}
      <button className="ic-btn ic-btn--primary" disabled={busy} onClick={openCanvas}>
        {busy ? t('common.loading') : t('canvas.newCanvas')}
      </button>
    </div>
  );
}
