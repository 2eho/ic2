import { useEffect, useState } from 'react';
import type { CanvasKernel } from '../kernel';

/** 小地图：开关常驻，点击/拖拽跳转视口（对齐 docs/design/10 §3.14）。 */
export function Minimap({ kernel, size }: { kernel: CanvasKernel; size: { x: number; y: number } }) {
  const [, setTick] = useState(0);
  const [collapsed, setCollapsed] = useState(false);
  useEffect(() => kernel.subscribe(() => setTick((n) => n + 1)), [kernel]);

  const W = 180;
  const H = 120;
  const bounds = kernel.scene.bounds();
  if (!bounds) return null;
  const pad = 100;
  const scale = Math.min(W / (bounds.w + pad * 2), H / (bounds.h + pad * 2));
  const toMap = (x: number, y: number) => ({
    x: (x - bounds.x + pad) * scale,
    y: (y - bounds.y + pad) * scale,
  });
  const view = kernel.viewport.visibleWorldRect(size);

  return (
    <div
      style={{
        position: 'absolute',
        right: 12,
        bottom: 12,
        width: W,
        height: collapsed ? 24 : H,
        background: 'var(--ic-surface)',
        border: '1px solid var(--ic-border)',
        borderRadius: 8,
        overflow: 'hidden',
        boxShadow: 'var(--ic-shadow)',
      }}
    >
      <button
        className="ic-btn ic-btn--ghost"
        style={{ width: '100%', height: 22, fontSize: 11, borderRadius: 0 }}
        onClick={() => setCollapsed((v) => !v)}
      >
        {collapsed ? '▲' : '▼'}
      </button>
      {!collapsed && (
        <div
          style={{ position: 'relative', width: W, height: H - 22, cursor: 'grab' }}
          onPointerDown={(e) => {
            const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
            const mx = e.clientX - r.left;
            const my = e.clientY - r.top;
            const world = { x: bounds.x - pad + mx / scale, y: bounds.y - pad + my / scale };
            kernel.viewport.focusRect({ ...view, x: world.x - view.w / 2, y: world.y - view.h / 2 }, size, kernel.viewport.current.k);
          }}
        >
          {kernel.scene.allNodes().map((n) => {
            const p = toMap(n.rect.x, n.rect.y);
            return (
              <span
                key={n.id}
                style={{
                  position: 'absolute',
                  left: p.x,
                  top: p.y,
                  width: Math.max(2, n.rect.w * scale),
                  height: Math.max(2, n.rect.h * scale),
                  background: n.type === 'group' ? 'transparent' : minimapColor(n.type),
                  border: n.type === 'group' ? '1px dashed var(--ic-border)' : 'none',
                  borderRadius: 2,
                  opacity: 0.85,
                }}
              />
            );
          })}
          <span
            style={{
              position: 'absolute',
              left: toMap(view.x, view.y).x,
              top: toMap(view.x, view.y).y,
              width: view.w * scale,
              height: view.h * scale,
              border: '1.5px solid var(--ic-accent)',
              borderRadius: 2,
              pointerEvents: 'none',
            }}
          />
        </div>
      )}
    </div>
  );
}

function minimapColor(type: string): string {
  switch (type) {
    case 'prompt':
      return '#7dd3fc';
    case 'image':
      return '#a5b4fc';
    case 'video':
      return '#fca5a5';
    case 'audio':
      return '#fcd34d';
    case 'generation':
      return '#86efac';
    default:
      return '#94a3b8';
  }
}
