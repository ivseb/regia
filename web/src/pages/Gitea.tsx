// Configurazione → Integrazioni → Gitea (DevSecOps): integrazioni con un Gitea self-hosted
// (anche http su una rete interna) o gitea.com, prova di connessione, repository dei proprietari
// con la proposta di associazione alle applicazioni, profili di pipeline con l'anteprima, stato
// delle sincronizzazioni. Solo classi e variabili del tema.
import { Select } from "../select";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError, api } from "../api";
import { githubApi, type Preview, type Profile, type ProfileProblem, type Repository, type SyncStatus } from "../githubApi";
import {
  giteaApi,
  type GTIntegration,
  type GTIntegrationInput,
  type GTIntegrationList,
  type GTRepo,
  type GTRun,
  type GTTestResult,
} from "../giteaApi";
import { PageHeader } from "../shell";
import { ErrorNote, SectionTitle, ago } from "../ui";
import { ConfirmButton, CopyBox, KV, MutError, SidePanel, Tag, dateTime } from "./adminUi";
import { useT } from "../i18n/react";
import { Trans } from "../i18n/Trans";
import type { Key } from "../i18n/core";
import { fmtNumber, fmtTime } from "../i18n/format";

export type GiteaTab = "integrazioni" | "repository" | "profili" | "sincronizzazioni";
export const GITEA_TABS: GiteaTab[] = ["integrazioni", "repository", "profili", "sincronizzazioni"];
const TAB_LABELS: Record<GiteaTab, Key> = {
  integrazioni: "altro.gitea.tab.integrazioni",
  repository: "altro.gitea.tab.repository",
  profili: "altro.gitea.tab.profili",
  sincronizzazioni: "altro.gitea.tab.sincronizzazioni",
};
const NEW = "nuovo";
const PATH = "/configurazione/integrazioni/gitea";

const SYNC: Record<SyncStatus, { label: Key; tone: "ok" | "flag" | "danger" | "muted" }> = {
  running: { label: "altro.gitea.sync.running", tone: "muted" },
  ok: { label: "altro.gitea.sync.ok", tone: "ok" },
  partial: { label: "altro.gitea.sync.partial", tone: "flag" },
  error: { label: "altro.gitea.sync.error", tone: "danger" },
  rate_limited: { label: "altro.gitea.sync.rate_limited", tone: "flag" },
};
const TRIGGER: Record<GTRun["trigger"], Key> = { schedule: "altro.gitea.trigger.schedule", manual: "altro.gitea.trigger.manual", webhook: "altro.gitea.trigger.webhook" };

type Search = { tab?: GiteaTab; sel?: string; integrazione?: string };

