import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/shared/api";
import { useWorkspace } from "./useWorkspace";
import { PluginManager } from "@/features/plugins/PluginManager";
import { PrefsPanel } from "./PrefsPanel";
import { ConfigTransfer } from "./ConfigTransfer";
import { DirectConnectPanel } from "./DirectConnectPanel";
import { ScriptEditor } from "./ScriptEditor";
import { usePrefs } from "./usePrefs";
import { ModelSelectModal } from "./ModelSelectModal";
import type { TFn } from "@/app/App";

/** 配置中心：渠道/凭据/模型/提示词来源/插件/边界可见性。 */
export function SettingsPage({
  t,
  theme = "light",
  locale = "zh-CN",
  onThemeChange = () => undefined,
  onLocaleChange = () => undefined,
}: {
  t: TFn;
  theme?: "light" | "dark";
  locale?: "zh-CN" | "en-US";
  onThemeChange?: (v: "light" | "dark") => void;
  onLocaleChange?: (v: "zh-CN" | "en-US") => void;
}) {
  const { workspaceId, ready } = useWorkspace();
  const qc = useQueryClient();
  // 偏好在这里读一次，传给需要它的子面板（避免每个面板各发一次请求）
  const {
    prefs,
    update: updatePrefs,
  } = usePrefs(workspaceId, ready);
  const meta = useQuery({ queryKey: ["meta"], queryFn: api.meta });
  const providers = useQuery({
    queryKey: ["providers", workspaceId],
    queryFn: () => api.listProviders(workspaceId),
    enabled: ready,
  });
  const sources = useQuery({
    queryKey: ["promptSources", workspaceId],
    queryFn: () => api.listPromptSources(workspaceId),
    enabled: ready,
  });

  const [provider, setProvider] = useState({
    id: "openai",
    name: "OpenAI",
    baseUrl: "https://api.openai.com",
    authKind: "bearer",
  });
  const [secret, setSecret] = useState("");
  const [credName, setCredName] = useState("默认");
  const [created, setCreated] = useState<{ masked: string } | null>(null);
  const [modelPickerFor, setModelPickerFor] = useState<string | null>(null);
  const [pickedModels, setPickedModels] = useState<Record<string, string[]>>(
    {},
  );

  const createCred = useMutation({
    mutationFn: async () => {
      // 渠道不存在时先创建（幂等：同名会返回已有）
      await api
        .createProvider(workspaceId, {
          id: provider.id,
          name: provider.name,
          kind: "builtin",
          baseUrl: provider.baseUrl,
          authKind: provider.authKind,
          capabilities: [
            "image.generate",
            "image.edit",
            "text.generate",
            "video.generate",
            "audio.generate",
            "model.list",
          ],
        })
        .catch(() => undefined);
      return api.createCredential(workspaceId, provider.id, {
        name: credName,
        secret,
        priority: 0,
      });
    },
    onSuccess: (c) => {
      setCreated({ masked: c.masked });
      setSecret("");
      void qc.invalidateQueries({ queryKey: ["providers", workspaceId] });
    },
  });

  const [testResult, setTestResult] = useState<{
    providerId: string;
    ok: boolean;
    latencyMs: number;
    modelCount: number;
    code?: string;
  } | null>(null);

  const testProvider = useMutation({
    mutationFn: (pid: string) => api.testProvider(workspaceId, pid),
    onSuccess: (result, pid) =>
      setTestResult({
        providerId: pid,
        ok: result.ok,
        latencyMs: result.latencyMs,
        modelCount: result.models?.length ?? 0,
        ...(result.error?.code ? { code: result.error.code } : {}),
      }),
  });

  const syncSource = useMutation({
    mutationFn: (sid: string) => api.syncPromptSource(workspaceId, sid),
    onSuccess: () =>
      qc.invalidateQueries({ queryKey: ["promptSources", workspaceId] }),
  });

  return (
    <div style={{ padding: 24, maxWidth: 900, margin: "0 auto" }}>
      <h1 style={{ fontSize: 20, marginTop: 0 }}>{t("settings.title")}</h1>

      {/* 偏好（服务端权威）+ URL 参数导入凭据 */}
      <PrefsPanel
        t={t}
        workspaceId={workspaceId}
        ready={ready}
        theme={theme}
        locale={locale}
        onThemeChange={onThemeChange}
        onLocaleChange={onLocaleChange}
      />

      {/* 本地直连模式（4.21）：默认关闭、必须显式确认风险、范围可选 */}
      <DirectConnectPanel t={t} prefs={prefs} update={updatePrefs} />

      <ScriptEditorSection
        t={t}
        workspaceId={workspaceId}
        providerId={provider.id}
        onSaved={() =>
          qc.invalidateQueries({ queryKey: ["providers", workspaceId] })
        }
      />

      {/* 渠道与凭据：密钥只提交一次，服务端加密，界面只显示掩码（INV-5） */}
      <section className="ic-card" style={{ padding: 16, marginBottom: 16 }}>
        <h2 style={{ fontSize: 15, marginTop: 0 }}>
          {t("settings.providers")}
        </h2>
        <div
          style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 10 }}
        >
          <label>
            <span className="ic-dim">{t("settings.providerName")}</span>
            <input
              className="ic-input"
              value={provider.name}
              onChange={(e) =>
                setProvider({
                  ...provider,
                  name: e.target.value,
                  id: e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, ""),
                })
              }
            />
          </label>
          <label>
            <span className="ic-dim">{t("settings.protocol")}</span>
            <select
              className="ic-select"
              value={provider.id}
              onChange={(e) => setProvider({ ...provider, id: e.target.value })}
            >
              <option value="openai">OpenAI 兼容</option>
              <option value="gemini">Gemini</option>
            </select>
          </label>
          <label>
            <span className="ic-dim">{t("settings.baseUrl")}</span>
            <input
              className="ic-input"
              value={provider.baseUrl}
              onChange={(e) =>
                setProvider({ ...provider, baseUrl: e.target.value })
              }
            />
          </label>
          <label>
            <span className="ic-dim">{t("settings.credentialName")}</span>
            <input
              className="ic-input"
              value={credName}
              onChange={(e) => setCredName(e.target.value)}
            />
          </label>
          <label style={{ gridColumn: "1 / -1" }}>
            <span className="ic-dim">{t("settings.apiKey")}</span>
            <input
              className="ic-input"
              type="password"
              value={secret}
              onChange={(e) => setSecret(e.target.value)}
            />
            <span className="ic-dim" style={{ fontSize: 12 }}>
              {t("settings.apiKeyHint")}
            </span>
          </label>
        </div>
        <button
          className="ic-btn ic-btn--primary"
          style={{ marginTop: 10 }}
          disabled={!secret || !ready}
          onClick={() => createCred.mutate()}
        >
          {t("common.save")}
        </button>
        {created && (
          <p className="ic-dim" style={{ fontSize: 12 }}>
            已保存：{created.masked}
          </p>
        )}
        {createCred.error && (
          <p className="ic-error">{t("errors.invalid_request")}</p>
        )}

        <ul style={{ listStyle: "none", padding: 0, marginTop: 12 }}>
          {(providers.data?.items ?? []).map((p) => (
            <li
              key={p.id}
              style={{
                fontSize: 13,
                padding: "4px 0",
                borderTop: "1px solid var(--ic-border)",
              }}
            >
              <div
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: 6,
                  flexWrap: "wrap",
                }}
              >
                <strong>{p.name}</strong>
                <span className="ic-dim ic-mono" style={{ fontSize: 11 }}>
                  {p.baseUrl}
                </span>
                <div style={{ flex: 1 }} />
                <button
                  className="ic-btn"
                  style={{ fontSize: 11, padding: "3px 8px" }}
                  onClick={() => setModelPickerFor(p.id)}
                >
                  {t("settings.models")}
                </button>
                <button
                  className="ic-btn"
                  style={{ fontSize: 11, padding: "3px 8px" }}
                  onClick={() => testProvider.mutate(p.id)}
                >
                  {t("settings.testConnection")}
                </button>
              </div>
              <div className="ic-dim" style={{ fontSize: 11 }}>
                {p.capabilities.join(", ")}
              </div>
              {testResult?.providerId === p.id && (
                <div className="ic-dim" style={{ fontSize: 11 }}>
                  {testResult.ok
                    ? `${t("settings.testOk")} · ${testResult.latencyMs}ms · ${testResult.modelCount} ${t("settings.models")}`
                    : t(`errors.${testResult.code ?? "internal"}`)}
                </div>
              )}
            </li>
          ))}
        </ul>
      </section>

      {/* 提示词来源 */}
      <section className="ic-card" style={{ padding: 16, marginBottom: 16 }}>
        <h2 style={{ fontSize: 15, marginTop: 0 }}>
          {t("settings.promptSources")}
        </h2>
        {(sources.data?.items ?? []).length === 0 && (
          <div className="ic-empty" style={{ padding: 16 }}>
            {t("common.empty")}
          </div>
        )}
        {(sources.data?.items ?? []).map((s) => (
          <div
            key={s.id}
            style={{
              display: "flex",
              gap: 8,
              alignItems: "center",
              fontSize: 13,
              padding: "6px 0",
              borderTop: "1px solid var(--ic-border)",
            }}
          >
            <strong>{s.name}</strong>
            <span className="ic-dim ic-mono" style={{ fontSize: 11 }}>
              {s.url}
            </span>
            <span className="ic-badge">{s.count}</span>
            <button
              className="ic-btn"
              style={{ fontSize: 11, padding: "3px 8px" }}
              onClick={() => syncSource.mutate(s.id)}
            >
              {t("settings.syncNow")}
            </button>
          </div>
        ))}
      </section>

      {/* 配置导入导出（默认不含凭据） */}
      <ConfigTransfer
        t={t}
        workspaceId={workspaceId}
        onImported={() => void qc.invalidateQueries()}
      />

      {/* 配置导入导出（默认不含凭据） */}
      <ConfigTransfer
        t={t}
        workspaceId={workspaceId}
        onImported={() => void qc.invalidateQueries()}
      />

      <PluginManager t={t} workspaceId={workspaceId} ready={ready} />

      {modelPickerFor && (
        <ModelSelectModal
          t={t}
          workspaceId={workspaceId}
          providerId={modelPickerFor}
          selected={pickedModels[modelPickerFor] ?? []}
          onClose={() => setModelPickerFor(null)}
          onSaved={(models) => {
            setPickedModels((prev) => ({ ...prev, [modelPickerFor]: models }));
            setModelPickerFor(null);
            void qc.invalidateQueries({ queryKey: ["models", workspaceId] });
          }}
        />
      )}

      {modelPickerFor && (
        <ModelSelectModal
          t={t}
          workspaceId={workspaceId}
          providerId={modelPickerFor}
          selected={pickedModels[modelPickerFor] ?? []}
          onClose={() => setModelPickerFor(null)}
          onSaved={(models) => {
            setPickedModels((prev) => ({ ...prev, [modelPickerFor]: models }));
            setModelPickerFor(null);
            void qc.invalidateQueries({ queryKey: ["models", workspaceId] });
          }}
        />
      )}

      {/* 边界可见性：不做静默的经验值（对齐 05 §3.2 与 11 §2.9） */}
      <section className="ic-card" style={{ padding: 16 }}>
        <h2 style={{ fontSize: 15, marginTop: 0 }}>{t("settings.limits")}</h2>
        <pre
          className="ic-mono"
          style={{
            background: "var(--ic-surface-2)",
            padding: 12,
            borderRadius: 8,
            overflow: "auto",
            margin: 0,
          }}
        >
          {JSON.stringify(meta.data?.limits ?? {}, null, 2)}
        </pre>
        <p className="ic-dim" style={{ fontSize: 12 }}>
          {t("common.version")}: {meta.data?.build.version} · commit{" "}
          {meta.data?.build.commit}
        </p>
      </section>
    </div>
  );
}


/**
 * 自定义调用脚本入口（4.14）。
 *
 * 单独一个小节而不是塞进渠道表单：脚本是「协议映射」，
 * 与「地址 + 密钥」是两种不同的关注点，混在一起会让表单长到没人愿意读。
 */
function ScriptEditorSection({
  t,
  workspaceId,
  providerId,
  onSaved,
}: {
  t: TFn;
  workspaceId: string;
  providerId: string;
  onSaved: () => void;
}) {
  const [open, setOpen] = useState(false);
  return (
    <section className="ic-card" style={{ padding: 16, marginBottom: 16 }}>
      <h2 style={{ fontSize: 15, marginTop: 0 }}>
        {t("settings.scriptEditor")}
      </h2>
      <p className="ic-dim" style={{ fontSize: 12, marginTop: 0 }}>
        {t("settings.scriptEditorHint")}
      </p>
      <button className="ic-btn" onClick={() => setOpen(true)}>
        {t("settings.scriptOpen")}
      </button>
      {open && (
        <ScriptEditor
          t={t}
          workspaceId={workspaceId}
          providerId={providerId}
          onClose={() => setOpen(false)}
          onSaved={() => {
            setOpen(false);
            onSaved();
          }}
        />
      )}
    </section>
  );
}
