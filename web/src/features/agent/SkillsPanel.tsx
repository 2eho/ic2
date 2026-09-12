import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/shared/api";
import type { TFn } from "@/app/App";

/**
 * Skills 管理（对齐 docs/design/10 §9.14）。
 *
 * Skills 与工具的区别必须体现在界面上：
 *   - 工具是「Agent 能做的动作」，会改画布/花钱，因此需要审批；
 *   - Skill 是「给模型的指令」，不产生副作用，因此不需要审批，
 *     但它会进系统提示词，所以有长度上限。
 * 把两者混在一个列表里会让用户以为 Skill 也需要授权，从而不敢用。
 */
export function SkillsPanel({
  t,
  workspaceId,
  ready,
}: {
  t: TFn;
  workspaceId: string;
  ready: boolean;
}) {
  const qc = useQueryClient();
  const list = useQuery({
    queryKey: ["agentSkills", workspaceId],
    queryFn: () => api.listSkills(workspaceId),
    enabled: ready,
    retry: false,
  });

  const toggle = useMutation({
    mutationFn: ({
      name,
      enabled,
      instructions,
    }: {
      name: string;
      enabled: boolean;
      instructions: string;
    }) => api.saveSkill(workspaceId, { name, instructions, enabled }),
    onSuccess: () =>
      qc.invalidateQueries({ queryKey: ["agentSkills", workspaceId] }),
  });

  const items = list.data?.items ?? [];
  if (items.length === 0) {
    return (
      <div className="ic-empty" style={{ padding: 12, fontSize: 12 }}>
        {t("common.empty")}
      </div>
    );
  }
  return (
    <ul style={{ listStyle: "none", padding: 0, margin: 0 }}>
      {items.map((s) => (
        <li
          key={s.name}
          style={{
            fontSize: 12,
            padding: "6px 0",
            borderTop: "1px solid var(--ic-border)",
          }}
        >
          <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
            <strong>{s.name}</strong>
            {s.tags.slice(0, 3).map((tag) => (
              <span key={tag} className="ic-badge" style={{ fontSize: 10 }}>
                {tag}
              </span>
            ))}
            <div style={{ flex: 1 }} />
            <button
              className="ic-btn"
              style={{ fontSize: 10, padding: "2px 6px" }}
              onClick={() =>
                toggle.mutate({
                  name: s.name,
                  enabled: !s.enabled,
                  instructions: s.instructions,
                })
              }
            >
              {s.enabled ? t("settings.disable") : t("settings.enable")}
            </button>
          </div>
          {s.description && <div className="ic-dim">{s.description}</div>}
        </li>
      ))}
    </ul>
  );
}
