import { useEffect, useMemo, useRef, useState } from 'react';
import { api } from '@/shared/api';
import type { CanvasKernel } from '../kernel';
import type { RawNode } from '../kernel/types';
import { useWorkspace } from '@/features/settings/useWorkspace';
import {
  buildAnglePrompt, cropRect, frameTime, reversePromptPlan, splitGrid, upscaleTarget, type AngleSpec,
} from './pure';
import type { TFn } from '@/app/App';

interface Props {
  t: TFn;
  tool: string;
  kernel: CanvasKernel;
  node: RawNode;
  onClose: () => void;
  onCommit: () => void;
}

/**
 * 图像工具对话框集合。
 * 纯几何/文案计算全部委托给 tools/pure.ts（已单测），这里只做交互与提交。
 *
 * 已知回归点：蒙版编辑在**缩放期间**打开会崩（原项目 v0.16.0 修复过）。
 * 这里用 `isNodeResizing` 门控——由内核的交互状态机提供，而不是靠 setTimeout。
 */
export function ImageToolDialog({ t, tool, kernel, node, onClose, onCommit }: Props) {
  const { workspaceId } = useWorkspace();
  const assetId = String(node.spec.assetId ?? '') || (node.result?.variants?.[node.result?.primary ?? 0]?.assetId ?? '');
  const resizing = kernel.interaction.current === 'resizing-node';

  const natural = useMemo(
    () => ({ w: Number(node.spec.naturalW ?? 0) || 1024, h: Number(node.spec.naturalH ?? 0) || 1024 }),
    [node.spec.naturalW, node.spec.naturalH],
  );

  if (resizing) {
    return <Modal title={t('common.loading')} onClose={onClose}><p className="ic-dim">请先结束缩放操作</p></Modal>;
  }

  const common = { t, kernel, node, assetId, workspaceId, natural, onClose, onCommit };

  switch (tool) {
    case 'info':
      return <InfoDialog {...common} />;
    case 'crop':
      return <CropDialog {...common} />;
    case 'split':
      return <SplitDialog {...common} />;
    case 'upscale':
      return <UpscaleDialog {...common} />;
    case 'multiAngle':
      return <AngleDialog {...common} />;
    case 'reversePrompt':
      return <ReversePromptDialog {...common} />;
    case 'videoFrame':
      return <VideoFrameDialog {...common} />;
    case 'superRes':
      return (
        <Modal title={t('canvas.tool.superRes')} onClose={onClose}>
          <p className="ic-dim">AI 超分需要服务端上采样模型，当前版本尚未接入。</p>
        </Modal>
      );
    case 'mask':
      return <MaskDialog {...common} />;
    case 'view':
      return (
        <Modal title={t('canvas.tool.viewImage')} onClose={onClose} wide>
          {assetId ? <img src={api.assetRawUrl(assetId, workspaceId)} alt="" style={{ maxWidth: '100%' }} /> : null}
        </Modal>
      );
    default:
      return null;
  }
}

interface CommonProps {
  t: TFn;
  kernel: CanvasKernel;
  node: RawNode;
  assetId: string;
  workspaceId: string;
  natural: { w: number; h: number };
  onClose: () => void;
  onCommit: () => void;
}

function Modal({
  title, onClose, children, wide,
}: { title: string; onClose: () => void; children: React.ReactNode; wide?: boolean }) {
  return (
    <div
      style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,.35)', display: 'grid', placeItems: 'center', zIndex: 1000 }}
      onPointerDown={(e) => e.stopPropagation()}
      onClick={onClose}
    >
      <div className="ic-card" style={{ width: wide ? '80vw' : 520, maxWidth: '92vw', maxHeight: '88vh', overflow: 'auto', padding: 16 }} onClick={(e) => e.stopPropagation()}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 12 }}>
          <strong>{title}</strong>
          <button className="ic-btn ic-btn--ghost" onClick={onClose}>✕</button>
        </div>
        {children}
      </div>
    </div>
  );
}

