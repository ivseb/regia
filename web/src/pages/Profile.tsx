import { Select } from "../select";
import { Suspense, lazy, useState, type FormEvent, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ssoApi, type APIToken } from "../api";
import { PageHeader, useMe } from "../shell";
import { ErrorNote, SectionTitle, ago, date } from "../ui";
import { useT } from "../i18n/react";
import { Trans } from "../i18n/Trans";
import type { Key } from "../i18n/core";

// Sezioni dell'aspetto (Appearance.tsx): caricate solo quando si apre il profilo.
const AppearanceSection = lazy(() => import("./Appearance").then((m) => ({ default: m.AppearanceSection })));
const TenantAppearanceSection = lazy(() => import("./Appearance").then((m) => ({ default: m.TenantAppearanceSection })));
// Autenticazione a due fattori con TOTP (Mfa.tsx, piano passo 15).
const MfaSection = lazy(() => import("./Mfa").then((m) => ({ default: m.MfaSection })));
// Lingua personale e predefinita dell'organizzazione (LanguageSettings.tsx).
const LanguageSection = lazy(() => import("./LanguageSettings").then((m) => ({ default: m.LanguageSection })));
const TenantLanguageSection = lazy(() => import("./LanguageSettings").then((m) => ({ default: m.TenantLanguageSection })));

// Profilo: dati dell'account, metodo di accesso e token API personali (piano, passo 15).

// Permessi dei token e tipi di membro: i testi stanno nel catalogo (profile.perm.*, profile.memberType.*).
const permLabel = (t: ReturnType<typeof useT>, scope: string) => {
  const key = `profile.perm.${scope}` as Key;
  const text = t(key);
  return text === key ? scope : text;
};
const memberTypeLabel = (t: ReturnType<typeof useT>, type: string) => {
  const key = `profile.memberType.${type}` as Key;
  const text = t(key);
  return text === key ? type : text;
};

const EXPIRY = [7, 30, 90, 180, 365];

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-1 gap-0.5 border-b border-hair py-2.5 sm:grid-cols-[200px_minmax(0,1fr)] sm:gap-4">
      <dt className="text-muted">{label}</dt>
      <dd className="m-0 min-w-0 break-words">{children}</dd>
    </div>
  );
}

export function Profile() {
  const t = useT();
  const me = useMe();
  const account = useQuery({ queryKey: ["account"], queryFn: ssoApi.account });
  const a = account.data;
  const ssoName = a?.oidc_name || "SSO";
  const tenantName = a?.tenant?.name ?? me.data?.tenants.find((x) => x.tenant_id === me.data?.active_tenant?.tenant_id)?.name;
  const orgName = tenantName ?? t("profile.thisOrganization");

  return (
    <>
      <PageHeader path="profilo" title={a?.display_name ?? me.data?.display_name ?? t("profile.title")} />
      <div className="flex max-w-[880px] flex-col gap-10 px-4 pb-10 md:px-7">
        <section className="flex flex-col">
          <SectionTitle>{t("profile.account")}</SectionTitle>
          <ErrorNote error={account.error} />
          {a && (
            <dl className="m-0">
              <Row label={t("profile.email")}>{a.email}</Row>
              <Row label={t("profile.activeOrganization")}>
                {tenantName ?? "—"}
                {a.tenant && <span className="text-muted"> · {memberTypeLabel(t, a.tenant.member_type)}</span>}
              </Row>
              <Row label={t("profile.session")}>
                {a.method === "oidc" ? t("profile.sessionSso", { name: ssoName }) : a.method === "token" ? t("profile.sessionToken") : t("profile.sessionPassword")}
                {a.last_login_at && <span className="text-muted"> · {t("profile.lastLogin", { when: ago(a.last_login_at) })}</span>}
              </Row>
              <Row label={t("profile.methods")}>
                <ul className="m-0 flex list-none flex-col gap-1 p-0">
                  {a.identities.map((id) => (
                    <li key={id.issuer}>
                      <span className="font-semibold">{ssoName}</span>
                      <span className="text-muted">
                        {" "}· {t("profile.linkedOn", { date: date(id.linked_at) })}
                        {id.last_login_at && <> · {t("profile.usedWhen", { when: ago(id.last_login_at) })}</>}
                      </span>
                      <span className="block truncate font-mono text-[11.5px] text-muted" title={id.issuer}>{id.issuer}</span>
                    </li>
                  ))}
                  {a.has_password && (
                    <li>
                      <span className="font-semibold">{t("profile.password")}</span>
                      {!a.password_login && (
                        <span className="text-muted"> · {t("profile.passwordOff", { name: ssoName })}{a.is_platform_admin ? ` (${t("profile.passwordEmergency")})` : ""}</span>
                      )}
                    </li>
                  )}
                  {a.identities.length === 0 && a.oidc_name && (
                    <li className="text-muted">{t("profile.noIdentity", { name: ssoName })}</li>
                  )}
                </ul>
              </Row>
            </dl>
          )}
        </section>
        {a && a.method !== "token" && (
          <Suspense fallback={null}>
            <MfaSection />
          </Suspense>
        )}
        <Suspense fallback={null}>
          {me.data?.active_tenant && <LanguageSection tenantName={orgName} />}
          {me.data?.active_tenant?.permissions.some((p) => p === "tenant.admin" || p === "*") && <TenantLanguageSection tenantName={orgName} />}
          <AppearanceSection />
          {me.data?.active_tenant?.permissions.some((p) => p === "tenant.admin" || p === "*") && (
            <TenantAppearanceSection tenantName={orgName} />
          )}
        </Suspense>
        {me.data?.active_tenant && <Tokens tenantName={orgName} />}
      </div>
    </>
  );
}

