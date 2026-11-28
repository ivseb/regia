// Configurazione → Integrazioni → ServiceNow (DevSecOps, sola lettura): integrazioni con
// un'istanza ServiceNow (Basic o OAuth), prova di connessione tabella per tabella, servizi
// CMDB con la proposta di associazione alle applicazioni, incidenti P1-P2 e change request
// letti, stato delle sincronizzazioni. Regia non scrive mai su ServiceNow.
// Solo classi e variabili del tema.
import { Select } from "../select";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError, api } from "../api";
import type { SyncStatus } from "../githubApi";
import {
  servicenowApi,
  type SNAuth,
  type SNDoraMode,
  type SNIntegration,
  type SNIntegrationInput,
  type SNIntegrationList,
  type SNRun,
  type SNService,
  type SNServiceStatus,
  type SNTestResult,
} from "../servicenowApi";
import { PageHeader } from "../shell";
import { ErrorNote, SectionTitle, ago } from "../ui";
import { ConfirmButton, CopyBox, KV, MutError, SidePanel, Tag, dateTime } from "./adminUi";
import { SNChangeRow, SNIncidentRow } from "./ServiceNowRecords";
import { useT } from "../i18n/react";
import { Trans } from "../i18n/Trans";
import type { Key } from "../i18n/core";
import { fmtNumber } from "../i18n/format";

export type ServiceNowTab = "integrazioni" | "servizi" | "incidenti" | "sincronizzazioni";
export const SERVICENOW_TABS: ServiceNowTab[] = ["integrazioni", "servizi", "incidenti", "sincronizzazioni"];
const TAB_LABELS: Record<ServiceNowTab, Key> = {
  integrazioni: "altro.servicenow.tab.integrazioni",
  servizi: "altro.servicenow.tab.servizi",
  incidenti: "altro.servicenow.tab.incidenti",
  sincronizzazioni: "altro.servicenow.tab.sincronizzazioni",
};
const NEW = "nuovo";
const PATH = "/configurazione/integrazioni/servicenow";
const DEFAULT_CLASS = "cmdb_ci_service";
const PRIORITIES = [1, 2, 3, 4, 5] as const;
const PRIO_LABELS: Record<(typeof PRIORITIES)[number], Key> = {
  1: "altro.servicenow.prio.1",
  2: "altro.servicenow.prio.2",
  3: "altro.servicenow.prio.3",
  4: "altro.servicenow.prio.4",
  5: "altro.servicenow.prio.5",
};

const SYNC: Record<SyncStatus, { label: Key; tone: "ok" | "flag" | "danger" | "muted" }> = {
  running: { label: "altro.servicenow.sync.running", tone: "muted" },
  ok: { label: "altro.servicenow.sync.ok", tone: "ok" },
  partial: { label: "altro.servicenow.sync.partial", tone: "flag" },
  error: { label: "altro.servicenow.sync.error", tone: "danger" },
  rate_limited: { label: "altro.servicenow.sync.rate_limited", tone: "flag" },
};
const TRIGGER: Record<SNRun["trigger"], Key> = {
  schedule: "altro.servicenow.trigger.schedule",
  manual: "altro.servicenow.trigger.manual",
  webhook: "altro.servicenow.trigger.webhook",
};

type Search = { tab?: ServiceNowTab; sel?: string; integrazione?: string };

export function ServiceNowPage({ tab, sel, integration }: { tab: ServiceNowTab; sel?: string; integration?: string }) {
  const t = useT();
  const navigate = useNavigate();
  const go = (s: Search) => navigate({ to: PATH, search: { tab, ...s } });
  const list = useQuery({ queryKey: ["sn-integrations"], queryFn: servicenowApi.list, refetchInterval: 10_000 });
  if (list.error instanceof ApiError && list.error.status === 403) {
    return (
      <>
        <PageHeader path={t("altro.servicenow.path")} title="ServiceNow" /* i18n-ok: nome del prodotto */ />
        <p className="px-4 text-muted md:px-7">{t("altro.servicenow.needAdmin")}</p>
      </>
    );
  }
  const items = list.data?.items ?? [];
  const current = items.find((i) => i.id === integration) ?? items[0];
  return (
    <>
      <PageHeader
        path={t("altro.servicenow.path")}
        title="ServiceNow" /* i18n-ok: nome del prodotto */
        actions={tab === "integrazioni" ? <button className="btn btn-primary" onClick={() => go({ sel: NEW })}>{t("altro.servicenow.newIntegration")}</button> : undefined}
      />
      <div role="tablist" aria-label={t("altro.servicenow.sections")} className="flex gap-6 overflow-x-auto border-b border-hair px-4 md:px-7">
        {SERVICENOW_TABS.map((k) => (
          <Link
            key={k}
            to={PATH}
            search={{ tab: k, integrazione: integration }}
            role="tab"
            aria-selected={tab === k}
            className={`shrink-0 pb-2.5 no-underline ${tab === k ? "border-b-2 border-fg font-semibold text-fg" : "text-muted hover:text-fg"}`}
          >
            {t(TAB_LABELS[k])}
          </Link>
        ))}
      </div>
      <ErrorNote error={list.error} />
      {tab === "integrazioni" && list.data && <IntegrationsTab data={list.data} selected={sel} onSelect={(s) => go({ sel: s })} />}
      {tab !== "integrazioni" && list.data && items.length === 0 && (
        <p className="px-4 py-6 text-muted md:px-7">
          <Trans k="altro.servicenow.noIntegrationYet" slots={{ link: <Link to={PATH} search={{ tab: "integrazioni", sel: NEW }}>{t("altro.servicenow.tab.integrazioni")}</Link> }} />
        </p>
      )}
      {tab === "servizi" && current && (
        <IntegrationPicker items={items} current={current} onPick={(id) => go({ integrazione: id })}>
          <ServicesTab key={current.id} it={current} />
        </IntegrationPicker>
      )}
      {tab === "incidenti" && current && (
        <IntegrationPicker items={items} current={current} onPick={(id) => go({ integrazione: id })}>
          <RecordsTab key={current.id} it={current} />
        </IntegrationPicker>
      )}
      {tab === "sincronizzazioni" && current && (
        <IntegrationPicker items={items} current={current} onPick={(id) => go({ integrazione: id })}>
          <SyncTab key={current.id} it={current} />
        </IntegrationPicker>
      )}
    </>
  );
}

