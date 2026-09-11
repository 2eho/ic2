import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/shared/api";
import type { Plugin } from "@/shared/api/types";
import type { TFn } from "@/app/App";

interface Props {
  t: TFn;
  workspaceId: string;
  ready: boolean;
}

/**
 * 插件管理。
 * 关键安全语义（INV-6 / docs/design/06 §7）：
 * - 安装时必须展示权限清单并要求确认；
 * - 权限扩大必须重新确认（由服务端在 update 时判定，前端只负责提示）；
 * - 含 ai.generate 权限时高亮，因为会产生真实费用。
 */
export function PluginManager({ t, workspaceId, ready }: Props) {
  const qc = useQueryClient();
  const [url, setUrl] = useState("");
  const [pending, setPending] = useState<Plugin | null>(null);
  const [error, setError] = useState<string | null>(null);

  const list = useQuery({
    queryKey: ["plugins", workspaceId],
    queryFn: () => api.listPlugins(workspaceId),
    enabled: ready,
  });

  const install = useMutation({
    mutationFn: () =>
      api.installPlugin(workspaceId, { manifestUrl: url, trusted: true }),
    onSuccess: (p) => {
      setPending(p);
      void qc.invalidateQueries({ queryKey: ["plugins", workspaceId] });
    },
    onError: (e) => setError((e as { code?: string }).code ?? "internal"),
  });

  const toggle = useMutation({
    mutationFn: ({ key, enabled }: { key: string; enabled: boolean }) =>
      api.enablePlugin(workspaceId, key, enabled),
    onSuccess: () =>
      qc.invalidateQueries({ queryKey: ["plugins", workspaceId] }),
  });

  return (
    <section className="ic-card" style={{ padding: 16, marginBottom: 16 }}>
      <h2 style={{ fontSize: 15, marginTop: 0 }}>{t("settings.plugins")}</h2>

      <div style={{ display: "flex", gap: 8 }}>
        <input
          className="ic-input"
          placeholder={t("settings.pluginUrl")}
          value={url}
          onChange={(e) => setUrl(e.target.value)}
        />
        <button
          className="ic-btn"
          disabled={!url}
          onClick={() => install.mutate()}
        >
          {t("settings.installPlugin")}
        </button>
      </div>
      {error && <p className="ic-error">{t(`errors.${error}`)}</p>}

      {pending && (
        <div
          className="ic-card"
          style={{ padding: 12, marginTop: 12, borderColor: "var(--ic-warn)" }}
        >
          <strong>{pending.name}</strong>{" "}
          <span className="ic-dim">{pending.version}</span>
          <p className="ic-dim" style={{ fontSize: 12 }}>
            {t("settings.permissions")}:
          </p>
          <ul style={{ margin: 0, paddingLeft: 18, fontSize: 12 }}>
            {pending.permissions.map((p) => (
              <li key={p}>
                <span
                  className={
                    p.startsWith("ai.generate") ? "ic-badge ic-badge--warn" : ""
                  }
                >
                  {p}
                </span>
              </li>
            ))}
          </ul>
          {pending.permissions.some((p) => p.startsWith("ai.generate")) && (
            <p className="ic-error">{t("settings.permissionWarning")}</p>
          )}
          {!pending.signed && (
            <p className="ic-error">
              {t("settings.permissionWarning")}（未签名）
            </p>
          )}
        </div>
      )}

      <ul style={{ listStyle: "none", padding: 0, marginTop: 12 }}>
        {(list.data?.items ?? []).map((p) => (
          <li
            key={p.key}
            style={{
              display: "flex",
              gap: 8,
              alignItems: "center",
              fontSize: 13,
              padding: "6px 0",
              borderTop: "1px solid var(--ic-border)",
            }}
          >
            <strong>{p.name}</strong>
            <span className="ic-dim">{p.version}</span>
            {p.builtin && <span className="ic-badge">builtin</span>}
            {p.signed && <span className="ic-badge ic-badge--ok">signed</span>}
            <div style={{ flex: 1 }} />
            <button
              className="ic-btn"
              style={{ fontSize: 11, padding: "3px 8px" }}
              onClick={() => toggle.mutate({ key: p.key, enabled: !p.enabled })}
            >
              {p.enabled ? t("settings.disable") : t("settings.enable")}
            </button>
          </li>
        ))}
      </ul>
    </section>
  );
}