function Tokens({ tenantName }: { tenantName: string }) {
  const t = useT();
  const qc = useQueryClient();
  const list = useQuery({ queryKey: ["api-tokens"], queryFn: ssoApi.tokens });
  const [name, setName] = useState("");
  const [days, setDays] = useState(90);
  const [reduced, setReduced] = useState(false);
  const [scopes, setScopes] = useState<string[]>([]);
  const [secret, setSecret] = useState<{ name: string; token: string } | null>(null);
  const [copied, setCopied] = useState(false);
  const create = useMutation({
    mutationFn: () => ssoApi.createToken({ name, expires_in_days: days, scopes: reduced ? scopes : [] }),
    onSuccess: (r) => {
      setSecret({ name: r.item.name, token: r.token });
      setCopied(false);
      setName("");
      setReduced(false);
      setScopes([]);
      qc.invalidateQueries({ queryKey: ["api-tokens"] });
    },
  });
  function submit(e: FormEvent) {
    e.preventDefault();
    create.mutate();
  }
  async function copy() {
    if (!secret) return;
    try {
      await navigator.clipboard.writeText(secret.token);
      setCopied(true);
    } catch {
      setCopied(false);
    }
  }
  const available = list.data?.scopes ?? [];
  const items = list.data?.items ?? [];

  return (
    <section className="flex flex-col gap-4">
      <SectionTitle aside={<span className="font-mono text-[11.5px] text-muted tabular">{items.length}</span>}>{t("profile.tokens.title")}</SectionTitle>
      <p className="m-0 max-w-[640px] text-muted">
        <Trans
          k="profile.tokens.text"
          slots={{
            // i18n-ok: intestazione HTTP, uguale in ogni lingua
            header: <code className="font-mono text-[12px] text-fg">Authorization: Bearer myt_…</code>,
            tenant: <span className="text-fg">{tenantName}</span>,
          }}
        />
      </p>

      {secret && (
        <div role="status" className="flex flex-col gap-2 border-l-2 border-accent bg-sel px-4 py-3">
          <span className="font-semibold">{t("profile.tokens.created", { name: secret.name })}</span>
          <code className="block select-all break-all font-mono text-[12.5px]">{secret.token}</code>
          <div className="flex flex-wrap gap-2">
            <button type="button" className="btn btn-primary py-1.5" onClick={copy}>{copied ? t("common.copied") : t("common.copy")}</button>
            <button type="button" className="btn py-1.5" onClick={() => setSecret(null)}>{t("common.done")}</button>
          </div>
        </div>
      )}

      <form onSubmit={submit} className="flex flex-col gap-3 border-b border-hair pb-5">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-end">
          <label className="flex min-w-0 flex-1 flex-col gap-1.5">
            <span className="label">{t("profile.tokens.name")}</span>
            <input className="field" required maxLength={80} placeholder={t("profile.tokens.namePlaceholder")} value={name} onChange={(e) => setName(e.target.value)} />
          </label>
          <label className="flex flex-col gap-1.5 sm:w-[160px]">
            <span className="label">{t("profile.tokens.expiry")}</span>
            <Select className="field" value={days} onChange={(e) => setDays(Number(e.target.value))}>
              {EXPIRY.map((d) => (
                <option key={d} value={d}>{d === 365 ? t("profile.tokens.oneYear") : t("profile.tokens.days", { count: d })}</option>
              ))}
            </Select>
          </label>
        </div>
        <fieldset className="m-0 flex flex-col gap-2 border-0 p-0">
          <legend className="label mb-1.5 p-0">{t("profile.tokens.permissions")}</legend>
          <label className="flex items-center gap-2">
            <input type="radio" name="scope-mode" checked={!reduced} onChange={() => setReduced(false)} />
            {t("profile.tokens.allMine")}
          </label>
          <label className="flex items-center gap-2">
            <input type="radio" name="scope-mode" checked={reduced} onChange={() => setReduced(true)} />
            {t("profile.tokens.some")}
          </label>
          {reduced && (
            <div className="grid grid-cols-1 gap-1.5 pl-6 sm:grid-cols-2">
              {available.map((s) => (
                <label key={s} className="flex items-start gap-2">
                  <input
                    type="checkbox"
                    className="mt-0.5"
                    checked={scopes.includes(s)}
                    onChange={(e) => setScopes((cur) => (e.target.checked ? [...cur, s] : cur.filter((x) => x !== s)))}
                  />
                  <span>
                    {permLabel(t, s)} <span className="font-mono text-[11px] text-muted">{s}</span>
                  </span>
                </label>
              ))}
              {scopes.length > 0 && scopes.every((s) => s.endsWith(".read")) && (
                <p className="m-0 text-[12px] text-muted sm:col-span-2">{t("profile.tokens.readOnly")}</p>
              )}
            </div>
          )}
        </fieldset>
        <ErrorNote error={create.error} />
        <div>
          <button className="btn btn-primary" disabled={create.isPending || !name.trim() || (reduced && scopes.length === 0)}>
            {create.isPending ? t("profile.tokens.creating") : t("profile.tokens.create")}
          </button>
        </div>
      </form>

      <ErrorNote error={list.error} />
      <ul className="m-0 flex list-none flex-col p-0">
        {items.map((tk) => (
          <TokenRow key={tk.id} token={tk} />
        ))}
        {!list.isPending && items.length === 0 && <li className="py-4 text-muted">{t("profile.tokens.empty")}</li>}
      </ul>
    </section>
  );
}

