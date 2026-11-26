// Organizzazioni (tenant) dall'interfaccia: il superamministratore dell'istanza vede l'elenco, ne crea
// una nuova (nome, identificativo, lingua, valuta) e capisce subito come entrarci e come metterci
// persone. Vedi internal/api/platform.go e docs/sviluppo/organizzazioni.md.
import { useId, useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { organizationsApi, type NewOrgResult, type OrgRow } from "../organizationsApi";
import { Select } from "../select";
import { PageHeader, useMe } from "../shell";
import { ConfirmButton } from "./adminUi";
import { ErrorNote, Loading, SectionTitle, date } from "../ui";
import { useT, useLang } from "../i18n/react";
import { LANGUAGES, isLang } from "../i18n/languages";

/** Dal nome all'identificativo: minuscole, cifre e trattini. */
function slugOf(name: string): string {
  return name
    .normalize("NFD")
    .replace(/[̀-ͯ]/g, "")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 40);
}

/** Cambia organizzazione come fa il menu e porta dove indicato. */
function useEnter(to: string) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  return useMutation({
    mutationFn: (tenantId: string) => api.switchTenant(tenantId),
    onSuccess: (m) => {
      qc.setQueryData(["me"], m);
      qc.resetQueries({ predicate: (q) => q.queryKey[0] !== "me" });
      navigate({ to: to as never });
    },
  });
}

export function OrganizationsPage() {
  const t = useT();
  const me = useMe();
  const list = useQuery({ queryKey: ["platform-tenants"], queryFn: organizationsApi.list, enabled: !!me.data?.is_platform_admin });
  const [done, setDone] = useState<NewOrgResult | null>(null);
  return (
    <>
      <PageHeader path={t("organizzazioni.path")} title={t("organizzazioni.title")} />
      <div className="flex max-w-[860px] flex-col gap-6 px-4 pb-12 md:px-7">
        {me.data && !me.data.is_platform_admin ? (
          <p role="alert" className="m-0 text-muted">{t("organizzazioni.riservato")}</p>
        ) : (
          <>
            <p className="m-0 max-w-3xl text-muted">{t("organizzazioni.intro")}</p>
            {done ? <Done res={done} onAgain={() => setDone(null)} /> : <CreateForm onDone={setDone} />}
            <section className="flex flex-col gap-3" aria-label={t("organizzazioni.elenco.title")}>
              <SectionTitle>{t("organizzazioni.elenco.title")}</SectionTitle>
              {list.isPending && <Loading />}
              {!list.data && <ErrorNote error={list.error} />}
              {list.data && (
                <ul className="m-0 flex list-none flex-col p-0" data-testid="organizzazioni-elenco">
                  {list.data.items.map((o) => (
                    <Row key={o.id} org={o} member={!!me.data?.tenants.some((x) => x.tenant_id === o.id)} />
                  ))}
                </ul>
              )}
            </section>
          </>
        )}
      </div>
    </>
  );
}