function InfoDialog({ t, node, onClose, assetId, workspaceId }: CommonProps) {
  const [showJson, setShowJson] = useState(false);
  return (
    <Modal title={t('canvas.tool.info')} onClose={onClose}>
      <dl style={{ display: 'grid', gridTemplateColumns: 'auto 1fr', gap: '6px 12px', fontSize: 13, margin: 0 }}>
        <dt className="ic-dim">ID</dt><dd className="ic-mono" style={{ margin: 0 }}>{node.id}</dd>
        <dt className="ic-dim">Type</dt><dd style={{ margin: 0 }}>{node.type}</dd>
        <dt className="ic-dim">State</dt><dd style={{ margin: 0 }}>{node.state}</dd>
        {assetId && <><dt className="ic-dim">Asset</dt><dd className="ic-mono" style={{ margin: 0 }}>{assetId}</dd></>}
        {Boolean(node.spec.naturalW) && (
          <><dt className="ic-dim">Size</dt><dd style={{ margin: 0 }}>{String(node.spec.naturalW)}×{String(node.spec.naturalH)}</dd></>
        )}
      </dl>
      {assetId && (
        <div style={{ marginTop: 12 }}>
          <button className="ic-btn" style={{ padding: '2px 8px', fontSize: 12 }} onClick={() => setShowJson((v) => !v)}>
            JSON
          </button>
          {showJson && (
            <pre className="ic-mono" style={{ background: 'var(--ic-surface-2)', padding: 10, borderRadius: 8, maxHeight: 240, overflow: 'auto' }}>
              {JSON.stringify(
                {
                  ...node,
                  // base64 折叠：不把整段 data URI 打到界面上（避免撑破 UI 与泄露内容）
                  spec: collapseDataURIs(node.spec),
                },
                null,
                2,
              )}
            </pre>
          )}
        </div>
      )}
      <img src={api.assetThumbUrl(assetId, workspaceId, 480)} alt="" style={{ maxWidth: '100%', marginTop: 12, borderRadius: 8 }} />
    </Modal>
  );
}

function collapseDataURIs(spec: Record<string, unknown>): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(spec)) {
    out[k] = typeof v === 'string' && v.startsWith('data:') ? `<data-uri ${v.length} bytes>` : v;
  }
  return out;
}

function CropDialog({ t, kernel, node, assetId, workspaceId, natural, onClose, onCommit }: CommonProps) {
  const [ratio, setRatio] = useState<'free' | 'original' | '1:1' | '16:9' | '9:16'>('free');
  const [rect, setRect] = useState({ x: 0, y: 0, w: natural.w, h: natural.h });

  const apply = () => {
    const r = cropRect(rect, natural);
    // 生成新节点并连线（原项目语义：裁剪结果为新图片节点）
    const child = kernel.createNode('image', { x: node.rect.x + node.rect.w + 60, y: node.rect.y });
    if (child) {
      kernel.dispatch({
        type: 'set-spec',
        id: child.id,
        patch: {
          crop: r,
          sourceAssetId: assetId,
          freeResize: ratio === 'free',
        },
      });
      kernel.createEdge(node.id, 'out', child.id, 'in');
      onCommit();
    }
    onClose();
  };

  return (
    <Modal title={t('canvas.tool.crop')} onClose={onClose}>
      <div style={{ marginBottom: 10 }}>
        {(['free', 'original', '1:1', '16:9', '9:16'] as const).map((r) => (
          <button
            key={r}
            className={`ic-btn ${ratio === r ? 'ic-btn--primary' : ''}`}
            style={{ marginRight: 6, padding: '2px 8px', fontSize: 12 }}
            onClick={() => {
              setRatio(r);
              if (r === 'original') setRect({ x: 0, y: 0, w: natural.w, h: natural.h });
              if (r === '1:1') setRect(square(natural, 1));
              if (r === '16:9') setRect(square(natural, 16 / 9));
              if (r === '9:16') setRect(square(natural, 9 / 16));
            }}
          >
            {r}
          </button>
        ))}
      </div>
      <img src={api.assetThumbUrl(assetId, workspaceId, 480)} alt="" style={{ width: '100%', borderRadius: 8, marginBottom: 10 }} />
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8 }}>
        {(['x', 'y', 'w', 'h'] as const).map((k) => (
          <label key={k}>
            <span className="ic-dim">{k}</span>
            <input className="ic-input" type="number" value={rect[k]}
              onChange={(e) => setRect({ ...rect, [k]: Number(e.target.value) })} />
          </label>
        ))}
      </div>
      <button className="ic-btn ic-btn--primary" style={{ marginTop: 12 }} onClick={apply}>
        {t('common.confirm')}
      </button>
    </Modal>
  );
}

function square(natural: { w: number; h: number }, ratio: number) {
  const w = Math.min(natural.w, Math.round(natural.h * ratio));
  const h = Math.round(w / ratio);
  return { x: Math.round((natural.w - w) / 2), y: Math.round((natural.h - h) / 2), w, h };
}

