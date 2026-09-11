import { useState } from "react";
import { useSession } from "@/app/useSession";
import type { TFn } from "@/app/App";

/** 登录/注册页。凭据只提交一次，服务端加密存储；前端永不接触模型密钥。 */
export function LoginPage({ t }: { t: TFn }) {
  const { login, register } = useSession();
  const [mode, setMode] = useState<"login" | "register">("login");
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [errorKey, setErrorKey] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setErrorKey(null);
    try {
      if (mode === "login") await login(email, password);
      else await register(email, name, password);
    } catch (err) {
      const code = (err as { code?: string }).code ?? "internal";
      setErrorKey(`errors.${code}`);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div
      style={{
        display: "grid",
        placeItems: "center",
        minHeight: "100%",
        padding: 24,
      }}
    >
      <form
        className="ic-card"
        style={{ width: 380, padding: 24 }}
        onSubmit={submit}
      >
        <h1 style={{ margin: "0 0 4px", fontSize: 20 }}>
          {t("common.appName")}
        </h1>
        <p className="ic-dim" style={{ margin: "0 0 20px", fontSize: 13 }}>
          {t("auth.loginHint")}
        </p>

        <label style={{ display: "block", marginBottom: 12 }}>
          <span className="ic-dim" style={{ fontSize: 12 }}>
            {t("auth.email")}
          </span>
          <input
            className="ic-input"
            type="email"
            required
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
        </label>

        {mode === "register" && (
          <label style={{ display: "block", marginBottom: 12 }}>
            <span className="ic-dim" style={{ fontSize: 12 }}>
              {t("auth.name")}
            </span>
            <input
              className="ic-input"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </label>
        )}

        <label style={{ display: "block", marginBottom: 16 }}>
          <span className="ic-dim" style={{ fontSize: 12 }}>
            {t("auth.password")}
          </span>
          <input
            className="ic-input"
            type="password"
            required
            minLength={8}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
          <span className="ic-dim" style={{ fontSize: 12 }}>
            {t("auth.passwordRule")}
          </span>
        </label>

        {errorKey && (
          <p className="ic-error" style={{ marginTop: 0 }}>
            {t(errorKey)}
          </p>
        )}

        <button
          className="ic-btn ic-btn--primary"
          style={{ width: "100%", justifyContent: "center" }}
          disabled={busy}
        >
          {mode === "login" ? t("auth.login") : t("auth.register")}
        </button>

        <p
          className="ic-dim"
          style={{ fontSize: 13, marginBottom: 0, textAlign: "center" }}
        >
          {mode === "login" ? t("auth.noAccount") : t("auth.hasAccount")}{" "}
          <button
            type="button"
            className="ic-btn ic-btn--ghost"
            style={{ padding: "2px 4px" }}
            onClick={() => setMode(mode === "login" ? "register" : "login")}
          >
            {mode === "login" ? t("auth.register") : t("auth.login")}
          </button>
        </p>
      </form>
    </div>
  );
}
