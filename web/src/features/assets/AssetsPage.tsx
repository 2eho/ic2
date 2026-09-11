import { useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '@/shared/api';
import { useWorkspace } from '@/features/settings/useWorkspace';
import type { TFn } from '@/app/App';

/** 我的素材：类型筛选 + 搜索 + 上传 + 删除（对齐 docs/design/10 §7）。 */
export function AssetsPage({ t }: { t: TFn }) {
  const { workspaceId, ready } = useWorkspace();
  const qc = useQueryClient();
  const [kind, setKind] = useState('');
  const [q, setQ] = useState('');
  const fileRef = useRef<HTMLInputElement>(null);

  const list = useQuery({
    queryKey: ['assets', workspaceId, kind],
    queryFn: () => api.listAssets(workspaceId, kind, 60),
    enabled: ready,
  });

  const upload = useMutation({
    mutationFn: (files: FileList) => Promise.all(Array.from(files).map((f) => api.uploadAsset(workspaceId, f, f.name))),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['assets', workspaceId] }),
  });

  const remove = useMutation({
    mutationFn: (id: string) => api.deleteAsset(id, workspaceId),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['assets', workspaceId] }),
  });

  const items = (list.data?.items ?? []).filter((a) => !q || (a.name ?? '').toLowerCase().includes(q.toLowerCase()));

  return (
    <div style={{ padding: 24, maxWidth: 1200, margin: '0 auto' }}>
      <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginBottom: 16, flexWrap: 'wrap' }}>
        <h1 style={{ fontSize: 20, margin: 0, marginRight: 12 }}>{t('assets.title')}</h1>
        <input className="ic-input" style={{ width: 200 }} placeholder={t('common.search')} value={q} onChange={(e) => setQ(e.target.value)} />
        <select className="ic-select" style={{ width: 120 }} value={kind} onChange={(e) => setKind(e.target.value)}>
          {['', 'image', 'video', 'audio', 'text', 'json'].map((k) => (
            <option key={k} value={k}>{k || t('assets.kind.all')}</option>
          ))}
        </select>
        <button className="ic-btn ic-btn--primary" onClick={() => fileRef.current?.click()}>
          {t('common.upload')}
        </button>
        <input ref={fileRef} type="file" multiple hidden onChange={(e) => e.target.files && upload.mutate(e.target.files)} />
      </div>

      {items.length === 0 && <div className="ic-empty">{t('common.empty')}</div>}

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(160px, 1fr))', gap: 12 }}>
        {items.map((a) => (
          <div key={a.id} className="ic-card" style={{ padding: 8 }}>
            {a.kind === 'image' ? (
              <img src={api.assetThumbUrl(a.id, workspaceId, 320)} alt={a.name} style={{ width: '100%', height: 120, objectFit: 'cover', borderRadius: 6 }} />
            ) : (
              <div className="ic-empty" style={{ padding: 20, fontSize: 12 }}>{a.kind}</div>
            )}
            <div style={{ fontSize: 12, marginTop: 6, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
              {a.name || a.id.slice(0, 12)}
            </div>
            <div className="ic-dim ic-mono" style={{ fontSize: 11 }}>{Math.round(a.size / 1024)}KB · {a.mime}</div>
            <div style={{ display: 'flex', gap: 4, marginTop: 6 }}>
              <a className="ic-btn" style={{ fontSize: 11, padding: '3px 8px', textDecoration: 'none' }} href={api.assetRawUrl(a.id, workspaceId)} download>
                {t('common.download')}
              </a>
              <button className="ic-btn ic-btn--danger" style={{ fontSize: 11, padding: '3px 8px' }} onClick={() => remove.mutate(a.id)}>
                {t('common.delete')}
              </button>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