function SplitDialog({ t, kernel, node, onClose, onCommit }: CommonProps) {
  const [rows, setRows] = useState(2);
  const [cols, setCols] = useState(2);
  const [error, setError] = useState<string | null>(null);

  const apply = () => {
    try {
      const cells = splitGrid({ rows, cols }, { w: Number(node.spec.naturalW) || 1024, h: Number(node.spec.naturalH) || 1024 });
      // 按原网格排列到右侧
      const cellW = node.rect.w / cols;
      const cellH = node.rect.h / rows;
      cells.forEach((c, i) => {
        const r = Math.floor(i / cols);
        const col = i % cols;
        const child = kernel.createNode('image', {
          x: node.rect.x + node.rect.w + 60 + col * (cellW + 12),
          y: node.rect.y + r * (cellH + 12),
        });
        if (child) {
          kernel.dispatch({ type: 'set-spec', id: child.id, patch: { crop: c, sourceAssetId: node.spec.assetId } });
          kernel.createEdge(node.id, 'out', child.id, 'in');
        }
      });
      onCommit();
      onClose();
    } catch (e) {
      setError((e as Error).message);
    }
  };

  return (
    <Modal title={t('canvas.tool.split')} onClose={onClose}>
      <div style={{ display: 'flex', gap: 12 }}>
        <label style={{ flex: 1 }}>
          <span className="ic-dim">rows (1–50)</span>
          <input className="ic-input" type="number" min={1} max={50} value={rows} onChange={(e) => setRows(Number(e.target.value))} />
        </label>
        <label style={{ flex: 1 }}>
          <span className="ic-dim">cols (1–50)</span>
          <input className="ic-input" type="number" min={1} max={50} value={cols} onChange={(e) => setCols(Number(e.target.value))} />
        </label>
      </div>
      <p className="ic-dim" style={{ fontSize: 12 }}>
        将生成 {rows * cols} 个节点（上限 200）
      </p>
      {error && <p className="ic-error">{error}</p>}
      <button className="ic-btn ic-btn--primary" onClick={apply}>{t('common.confirm')}</button>
    </Modal>
  );
}

function UpscaleDialog({ t, kernel, node, onClose, onCommit }: CommonProps) {
  const natural = { w: Number(node.spec.naturalW) || 1024, h: Number(node.spec.naturalH) || 1024 };
  const [edge, setEdge] = useState(Math.min(2048, Math.max(natural.w, natural.h) * 2));
  const [algorithm, setAlgorithm] = useState<'nearest' | 'bilinear' | 'highQuality'>('highQuality');
  const target = upscaleTarget(natural, edge);

  const apply = () => {
    if (!target) return;
    const child = kernel.createNode('image', { x: node.rect.x + node.rect.w + 60, y: node.rect.y });
    if (child) {
      kernel.dispatch({
        type: 'set-spec',
        id: child.id,
        patch: { upscale: { ...target, algorithm }, sourceAssetId: node.spec.assetId, freeResize: true },
      });
      kernel.createEdge(node.id, 'out', child.id, 'in');
      onCommit();
    }
    onClose();
  };

  return (
    <Modal title={t('canvas.tool.upscale')} onClose={onClose}>
      <label>
        <span className="ic-dim">目标最长边（≤4096）</span>
        <input className="ic-input" type="number" min={1} max={4096} value={edge} onChange={(e) => setEdge(Number(e.target.value))} />
      </label>
      <label style={{ display: 'block', marginTop: 8 }}>
        <span className="ic-dim">算法</span>
        <select className="ic-select" value={algorithm} onChange={(e) => setAlgorithm(e.target.value as typeof algorithm)}>
          <option value="nearest">最近邻（像素风）</option>
          <option value="bilinear">双线性</option>
          <option value="highQuality">高清插值</option>
        </select>
      </label>
      <p className="ic-dim" style={{ fontSize: 12 }}>
        {target ? `${natural.w}×${natural.h} → ${target.w}×${target.h}` : '已达上限：目标边长不大于原图'}
        {target?.capped && '（已触及 4096 上限）'}
      </p>
      <button className="ic-btn ic-btn--primary" disabled={!target} onClick={apply}>{t('common.confirm')}</button>
    </Modal>
  );
}

