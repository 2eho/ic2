import { useEffect, useState } from "react";
import { usePrefs } from "./usePrefs";
import { consumeCredentialParams } from "./url-import";
import type { TFn } from "@/app/App";

interface Props {
  t: TFn;
  workspaceId: string;
  ready: boolean;
  onLocaleChange: (locale: "zh-CN" | "en-US") => void;
  onThemeChange: (theme: "light" | "dark") => void;
  theme: "light" | "dark";
  locale: "zh-CN" | "en-US";
}

/**
 * 偏好面板（docs/design/10 §8.4 / §8.5）。
 *
 * 一个必须显式处理的行为：**URL 参数导入凭据**（§8.9）。
 * 组件挂载时检查地址栏，若带 apiKey/baseUrl 就提示用户「一键配置」，
 * 并在读取后立刻清除参数（防止密钥进浏览器历史与 referer）。
 *
 * 这里刻意**不自动写入**凭据：自动创建渠道意味着「点一个链接就产生一次外部调用」，
 * 用户不知道发生了什么。改为展示将要导入的内容并要求确认。
 */
export function PrefsPanel({
  t,
  workspaceId,
  ready,
  onLocaleChange,
  onThemeChange,
  theme,
  locale,
}: Props) {
  const { prefs, secrets, saving, error, update } = usePrefs(
    workspaceId,
    ready,
  );
  const [urlCred, setUrlCred] = useState<{
    baseUrl: string;
    apiKey: string;
    name?: string;
  } | null>(null);
  const [systemPrompt, setSystemPrompt] = useState("");

  // 挂载时消费一次地址栏参数（消费即清除）
  useEffect(() => {
    const parsed = consumeCredentialParams();
    if (parsed) setUrlCred(parsed);
  }, []);

  useEffect(() => {
    setSystemPrompt(prefs.generation?.systemPrompt ?? "");
  }, [prefs.generation?.systemPrompt]);

  return (
    <>
      {urlCred && (
        <section
          className="ic-card"
          style={{
            padding: 12,
            marginBottom: 12,
            borderColor: "var(--ic-warn)",
          }}
        >
          <strong style={{ fontSize: 13 }}>
            {t("settings.urlImportTitle")}
          </strong>
          <p className="ic-dim" style={{ fontSize: 12, margin: "4px 0" }}>
            {t("settings.urlImportHint")}
          </p>
          <dl className="ic-mono" style={{ fontSize: 11, margin: 0 }}>
            <div>baseUrl: {urlCred.baseUrl || "(空)"}</div>
            {/* 密钥只显示掩码：即使是在用户自己的屏幕上，也不该完整回显 */}
            <div>
              apiKey:{" "}
              {urlCred.apiKey ? `${urlCred.apiKey.slice(0, 4)}****` : "(空)"}
            </div>
          </dl>
          <div style={{ display: "flex", gap: 6, marginTop: 8 }}>
            <button
              className="ic-btn ic-btn--primary"
              style={{ fontSize: 11 }}
              onClick={() => setUrlCred(null)}
            >
              {t("settings.urlImportConfirm")}
            </button>
            <button
              className="ic-btn"
              style={{ fontSize: 11 }}
              onClick={() => setUrlCred(null)}
            >
              {t("common.cancel")}
            </button>
          </div>
          <p className="ic-dim" style={{ fontSize: 11, margin: "6px 0 0" }}>
            {t("settings.urlImportCleared")}
          </p>
        </section>
      )}

      <section className="ic-card" style={{ padding: 16, marginBottom: 16 }}>
        <h2 style={{ fontSize: 15, marginTop: 0 }}>
          {t("settings.appearance")}
        </h2>
        <div style={{ display: "flex", gap: 16, flexWrap: "wrap" }}>
          <label>
            <span className="ic-dim" style={{ fontSize: 12 }}>
              {t("settings.language")}
            </span>
            <select
              className="ic-select"
              value={prefs.locale ?? locale}
              onChange={(e) => {
                const next = e.target.value as "zh-CN" | "en-US";
                onLocaleChange(next);
                update({ locale: next });
              }}
            >
              <option value="zh-CN">简体中文</option>
              <option value="en-US">English</option>
            </select>
          </label>
          <label>
            <span className="ic-dim" style={{ fontSize: 12 }}>
              {t("settings.theme")}
            </span>
            <select
              className="ic-select"
              value={prefs.theme ?? theme}
              onChange={(e) => {
                const next = e.target.value as "light" | "dark";
                onThemeChange(next);
                update({ theme: next });
              }}
            >
              <option value="light">{t("settings.themeLight")}</option>
              <option value="dark">{t("settings.themeDark")}</option>
            </select>
          </label>
        </div>
      </section>

      <section className="ic-card" style={{ padding: 16, marginBottom: 16 }}>
        <h2 style={{ fontSize: 15, marginTop: 0 }}>
          {t("settings.defaultModels")}
        </h2>
        <p className="ic-dim" style={{ fontSize: 12, marginTop: 0 }}>
          {t("settings.defaultModelsHint")}
        </p>
        <div
          style={{
            display: "grid",
            gridTemplateColumns: "repeat(auto-fit, minmax(200px, 1fr))",
            gap: 10,
          }}
        >
          {(["image", "video", "text", "audio"] as const).map((kind) => (
            <label key={kind}>
              <span className="ic-dim" style={{ fontSize: 12 }}>
                {t(`workbench.kind.${kind}`) !== `workbench.kind.${kind}`
                  ? t(`workbench.kind.${kind}`)
                  : kind}
              </span>
              <input
                className="ic-input"
                placeholder={t("settings.followProviderDefault")}
                value={prefs.defaultModels?.[kind] ?? ""}
                onChange={(e) =>
                  update({
                    defaultModels: {
                      ...(prefs.defaultModels ?? {}),
                      [kind]: e.target.value,
                    },
                  })
                }
              />
            </label>
          ))}
        </div>
      </section>

      <section className="ic-card" style={{ padding: 16, marginBottom: 16 }}>
        <h2 style={{ fontSize: 15, marginTop: 0 }}>
          {t("settings.generation")}
        </h2>
        <div style={{ display: "grid", gap: 10 }}>
          <label>
            <span className="ic-dim" style={{ fontSize: 12 }}>
              {t("settings.defaultImageCount")}
            </span>
            <input
              className="ic-input"
              type="number"
              min={1}
              max={10}
              value={prefs.generation?.imageCount ?? 1}
              onChange={(e) =>
                update({
                  generation: {
                    ...(prefs.generation ?? {}),
                    imageCount: Number(e.target.value) || 1,
                  },
                })
              }
            />
          </label>
          <div
            style={{
              display: "grid",
              gridTemplateColumns: "repeat(auto-fit, minmax(140px, 1fr))",
              gap: 10,
            }}
          >
            <label>
              <span className="ic-dim" style={{ fontSize: 12 }}>
                {t("settings.audioVoice")}
              </span>
              <input
                className="ic-input"
                value={prefs.generation?.audioVoice ?? ""}
                onChange={(e) =>
                  update({
                    generation: {
                      ...(prefs.generation ?? {}),
                      audioVoice: e.target.value,
                    },
                  })
                }
              />
            </label>
            <label>
              <span className="ic-dim" style={{ fontSize: 12 }}>
                {t("settings.audioFormat")}
              </span>
              <input
                className="ic-input"
                value={prefs.generation?.audioFormat ?? ""}
                onChange={(e) =>
                  update({
                    generation: {
                      ...(prefs.generation ?? {}),
                      audioFormat: e.target.value,
                    },
                  })
                }
              />
            </label>
            <label>
              <span className="ic-dim" style={{ fontSize: 12 }}>
                {t("settings.audioSpeed")}
              </span>
              <input
                className="ic-input"
                value={prefs.generation?.audioSpeed ?? ""}
                onChange={(e) =>
                  update({
                    generation: {
                      ...(prefs.generation ?? {}),
                      audioSpeed: e.target.value,
                    },
                  })
                }
              />
            </label>
          </div>
          <label>
            <span className="ic-dim" style={{ fontSize: 12 }}>
              {t("settings.systemPrompt")}
            </span>
            <textarea
              className="ic-input"
              style={{ minHeight: 70, fontFamily: "inherit" }}
              value={systemPrompt}
              onChange={(e) => setSystemPrompt(e.target.value)}
              onBlur={() =>
                update({
                  generation: { ...(prefs.generation ?? {}), systemPrompt },
                })
              }
            />
            <span className="ic-dim" style={{ fontSize: 11 }}>
              {t("settings.systemPromptHint")}
            </span>
          </label>
        </div>
      </section>

      <section className="ic-card" style={{ padding: 16 }}>
        <h2 style={{ fontSize: 15, marginTop: 0 }}>
          {t("settings.extraCredentials")}
        </h2>
        <p className="ic-dim" style={{ fontSize: 12, marginTop: 0 }}>
          {t("settings.extraCredentialsHint")}
        </p>
        {Object.keys(secrets).length === 0 ? (
          <div className="ic-dim" style={{ fontSize: 12 }}>
            {t("common.empty")}
          </div>
        ) : (
          <ul style={{ listStyle: "none", padding: 0, margin: 0 }}>
            {Object.entries(secrets).map(([key, masked]) => (
              <li
                key={key}
                className="ic-mono"
                style={{ fontSize: 12, display: "flex", gap: 8 }}
              >
                <span style={{ flex: 1 }}>{key}</span>
                <span className="ic-dim">{masked}</span>
              </li>
            ))}
          </ul>
        )}
        {saving && (
          <p className="ic-dim" style={{ fontSize: 11 }}>
            {t("common.loading")}
          </p>
        )}
        {error && (
          <p className="ic-error" style={{ fontSize: 12 }}>
            {t(`errors.${error}`)}
          </p>
        )}
      </section>
    </>
  );
}
