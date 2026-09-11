import { useState } from 'react';
import { frameTime } from '../pure';
import { Modal } from './shared';
import type { CommonProps } from './shared';

export function VideoFrameDialog({ t, kernel, node, onClose, onCommit }: CommonProps) {
  const durationMs = Number(node.spec.durationMs ?? 0);
  const [kind, setKind] = useState<"first" | "last" | "current">("first");
  const time = frameTime(kind, durationMs / 1000, 0);

  const apply = () => {
    const child = kernel.createNode("image", {
      x: node.rect.x + node.rect.w + 60,
      y: node.rect.y,
    });
    if (child) {
      kernel.dispatch({
        type: "set-spec",
        id: child.id,
        patch: { frameAt: time, sourceAssetId: node.spec.assetId },
      });
      kernel.createEdge(node.id, "out", child.id, "in");
      onCommit();
    }
    onClose();
  };

  return (
    <Modal title={t("canvas.tool.videoFrame")} onClose={onClose}>
      {(["first", "last", "current"] as const).map((k) => (
        <label key={k} style={{ marginRight: 12 }}>
          <input
            type="radio"
            checked={kind === k}
            onChange={() => setKind(k)}
          />{" "}
          {k}
        </label>
      ))}
      <p className="ic-dim" style={{ fontSize: 12 }}>
        截帧时间：{time.toFixed(2)}s
      </p>
      <button className="ic-btn ic-btn--primary" onClick={apply}>
        {t("common.confirm")}
      </button>
    </Modal>
  );
}