function Row({ org, member }: { org: OrgRow; member: boolean }) {
  const t = useT();
  const qc = useQueryClient();
  const enter = useEnter("/configurazione/avvio");
  const me = useMe();
  const current = me.data?.active_tenant?.tenant_id === org.id;
  const suspended = org.status !== "active";
  const [editing, setEditing] = useState(false);
  const [name, setName] = useState(org.name);
  const nameId = useId();
  // Dopo ogni modifica si ricarica l'elenco e il profilo (il selettore dell'organizzazione ne dipende).
  const done = () => {
    qc.invalidateQueries({ queryKey: ["platform-tenants"] });
    qc.invalidateQueries({ queryKey: ["me"] });
  };
  const rename = useMutation({
    mutationFn: () => organizationsApi.rename(org.id, name),
    onSuccess: () => {
      setEditing(false);
      done();
    },
  });
  const suspend = useMutation({ mutationFn: () => organizationsApi.suspend(org.id), onSuccess: done });
  const reactivate = useMutation({ mutationFn: () => organizationsApi.reactivate(org.id), onSuccess: done });
  return (
    <li className="flex flex-col gap-2 border-b border-hair py-3" data-testid={`org-${org.slug}`}>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
        <div className="flex min-w-0 flex-1 basis-56 flex-col">
          {editing ? (
            <form
              className="flex flex-wrap items-center gap-2"
              onSubmit={(e) => {
                e.preventDefault();
                rename.mutate();
              }}
            >
              <input
                id={nameId}
                className="field min-w-0 flex-1 basis-48"
                aria-label={t("organizzazioni.rinomina.nome", { name: org.name })}
                required
                maxLength={120}
                autoFocus
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
              <button type="submit" className="btn btn-primary" disabled={rename.isPending || !name.trim()}>{t("organizzazioni.rinomina.salva")}</button>
              <button
                type="button"
                className="btn"
                onClick={() => {
                  setEditing(false);
                  setName(org.name);
                  rename.reset();
                }}
              >
                {t("organizzazioni.rinomina.annulla")}
              </button>
            </form>
          ) : (
            <span className="min-w-0 [overflow-wrap:anywhere] font-semibold">{org.name}</span>
          )}
          <span className="font-mono text-[12px] text-muted">{org.slug}</span>
        </div>
        <span className={`text-[12.5px] ${suspended ? "font-semibold text-fg" : "text-muted"}`} data-testid={`org-stato-${org.slug}`} aria-label={t("organizzazioni.stato.etichetta")}>
          {suspended ? t("organizzazioni.stato.sospesa") : t("organizzazioni.stato.attiva")}
        </span>
        <span className="text-[12.5px] text-muted">
          {LANGUAGES.find((l) => l.key === org.language)?.name} · {org.base_currency} · {t("organizzazioni.elenco.persone", { count: org.members })} · {date(org.created_at)}
        </span>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        {current ? (
          <span className="text-[12.5px] font-semibold">{t("organizzazioni.elenco.attiva")}</span>
        ) : member && !suspended ? (
          <button className="btn" disabled={enter.isPending} onClick={() => enter.mutate(org.id)}>
            {t("organizzazioni.entra")}
          </button>
        ) : !member ? (
          <span className="text-[12.5px] text-muted">{t("organizzazioni.elenco.nonMembro")}</span>
        ) : null}
        {!editing && (
          <button className="btn" onClick={() => setEditing(true)}>{t("organizzazioni.rinomina")}</button>
        )}
        {suspended ? (
          <button className="btn" disabled={reactivate.isPending} onClick={() => reactivate.mutate()}>{t("organizzazioni.riattiva")}</button>
        ) : current ? (
          <span className="text-[12.5px] text-muted">{t("organizzazioni.sospendi.stessa")}</span>
        ) : (
          <ConfirmButton label={t("organizzazioni.sospendi")} confirm={t("organizzazioni.sospendi.sicuro")} danger pending={suspend.isPending} onConfirm={() => suspend.mutate()} />
        )}
      </div>
      {!suspended && !current && <span className="text-[12.5px] text-muted">{t("organizzazioni.sospendi.effetto")}</span>}
      <ErrorNote error={rename.error ?? suspend.error ?? reactivate.error ?? enter.error} />
    </li>
  );
}

