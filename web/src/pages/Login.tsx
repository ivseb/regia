import { Select } from "../select";
import { Suspense, lazy, useEffect, useId, useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ssoApi } from "../api";
import { mfaApi, needsMfa } from "../adminApi";
import { ErrorNote } from "../ui";
import { useT, useLang } from "../i18n/react";
import { Logo, SourceLink } from "../brand";
import { LANGUAGES, isLang } from "../i18n/languages";
import { setLang, type Key } from "../i18n/core";

// Secondo passo del login con MFA (piano, passo 15): si carica solo quando serve.
const MfaLoginStep = lazy(() => import("./Mfa").then((m) => ({ default: m.MfaLoginStep })));

// Esiti dell'accesso unico: il backend torna a /?sso_error=codice.
const SSO_ERRORS: Record<string, Key> = {
  unknown_account: "auth.sso.unknown_account",
  email_unverified: "auth.sso.email_unverified",
  already_linked: "auth.sso.already_linked",
  state: "auth.sso.state",
  invalid: "auth.sso.invalid",
  unavailable: "auth.sso.unavailable",
  denied: "auth.sso.denied",
  failed: "auth.sso.failed",
};

/** Legge (una volta) l'esito dell'accesso unico dall'indirizzo e lo toglie dalla barra. */
function useSsoError(): string | null {
  const [code] = useState(() => new URLSearchParams(window.location.search).get("sso_error"));
  useEffect(() => {
    if (!code) return;
    const u = new URL(window.location.href);
    u.searchParams.delete("sso_error");
    window.history.replaceState(null, "", u.pathname + u.search + u.hash);
  }, [code]);
  return code;
}

/** Scelta della lingua prima dell'accesso: resta nel browser, poi vale quella del profilo. */
function LanguagePicker() {
  const t = useT();
  const lang = useLang();
  const id = useId();
  return (
    <div className="flex items-center gap-2 self-end text-[12px] text-muted">
      <label htmlFor={id}>{t("auth.login.language")}</label>
      <Select id={id} className="field w-auto py-1" value={lang} onChange={(e) => isLang(e.target.value) && void setLang(e.target.value)}>
        {LANGUAGES.map((l) => (
          <option key={l.key} value={l.key} lang={l.key}>{l.name}</option>
        ))}
      </Select>
    </div>
  );
}

/** Accesso: email e password, oppure accesso unico (SSO); con l'MFA un secondo passo. */
export function Login() {
  const t = useT();
  const qc = useQueryClient();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [emergency, setEmergency] = useState(false);
  const sso = useQuery({ queryKey: ["oidc-config"], queryFn: ssoApi.oidcConfig, staleTime: Infinity, retry: false });
  const ssoError = useSsoError();
  // Secondo fattore (piano, passo 15): con l'MFA la password apre solo il secondo passo.
  const [mfa, setMfa] = useState<{ setup: boolean } | null>(null);
  const login = useMutation({
    mutationFn: () => mfaApi.login(email, password),
    onSuccess: (r) => {
      if (needsMfa(r)) {
        setPassword("");
        setMfa({ setup: r.mfa_setup });
      } else qc.setQueryData(["me"], r);
    },
  });
  function submit(e: FormEvent) {
    e.preventDefault();
    login.mutate();
  }
  // Dopo l'accesso unico si torna alla pagina richiesta (senza il parametro dell'errore).
  const here = new URL(window.location.href);
  here.searchParams.delete("sso_error");
  const next = here.pathname + here.search;
  const ssoName = sso.data?.name || "SSO";
  const ssoOn = !!sso.data?.enabled;
  const passwordOn = sso.data ? sso.data.password_login : !sso.isPending;
  const showForm = passwordOn || emergency;
  return (
    <div className="grid min-h-screen grid-cols-1 md:grid-cols-[minmax(0,1fr)_480px]">
      <section className="flex flex-col justify-between bg-fg p-10 text-bg">
        <span className="flex items-center gap-2.5 text-[15px] font-bold"><Logo inverse />Regia</span>
        <div className="max-w-xl">
          <h1 className="m-0 text-[44px] leading-[1.02] font-bold tracking-tight">{t("auth.hero.title")}</h1>
          <p className="mt-4 max-w-md text-[15px] opacity-80">{t("auth.hero.text")}</p>
        </div>
        <span className="flex flex-col gap-1.5">
          <span className="font-mono text-[12px] opacity-70">DORA · NIS2 · ISO 27001 · GDPR · AgID</span>
          <SourceLink className="opacity-70" />
        </span>
      </section>
      {mfa ? (
        <div className="flex flex-col justify-center p-6 sm:p-10">
          <Suspense fallback={null}>
            <MfaLoginStep setup={mfa.setup} onDone={(me) => qc.setQueryData(["me"], me)} onCancel={() => setMfa(null)} />
          </Suspense>
        </div>
      ) : (
        <form onSubmit={submit} className="flex flex-col justify-center gap-5 p-6 sm:p-10">
          <LanguagePicker />
          <h2 className="m-0 border-b-2 border-rule pb-2 text-[22px] font-bold">{t("auth.login.title")}</h2>
          {ssoError && (
            <p role="alert" className="m-0 border-l-2 border-danger pl-3 text-danger">
              {t(SSO_ERRORS[ssoError] ?? SSO_ERRORS.failed, { name: ssoName })}
            </p>
          )}
          {ssoOn && (
            <a href={ssoApi.oidcStartUrl(next)} className="btn btn-primary justify-center py-3 text-[15px] no-underline">
              {t("auth.login.sso", { name: ssoName })}
            </a>
          )}
          {ssoOn && passwordOn && (
            <div className="flex items-center gap-3 text-[12px] text-muted" aria-hidden="true">
              <span className="h-px flex-1 bg-hair" />
              {t("auth.login.or")}
              <span className="h-px flex-1 bg-hair" />
            </div>
          )}
          {ssoOn && !passwordOn && !emergency && (
            <button type="button" className="self-start text-[12px] text-muted underline hover:text-fg" onClick={() => setEmergency(true)}>
              {t("auth.login.emergency")}
            </button>
          )}
          {showForm && (
            <>
              <label className="flex flex-col gap-1.5">
                <span className="label">{t("auth.login.email")}</span>
                <input className="field" type="email" required autoComplete="username" value={email} onChange={(e) => setEmail(e.target.value)} />
              </label>
              <label className="flex flex-col gap-1.5">
                <span className="label">{t("auth.login.password")}</span>
                <input className="field" type="password" required autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
              </label>
              <ErrorNote error={login.error} />
              <button className={`btn justify-center py-2.5 ${ssoOn ? "" : "btn-primary"}`} disabled={login.isPending}>
                {login.isPending ? t("auth.login.submitting") : ssoOn ? t("auth.login.withPassword") : t("auth.login.submit")}
              </button>
            </>
          )}
        </form>
      )}
    </div>
  );
}