function IntegrationPicker({ items, current, onPick, children }: { items: SNIntegration[]; current: SNIntegration; onPick: (id: string) => void; children: ReactNode }) {
  const t = useT();
  return (
    <div className="flex min-w-0 flex-col gap-4 px-4 py-5 md:px-7">
      {items.length > 1 && (
        <label className="flex max-w-[420px] flex-col gap-1.5">
          <span className="label">{t("altro.servicenow.integration")}</span>
          <Select className="field" value={current.id} onChange={(e) => onPick(e.target.value)}>
            {items.map((i) => (
              <option key={i.id} value={i.id}>{i.name}</option>
            ))}
          </Select>
        </label>
      )}
      {children}
    </div>
  );
}

function SyncTag({ status }: { status: SyncStatus | null }) {
  const t = useT();
  if (!status) return <Tag>{t("altro.servicenow.neverSynced")}</Tag>;
  const s = SYNC[status] ?? SYNC.error;
  return <Tag tone={s.tone}>{t(s.label)}</Tag>;
}

const mono = (s: string) => <span className="font-mono text-[12px]">{s}</span>;

// ---------------------------------------------------------------------
// Integrazioni
// ---------------------------------------------------------------------

function IntegrationsTab({ data, selected, onSelect }: { data: SNIntegrationList; selected?: string; onSelect: (s?: string) => void }) {
  const t = useT();
  const panel = useRef<HTMLDivElement>(null);
  const [secret, setSecret] = useState<string | null>(null);
  useEffect(() => {
    if (!selected || window.innerWidth >= 768) return;
    const t = setTimeout(() => panel.current?.scrollIntoView({ block: "start" }), 80);
    return () => clearTimeout(t);
  }, [selected]);
  const it = data.items.find((i) => i.id === selected);
  return (
    <div className="flex flex-wrap">
      <div className="flex min-w-0 flex-[999_1_480px] flex-col gap-5 px-4 py-5 md:px-7">
        <p className="m-0 max-w-[680px] text-muted">{t("altro.servicenow.intro")}</p>
        {secret && <CopyBox title={t("altro.servicenow.secretTitle")} value={secret} note={t("altro.servicenow.secretNote")} onDone={() => setSecret(null)} />}
        <ul className="m-0 flex list-none flex-col p-0">
          {data.items.map((i) => (
            <li key={i.id} className={`border-b border-hair py-2.5 ${i.id === selected ? "bg-sel" : ""}`}>
              <button className="flex w-full flex-wrap items-baseline gap-x-2 text-left" aria-current={i.id === selected ? "true" : undefined} onClick={() => onSelect(i.id)}>
                <span className="font-medium hover:underline">{i.name}</span>
                {!i.active && <Tag>{t("altro.servicenow.inactive")}</Tag>}
                <SyncTag status={i.sync.last_status} />
                {i.services.suggested > 0 && <Tag tone="accent">{t("altro.servicenow.toLink", { count: i.services.suggested })}</Tag>}
                {i.records.pending > 0 && <Tag tone="flag">{t("altro.servicenow.toDecide", { count: i.records.pending })}</Tag>}
                <span className="ml-auto text-[12px] text-muted">
                  {i.sync.last_finished_at ? t("altro.servicenow.lastRead", { when: ago(i.sync.last_finished_at) }) : t("altro.servicenow.neverRead")}
                </span>
              </button>
              <span className="block min-w-0 [overflow-wrap:anywhere] font-mono text-[11.5px] text-muted" title={i.config.base_url}>
                {i.config.base_url} · {i.config.service_class}
              </span>
              <span className="block text-[12px] text-muted tabular">
                {t("altro.servicenow.summary", { confirmed: i.services.confirmed, total: i.services.total })} ·{" "}
                {t("altro.servicenow.recordsLine", { incidents: i.records.incidents, changes: i.records.changes })}
              </span>
            </li>
          ))}
          {data.items.length === 0 && <li className="py-8 text-muted">{t("altro.servicenow.noIntegrations")}</li>}
        </ul>
        <AccessHelp />
      </div>
      {selected && (
        <div ref={panel} className="flex min-w-0 scroll-mt-14 flex-[1_1_460px]">
          <SidePanel label={t("altro.servicenow.panelLabel")} onClose={() => onSelect(undefined)}>
            {selected === NEW ? (
              <IntegrationForm data={data} onSaved={(id) => onSelect(id)} onCancel={() => onSelect(undefined)} />
            ) : it ? (
              <IntegrationPanel key={it.id} it={it} data={data} onSecret={setSecret} onDeleted={() => onSelect(undefined)} />
            ) : (
              <p className="text-muted">{t("altro.servicenow.notFound")}</p>
            )}
          </SidePanel>
        </div>
      )}
    </div>
  );
}

function AccessHelp() {
  const t = useT();
  return (
    <section className="flex max-w-[680px] flex-col gap-2">
      <SectionTitle>{t("altro.servicenow.helpTitle")}</SectionTitle>
      <ol className="m-0 flex list-decimal flex-col gap-1 pl-5">
        <li>{t("altro.servicenow.help1")}</li>
        <li>
          <Trans
            k="altro.servicenow.help2"
            slots={{ /* i18n-ok: nomi delle tabelle */ incident: mono("incident"), change: mono("change_request"), cmdb: mono(DEFAULT_CLASS) }}
          />
        </li>
        <li>{t("altro.servicenow.help3")}</li>
        <li>{t("altro.servicenow.help4")}</li>
      </ol>
    </section>
  );
}

