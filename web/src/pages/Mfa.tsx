// Autenticazione a due fattori con TOTP (piano, passo 15): sezione del profilo e secondo
// passo del login. Il QR arriva dal server come SVG (nessun servizio esterno) e si mostra
// come immagine data: (la CSP lo consente), sempre scuro su bianco perché le app lo leggano.
import { useEffect, useRef, useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { Me } from "../api";
import { mfaApi, type MfaSetup } from "../adminApi";
import { ErrorNote, SectionTitle } from "../ui";
import { CopyBox, dateTime } from "./adminUi";
import { useT } from "../i18n/react";
import { Trans } from "../i18n/Trans";

function QR({ setup }: { setup: MfaSetup }) {
  const t = useT();
  return (
    <div className="flex flex-col gap-3 sm:flex-row sm:items-start">
      <img
        src={`data:image/svg+xml;charset=utf-8,${encodeURIComponent(setup.qr_svg)}`}
        alt={t("auth.mfa.qrAlt")}
        width={196}
        height={196}
        className="shrink-0 border border-hair"
      />
      <div className="flex min-w-0 flex-col gap-1.5">
        <span>{t("auth.mfa.qrText")}</span>
        <code className="block select-all break-all font-mono text-[14px] tracking-wide">{setup.secret}</code>
        <span className="text-[12px] text-muted">{t("auth.mfa.qrType")}</span>
      </div>
    </div>
  );
}

function CodeField({ value, onChange, recovery, autoFocus }: { value: string; onChange: (v: string) => void; recovery?: boolean; autoFocus?: boolean }) {
  const t = useT();
  return (
    <label className="flex flex-col gap-1.5">
      <span className="label">{recovery ? t("auth.mfa.codeRecovery") : t("auth.mfa.codeDigits")}</span>
      <input
        className="field font-mono text-[16px] tracking-widest sm:max-w-[220px]"
        required
        autoFocus={autoFocus}
        autoComplete="one-time-code"
        inputMode={recovery ? "text" : "numeric"}
        maxLength={recovery ? 14 : 7}
        placeholder={recovery ? "xxxxx-xxxxx" : "123456"}
        value={value}
        onChange={(e) => onChange(e.target.value)}
      />
    </label>
  );
}

function RecoveryCodes({ codes, onDone }: { codes: string[]; onDone: () => void }) {
  const t = useT();
  return (
    <CopyBox
      title={t("auth.mfa.recoveryTitle")}
      value={codes.join("\n")}
      note={t("auth.mfa.recoveryNote")}
      onDone={onDone}
    />
  );
}

/** Sezione del profilo. */
export function MfaSection() {
  const t = useT();
  const qc = useQueryClient();
  const status = useQuery({ queryKey: ["mfa-status"], queryFn: mfaApi.status });
  const [setup, setSetup] = useState<MfaSetup | null>(null);
  const [code, setCode] = useState("");
  const [codes, setCodes] = useState<string[] | null>(null);
  const [action, setAction] = useState<"" | "disable" | "codes">("");
  const refresh = () => qc.invalidateQueries({ queryKey: ["mfa-status"] });
  const begin = useMutation({ mutationFn: mfaApi.setup, onSuccess: (s) => { setSetup(s); setCode(""); } });
  const enable = useMutation({
    mutationFn: () => mfaApi.enable(code),
    onSuccess: (r) => {
      setCodes(r.recovery_codes);
      setSetup(null);
      setCode("");
      refresh();
    },
  });
  const disable = useMutation({ mutationFn: () => mfaApi.disable(code), onSuccess: () => { setAction(""); setCode(""); refresh(); } });
  const regen = useMutation({ mutationFn: () => mfaApi.recoveryCodes(code), onSuccess: (r) => { setCodes(r.recovery_codes); setAction(""); setCode(""); refresh(); } });
  const s = status.data;

  function submit(e: FormEvent) {
    e.preventDefault();
    if (setup) enable.mutate();
    else if (action === "disable") disable.mutate();
    else if (action === "codes") regen.mutate();
  }

  return (
    <section className="flex flex-col gap-4">
      <SectionTitle aside={s?.enabled ? <span className="text-[12px] font-semibold text-ok">{t("auth.mfa.on")}</span> : undefined}>{t("auth.mfa.title")}</SectionTitle>
      <ErrorNote error={status.error} />
      {codes && <RecoveryCodes codes={codes} onDone={() => setCodes(null)} />}
      {s && !s.available && (
        <p className="m-0 text-muted">{t("auth.mfa.ssoNote")}</p>
      )}
      {s?.available && !s.enabled && !setup && (
        <div className="flex flex-col gap-3">
          <p className="m-0 max-w-[640px]">
            {t("auth.mfa.intro")}
            {s.required && <span className="font-semibold text-flag"> {t("auth.mfa.requiredByOrg")}</span>}
          </p>
          <div>
            <button className="btn btn-primary" disabled={begin.isPending} onClick={() => begin.mutate()}>{t("auth.mfa.activate")}</button>
          </div>
          <ErrorNote error={begin.error} />
        </div>
      )}
      {setup && (
        <form onSubmit={submit} className="flex flex-col gap-4">
          <QR setup={setup} />
          <CodeField value={code} onChange={setCode} autoFocus />
          <ErrorNote error={enable.error} />
          <div className="flex flex-wrap gap-2">
            <button className="btn btn-primary" disabled={enable.isPending || !code.trim()}>{t("auth.mfa.confirmActivate")}</button>
            <button type="button" className="btn" onClick={() => setSetup(null)}>{t("common.cancel")}</button>
          </div>
        </form>
      )}
      {s?.enabled && (
        <div className="flex flex-col gap-3">
          <p className="m-0">
            <Trans
              k="auth.mfa.enabledSince"
              params={{ date: dateTime(s.enabled_at) }}
              slots={{ left: <span className={s.recovery_codes_left < 3 ? "font-semibold text-flag" : "font-semibold"}>{s.recovery_codes_left}</span> }}
            />
            {s.required && <span className="text-muted"> {t("auth.mfa.requiredPolicy")}</span>}
          </p>
          {action === "" ? (
            <div className="flex flex-wrap gap-2">
              <button className="btn py-1.5" onClick={() => { setAction("codes"); setCode(""); }}>{t("auth.mfa.newCodes")}</button>
              <button
                className="btn btn-danger py-1.5"
                disabled={s.required}
                title={s.required ? t("auth.mfa.disableBlocked") : undefined}
                onClick={() => { setAction("disable"); setCode(""); }}
              >
                {t("auth.mfa.disable")}
              </button>
            </div>
          ) : (
            <form onSubmit={submit} className="flex flex-col gap-3">
              <p className="m-0 text-muted">
                {action === "disable" ? t("auth.mfa.disablePrompt") : t("auth.mfa.codesPrompt")}
              </p>
              <CodeField value={code} onChange={setCode} autoFocus />
              <ErrorNote error={action === "disable" ? disable.error : regen.error} />
              <div className="flex flex-wrap gap-2">
                <button className={`btn ${action === "disable" ? "btn-danger" : "btn-primary"}`} disabled={disable.isPending || regen.isPending || !code.trim()}>
                  {action === "disable" ? t("auth.mfa.disable") : t("auth.mfa.generate")}
                </button>
                <button type="button" className="btn" onClick={() => setAction("")}>{t("common.cancel")}</button>
              </div>
            </form>
          )}
        </div>
      )}
    </section>
  );
}

/** Secondo passo del login: codice (o codice di recupero), oppure attivazione obbligatoria. */
export function MfaLoginStep({ setup, onDone, onCancel }: { setup: boolean; onDone: (me: Me) => void; onCancel: () => void }) {
  const t = useT();
  const [code, setCode] = useState("");
  const [recovery, setRecovery] = useState(false);
  const [codes, setCodes] = useState<{ me: Me; list: string[] } | null>(null);
  const begin = useMutation({ mutationFn: mfaApi.challengeSetup });
  const verify = useMutation({ mutationFn: () => mfaApi.verify(code), onSuccess: onDone });
  const confirm = useMutation({ mutationFn: () => mfaApi.challengeConfirm(code), onSuccess: (r) => setCodes({ me: r.me, list: r.recovery_codes }) });
  const { mutate: startSetup } = begin;
  // Una sola richiesta: ogni chiamata genera un segreto nuovo e annulla il precedente.
  const started = useRef(false);
  useEffect(() => {
    if (setup && !started.current) {
      started.current = true;
      startSetup();
    }
  }, [setup, startSetup]);
  const expired = [verify.error, confirm.error, begin.error].some((e) => (e as { status?: number } | null)?.status === 401);

  function submit(e: FormEvent) {
    e.preventDefault();
    if (setup) confirm.mutate();
    else verify.mutate();
  }

  if (codes) {
    return (
      <div className="flex flex-col gap-5">
        <h2 className="m-0 border-b-2 border-rule pb-2 text-[22px] font-bold">{t("auth.mfa.stepDoneTitle")}</h2>
        <RecoveryCodes codes={codes.list} onDone={() => onDone(codes.me)} />
        <button className="btn btn-primary justify-center py-2.5" onClick={() => onDone(codes.me)}>{t("auth.mfa.continue")}</button>
      </div>
    );
  }
  return (
    <form onSubmit={submit} className="flex flex-col gap-5">
      <h2 className="m-0 border-b-2 border-rule pb-2 text-[22px] font-bold">{setup ? t("auth.mfa.stepSetupTitle") : t("auth.mfa.stepTitle")}</h2>
      {setup ? (
        <>
          <p className="m-0">{t("auth.mfa.stepSetupText")}</p>
          {begin.data && <QR setup={begin.data} />}
        </>
      ) : (
        <p className="m-0">{recovery ? t("auth.mfa.stepRecoveryPrompt") : t("auth.mfa.stepAppPrompt")}</p>
      )}
      {expired ? (
        <p role="alert" className="m-0 border-l-2 border-danger pl-3 text-danger">{t("auth.mfa.expired")}</p>
      ) : (
        <>
          <CodeField value={code} onChange={setCode} recovery={recovery} autoFocus />
          <ErrorNote error={verify.error ?? confirm.error ?? begin.error} />
          <button className="btn btn-primary justify-center py-2.5" disabled={verify.isPending || confirm.isPending || !code.trim() || (setup && !begin.data)}>
            {setup ? t("auth.mfa.confirmLogin") : t("auth.mfa.login")}
          </button>
        </>
      )}
      <div className="flex flex-wrap justify-between gap-3 text-[12px]">
        {!setup && !expired && (
          <button type="button" className="text-muted underline hover:text-fg" onClick={() => { setRecovery((r) => !r); setCode(""); }}>
            {recovery ? t("auth.mfa.useApp") : t("auth.mfa.useRecovery")}
          </button>
        )}
        <button type="button" className="text-muted underline hover:text-fg" onClick={onCancel}>{t("auth.mfa.back")}</button>
      </div>
    </form>
  );
}
