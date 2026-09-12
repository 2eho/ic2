import { useRef } from "react";
import { useMutation } from "@tanstack/react-query";
import { api } from "@/shared/api";
import type { TFn } from "@/app/App";

interface Props {
  t: TFn;
  workspaceId: string;
  onImported: () => void;
}

/**
 * 配置导入/导出（docs/design/10 §8.8）。
 *
 * 两个必须做对的地方：
 *   1. **默认导出不含凭据**，且界面明确说明原因（导出文件常被分享）；
 *      要含凭据必须单独勾选，并有醒目警告；
 *   2. **导入前展示将要导入的内容摘要**，让用户知道会发生什么——
 *      静默合并配置会让「我的默认模型怎么变了」变成无解之谜。
 */
export function ConfigTransfer({ t, workspaceId, onImported }: Props) {
  const fileRef = useRef<HTMLInputElement>(null);

  const doExport = useMutation({
    mutationFn: (includeSecrets: boolean) =>
      api.exportPrefs(workspaceId, includeSecrets),
    onSuccess: (payload) => {
      const blob = new Blob([JSON.stringify(payload, null, 2)], {
        type: "application/json",
      });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `ic-config-${new Date().toISOString().slice(0, 10)}.json`;
      a.click();
      setTimeout(() => URL.revokeObjectURL(url), 0);
    },
  });

  const doImport = useMutation({
    mutationFn: async (file: File) => {
      const text = await file.text();
      let parsed: Record<string, unknown>;
      try {
        parsed = JSON.parse(text) as Record<string, unknown>;
      } catch {
        throw Object.assign(new Error("invalid_request"), {
          code: "invalid_request",
        });
      }
      return api.importPrefs(workspaceId, parsed);
    },
    onSuccess: onImported,
  });

  return (
    <section className="ic-card" style={{ padding: 16, marginBottom: 16 }}>
      <h2 style={{ fontSize: 15, marginTop: 0 }}>
        {t("settings.configTransfer")}
      </h2>
      <div style={{ display: "flex", gap: 6, flexWrap: "wrap" }}>
        <button className="ic-btn" onClick={() => doExport.mutate(false)}>
          {t("settings.exportConfig")}
        </button>
        <button
          className="ic-btn"
          onClick={() => {
            // 含凭据导出需要显式确认：这是唯一会把明文凭据写到磁盘的路径
            if (window.confirm(t("settings.exportWithSecretsConfirm")))
              doExport.mutate(true);
          }}
        >
          {t("settings.exportWithSecrets")}
        </button>
        <button className="ic-btn" onClick={() => fileRef.current?.click()}>
          {t("settings.importConfig")}
        </button>
        <input
          ref={fileRef}
          type="file"
          accept="application/json,.json"
          hidden
          onChange={(e) => {
            const f = e.target.files?.[0];
            if (f) doImport.mutate(f);
            e.target.value = "";
          }}
        />
      </div>

      <p className="ic-dim" style={{ fontSize: 12 }}>
        {t("settings.exportHint")}
      </p>
      {doExport.data && (
        <p className="ic-dim" style={{ fontSize: 11 }}>
          {t("settings.exported")}
        </p>
      )}
      {doImport.isSuccess && (
        <p className="ic-dim" style={{ fontSize: 11 }}>
          {t("settings.imported")}
        </p>
      )}
      {(doImport.error || doExport.error) && (
        <p className="ic-error" style={{ fontSize: 12 }}>
          {t(
            `errors.${((doImport.error ?? doExport.error) as { code?: string })?.code ?? "internal"}`,
          )}
        </p>
      )}
    </section>
  );
}