function AngleDialog({ t, kernel, node, onClose, onCommit }: CommonProps) {
  const [spec, setSpec] = useState<AngleSpec>({ horizontal: 0, pitch: 0, distance: 1, wideAngle: false });
  const prompt = buildAnglePrompt(spec);

  const apply = () => {
    // 生成「文本节点 + 生成节点」并按原项目语义连线
    const text = kernel.createNode('prompt', { x: node.rect.x + node.rect.w + 60, y: node.rect.y });
    const gen = kernel.createNode('generation', { x: node.rect.x + node.rect.w + 60, y: node.rect.y + 260 });
    if (text && gen) {
      kernel.dispatch({ type: 'set-spec', id: text.id, patch: { text: prompt } });
      kernel.dispatch({ type: 'set-spec', id: gen.id, patch: { capability: 'image.edit', params: { angle: spec } } });
      kernel.createEdge(text.id, 'out', gen.id, 'prompt');
      kernel.createEdge(node.id, 'out', gen.id, 'ref');
      onCommit();
    }
    onClose();
  };

  return (
    <Modal title={t('canvas.tool.multiAngle')} onClose={onClose}>
      {(
        [
          ['horizontal', -90, 90, '水平角度'],
          ['pitch', -90, 90, '俯仰角度'],
          ['distance', 0, 2, '镜头距离'],
        ] as const
      ).map(([key, min, max, label]) => (
        <label key={key} style={{ display: 'block', marginBottom: 8 }}>
          <span className="ic-dim">{label}: {spec[key]}</span>
          <input
            className="ic-input"
            type="range"
            min={min}
            max={max}
            step={key === 'distance' ? 0.1 : 1}
            value={spec[key]}
            onChange={(e) => setSpec({ ...spec, [key]: Number(e.target.value) })}
          />
        </label>
      ))}
      <label style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
        <input type="checkbox" checked={spec.wideAngle} onChange={(e) => setSpec({ ...spec, wideAngle: e.target.checked })} />
        <span>广角</span>
      </label>
      <pre className="ic-mono" style={{ background: 'var(--ic-surface-2)', padding: 10, borderRadius: 8, whiteSpace: 'pre-wrap' }}>
        {prompt}
      </pre>
      <button className="ic-btn ic-btn--primary" onClick={apply}>{t('common.confirm')}</button>
    </Modal>
  );
}

function ReversePromptDialog({ t, kernel, node, onClose, onCommit }: CommonProps) {
  const plan = reversePromptPlan(node.rect);
  const apply = () => {
    const text = kernel.createNode('prompt', { x: plan.textNode.rect.x, y: plan.textNode.rect.y });
    const gen = kernel.createNode('generation', { x: plan.configNode.rect.x, y: plan.configNode.rect.y });
    if (text && gen) {
      kernel.dispatch({ type: 'set-spec', id: text.id, patch: { text: plan.textNode.spec.text } });
      kernel.dispatch({ type: 'set-spec', id: gen.id, patch: { capability: 'text.generate' } });
      kernel.createEdge(text.id, 'out', gen.id, 'prompt');
      kernel.createEdge(node.id, 'out', gen.id, 'ref');
      onCommit();
    }
    onClose();
  };
  return (
    <Modal title={t('canvas.tool.reversePrompt')} onClose={onClose}>
      <p className="ic-dim" style={{ fontSize: 13 }}>将创建「文本」与「生成」两个节点并自动连线。</p>
      <button className="ic-btn ic-btn--primary" onClick={apply}>{t('common.confirm')}</button>
    </Modal>
  );
}

function VideoFrameDialog({ t, kernel, node, onClose, onCommit }: CommonProps) {
  const durationMs = Number(node.spec.durationMs ?? 0);
  const [kind, setKind] = useState<'first' | 'last' | 'current'>('first');
  const time = frameTime(kind, durationMs / 1000, 0);

  const apply = () => {
    const child = kernel.createNode('image', { x: node.rect.x + node.rect.w + 60, y: node.rect.y });
    if (child) {
      kernel.dispatch({ type: 'set-spec', id: child.id, patch: { frameAt: time, sourceAssetId: node.spec.assetId } });
      kernel.createEdge(node.id, 'out', child.id, 'in');
      onCommit();
    }
    onClose();
  };

  return (
    <Modal title={t('canvas.tool.videoFrame')} onClose={onClose}>
      {(['first', 'last', 'current'] as const).map((k) => (
        <label key={k} style={{ marginRight: 12 }}>
          <input type="radio" checked={kind === k} onChange={() => setKind(k)} /> {k}
        </label>
      ))}
      <p className="ic-dim" style={{ fontSize: 12 }}>截帧时间：{time.toFixed(2)}s</p>
      <button className="ic-btn ic-btn--primary" onClick={apply}>{t('common.confirm')}</button>
    </Modal>
  );
}