function IntegrationForm({ it, data, onSaved, onCancel }: { it?: SNIntegration; data: SNIntegrationList; onSaved: (id: string) => void; onCancel?: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const c = it?.config;
  const [name, setName] = useState(it?.name ?? "ServiceNow");
  const [baseURL, setBaseURL] = useState(c?.base_url ?? "");
  const [auth, setAuth] = useState<SNAuth>(c?.auth ?? "basic");
  const [username, setUsername] = useState(c?.username ?? "");
  const [password, setPassword] = useState("");
  const [clientID, setClientID] = useState(c?.client_id ?? "");
  const [clientSecret, setClientSecret] = useState("");
  const [svcClass, setSvcClass] = useState(c?.service_class || DEFAULT_CLASS);
  const [filter, setFilter] = useState(c?.service_filter ?? "");
  const [codeField, setCodeField] = useState(c?.code_field ?? "");
  const [prios, setPrios] = useState<number[]>(c?.priorities?.length ? c.priorities : [1, 2]);
  const [dora, setDora] = useState<SNDoraMode>(c?.dora_mode ?? "confirm");
  const [lookback, setLookback] = useState(c?.lookback_days ?? 90);
  const [interval, setInterval] = useState(c?.interval_minutes ?? 30);
  const [priv, setPriv] = useState(c?.private_network ?? false);
  const [proxy, setProxy] = useState(c?.use_proxy ?? false);
  const [ca, setCA] = useState(c?.ca_certificate ?? "");
  const [removeHook, setRemoveHook] = useState(false);
  const [active, setActive] = useState(it?.active ?? true);
  const save = useMutation({
    mutationFn: async () => {
      const body: SNIntegrationInput = {
        name, active, base_url: baseURL, auth,
        username: auth === "basic" ? username : "", password: auth === "basic" && password ? password : undefined,
        client_id: auth === "oauth" ? clientID : "", client_secret: auth === "oauth" && clientSecret ? clientSecret : undefined,
        service_class: svcClass, service_filter: filter, code_field: codeField, priorities: [...prios].sort(), dora_mode: dora,
        lookback_days: Number(lookback) || 90, interval_minutes: Number(interval) || 30,
        private_network: priv, use_proxy: proxy, ca_certificate: ca, remove_webhook_secret: removeHook || undefined,
      };
      if (it) {
        await servicenowApi.update(it.id, body);
        return it.id;
      }
      return (await servicenowApi.create(body)).id;
    },
    onSuccess: (id) => {
      qc.invalidateQueries({ queryKey: ["sn-integrations"] });
      setPassword("");
      setClientSecret("");
      setRemoveHook(false);
      onSaved(id);
    },
  });
  const err = save.error instanceof ApiError ? save.error : null;
  const bad = (f: string) => err?.field === f;
  const savedSecret = auth === "basic" ? !!it?.has_password && c?.auth === "basic" : !!it?.has_client_secret && c?.auth === "oauth";
  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      {!it && <SectionTitle>{t("altro.servicenow.newIntegration")}</SectionTitle>}
      <label className="flex flex-col gap-1.5">
        <span className="label">{t("altro.servicenow.name")}</span>
        <input className="field" required maxLength={80} value={name} aria-invalid={bad("name")} onChange={(e) => setName(e.target.value)} />
      </label>
      <label className="flex flex-col gap-1.5">
        <span className="label">{t("altro.servicenow.instance")}</span>
        <input className="field font-mono" required placeholder={t("altro.servicenow.instancePh")} value={baseURL} aria-invalid={bad("base_url")} onChange={(e) => setBaseURL(e.target.value)} />
        <span className="text-[12px] text-muted">
          <Trans k="altro.servicenow.instanceHelp" slots={{ /* i18n-ok: esempio di indirizzo */ name: mono("acme"), url: mono("https://acme.service-now.com") }} />
        </span>
      </label>
      <fieldset className="m-0 flex flex-col gap-2 border-0 p-0">
        <legend className="label mb-1.5 p-0">{t("altro.servicenow.auth")}</legend>
        <label className="flex items-center gap-2">
          <input type="radio" name="sn-auth" checked={auth === "basic"} onChange={() => setAuth("basic")} /> {t("altro.servicenow.authBasic")}
        </label>
        <label className="flex items-center gap-2">
          <input type="radio" name="sn-auth" checked={auth === "oauth"} onChange={() => setAuth("oauth")} /> {t("altro.servicenow.authOauth")}
        </label>
      </fieldset>
      {auth === "basic" ? (
        <div className="flex flex-wrap gap-3">
          <label className="flex min-w-[180px] flex-1 flex-col gap-1.5">
            <span className="label">{t("altro.servicenow.username")}</span>
            <input className="field font-mono" required autoComplete="off" value={username} aria-invalid={bad("username")} onChange={(e) => setUsername(e.target.value)} />
          </label>
          <label className="flex min-w-[180px] flex-1 flex-col gap-1.5">
            <span className="label">{t("altro.servicenow.password")}</span>
            <input
              className="field font-mono"
              type="password"
              autoComplete="new-password"
              required={!savedSecret}
              placeholder={savedSecret ? t("altro.servicenow.savedKeep") : undefined}
              value={password}
              aria-invalid={bad("password")}
              onChange={(e) => setPassword(e.target.value)}
            />
          </label>
        </div>
      ) : (
        <div className="flex flex-wrap gap-3">
          <label className="flex min-w-[180px] flex-1 flex-col gap-1.5">
            <span className="label">{t("altro.servicenow.clientId")}</span>
            <input className="field font-mono" required autoComplete="off" value={clientID} aria-invalid={bad("client_id")} onChange={(e) => setClientID(e.target.value)} />
          </label>
          <label className="flex min-w-[180px] flex-1 flex-col gap-1.5">
            <span className="label">{t("altro.servicenow.clientSecret")}</span>
            <input
              className="field font-mono"
              type="password"
              autoComplete="new-password"
              required={!savedSecret}
              placeholder={savedSecret ? t("altro.servicenow.savedKeep") : undefined}
              value={clientSecret}
              aria-invalid={bad("client_secret")}
              onChange={(e) => setClientSecret(e.target.value)}
            />
          </label>
        </div>
      )}
      <label className="flex flex-col gap-1.5">
        <span className="label">{t("altro.servicenow.serviceClass")}</span>
        <input className="field font-mono" required value={svcClass} aria-invalid={bad("service_class")} onChange={(e) => setSvcClass(e.target.value)} />
        <span className="text-[12px] text-muted">
          <Trans k="altro.servicenow.serviceClassHelp" slots={{ /* i18n-ok: nomi delle classi */ base: mono(DEFAULT_CLASS), auto: mono("cmdb_ci_service_auto") }} />
        </span>
      </label>
      <div className="flex flex-wrap gap-3">
        <label className="flex min-w-[180px] flex-1 flex-col gap-1.5">
          <span className="label">{t("altro.servicenow.filter")}</span>
          <input className="field font-mono" placeholder="operational_status=1" /* i18n-ok: esempio di encoded query */ value={filter} aria-invalid={bad("service_filter")} onChange={(e) => setFilter(e.target.value)} />
          <span className="text-[12px] text-muted">{t("altro.servicenow.filterHelp")}</span>
        </label>
        <label className="flex min-w-[180px] flex-1 flex-col gap-1.5">
          <span className="label">{t("altro.servicenow.codeField")}</span>
          <input className="field font-mono" placeholder="u_codice" /* i18n-ok: esempio di campo */ value={codeField} aria-invalid={bad("code_field")} onChange={(e) => setCodeField(e.target.value)} />
          <span className="text-[12px] text-muted">{t("altro.servicenow.codeFieldHelp")}</span>
        </label>
      </div>
      <fieldset className="m-0 flex min-w-0 flex-col gap-1.5 border-0 p-0" aria-invalid={bad("priorities")}>
        <legend className="label mb-1.5 p-0">{t("altro.servicenow.priorities")}</legend>
        <div className="flex flex-wrap gap-x-4 gap-y-1.5">
          {PRIORITIES.map((p) => (
            <label key={p} className="flex items-center gap-1.5 text-[13px]">
              <input
                type="checkbox"
                checked={prios.includes(p)}
                onChange={(e) => setPrios((cur) => (e.target.checked ? [...cur.filter((x) => x !== p), p] : cur.filter((x) => x !== p)))}
              />
              {t(PRIO_LABELS[p])}
            </label>
          ))}
        </div>
        <span className="text-[12px] text-muted">{t("altro.servicenow.prioritiesHelp")}</span>
      </fieldset>
      <fieldset className="m-0 flex flex-col gap-2 border-0 p-0">
        <legend className="label mb-1.5 p-0">{t("altro.servicenow.dora")}</legend>
        <label className="flex items-start gap-2">
          <input type="radio" name="sn-dora" className="mt-1" checked={dora === "confirm"} onChange={() => setDora("confirm")} />
          <span>
            {t("altro.servicenow.doraConfirm")}
            <span className="block text-[12px] text-muted">{t("altro.servicenow.doraConfirmHelp")}</span>
          </span>
        </label>
        <label className="flex items-start gap-2">
          <input type="radio" name="sn-dora" className="mt-1" checked={dora === "auto"} onChange={() => setDora("auto")} />
          <span>
            {t("altro.servicenow.doraAuto")}
            <span className="block text-[12px] text-muted">{t("altro.servicenow.doraAutoHelp")}</span>
          </span>
        </label>
      </fieldset>
      <div className="flex flex-wrap gap-3">
        <label className="flex min-w-[140px] flex-1 flex-col gap-1.5">
          <span className="label">{t("altro.servicenow.lookback")}</span>
          <input className="field tabular" type="number" min={1} max={730} value={lookback} aria-invalid={bad("lookback_days")} onChange={(e) => setLookback(Number(e.target.value))} />
        </label>
        <label className="flex min-w-[140px] flex-1 flex-col gap-1.5">
          <span className="label">{t("altro.servicenow.interval")}</span>
          <input className="field tabular" type="number" min={5} max={1440} value={interval} aria-invalid={bad("interval_minutes")} onChange={(e) => setInterval(Number(e.target.value))} />
        </label>
      </div>
      <details className="flex flex-col gap-3" open={bad("private_network") || bad("use_proxy") || bad("ca_certificate") || undefined}>
        <summary className="cursor-pointer text-muted">{t("altro.servicenow.advanced")}</summary>
        <div className="mt-3 flex flex-col gap-3">
          <label className="flex items-start gap-2">
            <input type="checkbox" className="mt-0.5" checked={priv} disabled={!data.allow_private && !priv} aria-invalid={bad("private_network")} onChange={(e) => setPriv(e.target.checked)} />
            <span>
              {t("altro.servicenow.internalNet")}
              <span className="block text-[12px] text-muted">
                {data.dev ? t("altro.servicenow.devMode") : data.allow_private ? t("altro.servicenow.allowPriv") : t("altro.servicenow.noPriv")}
              </span>
            </span>
          </label>
          <label className="flex items-start gap-2">
            <input type="checkbox" className="mt-0.5" checked={proxy} disabled={!data.proxy && !proxy} aria-invalid={bad("use_proxy")} onChange={(e) => setProxy(e.target.checked)} />
            <span>
              {t("altro.servicenow.useProxy")}
              <span className="block break-all text-[12px] text-muted">
                {data.proxy ? <Trans k="altro.servicenow.proxyVia" slots={{ proxy: mono(data.proxy) }} /> : t("altro.servicenow.noProxy")}
              </span>
            </span>
          </label>
          <label className="flex flex-col gap-1.5">
            <span className="label">{t("altro.servicenow.ca")}</span>
            <textarea className="field min-h-12 font-mono text-[12px]" placeholder="-----BEGIN CERTIFICATE-----" value={ca} aria-invalid={bad("ca_certificate")} onChange={(e) => setCA(e.target.value)} />
            <span className="text-[12px] text-muted">{t("altro.servicenow.caHelp")}</span>
          </label>
          {it?.has_webhook_secret && (
            <label className="flex items-center gap-2">
              <input type="checkbox" checked={removeHook} onChange={(e) => setRemoveHook(e.target.checked)} /> {t("altro.servicenow.removeHook")}
            </label>
          )}
        </div>
      </details>
      <label className="flex items-center gap-2">
        <input type="checkbox" checked={active} onChange={(e) => setActive(e.target.checked)} /> {t("altro.servicenow.active")}
      </label>
      <ErrorNote error={save.error} />
      <div className="flex flex-wrap gap-2">
        <button className="btn btn-primary" disabled={save.isPending}>{it ? t("altro.servicenow.save") : t("altro.servicenow.create")}</button>
        {onCancel && <button type="button" className="btn" onClick={onCancel}>{t("altro.servicenow.cancel")}</button>}
      </div>
    </form>
  );
}

