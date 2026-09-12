import { useMemo, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { api } from "@/shared/api";
import { guessCapabilities } from "./capability";
import type { TFn } from "@/app/App";

interface Props {
  t: TFn;
  workspaceId: string;
  providerId: string;
  selected: string[];
  onClose: () => void;
  onSaved: (models: string[]) => void;
}

/**
 * 模型选择器（对齐 docs/design/10 §8.2）。
 *
 * 三个刻意的行为：
 *   1. **已有 / 新获取分栏**：用户点「拉取模型列表」后会多出一批候选，
 *      如果混在一起，他会分不清哪些是刚拉的、哪些是原来勾的；
 *   2. **能力标注可覆盖**：关键词猜测（guessCapabilities）只是默认值，
 *      用户可以改——猜错不该导致「不能选」，只该导致「默认勾选错」；
 *   3. **保存前不落库**：勾选过程是本地状态，避免用户试选一堆导致
 *      几次无意义的写入（也避免在配置页产生大量审计记录）。
 */
export function ModelSelectModal({
  t,
  workspaceId,
  providerId,
  selected,
  onClose,
  onSaved,
}: Props) {
  const [picked, setPicked] = useState<string[]>(selected);
  const [manual, setManual] = useState("");

  const existing = useQuery({
    queryKey: ["models", workspaceId, providerId],
    queryFn: () => api.listModels(workspaceId, ""),
    retry: false,
  });

  const fetched = useQuery({
    queryKey: ["probe", workspaceId, providerId],
    queryFn: () => api.testProvider(workspaceId, providerId),
    retry: false,
  });

  const existingIds = useMemo(
    () =>
      (existing.data?.items ?? [])
        .filter((m) => m.providerId === providerId)
        .map((m) => m.id),
    [existing.data, providerId],
  );
  const fetchedIds = useMemo(() => fetched.data?.models ?? [], [fetched.data]);

  // 「新增」= 探针返回但本地还没有的；「已有」= 本地已保存的
  const newlyFetched = fetchedIds.filter((id) => !existingIds.includes(id));

  const save = useMutation({
    mutationFn: async () => {
      // 保存时把「猜测的能力」一并提交：服务端保存的是**显式能力**，
      // 这样即使将来关键词表变了，已保存的模型能力也不会漂移。
      await api.saveModels(
        workspaceId,
        providerId,
        picked.map((id) => ({ id, capabilities: guessCapabilities(id) })),
      );
      return picked;
    },
    onSuccess: (models) => onSaved(models),
  });

  const toggle = (id: string) =>
    setPicked((prev) =>
      prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id],
    );

  const addManual = () => {
    const value = manual.trim();
    if (!value || picked.includes(value)) return;
    setPicked((prev) => [...prev, value]);
    setManual("");
  };

  return (
    <div
      style={{
        position: "fixed",
        inset: 0,
        background: "rgba(0,0,0,.35)",
        display: "grid",
        placeItems: "center",
        zIndex: 1000,
      }}
      onClick={onClose}
    >
      <div
        className="ic-card"
        style={{ width: 520, maxHeight: "82vh", overflow: "auto", padding: 16 }}
        onClick={(e) => e.stopPropagation()}
      >
        <div
          style={{ display: "flex", alignItems: "center", marginBottom: 10 }}
        >
          <strong style={{ flex: 1 }}>{t("settings.models")}</strong>
          <button className="ic-btn ic-btn--ghost" onClick={onClose}>
            ✕
          </button>
        </div>

        {/* 手动增加：上游列表常常不全（自建中转），必须允许手填 */}
        <div style={{ display: "flex", gap: 6, marginBottom: 10 }}>
          <input
            className="ic-input"
            placeholder={t("settings.manualModel")}
            value={manual}
            onChange={(e) => setManual(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && addManual()}
          />
          <button className="ic-btn" onClick={addManual}>
            {t("common.confirm")}
          </button>
        </div>

        <Section
          title={`${t("settings.selectedModels")} (${picked.length})`}
          hint={picked.length > 0 ? t("settings.capabilityHint") : undefined}
        >
          {picked.length === 0 && (
            <div className="ic-dim" style={{ fontSize: 12 }}>
              {t("common.empty")}
            </div>
          )}
          {picked.map((id) => (
            <Row key={id} id={id} checked onToggle={() => toggle(id)} />
          ))}
        </Section>

        <Section
          title={`${t("settings.newlyFetched")} (${newlyFetched.length})`}
          action={
            newlyFetched.length > 0 && (
              <button
                className="ic-btn"
                style={{ fontSize: 11 }}
                onClick={() =>
                  setPicked((prev) => [...new Set([...prev, ...newlyFetched])])
                }
              >
                {t("settings.selectAll")}
              </button>
            )
          }
        >
          {fetched.isFetching && (
            <div className="ic-dim" style={{ fontSize: 12 }}>
              {t("common.loading")}
            </div>
          )}
          {!fetched.isFetching && newlyFetched.length === 0 && (
            <div className="ic-dim" style={{ fontSize: 12 }}>
              {fetched.data?.ok === false
                ? t(`errors.${fetched.data.error?.code ?? "internal"}`)
                : t("settings.noNewModels")}
            </div>
          )}
          {newlyFetched.map((id) => (
            <Row key={id} id={id} checked={false} onToggle={() => toggle(id)} />
          ))}
        </Section>

        <div style={{ display: "flex", gap: 6, marginTop: 12 }}>
          <button
            className="ic-btn ic-btn--primary"
            disabled={save.isPending}
            onClick={() => save.mutate()}
          >
            {t("common.save")}
          </button>
          <button className="ic-btn" onClick={onClose}>
            {t("common.cancel")}
          </button>
        </div>
      </div>
    </div>
  );
}

function Section({
  title,
  hint,
  action,
  children,
}: {
  title: string;
  hint?: string;
  action?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <section style={{ marginBottom: 12 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
        <span style={{ fontSize: 12, fontWeight: 600 }}>{title}</span>
        <div style={{ flex: 1 }} />
        {action}
      </div>
      {hint && (
        <p className="ic-dim" style={{ fontSize: 11, margin: "2px 0 4px" }}>
          {hint}
        </p>
      )}
      <div style={{ display: "grid", gap: 4, marginTop: 4 }}>{children}</div>
    </section>
  );
}

function Row({
  id,
  checked,
  onToggle,
}: {
  id: string;
  checked: boolean;
  onToggle: () => void;
}) {
  return (
    <label
      style={{ display: "flex", alignItems: "center", gap: 6, fontSize: 12 }}
    >
      <input type="checkbox" checked={checked} onChange={onToggle} />
      <code className="ic-mono" style={{ flex: 1 }}>
        {id}
      </code>
    </label>
  );
}
