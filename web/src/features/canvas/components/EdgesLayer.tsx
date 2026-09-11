import { useEffect, useState } from 'react';
import type { CanvasKernel } from '../kernel';

/** 连线层：单个 SVG，统一使用世界坐标（随 wrapper 一起变换）。 */
export function EdgesLayer({ kernel }: { kernel: CanvasKernel }) {
  const [, setTick] = useState(0);
  useEffect(() => kernel.subscribe(() => setTick((n) => n + 1)), [kernel]);

  const edges = kernel.scene.allEdges();
  const bounds = kernel.scene.bounds() ?? { x: 0, y: 0, w: 1000, h: 1000 };
  const pad = 200;

  return (
    <svg
      style={{
        position: 'absolute',
        left: bounds.x - pad,
        top: bounds.y - pad,
        width: bounds.w + pad * 2,
        height: bounds.h + pad * 2,
        overflow: 'visible',
        pointerEvents: 'none',
      }}
    >
      {edges.map((e) => {
        const from = kernel.scene.getNode(e.from.nodeId);
        const to = kernel.scene.getNode(e.to.nodeId);
        if (!from || !to) return null;
        const a = portAnchor(from, e.from.portId, 'out');
        const b = portAnchor(to, e.to.portId, 'in');
        const dx = Math.max(40, Math.abs(b.x - a.x) * 0.4);
        const d = `M ${a.x} ${a.y} C ${a.x + dx} ${a.y}, ${b.x - dx} ${b.y}, ${b.x} ${b.y}`;
        return (
          <g key={e.id}>
            <path d={d} fill="none" stroke="var(--ic-border)" strokeWidth={2} />
            <path d={d} fill="none" stroke="var(--ic-accent)" strokeWidth={1} strokeDasharray="6 6" opacity={0.5} />
          </g>
        );
      })}
    </svg>
  );
}

/** 端口在世界坐标中的锚点位置（与 NodeShell 的渲染规则保持一致）。 */
function portAnchor(
  node: { rect: { x: number; y: number; w: number; h: number }; ports: { inputs: unknown[]; outputs: unknown[] } },
  portId: string,
  side: 'in' | 'out',
): { x: number; y: number } {
  const list = side === 'in' ? node.ports.inputs : node.ports.outputs;
  const idx = Math.max(0, list.findIndex((p) => (p as { id: string }).id === portId));
  const ratio = (idx + 1) / (list.length + 1);
  return {
    x: side === 'in' ? node.rect.x : node.rect.x + node.rect.w,
    y: node.rect.y + node.rect.h * ratio,
  };
}
