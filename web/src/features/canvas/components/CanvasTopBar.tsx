import { useState } from 'react';
import { Link } from 'react-router-dom';
import type { CanvasKernel } from '../kernel';
import { useKernelViewport } from '../hooks/useKernel';
import type { TFn } from '@/app/App';

interface Props {
  t: TFn;
  kernel: CanvasKernel;
  canvasId: string;
  syncState: 'idle' | 'syncing' | 'error' | 'conflict' | 'offline';
  onToggleRuns: () => void;
  onSaveViewport: () => void;
}

const SHORTCUTS: Array<[string, string]> = [
  ['Ctrl/Cmd + Z', '撤销'],
  ['Ctrl/Cmd + Shift + Z', '重做'],
  ['Ctrl/Cmd + Y', '重做'],
  ['Ctrl/Cmd + A', '全选'],
  ['Ctrl/Cmd + 拖拽', '框选'],
  ['空格 + 拖拽', '平移画布'],
  ['Ctrl + 滚轮', '缩放'],
  ['Delete / Backspace', '删除选中'],
  ['Escape', '取消选择'],
  ['双击标题', '重命名节点'],
  ['双击节点', '编辑内容'],
  ['右键空白', '创建节点'],
  ['右键节点', '节点菜单'],
  ['拖入文件', '上传到画布'],
  ['Ctrl/Cmd + C / V', '复制粘贴'],
];

export function CanvasTopBar({ t, kernel, canvasId, syncState, onToggleRuns, onSaveViewport }: Props) {
  const [showShortcuts, setShowShortcuts] = useState(false);
  const vp = useKernelViewport(kernel);

  const syncLabel = {
    idle: '',
    syncing: '同步中…',
    error: t('canvas.syncFail'),
    conflict: t('canvas.conflict'),
    offline: t('canvas.offline'),
  }[syncState];

  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 10,
        padding: '8px 12px',
        borderBottom: '1px solid var(--ic-border)',
        background: 'var(--ic-surface)',
        zIndex: 10,
      }}
    >
      <Link className="ic-btn ic-btn--ghost" to="/projects">
        ← {t('common.back')}
      </Link>
      <strong style={{ fontSize: 14 }}>{kernel.documentId}</strong>
      <span className="ic-badge">v{kernel.currentVersion}</span>
      <span className="ic-badge">{kernel.scene.size} {t('canvas.nodes')}</span>
      <span className="ic-dim ic-mono" style={{ fontSize: 11 }}>
        {Math.round(vp.k * 100)}% · ({Math.round(vp.x)}, {Math.round(vp.y)})
      </span>
      {syncLabel && <span className="ic-badge ic-badge--warn">{syncLabel}</span>}
      <div style={{ flex: 1 }} />
      <button className="ic-btn" onClick={onToggleRuns}>{t('canvas.run.title')}</button>
      <button className="ic-btn" onClick={() => setShowShortcuts(true)}>{t('canvas.shortcuts')}</button>
      <button className="ic-btn" onClick={onSaveViewport}>保存视口</button>
      <a className="ic-btn" href={`/api/v1/canvases/${canvasId}/export`} download>
        {t('assets.exportZip')}
      </a>

      {showShortcuts && (
        <div
          style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,.35)', display: 'grid', placeItems: 'center', zIndex: 1000 }}
          onClick={() => setShowShortcuts(false)}
        >
          <div className="ic-card" style={{ width: 420, padding: 16 }} onClick={(e) => e.stopPropagation()}>
            <strong>{t('canvas.shortcuts')}</strong>
            <table style={{ width: '100%', marginTop: 10, fontSize: 13, borderCollapse: 'collapse' }}>
              <tbody>
                {SHORTCUTS.map(([k, v]) => (
                  <tr key={k}>
                    <td className="ic-mono" style={{ padding: '3px 0' }}>{k}</td>
                    <td className="ic-dim">{v}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  );
}
