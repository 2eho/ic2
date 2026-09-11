import { useEffect, useMemo, useRef } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '@/shared/api';
import { useWorkspace } from '@/features/settings/useWorkspace';
import { createPluginHost, type HostHandlers } from './sandbox/host';
import type { PluginNodeSnapshot } from './sandbox/protocol';
import type { CanvasKernel } from '@/features/canvas/kernel';
import type { RawNode } from '@/features/canvas/kernel/types';

interface Props {
  kernel: CanvasKernel;
  node: RawNode;
  onLog?: (level: 'info' | 'warn' | 'error', message: string) => void;
}

/**
 * 插件节点渲染器。
 *
 * 降级策略（对齐 docs/design/06 §7 与 docs/design/10 §10.11）：
 * - 插件未安装/未启用 → 展示「需要插件 X」+ 安装入口，而不是空白节点；
 * - 插件崩溃 → 仅停用该节点，主页面不受影响。
 */
export function PluginNodeView({ kernel, node, onLog }: Props) {
  const { workspaceId, ready } = useWorkspace();
  const hostRef = useRef<HTMLDivElement>(null);
  const [pluginKey] = node.type.split(':');
  const pluginQuery = useQuery({
    queryKey: ['plugins', workspaceId],
    queryFn: () => api.listPlugins(workspaceId),
    enabled: ready,
  });

  const plugin = pluginQuery.data?.items.find((p) => p.key === pluginKey);
  const nodeDef = plugin?.nodes.find((n) => n.type === node.type);
  const assetId = String(node.spec.assetId ?? '');

  const snapshot: PluginNodeSnapshot = useMemo(
    () => ({
      id: node.id,
      type: node.type,
      title: node.title,
      rect: node.rect,
      config: node.spec,
      schemaVersion: nodeDef?.configVersion ?? 1,
    }),
    [node.id, node.type, node.title, node.rect, node.spec, nodeDef?.configVersion],
  );

  useEffect(() => {
    if (!plugin?.enabled || !hostRef.current) return;
    const host = createPluginHost({
      pluginKey: plugin.key,
      version: plugin.version,
      permissions: plugin.permissions,
      allowedHosts: [],
      // bundle 由服务端分发；这里用最小壳，真实渲染逻辑由 bundle 提供
      bundle: '',
      node: snapshot,
      theme: document.documentElement.dataset.theme === 'dark' ? 'dark' : 'light',
      handlers: buildHandlers(kernel, node, hostRef.current),
      onSizeChange: (w, h) => {
        kernel.dispatch({ type: 'resize-node', id: node.id, rect: { ...node.rect, w, h }, keepAspect: false });
      },
      onLog,
    });
    const el = host.render();
    hostRef.current.replaceChildren(el);
    return () => host.dispose();
  }, [plugin?.enabled, plugin?.key, plugin?.version, kernel, node.id, snapshot, onLog, plugin?.permissions]);

  if (!ready || pluginQuery.isLoading) {
    return <div className="ic-empty" style={{ padding: 12, fontSize: 12 }}>…</div>;
  }

  // 缺插件：明确提示 + 安装入口（不是空白）
  if (!plugin || !plugin.enabled) {
    return (
      <div className="ic-empty" style={{ padding: 12, fontSize: 12 }}>
        <div>需要插件 {pluginKey}</div>
        <button
          className="ic-btn"
          style={{ marginTop: 8, fontSize: 12 }}
          onClick={() => {
            void api.enablePlugin(workspaceId, pluginKey, true).then(() => pluginQuery.refetch());
          }}
        >
          启用
        </button>
      </div>
    );
  }

  return (
    <div ref={hostRef} style={{ width: '100%', height: '100%' }} data-plugin-key={pluginKey} data-asset={assetId} />
  );
}

/** 把宿主能力收敛到最小集合，并统一记审计。 */
function buildHandlers(kernel: CanvasKernel, node: RawNode, host: HTMLElement): HostHandlers {
  return {
    nodeGet: () => ({
      id: node.id,
      type: node.type,
      title: node.title,
      rect: node.rect,
      config: node.spec,
      schemaVersion: Number(node.spec.schemaVersion ?? 1),
    }),
    nodePatch: (config) => {
      kernel.dispatch({ type: 'set-spec', id: node.id, patch: config });
    },
    nodeResize: (w, h) => {
      kernel.dispatch({ type: 'resize-node', id: node.id, rect: { ...node.rect, w, h }, keepAspect: false });
    },
    nodeEmit: () => {
      // 插件节点输出通过 spec 承载，具体连线由用户操作
    },
    graphUpstream: () => kernel.scene.upstreamOf(node.id),
    graphDownstream: () => kernel.scene.downstreamOf(node.id),
    toast: (message) => {
      // 用 aria-live 区域而不是 alert，避免阻塞主线程
      const el = document.createElement('div');
      el.setAttribute('role', 'status');
      el.className = 'ic-badge';
      el.textContent = message;
      el.style.position = 'fixed';
      el.style.bottom = '80px';
      el.style.left = '50%';
      el.style.transform = 'translateX(-50%)';
      document.body.appendChild(el);
      setTimeout(() => el.remove(), 2000);
    },
    audit: (entry) => {
      host.dataset.lastAudit = `${entry.method}:${entry.allowed ? 'ok' : 'denied'}`;
    },
  };
}