function MaskDialog({ t, kernel, node, assetId, workspaceId, natural, onClose, onCommit }: CommonProps) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const [brush, setBrush] = useState(40);
  const [history, setHistory] = useState<ImageData[]>([]);
  const drawing = useRef(false);
  const last = useRef<{ x: number; y: number } | null>(null);

  useEffect(() => {
    const c = canvasRef.current;
    if (!c) return;
    c.width = 512;
    c.height = Math.round((512 * natural.h) / natural.w);
    const ctx = c.getContext('2d');
    if (!ctx) return;
    ctx.clearRect(0, 0, c.width, c.height);
    const img = new Image();
    img.crossOrigin = 'anonymous';
    img.src = api.assetThumbUrl(assetId, workspaceId, 512);
    img.onload = () => {
      ctx.globalAlpha = 0.9;
      ctx.drawImage(img, 0, 0, c.width, c.height);
      ctx.globalAlpha = 1;
    };
  }, [assetId, workspaceId, natural.w, natural.h]);

  const toCanvas = (e: React.PointerEvent) => {
    const c = canvasRef.current!;
    const r = c.getBoundingClientRect();
    return { x: ((e.clientX - r.left) / r.width) * c.width, y: ((e.clientY - r.top) / r.height) * c.height };
  };

  const stroke = (from: { x: number; y: number }, to: { x: number; y: number }, erase: boolean) => {
    const ctx = canvasRef.current?.getContext('2d');
    if (!ctx) return;
    ctx.globalCompositeOperation = erase ? 'destination-out' : 'source-over';
    ctx.strokeStyle = 'rgba(79,70,229,.55)';
    ctx.lineWidth = brush;
    ctx.lineCap = 'round';
    ctx.beginPath();
    ctx.moveTo(from.x, from.y);
    ctx.lineTo(to.x, to.y);
    ctx.stroke();
    ctx.globalCompositeOperation = 'source-over';
  };

  const snapshot = () => {
    const ctx = canvasRef.current?.getContext('2d');
    if (!ctx || !canvasRef.current) return;
    setHistory((h) => [...h.slice(-19), ctx.getImageData(0, 0, canvasRef.current!.width, canvasRef.current!.height)]);
  };

  const undo = () => {
    const prev = history[history.length - 1];
    const ctx = canvasRef.current?.getContext('2d');
    if (!prev || !ctx) return;
    ctx.putImageData(prev, 0, 0);
    setHistory((h) => h.slice(0, -1));
  };

  const exportMaskOnly = () => {
    const c = canvasRef.current;
    if (!c) return;
    const link = document.createElement('a');
    link.download = `${node.title || 'mask'}.png`;
    link.href = c.toDataURL('image/png');
    link.click();
  };

  const generate = () => {
    // 生成编辑节点：以「原图 + 蒙版标注图」两张参考图走 image.edit
    const child = kernel.createNode('generation', { x: node.rect.x + node.rect.w + 60, y: node.rect.y });
    if (child) {
      kernel.dispatch({
        type: 'set-spec',
        id: child.id,
        patch: { capability: 'image.edit', params: { masked: true}, outputCount: 1 },
      });
      kernel.createEdge(node.id, 'out', child.id, 'ref');
      onCommit();
    }
    onClose();
  };

  return (
    <Modal title={t('canvas.tool.mask')} onClose={onClose} wide>
      <div style={{ display: 'flex', gap: 8, marginBottom: 8, alignItems: 'center', flexWrap: 'wrap' }}>
        <label>
          <span className="ic-dim">笔刷 {brush}</span>
          <input type="range" min={4} max={120} value={brush} onChange={(e) => setBrush(Number(e.target.value))} />
        </label>
        <button className="ic-btn" onClick={undo} disabled={history.length === 0}>撤销</button>
        <button className="ic-btn" onClick={exportMaskOnly}>仅导出</button>
        <button className="ic-btn ic-btn--primary" onClick={generate}>立刻生成</button>
      </div>
      <canvas
        ref={canvasRef}
        style={{ width: '100%', borderRadius: 8, cursor: 'crosshair', touchAction: 'none' }}
        onPointerDown={(e) => {
          snapshot();
          drawing.current = true;
          last.current = toCanvas(e);
          (e.target as HTMLElement).setPointerCapture(e.pointerId);
        }}
        onPointerMove={(e) => {
          if (!drawing.current) return;
          const p = toCanvas(e);
          if (last.current) stroke(last.current, p, e.shiftKey);
          last.current = p;
        }}
        onPointerUp={() => {
          drawing.current = false;
          last.current = null;
        }}
      />
      <p className="ic-dim" style={{ fontSize: 12 }}>按住 Shift 为擦除；遮罩区域会被标记为需要重绘。</p>
    </Modal>
  );
}
