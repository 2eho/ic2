import { useEffect, useMemo, useState } from 'react';
import { useParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { api, ApiFailure, connectCanvasEvents, isRetryable, newIdempotencyKey } from '@/shared/api';
import { CanvasKernel } from './kernel';
import { CanvasSurface } from './components/CanvasSurface';
import { CanvasToolbar } from './components/CanvasToolbar';
import { SidePanel } from './components/SidePanel';
import { RunPanel } from './components/RunPanel';
import { CanvasTopBar } from './components/CanvasTopBar';
import type { TFn } from '@/app/App';
import type { Locale } from '@/shared/i18n';
import { useCanvasSync } from './store/useCanvasSync';

type SyncState = 'idle' | 'syncing' | 'error' | 'conflict' | 'offline';

export function CanvasPage({ t, locale }: { t: TFn; locale: Locale }) {
  const { canvasId = '' } = useParams();
  const docQuery = useQuery({ queryKey: ['canvas', canvasId], queryFn: () => api.getCanvas(canvasId) });
  const [kernel, setKernel] = useState<CanvasKernel | null>(null);
  const [syncState, setSyncState] = useState<SyncState>('idle');
  const [sidePanel, setSidePanel] = useState<'nodes' | 'assets' | 'prompts'>('nodes');
  const [showRuns, setShowRuns] = useState(false);
  const [runTick, setRunTick] = useState(0);

  // 一画布一内核实例；切换画布时必须重建，否则会串数据
  useEffect(() => {
    if (!docQuery.data) return;
    setKernel(new CanvasKernel(docQuery.data));
  }, [docQuery.data]);

  const sync = useCanvasSync({ canvasId, kernel, t, onState: setSyncState });

  // SSE：单连接多路复用（见 docs/design/03 §2.6）
  useEffect(() => {
    if (!kernel) return;
    const conn = connectCanvasEvents({
      canvasId,
      onEvent: (ev) => {
        if (!ev.op) return;
        // 本端提交的回声按 actor 忽略（服务端会带上 actorId）
        sync.applyRemoteOp(ev.actor, ev.op as never);
        if (ev.type === 'run.step' || ev.type === 'run.step.delta') setRunTick((n) => n + 1);
      },
      onReconnect: () => setSyncState('idle'),
      onError: () => setSyncState('offline'),
    });
    return () => conn.disconnect();
  }, [kernel, canvasId, sync]);

  // 快捷键：Ctrl+Z / Ctrl+Shift+Z / Ctrl+Y / Ctrl+A / Delete / Esc
  useEffect(() => {
    if (!kernel) return;
    const onKey = (e: KeyboardEvent) => {
      const action = kernel.handleShortcut({
        key: e.key,
        ctrlKey: e.ctrlKey,
        metaKey: e.metaKey,
        shiftKey: e.shiftKey,
        altKey: e.altKey,
        target: e.target as HTMLElement,
      });
      if (!action) return;
      e.preventDefault();
      switch (action) {
        case 'undo':
          if (kernel.undoOnce()) sync.flush();
          break;
        case 'redo':
          if (kernel.redoOnce()) sync.flush();
          break;
        case 'select-all':
          kernel.setSelection({ nodes: kernel.scene.allNodes().map((n) => n.id), edges: [] });
          break;
        case 'delete':
          if (kernel.currentSelection.nodes.length) {
            kernel.dispatch({ type: 'delete-nodes', ids: kernel.currentSelection.nodes });
            sync.flush();
          }
          break;
        case 'escape':
          kernel.setSelection({ nodes: [], edges: [] });
          break;
        default:
          break;
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [kernel, sync]);

  // 冲突 / 错误提示
  useMemo(() => {
    if (sync.conflictDoc) setSyncState('conflict');
  }, [sync.conflictDoc]);

  if (docQuery.isLoading) return <div className="ic-empty">{t('common.loading')}</div>;
  if (docQuery.error) {
    const err = docQuery.error as ApiFailure;
    return <div className="ic-empty">{t(`errors.${err.code}`)}</div>;
  }
  if (!kernel) return <div className="ic-empty">{t('common.loading')}</div>;

  return (
    <div style={{ position: 'absolute', inset: 0, display: 'flex', flexDirection: 'column' }}>
      <CanvasTopBar
        t={t}
        kernel={kernel}
        canvasId={canvasId}
        syncState={syncState}
        onToggleRuns={() => setShowRuns((v) => !v)}
        onSaveViewport={() => sync.flushViewport()}
      />
      <div style={{ flex: 1, minHeight: 0, display: 'flex' }}>
        <SidePanel
          t={t}
          locale={locale}
          kernel={kernel}
          tab={sidePanel}
          onTabChange={setSidePanel}
          onLocate={(nodeId) => {
            const n = kernel.scene.getNode(nodeId);
            if (!n) return;
            const host = document.querySelector('[data-canvas-host]') as HTMLElement | null;
            const rect = host?.getBoundingClientRect();
            kernel.viewport.focusRect(n.rect, {
              x: rect?.width ?? window.innerWidth,
              y: rect?.height ?? window.innerHeight,
            });
          }}
        />
        <div style={{ flex: 1, minWidth: 0, position: 'relative' }}>
          <CanvasSurface
            t={t}
            kernel={kernel}
            onCommit={() => sync.flush()}
            onRun={async (nodeIds) => {
              try {
                await api.createRun(canvasId, nodeIds, newIdempotencyKey());
                setShowRuns(true);
                setRunTick((n) => n + 1);
              } catch (e) {
                if (isRetryable(e)) setSyncState('error');
              }
            }}
          />
          <CanvasToolbar
            t={t}
            kernel={kernel}
            onCommit={() => sync.flush()}
            onCreateNode={(type) => {
              const host = document.querySelector('[data-canvas-host]') as HTMLElement | null;
              const rect = host?.getBoundingClientRect();
              const center = kernel.viewport.toWorld({
                x: (rect?.width ?? 800) / 2,
                y: (rect?.height ?? 600) / 2,
              });
              const d = kernel.createNode(type, center);
              if (d) sync.flush();
            }}
          />
          {showRuns && (
            <RunPanel
              t={t}
              canvasId={canvasId}
              tick={runTick}
              onClose={() => setShowRuns(false)}
              onUseResult={(nodeId, assetId) => {
                kernel.dispatch({ type: 'set-spec', id: nodeId, patch: { assetId } });
                sync.flush();
              }}
            />
          )}
        </div>
      </div>
    </div>
  );
}