// Il percorso guidato: cosa è fatto e cosa manca, con il prossimo passo a un clic.
function Steps({ it, tested }: { it: SNIntegration; tested: SNTestResult | null }) {
  const t = useT();
  const creds = it.config.auth === "oauth" ? it.has_client_secret : it.has_password;
  const steps: { done: boolean; label: string; to?: ServiceNowTab; hint: string }[] = [
    { done: creds, label: t("altro.servicenow.step.creds"), hint: t("altro.servicenow.step.credsHint") },
    { done: !!tested?.ok, label: t("altro.servicenow.step.tested"), hint: t("altro.servicenow.step.testedHint") },
    { done: it.services.total > 0, label: t("altro.servicenow.step.found"), to: "servizi", hint: t("altro.servicenow.step.foundHint") },
    { done: it.services.confirmed > 0, label: t("altro.servicenow.step.linked"), to: "servizi", hint: t("altro.servicenow.step.linkedHint") },
    {
      done: !!it.sync.last_finished_at && it.sync.last_status !== "error",
      label: t("altro.servicenow.step.synced"),
      to: "sincronizzazioni",
      hint: t("altro.servicenow.step.syncedHint"),
    },
  ];
  const next = steps.findIndex((s) => !s.done);
  return (
    <ol className="m-0 flex list-none flex-col gap-1 p-0" aria-label={t("altro.servicenow.progress")}>
      {steps.map((s, i) => (
        <li key={s.label} className="flex items-baseline gap-2 text-[13px]">
          <span className={`w-4 shrink-0 text-center font-mono ${s.done ? "text-ok" : i === next ? "text-accent" : "text-muted"}`} aria-hidden>{s.done ? "✓" : i + 1}</span>
          <span className={s.done ? "text-muted" : i === next ? "font-medium" : "text-muted"}>
            {s.label}
            {s.done && <span className="sr-only"> {t("altro.servicenow.step.doneSr")}</span>}
            {i === next && (
              <>
                {" "}
                <span className="font-normal text-muted">
                  — {s.to ? <Link to={PATH} search={{ tab: s.to, integrazione: it.id }}>{s.hint}</Link> : s.hint}
                </span>
              </>
            )}
          </span>
        </li>
      ))}
    </ol>
  );
}

