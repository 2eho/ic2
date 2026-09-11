import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api } from "@/shared/api";
import type { TFn } from "@/app/App";

/** 首页：版本信息 + 提示词展示墙 + 快速入口。 */
export function HomePage({ t }: { t: TFn }) {
  const meta = useQuery({ queryKey: ["meta"], queryFn: api.meta });
  const prompts = useQuery({
    queryKey: ["prompts", "home"],
    queryFn: () => api.searchPrompts("", "", [], 12),
    retry: false,
  });

  return (
    <div style={{ padding: 24, maxWidth: 1200, margin: "0 auto" }}>
      <section style={{ marginBottom: 28 }}>
        <h1 style={{ margin: "0 0 6px", fontSize: 24 }}>
          {t("common.appTagline")}
        </h1>
        <p className="ic-dim" style={{ margin: 0 }}>
          {t("common.version")} {meta.data?.build.version ?? "-"} ·{" "}
          {meta.data?.build.mode ?? "-"}
        </p>
      </section>

      <section
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(auto-fit, minmax(200px, 1fr))",
          gap: 12,
          marginBottom: 32,
        }}
      >
        {[
          {
            to: "/projects",
            title: t("nav.canvases"),
            hint: t("canvas.newCanvas"),
          },
          {
            to: "/workbench/image",
            title: t("workbench.image"),
            hint: t("workbench.generate"),
          },
          {
            to: "/workbench/video",
            title: t("workbench.video"),
            hint: t("workbench.generate"),
          },
          {
            to: "/prompts",
            title: t("prompts.title"),
            hint: t("prompts.searchPlaceholder"),
          },
        ].map((c) => (
          <Link
            key={c.to}
            to={c.to}
            className="ic-card"
            style={{ padding: 16, textDecoration: "none", color: "inherit" }}
          >
            <strong>{c.title}</strong>
            <div className="ic-dim" style={{ fontSize: 12, marginTop: 4 }}>
              {c.hint}
            </div>
          </Link>
        ))}
      </section>

      <section>
        <h2 style={{ fontSize: 16 }}>{t("prompts.title")}</h2>
        {prompts.data && prompts.data.items.length > 0 ? (
          <div
            style={{
              display: "grid",
              gridTemplateColumns: "repeat(auto-fill, minmax(200px, 1fr))",
              gap: 12,
            }}
          >
            {prompts.data.items.map((p) => (
              <div key={p.id} className="ic-card" style={{ padding: 12 }}>
                {p.coverUrl && (
                  <img
                    src={p.coverUrl}
                    alt=""
                    style={{ width: "100%", borderRadius: 6, marginBottom: 8 }}
                  />
                )}
                <strong style={{ fontSize: 13 }}>{p.title}</strong>
                <p
                  className="ic-dim"
                  style={{ fontSize: 12, margin: "6px 0 0" }}
                >
                  {p.content.slice(0, 60)}
                </p>
              </div>
            ))}
          </div>
        ) : (
          <div className="ic-empty">{t("common.empty")}</div>
        )}
      </section>
    </div>
  );
}
