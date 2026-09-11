import { useState } from 'react';
import { api } from '@/shared/api';
import { Modal, collapseDataURIs } from './shared';
import type { CommonProps } from './shared';

export function InfoDialog({ t, node, onClose, assetId, workspaceId }: CommonProps) {
  const [showJson, setShowJson] = useState(false);
  return (
    <Modal title={t("canvas.tool.info")} onClose={onClose}>
      <dl
        style={{
          display: "grid",
          gridTemplateColumns: "auto 1fr",
          gap: "6px 12px",
          fontSize: 13,
          margin: 0,
        }}
      >
        <dt className="ic-dim">ID</dt>
        <dd className="ic-mono" style={{ margin: 0 }}>
          {node.id}
        </dd>
        <dt className="ic-dim">Type</dt>
        <dd style={{ margin: 0 }}>{node.type}</dd>
        <dt className="ic-dim">State</dt>
        <dd style={{ margin: 0 }}>{node.state}</dd>
        {assetId && (
          <>
            <dt className="ic-dim">Asset</dt>
            <dd className="ic-mono" style={{ margin: 0 }}>
              {assetId}
            </dd>
          </>
        )}
        {Boolean(node.spec.naturalW) && (
          <>
            <dt className="ic-dim">Size</dt>
            <dd style={{ margin: 0 }}>
              {String(node.spec.naturalW)}×{String(node.spec.naturalH)}
            </dd>
          </>
        )}
      </dl>
      {assetId && (
        <div style={{ marginTop: 12 }}>
          <button
            className="ic-btn"
            style={{ padding: "2px 8px", fontSize: 12 }}
            onClick={() => setShowJson((v) => !v)}
          >
            JSON
          </button>
          {showJson && (
            <pre
              className="ic-mono"
              style={{
                background: "var(--ic-surface-2)",
                padding: 10,
                borderRadius: 8,
                maxHeight: 240,
                overflow: "auto",
              }}
            >
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
      <img
        src={api.assetThumbUrl(assetId, workspaceId, 480)}
        alt=""
        style={{ maxWidth: "100%", marginTop: 12, borderRadius: 8 }}
      />
    </Modal>
  );
}