function TestResultBox({ r }: { r: SNTestResult }) {
  const t = useT();
  return (
    <div role="status" className={`flex min-w-0 flex-col gap-1 border-l-2 pl-3 ${r.ok ? "border-ok" : "border-danger"}`}>
      <span className={`font-semibold ${r.ok ? "text-ok" : "text-danger"}`}>{r.message}</span>
      {r.details.map((d) => (
        <span key={d} className="text-[12.5px] text-muted">{d}</span>
      ))}
      {r.warnings.map((w) => (
        <span key={w} className="text-[12.5px] text-flag">{w}</span>
      ))}
      {r.tables.length > 0 && (
        <>
          {/* Da telefono un elenco, da schermo largo una tabella. */}
          <ul className="m-0 mt-2 flex list-none flex-col p-0 md:hidden" aria-label={t("altro.servicenow.tables")}>
            {r.tables.map((x) => (
              <li key={x.table} className="flex flex-col border-b border-hair py-1.5 text-[13px]">
                <span className="flex flex-wrap items-baseline gap-x-2">
                  <span className="font-medium">{x.label}</span>
                  <span className="font-mono text-[11.5px] text-muted">{x.table}</span>
                  <span className={`ml-auto ${x.readable ? "text-ok" : "font-semibold text-danger"}`}>
                    {x.readable ? t("altro.servicenow.tableOk", { count: x.sample }) : t("altro.servicenow.tableNo")}
                  </span>
                </span>
                {x.error && <span className="break-words text-[12px] text-danger">{x.error}</span>}
              </li>
            ))}
          </ul>
          <table className="mt-2 hidden w-full border-collapse text-left text-[13px] md:table">
            <caption className="label pb-1 text-left">{t("altro.servicenow.tables")}</caption>
            <thead>
              <tr className="border-b border-rule">
                <th scope="col" className="label py-1 pr-3 font-normal">{t("altro.servicenow.th.table")}</th>
                <th scope="col" className="label py-1 pr-3 font-normal">{t("altro.servicenow.th.result")}</th>
                <th scope="col" className="label py-1 text-right font-normal">{t("altro.servicenow.th.sample")}</th>
              </tr>
            </thead>
            <tbody>
              {r.tables.map((x) => (
                <tr key={x.table} className="border-b border-hair align-baseline">
                  <td className="py-1.5 pr-3">
                    <span className="block">{x.label}</span>
                    <span className="block font-mono text-[11.5px] text-muted">{x.table}</span>
                  </td>
                  <td className="py-1.5 pr-3">
                    <span className={x.readable ? "text-ok" : "font-semibold text-danger"}>{x.readable ? t("altro.servicenow.readable") : t("altro.servicenow.tableNo")}</span>
                    {x.error && <span className="block break-words text-[12px] text-danger">{x.error}</span>}
                  </td>
                  <td className="py-1.5 text-right font-mono tabular">{x.readable ? fmtNumber(x.sample) : "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}
    </div>
  );
}

function IntegrationPanel({ it, data, onSecret, onDeleted }: { it: SNIntegration; data: SNIntegrationList; onSecret: (s: string) => void; onDeleted: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const [result, setResult] = useState<SNTestResult | null>(null);
  const test = useMutation({ mutationFn: () => servicenowApi.test(it.id), onSuccess: setResult });
  const secret = useMutation({
    mutationFn: () => servicenowApi.webhookSecret(it.id),
    onSuccess: (r) => {
      onSecret(r.secret);
      qc.invalidateQueries({ queryKey: ["sn-integrations"] });
    },
  });
  const del = useMutation({
    mutationFn: () => servicenowApi.remove(it.id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["sn-integrations"] });
      onDeleted();
    },
  });
  const webhookURL = (data.base_url || window.location.origin).replace(/\/$/, "") + it.webhook_path;
  const c = it.config;
  return (
    <div className="flex min-w-0 flex-col gap-5">
      <div className="flex flex-col gap-1">
        <h2 className="m-0 break-words text-[22px] font-bold">{it.name}</h2>
        <span className="text-[12px] text-muted">{t("altro.servicenow.created", { date: dateTime(it.created_at), when: ago(it.updated_at) })}</span>
      </div>
      <Steps it={it} tested={result} />
      <div className="flex flex-wrap gap-2">
        <button className="btn btn-primary py-1.5" disabled={test.isPending} onClick={() => test.mutate()}>
          {test.isPending ? t("altro.servicenow.testing") : t("altro.servicenow.test")}
        </button>
        <Link className="btn py-1.5 no-underline" to={PATH} search={{ tab: "servizi", integrazione: it.id }}>{t("altro.servicenow.tab.servizi")}</Link>
        <Link className="btn py-1.5 no-underline" to={PATH} search={{ tab: "incidenti", integrazione: it.id }}>{t("altro.servicenow.tab.incidenti")}</Link>
        <Link className="btn py-1.5 no-underline" to={PATH} search={{ tab: "sincronizzazioni", integrazione: it.id }}>{t("altro.servicenow.tab.sincronizzazioni")}</Link>
      </div>
      {result && <TestResultBox r={result} />}
      <MutError errors={[test.error, secret.error, del.error]} />
      <dl className="m-0">
        <KV label={t("altro.servicenow.instanceShort")}>
          <span className="block break-all font-mono text-[12px]">{c.base_url}</span>
        </KV>
        <KV label={t("altro.servicenow.auth")}>
          {c.auth === "oauth"
            ? t("altro.servicenow.authLineOauth", { client: c.client_id ?? "" })
            : t("altro.servicenow.authLineBasic", { user: c.username ?? "" })}
          <span className="block text-[12px] text-muted">
            {(c.auth === "oauth" ? it.has_client_secret : it.has_password) ? t("altro.servicenow.secretSaved") : t("altro.servicenow.secretMissing")}
          </span>
        </KV>
        <KV label={t("altro.servicenow.readWhat")}>
          {t("altro.servicenow.readLine", { priorities: c.priorities.map((p) => `P${p}`).join(", "), days: c.lookback_days })}
          <span className="block text-[12px] text-muted">
            {c.dora_mode === "auto" ? t("altro.servicenow.doraAuto") : t("altro.servicenow.doraConfirm")}
          </span>
        </KV>
        <KV label="Webhook" /* i18n-ok: termine tecnico */>
          <span className="block break-all font-mono text-[12px]">{webhookURL}</span>
          <span className="block text-[12px] text-muted">{it.has_webhook_secret ? t("altro.servicenow.hookSet") : t("altro.servicenow.hookNone")}</span>
          <span className="block text-[12px] text-muted">
            <Trans k="altro.servicenow.hookHelp" slots={{ /* i18n-ok: nome dell'intestazione */ header: mono("X-Regia-Signature") }} />
          </span>
        </KV>
        <KV label={t("altro.servicenow.lastReadLabel")}>
          <SyncTag status={it.sync.last_status} /> {it.sync.last_finished_at && dateTime(it.sync.last_finished_at)}
          {it.sync.last_error && <span className="block break-words text-[12px] text-danger">{it.sync.last_error}</span>}
        </KV>
      </dl>
      <div className="flex flex-wrap gap-2">
        <ConfirmButton
          label={it.has_webhook_secret ? t("altro.servicenow.newSecret") : t("altro.servicenow.genSecret")}
          confirm={it.has_webhook_secret ? t("altro.servicenow.replaceSecret") : t("altro.servicenow.generate")}
          pending={secret.isPending}
          onConfirm={() => secret.mutate()}
        />
        <ConfirmButton label={t("altro.servicenow.delete")} confirm={t("altro.servicenow.deleteIntegration")} danger pending={del.isPending} onConfirm={() => del.mutate()} />
      </div>
      <IntegrationForm key={it.updated_at} it={it} data={data} onSaved={() => undefined} />
    </div>
  );
}

// ---------------------------------------------------------------------
// Servizi CMDB
// ---------------------------------------------------------------------

const SERVICE_STATUS: Record<SNServiceStatus, { label: Key; tone: "ok" | "accent" | "muted" | "fg" }> = {
  confirmed: { label: "altro.servicenow.status.confirmed", tone: "ok" },
  suggested: { label: "altro.servicenow.status.suggested", tone: "accent" },
  none: { label: "altro.servicenow.status.none", tone: "fg" },
  ignored: { label: "altro.servicenow.status.ignored", tone: "muted" },
};

function ServicesTab({ it }: { it: SNIntegration }) {
  const t = useT();
  const qc = useQueryClient();
  const services = useQuery({ queryKey: ["sn-services", it.id], queryFn: () => servicenowApi.services(it.id) });
  const apps = useQuery({ queryKey: ["applications", ""], queryFn: () => api.applications("") });
  const [filter, setFilter] = useState<"tutti" | SNServiceStatus>("tutti");
  const [text, setText] = useState("");
  const [chosen, setChosen] = useState<string[]>([]);
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["sn-services", it.id] });
    qc.invalidateQueries({ queryKey: ["sn-integrations"] });
  };
  const discover = useMutation({ mutationFn: () => servicenowApi.discover(it.id), onSuccess: refresh });
  const accept = useMutation({
    mutationFn: () => servicenowApi.accept(it.id, chosen),
    onSuccess: () => {
      setChosen([]);
      refresh();
    },
  });
  const all = services.data?.items ?? [];
  const needle = text.trim().toLowerCase();
  const items = all.filter(
    (s) => (filter === "tutti" || s.status === filter) && (!needle || [s.name, s.class, s.code ?? ""].some((v) => v.toLowerCase().includes(needle))),
  );
  const suggested = all.filter((s) => s.status === "suggested");
  const appOptions = (apps.data?.items ?? []).map((a) => ({ id: a.id, label: `${a.name}${a.code ? ` · ${a.code}` : ""}` }));
  return (
    <div className="flex min-w-0 flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2">
        <button className="btn btn-primary py-1.5" disabled={discover.isPending} onClick={() => discover.mutate()}>
          {discover.isPending ? t("altro.servicenow.reading") : t("altro.servicenow.discover")}
        </button>
        <button className="btn py-1.5" disabled={accept.isPending || chosen.length === 0} onClick={() => accept.mutate()}>
          {t("altro.servicenow.confirmChosen", { count: chosen.length })}
        </button>
        {suggested.length > 0 && (
          <button type="button" className="text-[12px] text-accent underline" onClick={() => setChosen(suggested.map((s) => s.id))}>
            {t("altro.servicenow.chooseAll")}
          </button>
        )}
        <input
          className="field ml-auto w-auto min-w-[140px] max-w-full"
          type="search"
          aria-label={t("altro.servicenow.searchAria")}
          placeholder={t("altro.servicenow.searchPh")}
          value={text}
          onChange={(e) => setText(e.target.value)}
        />
        <Select className="field w-auto" aria-label={t("altro.servicenow.filterAria")} value={filter} onChange={(e) => setFilter(e.target.value as typeof filter)}>
          <option value="tutti">{t("altro.servicenow.f.all")}</option>
          <option value="suggested">{t("altro.servicenow.f.suggested")}</option>
          <option value="none">{t("altro.servicenow.f.none")}</option>
          <option value="confirmed">{t("altro.servicenow.f.confirmed")}</option>
          <option value="ignored">{t("altro.servicenow.f.ignored")}</option>
        </Select>
      </div>
      {discover.data && (
        <p role="status" className="m-0 text-[13px] text-muted">
          {t("altro.servicenow.discovered", { found: discover.data.found, new: discover.data.new, suggested: discover.data.suggested })}
          {discover.data.missing > 0 && t("altro.servicenow.discoveredMissing", { missing: discover.data.missing })}.
        </p>
      )}
      <MutError errors={[discover.error, accept.error, services.error]} />
      <p className="m-0 max-w-[720px] text-[13px] text-muted">
        <Trans k="altro.servicenow.servicesNote" slots={{ cls: mono(it.config.service_class) }} />
      </p>
      <ul className="m-0 flex list-none flex-col border-t-2 border-rule p-0">
        {items.map((s) => (
          <ServiceRow
            key={s.id + s.status + (s.application?.id ?? "")}
            it={it}
            s={s}
            apps={appOptions}
            checked={chosen.includes(s.id)}
            onCheck={(on) => setChosen((c) => (on ? [...c, s.id] : c.filter((x) => x !== s.id)))}
            onChanged={refresh}
          />
        ))}
        {services.data && items.length === 0 && <li className="py-6 text-muted">{t("altro.servicenow.noServices")}</li>}
      </ul>
    </div>
  );
}

function ServiceRow({
  it,
  s,
  apps,
  checked,
  onCheck,
  onChanged,
}: {
  it: SNIntegration;
  s: SNService;
  apps: { id: string; label: string }[];
  checked: boolean;
  onCheck: (on: boolean) => void;
  onChanged: () => void;
}) {
  const t = useT();
  const [app, setApp] = useState(s.application?.id ?? s.suggestion?.application.id ?? "");
  const map = useMutation({
    mutationFn: (status: "confirmed" | "ignored" | "none") => servicenowApi.mapService(it.id, s.id, status === "confirmed" ? { status, application_id: app } : { status }),
    onSuccess: onChanged,
  });
  const st = SERVICE_STATUS[s.status] ?? SERVICE_STATUS.none;
  return (
    <li className={`flex min-w-0 flex-col gap-2 border-b border-hair py-3 ${s.missing ? "opacity-60" : ""}`}>
      <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
        {s.status === "suggested" && <input type="checkbox" aria-label={t("altro.servicenow.choose", { name: s.name })} checked={checked} onChange={(e) => onCheck(e.target.checked)} />}
        {s.url ? (
          <a className="break-words font-medium" href={s.url} target="_blank" rel="noreferrer">{s.name}</a>
        ) : (
          <span className="break-words font-medium">{s.name}</span>
        )}
        <Tag tone={st.tone}>{t(st.label)}</Tag>
        {s.missing && <Tag tone="flag">{t("altro.servicenow.missingTag")}</Tag>}
        {s.last_error && <Tag tone="danger" title={s.last_error}>{t("altro.servicenow.errorTag")}</Tag>}
        <span className="ml-auto text-[12px] text-muted">{s.last_synced_at ? t("altro.servicenow.readAgo", { when: ago(s.last_synced_at) }) : ""}</span>
      </div>
      <span className="flex flex-wrap gap-x-3 gap-y-0.5 text-[12px] text-muted">
        <span>
          <Trans k="altro.servicenow.classLine" slots={{ cls: <span className="font-mono text-fg">{s.class}</span> }} />
        </span>
        {s.code && (
          <span>
            <Trans k="altro.servicenow.codeLine" slots={{ code: <span className="font-mono text-fg">{s.code}</span> }} />
          </span>
        )}
        <span className="tabular">{t("altro.servicenow.countsLine", { incidents: s.incidents, changes: s.changes })}</span>
      </span>
      {s.status === "confirmed" && s.application && (
        <span className="text-[13px]">
          <Trans
            k="altro.servicenow.confirmedLine"
            slots={{
              name: <Link to="/e/$key" params={{ key: s.application.key || s.application.id }} className="font-medium">{s.application.name}</Link>,
              code: <span className="font-mono text-[11.5px] text-muted">{s.application.code || s.application.key}</span>,
            }}
          />
        </span>
      )}
      {s.status !== "confirmed" && s.suggestion && (
        <span className="text-[13px]">
          <Trans
            k="altro.servicenow.suggestionLine"
            slots={{
              name: <span className="font-medium">{s.suggestion.application.name}</span>,
              score: <span className="font-mono text-[11.5px] text-muted tabular">{Math.round(s.suggestion.score * 100)}%</span>,
              reason: <span className="text-muted">{s.suggestion.reason}</span>,
            }}
          />
        </span>
      )}
      {s.last_error && <span className="break-words text-[12px] text-danger">{s.last_error}</span>}
      {s.status !== "confirmed" ? (
        <div className="flex flex-wrap items-end gap-2">
          <label className="flex min-w-0 flex-[2_1_220px] flex-col gap-1">
            <span className="label">{t("altro.servicenow.application")}</span>
            <Select className="field" value={app} onChange={(e) => setApp(e.target.value)}>
              <option value="">{t("altro.servicenow.pick")}</option>
              {apps.map((a) => (
                <option key={a.id} value={a.id}>{a.label}</option>
              ))}
            </Select>
          </label>
          <button className="btn btn-primary py-1.5" disabled={!app || map.isPending} onClick={() => map.mutate("confirmed")}>
            {s.suggestion && app === s.suggestion.application.id ? t("altro.servicenow.confirm") : t("altro.servicenow.link")}
          </button>
          {s.status !== "ignored" ? (
            <button className="btn py-1.5" disabled={map.isPending} onClick={() => map.mutate("ignored")}>{t("altro.servicenow.ignore")}</button>
          ) : (
            <button className="btn py-1.5" disabled={map.isPending} onClick={() => map.mutate("none")}>{t("altro.servicenow.resume")}</button>
          )}
        </div>
      ) : (
        <div>
          <button type="button" className="text-[12px] text-muted underline" disabled={map.isPending} onClick={() => map.mutate("none")}>
            {t("altro.servicenow.unlink")}
          </button>
        </div>
      )}
      <MutError errors={[map.error]} />
    </li>
  );
}

// ---------------------------------------------------------------------
// Incidenti e change letti
// ---------------------------------------------------------------------

function RecordsTab({ it }: { it: SNIntegration }) {
  const t = useT();
  const inc = useQuery({ queryKey: ["sn-incidents", "integration", it.id], queryFn: () => servicenowApi.incidents({ integration: it.id, limit: 100 }) });
  const chg = useQuery({ queryKey: ["sn-changes", "integration", it.id], queryFn: () => servicenowApi.changes({ integration: it.id, limit: 100 }) });
  return (
    <div className="flex min-w-0 flex-col gap-6">
      <p className="m-0 max-w-[720px] text-[13px] text-muted">{t("altro.servicenow.recordsIntro")}</p>
      {it.records.pending > 0 && (
        <p className="m-0 max-w-[720px] border-l-2 border-flag pl-3 text-[13px]">
          <Trans
            k="altro.servicenow.pendingNote"
            params={{ count: it.records.pending }}
            slots={{ link: <Link to="/rischi" search={{ tab: "incidenti" }}>{t("altro.servicenow.pendingLink")}</Link> }}
          />
        </p>
      )}
      <MutError errors={[inc.error, chg.error]} />
      <section className="flex min-w-0 flex-col gap-1">
        <SectionTitle aside={<span className="font-mono text-[11.5px] text-muted tabular">{inc.data?.items.length ?? 0}</span>}>{t("altro.servicenow.incidents")}</SectionTitle>
        <ul className="m-0 flex list-none flex-col p-0">
          {(inc.data?.items ?? []).map((x) => (
            <SNIncidentRow key={x.id} x={x} />
          ))}
          {inc.data?.items.length === 0 && <li className="py-3 text-muted">{t("altro.servicenow.noIncidents")}</li>}
        </ul>
      </section>
      <section className="flex min-w-0 flex-col gap-1">
        <SectionTitle aside={<span className="font-mono text-[11.5px] text-muted tabular">{chg.data?.items.length ?? 0}</span>}>{t("altro.servicenow.changes")}</SectionTitle>
        <ul className="m-0 flex list-none flex-col p-0">
          {(chg.data?.items ?? []).map((x) => (
            <SNChangeRow key={x.id} x={x} />
          ))}
          {chg.data?.items.length === 0 && <li className="py-3 text-muted">{t("altro.servicenow.noChanges")}</li>}
        </ul>
      </section>
    </div>
  );
}

// ---------------------------------------------------------------------
// Sincronizzazioni
// ---------------------------------------------------------------------

function SyncTab({ it }: { it: SNIntegration }) {
  const t = useT();
  const qc = useQueryClient();
  const runs = useQuery({ queryKey: ["sn-runs", it.id], queryFn: () => servicenowApi.runs(it.id), refetchInterval: it.sync.running ? 2_000 : 10_000 });
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["sn-runs", it.id] });
    qc.invalidateQueries({ queryKey: ["sn-integrations"] });
    qc.invalidateQueries({ queryKey: ["sn-incidents"] });
    qc.invalidateQueries({ queryKey: ["sn-changes"] });
  };
  const sync = useMutation({ mutationFn: (full: boolean) => servicenowApi.sync(it.id, full), onSuccess: () => setTimeout(refresh, 600) });
  return (
    <div className="flex min-w-0 flex-col gap-5">
      <dl className="m-0 max-w-[760px]">
        <KV label={t("altro.servicenow.state")}>
          <SyncTag status={it.sync.running ? "running" : it.sync.last_status} />{" "}
          {it.sync.last_finished_at && <span className="text-muted">{t("altro.servicenow.lastShort", { when: dateTime(it.sync.last_finished_at) })}</span>}
          {it.sync.last_error && <span className="block break-words text-[12px] text-danger">{it.sync.last_error}</span>}
        </KV>
        <KV label={t("altro.servicenow.next")}>
          {it.active ? (it.sync.next_run_at ? dateTime(it.sync.next_run_at) : t("altro.servicenow.withinMinute")) : t("altro.servicenow.deactivated")}
        </KV>
        <KV label={t("altro.servicenow.intervalLabel")}>{t("altro.servicenow.everyMinutes", { count: it.config.interval_minutes })}</KV>
        <KV label={t("altro.servicenow.firstRead")}>{t("altro.servicenow.daysBack", { count: it.config.lookback_days })}</KV>
      </dl>
      <div className="flex flex-wrap gap-2">
        <button className="btn btn-primary py-1.5" disabled={sync.isPending || it.sync.running || !it.active} onClick={() => sync.mutate(false)}>
          {t("altro.servicenow.syncNow")}
        </button>
        <ConfirmButton
          label={t("altro.servicenow.rereadAll")}
          confirm={t("altro.servicenow.rereadConfirm")}
          pending={sync.isPending}
          disabled={it.sync.running || !it.active}
          onConfirm={() => sync.mutate(true)}
        />
      </div>
      {it.services.confirmed === 0 && <p className="m-0 text-[13px] text-flag">{t("altro.servicenow.noConfirmed")}</p>}
      <MutError errors={[sync.error, runs.error]} />
      <section className="flex flex-col gap-2">
        <SectionTitle aside={<span className="font-mono text-[11.5px] text-muted tabular">{runs.data?.items.length ?? 0}</span>}>{t("altro.servicenow.latest")}</SectionTitle>
        <ul className="m-0 flex list-none flex-col p-0">
          {(runs.data?.items ?? []).map((r) => (
            <RunRow key={r.id} r={r} />
          ))}
          {runs.data?.items.length === 0 && <li className="py-3 text-muted">{t("altro.servicenow.noRuns")}</li>}
        </ul>
      </section>
    </div>
  );
}