export function GiteaPage({ tab, sel, integration }: { tab: GiteaTab; sel?: string; integration?: string }) {
  const t = useT();
  const navigate = useNavigate();
  const go = (s: Search) => navigate({ to: PATH, search: { tab, ...s } });
  const list = useQuery({ queryKey: ["gt-integrations"], queryFn: giteaApi.list, refetchInterval: 10_000 });
  if (list.error instanceof ApiError && list.error.status === 403) {
    return (
      <>
        <PageHeader path={t("altro.gitea.path")} title="Gitea" /* i18n-ok: nome del prodotto */ />
        <p className="px-4 text-muted md:px-7">{t("altro.gitea.needAdmin")}</p>
      </>
    );
  }
  const items = list.data?.items ?? [];
  const current = items.find((i) => i.id === integration) ?? items[0];
  return (
    <>
      <PageHeader
        path={t("altro.gitea.path")}
        title="Gitea" /* i18n-ok: nome del prodotto */
        actions={tab === "integrazioni" ? <button className="btn btn-primary" onClick={() => go({ sel: NEW })}>{t("altro.gitea.newIntegration")}</button> : undefined}
      />
      <div role="tablist" aria-label={t("altro.gitea.sections")} className="flex gap-6 overflow-x-auto border-b border-hair px-4 md:px-7">
        {GITEA_TABS.map((k) => (
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
      {list.data && !list.data.storing && (
        <p className="mx-4 mt-4 max-w-[760px] border-l-2 border-flag pl-3 text-[13px] md:mx-7">
          {t("altro.gitea.notStoring")}
        </p>
      )}
      <ErrorNote error={list.error} />
      {tab === "integrazioni" && list.data && <IntegrationsTab data={list.data} selected={sel} onSelect={(s) => go({ sel: s })} />}
      {tab !== "integrazioni" && tab !== "profili" && list.data && items.length === 0 && (
        <p className="px-4 py-6 text-muted md:px-7">
          <Trans k="altro.gitea.noIntegrationYet" slots={{ link: <Link to={PATH} search={{ tab: "integrazioni", sel: NEW }}>{t("altro.gitea.tab.integrazioni")}</Link> }} />
        </p>
      )}
      {tab === "repository" && current && (
        <IntegrationPicker items={items} current={current} onPick={(id) => go({ integrazione: id })}>
          <ReposTab key={current.id} it={current} />
        </IntegrationPicker>
      )}
      {tab === "sincronizzazioni" && current && (
        <IntegrationPicker items={items} current={current} onPick={(id) => go({ integrazione: id })}>
          <SyncTab key={current.id} it={current} />
        </IntegrationPicker>
      )}
      {tab === "profili" && <ProfilesTab integrations={items} selected={sel} onSelect={(s) => go({ sel: s })} />}
    </>
  );
}

function IntegrationPicker({ items, current, onPick, children }: { items: GTIntegration[]; current: GTIntegration; onPick: (id: string) => void; children: ReactNode }) {
  const t = useT();
  return (
    <div className="flex flex-col gap-4 px-4 py-5 md:px-7">
      {items.length > 1 && (
        <label className="flex max-w-[420px] flex-col gap-1.5">
          <span className="label">{t("altro.gitea.integration")}</span>
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
  if (!status) return <Tag>{t("altro.gitea.neverSynced")}</Tag>;
  const s = SYNC[status];
  return <Tag tone={s.tone}>{t(s.label)}</Tag>;
}

function rateText(t: ReturnType<typeof useT>, r: { remaining: number; limit: number; reset: string } | null | undefined) {
  if (!r) return t("altro.gitea.rateNone");
  return t("altro.gitea.rate", { remaining: fmtNumber(r.remaining), limit: fmtNumber(r.limit), time: fmtTime(r.reset, { hour: "2-digit", minute: "2-digit" }) });
}

function lines(s: string): string[] {
  return s
    .split(/[\n,]/)
    .map((x) => x.trim())
    .filter(Boolean);
}

// ---------------------------------------------------------------------
// Integrazioni
// ---------------------------------------------------------------------

function IntegrationsTab({ data, selected, onSelect }: { data: GTIntegrationList; selected?: string; onSelect: (s?: string) => void }) {
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
        <p className="m-0 max-w-[680px] text-muted">
          {t("altro.gitea.intro")}
        </p>
        {secret && (
          <CopyBox
            title={t("altro.gitea.secretTitle")}
            value={secret}
            note={t("altro.gitea.secretNote")}
            onDone={() => setSecret(null)}
          />
        )}
        <ul className="m-0 flex list-none flex-col p-0">
          {data.items.map((i) => (
            <li key={i.id} className={`border-b border-hair py-2.5 ${i.id === selected ? "bg-sel" : ""}`}>
              <button className="flex w-full flex-wrap items-baseline gap-x-2 text-left" aria-current={i.id === selected ? "true" : undefined} onClick={() => onSelect(i.id)}>
                <span className="font-medium hover:underline">{i.name}</span>
                {!i.active && <Tag>{t("altro.gitea.inactive")}</Tag>}
                <SyncTag status={i.sync.last_status} />
                {i.repos.suggested > 0 && <Tag tone="accent">{t("altro.gitea.toLink", { count: i.repos.suggested })}</Tag>}
                <span className="ml-auto text-[12px] text-muted">{i.sync.last_finished_at ? t("altro.gitea.lastRead", { when: ago(i.sync.last_finished_at) }) : t("altro.gitea.neverRead")}</span>
              </button>
              <span className="block min-w-0 [overflow-wrap:anywhere] font-mono text-[11.5px] text-muted" title={i.config.base_url}>
                {i.config.base_url} · {[...(i.config.owners ?? []), ...(i.config.repositories ?? [])].join(", ")}
              </span>
              <span className="block text-[12px] text-muted tabular">
                {t("altro.gitea.summary", { confirmed: i.repos.confirmed, total: i.repos.total })}
                {i.profile_name && <> · {t("altro.gitea.profileOf", { name: i.profile_name, version: i.profile_version ?? "" })}</>}
              </span>
            </li>
          ))}
          {data.items.length === 0 && <li className="py-8 text-muted">{t("altro.gitea.noIntegrations")}</li>}
        </ul>
        <TokenHelp />
      </div>
      {selected && (
        <div ref={panel} className="flex min-w-0 scroll-mt-14 flex-[1_1_460px]">
          <SidePanel label={t("altro.gitea.panelLabel")} onClose={() => onSelect(undefined)}>
            {selected === NEW ? (
              <IntegrationForm data={data} onSaved={(id) => onSelect(id)} onCancel={() => onSelect(undefined)} />
            ) : it ? (
              <IntegrationPanel key={it.id} it={it} data={data} onSecret={setSecret} onDeleted={() => onSelect(undefined)} />
            ) : (
              <p className="text-muted">{t("altro.gitea.notFound")}</p>
            )}
          </SidePanel>
        </div>
      )}
    </div>
  );
}

function TokenHelp() {
  const t = useT();
  return (
    <section className="flex max-w-[680px] flex-col gap-2">
      <SectionTitle>{t("altro.gitea.tokenHelpTitle")}</SectionTitle>
      <ol className="m-0 flex list-decimal flex-col gap-1 pl-5">
        <li><Trans k="altro.gitea.tokenHelp1" slots={{ add: <span className="font-medium">{t("altro.gitea.tokenAdd")}</span> }} /></li>
        <li>
          <Trans /* i18n-ok: nomi tecnici */ k="altro.gitea.tokenHelp2" slots={{ scope: <span className="font-mono text-[12px]">read:repository</span>, org: <span className="font-mono text-[12px]">read:organization</span> }} />
        </li>
        <li>{t("altro.gitea.tokenHelp3")}</li>
        <li>{t("altro.gitea.tokenHelp4")}</li>
      </ol>
      <p className="m-0 text-muted">{t("altro.gitea.tokenHelpGuide")}</p>
    </section>
  );
}

function IntegrationForm({ it, data, onSaved, onCancel }: { it?: GTIntegration; data: GTIntegrationList; onSaved: (id: string) => void; onCancel?: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const profiles = useQuery({ queryKey: ["gh-profiles"], queryFn: githubApi.profiles });
  const c = it?.config;
  const [name, setName] = useState(it?.name ?? "Gitea");
  const [baseURL, setBaseURL] = useState(c?.base_url.replace(/\/api\/v1$/, "") ?? "");
  const [owners, setOwners] = useState((c?.owners ?? []).join(", "));
  const [repos, setRepos] = useState((c?.repositories ?? []).join("\n"));
  const [token, setToken] = useState("");
  const [hook, setHook] = useState("");
  const [interval, setInterval] = useState(c?.interval_minutes ?? 30);
  const [profileID, setProfileID] = useState(c?.profile_id ?? "");
  const [verdict, setVerdict] = useState(c?.verdict_check ?? "");
  const [env, setEnv] = useState(c?.release_environment ?? "");
  const [priv, setPriv] = useState(c?.private_network ?? false);
  const [ca, setCA] = useState(c?.ca_certificate ?? "");
  const [archived, setArchived] = useState(c?.include_archived ?? false);
  const [active, setActive] = useState(it?.active ?? true);
  const [byRepo, setByRepo] = useState((c?.repositories ?? []).length > 0 && (c?.owners ?? []).length === 0);
  const save = useMutation({
    mutationFn: async () => {
      const body: GTIntegrationInput = {
        name, active, base_url: baseURL, owners: byRepo ? [] : lines(owners), repositories: byRepo ? lines(repos) : [],
        token: token || undefined, webhook_secret: hook || undefined, interval_minutes: Number(interval) || 30, profile_id: profileID,
        verdict_check: verdict, release_environment: env, private_network: priv, ca_certificate: ca, include_archived: archived,
      };
      if (it) {
        await giteaApi.update(it.id, body);
        return it.id;
      }
      return (await giteaApi.create(body)).id;
    },
    onSuccess: (id) => {
      qc.invalidateQueries({ queryKey: ["gt-integrations"] });
      setToken("");
      setHook("");
      onSaved(id);
    },
  });
  const err = save.error instanceof ApiError ? save.error : null;
  const bad = (f: string) => err?.field === f;
  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      {!it && <SectionTitle>{t("altro.gitea.newIntegration")}</SectionTitle>}
      <label className="flex flex-col gap-1.5">
        <span className="label">{t("altro.gitea.name")}</span>
        <input className="field" required maxLength={80} value={name} aria-invalid={bad("name")} onChange={(e) => setName(e.target.value)} />
      </label>
      <label className="flex flex-col gap-1.5">
        <span className="label">{t("altro.gitea.address")}</span>
        <input className="field font-mono" required value={baseURL} aria-invalid={bad("base_url")} onChange={(e) => setBaseURL(e.target.value)} />
        <span className="text-[12px] text-muted">{t("altro.gitea.addressHelp")}</span>
      </label>
      <fieldset className="m-0 flex flex-col gap-2 border-0 p-0">
        <legend className="label mb-1.5 p-0">{t("altro.gitea.what")}</legend>
        <label className="flex items-center gap-2">
          <input type="radio" name="scope" checked={!byRepo} onChange={() => setByRepo(false)} /> {t("altro.gitea.scopeOwners")}
        </label>
        <label className="flex items-center gap-2">
          <input type="radio" name="scope" checked={byRepo} onChange={() => setByRepo(true)} /> {t("altro.gitea.scopeRepos")}
        </label>
      </fieldset>
      {!byRepo ? (
        <label className="flex flex-col gap-1.5">
          <span className="label">{t("altro.gitea.owners")}</span>
          <input className="field font-mono" required placeholder={t("altro.gitea.ownersPh")} value={owners} aria-invalid={bad("owners")} onChange={(e) => setOwners(e.target.value)} />
        </label>
      ) : (
        <label className="flex flex-col gap-1.5">
          <span className="label">{t("altro.gitea.repos")}</span>
          <textarea className="field min-h-12 font-mono text-[12px]" required placeholder={t("altro.gitea.reposPh")} value={repos} aria-invalid={bad("repositories")} onChange={(e) => setRepos(e.target.value)} />
        </label>
      )}
      <label className="flex flex-col gap-1.5">
        <span className="label">{t("altro.gitea.tokenLabel")}</span>
        <input
          className="field font-mono"
          type="password"
          autoComplete="off"
          required={!it?.has_token}
          placeholder={it?.has_token ? t("altro.gitea.savedKeep") : t("altro.gitea.tokenPh")}
          value={token}
          aria-invalid={bad("token")}
          onChange={(e) => setToken(e.target.value)}
        />
      </label>
      <label className="flex flex-col gap-1.5">
        <span className="label">{t("altro.gitea.profile")}</span>
        <Select className="field" value={profileID} aria-invalid={bad("profile_id")} onChange={(e) => setProfileID(e.target.value)}>
          <option value="">{t("altro.gitea.noProfile")}</option>
          {(profiles.data?.items ?? []).map((p) => (
            <option key={p.id} value={p.id}>{p.name} (v{p.version})</option>
          ))}
        </Select>
        <span className="text-[12px] text-muted">
          <Trans k="altro.gitea.profileHelp" slots={{ link: <Link to={PATH} search={{ tab: "profili" }}>{t("altro.gitea.profileHelpLink")}</Link> }} />
        </span>
      </label>
      <div className="flex flex-wrap gap-3">
        <label className="flex min-w-[140px] flex-1 flex-col gap-1.5">
          <span className="label">{t("altro.gitea.interval")}</span>
          <input className="field tabular" type="number" min={5} max={1440} value={interval} aria-invalid={bad("interval_minutes")} onChange={(e) => setInterval(Number(e.target.value))} />
        </label>
        <label className="flex min-w-[140px] flex-1 flex-col gap-1.5">
          <span className="label">{t("altro.gitea.verdictCheck")}</span>
          <input className="field font-mono" placeholder={t("altro.gitea.verdictPh")} value={verdict} onChange={(e) => setVerdict(e.target.value)} />
        </label>
        <label className="flex min-w-[140px] flex-1 flex-col gap-1.5">
          <span className="label">{t("altro.gitea.env")}</span>
          <input className="field font-mono" placeholder="prod" /* i18n-ok: valore d'esempio */ value={env} aria-invalid={bad("release_environment")} onChange={(e) => setEnv(e.target.value)} />
        </label>
      </div>
      <details className="flex flex-col gap-3">
        <summary className="cursor-pointer text-muted">{t("altro.gitea.advanced")}</summary>
        <div className="mt-3 flex flex-col gap-3">
          <label className="flex items-start gap-2">
            <input type="checkbox" className="mt-0.5" checked={priv} disabled={!data.allow_private && !priv} onChange={(e) => setPriv(e.target.checked)} />
            <span>
              {t("altro.gitea.internalNet")}
              <span className="block text-[12px] text-muted">
                {data.dev
                  ? t("altro.gitea.devMode")
                  : data.allow_private
                    ? t("altro.gitea.allowPriv")
                    : t("altro.gitea.noPriv")}
              </span>
            </span>
          </label>
          <label className="flex flex-col gap-1.5">
            <span className="label">{t("altro.gitea.ca")}</span>
            <textarea className="field min-h-12 font-mono text-[12px]" placeholder="-----BEGIN CERTIFICATE-----" value={ca} aria-invalid={bad("ca_certificate")} onChange={(e) => setCA(e.target.value)} />
          </label>
          <label className="flex flex-col gap-1.5">
            <span className="label">{t("altro.gitea.hook")}</span>
            <input
              className="field font-mono"
              type="password"
              autoComplete="off"
              placeholder={it?.has_webhook_secret ? t("altro.gitea.savedKeep") : t("altro.gitea.hookPh")}
              value={hook}
              aria-invalid={bad("webhook_secret")}
              onChange={(e) => setHook(e.target.value)}
            />
          </label>
          <label className="flex items-center gap-2">
            <input type="checkbox" checked={archived} onChange={(e) => setArchived(e.target.checked)} /> {t("altro.gitea.archived")}
          </label>
        </div>
      </details>
      <label className="flex items-center gap-2">
        <input type="checkbox" checked={active} onChange={(e) => setActive(e.target.checked)} /> {t("altro.gitea.auth.active")}
      </label>
      <ErrorNote error={save.error} />
      <div className="flex flex-wrap gap-2">
        <button className="btn btn-primary" disabled={save.isPending}>{it ? t("altro.gitea.save") : t("altro.gitea.create")}</button>
        {onCancel && <button type="button" className="btn" onClick={onCancel}>{t("altro.gitea.cancel")}</button>}
      </div>
    </form>
  );
}

// Il percorso guidato: cosa è fatto e cosa manca, con il prossimo passo a un clic.
function Steps({ it, tested }: { it: GTIntegration; tested: GTTestResult | null }) {
  const t = useT();
  const steps: { done: boolean; label: string; to?: { tab: GiteaTab }; hint: string }[] = [
    { done: it.has_token, label: t("altro.gitea.step.token"), hint: t("altro.gitea.step.tokenHint") },
    { done: !!tested?.ok, label: t("altro.gitea.step.tested"), hint: t("altro.gitea.step.testedHint") },
    { done: it.repos.total > 0, label: t("altro.gitea.step.found"), to: { tab: "repository" }, hint: t("altro.gitea.step.foundHint") },
    { done: it.repos.confirmed > 0, label: t("altro.gitea.step.linked"), to: { tab: "repository" }, hint: t("altro.gitea.step.linkedHint") },
    { done: !!it.sync.last_finished_at && it.sync.last_status !== "error", label: t("altro.gitea.step.synced"), to: { tab: "sincronizzazioni" }, hint: t("altro.gitea.step.syncedHint") },
  ];
  const next = steps.findIndex((s) => !s.done);
  return (
    <ol className="m-0 flex list-none flex-col gap-1 p-0" aria-label={t("altro.gitea.progress")}>
      {steps.map((s, i) => (
        <li key={s.label} className="flex items-baseline gap-2 text-[13px]">
          <span className={`w-4 shrink-0 text-center font-mono ${s.done ? "text-ok" : i === next ? "text-accent" : "text-muted"}`} aria-hidden>{s.done ? "✓" : i + 1}</span>
          <span className={s.done ? "text-muted" : i === next ? "font-medium" : "text-muted"}>
            {s.label}
            {i === next && (
              <>
                {" "}
                <span className="font-normal text-muted">
                  — {s.to ? <Link to={PATH} search={{ tab: s.to.tab, integrazione: it.id }}>{s.hint}</Link> : s.hint}
                </span>
              </>
            )}
          </span>
        </li>
      ))}
    </ol>
  );
}

function IntegrationPanel({ it, data, onSecret, onDeleted }: { it: GTIntegration; data: GTIntegrationList; onSecret: (s: string) => void; onDeleted: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const [result, setResult] = useState<GTTestResult | null>(null);
  const test = useMutation({ mutationFn: () => giteaApi.test(it.id), onSuccess: setResult });
  const secret = useMutation({
    mutationFn: () => giteaApi.webhookSecret(it.id),
    onSuccess: (r) => {
      onSecret(r.secret);
      qc.invalidateQueries({ queryKey: ["gt-integrations"] });
    },
  });
  const del = useMutation({
    mutationFn: () => giteaApi.remove(it.id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["gt-integrations"] });
      onDeleted();
    },
  });
  const webhookURL = (data.base_url || window.location.origin) + it.webhook_path;
  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-col gap-1">
        <h2 className="m-0 text-[22px] font-bold">{it.name}</h2>
        <span className="text-[12px] text-muted">{t("altro.gitea.created", { date: dateTime(it.created_at), when: ago(it.updated_at) })}</span>
      </div>
      <Steps it={it} tested={result} />
      <div className="flex flex-wrap gap-2">
        <button className="btn btn-primary py-1.5" disabled={test.isPending} onClick={() => test.mutate()}>
          {test.isPending ? t("altro.gitea.testing") : t("altro.gitea.test")}
        </button>
        <Link className="btn py-1.5 no-underline" to={PATH} search={{ tab: "repository", integrazione: it.id }}>{t("altro.gitea.tab.repository")}</Link>
        <Link className="btn py-1.5 no-underline" to={PATH} search={{ tab: "sincronizzazioni", integrazione: it.id }}>{t("altro.gitea.tab.sincronizzazioni")}</Link>
      </div>
      {result && (
        <div role="status" className={`flex flex-col gap-1 border-l-2 pl-3 ${result.ok ? "border-ok" : "border-danger"}`}>
          <span className={`font-semibold ${result.ok ? "text-ok" : "text-danger"}`}>{result.message}</span>
          {result.details.map((d) => (
            <span key={d} className="text-[12.5px] text-muted">{d}</span>
          ))}
          {result.warnings.map((w) => (
            <span key={w} className="text-[12.5px] text-flag">{w}</span>
          ))}
        </div>
      )}
      <MutError errors={[test.error, secret.error, del.error]} />
      <dl className="m-0">
        <KV label={t("altro.gitea.tokenLabelShort")}>{it.has_token ? t("altro.gitea.tokenLine") : t("altro.gitea.tokenMissing")}</KV>
        <KV label="Webhook">
          <span className="block break-all font-mono text-[12px]">{webhookURL}</span>
          <span className="text-[12px] text-muted">
            {it.has_webhook_secret ? t("altro.gitea.secretSet") : t("altro.gitea.noSecret")}
          </span>
        </KV>
        <KV label={t("altro.gitea.lastReadLabel")}>
          <SyncTag status={it.sync.last_status} /> {it.sync.last_finished_at && dateTime(it.sync.last_finished_at)}
          {it.sync.last_error && <span className="block text-[12px] text-danger">{it.sync.last_error}</span>}
        </KV>
        <KV label={t("altro.gitea.rateLimits")}>{rateText(t, it.sync.rate_limit)}</KV>
      </dl>
      <div className="flex flex-wrap gap-2">
        <ConfirmButton
          label={it.has_webhook_secret ? t("altro.gitea.newSecret") : t("altro.gitea.genSecret")}
          confirm={it.has_webhook_secret ? t("altro.gitea.replaceSecret") : t("altro.gitea.generate")}
          pending={secret.isPending}
          onConfirm={() => secret.mutate()}
        />
        <ConfirmButton label={t("altro.gitea.delete")} confirm={t("altro.gitea.deleteIntegration")} danger pending={del.isPending} onConfirm={() => del.mutate()} />
      </div>
      <IntegrationForm key={it.updated_at} it={it} data={data} onSaved={() => undefined} />
    </div>
  );
}

// ---------------------------------------------------------------------
// Progetti
// ---------------------------------------------------------------------

const PROJECT_STATUS: Record<Repository["status"], { label: Key; tone: "ok" | "accent" | "muted" | "fg" }> = {
  confirmed: { label: "altro.gitea.status.confirmed", tone: "ok" },
  suggested: { label: "altro.gitea.status.suggested", tone: "accent" },
  none: { label: "altro.gitea.status.none", tone: "fg" },
  ignored: { label: "altro.gitea.status.ignored", tone: "muted" },
};

function ReposTab({ it }: { it: GTIntegration }) {
  const t = useT();
  const qc = useQueryClient();
  const repos = useQuery({ queryKey: ["gt-repos", it.id], queryFn: () => giteaApi.repos(it.id) });
  const apps = useQuery({ queryKey: ["applications", ""], queryFn: () => api.applications("") });
  const [filter, setFilter] = useState<"tutti" | GTRepo["status"]>("tutti");
  const [text, setText] = useState("");
  const [chosen, setChosen] = useState<string[]>([]);
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["gt-repos", it.id] });
    qc.invalidateQueries({ queryKey: ["gt-integrations"] });
  };
  const discover = useMutation({ mutationFn: () => giteaApi.discover(it.id), onSuccess: refresh });
  const accept = useMutation({ mutationFn: () => giteaApi.accept(it.id, chosen), onSuccess: () => { setChosen([]); refresh(); } });
  const all = repos.data?.items ?? [];
  const items = all.filter((r) => (filter === "tutti" || r.status === filter) && (!text || r.full_name.toLowerCase().includes(text.toLowerCase())));
  const suggested = all.filter((r) => r.status === "suggested");
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2">
        <button className="btn btn-primary py-1.5" disabled={discover.isPending} onClick={() => discover.mutate()}>
          {discover.isPending ? t("altro.gitea.reading") : t("altro.gitea.discover")}
        </button>
        <button className="btn py-1.5" disabled={accept.isPending || chosen.length === 0} onClick={() => accept.mutate()}>
          {t("altro.gitea.confirmChosen", { count: chosen.length })}
        </button>
        {suggested.length > 0 && (
          <button type="button" className="text-[12px] text-accent underline" onClick={() => setChosen(suggested.map((r) => r.id))}>
            {t("altro.gitea.chooseAll")}
          </button>
        )}
        <input className="field ml-auto w-auto min-w-[140px]" type="search" aria-label={t("altro.gitea.searchAria")} placeholder={t("altro.gitea.searchPh")} value={text} onChange={(e) => setText(e.target.value)} />
        <Select className="field w-auto" aria-label={t("altro.gitea.filter")} value={filter} onChange={(e) => setFilter(e.target.value as typeof filter)}>
          <option value="tutti">{t("altro.gitea.f.all")}</option>
          <option value="suggested">{t("altro.gitea.f.suggested")}</option>
          <option value="none">{t("altro.gitea.f.none")}</option>
          <option value="confirmed">{t("altro.gitea.f.confirmed")}</option>
          <option value="ignored">{t("altro.gitea.f.ignored")}</option>
        </Select>
      </div>
      {discover.data && (
        <p role="status" className="m-0 text-[13px] text-muted">
          {t("altro.gitea.discovered", { found: discover.data.found, new: discover.data.new, suggested: discover.data.suggested })}
          {discover.data.missing > 0 && t("altro.gitea.discoveredMissing", { missing: discover.data.missing })}.
        </p>
      )}
      <MutError errors={[discover.error, accept.error, repos.error]} />
      <p className="m-0 max-w-[720px] text-[13px] text-muted">
        {t("altro.gitea.reposNote")}
      </p>
      <ul className="m-0 flex list-none flex-col border-t-2 border-rule p-0">
        {items.map((r) => (
          <RepoRow
            key={r.id + r.status + (r.application?.id ?? "")}
            it={it}
            r={r}
            apps={(apps.data?.items ?? []).map((a) => ({ id: a.id, label: `${a.name}${a.code ? ` · ${a.code}` : ""}` }))}
            checked={chosen.includes(r.id)}
            onCheck={(on) => setChosen((c) => (on ? [...c, r.id] : c.filter((x) => x !== r.id)))}
            onChanged={refresh}
          />
        ))}
        {repos.data && items.length === 0 && <li className="py-6 text-muted">{t("altro.gitea.noRepos")}</li>}
      </ul>
    </div>
  );
}