function TokenRow({ token: tk }: { token: APIToken }) {
  const t = useT();
  const qc = useQueryClient();
  const [confirm, setConfirm] = useState(false);
  const revoke = useMutation({
    mutationFn: () => ssoApi.revokeToken(tk.id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["api-tokens"] }),
  });
  const expired = new Date(tk.expires_at).getTime() < Date.now();
  return (
    <li className="flex flex-wrap items-center gap-x-4 gap-y-1 border-b border-hair py-3">
      <div className="flex min-w-0 flex-1 basis-[260px] flex-col gap-0.5">
        <span className="flex flex-wrap items-baseline gap-x-2">
          <span className="font-semibold">{tk.name}</span>
          <span className="font-mono text-[12px] text-accent">{tk.hint}…</span>
        </span>
        <span className="text-[12px] text-muted">
          {tk.scopes.length === 0 ? t("profile.tokens.allPermissions") : tk.scopes.map((s) => permLabel(t, s)).join(", ")}
        </span>
      </div>
      <div className="flex flex-col text-[12px] tabular sm:items-end">
        <span className={expired ? "font-semibold text-danger" : ""}>
          {expired ? t("profile.tokens.expired", { date: date(tk.expires_at) }) : t("profile.tokens.expires", { date: date(tk.expires_at) })}
        </span>
        <span className="text-muted">{tk.last_used_at ? t("profile.tokens.lastUsed", { when: ago(tk.last_used_at) }) : t("profile.tokens.neverUsed")}</span>
      </div>
      <div className="flex gap-2">
        {confirm ? (
          <>
            <button className="btn btn-danger py-1" disabled={revoke.isPending} onClick={() => revoke.mutate()}>
              {t("profile.tokens.confirmRevoke")}
            </button>
            <button className="btn py-1" onClick={() => setConfirm(false)}>{t("common.cancel")}</button>
          </>
        ) : (
          <button className="btn py-1" onClick={() => setConfirm(true)}>{t("profile.tokens.revoke")}</button>
        )}
      </div>
      {revoke.error ? <div className="basis-full"><ErrorNote error={revoke.error} /></div> : null}
    </li>
  );
}
