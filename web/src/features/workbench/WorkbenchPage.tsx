import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api, ApiFailure, newIdempotencyKey } from '@/shared/api';
import { useWorkspace } from '@/features/settings/useWorkspace';
import type { Run } from '@/shared/api/types';
import type { TFn } from '@/app/App';

interface Props {
  t: TFn;
  mode: 'image' | 'video';
}

/**
 * 工作台：与画布**共用执行引擎**（对齐 docs/design/10 §6.13 的调整）。
 * 这里不落画布，只做直通生成 + 历史记录。
 */
export function WorkbenchPage({ t, mode }: Props) {
  const { workspaceId, ready } = useWorkspace();
  const qc = useQueryClient();
  const [prompt, setPrompt] = useState('');
  const [count, setCount] = useState(1);
  const [size, setSize] = useState('1024x1024');
  const [seconds, setSeconds] = useState(8);
  const [runs, setRuns] = useState<Run[]>([]);
  const [error, setError] = useState<string | null>(null);

  const models = useQuery({
    queryKey: ['models', workspaceId, mode],
    queryFn: () => api.listModels(workspaceId, mode === 'image' ? 'image.generate' : 'video.generate'),
    enabled: ready,
    retry: false,
  });
  const [model, setModel] = useState('');

  const generate = useMutation({
    mutationFn: async () => {
      const run = await api.generate(
        workspaceId,
        {
          capability: mode === 'image' ? 'image.generate' : 'video.generate',
          model: model || undefined,
          prompt,
          params: mode === 'image' ? { size } : { seconds: String(seconds) },
          outputCount: count,
        },
        newIdempotencyKey(),
      );
      return run;
    },
    onSuccess: (run) => {
      setRuns((prev) => [run, ...prev]);
      setError(null);
      // 轮询运行状态直到终态（工作台无画布，因此不走 SSE 画布通道）
      void pollRun(run.id);
      void qc.invalidateQueries({ queryKey: ['assets', workspaceId] });
    },
    onError: (e) => {
      setError(e instanceof ApiFailure ? e.code : 'internal');
    },
  });

  async function pollRun(runId: string) {
    for (let i = 0; i < 60; i++) {
      await new Promise((r) => setTimeout(r, 1000));
      try {
        const fresh = await api.getRun(runId);
        setRuns((prev) => prev.map((r) => (r.id === runId ? fresh : r)));
        if (['succeeded', 'failed', 'canceled', 'partial'].includes(fresh.status)) return;
      } catch {
        return;
      }
    }
  }

  return (
    <div style={{ display: 'flex', height: '100%', minHeight: 0 }}>
      <section style={{ flex: 1, padding: 24, overflow: 'auto' }}>
        <h1 style={{ fontSize: 20, marginTop: 0 }}>
          {mode === 'image' ? t('workbench.image') : t('workbench.video')}
        </h1>

        <label style={{ display: 'block', marginBottom: 12 }}>
          <span className="ic-dim">{t('workbench.prompt')}</span>
          <textarea
            className="ic-textarea"
            rows={5}
            placeholder={t('workbench.promptPlaceholder')}
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
          />
        </label>

        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(160px, 1fr))', gap: 12 }}>
          <label>
            <span className="ic-dim">{t('workbench.model')}</span>
            <select className="ic-select" value={model} onChange={(e) => setModel(e.target.value)}>
              <option value="">—</option>
              {(models.data?.items ?? []).map((m) => (
                <option key={m.id} value={m.id}>{m.id}</option>
              ))}
            </select>
          </label>

          {mode === 'image' ? (
            <>
              <label>
                <span className="ic-dim">{t('workbench.size')}</span>
                <select className="ic-select" value={size} onChange={(e) => setSize(e.target.value)}>
                  {['1024x1024', '1536x1024', '1024x1536', '512x512'].map((s) => (
                    <option key={s} value={s}>{s}</option>
                  ))}
                </select>
              </label>
              <label>
                <span className="ic-dim">{t('workbench.count')}（1–10）</span>
                <input
                  className="ic-input" type="number" min={1} max={10} value={count}
                  onChange={(e) => setCount(Math.min(10, Math.max(1, Number(e.target.value) || 1)))}
                />
              </label>
            </>
          ) : (
            <label>
              <span className="ic-dim">{t('workbench.seconds')}（4–30）</span>
              <input
                className="ic-input" type="number" min={4} max={30} value={seconds}
                onChange={(e) => setSeconds(Math.min(30, Math.max(4, Number(e.target.value) || 4)))}
              />
            </label>
          )}
        </div>

        {error && <p className="ic-error">{t(`errors.${error}`)}</p>}

        <button
          className="ic-btn ic-btn--primary"
          style={{ marginTop: 16 }}
          disabled={!prompt.trim() || !ready || generate.isPending}
          onClick={() => generate.mutate()}
        >
          {generate.isPending ? t('common.loading') : mode === 'image' ? t('workbench.generateCount', { n: count }) : t('workbench.generate')}
        </button>
      </section>

      <aside style={{ width: 380, borderLeft: '1px solid var(--ic-border)', background: 'var(--ic-surface)', padding: 16, overflow: 'auto' }}>
        <strong>{t('workbench.history')}</strong>
        {runs.length === 0 && <div className="ic-empty">{t('common.empty')}</div>}
        {runs.map((r) => (
          <div key={r.id} className="ic-card" style={{ padding: 10, marginTop: 10 }}>
            <div style={{ display: 'flex', gap: 6, alignItems: 'center', fontSize: 12 }}>
              <span className={`ic-badge ${r.status === 'succeeded' ? 'ic-badge--ok' : r.status === 'failed' ? 'ic-badge--danger' : r.status === 'partial' ? 'ic-badge--warn' : ''}`}>
                {r.status}
              </span>
              <span className="ic-dim">{r.steps.length} steps</span>
            </div>
            {r.error && <div className="ic-error" style={{ fontSize: 12 }}>{t(`errors.${r.error.code}`)}</div>}
            {r.steps.map((s) => (
              <div key={s.id} style={{ marginTop: 6, display: 'flex', flexWrap: 'wrap', gap: 4 }}>
                {(s.outputs ?? []).map((id) => (
                  <img
                    key={id}
                    src={api.assetThumbUrl(id, workspaceId, 160)}
                    alt=""
                    style={{ width: 72, height: 72, objectFit: 'cover', borderRadius: 6 }}
                  />
                ))}
              </div>
            ))}
            <button
              className="ic-btn"
              style={{ fontSize: 11, padding: '3px 8px', marginTop: 6 }}
              onClick={() => {
                setPrompt(prompt);
              }}
            >
              {t('workbench.reuseParams')}
            </button>
          </div>
        ))}
      </aside>
    </div>
  );
}