function CreateForm({ onDone }: { onDone: (r: NewOrgResult) => void }) {
  const t = useT();
  const qc = useQueryClient();
  const ids = [useId(), useId(), useId(), useId(), useId()];
  const ui = useLang();
  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [slugTouched, setSlugTouched] = useState(false);
  const [language, setLanguage] = useState<"it" | "en">(isLang(ui) ? ui : "it");
  const [currency, setCurrency] = useState("EUR");
  const [adminEmail, setAdminEmail] = useState("");
  const create = useMutation({
    mutationFn: () => organizationsApi.create({ name, slug, language, base_currency: currency, admin_email: adminEmail.trim() || undefined }),
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ["platform-tenants"] });
      qc.invalidateQueries({ queryKey: ["me"] });
      onDone(r);
    },
  });
  return (
    <form
      className="flex flex-col gap-4 border border-rule p-4"
      aria-label={t("organizzazioni.nuova.title")}
      onSubmit={(e) => {
        e.preventDefault();
        create.mutate();
      }}
    >
      <h2 className="m-0 text-[16px] font-bold">{t("organizzazioni.nuova.title")}</h2>
      <div className="flex flex-col gap-1.5">
        <label htmlFor={ids[0]} className="label">{t("organizzazioni.nuova.nome")}</label>
        <input
          id={ids[0]}
          className="field"
          required
          maxLength={120}
          value={name}
          onChange={(e) => {
            setName(e.target.value);
            if (!slugTouched) setSlug(slugOf(e.target.value));
          }}
        />
      </div>
      <div className="flex flex-col gap-1.5">
        <label htmlFor={ids[1]} className="label">{t("organizzazioni.nuova.slug")}</label>
        <input
          id={ids[1]}
          className="field font-mono"
          required
          maxLength={63}
          value={slug}
          onChange={(e) => {
            setSlugTouched(true);
            setSlug(e.target.value.toLowerCase());
          }}
        />
        <span className="text-[12.5px] text-muted">{t("organizzazioni.nuova.slugHelp")}</span>
      </div>
      <div className="flex flex-wrap gap-4">
        <div className="flex min-w-[200px] flex-1 flex-col gap-1.5">
          <label htmlFor={ids[2]} className="label">{t("organizzazioni.nuova.lingua")}</label>
          <Select id={ids[2]} className="field" value={language} onChange={(e) => setLanguage(e.target.value as "it" | "en")}>
            {LANGUAGES.map((l) => (
              <option key={l.key} value={l.key} lang={l.key}>{l.name}</option>
            ))}
          </Select>
          <span className="text-[12.5px] text-muted">{t("organizzazioni.nuova.linguaHelp")}</span>
        </div>
        <div className="flex min-w-[140px] flex-col gap-1.5">
          <label htmlFor={ids[3]} className="label">{t("organizzazioni.nuova.valuta")}</label>
          <input id={ids[3]} className="field font-mono uppercase" required maxLength={3} value={currency} onChange={(e) => setCurrency(e.target.value.toUpperCase())} />
        </div>
      </div>
      <div className="flex flex-col gap-1.5">
        <label htmlFor={ids[4]} className="label">{t("organizzazioni.nuova.altroAdmin")}</label>
        <input id={ids[4]} className="field" type="email" autoComplete="off" value={adminEmail} onChange={(e) => setAdminEmail(e.target.value)} />
        <span className="text-[12.5px] text-muted">{t("organizzazioni.nuova.altroAdminHelp")}</span>
      </div>
      <ErrorNote error={create.error} />
      <div>
        <button type="submit" className="btn btn-primary" disabled={create.isPending || !name.trim() || !slug}>
          {create.isPending ? t("organizzazioni.nuova.creando") : t("organizzazioni.nuova.crea")}
        </button>
      </div>
    </form>
  );
}

function Done({ res, onAgain }: { res: NewOrgResult; onAgain: () => void }) {
  const t = useT();
  const enter = useEnter("/configurazione/avvio");
  const enterPeople = useEnter("/configurazione/persone");
  const link = res.invite?.link;
  const href = link ? (link.url ?? `${window.location.origin}${link.path}`) : "";
  return (
    <section className="flex flex-col gap-4 border border-rule p-4" aria-label={t("organizzazioni.fatto.title", { name: res.name })} data-testid="organizzazione-creata">
      <h2 className="m-0 text-[16px] font-bold">{t("organizzazioni.fatto.title", { name: res.name })}</h2>
      <p className="m-0">{t("organizzazioni.fatto.testo")}</p>
      <div className="flex flex-wrap gap-2">
        <button className="btn btn-primary" disabled={enter.isPending} onClick={() => enter.mutate(res.tenant_id)}>
          {t("organizzazioni.entra")}
        </button>
        <button className="btn" onClick={onAgain}>{t("organizzazioni.fatto.altra")}</button>
      </div>
      <ErrorNote error={enter.error} />
      <div className="flex flex-col gap-2 border-t border-hair pt-3" data-testid="organizzazione-persone">
        <h3 className="m-0 text-[14px] font-bold">{t("organizzazioni.persone.title")}</h3>
        <ol className="m-0 flex flex-col gap-1 pl-5">
          <li>{t("organizzazioni.persone.passo1")}</li>
          <li>{t("organizzazioni.persone.passo2")}</li>
          <li>{t("organizzazioni.persone.passo3")}</li>
        </ol>
        <p className="m-0 text-[12.5px] text-muted">{t("organizzazioni.persone.nota")}</p>
        <div>
          <button className="btn" disabled={enterPeople.isPending} onClick={() => enterPeople.mutate(res.tenant_id)}>
            {t("organizzazioni.persone.vai")}
          </button>
        </div>
      </div>
      {res.invite && (
        <div className="flex flex-col gap-1.5 border-t border-hair pt-3" data-testid="organizzazione-invito">
          <p className="m-0">{res.invite.emailed ? t("organizzazioni.invito.inviato", { email: res.invite.email }) : t("organizzazioni.invito.copia", { email: res.invite.email })}</p>
          {link && <input className="field font-mono" readOnly aria-label={t("organizzazioni.invito.link")} value={href} onFocus={(e) => e.currentTarget.select()} />}
        </div>
      )}
      {res.invite_error && <p role="alert" className="m-0 text-muted">{t("organizzazioni.invito.errore")}</p>}
    </section>
  );
}
