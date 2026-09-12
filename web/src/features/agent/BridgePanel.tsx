import { useState } from "react";
import type { BridgeConfig, BridgeHealth } from "@/shared/client/bridge";
import type { TFn } from "@/app/App";

interface Props {
  t: TFn;
  config: BridgeConfig;
  health: BridgeHealth | null;
  error: string | null;
  onSave: (cfg: BridgeConfig) => void;
  onProbe: () => void;
}

/**
 * 本机桥接器配置面板。
 *
 * 界面上必须讲清楚的一件事：这里的「令牌」与「模型 API Key」不是同一种东西。
 *   - 模型 API Key：能花你的钱、访问上游账号 → **只存服务端**（INV-5）；
 *   - 桥接器令牌：只能访问你自己机器上只监听 127.0.0.1 的端口 → 可以存浏览器。
 * 如果界面不说明，用户会得出「既然这个能存，那 Key 也能存」的错误结论——
 * 这正是原项目把 Key 存 localStorage 的心理成因。
 */
export function BridgePanel({
  t,
  config,
  health,
  error,
  onSave,
  onProbe,
}: Props) {
  const [url, setUrl] = useState(config.url);
  const [token, setToken] = useState(config.token);

  return (
    <div className="ic-card" style={{ padding: 10, marginBottom: 8 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
        <strong style={{ fontSize: 12 }}>{t("agent.localAgent")}</strong>
        <span
          className={`ic-badge ${health ? "ic-badge--ok" : error ? "ic-badge--danger" : ""}`}
          style={{ fontSize: 10 }}
        >
          {health
            ? health.backends.join(", ") || t("agent.bridgeNoBackend")
            : error
              ? t(error)
              : t("agent.bridgeUnknown")}
        </span>
      </div>

      <label style={{ display: "block", marginTop: 6 }}>
        <span className="ic-dim" style={{ fontSize: 11 }}>
          {t("agent.bridgeUrl")}
        </span>
        <input
          className="ic-input"
          style={{ fontSize: 12 }}
          value={url}
          onChange={(e) => setUrl(e.target.value)}
        />
      </label>
      <label style={{ display: "block", marginTop: 4 }}>
        <span className="ic-dim" style={{ fontSize: 11 }}>
          {t("agent.bridgeToken")}
        </span>
        <input
          className="ic-input"
          style={{ fontSize: 12 }}
          type="password"
          value={token}
          onChange={(e) => setToken(e.target.value)}
        />
      </label>
      <p className="ic-dim" style={{ fontSize: 11, margin: "6px 0 0" }}>
        {t("agent.bridgeTokenHint")}
      </p>

      <div style={{ display: "flex", gap: 6, marginTop: 8 }}>
        <button
          className="ic-btn"
          style={{ fontSize: 11 }}
          onClick={() => onSave({ url, token })}
        >
          {t("common.save")}
        </button>
        <button className="ic-btn" style={{ fontSize: 11 }} onClick={onProbe}>
          {t("settings.testConnection")}
        </button>
      </div>

      <p
        className="ic-dim"
        style={{ fontSize: 11, margin: "6px 0 0", lineHeight: 1.5 }}
      >
        {t("agent.bridgeStartHint")}
        <br />
        <code className="ic-mono">node canvas-agent/src/cli.js</code>
      </p>
    </div>
  );
}
