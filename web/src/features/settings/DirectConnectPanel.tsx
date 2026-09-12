import { useState } from "react";
import type { WorkspacePrefs } from "@/shared/api";
import type { TFn } from "@/app/App";

/** 直连生效范围的可选项（与服务端 providerCapabilityKnown 的白名单一致）。 */
export const DIRECT_CAPABILITIES = [
  "image.generate",
  "image.edit",
  "image.upscale",
  "text.generate",
  "video.generate",
  "audio.generate",
] as const;

/** 默认生效范围：与服务端 DirectDefaultScope 保持一致。 */
export const DIRECT_DEFAULT_SCOPE = ["image.generate"];

interface Props {
  t: TFn;
  prefs: WorkspacePrefs;
  update: (patch: WorkspacePrefs) => void;
}

/**
 * 本地直连模式开关（4.21）。
 *
 * 这是个**降级通道**，不是常规路径：正常形态是服务端统一编排
 * （凭据在服务端、执行在服务端、计量与限额也都在服务端）。
 * 它存在的理由是内网模型与「密钥不离开本机」这两类真实需求。
 *
 * 交互上有一条不可省略的规则：**先看风险说明，再勾选确认，才能开启**。
 * 服务端也会校验（acknowledgedAt 为空即不生效），所以这里不是「礼貌性提示」，
 * 而是与后端同一份约束 —— 只放 UI 的约束可以绕过，只放后端的约束用户看不懂。
 *
 * 范围（Scope）默认只有 image.generate。全部走直连等于同时绕过服务端编排、
 * 计量与限额 —— 那不是一个开关该有的权力，所以需要用户显式勾选每一项。
 */
export function DirectConnectPanel({ t, prefs, update }: Props) {
  const direct = prefs.direct ?? { enabled: false };
  const [acknowledged, setAcknowledged] = useState(
    Boolean(direct.acknowledgedAt),
  );
  const [error, setError] = useState<string | null>(null);

  const scope = direct.scope?.length ? direct.scope : DIRECT_DEFAULT_SCOPE;

  const patchDirect = (patch: Partial<NonNullable<WorkspacePrefs["direct"]>>) =>
    update({
      direct: {
        ...direct,
        enabled: direct.enabled ?? false,
        scope: [...scope],
        ...patch,
      },
    });

  const toggleEnabled = (next: boolean) => {
    if (!next) {
      patchDirect({ enabled: false });
      return;
    }
    if (!acknowledged) {
      // 服务端会拒绝（acknowledgedAt 为空 → 422），这里先给出可读的原因，
      // 而不是让用户看到一条后端错误码。
      setError(t("settings.directNeedsAck"));
      return;
    }
    setError(null);
    patchDirect({
      enabled: true,
      acknowledgedAt: direct.acknowledgedAt ?? new Date().toISOString(),
    });
  };

  const toggleScope = (cap: string, on: boolean) => {
    const next = on
      ? [...new Set([...scope, cap])]
      : scope.filter((c) => c !== cap);
    patchDirect({ scope: next });
  };

  return (
    <section className="ic-card" style={{ padding: 16, marginBottom: 16 }}>
      <h2 style={{ fontSize: 15, marginTop: 0 }}>
        {t("settings.directTitle")}
      </h2>
      <p className="ic-dim" style={{ fontSize: 12, marginTop: 0 }}>
        {t("settings.directHint")}
      </p>

      <label style={{ display: "flex", gap: 8, alignItems: "center" }}>
        <input
          type="checkbox"
          checked={acknowledged}
          onChange={(e) => {
            setAcknowledged(e.target.checked);
            if (!e.target.checked) {
              // 取消确认必须连带关闭开关：留下一个「已开启但未确认」的状态
              // 会让服务端拒绝所有写入，用户看到的是「保存没反应」。
              patchDirect({ enabled: false, acknowledgedAt: "" });
            }
          }}
        />
        <span style={{ fontSize: 12 }}>{t("settings.directRisk")}</span>
      </label>

      <label
        style={{ display: "flex", gap: 8, alignItems: "center", marginTop: 8 }}
      >
        <input
          type="checkbox"
          checked={Boolean(direct.enabled) && acknowledged}
          disabled={!acknowledged}
          onChange={(e) => toggleEnabled(e.target.checked)}
        />
        <span style={{ fontSize: 12 }}>{t("settings.directEnable")}</span>
      </label>

      {direct.enabled && acknowledged && (
        <>
          <label style={{ display: "block", marginTop: 10 }}>
            <span className="ic-dim" style={{ fontSize: 12 }}>
              {t("settings.directBaseUrl")}
            </span>
            <input
              className="ic-input"
              placeholder="http://127.0.0.1:8317"
              value={direct.baseUrl ?? ""}
              onChange={(e) => patchDirect({ baseUrl: e.target.value })}
            />
            <span className="ic-dim" style={{ fontSize: 11 }}>
              {t("settings.directBaseUrlHint")}
            </span>
          </label>

          <div style={{ marginTop: 10 }}>
            <span className="ic-dim" style={{ fontSize: 12 }}>
              {t("settings.directScope")}
            </span>
            <div style={{ display: "flex", gap: 10, flexWrap: "wrap" }}>
              {DIRECT_CAPABILITIES.map((cap) => (
                <label
                  key={cap}
                  style={{ display: "flex", gap: 4, alignItems: "center" }}
                >
                  <input
                    type="checkbox"
                    checked={scope.includes(cap)}
                    onChange={(e) => toggleScope(cap, e.target.checked)}
                  />
                  <span className="ic-mono" style={{ fontSize: 11 }}>
                    {cap}
                  </span>
                </label>
              ))}
            </div>
          </div>
        </>
      )}

      {error && (
        <p className="ic-error" style={{ fontSize: 12 }}>
          {error}
        </p>
      )}
    </section>
  );
}
