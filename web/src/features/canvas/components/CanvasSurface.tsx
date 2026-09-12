import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import type { CanvasKernel } from "../kernel";
import type { Modifiers } from "../kernel/interaction";
import { NodeShell } from "./NodeShell";
import { EdgesLayer } from "./EdgesLayer";
import { Minimap } from "./Minimap";
import { ZoomControls } from "./ZoomControls";
import { ContextMenu } from "./ContextMenu";
import { SelectionToolbar } from "./SelectionToolbar";
import { useKernelSelection } from "../hooks/useKernel";
import { NODE_SCHEMAS } from "../kernel/schema";
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
  // 落点为空白时的创建菜单（3.7）
  const [connectMenu, setConnectMenu] = useState<{
    screenX: number;
    screenY: number;
    worldX: number;
    worldY: number;
    fromNodeId: string;
    fromPort: string;
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

    // 端口命中：在屏幕坐标下找最近的可拖拽端口锚点（3.x）
    const portHit = hitPortAnchor(screen, kernel);

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
      hitPort: portHit ?? undefined,
      modifiers: modifiers(e),
    });
    if (intent.type === "start-drag" || intent.type === "none") {
      if (hit) {
        if (e.shiftKey) {
          // Shift 点击：未选中 → 追加；已选中 → 反选（与主流画布一致）。
          // 走内核的 toggleSelection / additive 路径，保证选区语义只有一处真源。
          if (selection.nodes.includes(hit.id)) {
            kernel.toggleSelection({ nodes: [hit.id], edges: [] });
          } else {
            kernel.setSelection(
              { nodes: [hit.id], edges: [] },
              { additive: true },
            );
          }
        } else if (!selection.nodes.includes(hit.id)) {
          kernel.setSelection({ nodes: [hit.id], edges: [] });
        }
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
    if (intent.type === "connect-drag") {
      const from = kernel.interaction.connecting;
      if (from) {
        const node = kernel.scene.getNode(from.nodeId);
        if (node) {
          const anchor = portAnchorScreen(
            node,
            from.portId,
            from.side,
            kernel,
          );
          setConnection({
            x1: anchor.x,
            y1: anchor.y,
            x2: intent.point.x,
            y2: intent.point.y,
          });
        }
      }
      return;
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
    if (state === "connecting") {
      // 连线的落点由这里决定：只有渲染层知道屏幕 → 世界 → 端口锚点的换算。
      const world = kernel.viewport.toWorld(screen);
      const target = kernel.scene.hitTest(world, { skipGroups: true });
      const port = target ? hitPortAnchor(screen, kernel, target.id) : null;
      const connectIntent = kernel.interaction.commitConnect(
        port ? { nodeId: port.nodeId, portId: port.portId } : null,
        screen,
      );
      const ops = kernel.handleIntent(connectIntent);
      if (ops.length > 0) onCommit();
      const blank = kernel.takeConnectBlank();
      if (blank) {
        setConnectMenu({
          screenX: screen.x,
          screenY: screen.y,
          worldX: kernel.viewport.toWorld(screen).x,
          worldY: kernel.viewport.toWorld(screen).y,
          fromNodeId: blank.fromNodeId,
          fromPort: blank.fromPort,
        });
      }
      setConnection(null);
      return;
    }
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
      // Shift + 框选：并入既有选区（对等矩阵 3.3「Shift 追加」）。
      kernel.setSelection({ nodes: hits, edges: [] }, { additive: e.shiftKey });
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

      {/* 3.7：连线落到空白处 → 创建节点菜单（与右键菜单同一套外观） */}
      {connectMenu && (
        <ConnectCreateMenu
          t={t}
          x={connectMenu.screenX}
          y={connectMenu.screenY}
          kernel={kernel}
          worldX={connectMenu.worldX}
          worldY={connectMenu.worldY}
          fromNodeId={connectMenu.fromNodeId}
          fromPort={connectMenu.fromPort}
          onClose={() => setConnectMenu(null)}
          onCommit={onCommit}
        />
      )}

      {/* 3.18 多选工具栏：一键对齐 / 等间距（选中 ≥2 时出现） */}
      <SelectionToolbar t={t} kernel={kernel} onCommit={onCommit} />

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

/**
 * 在屏幕坐标下命中端口锚点。
 *
 * 为什么用「屏幕坐标 + 距离阈值」而不是 DOM 事件：端点是 10px 的小圆点，
 * 用 DOM 事件需要给每个端点挂 handler，且缩放后点击区域会随视口一起变大变小
 * （放大时 10px 变成 30px，用户会觉得「怎么点都命中」）。这里统一按屏幕像素
 * 判定，命中区域与视觉大小始终一致。
 *
 * onlyNodeID 用于松开鼠标时**只在落点节点上**找端口：
 * 不限定时会把手指附近的另一个节点端口当成落点，产生用户没打算建的连线。
 */
function hitPortAnchor(
  screen: { x: number; y: number },
  kernel: CanvasKernel,
  onlyNodeID?: string,
): { nodeId: string; portId: string; side: "in" | "out"; kind: string } | null {
  const RADIUS = 12;
  let best: {
    nodeId: string;
    portId: string;
    side: "in" | "out";
    kind: string;
    dist: number;
  } | null = null;
  for (const node of kernel.scene.allNodes()) {
    if (onlyNodeID && node.id !== onlyNodeID) continue;
    for (const side of ["in", "out"] as const) {
      const ports = side === "in" ? node.ports.inputs : node.ports.outputs;
      for (const p of ports) {
        const anchor = portAnchorScreen(node, p.id, side, kernel);
        const dist = Math.abs(anchor.x - screen.x) + Math.abs(anchor.y - screen.y);
        if (dist <= RADIUS && (!best || dist < best.dist)) {
          best = {
            nodeId: node.id,
            portId: p.id,
            side,
            kind: p.kind,
            dist,
          };
        }
      }
    }
  }
  if (!best) return null;
  return {
    nodeId: best.nodeId,
    portId: best.portId,
    side: best.side,
    kind: best.kind,
  };
}

/**
 * 端口锚点的屏幕坐标。
 *
 * 与 NodeShell 的渲染规则必须一致（输入在左、输出在右，垂直均分）。
 * 不一致的表现是「预览线从一个位置出发、实际连线接到另一个位置」——
 * 界面上看起来只是有点歪，排查时却要同时看三个文件。
 */
function portAnchorScreen(
  node: {
    rect: { x: number; y: number; w: number; h: number };
    ports: { inputs: unknown[]; outputs: unknown[] };
  },
  portId: string,
  side: "in" | "out",
  kernel: CanvasKernel,
): { x: number; y: number } {
  const list = side === "in" ? node.ports.inputs : node.ports.outputs;
  const idx = Math.max(
    0,
    list.findIndex((p) => (p as { id: string }).id === portId),
  );
  const ratio = (idx + 1) / (list.length + 1);
  const world = {
    x: side === "in" ? node.rect.x : node.rect.x + node.rect.w,
    y: node.rect.y + node.rect.h * ratio,
  };
  return kernel.viewport.toScreen(world);
}


/** 连线落到空白处时的创建菜单（3.7）。
 *
 * 只列出**与该端口类型兼容**的节点：不可连的选项如果出现在菜单里，
 * 用户点下去会得到「创建了但没连上」——比不显示更让人困惑。
 */
function ConnectCreateMenu({
  t,
  x,
  y,
  kernel,
  worldX,
  worldY,
  fromNodeId,
  fromPort,
  onClose,
  onCommit,
}: {
  t: TFn;
  x: number;
  y: number;
  kernel: CanvasKernel;
  worldX: number;
  worldY: number;
  fromNodeId: string;
  fromPort: string;
  onClose: () => void;
  onCommit: () => void;
}) {
  const from = kernel.scene.getNode(fromNodeId);
  const port = from
    ? [...from.ports.inputs, ...from.ports.outputs].find((p) => p.id === fromPort)
    : undefined;
  const kind = port?.kind ?? "text";

  const options = connectOptions(kind);
  if (options.length === 0) {
    return (
      <div
        className="ic-card"
        style={{ position: "absolute", left: x, top: y, padding: 10, zIndex: 1001 }}
        onMouseLeave={onClose}
      >
        <span className="ic-dim" style={{ fontSize: 12 }}>
          没有可连接的节点类型
        </span>
      </div>
    );
  }

  return (
    <div
      className="ic-card"
      style={{ position: "absolute", left: x, top: y, padding: 8, zIndex: 1001 }}
      onMouseLeave={onClose}
    >
      <div className="ic-dim" style={{ fontSize: 11, marginBottom: 6 }}>
        {t("canvas.createAndConnect")}
      </div>
      {options.map((d) => (
        <button
          key={d.type}
          className="ic-btn ic-btn--ghost"
          style={{ display: "block", width: "100%", textAlign: "left", fontSize: 12 }}
          onClick={() => {
            const node = kernel.createNode(d.type, { x: worldX, y: worldY });
            if (node) {
              // 连线方向按端口语义归一：从输出端口创建的是下游，反之是上游。
              const port = from
                ? [...from.ports.inputs, ...from.ports.outputs].find(
                    (p) => p.id === fromPort,
                  )
                : undefined;
              const isOut = from?.ports.outputs.some((p) => p.id === fromPort);
              void port;
              const target = node.ports.inputs.find((p) => p.kind === kind);
              const targetOut = node.ports.outputs.find((p) => p.kind === kind);
              if (isOut && target) {
                kernel.createEdge(fromNodeId, fromPort, node.id, target.id);
              } else if (!isOut && targetOut) {
                kernel.createEdge(node.id, targetOut.id, fromNodeId, fromPort);
              }
              onCommit();
            }
            onClose();
          }}
        >
          {d.title}
        </button>
      ))}
    </div>
  );
}


/**
 * 按端口类型列出可创建的节点（3.7）。
 *
 * 判据是**服务端 schema 的端口兼容性**（同 kind 才算可连），不是一张手工维护的
 * 对应表：手工表会在新增节点类型时漏更新，症状是「新类型节点在菜单里不出现」。
 */
function connectOptions(kind: string): Array<{ type: string; title: string }> {
  const out: Array<{ type: string; title: string }> = [];
  for (const [type, schema] of Object.entries(NODE_SCHEMAS)) {
    if (type === "group" || type === "run") continue;
    const ports =
      kind === "text"
        ? schema.ports.inputs
        : schema.ports.inputs.filter((p) => p.kind === kind);
    const outputs = schema.ports.outputs.filter((p) => p.kind === kind);
    if (ports.length > 0 || outputs.length > 0) {
      out.push({ type, title: schema.title });
    }
  }
  return out;
}