function RepoRow({
  it,
  r,
  apps,
  checked,
  onCheck,
  onChanged,
}: {
  it: GTIntegration;
  r: GTRepo;
  apps: { id: string; label: string }[];
  checked: boolean;
  onCheck: (on: boolean) => void;
  onChanged: () => void;
}) {
  const t = useT();
  const [app, setApp] = useState(r.application?.id ?? r.suggestion?.application.id ?? "");
  const [svc, setSvc] = useState(r.service_name);
  const map = useMutation({
    mutationFn: (status: "confirmed" | "ignored" | "none") =>
      giteaApi.mapRepo(it.id, r.id, status === "confirmed" ? { status, application_id: app, service_name: svc } : { status }),
    onSuccess: onChanged,
  });
  const st = PROJECT_STATUS[r.status];
  return (
    <li className={`flex flex-col gap-2 border-b border-hair py-3 ${r.missing ? "opacity-60" : ""}`}>
      <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
        {r.status === "suggested" && <input type="checkbox" aria-label={t("altro.gitea.choose", { name: r.full_name })} checked={checked} onChange={(e) => onCheck(e.target.checked)} />}
        <a className="break-all font-mono text-[13px] font-medium" href={r.html_url} target="_blank" rel="noreferrer">{r.full_name}</a>
        <Tag tone={st.tone}>{t(st.label)}</Tag>
        {r.archived && <Tag>{t("altro.gitea.archivedTag")}</Tag>}
        {r.missing && <Tag tone="flag">{t("altro.gitea.missingTag")}</Tag>}
        {r.last_error && <Tag tone="danger" title={r.last_error}>{t("altro.gitea.errorTag")}</Tag>}
        <span className="ml-auto text-[12px] text-muted">{r.last_synced_at ? t("altro.gitea.readAgo", { when: ago(r.last_synced_at) }) : ""}</span>
      </div>
      {r.description && <span className="text-[12.5px] text-muted">{r.description}</span>}
      {r.status === "confirmed" && r.application && (
        <span className="text-[13px]">
          <Trans
            k="altro.gitea.confirmedLine"
            slots={{
              name: <span className="font-medium">{r.application.name}</span>,
              code: <span className="font-mono text-[11.5px] text-muted">{r.application.code || r.application.key}</span>,
              service: <span className="font-mono text-[12px]">{r.service_name}</span>,
            }}
          />
        </span>
      )}
      {r.status === "suggested" && r.suggestion && (
        <span className="text-[13px]">
          <Trans
            k="altro.gitea.suggestionLine"
            slots={{
              name: <span className="font-medium">{r.suggestion.application.name}</span>,
              score: <span className="font-mono text-[11.5px] text-muted tabular">{Math.round(r.suggestion.score * 100)}%</span>,
              reason: <span className="text-muted">· {r.suggestion.reason}</span>,
            }}
          />
        </span>
      )}
      {r.last_error && <span className="break-words text-[12px] text-danger">{r.last_error}</span>}
      {r.status !== "confirmed" ? (
        <div className="flex flex-wrap items-end gap-2">
          <label className="flex min-w-[200px] flex-[2_1_220px] flex-col gap-1">
            <span className="label">{t("altro.gitea.application")}</span>
            <Select className="field" value={app} onChange={(e) => setApp(e.target.value)}>
              <option value="">{t("altro.gitea.pick")}</option>
              {apps.map((a) => (
                <option key={a.id} value={a.id}>{a.label}</option>
              ))}
            </Select>
          </label>
          <label className="flex min-w-[140px] flex-[1_1_160px] flex-col gap-1">
            <span className="label">{t("altro.gitea.service")}</span>
            <input className="field font-mono" value={svc} onChange={(e) => setSvc(e.target.value)} />
          </label>
          <button className="btn btn-primary py-1.5" disabled={!app || map.isPending} onClick={() => map.mutate("confirmed")}>{t("altro.gitea.link")}</button>
          {r.status !== "ignored" ? (
            <button className="btn py-1.5" disabled={map.isPending} onClick={() => map.mutate("ignored")}>{t("altro.gitea.ignore")}</button>
          ) : (
            <button className="btn py-1.5" disabled={map.isPending} onClick={() => map.mutate("none")}>{t("altro.gitea.resume")}</button>
          )}
        </div>
      ) : (
        <div>
          <button type="button" className="text-[12px] text-muted underline" disabled={map.isPending} onClick={() => map.mutate("none")}>
            {t("altro.gitea.unlink")}
          </button>
        </div>
      )}
      <MutError errors={[map.error]} />
    </li>
  );
}