function RunRow({ r }: { r: SNRun }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const s = r.stats;
  const notes = (s.errors?.length ?? 0) + (s.warnings?.length ?? 0);
  return (
    <li className="flex flex-col gap-0.5 border-b border-hair py-2">
      <div className="flex flex-wrap items-baseline gap-x-2">
        <SyncTag status={r.status} />
        <span className="font-medium">{t(TRIGGER[r.trigger] ?? TRIGGER.manual)}</span>
        <span className="text-[12px] text-muted">{r.scope}</span>
        <span className="ml-auto font-mono text-[11.5px] text-muted tabular">{dateTime(r.started_at)}</span>
      </div>
      <span className="text-[12px] text-muted tabular">
        {t("altro.servicenow.statsLine", {
          services: s.services,
          incidents: s.incidents,
          newIncidents: s.new_incidents,
          changes: s.changes,
          newChanges: s.new_changes,
          requests: s.requests,
          seconds: fmtNumber(s.duration_ms / 1000, { maximumFractionDigits: 1 }),
        })}
        {s.discovered ? <> · {t("altro.servicenow.statsDiscovered", { count: s.discovered, new: s.new_services ?? 0 })}</> : null}
      </span>
      {r.error && <span className="break-words text-[12px] text-danger">{r.error}</span>}
      {notes > 0 && (
        <button type="button" className="self-start text-[12px] text-muted underline" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
          {open ? t("altro.servicenow.hideDetails") : t("altro.servicenow.notes", { count: notes })}
        </button>
      )}
      {open && (
        <ul className="m-0 flex list-none flex-col gap-0.5 border-l-2 border-hair pl-3 text-[12px]">
          {(s.errors ?? []).map((e) => (
            <li key={e} className="break-words text-danger">{e}</li>
          ))}
          {(s.warnings ?? []).map((w) => (
            <li key={w} className="break-words text-muted">{w}</li>
          ))}
        </ul>
      )}
    </li>
  );
}
