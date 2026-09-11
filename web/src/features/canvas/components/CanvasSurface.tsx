import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import type { CanvasKernel } from "../kernel";
import type { Modifiers } from "../kernel/interaction";
import { NodeShell } from "./NodeShell";
import { EdgesLayer } from "./EdgesLayer";
import { Minimap } from "./Minimap";
import { ZoomControls } from "./ZoomControls";
import { ContextMenu } from "./ContextMenu";
import { useKernelSelection } from "../hooks/useKernel";
import type { TFn } from "@/app/App";

interface Props {
  t: TFn;
  kernel: CanvasKernel;
  onCommit: () => void;
  onRun: (nodeIds: string[]) => void;
}

/**
 * 画布渲染宿主。
 *
 * 分层（docs/design/04 §2.1）：
 *   grid → edges(SVG) → nodes(DOM) → overlay → widgets
 *
 * 视口变换**只写一个 transform 到 wrapper**，不触发 React 重渲染节点树。
 */
export function CanvasSurface({ t, kernel, onCommit, onRun }: Props) {
  const hostRef = useRef<HTMLDivElement>(null);
  const wrapperRef = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState({ x: 800, y: 600 });
  const [visible, setVisible] = useState(() => kernel.scene.allNodes());
  const [marquee, setMarquee] = useState<{
    x: number;
    y: number;
    w: number;
    h: number;
  } | null>(null);
  const [menu, setMenu] = useState<{
    x: number;
    y: number;
    nodeId?: string;
  } | null>(null);
  const [connection, setConnection] = useState<{
    x1: number;
    y1: number;
    x2: number;
    y2: number;
  } | null>(null);
  const selection = useKernelSelection(kernel);

  // 视口 → 单一 transform（合成层完成，不进 React）
  useEffect(() => {
    const apply = (v: { x: number; y: number; k: number }) => {
      if (wrapperRef.current) {
        wrapperRef.current.style.transform = `translate3d(${v.x}px, ${v.y}px, 0) scale(${v.k})`;
        wrapperRef.current.style.transformOrigin = "0 0";
      }
    };
    apply(kernel.viewport.current);
    return kernel.subscribeViewport(apply);
  }, [kernel]);

  // 文档变化 → 重算可见节点（视口裁剪）
  useEffect(() => {
    const recompute = () => {
      const world = kernel.viewport.visibleWorldRect(size, 280);
      setVisible(kernel.scene.queryRect(world));
    };
    recompute();
    const unsub = kernel.subscribe(recompute);
    const unsubVp = kernel.subscribeViewport(recompute);
    return () => {
      unsub();
      unsubVp();
    };
  }, [kernel, size]);

  useEffect(() => {
    const el = hostRef.current;
    if (!el) return;
    const update = () => {
      const r = el.getBoundingClientRect();
      setSize({ x: r.width, y: r.height });
    };
    update();
    const ro = new ResizeObserver(update);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const modifiers = (
    e: React.PointerEvent | React.MouseEvent | React.KeyboardEvent,
  ): Modifiers => ({
    shift: e.shiftKey,
    ctrl: e.ctrlKey,
    meta: e.metaKey,
    alt: e.altKey,
    space: kernel.interaction.spaceHeld,
  });

  const onPointerDown = (e: React.PointerEvent) => {
    if (e.button !== 0 && e.button !== 1) return;
    (e.target as HTMLElement).setPointerCapture?.(e.pointerId);
    setMenu(null);
    const screen = {
      x: e.clientX - (hostRef.current?.getBoundingClientRect().left ?? 0),
      y: e.clientY - (hostRef.current?.getBoundingClientRect().top ?? 0),
    };
    const world = kernel.viewport.toWorld(screen);
    const hit = kernel.scene.hitTest(world, { skipGroups: true });
    const worldRect = kernel.viewport.visibleWorldRect(size);
    const screenRect = { x: 0, y: 0, w: size.x, h: size.y };
    void worldRect;

    // 手柄命中优先（仅单选时）
    let handle: ReturnType<
      typeof import("../kernel/geometry").hitResizeHandle
    > = null;
    const selNodes = selection.nodes;
    if (selNodes.length === 1) {
      const n = kernel.scene.getNode(selNodes[0]);
      if (n) {
        const r = kernel.viewport.rectToScreen(n.rect);
        handle = hitHandle(screen, r);
      }
    }
    void screenRect;

    const intent = kernel.interaction.handle({
      type: "pointerdown",
      point: screen,
      hitNode: hit?.id,
      hitHandle: handle ?? undefined,
      modifiers: modifiers(e),
    });
    if (intent.type === "start-drag" || intent.type === "none") {
      if (hit && !selection.nodes.includes(hit.id)) {
        kernel.setSelection({ nodes: [hit.id], edges: [] });
      }
    }
    kernel.noteDown(screen);
    kernel.notePointer(screen);
    kernel.handleIntent(intent);
  };

  const onPointerMove = (e: React.PointerEvent) => {
    const rect = hostRef.current?.getBoundingClientRect();
    const screen = {
      x: e.clientX - (rect?.left ?? 0),
      y: e.clientY - (rect?.top ?? 0),
    };
    kernel.notePointer(screen);
    const intent = kernel.interaction.handle({
      type: "pointermove",
      point: screen,
      modifiers: modifiers(e),
    });
    if (intent.type === "update-marquee") {
      const start = { x: 0, y: 0 };
      void start;
    }
    const ops = kernel.handleIntent(intent);
    if (ops.length > 0) onCommit();
  };

  const onPointerUp = (e: React.PointerEvent) => {
    const rect = hostRef.current?.getBoundingClientRect();
    const screen = {
      x: e.clientX - (rect?.left ?? 0),
      y: e.clientY - (rect?.top ?? 0),
    };
    const state = kernel.interaction.current;
    const intent = kernel.interaction.handle({
      type: "pointerup",
      point: screen,
    });
    if (state === "marquee") {
      // 框选提交：把屏幕矩形转世界坐标后做交集命中
      const start = kernel.lastDownPoint ?? screen;
      const a = kernel.viewport.toWorld(start);
      const b = kernel.viewport.toWorld(screen);
      const rectWorld = {
        x: Math.min(a.x, b.x),
        y: Math.min(a.y, b.y),
        w: Math.abs(b.x - a.x),
        h: Math.abs(b.y - a.y),
      };
      const hits = kernel.scene
        .hitRect(rectWorld, { skipGroups: true })
        .map((n) => n.id);
      kernel.setSelection({ nodes: hits, edges: [] });
    }
    setMarquee(null);
    setConnection(null);
    kernel.handleIntent(intent);
  };

  const onWheel = (e: React.WheelEvent) => {
    const rect = hostRef.current?.getBoundingClientRect();
    const screen = {
      x: e.clientX - (rect?.left ?? 0),
      y: e.clientY - (rect?.top ?? 0),
    };
    // 触控板双指滚动 vs 缩放：ctrlKey 为真时才缩放（对齐原项目体验）
    if (!e.ctrlKey && !e.metaKey) return;
    e.preventDefault();
    kernel.handleIntent({
      type: "zoom",
      point: screen,
      delta: e.deltaY * 0.0025,
    });
  };

  const onContextMenu = (e: React.MouseEvent) => {
    e.preventDefault();
    const rect = hostRef.current?.getBoundingClientRect();
    const screen = {
      x: e.clientX - (rect?.left ?? 0),
      y: e.clientY - (rect?.top ?? 0),
    };
    const world = kernel.viewport.toWorld(screen);
    const hit = kernel.scene.hitTest(world, { skipGroups: false });
    setMenu({ x: e.clientX, y: e.clientY, nodeId: hit?.id });
    if (hit) kernel.setSelection({ nodes: [hit.id], edges: [] });
  };

  return (
    <div
      ref={hostRef}
      data-canvas-host
      style={{
        position: "absolute",
        inset: 0,
        overflow: "hidden",
        touchAction: "none",
        cursor:
          kernel.interaction.current === "panning" ? "grabbing" : "default",
      }}
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={onPointerUp}
      onPointerCancel={onPointerUp}
      onWheel={onWheel}
      onContextMenu={onContextMenu}
      onDoubleClick={(e) => {
        const rect = hostRef.current?.getBoundingClientRect();
        const screen = {
          x: e.clientX - (rect?.left ?? 0),
          y: e.clientY - (rect?.top ?? 0),
        };
        const world = kernel.viewport.toWorld(screen);
        const hit = kernel.scene.hitTest(world, { skipGroups: true });
        kernel.handleIntent(
          kernel.interaction.handle({
            type: "dblclick",
            point: screen,
            hitNode: hit?.id,
          }),
        );
      }}
      tabIndex={0}
    >
      {/* 背景层：网格/点阵（主题相关，纯 CSS） */}
      <div
        style={{
          position: "absolute",
          inset: 0,
          backgroundImage:
            kernel.settings.background === "dots"
              ? "radial-gradient(circle, var(--ic-border) 1px, transparent 1px)"
              : kernel.settings.background === "lines"
                ? "linear-gradient(var(--ic-border) 1px, transparent 1px), linear-gradient(90deg, var(--ic-border) 1px, transparent 1px)"
                : "none",
          backgroundSize: "24px 24px",
          opacity: 0.7,
        }}
      />

      {/* 内容 wrapper：唯一受视口变换影响的节点 */}
      <div
        ref={wrapperRef}
        style={{
          position: "absolute",
          top: 0,
          left: 0,
          willChange: "transform",
        }}
      >
        <EdgesLayer kernel={kernel} />
        {visible.map((n) => (
          <NodeShell
            key={n.id}
            t={t}
            kernel={kernel}
            nodeId={n.id}
            onCommit={onCommit}
            onRun={onRun}
          />
        ))}
      </div>

      {/* overlay：选中框与连线预览（不随视口缩放体积） */}
      <svg
        style={{
          position: "absolute",
          inset: 0,
          pointerEvents: "none",
          width: "100%",
          height: "100%",
        }}
      >
        {selection.nodes.map((id) => {
          const n = kernel.scene.getNode(id);
          if (!n) return null;
          const r = kernel.viewport.rectToScreen(n.rect);
          return (
            <rect
              key={id}
              x={r.x - 2}
              y={r.y - 2}
              width={r.w + 4}
              height={r.h + 4}
              fill="none"
              stroke="var(--ic-accent)"
              strokeWidth={1.5}
              strokeDasharray="4 3"
            />
          );
        })}
        {marquee && (
          <rect
            x={marquee.x}
            y={marquee.y}
            width={marquee.w}
            height={marquee.h}
            fill="var(--ic-accent-soft)"
            stroke="var(--ic-accent)"
            strokeWidth={1}
          />
        )}
        {connection && (
          <line
            x1={connection.x1}
            y1={connection.y1}
            x2={connection.x2}
            y2={connection.y2}
            stroke="var(--ic-accent)"
            strokeWidth={2}
            strokeDasharray="5 4"
          />
        )}
      </svg>

      <Minimap kernel={kernel} size={size} />
      <ZoomControls kernel={kernel} size={size} t={t} />

      {menu && (
        <ContextMenu
          t={t}
          x={menu.x}
          y={menu.y}
          nodeId={menu.nodeId}
          kernel={kernel}
          onClose={() => setMenu(null)}
          onCommit={onCommit}
          onRun={onRun}
        />
      )}

      {/* 状态与提示 */}
      <div
        style={{
          position: "absolute",
          left: 12,
          bottom: 12,
          display: "flex",
          gap: 8,
          alignItems: "center",
        }}
      >
        <span className="ic-badge">
          {kernel.scene.size} {t("canvas.nodes")}
        </span>
        {kernel.settings.readOnly && (
          <span className="ic-badge ic-badge--warn">
            {t("canvas.readonly")}
          </span>
        )}
      </div>
    </div>
  );
}

function hitHandle(
  screen: { x: number; y: number },
  r: { x: number; y: number; w: number; h: number },
) {
  const importHit = (
    globalThis as unknown as {
      __icHitHandle?: typeof import("../kernel/geometry").hitResizeHandle;
    }
  ).__icHitHandle;
  if (importHit) return importHit(screen, r);
  // 内联实现，避免动态 import（保持同步）
  const hs = 10;
  const centers: Record<string, { x: number; y: number }> = {
    nw: { x: r.x, y: r.y },
    n: { x: r.x + r.w / 2, y: r.y },
    ne: { x: r.x + r.w, y: r.y },
    e: { x: r.x + r.w, y: r.y + r.h / 2 },
    se: { x: r.x + r.w, y: r.y + r.h },
    s: { x: r.x + r.w / 2, y: r.y + r.h },
    sw: { x: r.x, y: r.y + r.h },
    w: { x: r.x, y: r.y + r.h / 2 },
  };
  for (const [k, c] of Object.entries(centers)) {
    if (
      Math.abs(screen.x - c.x) <= hs / 2 &&
      Math.abs(screen.y - c.y) <= hs / 2
    ) {
      return k as "nw" | "n" | "ne" | "e" | "se" | "s" | "sw" | "w";
    }
  }
  return null;
}

/** 供调试与 e2e 使用：暴露内核到 window（仅开发构建）。 */
export function useExposeKernel(kernel: CanvasKernel | null) {
  useSyncExternalStore(
    (cb) => (kernel ? kernel.subscribe(cb) : () => {}),
    () => kernel?.currentVersion ?? 0,
  );
  useEffect(() => {
    if (kernel && import.meta.env.DEV) {
      (window as unknown as { __icKernel?: CanvasKernel }).__icKernel = kernel;
    }
  }, [kernel]);
}