// ---------------------------------------------------------------------
// Sincronizzazioni
// ---------------------------------------------------------------------

function SyncTab({ it }: { it: GTIntegration }) {
  const t = useT();
  const qc = useQueryClient();
  const runs = useQuery({ queryKey: ["gt-runs", it.id], queryFn: () => giteaApi.runs(it.id), refetchInterval: it.sync.running ? 2_000 : 10_000 });
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["gt-runs", it.id] });
    qc.invalidateQueries({ queryKey: ["gt-integrations"] });
  };
  const sync = useMutation({ mutationFn: (full: boolean) => giteaApi.sync(it.id, full), onSuccess: () => setTimeout(refresh, 600) });
  return (
    <div className="flex flex-col gap-5">
      <dl className="m-0 max-w-[760px]">
        <KV label={t("altro.gitea.state")}>
          <SyncTag status={it.sync.running ? "running" : it.sync.last_status} />{" "}
          {it.sync.last_finished_at && <span className="text-muted">{t("altro.gitea.lastShort", { when: dateTime(it.sync.last_finished_at) })}</span>}
          {it.sync.last_error && <span className="block break-words text-[12px] text-danger">{it.sync.last_error}</span>}
        </KV>
        <KV label={t("altro.gitea.next")}>{it.active ? (it.sync.next_run_at ? dateTime(it.sync.next_run_at) : t("altro.gitea.withinMinute")) : t("altro.gitea.deactivated")}</KV>
        <KV label={t("altro.gitea.intervalLabel")}>{t("altro.gitea.everyMinutes", { count: it.config.interval_minutes })}</KV>
        <KV label={t("altro.gitea.rateLimits")}>{rateText(t, it.sync.rate_limit)}</KV>
      </dl>
      <div className="flex flex-wrap gap-2">
        <button className="btn btn-primary py-1.5" disabled={sync.isPending || it.sync.running || !it.active} onClick={() => sync.mutate(false)}>
          {t("altro.gitea.syncNow")}
        </button>
        <ConfirmButton label={t("altro.gitea.rereadAll")} confirm={t("altro.gitea.rereadConfirm")} pending={sync.isPending} disabled={it.sync.running || !it.active} onConfirm={() => sync.mutate(true)} />
      </div>
      <MutError errors={[sync.error, runs.error]} />
      <section className="flex flex-col gap-2">
        <SectionTitle aside={<span className="font-mono text-[11.5px] text-muted tabular">{runs.data?.items.length ?? 0}</span>}>{t("altro.gitea.latest")}</SectionTitle>
        <ul className="m-0 flex list-none flex-col p-0">
          {(runs.data?.items ?? []).map((r) => (
            <RunRow key={r.id} r={r} />
          ))}
          {runs.data?.items.length === 0 && <li className="py-3 text-muted">{t("altro.gitea.noRuns")}</li>}
        </ul>
      </section>
    </div>
  );
}

