// Configurazione → Integrazioni → ALM / Quality Center (DevSecOps, sola lettura): integrazioni
// con un server ALM (API key o utente di servizio), prova di connessione progetto per progetto,
// unità (progetti, cartelle o valori di un campo) con la proposta di associazione alle
// applicazioni, test set letti con il numero di change, stato delle sincronizzazioni.
// Regia non scrive mai su ALM. Solo classi e variabili del tema.
import { Select } from "../select";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError, api } from "../api";
import type { SyncStatus } from "../githubApi";
import {
  ALM_DEFAULT_CHANGE_FIELD,
  ALM_DEFAULT_CHANGE_PATTERN,
  ALM_DEFAULT_CLOSED,
  ALM_MIN_INTERVAL,
  almApi,
  type ALMAppSource,
  type ALMAuth,
  type ALMChangeSource,
  type ALMIntegration,
  type ALMIntegrationInput,
  type ALMIntegrationList,
  type ALMProject,
  type ALMProjectTest,
  type ALMRun,
  type ALMTestResult,
  type ALMTestSet,
  type ALMUnit,
  type ALMUnitKind,
  type ALMUnitStatus,
} from "../almApi";
import { PageHeader } from "../shell";
import { ErrorNote, SectionTitle, ago } from "../ui";
import { ConfirmButton, KV, MutError, SidePanel, Tag, dateTime } from "./adminUi";
import { useT } from "../i18n/react";
import { Trans } from "../i18n/Trans";
import type { Key } from "../i18n/core";
import { fmtNumber } from "../i18n/format";

export type AlmTab = "integrazioni" | "unita" | "testset" | "sincronizzazioni";
export const ALM_TABS: AlmTab[] = ["integrazioni", "unita", "testset", "sincronizzazioni"];
const TAB_LABELS: Record<AlmTab, Key> = {
  integrazioni: "altro.alm.tab.integrazioni",
  unita: "altro.alm.tab.unita",
  testset: "altro.alm.tab.testset",
  sincronizzazioni: "altro.alm.tab.sincronizzazioni",
};
const NEW = "nuovo";
const PATH = "/configurazione/integrazioni/alm";
const TITLE = "ALM / Quality Center"; // i18n-ok: nome del prodotto

const SYNC: Record<SyncStatus, { label: Key; tone: "ok" | "flag" | "danger" | "muted" }> = {
  running: { label: "altro.alm.sync.running", tone: "muted" },
  ok: { label: "altro.alm.sync.ok", tone: "ok" },
  partial: { label: "altro.alm.sync.partial", tone: "flag" },
  error: { label: "altro.alm.sync.error", tone: "danger" },
  rate_limited: { label: "altro.alm.sync.rate_limited", tone: "flag" },
};
const TRIGGER: Record<string, Key> = {
  schedule: "altro.alm.trigger.schedule",
  manual: "altro.alm.trigger.manual",
};
const CHANGE_SOURCES: { key: ALMChangeSource; label: Key }[] = [
  { key: "test_set_field", label: "altro.alm.changeSource.test_set_field" },
  { key: "test_set_name", label: "altro.alm.changeSource.test_set_name" },
  { key: "cycle_field", label: "altro.alm.changeSource.cycle_field" },
  { key: "release_field", label: "altro.alm.changeSource.release_field" },
];
const APP_SOURCES: { key: ALMAppSource; label: Key; help: Key }[] = [
  { key: "project", label: "altro.alm.appSource.project", help: "altro.alm.appSource.projectHelp" },
  { key: "folder", label: "altro.alm.appSource.folder", help: "altro.alm.appSource.folderHelp" },
  { key: "test_set_field", label: "altro.alm.appSource.test_set_field", help: "altro.alm.appSource.test_set_fieldHelp" },
];
const UNIT_KIND: Record<ALMUnitKind, Key> = {
  project: "altro.alm.kind.project",
  folder: "altro.alm.kind.folder",
  value: "altro.alm.kind.value",
};

type Search = { tab?: AlmTab; sel?: string; integrazione?: string };

