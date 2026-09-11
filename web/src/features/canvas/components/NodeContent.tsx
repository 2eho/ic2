import { useState } from 'react';
import { api } from '@/shared/api';
import type { CanvasKernel } from '../kernel';
import type { RawNode } from '../kernel/types';
import { useWorkspace } from '@/features/settings/useWorkspace';
import { GenerationPanel } from './GenerationPanel';
import type { TFn } from '@/app/App';

interface Props {
  t: TFn;
  kernel: CanvasKernel;
  node: RawNode;
  onCommit: () => void;
  onRun: (nodeIds: string[]) => void;
}

/** 按节点类型分发内容（原 canvas-node.tsx 960 行的替代：每种类型一个小组件）。 */
export function NodeContent({ t, kernel, node, onCommit, onRun }: Props) {
  switch (node.type) {
    case 'prompt':
      return <PromptContent kernel={kernel} node={node} onCommit={onCommit} />;
    case 'image':
      return <ImageContent kernel={kernel} node={node} />;
    case 'video':
      return <VideoContent node={node} />;
    case 'audio':
      return <AudioContent node={node} />;
    case 'group':
      return <GroupContent kernel={kernel} node={node} />;
    case 'generation':
      return <GenerationPanel t={t} kernel={kernel} node={node} onCommit={onCommit} onRun={onRun} />;
    case 'run':
      return <RunAnchorContent t={t} node={node} />;
    default:
      return <PluginContent t={t} node={node} />;
  }
}

function PromptContent({ kernel, node, onCommit }: { kernel: CanvasKernel; node: RawNode; onCommit: () => void }) {
  const text = String(node.spec.text ?? '');
  const [draft, setDraft] = useState(text);
  const fontSize = Number(node.spec.fontSize ?? 13);
  return (
    <textarea
      value={draft}
      onChange={(e) => setDraft(e.target.value)}
      onBlur={() => {
        if (draft !== text) {
          kernel.dispatch({ type: 'set-spec', id: node.id, patch: { text: draft } });
          onCommit();
        }
      }}
      style={{
        width: '100%',
        height: '100%',
        border: 'none',
        outline: 'none',
        resize: 'none',
        padding: 8,
        background: 'transparent',
        fontSize,
        lineHeight: 1.5,
        color: 'var(--ic-text)',
      }}
      placeholder="输入提示词…"
    />
  );
}

function ImageContent({ kernel, node }: { kernel: CanvasKernel; node: RawNode }) {
  const { workspaceId } = useWorkspace();
  const assetId = String(node.spec.assetId ?? '');
  const variants = node.result?.variants ?? [];
  const current = variants[node.result?.primary ?? 0];
  const src = assetId
    ? api.assetThumbUrl(assetId, workspaceId, 640)
    : current?.assetId
      ? api.assetThumbUrl(current.assetId, workspaceId, 640)
      : '';
  if (!src) {
    return <div className="ic-empty" style={{ padding: 12, fontSize: 12 }}>空</div>;
  }
  const showInfo = kernel.settings.imageInfo;
  return (
    <div style={{ width: '100%', height: '100%', position: 'relative' }}>
      <img
        src={src}
        alt={node.title}
        decoding="async"
        loading="lazy"
        style={{ width: '100%', height: '100%', objectFit: (node.spec.fit === 'contain' ? 'contain' : node.spec.fit === 'fill' ? 'fill' : 'cover') }}
      />
      {showInfo && (
        <span className="ic-badge" style={{ position: 'absolute', right: 6, bottom: 6, fontSize: 10 }}>
          {String(node.spec.naturalW ?? '')}×{String(node.spec.naturalH ?? '')}
        </span>
      )}
    </div>
  );
}

function VideoContent({ node }: { node: RawNode }) {
  const { workspaceId } = useWorkspace();
  const assetId = String(node.spec.assetId ?? '');
  if (!assetId) return <div className="ic-empty" style={{ padding: 12, fontSize: 12 }}>空</div>;
  return (
    <video
      src={api.assetRawUrl(assetId, workspaceId)}
      controls={node.spec.controls !== false}
      loop={node.spec.loop !== false}
      preload="metadata"
      style={{ width: '100%', height: '100%', objectFit: 'contain', background: '#000' }}
    />
  );
}

function AudioContent({ node }: { node: RawNode }) {
  const { workspaceId } = useWorkspace();
  const assetId = String(node.spec.assetId ?? '');
  if (!assetId) return <div className="ic-empty" style={{ padding: 12, fontSize: 12 }}>空</div>;
  return (
    <div style={{ display: 'grid', placeItems: 'center', height: '100%', padding: 10 }}>
      <audio src={api.assetRawUrl(assetId, workspaceId)} controls style={{ width: '100%' }} />
    </div>
  );
}

function GroupContent({ kernel, node }: { kernel: CanvasKernel; node: RawNode }) {
  const children = kernel.scene.childrenOf(node.id).length;
  return (
    <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'flex-end', padding: 4 }}>
      <span className="ic-badge" style={{ fontSize: 10 }}>{children}</span>
    </div>
  );
}

function RunAnchorContent({ t, node }: { t: TFn; node: RawNode }) {
  return (
    <div style={{ padding: 8, fontSize: 12 }} className="ic-dim">
      {String(node.spec.runId ?? t('canvas.run.empty'))}
    </div>
  );
}

/** 插件节点：渲染在 iframe 沙箱中（M5），未安装时给出明确提示而不是空白。 */
function PluginContent({ t, node }: { t: TFn; node: RawNode }) {
  const pluginKey = node.type.split(':')[0];
  return (
    <div className="ic-empty" style={{ padding: 12, fontSize: 12 }}>
      {t('plugins.missing', { name: pluginKey })}
    </div>
  );
}