function RunRow({ r }: { r: GTRun }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const s = r.stats;
  const notes = (s.errors?.length ?? 0) + (s.warnings?.length ?? 0);
  return (
    <li className="flex flex-col gap-0.5 border-b border-hair py-2">
      <div className="flex flex-wrap items-baseline gap-x-2">
        <SyncTag status={r.status} />
        <span className="font-medium">{t(TRIGGER[r.trigger])}</span>
        <span className="text-[12px] text-muted">{r.scope}</span>
        <span className="ml-auto font-mono text-[11.5px] text-muted tabular">{dateTime(r.started_at)}</span>
      </div>
      <span className="text-[12px] text-muted tabular">
        {t("altro.gitea.statsLine", { repos: s.repos, releases: s.releases, sboms: s.sboms, reports: s.vuln_reports, checks: s.check_runs, attachments: s.attachments, requests: s.requests, seconds: fmtNumber(s.duration_ms / 1000, { maximumFractionDigits: 1 }) })}
        {s.profile_version ? <> · {t("altro.gitea.profileVersion", { version: s.profile_version })}</> : null}
      </span>
      {r.error && <span className="break-words text-[12px] text-danger">{r.error}</span>}
      {notes > 0 && (
        <button type="button" className="self-start text-[12px] text-muted underline" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
          {open ? t("altro.gitea.hideDetails") : t("altro.gitea.notes", { count: notes })}
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

// ---------------------------------------------------------------------
// Profili di pipeline
// ---------------------------------------------------------------------

function ProfilesTab({ integrations, selected, onSelect }: { integrations: GTIntegration[]; selected?: string; onSelect: (s?: string) => void }) {
  const t = useT();
  const qc = useQueryClient();
  const list = useQuery({ queryKey: ["gh-profiles"], queryFn: githubApi.profiles });
  const presets = useQuery({ queryKey: ["gt-presets"], queryFn: giteaApi.presets });
  const create = useMutation({
    mutationFn: (i: number) => {
      const p = presets.data!.presets[i];
      return githubApi.createProfile({ name: p.name, description: p.description, definition: p.definition });
    },
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ["gh-profiles"] });
      onSelect(r.id);
    },
  });
  const items = list.data?.items ?? [];
  const prof = items.find((p) => p.id === selected);
  return (
    <div className="flex flex-wrap">
      <div className="flex min-w-0 flex-[999_1_420px] flex-col gap-5 px-4 py-5 md:px-7">
        <p className="m-0 max-w-[680px] text-muted">
          {t("altro.gitea.profilesIntro")}
        </p>
        {(presets.data?.presets ?? []).map((p, i) => (
          <section key={p.key} className="flex max-w-[680px] flex-col gap-2 border-l-2 border-accent pl-3">
            <span className="font-medium">{p.name}</span>
            <span className="text-[12.5px] text-muted">{p.description}</span>
            <div>
              <button className="btn btn-primary py-1.5" disabled={create.isPending} onClick={() => create.mutate(i)}>{t("altro.gitea.createFromPreset")}</button>
            </div>
          </section>
        ))}
        <MutError errors={[create.error, list.error, presets.error]} />
        <ul className="m-0 flex list-none flex-col p-0">
          {items.map((p) => (
            <li key={p.id} className={`border-b border-hair py-2.5 ${p.id === selected ? "bg-sel" : ""}`}>
              <button className="flex w-full flex-wrap items-baseline gap-x-2 text-left" aria-current={p.id === selected ? "true" : undefined} onClick={() => onSelect(p.id)}>
                <span className="font-medium hover:underline">{p.name}</span>
                <span className="font-mono text-[11.5px] text-muted">{p.key}</span>
                <Tag tone="fg">v{p.version}</Tag>
                <span className="ml-auto text-[12px] text-muted">{p.used_by.length ? t("altro.gitea.usedBy", { names: p.used_by.join(", ") }) : t("altro.gitea.unused")}</span>
              </button>
            </li>
          ))}
          {list.data && items.length === 0 && <li className="py-8 text-muted">{t("altro.gitea.noProfiles")}</li>}
        </ul>
        <p className="m-0 max-w-[680px] text-[12.5px] text-muted">
          <Trans
            k="altro.gitea.assetsHelp"
            slots={{ /* i18n-ok: nome del file */ star: <span className="font-mono">*</span>, q: <span className="font-mono">?</span>, example: <span className="font-mono">gl-sbom-*.cdx.json</span> }}
          />
        </p>
      </div>
      {prof && (
        <div className="flex min-w-0 flex-[1_1_520px]">
          <SidePanel label={t("altro.gitea.editorLabel")} onClose={() => onSelect(undefined)}>
            <ProfileEditor key={prof.id + prof.version} p={prof} integrations={integrations} />
          </SidePanel>
        </div>
      )}
    </div>
  );
}

function ProfileEditor({ p, integrations }: { p: Profile; integrations: GTIntegration[] }) {
  const t = useT();
  const qc = useQueryClient();
  const [text, setText] = useState(() => JSON.stringify(p.definition, null, 2));
  const [note, setNote] = useState("");
  const [problems, setProblems] = useState<ProfileProblem[] | null>(null);
  const parsed = useMemo(() => {
    try {
      return { ok: true as const, value: JSON.parse(text) as unknown };
    } catch (e) {
      return { ok: false as const, error: (e as Error).message };
    }
  }, [text]);
  const validate = useMutation({ mutationFn: () => githubApi.validate(parsed.ok ? parsed.value : null), onSuccess: (r) => setProblems(r.problems) });
  const save = useMutation({
    mutationFn: () => githubApi.updateProfile(p.id, { name: p.name, description: p.description, definition: parsed.ok ? parsed.value : null, note }),
    onSuccess: () => {
      setNote("");
      setProblems(null);
      qc.invalidateQueries({ queryKey: ["gh-profiles"] });
    },
    onError: (e) => {
      if (e instanceof ApiError && e.problems) setProblems(e.problems.map((x) => ({ path: x.path, message: x.message })));
    },
  });
  const dirty = parsed.ok && JSON.stringify(parsed.value) !== JSON.stringify(p.definition);
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-col gap-1">
        <h2 className="m-0 text-[22px] font-bold">{p.name}</h2>
        <span className="text-[12px] text-muted"><Trans k="altro.gitea.versionLine" params={{ version: p.version }} slots={{ key: <span className="font-mono">{p.key}</span> }} /></span>
      </div>
      <label className="flex flex-col gap-1.5">
        <span className="label">{t("altro.gitea.definition")}</span>
        <textarea
          className="field min-h-[280px] font-mono text-[12px] leading-[1.45]"
          spellCheck={false}
          value={text}
          aria-invalid={!parsed.ok || (problems?.length ?? 0) > 0}
          onChange={(e) => {
            setText(e.target.value);
            setProblems(null);
          }}
        />
      </label>
      {!parsed.ok && <p role="alert" className="m-0 text-danger">{t("altro.gitea.invalidJson", { error: parsed.error })}</p>}
      {problems && problems.length === 0 && <p role="status" className="m-0 text-ok">{t("altro.gitea.valid")}</p>}
      {problems && problems.length > 0 && (
        <ul role="alert" className="m-0 flex list-none flex-col gap-0.5 border-l-2 border-danger pl-3 text-[12.5px]">
          {problems.map((x) => (
            <li key={x.path + x.message}><span className="font-mono">{x.path || t("altro.gitea.profileFallback")}</span>: {x.message}</li>
          ))}
        </ul>
      )}
      <label className="flex flex-col gap-1.5">
        <span className="label">{t("altro.gitea.versionNote")}</span>
        <input className="field" maxLength={500} placeholder={t("altro.gitea.notePh")} value={note} onChange={(e) => setNote(e.target.value)} />
      </label>
      <div className="flex flex-wrap gap-2">
        <button className="btn btn-primary py-1.5" disabled={!parsed.ok || save.isPending} onClick={() => save.mutate()}>{dirty ? t("altro.gitea.saveNew") : t("altro.gitea.save")}</button>
        <button className="btn py-1.5" disabled={!parsed.ok || validate.isPending} onClick={() => validate.mutate()}>{t("altro.gitea.validate")}</button>
        <a className="btn py-1.5 no-underline" href={githubApi.exportURL(p.id)} download>{t("altro.gitea.export")}</a>
      </div>
      <MutError errors={[save.error, validate.error]} />
      <PreviewBox integrations={integrations} definition={parsed.ok ? parsed.value : null} />
    </div>
  );
}