export function AlmPage({ tab, sel, integration }: { tab: AlmTab; sel?: string; integration?: string }) {
  const t = useT();
  const navigate = useNavigate();
  const go = (s: Search) => navigate({ to: PATH, search: { tab, ...s } });
  const list = useQuery({ queryKey: ["alm-integrations"], queryFn: almApi.list, refetchInterval: 10_000 });
  if (list.error instanceof ApiError && list.error.status === 403) {
    return (
      <>
        <PageHeader path={t("altro.alm.path")} title={TITLE} />
        <p className="px-4 text-muted md:px-7">{t("altro.alm.needAdmin")}</p>
      </>
    );
  }
  const items = list.data?.items ?? [];
  const current = items.find((i) => i.id === integration) ?? items[0];
  return (
    <>
      <PageHeader
        path={t("altro.alm.path")}
        title={TITLE}
        actions={tab === "integrazioni" ? <button className="btn btn-primary" onClick={() => go({ sel: NEW })}>{t("altro.alm.newIntegration")}</button> : undefined}
      />
      <div role="tablist" aria-label={t("altro.alm.sections")} className="flex gap-6 overflow-x-auto border-b border-hair px-4 md:px-7">
        {ALM_TABS.map((k) => (
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
          <Trans k="altro.alm.noIntegrationYet" slots={{ link: <Link to={PATH} search={{ tab: "integrazioni", sel: NEW }}>{t("altro.alm.tab.integrazioni")}</Link> }} />
        </p>
      )}
      {tab === "unita" && current && (
        <IntegrationPicker items={items} current={current} onPick={(id) => go({ integrazione: id })}>
          <UnitsTab key={current.id} it={current} />
        </IntegrationPicker>
      )}
      {tab === "testset" && current && (
        <IntegrationPicker items={items} current={current} onPick={(id) => go({ integrazione: id })}>
          <TestSetsTab key={current.id} it={current} />
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

function IntegrationPicker({ items, current, onPick, children }: { items: ALMIntegration[]; current: ALMIntegration; onPick: (id: string) => void; children: ReactNode }) {
  const t = useT();
  return (
    <div className="flex min-w-0 flex-col gap-4 px-4 py-5 md:px-7">
      {items.length > 1 && (
        <label className="flex max-w-[420px] flex-col gap-1.5">
          <span className="label">{t("altro.alm.integration")}</span>
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
  if (!status) return <Tag>{t("altro.alm.neverSynced")}</Tag>;
  const s = SYNC[status] ?? SYNC.error;
  return <Tag tone={s.tone}>{t(s.label)}</Tag>;
}

const mono = (s: string) => <span className="font-mono text-[12px]">{s}</span>;
const projectName = (p: ALMProject) => `${p.domain} / ${p.project}`;

function useDebounced<T>(v: T, ms: number): T {
  const [d, setD] = useState(v);
  useEffect(() => {
    const h = setTimeout(() => setD(v), ms);
    return () => clearTimeout(h);
  }, [v, ms]);
  return d;
}

// ---------------------------------------------------------------------
// Integrazioni
// ---------------------------------------------------------------------

function IntegrationsTab({ data, selected, onSelect }: { data: ALMIntegrationList; selected?: string; onSelect: (s?: string) => void }) {
  const t = useT();
  const panel = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!selected || window.innerWidth >= 768) return;
    const h = setTimeout(() => panel.current?.scrollIntoView({ block: "start" }), 80);
    return () => clearTimeout(h);
  }, [selected]);
  const it = data.items.find((i) => i.id === selected);
  return (
    <div className="flex flex-wrap">
      <div className="flex min-w-0 flex-[999_1_480px] flex-col gap-5 px-4 py-5 md:px-7">
        <p className="m-0 max-w-[680px] text-muted">{t("altro.alm.intro")}</p>
        <ul className="m-0 flex list-none flex-col p-0">
          {data.items.map((i) => (
            <li key={i.id} className={`border-b border-hair py-2.5 ${i.id === selected ? "bg-sel" : ""}`}>
              <button className="flex w-full flex-wrap items-baseline gap-x-2 text-left" aria-current={i.id === selected ? "true" : undefined} onClick={() => onSelect(i.id)}>
                <span className="font-medium hover:underline">{i.name}</span>
                {!i.active && <Tag>{t("altro.alm.inactive")}</Tag>}
                <SyncTag status={i.sync.last_status} />
                {i.units.suggested > 0 && <Tag tone="accent">{t("altro.alm.toLink", { count: i.units.suggested })}</Tag>}
                <span className="ml-auto text-[12px] text-muted">
                  {i.sync.last_finished_at ? t("altro.alm.lastRead", { when: ago(i.sync.last_finished_at) }) : t("altro.alm.neverRead")}
                </span>
              </button>
              <span className="block min-w-0 [overflow-wrap:anywhere] font-mono text-[11.5px] text-muted" title={i.config.base_url}>
                {i.config.base_url} · {i.config.projects.map(projectName).join(", ")}
              </span>
              <span className="block text-[12px] text-muted tabular">
                {t("altro.alm.summary", { confirmed: i.units.confirmed, total: i.units.total })} ·{" "}
                {t("altro.alm.recordsLine", { testSets: i.records.test_sets, linked: i.records.linked, defects: i.records.defects })}
              </span>
            </li>
          ))}
          {data.items.length === 0 && <li className="py-8 text-muted">{t("altro.alm.noIntegrations")}</li>}
        </ul>
        <AccessHelp />
      </div>
      {selected && (
        <div ref={panel} className="flex min-w-0 scroll-mt-14 flex-[1_1_460px]">
          <SidePanel label={t("altro.alm.panelLabel")} onClose={() => onSelect(undefined)}>
            {selected === NEW ? (
              <IntegrationForm data={data} onSaved={(id) => onSelect(id)} onCancel={() => onSelect(undefined)} />
            ) : it ? (
              <IntegrationPanel key={it.id} it={it} data={data} onDeleted={() => onSelect(undefined)} />
            ) : (
              <p className="text-muted">{t("altro.alm.notFound")}</p>
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
      <SectionTitle>{t("altro.alm.helpTitle")}</SectionTitle>
      <ol className="m-0 flex list-decimal flex-col gap-1 pl-5">
        <li>{t("altro.alm.help1")}</li>
        <li>{t("altro.alm.help2")}</li>
        <li>
          <Trans k="altro.alm.help3" slots={{ /* i18n-ok: nome del campo di ALM */ field: mono(ALM_DEFAULT_CHANGE_FIELD) }} />
        </li>
        <li>{t("altro.alm.help4")}</li>
      </ol>
    </section>
  );
}

/** Le righe dei progetti: almeno una, anche vuota. */
const projectRows = (p?: ALMProject[]): ALMProject[] => (p && p.length ? p.map((x) => ({ ...x })) : [{ domain: "", project: "" }]);

function IntegrationForm({ it, data, onSaved, onCancel }: { it?: ALMIntegration; data: ALMIntegrationList; onSaved: (id: string) => void; onCancel?: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const c = it?.config;
  const [name, setName] = useState(it?.name ?? "ALM");
  const [baseURL, setBaseURL] = useState(c?.base_url ?? "");
  const [auth, setAuth] = useState<ALMAuth>(c?.auth ?? "apikey");
  const [username, setUsername] = useState(c?.username ?? "");
  const [password, setPassword] = useState("");
  const [clientID, setClientID] = useState(c?.client_id ?? "");
  const [clientSecret, setClientSecret] = useState("");
  const [projects, setProjects] = useState<ALMProject[]>(projectRows(c?.projects));
  const [changeSource, setChangeSource] = useState<ALMChangeSource>(c?.change_source ?? "test_set_field");
  const [changeField, setChangeField] = useState(c?.change_field ?? ALM_DEFAULT_CHANGE_FIELD);
  const [changePattern, setChangePattern] = useState(c?.change_pattern || ALM_DEFAULT_CHANGE_PATTERN);
  const [appSource, setAppSource] = useState<ALMAppSource>(c?.app_source ?? "project");
  const [appField, setAppField] = useState(c?.app_field ?? "");
  const [closed, setClosed] = useState((c?.defect_closed ?? ALM_DEFAULT_CLOSED).join(", "));
  const [keepTester, setKeepTester] = useState(c?.keep_tester ?? false);
  const [timezone, setTimezone] = useState(c?.timezone || "Europe/Rome");
  const [lookback, setLookback] = useState(c?.lookback_days ?? 90);
  const [interval, setInterval] = useState(c?.interval_minutes ?? 60);
  const [priv, setPriv] = useState(c?.private_network ?? false);
  const [proxy, setProxy] = useState(c?.use_proxy ?? false);
  const [ca, setCA] = useState(c?.ca_certificate ?? "");
  const [active, setActive] = useState(it?.active ?? true);
  const save = useMutation({
    mutationFn: async () => {
      const body: ALMIntegrationInput = {
        name, active, base_url: baseURL, auth,
        username: auth === "user" ? username : "", password: auth === "user" && password ? password : undefined,
        client_id: auth === "apikey" ? clientID : "", client_secret: auth === "apikey" && clientSecret ? clientSecret : undefined,
        projects: projects.map((p) => ({ domain: p.domain.trim(), project: p.project.trim() })).filter((p) => p.domain || p.project),
        change_source: changeSource,
        change_field: changeSource === "test_set_name" ? "" : changeField.trim(),
        change_pattern: changePattern.trim(),
        app_source: appSource,
        app_field: appSource === "test_set_field" ? appField.trim() : "",
        defect_closed: closed.split(",").map((s) => s.trim()).filter(Boolean),
        keep_tester: keepTester,
        timezone: timezone.trim(),
        lookback_days: Number(lookback) || 90, interval_minutes: Number(interval) || 60,
        private_network: priv, use_proxy: proxy, ca_certificate: ca,
      };
      if (it) {
        await almApi.update(it.id, body);
        return it.id;
      }
      return (await almApi.create(body)).id;
    },
    onSuccess: (id) => {
      qc.invalidateQueries({ queryKey: ["alm-integrations"] });
      setPassword("");
      setClientSecret("");
      onSaved(id);
    },
  });
  const err = save.error instanceof ApiError ? save.error : null;
  const bad = (f: string) => err?.field === f;
  const savedSecret = auth === "user" ? !!it?.has_password && c?.auth === "user" : !!it?.has_client_secret && c?.auth === "apikey";
  const setProject = (i: number, patch: Partial<ALMProject>) => setProjects((cur) => cur.map((p, j) => (j === i ? { ...p, ...patch } : p)));
  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      {!it && <SectionTitle>{t("altro.alm.newIntegration")}</SectionTitle>}
      <label className="flex flex-col gap-1.5">
        <span className="label">{t("altro.alm.name")}</span>
        <input className="field" required maxLength={80} value={name} aria-invalid={bad("name")} onChange={(e) => setName(e.target.value)} />
      </label>
      <label className="flex flex-col gap-1.5">
        <span className="label">{t("altro.alm.baseUrl")}</span>
        <input className="field font-mono" required placeholder={t("altro.alm.baseUrlPh")} value={baseURL} aria-invalid={bad("base_url")} onChange={(e) => setBaseURL(e.target.value)} />
        <span className="text-[12px] text-muted">
          <Trans k="altro.alm.baseUrlHelp" slots={{ /* i18n-ok: parte dell'indirizzo */ qcbin: mono("/qcbin") }} />
        </span>
      </label>
      <fieldset className="m-0 flex flex-col gap-2 border-0 p-0">
        <legend className="label mb-1.5 p-0">{t("altro.alm.auth")}</legend>
        <label className="flex items-center gap-2">
          <input type="radio" name="alm-auth" checked={auth === "apikey"} onChange={() => setAuth("apikey")} /> {t("altro.alm.authApiKey")}
        </label>
        <label className="flex items-center gap-2">
          <input type="radio" name="alm-auth" checked={auth === "user"} onChange={() => setAuth("user")} /> {t("altro.alm.authUser")}
        </label>
        <span className="text-[12px] text-muted">{auth === "apikey" ? t("altro.alm.authApiKeyHelp") : t("altro.alm.authUserHelp")}</span>
      </fieldset>
      {auth === "apikey" ? (
        <div className="flex flex-wrap gap-3">
          <label className="flex min-w-[180px] flex-1 flex-col gap-1.5">
            <span className="label">{t("altro.alm.clientId")}</span>
            <input className="field font-mono" required autoComplete="off" value={clientID} aria-invalid={bad("client_id")} onChange={(e) => setClientID(e.target.value)} />
          </label>
          <label className="flex min-w-[180px] flex-1 flex-col gap-1.5">
            <span className="label">{t("altro.alm.clientSecret")}</span>
            <input
              className="field font-mono"
              type="password"
              autoComplete="new-password"
              required={!savedSecret}
              placeholder={savedSecret ? t("altro.alm.savedKeep") : undefined}
              value={clientSecret}
              aria-invalid={bad("client_secret")}
              onChange={(e) => setClientSecret(e.target.value)}
            />
          </label>
        </div>
      ) : (
        <div className="flex flex-wrap gap-3">
          <label className="flex min-w-[180px] flex-1 flex-col gap-1.5">
            <span className="label">{t("altro.alm.username")}</span>
            <input className="field font-mono" required autoComplete="off" value={username} aria-invalid={bad("username")} onChange={(e) => setUsername(e.target.value)} />
          </label>
          <label className="flex min-w-[180px] flex-1 flex-col gap-1.5">
            <span className="label">{t("altro.alm.password")}</span>
            <input
              className="field font-mono"
              type="password"
              autoComplete="new-password"
              required={!savedSecret}
              placeholder={savedSecret ? t("altro.alm.savedKeep") : undefined}
              value={password}
              aria-invalid={bad("password")}
              onChange={(e) => setPassword(e.target.value)}
            />
          </label>
        </div>
      )}
      <p className="m-0 text-[12px] text-muted">{t("altro.alm.licenseNote")}</p>
      <fieldset className="m-0 flex min-w-0 flex-col gap-2 border-0 p-0" aria-invalid={bad("projects")}>
        <legend className="label mb-1.5 p-0">{t("altro.alm.projects")}</legend>
        {projects.map((p, i) => (
          <div key={i} className="flex min-w-0 flex-wrap items-center gap-2">
            <input
              className="field min-w-0 flex-[1_1_120px] font-mono"
              aria-label={t("altro.alm.domainRow", { n: i + 1 })}
              placeholder={t("altro.alm.domain")}
              value={p.domain}
              onChange={(e) => setProject(i, { domain: e.target.value })}
            />
            <input
              className="field min-w-0 flex-[1_1_140px] font-mono"
              aria-label={t("altro.alm.projectRow", { n: i + 1 })}
              placeholder={t("altro.alm.project")}
              value={p.project}
              onChange={(e) => setProject(i, { project: e.target.value })}
            />
            {projects.length > 1 && (
              <button type="button" className="text-[12px] text-muted underline" onClick={() => setProjects((cur) => cur.filter((_, j) => j !== i))}>
                {t("altro.alm.removeProject")}
              </button>
            )}
          </div>
        ))}
        <button type="button" className="btn self-start py-1 text-[12.5px]" onClick={() => setProjects((cur) => [...cur, { domain: cur[cur.length - 1]?.domain ?? "", project: "" }])}>
          {t("altro.alm.addProject")}
        </button>
        <span className="text-[12px] text-muted">{t("altro.alm.projectsHelp")}</span>
      </fieldset>
      <fieldset className="m-0 flex min-w-0 flex-col gap-2 border-0 p-0">
        <legend className="label mb-1.5 p-0">{t("altro.alm.change")}</legend>
        <label className="flex flex-col gap-1.5">
          <span className="text-[13px]">{t("altro.alm.changeSource")}</span>
          <Select className="field" value={changeSource} aria-invalid={bad("change_source")} onChange={(e) => setChangeSource(e.target.value as ALMChangeSource)}>
            {CHANGE_SOURCES.map((s) => (
              <option key={s.key} value={s.key}>{t(s.label)}</option>
            ))}
          </Select>
        </label>
        {changeSource !== "test_set_name" && (
          <label className="flex flex-col gap-1.5">
            <span className="text-[13px]">{t("altro.alm.changeField")}</span>
            <input className="field font-mono" required placeholder={ALM_DEFAULT_CHANGE_FIELD} value={changeField} aria-invalid={bad("change_field")} onChange={(e) => setChangeField(e.target.value)} />
            <span className="text-[12px] text-muted">{t("altro.alm.fieldHelp")}</span>
          </label>
        )}
        <label className="flex flex-col gap-1.5">
          <span className="text-[13px]">{t("altro.alm.changePattern")}</span>
          <input className="field font-mono" value={changePattern} aria-invalid={bad("change_pattern")} onChange={(e) => setChangePattern(e.target.value)} />
          <span className="text-[12px] text-muted">
            <Trans k="altro.alm.changePatternHelp" slots={{ /* i18n-ok: esempi di numero di change */ a: mono("CHG0045120"), b: mono("RFC123456") }} />
          </span>
        </label>
      </fieldset>
      <fieldset className="m-0 flex min-w-0 flex-col gap-2 border-0 p-0" aria-invalid={bad("app_source")}>
        <legend className="label mb-1.5 p-0">{t("altro.alm.appSource")}</legend>
        {APP_SOURCES.map((s) => (
          <label key={s.key} className="flex items-start gap-2">
            <input type="radio" name="alm-app" className="mt-1" checked={appSource === s.key} onChange={() => setAppSource(s.key)} />
            <span>
              {t(s.label)}
              <span className="block text-[12px] text-muted">{t(s.help)}</span>
            </span>
          </label>
        ))}
        {appSource === "test_set_field" && (
          <label className="flex flex-col gap-1.5 pl-6">
            <span className="text-[13px]">{t("altro.alm.appField")}</span>
            <input className="field font-mono" required placeholder="user-02" /* i18n-ok: esempio di campo */ value={appField} aria-invalid={bad("app_field")} onChange={(e) => setAppField(e.target.value)} />
          </label>
        )}
      </fieldset>
      <label className="flex flex-col gap-1.5">
        <span className="label">{t("altro.alm.defectClosed")}</span>
        <input className="field font-mono" value={closed} aria-invalid={bad("defect_closed")} onChange={(e) => setClosed(e.target.value)} />
        <span className="text-[12px] text-muted">{t("altro.alm.defectClosedHelp")}</span>
      </label>
      <label className="flex items-start gap-2">
        <input type="checkbox" className="mt-0.5" checked={keepTester} onChange={(e) => setKeepTester(e.target.checked)} />
        <span>
          {t("altro.alm.keepTester")}
          <span className="block text-[12px] text-muted">{t("altro.alm.keepTesterHelp")}</span>
        </span>
      </label>
      <div className="flex flex-wrap gap-3">
        <label className="flex min-w-[140px] flex-1 flex-col gap-1.5">
          <span className="label">{t("altro.alm.timezone")}</span>
          <input className="field font-mono" value={timezone} aria-invalid={bad("timezone")} onChange={(e) => setTimezone(e.target.value)} />
          <span className="text-[12px] text-muted">{t("altro.alm.timezoneHelp")}</span>
        </label>
        <label className="flex min-w-[140px] flex-1 flex-col gap-1.5">
          <span className="label">{t("altro.alm.lookback")}</span>
          <input className="field tabular" type="number" min={1} max={1095} value={lookback} aria-invalid={bad("lookback_days")} onChange={(e) => setLookback(Number(e.target.value))} />
        </label>
        <label className="flex min-w-[140px] flex-1 flex-col gap-1.5">
          <span className="label">{t("altro.alm.interval")}</span>
          <input className="field tabular" type="number" min={ALM_MIN_INTERVAL} max={1440} value={interval} aria-invalid={bad("interval_minutes")} onChange={(e) => setInterval(Number(e.target.value))} />
        </label>
      </div>
      <details className="flex flex-col gap-3" open={bad("private_network") || bad("use_proxy") || bad("ca_certificate") || undefined}>
        <summary className="cursor-pointer text-muted">{t("altro.alm.advanced")}</summary>
        <div className="mt-3 flex flex-col gap-3">
          <label className="flex items-start gap-2">
            <input type="checkbox" className="mt-0.5" checked={priv} disabled={!data.allow_private && !priv} aria-invalid={bad("private_network")} onChange={(e) => setPriv(e.target.checked)} />
            <span>
              {t("altro.alm.internalNet")}
              <span className="block text-[12px] text-muted">
                {data.dev ? t("altro.alm.devMode") : data.allow_private ? t("altro.alm.allowPriv") : t("altro.alm.noPriv")}
              </span>
            </span>
          </label>
          <label className="flex items-start gap-2">
            <input type="checkbox" className="mt-0.5" checked={proxy} disabled={!data.proxy && !proxy} aria-invalid={bad("use_proxy")} onChange={(e) => setProxy(e.target.checked)} />
            <span>
              {t("altro.alm.useProxy")}
              <span className="block break-all text-[12px] text-muted">
                {data.proxy ? <Trans k="altro.alm.proxyVia" slots={{ proxy: mono(data.proxy) }} /> : t("altro.alm.noProxy")}
              </span>
            </span>
          </label>
          <label className="flex flex-col gap-1.5">
            <span className="label">{t("altro.alm.ca")}</span>
            <textarea className="field min-h-12 font-mono text-[12px]" placeholder="-----BEGIN CERTIFICATE-----" value={ca} aria-invalid={bad("ca_certificate")} onChange={(e) => setCA(e.target.value)} />
            <span className="text-[12px] text-muted">{t("altro.alm.caHelp")}</span>
          </label>
        </div>
      </details>
      <label className="flex items-center gap-2">
        <input type="checkbox" checked={active} onChange={(e) => setActive(e.target.checked)} /> {t("altro.alm.active")}
      </label>
      <ErrorNote error={save.error} />
      <div className="flex flex-wrap gap-2">
        <button className="btn btn-primary" disabled={save.isPending}>{it ? t("altro.alm.save") : t("altro.alm.create")}</button>
        {onCancel && <button type="button" className="btn" onClick={onCancel}>{t("altro.alm.cancel")}</button>}
      </div>
    </form>
  );
}

// Il percorso guidato: cosa è fatto e cosa manca, con il prossimo passo a un clic.
function Steps({ it, tested }: { it: ALMIntegration; tested: ALMTestResult | null }) {
  const t = useT();
  const creds = it.config.auth === "user" ? it.has_password : it.has_client_secret;
  const steps: { done: boolean; label: string; to?: AlmTab; hint: string }[] = [
    { done: creds, label: t("altro.alm.step.creds"), hint: t("altro.alm.step.credsHint") },
    { done: !!tested?.ok, label: t("altro.alm.step.tested"), hint: t("altro.alm.step.testedHint") },
    { done: it.units.total > 0, label: t("altro.alm.step.found"), to: "unita", hint: t("altro.alm.step.foundHint") },
    { done: it.units.confirmed > 0, label: t("altro.alm.step.linked"), to: "unita", hint: t("altro.alm.step.linkedHint") },
    {
      done: !!it.sync.last_finished_at && it.sync.last_status !== "error",
      label: t("altro.alm.step.synced"),
      to: "sincronizzazioni",
      hint: t("altro.alm.step.syncedHint"),
    },
  ];
  const next = steps.findIndex((s) => !s.done);
  return (
    <ol className="m-0 flex list-none flex-col gap-1 p-0" aria-label={t("altro.alm.progress")}>
      {steps.map((s, i) => (
        <li key={s.label} className="flex items-baseline gap-2 text-[13px]">
          <span className={`w-4 shrink-0 text-center font-mono ${s.done ? "text-ok" : i === next ? "text-accent" : "text-muted"}`} aria-hidden>{s.done ? "✓" : i + 1}</span>
          <span className={s.done ? "text-muted" : i === next ? "font-medium" : "text-muted"}>
            {s.label}
            {s.done && <span className="sr-only"> {t("altro.alm.step.doneSr")}</span>}
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

function TestResultBox({ r }: { r: ALMTestResult }) {
  const t = useT();
  return (
    <div role="status" className={`flex min-w-0 flex-col gap-1 border-l-2 pl-3 ${r.ok ? "border-ok" : "border-danger"}`}>
      <span className={`font-semibold ${r.ok ? "text-ok" : "text-danger"}`}>{r.message}</span>
      {r.method && <span className="text-[12.5px] text-muted">{t("altro.alm.method", { method: r.method })}</span>}
      {r.details.map((d) => (
        <span key={d} className="text-[12.5px] text-muted">{d}</span>
      ))}
      {r.warnings.map((w) => (
        <span key={w} className="text-[12.5px] text-flag">{w}</span>
      ))}
      {r.projects.map((p) => (
        <ProjectTestBox key={p.project} p={p} />
      ))}
    </div>
  );
}

function ProjectTestBox({ p }: { p: ALMProjectTest }) {
  const t = useT();
  const label = t("altro.alm.collectionsOf", { project: p.project });
  return (
    <div className="mt-2 flex min-w-0 flex-col">
      <span className="flex flex-wrap items-baseline gap-x-2 text-[13px]">
        <span className="font-mono font-semibold [overflow-wrap:anywhere]">{p.project}</span>
        <span className={p.readable ? "text-ok" : "font-semibold text-danger"}>{p.readable ? t("altro.alm.readable") : t("altro.alm.notReadable")}</span>
      </span>
      {p.error && <span className="break-words text-[12px] text-danger">{p.error}</span>}
      {p.collections.length > 0 && (
        <>
          {/* Da telefono un elenco, da schermo largo una tabella. */}
          <ul className="m-0 mt-1 flex list-none flex-col p-0 md:hidden" aria-label={label}>
            {p.collections.map((x) => (
              <li key={x.collection} className="flex flex-col border-b border-hair py-1.5 text-[13px]">
                <span className="flex flex-wrap items-baseline gap-x-2">
                  <span className="font-medium">{x.label}</span>
                  <span className="font-mono text-[11.5px] text-muted">{x.collection}</span>
                  <span className={`ml-auto ${x.readable ? "text-ok" : "font-semibold text-danger"}`}>
                    {x.readable ? t("altro.alm.collectionOk", { count: x.sample }) : t("altro.alm.notReadable")}
                  </span>
                </span>
                {x.error && <span className="break-words text-[12px] text-danger">{x.error}</span>}
              </li>
            ))}
          </ul>
          <table className="mt-1 hidden w-full border-collapse text-left text-[13px] md:table" aria-label={label}>
            <thead>
              <tr className="border-b border-rule">
                <th scope="col" className="label py-1 pr-3 font-normal">{t("altro.alm.th.collection")}</th>
                <th scope="col" className="label py-1 pr-3 font-normal">{t("altro.alm.th.result")}</th>
                <th scope="col" className="label py-1 text-right font-normal">{t("altro.alm.th.sample")}</th>
              </tr>
            </thead>
            <tbody>
              {p.collections.map((x) => (
                <tr key={x.collection} className="border-b border-hair align-baseline">
                  <td className="py-1.5 pr-3">
                    <span className="block">{x.label}</span>
                    <span className="block font-mono text-[11.5px] text-muted">{x.collection}</span>
                  </td>
                  <td className="py-1.5 pr-3">
                    <span className={x.readable ? "text-ok" : "font-semibold text-danger"}>{x.readable ? t("altro.alm.readable") : t("altro.alm.notReadable")}</span>
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

function changeLine(t: ReturnType<typeof useT>, it: ALMIntegration): string {
  const c = it.config;
  const where = CHANGE_SOURCES.find((s) => s.key === c.change_source);
  const base = where ? t(where.label) : c.change_source;
  return c.change_source === "test_set_name" || !c.change_field ? base : `${base} · ${c.change_field}`;
}

function IntegrationPanel({ it, data, onDeleted }: { it: ALMIntegration; data: ALMIntegrationList; onDeleted: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const [result, setResult] = useState<ALMTestResult | null>(null);
  const test = useMutation({ mutationFn: () => almApi.test(it.id), onSuccess: setResult });
  const del = useMutation({
    mutationFn: () => almApi.remove(it.id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["alm-integrations"] });
      onDeleted();
    },
  });
  const c = it.config;
  const app = APP_SOURCES.find((s) => s.key === c.app_source);
  return (
    <div className="flex min-w-0 flex-col gap-5">
      <div className="flex flex-col gap-1">
        <h2 className="m-0 break-words text-[22px] font-bold">{it.name}</h2>
        <span className="text-[12px] text-muted">{t("altro.alm.created", { date: dateTime(it.created_at), when: ago(it.updated_at) })}</span>
      </div>
      <Steps it={it} tested={result} />
      <div className="flex flex-wrap gap-2">
        <button className="btn btn-primary py-1.5" disabled={test.isPending} onClick={() => test.mutate()}>
          {test.isPending ? t("altro.alm.testing") : t("altro.alm.test")}
        </button>
        <Link className="btn py-1.5 no-underline" to={PATH} search={{ tab: "unita", integrazione: it.id }}>{t("altro.alm.tab.unita")}</Link>
        <Link className="btn py-1.5 no-underline" to={PATH} search={{ tab: "testset", integrazione: it.id }}>{t("altro.alm.tab.testset")}</Link>
        <Link className="btn py-1.5 no-underline" to={PATH} search={{ tab: "sincronizzazioni", integrazione: it.id }}>{t("altro.alm.tab.sincronizzazioni")}</Link>
      </div>
      {result && <TestResultBox r={result} />}
      <MutError errors={[test.error, del.error]} />
      <dl className="m-0">
        <KV label={t("altro.alm.baseUrlShort")}>
          <span className="block break-all font-mono text-[12px]">{c.base_url}</span>
        </KV>
        <KV label={t("altro.alm.auth")}>
          {c.auth === "user" ? t("altro.alm.authLineUser", { user: c.username ?? "" }) : t("altro.alm.authLineApiKey", { client: c.client_id ?? "" })}
          <span className="block text-[12px] text-muted">
            {(c.auth === "user" ? it.has_password : it.has_client_secret) ? t("altro.alm.secretSaved") : t("altro.alm.secretMissing")}
          </span>
        </KV>
        <KV label={t("altro.alm.projects")}>
          <span className="block break-words font-mono text-[12px]">{c.projects.map(projectName).join(", ")}</span>
        </KV>
        <KV label={t("altro.alm.change")}>
          {changeLine(t, it)}
          <span className="block break-all font-mono text-[11.5px] text-muted">{c.change_pattern}</span>
        </KV>
        <KV label={t("altro.alm.appSource")}>
          {app ? t(app.label) : c.app_source}
          {c.app_field && <span className="font-mono text-[12px] text-muted"> · {c.app_field}</span>}
        </KV>
        <KV label={t("altro.alm.lastReadLabel")}>
          <SyncTag status={it.sync.last_status} /> {it.sync.last_finished_at && dateTime(it.sync.last_finished_at)}
          {it.sync.last_error && <span className="block break-words text-[12px] text-danger">{it.sync.last_error}</span>}
        </KV>
      </dl>
      <div className="flex flex-wrap gap-2">
        <ConfirmButton label={t("altro.alm.delete")} confirm={t("altro.alm.deleteIntegration")} danger pending={del.isPending} onConfirm={() => del.mutate()} />
      </div>
      <IntegrationForm key={it.updated_at} it={it} data={data} onSaved={() => undefined} />
    </div>
  );
}

// ---------------------------------------------------------------------
// Unità: progetti, cartelle o valori di un campo, associati alle applicazioni
// ---------------------------------------------------------------------

const UNIT_STATUS: Record<ALMUnitStatus, { label: Key; tone: "ok" | "accent" | "muted" | "fg" }> = {
  confirmed: { label: "altro.alm.status.confirmed", tone: "ok" },
  suggested: { label: "altro.alm.status.suggested", tone: "accent" },
  none: { label: "altro.alm.status.none", tone: "fg" },
  ignored: { label: "altro.alm.status.ignored", tone: "muted" },
};

function UnitsTab({ it }: { it: ALMIntegration }) {
  const t = useT();
  const qc = useQueryClient();
  const units = useQuery({ queryKey: ["alm-units", it.id], queryFn: () => almApi.units(it.id) });
  const apps = useQuery({ queryKey: ["applications", ""], queryFn: () => api.applications("") });
  const [filter, setFilter] = useState<"tutti" | ALMUnitStatus>("tutti");
  const [text, setText] = useState("");
  const [chosen, setChosen] = useState<string[]>([]);
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["alm-units", it.id] });
    qc.invalidateQueries({ queryKey: ["alm-integrations"] });
  };
  const discover = useMutation({ mutationFn: () => almApi.discover(it.id), onSuccess: refresh });
  const accept = useMutation({
    mutationFn: () => almApi.accept(it.id, chosen),
    onSuccess: () => {
      setChosen([]);
      refresh();
    },
  });
  const all = units.data?.items ?? [];
  const needle = text.trim().toLowerCase();
  const items = all.filter((u) => (filter === "tutti" || u.status === filter) && (!needle || [u.name, u.path].some((v) => v.toLowerCase().includes(needle))));
  const suggested = all.filter((u) => u.status === "suggested");
  const appOptions = (apps.data?.items ?? []).map((a) => ({ id: a.id, label: `${a.name}${a.code ? ` · ${a.code}` : ""}` }));
  const kind = APP_SOURCES.find((s) => s.key === it.config.app_source);
  return (
    <div className="flex min-w-0 flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2">
        <button className="btn btn-primary py-1.5" disabled={discover.isPending} onClick={() => discover.mutate()}>
          {discover.isPending ? t("altro.alm.reading") : t("altro.alm.discover")}
        </button>
        <button className="btn py-1.5" disabled={accept.isPending || chosen.length === 0} onClick={() => accept.mutate()}>
          {t("altro.alm.confirmChosen", { count: chosen.length })}
        </button>
        {suggested.length > 0 && (
          <button type="button" className="text-[12px] text-accent underline" onClick={() => setChosen(suggested.map((u) => u.id))}>
            {t("altro.alm.chooseAll")}
          </button>
        )}
        <input
          className="field ml-auto w-auto min-w-[140px] max-w-full"
          type="search"
          aria-label={t("altro.alm.searchAria")}
          placeholder={t("altro.alm.searchPh")}
          value={text}
          onChange={(e) => setText(e.target.value)}
        />
        <Select className="field w-auto" aria-label={t("altro.alm.filterAria")} value={filter} onChange={(e) => setFilter(e.target.value as typeof filter)}>
          <option value="tutti">{t("altro.alm.f.all")}</option>
          <option value="suggested">{t("altro.alm.f.suggested")}</option>
          <option value="none">{t("altro.alm.f.none")}</option>
          <option value="confirmed">{t("altro.alm.f.confirmed")}</option>
          <option value="ignored">{t("altro.alm.f.ignored")}</option>
        </Select>
      </div>
      {discover.data && (
        <p role="status" className="m-0 text-[13px] text-muted">
          {t("altro.alm.discovered", { found: discover.data.found, new: discover.data.new, suggested: discover.data.suggested })}
          {discover.data.missing > 0 && t("altro.alm.discoveredMissing", { missing: discover.data.missing })}.
        </p>
      )}
      <MutError errors={[discover.error, accept.error, units.error]} />
      <p className="m-0 max-w-[720px] text-[13px] text-muted">{t("altro.alm.unitsNote", { source: kind ? t(kind.label) : it.config.app_source })}</p>
      <ul className="m-0 flex list-none flex-col border-t-2 border-rule p-0">
        {items.map((u) => (
          <UnitRow
            key={u.id + u.status + (u.application?.id ?? "")}
            it={it}
            u={u}
            apps={appOptions}
            checked={chosen.includes(u.id)}
            onCheck={(on) => setChosen((c) => (on ? [...c, u.id] : c.filter((x) => x !== u.id)))}
            onChanged={refresh}
          />
        ))}
        {units.data && items.length === 0 && <li className="py-6 text-muted">{t("altro.alm.noUnits")}</li>}
      </ul>
    </div>
  );
}

function UnitRow({
  it,
  u,
  apps,
  checked,
  onCheck,
  onChanged,
}: {
  it: ALMIntegration;
  u: ALMUnit;
  apps: { id: string; label: string }[];
  checked: boolean;
  onCheck: (on: boolean) => void;
  onChanged: () => void;
}) {
  const t = useT();
  const [app, setApp] = useState(u.application?.id ?? u.suggestion?.application.id ?? "");
  const map = useMutation({
    mutationFn: (status: "confirmed" | "ignored" | "none") => almApi.mapUnit(it.id, u.id, status === "confirmed" ? { status, application_id: app } : { status }),
    onSuccess: onChanged,
  });
  const st = UNIT_STATUS[u.status] ?? UNIT_STATUS.none;
  return (
    <li className={`flex min-w-0 flex-col gap-2 border-b border-hair py-3 ${u.missing ? "opacity-60" : ""}`}>
      <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
        {u.status === "suggested" && <input type="checkbox" aria-label={t("altro.alm.choose", { name: u.name })} checked={checked} onChange={(e) => onCheck(e.target.checked)} />}
        <span className="break-words font-medium">{u.name}</span>
        <Tag>{t(UNIT_KIND[u.kind] ?? UNIT_KIND.value)}</Tag>
        <Tag tone={st.tone}>{t(st.label)}</Tag>
        {u.missing && <Tag tone="flag">{t("altro.alm.missingTag")}</Tag>}
        <span className="ml-auto text-[12px] text-muted">{u.last_synced_at ? t("altro.alm.readAgo", { when: ago(u.last_synced_at) }) : ""}</span>
      </div>
      <span className="flex flex-wrap gap-x-3 gap-y-0.5 text-[12px] text-muted">
        <span className="min-w-0 [overflow-wrap:anywhere] font-mono">{u.path}</span>
        <span className="tabular">{t("altro.alm.unitTestSets", { count: u.test_sets })}</span>
      </span>
      {u.status === "confirmed" && u.application && (
        <span className="text-[13px]">
          <Trans
            k="altro.alm.confirmedLine"
            slots={{
              name: <Link to="/e/$key" params={{ key: u.application.key || u.application.id }} className="font-medium">{u.application.name}</Link>,
              code: <span className="font-mono text-[11.5px] text-muted">{u.application.code || u.application.key}</span>,
            }}
          />
        </span>
      )}
      {u.status !== "confirmed" && u.suggestion && (
        <span className="text-[13px]">
          <Trans
            k="altro.alm.suggestionLine"
            slots={{
              name: <span className="font-medium">{u.suggestion.application.name}</span>,
              score: <span className="font-mono text-[11.5px] text-muted tabular">{Math.round(u.suggestion.score * 100)}%</span>,
              reason: <span className="text-muted">{u.suggestion.reason}</span>,
            }}
          />
        </span>
      )}
      {u.status !== "confirmed" ? (
        <div className="flex flex-wrap items-end gap-2">
          <label className="flex min-w-0 flex-[2_1_220px] flex-col gap-1">
            <span className="label">{t("altro.alm.application")}</span>
            <Select className="field" value={app} onChange={(e) => setApp(e.target.value)}>
              <option value="">{t("altro.alm.pick")}</option>
              {apps.map((a) => (
                <option key={a.id} value={a.id}>{a.label}</option>
              ))}
            </Select>
          </label>
          <button className="btn btn-primary py-1.5" disabled={!app || map.isPending} onClick={() => map.mutate("confirmed")}>
            {u.suggestion && app === u.suggestion.application.id ? t("altro.alm.confirm") : t("altro.alm.link")}
          </button>
          {u.status !== "ignored" ? (
            <button className="btn py-1.5" disabled={map.isPending} onClick={() => map.mutate("ignored")}>{t("altro.alm.ignore")}</button>
          ) : (
            <button className="btn py-1.5" disabled={map.isPending} onClick={() => map.mutate("none")}>{t("altro.alm.resume")}</button>
          )}
        </div>
      ) : (
        <div>
          <button type="button" className="text-[12px] text-muted underline" disabled={map.isPending} onClick={() => map.mutate("none")}>
            {t("altro.alm.unlink")}
          </button>
        </div>
      )}
      <MutError errors={[map.error]} />
    </li>
  );
}

// ---------------------------------------------------------------------
// Test set letti
// ---------------------------------------------------------------------

function TestSetsTab({ it }: { it: ALMIntegration }) {
  const t = useT();
  const [change, setChange] = useState("");
  const wanted = useDebounced(change.trim(), 300);
  const sets = useQuery({
    queryKey: ["alm-test-sets", it.id, wanted],
    queryFn: () => almApi.testSets({ integration: it.id, change: wanted, limit: 200 }),
  });
  const items = sets.data?.items ?? [];
  return (
    <div className="flex min-w-0 flex-col gap-4">
      <p className="m-0 max-w-[720px] text-[13px] text-muted">{t("altro.alm.testSetsIntro")}</p>
      <label className="flex max-w-[320px] flex-col gap-1.5">
        <span className="label">{t("altro.alm.changeFilter")}</span>
        <input className="field font-mono" type="search" placeholder="CHG0045120" /* i18n-ok: esempio di numero di change */ value={change} onChange={(e) => setChange(e.target.value)} />
      </label>
      <MutError errors={[sets.error]} />
      <section className="flex min-w-0 flex-col gap-1">
        <SectionTitle aside={<span className="font-mono text-[11.5px] text-muted tabular">{items.length}</span>}>{t("altro.alm.tab.testset")}</SectionTitle>
        <ul className="m-0 flex list-none flex-col p-0">
          {items.map((x) => (
            <TestSetRow key={x.id} x={x} />
          ))}
          {sets.data && items.length === 0 && <li className="py-3 text-muted">{wanted ? t("altro.alm.noTestSetsFor", { change: wanted }) : t("altro.alm.noTestSets")}</li>}
        </ul>
      </section>
    </div>
  );
}

const COUNT_KEYS: { k: keyof ALMTestSet["counts"]; label: Key; tone?: string }[] = [
  { k: "passed", label: "altro.alm.count.passed", tone: "text-ok" },
  { k: "failed", label: "altro.alm.count.failed", tone: "text-danger" },
  { k: "blocked", label: "altro.alm.count.blocked", tone: "text-flag" },
  { k: "not_completed", label: "altro.alm.count.not_completed" },
  { k: "no_run", label: "altro.alm.count.no_run" },
  { k: "na", label: "altro.alm.count.na" },
];

function TestSetRow({ x }: { x: ALMTestSet }) {
  const t = useT();
  const c = x.counts;
  const where = [x.project, x.folder].filter(Boolean).join(" · ");
  const when = [x.cycle, x.release].filter(Boolean).join(" · ");
  return (
    <li className="flex min-w-0 flex-col gap-0.5 border-b border-hair py-2">
      <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
        <span className="break-words font-medium">{x.name}</span>
        {x.status && <Tag>{x.status}</Tag>}
        {x.change ? <span className="font-mono text-[12px]">{x.change}</span> : <Tag tone="flag">{t("altro.alm.noChange")}</Tag>}
        {x.open_defects > 0 && <Tag tone="danger">{t("altro.alm.openDefects", { count: x.open_defects })}</Tag>}
        <span className="ml-auto text-[12px] text-muted">{x.updated_at ? t("altro.alm.readAgo", { when: ago(x.updated_at) }) : ""}</span>
      </div>
      <span className="flex flex-wrap gap-x-3 gap-y-0.5 text-[12px] text-muted">
        {where && <span className="min-w-0 [overflow-wrap:anywhere]">{where}</span>}
        {when && <span className="min-w-0 [overflow-wrap:anywhere]">{when}</span>}
        {x.application ? (
          <Link to="/e/$key" params={{ key: x.application }} className="font-mono">{x.application}</Link>
        ) : (
          <span>{t("altro.alm.noApplication")}</span>
        )}
      </span>
      <span className="flex flex-wrap gap-x-2 text-[12px] tabular">
        <span className="font-semibold">{t("altro.alm.count.planned", { n: fmtNumber(c.planned) })}</span>
        {COUNT_KEYS.filter((k) => c[k.k] > 0).map((k) => (
          <span key={k.k} className={k.tone ?? "text-muted"}>· {t(k.label, { n: fmtNumber(c[k.k]) })}</span>
        ))}
      </span>
      {x.url && (
        <a className="self-start text-[12px]" href={x.url}>{t("altro.alm.openInAlm")}</a>
      )}
    </li>
  );
}

// ---------------------------------------------------------------------
// Sincronizzazioni
// ---------------------------------------------------------------------

function SyncTab({ it }: { it: ALMIntegration }) {
  const t = useT();
  const qc = useQueryClient();
  const runs = useQuery({ queryKey: ["alm-runs", it.id], queryFn: () => almApi.runs(it.id), refetchInterval: it.sync.running ? 2_000 : 10_000 });
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["alm-runs", it.id] });
    qc.invalidateQueries({ queryKey: ["alm-integrations"] });
    qc.invalidateQueries({ queryKey: ["alm-test-sets"] });
    qc.invalidateQueries({ queryKey: ["alm-units", it.id] });
  };
  const sync = useMutation({ mutationFn: (full: boolean) => almApi.sync(it.id, full), onSuccess: () => setTimeout(refresh, 600) });
  return (
    <div className="flex min-w-0 flex-col gap-5">
      <dl className="m-0 max-w-[760px]">
        <KV label={t("altro.alm.state")}>
          <SyncTag status={it.sync.running ? "running" : it.sync.last_status} />{" "}
          {it.sync.last_finished_at && <span className="text-muted">{t("altro.alm.lastShort", { when: dateTime(it.sync.last_finished_at) })}</span>}
          {it.sync.last_error && <span className="block break-words text-[12px] text-danger">{it.sync.last_error}</span>}
        </KV>
        <KV label={t("altro.alm.next")}>
          {it.active ? (it.sync.next_run_at ? dateTime(it.sync.next_run_at) : t("altro.alm.withinMinute")) : t("altro.alm.deactivated")}
        </KV>
        <KV label={t("altro.alm.intervalLabel")}>{t("altro.alm.everyMinutes", { count: it.config.interval_minutes })}</KV>
        <KV label={t("altro.alm.firstRead")}>{t("altro.alm.daysBack", { count: it.config.lookback_days })}</KV>
      </dl>
      <div className="flex flex-wrap gap-2">
        <button className="btn btn-primary py-1.5" disabled={sync.isPending || it.sync.running || !it.active} onClick={() => sync.mutate(false)}>
          {t("altro.alm.syncNow")}
        </button>
        <ConfirmButton
          label={t("altro.alm.rereadAll")}
          confirm={t("altro.alm.rereadConfirm")}
          pending={sync.isPending}
          disabled={it.sync.running || !it.active}
          onConfirm={() => sync.mutate(true)}
        />
      </div>
      <p className="m-0 max-w-[720px] text-[12px] text-muted">{t("altro.alm.licenseNote")}</p>
      {it.units.confirmed === 0 && <p className="m-0 text-[13px] text-flag">{t("altro.alm.noConfirmed")}</p>}
      <MutError errors={[sync.error, runs.error]} />
      <section className="flex flex-col gap-2">
        <SectionTitle aside={<span className="font-mono text-[11.5px] text-muted tabular">{runs.data?.items.length ?? 0}</span>}>{t("altro.alm.latest")}</SectionTitle>
        <ul className="m-0 flex list-none flex-col p-0">
          {(runs.data?.items ?? []).map((r) => (
            <RunRow key={r.id} r={r} />
          ))}
          {runs.data?.items.length === 0 && <li className="py-3 text-muted">{t("altro.alm.noRuns")}</li>}
        </ul>
      </section>
    </div>
  );
}

function RunRow({ r }: { r: ALMRun }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const s = r.stats;
  const notes = (s.errors?.length ?? 0) + (s.warnings?.length ?? 0);
  return (
    <li className="flex flex-col gap-0.5 border-b border-hair py-2">
      <div className="flex flex-wrap items-baseline gap-x-2">
        <SyncTag status={r.status} />
        <span className="font-medium">{TRIGGER[r.trigger] ? t(TRIGGER[r.trigger]) : r.trigger}</span>
        <span className="text-[12px] text-muted">{r.scope}</span>
        <span className="ml-auto font-mono text-[11.5px] text-muted tabular">{dateTime(r.started_at)}</span>
      </div>
      <span className="text-[12px] text-muted tabular">
        {t("altro.alm.statsLine", {
          projects: s.projects,
          testSets: s.test_sets,
          newTestSets: s.new_test_sets,
          linked: s.linked,
          runs: s.runs,
          newRuns: s.new_runs,
          defects: s.defects,
          requests: s.requests,
          seconds: fmtNumber(s.duration_ms / 1000, { maximumFractionDigits: 1 }),
        })}
        {s.new_units ? <> · {t("altro.alm.statsUnits", { count: s.new_units })}</> : null}
      </span>
      {r.error && <span className="break-words text-[12px] text-danger">{r.error}</span>}
      {notes > 0 && (
        <button type="button" className="self-start text-[12px] text-muted underline" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
          {open ? t("altro.alm.hideDetails") : t("altro.alm.notes", { count: notes })}
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
