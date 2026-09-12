import { Link } from "react-router-dom";
import type { TFn } from "./App";

export function NotFoundPage({ t }: { t: TFn }) {
  return (
    <div className="ic-empty">
      <h2>404</h2>
      <p>{t("nav.notFound")}</p>
      <Link className="ic-btn" to="/">
        {t("common.back")}
      </Link>
    </div>
  );
}