function PreviewBox({ integrations, definition }: { integrations: GTIntegration[]; definition: unknown }) {
  const t = useT();
  const [picked, setInteg] = useState("");
  const integ = picked || integrations[0]?.id || "";
  const repos = useQuery({ queryKey: ["gt-repos", integ], queryFn: () => giteaApi.repos(integ), enabled: !!integ });
  const [proj, setProj] = useState("");
  const [tag, setTag] = useState("");
  const run = useMutation({ mutationFn: () => giteaApi.preview(integ, { repository: proj, definition, tag: tag.trim() || undefined }) });
  const options = (repos.data?.items ?? []).filter((r) => !r.missing && r.status !== "ignored");
  useEffect(() => {
    if (!proj && options.length) setProj((options.find((r) => r.status === "confirmed") ?? options[0]).full_name);
  }, [proj, options]);
  if (!integrations.length) {
    return <p className="m-0 text-[12.5px] text-muted">{t("altro.gitea.needIntegration")}</p>;
  }
  return (
    <section className="flex flex-col gap-3">
      <SectionTitle>{t("altro.gitea.tryTitle")}</SectionTitle>
      <p className="m-0 text-[12.5px] text-muted">{t("altro.gitea.tryHelp")}</p>
      <div className="flex flex-wrap items-end gap-2">
        {integrations.length > 1 && (
          <label className="flex min-w-[160px] flex-1 flex-col gap-1">
            <span className="label">{t("altro.gitea.integration")}</span>
            <Select className="field" value={integ} onChange={(e) => { setInteg(e.target.value); setProj(""); }}>
              {integrations.map((i) => (
                <option key={i.id} value={i.id}>{i.name}</option>
              ))}
            </Select>
          </label>
        )}
        <label className="flex min-w-[200px] flex-[2_1_220px] flex-col gap-1">
          <span className="label">{t("altro.gitea.repo")}</span>
          <Select className="field" value={proj} onChange={(e) => setProj(e.target.value)}>
            {options.map((r) => (
              <option key={r.id} value={r.full_name}>{r.full_name}</option>
            ))}
          </Select>
        </label>
        <label className="flex min-w-[110px] flex-1 flex-col gap-1">
          <span className="label">{t("altro.gitea.tag")}</span>
          <input className="field font-mono" placeholder={t("altro.gitea.tagPh")} value={tag} onChange={(e) => setTag(e.target.value)} />
        </label>
        <button className="btn btn-primary py-1.5" disabled={!proj || !definition || run.isPending} onClick={() => run.mutate()}>{run.isPending ? t("altro.gitea.tryReading") : t("altro.gitea.try")}</button>
      </div>
      <MutError errors={[run.error]} />
      {run.data && <PreviewResult p={run.data} />}
    </section>
  );
}

function PreviewResult({ p }: { p: Preview }) {
  const t = useT();
  const r = p.release;
  const cves = r?.cves_at_release;
  return (
    <div className="flex flex-col gap-3 border-l-2 border-accent pl-3">
      <span className="text-[12px] text-muted">
        <Trans
          k="altro.gitea.headline"
          params={{ repository: p.repository, profile: p.profile, requests: p.requests }}
          slots={{ service: <span className="font-mono">{p.service.name}</span>, app: p.service.application ? t("altro.gitea.appPart", { name: p.service.application }) : "" }}
        />
      </span>
      {r ? (
        <dl className="m-0">
          <KV label="Release">
            <Trans
              k="altro.gitea.releaseLine"
              params={{ environment: r.environment, date: dateTime(r.at) }}
              slots={{ tag: <span className="font-mono">{p.release_tag}</span>, version: <span className="font-mono">{r.version}</span> }}
            />
          </KV>
          <KV label={t("altro.gitea.source")}>
            {p.assets_read.length ? p.assets_read.join(", ") : t("altro.gitea.noFile")}
            {p.assets.length > 0 && <span className="block text-[12px] text-muted">{t("altro.gitea.present", { names: p.assets.join(", ") })}</span>}
          </KV>
          {r.commit && <KV label="Commit"><span className="font-mono text-[12px]">{r.commit.slice(0, 12)}</span></KV>}
          <KV label={t("altro.gitea.who")}>{r.requested_by ? t("altro.gitea.publishedBy", { name: r.requested_by }) : "—"}</KV>
          {cves && (
            <KV label={t("altro.gitea.cves")}>
              <span className="tabular">
                {t("altro.gitea.cveLine", { critical: cves.critical, fixableCritical: cves.fixable_critical, high: cves.high, fixableHigh: cves.fixable_high, medium: cves.medium, low: cves.low })}
              </span>
            </KV>
          )}
          {p.vulnerabilities && p.vulnerabilities.sample.length > 0 && (
            <KV label={t("altro.gitea.topVulns")}>
              <span className="flex flex-col gap-0.5 text-[12.5px]">
                {p.vulnerabilities.sample.slice(0, 5).map((v) => (
                  <span key={v.id + v.package}>
                    <span className="font-mono">{v.id}</span> {v.package} {v.version} · {v.severity}
                    {v.fixed_in && <span className="text-muted"> → {v.fixed_in}</span>}
                  </span>
                ))}
              </span>
            </KV>
          )}
          {p.sbom && <KV label="SBOM">{t("altro.gitea.sbom", { format: p.sbom.format, count: p.sbom.components })}</KV>}
        </dl>
      ) : (
        <span className="text-muted">{t("altro.gitea.noRelease")}</span>
      )}
      {p.check_run && (
        <div className="flex flex-col gap-1 text-[13px]">
          <span>
            <Trans
              k="altro.gitea.pipelineOn"
              slots={{
                ref: <span className="font-mono text-[12px]">{p.check_run.ref}</span>,
                commit: <span className="font-mono text-[12px]">{p.check_run.commit.slice(0, 12)}</span>,
                verdict: <span className={p.check_run.verdict === "passed" ? "font-semibold text-ok" : "font-semibold text-danger"}>{p.check_run.verdict}</span>,
              }}
            />
          </span>
          <span className="flex flex-wrap gap-1">
            {p.check_run.checks.map((c) => (
              <Tag key={c.name} tone={c.outcome === "passed" ? "ok" : c.outcome === "failed" ? "danger" : "muted"} title={c.summary}>{c.name}: {c.outcome}</Tag>
            ))}
          </span>
        </div>
      )}
      {p.warnings.length > 0 && (
        <ul className="m-0 flex list-none flex-col gap-0.5 text-[12px] text-flag">
          {p.warnings.map((w) => (
            <li key={w} className="break-words">{w}</li>
          ))}
        </ul>
      )}
    </div>
  );
}
