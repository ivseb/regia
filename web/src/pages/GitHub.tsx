// Configurazione → Integrazioni → GitHub (DevSecOps, fase 1): integrazioni GitHub e GitHub
// Enterprise Server, prova di connessione, repository con la proposta di associazione alle
// applicazioni, profili di pipeline con l'editor JSON e l'anteprima, stato delle
// sincronizzazioni. Solo classi e variabili del tema.
import { Select } from "../select";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError, api } from "../api";
import {
  githubApi,
  type Integration,
  type IntegrationInput,
  type IntegrationList,
  type Preview,
  type Profile,
  type ProfileProblem,
  type Repository,
  type Run,
  type SyncStatus,
  type TestResult,
} from "../githubApi";
import { PageHeader } from "../shell";
import { ErrorNote, SectionTitle, ago } from "../ui";
import { ConfirmButton, CopyBox, KV, MutError, SidePanel, Tag, dateTime } from "./adminUi";
import { useT } from "../i18n/react";
import { Trans } from "../i18n/Trans";
import type { Key } from "../i18n/core";
import { fmtNumber, fmtTime } from "../i18n/format";

export type GitHubTab = "integrazioni" | "repository" | "profili" | "sincronizzazioni";
export const GITHUB_TABS: GitHubTab[] = ["integrazioni", "repository", "profili", "sincronizzazioni"];
const TAB_LABELS: Record<GitHubTab, Key> = {
  integrazioni: "altro.github.tab.integrazioni",
  repository: "altro.github.tab.repository",
  profili: "altro.github.tab.profili",
  sincronizzazioni: "altro.github.tab.sincronizzazioni",
};
const NEW = "nuovo";
const PATH = "/configurazione/integrazioni/github";

const SYNC: Record<SyncStatus, { label: Key; tone: "ok" | "flag" | "danger" | "muted" }> = {
  running: { label: "altro.github.sync.running", tone: "muted" },
  ok: { label: "altro.github.sync.ok", tone: "ok" },
  partial: { label: "altro.github.sync.partial", tone: "flag" },
  error: { label: "altro.github.sync.error", tone: "danger" },
  rate_limited: { label: "altro.github.sync.rate_limited", tone: "flag" },
};
const TRIGGER: Record<Run["trigger"], Key> = { schedule: "altro.github.trigger.schedule", manual: "altro.github.trigger.manual", webhook: "altro.github.trigger.webhook" };

type Search = { tab?: GitHubTab; sel?: string; integrazione?: string };

export function GitHubPage({ tab, sel, integration }: { tab: GitHubTab; sel?: string; integration?: string }) {
  const t = useT();
  const navigate = useNavigate();
  const go = (s: Search) => navigate({ to: PATH, search: { tab, ...s } });
  const list = useQuery({ queryKey: ["gh-integrations"], queryFn: githubApi.list, refetchInterval: 10_000 });
  if (list.error instanceof ApiError && list.error.status === 403) {
    return (
      <>
        <PageHeader path={t("altro.github.path")} title="GitHub" /* i18n-ok: nome del prodotto */ />
        <p className="px-4 text-muted md:px-7">{t("altro.github.needAdmin")}</p>
      </>
    );
  }
  const items = list.data?.items ?? [];
  const current = items.find((i) => i.id === integration) ?? items[0];
  return (
    <>
      <PageHeader
        path={t("altro.github.path")}
        title="GitHub" /* i18n-ok: nome del prodotto */
        actions={
          tab === "integrazioni" ? (
            <button className="btn btn-primary" onClick={() => go({ sel: NEW })}>{t("altro.github.newIntegration")}</button>
          ) : undefined
        }
      />
      <div role="tablist" aria-label={t("altro.github.sections")} className="flex gap-6 overflow-x-auto border-b border-hair px-4 md:px-7">
        {GITHUB_TABS.map((k) => (
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
          {t("altro.github.notStoring")}
        </p>
      )}
      <ErrorNote error={list.error} />
      {tab === "integrazioni" && list.data && <IntegrationsTab data={list.data} selected={sel} onSelect={(s) => go({ sel: s })} />}
      {tab !== "integrazioni" && tab !== "profili" && list.data && items.length === 0 && (
        <p className="px-4 py-6 text-muted md:px-7">
          <Trans k="altro.github.noIntegrationYet" slots={{ link: <Link to={PATH} search={{ tab: "integrazioni", sel: NEW }}>{t("altro.github.tab.integrazioni")}</Link> }} />
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

function IntegrationPicker({ items, current, onPick, children }: { items: Integration[]; current: Integration; onPick: (id: string) => void; children: ReactNode }) {
  const t = useT();
  return (
    <div className="flex flex-col gap-4 px-4 py-5 md:px-7">
      {items.length > 1 && (
        <label className="flex max-w-[420px] flex-col gap-1.5">
          <span className="label">{t("altro.github.integration")}</span>
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
  if (!status) return <Tag>{t("altro.github.neverSynced")}</Tag>;
  const s = SYNC[status];
  return <Tag tone={s.tone}>{t(s.label)}</Tag>;
}

function rateText(t: ReturnType<typeof useT>, r: { remaining: number; limit: number; reset: string } | null | undefined) {
  if (!r) return "—";
  return t("altro.github.rate", { remaining: fmtNumber(r.remaining), limit: fmtNumber(r.limit), time: fmtTime(r.reset, { hour: "2-digit", minute: "2-digit" }) });
}

// ---------------------------------------------------------------------
// Integrazioni
// ---------------------------------------------------------------------

function IntegrationsTab({ data, selected, onSelect }: { data: IntegrationList; selected?: string; onSelect: (s?: string) => void }) {
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
          {t("altro.github.intro")}
        </p>
        {secret && (
          <CopyBox
            title={t("altro.github.secretTitle")}
            value={secret}
            note={t("altro.github.secretNote")}
            onDone={() => setSecret(null)}
          />
        )}
        <ul className="m-0 flex list-none flex-col p-0">
          {data.items.map((i) => (
            <li key={i.id} className={`border-b border-hair py-2.5 ${i.id === selected ? "bg-sel" : ""}`}>
              <button className="flex w-full flex-wrap items-baseline gap-x-2 text-left" aria-current={i.id === selected ? "true" : undefined} onClick={() => onSelect(i.id)}>
                <span className="font-medium hover:underline">{i.name}</span>
                {!i.active && <Tag>{t("altro.github.inactive")}</Tag>}
                <SyncTag status={i.sync.last_status} />
                {i.repositories.suggested > 0 && <Tag tone="accent">{t("altro.github.toLink", { count: i.repositories.suggested })}</Tag>}
                <span className="ml-auto text-[12px] text-muted">
                  {i.sync.last_finished_at ? t("altro.github.lastRead", { when: ago(i.sync.last_finished_at) }) : t("altro.github.neverRead")}
                </span>
              </button>
              <span className="block truncate font-mono text-[11.5px] text-muted" title={i.config.base_url}>
                {i.config.base_url} · {i.config.owners.join(", ")}
              </span>
              <span className="block text-[12px] text-muted tabular">
                {t("altro.github.summary", { auth: i.config.auth_mode === "app" ? t("altro.github.authApp") : t("altro.github.authToken"), confirmed: i.repositories.confirmed, total: i.repositories.total })}
                {i.profile_name && <> · {t("altro.github.profileOf", { name: i.profile_name, version: i.profile_version ?? "" })}</>}
              </span>
            </li>
          ))}
          {data.items.length === 0 && <li className="py-8 text-muted">{t("altro.github.noIntegrations")}</li>}
        </ul>
        <AppHelp />
      </div>
      {selected && (
        <div ref={panel} className="flex min-w-0 scroll-mt-14 flex-[1_1_460px]">
          <SidePanel label={t("altro.github.panelLabel")} onClose={() => onSelect(undefined)}>
            {selected === NEW ? (
              <IntegrationForm data={data} onSaved={(id) => onSelect(id)} onCancel={() => onSelect(undefined)} />
            ) : it ? (
              <IntegrationPanel key={it.id} it={it} data={data} onSecret={setSecret} onDeleted={() => onSelect(undefined)} />
            ) : (
              <p className="text-muted">{t("altro.github.notFound")}</p>
            )}
          </SidePanel>
        </div>
      )}
    </div>
  );
}

function AppHelp() {
  const t = useT();
  return (
    <section className="flex max-w-[680px] flex-col gap-2">
      <SectionTitle>{t("altro.github.appHelpTitle")}</SectionTitle>
      <p className="m-0">
        <Trans
          k="altro.github.appHelp"
          slots={{
            contents: <span className="font-mono text-[12px]">Contents</span>, // i18n-ok: nome tecnico
            metadata: <span className="font-mono text-[12px]">Metadata</span>, // i18n-ok: nome tecnico
            deployments: <span className="font-mono text-[12px]">Deployments</span>, // i18n-ok: nome tecnico
            checks: <span className="font-mono text-[12px]">Checks</span>, // i18n-ok: nome tecnico
          }}
        />
      </p>
      <p className="m-0 text-muted">{t("altro.github.appHelpGuide")}</p>
    </section>
  );
}

function lines(s: string): string[] {
  return s
    .split(/[\n,]/)
    .map((x) => x.trim())
    .filter(Boolean);
}

function IntegrationForm({ it, data, onSaved, onCancel }: { it?: Integration; data: IntegrationList; onSaved: (id: string) => void; onCancel?: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const profiles = useQuery({ queryKey: ["gh-profiles"], queryFn: githubApi.profiles });
  const c = it?.config;
  const [name, setName] = useState(it?.name ?? "GitHub");
  const [baseURL, setBaseURL] = useState(c?.base_url ?? "https://api.github.com");
  const [owners, setOwners] = useState((c?.owners ?? []).join(", "));
  const [mode, setMode] = useState<"app" | "token">(c?.auth_mode ?? "app");
  const [appID, setAppID] = useState(c?.app_id ?? "");
  const [instID, setInstID] = useState(c?.installation_id ?? "");
  const [key, setKey] = useState("");
  const [token, setToken] = useState("");
  const [hook, setHook] = useState("");
  const [interval, setInterval] = useState(c?.interval_minutes ?? 30);
  const [profileID, setProfileID] = useState(c?.profile_id ?? "");
  const [verdict, setVerdict] = useState(c?.verdict_check ?? "");
  const [env, setEnv] = useState(c?.release_environment ?? "");
  const [gitops, setGitops] = useState((c?.gitops_repositories ?? []).join("\n"));
  const [priv, setPriv] = useState(c?.private_network ?? false);
  const [ca, setCA] = useState(c?.ca_certificate ?? "");
  const [archived, setArchived] = useState(c?.include_archived ?? false);
  const [active, setActive] = useState(it?.active ?? true);
  const save = useMutation({
    mutationFn: async () => {
      const body: IntegrationInput = {
        name, active, base_url: baseURL, owners: lines(owners), auth_mode: mode, app_id: appID, installation_id: instID,
        token: token || undefined, private_key: key || undefined, webhook_secret: hook || undefined,
        interval_minutes: Number(interval) || 30, profile_id: profileID, verdict_check: verdict, release_environment: env,
        gitops_repositories: lines(gitops), private_network: priv, ca_certificate: ca, include_archived: archived,
      };
      if (it) {
        await githubApi.update(it.id, body);
        return it.id;
      }
      return (await githubApi.create(body)).id;
    },
    onSuccess: (id) => {
      qc.invalidateQueries({ queryKey: ["gh-integrations"] });
      setKey("");
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
      {!it && <SectionTitle>{t("altro.github.newIntegration")}</SectionTitle>}
      <label className="flex flex-col gap-1.5">
        <span className="label">{t("altro.github.name")}</span>
        <input className="field" required maxLength={80} value={name} aria-invalid={bad("name")} onChange={(e) => setName(e.target.value)} />
      </label>
      <label className="flex flex-col gap-1.5">
        <span className="label">{t("altro.github.baseUrl")}</span>
        <input className="field font-mono" required value={baseURL} aria-invalid={bad("base_url")} onChange={(e) => setBaseURL(e.target.value)} />
        <span className="text-[12px] text-muted">{t("altro.github.baseUrlHelp")}</span>
      </label>
      <label className="flex flex-col gap-1.5">
        <span className="label">{t("altro.github.owners")}</span>
        <input className="field font-mono" required placeholder={t("altro.github.ownersPh")} value={owners} aria-invalid={bad("owners")} onChange={(e) => setOwners(e.target.value)} />
      </label>
      <fieldset className="m-0 flex flex-col gap-2 border-0 p-0">
        <legend className="label mb-1.5 p-0">{t("altro.github.authLabel")}</legend>
        <label className="flex items-center gap-2">
          <input type="radio" name="mode" checked={mode === "app"} onChange={() => setMode("app")} /> {t("altro.github.authAppRec")}
        </label>
        <label className="flex items-center gap-2">
          <input type="radio" name="mode" checked={mode === "token"} onChange={() => setMode("token")} /> {t("altro.github.authTokenTest")}
        </label>
      </fieldset>
      {mode === "app" ? (
        <>
          <div className="flex flex-wrap gap-3">
            <label className="flex min-w-[140px] flex-1 flex-col gap-1.5">
              <span className="label">{/* i18n-ok: nome tecnico */}App ID</span>
              <input className="field font-mono" inputMode="numeric" value={appID} aria-invalid={bad("app_id")} onChange={(e) => setAppID(e.target.value)} />
            </label>
            <label className="flex min-w-[140px] flex-1 flex-col gap-1.5">
              <span className="label">{/* i18n-ok: nome tecnico */}Installation ID</span>
              <input className="field font-mono" inputMode="numeric" value={instID} aria-invalid={bad("installation_id")} onChange={(e) => setInstID(e.target.value)} />
            </label>
          </div>
          <label className="flex flex-col gap-1.5">
            <span className="label">{t("altro.github.privateKey")}</span>
            <textarea
              className="field min-h-20 font-mono text-[12px]"
              placeholder={it?.has_private_key ? t("altro.github.keySaved") : "-----BEGIN RSA PRIVATE KEY-----"}
              value={key}
              aria-invalid={bad("private_key")}
              onChange={(e) => setKey(e.target.value)}
            />
          </label>
        </>
      ) : (
        <label className="flex flex-col gap-1.5">
          <span className="label">{t("altro.github.token")}</span>
          <input
            className="field font-mono"
            type="password"
            autoComplete="off"
            placeholder={it?.has_token ? t("altro.github.savedKeep") : t("altro.github.tokenPh")}
            value={token}
            aria-invalid={bad("token")}
            onChange={(e) => setToken(e.target.value)}
          />
        </label>
      )}
      <label className="flex flex-col gap-1.5">
        <span className="label">{t("altro.github.profile")}</span>
        <Select className="field" value={profileID} aria-invalid={bad("profile_id")} onChange={(e) => setProfileID(e.target.value)}>
          <option value="">{t("altro.github.noProfile")}</option>
          {(profiles.data?.items ?? []).map((p) => (
            <option key={p.id} value={p.id}>{p.name} (v{p.version})</option>
          ))}
        </Select>
      </label>
      <label className="flex flex-col gap-1.5">
        <span className="label">{t("altro.github.gitops")}</span>
        <textarea className="field min-h-12 font-mono text-[12px]" placeholder={t("altro.github.gitopsPh")} value={gitops} aria-invalid={bad("gitops_repositories")} onChange={(e) => setGitops(e.target.value)} />
        <span className="text-[12px] text-muted">{t("altro.github.gitopsHelp")}</span>
      </label>
      <div className="flex flex-wrap gap-3">
        <label className="flex min-w-[140px] flex-1 flex-col gap-1.5">
          <span className="label">{t("altro.github.interval")}</span>
          <input className="field tabular" type="number" min={5} max={1440} value={interval} aria-invalid={bad("interval_minutes")} onChange={(e) => setInterval(Number(e.target.value))} />
        </label>
        <label className="flex min-w-[140px] flex-1 flex-col gap-1.5">
          <span className="label">{t("altro.github.verdict")}</span>
          <input className="field font-mono" placeholder={t("altro.github.verdictPh")} value={verdict} onChange={(e) => setVerdict(e.target.value)} />
        </label>
        <label className="flex min-w-[140px] flex-1 flex-col gap-1.5">
          <span className="label">{t("altro.github.env")}</span>
          <input className="field font-mono" placeholder="prod" /* i18n-ok: valore d'esempio */ value={env} aria-invalid={bad("release_environment")} onChange={(e) => setEnv(e.target.value)} />
        </label>
      </div>
      <details className="flex flex-col gap-3">
        <summary className="cursor-pointer text-muted">{t("altro.github.advanced")}</summary>
        <div className="mt-3 flex flex-col gap-3">
          <label className="flex items-start gap-2">
            <input type="checkbox" className="mt-0.5" checked={priv} disabled={!data.allow_private && !priv} onChange={(e) => setPriv(e.target.checked)} />
            <span>
              {t("altro.github.internalNet")}
              <span className="block text-[12px] text-muted">
                {data.dev
                  ? t("altro.github.devMode")
                  : data.allow_private
                    ? t("altro.github.allowPriv")
                    : t("altro.github.noPriv")}
              </span>
            </span>
          </label>
          <label className="flex flex-col gap-1.5">
            <span className="label">{t("altro.github.ca")}</span>
            <textarea className="field min-h-12 font-mono text-[12px]" placeholder="-----BEGIN CERTIFICATE-----" value={ca} aria-invalid={bad("ca_certificate")} onChange={(e) => setCA(e.target.value)} />
          </label>
          <label className="flex flex-col gap-1.5">
            <span className="label">{t("altro.github.hook")}</span>
            <input
              className="field font-mono"
              type="password"
              autoComplete="off"
              placeholder={it?.has_webhook_secret ? t("altro.github.savedKeep") : t("altro.github.hookPh")}
              value={hook}
              aria-invalid={bad("webhook_secret")}
              onChange={(e) => setHook(e.target.value)}
            />
          </label>
          <label className="flex items-center gap-2">
            <input type="checkbox" checked={archived} onChange={(e) => setArchived(e.target.checked)} /> {t("altro.github.archived")}
          </label>
        </div>
      </details>
      <label className="flex items-center gap-2">
        <input type="checkbox" checked={active} onChange={(e) => setActive(e.target.checked)} /> {t("altro.github.auth.active")}
      </label>
      <ErrorNote error={save.error} />
      <div className="flex flex-wrap gap-2">
        <button className="btn btn-primary" disabled={save.isPending}>{it ? t("altro.github.save") : t("altro.github.create")}</button>
        {onCancel && <button type="button" className="btn" onClick={onCancel}>{t("altro.github.cancel")}</button>}
      </div>
    </form>
  );
}

function IntegrationPanel({ it, data, onSecret, onDeleted }: { it: Integration; data: IntegrationList; onSecret: (s: string) => void; onDeleted: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const [result, setResult] = useState<TestResult | null>(null);
  const test = useMutation({ mutationFn: () => githubApi.test(it.id), onSuccess: setResult });
  const secret = useMutation({
    mutationFn: () => githubApi.webhookSecret(it.id),
    onSuccess: (r) => {
      onSecret(r.secret);
      qc.invalidateQueries({ queryKey: ["gh-integrations"] });
    },
  });
  const del = useMutation({
    mutationFn: () => githubApi.remove(it.id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["gh-integrations"] });
      onDeleted();
    },
  });
  const webhookURL = (data.base_url || window.location.origin) + it.webhook_path;
  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-col gap-1">
        <h2 className="m-0 text-[22px] font-bold">{it.name}</h2>
        <span className="text-[12px] text-muted">{t("altro.github.created", { date: dateTime(it.created_at), when: ago(it.updated_at) })}</span>
      </div>
      <div className="flex flex-wrap gap-2">
        <button className="btn btn-primary py-1.5" disabled={test.isPending} onClick={() => test.mutate()}>
          {test.isPending ? t("altro.github.testing") : t("altro.github.test")}
        </button>
        <Link className="btn py-1.5 no-underline" to={PATH} search={{ tab: "repository", integrazione: it.id }}>{t("altro.github.tab.repository")}</Link>
        <Link className="btn py-1.5 no-underline" to={PATH} search={{ tab: "sincronizzazioni", integrazione: it.id }}>{t("altro.github.tab.sincronizzazioni")}</Link>
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
        <KV label={t("altro.github.secrets")}>
          {it.config.auth_mode === "app" ? (it.has_private_key ? t("altro.github.keyOk") : t("altro.github.keyMissing")) : it.has_token ? t("altro.github.tokOk") : t("altro.github.tokMissing")}
        </KV>
        <KV label="Webhook">
          <span className="block break-all font-mono text-[12px]">{webhookURL}</span>
          <span className="text-[12px] text-muted">
            {it.has_webhook_secret ? t("altro.github.secretSet") : t("altro.github.noSecret")}
          </span>
        </KV>
        <KV label={t("altro.github.lastReadLabel")}>
          <SyncTag status={it.sync.last_status} /> {it.sync.last_finished_at && dateTime(it.sync.last_finished_at)}
          {it.sync.last_error && <span className="block text-[12px] text-danger">{it.sync.last_error}</span>}
        </KV>
        <KV label={t("altro.github.rateLimits")}>{rateText(t, it.sync.rate_limit)}</KV>
      </dl>
      <div className="flex flex-wrap gap-2">
        <ConfirmButton
          label={it.has_webhook_secret ? t("altro.github.newSecret") : t("altro.github.genSecret")}
          confirm={it.has_webhook_secret ? t("altro.github.replaceSecret") : t("altro.github.generate")}
          pending={secret.isPending}
          onConfirm={() => secret.mutate()}
        />
        <ConfirmButton label={t("altro.github.delete")} confirm={t("altro.github.deleteIntegration")} danger pending={del.isPending} onConfirm={() => del.mutate()} />
      </div>
      <IntegrationForm key={it.updated_at} it={it} data={data} onSaved={() => undefined} />
    </div>
  );
}

// ---------------------------------------------------------------------
// Repository
// ---------------------------------------------------------------------

const REPO_STATUS: Record<Repository["status"], { label: Key; tone: "ok" | "accent" | "muted" | "fg" }> = {
  confirmed: { label: "altro.github.status.confirmed", tone: "ok" },
  suggested: { label: "altro.github.status.suggested", tone: "accent" },
  none: { label: "altro.github.status.none", tone: "fg" },
  ignored: { label: "altro.github.status.ignored", tone: "muted" },
};

function ReposTab({ it }: { it: Integration }) {
  const t = useT();
  const qc = useQueryClient();
  const repos = useQuery({ queryKey: ["gh-repos", it.id], queryFn: () => githubApi.repos(it.id) });
  const apps = useQuery({ queryKey: ["applications", ""], queryFn: () => api.applications("") });
  const [filter, setFilter] = useState<"tutti" | Repository["status"]>("tutti");
  const [chosen, setChosen] = useState<string[]>([]);
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["gh-repos", it.id] });
    qc.invalidateQueries({ queryKey: ["gh-integrations"] });
  };
  const discover = useMutation({ mutationFn: () => githubApi.discover(it.id), onSuccess: refresh });
  const accept = useMutation({ mutationFn: () => githubApi.accept(it.id, chosen), onSuccess: () => { setChosen([]); refresh(); } });
  const items = (repos.data?.items ?? []).filter((r) => filter === "tutti" || r.status === filter);
  const suggested = (repos.data?.items ?? []).filter((r) => r.status === "suggested");
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2">
        <button className="btn btn-primary py-1.5" disabled={discover.isPending} onClick={() => discover.mutate()}>
          {discover.isPending ? t("altro.github.reading") : t("altro.github.discover")}
        </button>
        <button className="btn py-1.5" disabled={accept.isPending || chosen.length === 0} onClick={() => accept.mutate()}>
          {t("altro.github.confirmChosen", { count: chosen.length })}
        </button>
        {suggested.length > 0 && (
          <button type="button" className="text-[12px] text-accent underline" onClick={() => setChosen(suggested.map((r) => r.id))}>
            {t("altro.github.chooseAll")}
          </button>
        )}
        <Select className="field ml-auto w-auto" aria-label={t("altro.github.filter")} value={filter} onChange={(e) => setFilter(e.target.value as typeof filter)}>
          <option value="tutti">{t("altro.github.f.all")}</option>
          <option value="suggested">{t("altro.github.f.suggested")}</option>
          <option value="none">{t("altro.github.f.none")}</option>
          <option value="confirmed">{t("altro.github.f.confirmed")}</option>
          <option value="ignored">{t("altro.github.f.ignored")}</option>
        </Select>
      </div>
      {discover.data && (
        <p role="status" className="m-0 text-[13px] text-muted">
          {t("altro.github.discovered", { found: discover.data.found, new: discover.data.new, suggested: discover.data.suggested })}
          {discover.data.missing > 0 && t("altro.github.discoveredMissing", { missing: discover.data.missing })}.
        </p>
      )}
      <MutError errors={[discover.error, accept.error, repos.error]} />
      <p className="m-0 max-w-[720px] text-[13px] text-muted">
        {t("altro.github.reposNote")}
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
        {repos.data && items.length === 0 && <li className="py-6 text-muted">{t("altro.github.noRepos")}</li>}
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
  it: Integration;
  r: Repository;
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
      githubApi.mapRepo(it.id, r.id, status === "confirmed" ? { status, application_id: app, service_name: svc } : { status }),
    onSuccess: onChanged,
  });
  const st = REPO_STATUS[r.status];
  return (
    <li className={`flex flex-col gap-2 border-b border-hair py-3 ${r.missing ? "opacity-60" : ""}`}>
      <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
        {r.status === "suggested" && (
          <input type="checkbox" aria-label={t("altro.github.choose", { name: r.full_name })} checked={checked} onChange={(e) => onCheck(e.target.checked)} />
        )}
        <a className="font-mono text-[13px] font-medium" href={r.html_url} target="_blank" rel="noreferrer">{r.full_name}</a>
        <Tag tone={st.tone}>{t(st.label)}</Tag>
        {r.archived && <Tag>{t("altro.github.archivedTag")}</Tag>}
        {r.missing && <Tag tone="flag">{t("altro.github.missingTag")}</Tag>}
        {r.last_error && <Tag tone="danger" title={r.last_error}>{t("altro.github.errorTag")}</Tag>}
        <span className="ml-auto text-[12px] text-muted">{r.last_synced_at ? t("altro.github.readAgo", { when: ago(r.last_synced_at) }) : ""}</span>
      </div>
      {r.description && <span className="text-[12.5px] text-muted">{r.description}</span>}
      {r.status === "confirmed" && r.application && (
        <span className="text-[13px]">
          <Trans
            k="altro.github.confirmedLine"
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
            k="altro.github.suggestionLine"
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
            <span className="label">{t("altro.github.application")}</span>
            <Select className="field" value={app} onChange={(e) => setApp(e.target.value)}>
              <option value="">{t("altro.github.pick")}</option>
              {apps.map((a) => (
                <option key={a.id} value={a.id}>{a.label}</option>
              ))}
            </Select>
          </label>
          <label className="flex min-w-[140px] flex-[1_1_160px] flex-col gap-1">
            <span className="label">{t("altro.github.service")}</span>
            <input className="field font-mono" value={svc} onChange={(e) => setSvc(e.target.value)} />
          </label>
          <button className="btn btn-primary py-1.5" disabled={!app || map.isPending} onClick={() => map.mutate("confirmed")}>{t("altro.github.link")}</button>
          {r.status !== "ignored" ? (
            <button className="btn py-1.5" disabled={map.isPending} onClick={() => map.mutate("ignored")}>{t("altro.github.ignore")}</button>
          ) : (
            <button className="btn py-1.5" disabled={map.isPending} onClick={() => map.mutate("none")}>{t("altro.github.resume")}</button>
          )}
        </div>
      ) : (
        <div>
          <button type="button" className="text-[12px] text-muted underline" disabled={map.isPending} onClick={() => map.mutate("none")}>
            {t("altro.github.unlink")}
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

function SyncTab({ it }: { it: Integration }) {
  const t = useT();
  const qc = useQueryClient();
  const runs = useQuery({ queryKey: ["gh-runs", it.id], queryFn: () => githubApi.runs(it.id), refetchInterval: it.sync.running ? 2_000 : 10_000 });
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["gh-runs", it.id] });
    qc.invalidateQueries({ queryKey: ["gh-integrations"] });
  };
  const sync = useMutation({ mutationFn: (full: boolean) => githubApi.sync(it.id, full), onSuccess: () => setTimeout(refresh, 600) });
  return (
    <div className="flex flex-col gap-5">
      <dl className="m-0 max-w-[760px]">
        <KV label={t("altro.github.state")}>
          <SyncTag status={it.sync.running ? "running" : it.sync.last_status} />{" "}
          {it.sync.last_finished_at && <span className="text-muted">{t("altro.github.lastShort", { when: dateTime(it.sync.last_finished_at) })}</span>}
          {it.sync.last_error && <span className="block break-words text-[12px] text-danger">{it.sync.last_error}</span>}
        </KV>
        <KV label={t("altro.github.next")}>{it.active ? (it.sync.next_run_at ? dateTime(it.sync.next_run_at) : t("altro.github.withinMinute")) : t("altro.github.deactivated")}</KV>
        <KV label={t("altro.github.intervalLabel")}>{t("altro.github.everyMinutes", { count: it.config.interval_minutes })}</KV>
        <KV label={t("altro.github.rateLimits")}>{rateText(t, it.sync.rate_limit)}</KV>
      </dl>
      <div className="flex flex-wrap gap-2">
        <button className="btn btn-primary py-1.5" disabled={sync.isPending || it.sync.running || !it.active} onClick={() => sync.mutate(false)}>
          {t("altro.github.syncNow")}
        </button>
        <ConfirmButton
          label={t("altro.github.rereadAll")}
          confirm={t("altro.github.rereadConfirm")}
          pending={sync.isPending}
          disabled={it.sync.running || !it.active}
          onConfirm={() => sync.mutate(true)}
        />
      </div>
      <MutError errors={[sync.error, runs.error]} />
      <section className="flex flex-col gap-2">
        <SectionTitle aside={<span className="font-mono text-[11.5px] text-muted tabular">{runs.data?.items.length ?? 0}</span>}>{t("altro.github.latest")}</SectionTitle>
        <ul className="m-0 flex list-none flex-col p-0">
          {(runs.data?.items ?? []).map((r) => (
            <RunRow key={r.id} r={r} />
          ))}
          {runs.data?.items.length === 0 && <li className="py-3 text-muted">{t("altro.github.noRuns")}</li>}
        </ul>
      </section>
    </div>
  );
}

function RunRow({ r }: { r: Run }) {
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
        {t("altro.github.statsLine", { repositories: s.repositories, releases: s.releases, sboms: s.sboms, reports: s.vuln_reports, deploys: s.deployments + s.gitops_deployments, checks: s.check_runs, requests: s.requests, seconds: fmtNumber(s.duration_ms / 1000, { maximumFractionDigits: 1 }) })}
        {s.profile_version ? <> · {t("altro.github.profileVersion", { version: s.profile_version })}</> : null}
      </span>
      {r.error && <span className="break-words text-[12px] text-danger">{r.error}</span>}
      {notes > 0 && (
        <button type="button" className="self-start text-[12px] text-muted underline" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
          {open ? t("altro.github.hideDetails") : t("altro.github.notes", { count: notes })}
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

function ProfilesTab({ integrations, selected, onSelect }: { integrations: Integration[]; selected?: string; onSelect: (s?: string) => void }) {
  const t = useT();
  const qc = useQueryClient();
  const list = useQuery({ queryKey: ["gh-profiles"], queryFn: githubApi.profiles });
  const [preset, setPreset] = useState("");
  const [importErr, setImportErr] = useState<unknown>(null);
  const [imported, setImported] = useState<string | null>(null);
  const create = useMutation({
    mutationFn: () => githubApi.createProfile({ preset }),
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ["gh-profiles"] });
      onSelect(r.id);
    },
  });
  const presets = list.data?.presets ?? [];
  useEffect(() => {
    if (!preset && presets.length) setPreset(presets[0].key);
  }, [preset, presets]);
  async function importFile(f: File) {
    setImportErr(null);
    setImported(null);
    try {
      const doc = JSON.parse(await f.text());
      const r = await githubApi.importProfile(doc);
      qc.invalidateQueries({ queryKey: ["gh-profiles"] });
      setImported(r.created ? t("altro.github.imported", { key: r.key }) : t("altro.github.updated", { key: r.key, version: r.version }));
      onSelect(r.id);
    } catch (e) {
      setImportErr(e instanceof SyntaxError ? new ApiError(400, "json", t("altro.github.badFile")) : e);
    }
  }
  const items = list.data?.items ?? [];
  const prof = items.find((p) => p.id === selected);
  return (
    <div className="flex flex-wrap">
      <div className="flex min-w-0 flex-[999_1_420px] flex-col gap-5 px-4 py-5 md:px-7">
        <p className="m-0 max-w-[680px] text-muted">
          {t("altro.github.profilesIntro")}
        </p>
        <div className="flex flex-wrap items-end gap-2">
          <label className="flex min-w-[220px] flex-1 flex-col gap-1.5">
            <span className="label">{t("altro.github.newFromPreset")}</span>
            <Select className="field" value={preset} onChange={(e) => setPreset(e.target.value)}>
              {presets.map((p) => (
                <option key={p.key} value={p.key}>{p.name}</option>
              ))}
            </Select>
          </label>
          <button className="btn btn-primary py-1.5" disabled={!preset || create.isPending} onClick={() => create.mutate()}>{t("altro.github.createProfile")}</button>
          <label className="btn py-1.5">
            {t("altro.github.importJson")}
            <input
              type="file"
              accept="application/json,.json"
              className="sr-only"
              onChange={(e) => {
                const f = e.target.files?.[0];
                if (f) void importFile(f);
                e.target.value = "";
              }}
            />
          </label>
        </div>
        {presets.find((p) => p.key === preset) && <p className="m-0 max-w-[680px] text-[12.5px] text-muted">{presets.find((p) => p.key === preset)?.description}</p>}
        {imported && <p role="status" className="m-0 text-ok">{imported}</p>}
        <MutError errors={[create.error, importErr, list.error]} />
        <ul className="m-0 flex list-none flex-col p-0">
          {items.map((p) => (
            <li key={p.id} className={`border-b border-hair py-2.5 ${p.id === selected ? "bg-sel" : ""}`}>
              <button className="flex w-full flex-wrap items-baseline gap-x-2 text-left" aria-current={p.id === selected ? "true" : undefined} onClick={() => onSelect(p.id)}>
                <span className="font-medium hover:underline">{p.name}</span>
                <span className="font-mono text-[11.5px] text-muted">{p.key}</span>
                <Tag tone="fg">v{p.version}</Tag>
                <span className="ml-auto text-[12px] text-muted">{p.used_by.length ? t("altro.github.usedBy", { names: p.used_by.join(", ") }) : t("altro.github.unused")}</span>
              </button>
            </li>
          ))}
          {list.data && items.length === 0 && <li className="py-8 text-muted">{t("altro.github.noProfiles")}</li>}
        </ul>
      </div>
      {prof && (
        <div className="flex min-w-0 flex-[1_1_520px]">
          <SidePanel label={t("altro.github.editorLabel")} onClose={() => onSelect(undefined)}>
            <ProfileEditor key={prof.id + prof.version} p={prof} integrations={integrations} onDeleted={() => onSelect(undefined)} />
          </SidePanel>
        </div>
      )}
    </div>
  );
}

function ProfileEditor({ p, integrations, onDeleted }: { p: Profile; integrations: Integration[]; onDeleted: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const detail = useQuery({ queryKey: ["gh-profile", p.id, p.version], queryFn: () => githubApi.profile(p.id) });
  const [name, setName] = useState(p.name);
  const [description, setDescription] = useState(p.description);
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
  const validate = useMutation({
    mutationFn: () => githubApi.validate(parsed.ok ? parsed.value : null),
    onSuccess: (r) => setProblems(r.problems),
  });
  const save = useMutation({
    mutationFn: () => githubApi.updateProfile(p.id, { name, description, definition: parsed.ok ? parsed.value : null, note }),
    onSuccess: () => {
      setNote("");
      setProblems(null);
      qc.invalidateQueries({ queryKey: ["gh-profiles"] });
    },
    onError: (e) => {
      if (e instanceof ApiError && e.problems) setProblems(e.problems.map((x) => ({ path: x.path, message: x.message })));
    },
  });
  const del = useMutation({
    mutationFn: () => githubApi.deleteProfile(p.id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["gh-profiles"] });
      onDeleted();
    },
  });
  const dirty = parsed.ok && JSON.stringify(parsed.value) !== JSON.stringify(p.definition);
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-col gap-1">
        <h2 className="m-0 text-[22px] font-bold">{p.name}</h2>
        <span className="text-[12px] text-muted">
          <Trans k="altro.github.versionLine" params={{ version: p.version, origin: p.preset ? t("altro.github.fromPreset") : t("altro.github.custom") }} slots={{ key: <span className="font-mono">{p.key}</span> }} />
        </span>
      </div>
      <label className="flex flex-col gap-1.5">
        <span className="label">{t("altro.github.name")}</span>
        <input className="field" maxLength={120} value={name} onChange={(e) => setName(e.target.value)} />
      </label>
      <label className="flex flex-col gap-1.5">
        <span className="label">{t("altro.github.description")}</span>
        <textarea className="field min-h-12" maxLength={2000} value={description} onChange={(e) => setDescription(e.target.value)} />
      </label>
      <label className="flex flex-col gap-1.5">
        <span className="label">{t("altro.github.definition")}</span>
        <textarea
          className="field min-h-[360px] font-mono text-[12px] leading-[1.45]"
          spellCheck={false}
          value={text}
          aria-invalid={!parsed.ok || (problems?.length ?? 0) > 0}
          onChange={(e) => {
            setText(e.target.value);
            setProblems(null);
          }}
        />
        <span className="text-[12px] text-muted">
          <Trans
            k="altro.github.pathsHelp"
            slots={{
              a: <span className="font-mono">release.digest</span>, // i18n-ok: nome tecnico
              b: <span className="font-mono">checks[0].name</span>, // i18n-ok: nome tecnico
              c: <span className="font-mono">annotations["ci/version"]</span>, // i18n-ok: nome tecnico
              d: <span className="font-mono">=prod</span>, // i18n-ok: nome tecnico
            }}
          />
        </span>
      </label>
      {!parsed.ok && <p role="alert" className="m-0 text-danger">{t("altro.github.invalidJson", { error: parsed.error })}</p>}
      {problems && problems.length === 0 && <p role="status" className="m-0 text-ok">{t("altro.github.valid")}</p>}
      {problems && problems.length > 0 && (
        <ul role="alert" className="m-0 flex list-none flex-col gap-0.5 border-l-2 border-danger pl-3 text-[12.5px]">
          {problems.map((x) => (
            <li key={x.path + x.message}>
              <span className="font-mono">{x.path || t("altro.github.profileFallback")}</span>: {x.message}
            </li>
          ))}
        </ul>
      )}
      <label className="flex flex-col gap-1.5">
        <span className="label">{t("altro.github.versionNote")}</span>
        <input className="field" maxLength={500} placeholder={t("altro.github.notePh")} value={note} onChange={(e) => setNote(e.target.value)} />
      </label>
      <div className="flex flex-wrap gap-2">
        <button className="btn btn-primary py-1.5" disabled={!parsed.ok || save.isPending} onClick={() => save.mutate()}>
          {dirty ? t("altro.github.saveNew") : t("altro.github.save")}
        </button>
        <button className="btn py-1.5" disabled={!parsed.ok || validate.isPending} onClick={() => validate.mutate()}>{t("altro.github.validate")}</button>
        <a className="btn py-1.5 no-underline" href={githubApi.exportURL(p.id)} download>{t("altro.github.export")}</a>
        <ConfirmButton
          label={t("altro.github.delete")}
          confirm={t("altro.github.deleteProfile")}
          danger
          pending={del.isPending}
          disabled={p.used_by.length > 0}
          title={p.used_by.length ? t("altro.github.usedTitle") : undefined}
          onConfirm={() => del.mutate()}
        />
      </div>
      <MutError errors={[save.error, validate.error, del.error]} />
      <PreviewBox integrations={integrations} definition={parsed.ok ? parsed.value : null} />
      <section className="flex flex-col gap-2">
        <SectionTitle>{t("altro.github.versions")}</SectionTitle>
        <ul className="m-0 flex list-none flex-col p-0 text-[12.5px]">
          {(detail.data?.versions ?? []).map((v) => (
            <li key={v.version} className="flex flex-wrap gap-x-2 border-b border-hair py-1.5">
              <span className="font-mono tabular">v{v.version}</span>
              <span className="text-muted">{dateTime(v.created_at)}</span>
              {v.created_by && <span className="text-muted">· {v.created_by}</span>}
              {v.note && <span>· {v.note}</span>}
            </li>
          ))}
        </ul>
      </section>
    </div>
  );
}

function PreviewBox({ integrations, definition }: { integrations: Integration[]; definition: unknown }) {
  const t = useT();
  const [integ, setInteg] = useState(integrations[0]?.id ?? "");
  const repos = useQuery({ queryKey: ["gh-repos", integ], queryFn: () => githubApi.repos(integ), enabled: !!integ });
  const [repo, setRepo] = useState("");
  const [tag, setTag] = useState("");
  const run = useMutation({ mutationFn: () => githubApi.preview(integ, { repository: repo, definition, tag: tag.trim() || undefined }) });
  const options = (repos.data?.items ?? []).filter((r) => !r.missing && r.status !== "ignored");
  useEffect(() => {
    if (!repo && options.length) setRepo((options.find((r) => r.status === "confirmed") ?? options[0]).full_name);
  }, [repo, options]);
  if (!integrations.length) {
    return <p className="m-0 text-[12.5px] text-muted">{t("altro.github.needIntegration")}</p>;
  }
  return (
    <section className="flex flex-col gap-3">
      <SectionTitle>{t("altro.github.tryTitle")}</SectionTitle>
      <p className="m-0 text-[12.5px] text-muted">{t("altro.github.tryHelp")}</p>
      <div className="flex flex-wrap items-end gap-2">
        {integrations.length > 1 && (
          <label className="flex min-w-[160px] flex-1 flex-col gap-1">
            <span className="label">{t("altro.github.integration")}</span>
            <Select className="field" value={integ} onChange={(e) => { setInteg(e.target.value); setRepo(""); }}>
              {integrations.map((i) => (
                <option key={i.id} value={i.id}>{i.name}</option>
              ))}
            </Select>
          </label>
        )}
        <label className="flex min-w-[200px] flex-[2_1_220px] flex-col gap-1">
          <span className="label">{t("altro.github.repository")}</span>
          <Select className="field" value={repo} onChange={(e) => setRepo(e.target.value)}>
            {options.map((r) => (
              <option key={r.id} value={r.full_name}>{r.full_name}</option>
            ))}
          </Select>
        </label>
        <label className="flex min-w-[110px] flex-1 flex-col gap-1">
          <span className="label">{t("altro.github.tag")}</span>
          <input className="field font-mono" placeholder={t("altro.github.tagPh")} value={tag} onChange={(e) => setTag(e.target.value)} />
        </label>
        <button className="btn btn-primary py-1.5" disabled={!repo || !definition || run.isPending} onClick={() => run.mutate()}>
          {run.isPending ? t("altro.github.tryReading") : t("altro.github.try")}
        </button>
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
          k="altro.github.headline"
          params={{ repository: p.repository, profile: p.profile, requests: p.requests }}
          slots={{ service: <span className="font-mono">{p.service.name}</span>, app: p.service.application ? t("altro.github.appPart", { name: p.service.application }) : "" }}
        />
      </span>
      {r ? (
        <dl className="m-0">
          <KV label="Release">
            <Trans
              k="altro.github.releaseLine"
              params={{ environment: r.environment, date: dateTime(r.at) }}
              slots={{ tag: <span className="font-mono">{p.release_tag}</span>, version: <span className="font-mono">{r.version}</span> }}
            />
          </KV>
          <KV label={t("altro.github.assetsRead")}>
            {p.assets_read.length ? p.assets_read.join(", ") : t("altro.github.none")}
            {p.assets.length > p.assets_read.length && <span className="block text-[12px] text-muted">{t("altro.github.present", { names: p.assets.join(", ") })}</span>}
          </KV>
          {(r.image || r.digest) && (
            <KV label={t("altro.github.artifact")}>
              <span className="block break-all font-mono text-[12px]">{r.image}</span>
              <span className="block break-all font-mono text-[11.5px] text-muted">{r.digest}</span>
            </KV>
          )}
          {r.commit && <KV label="Commit"><span className="font-mono text-[12px]">{r.commit.slice(0, 12)}</span></KV>}
          {(r.change_number || r.change_title) && <KV label="Change">{[r.change_number, r.change_title].filter(Boolean).join(" · ")}</KV>}
          <KV label={t("altro.github.who")}>
            {r.requested_by ? t("altro.github.requestedBy", { name: r.requested_by }) : "—"}
            {r.approved_by && (r.approved_at ? t("altro.github.approvedByOn", { name: r.approved_by, date: dateTime(r.approved_at) }) : t("altro.github.approvedBy", { name: r.approved_by }))}
          </KV>
          {cves && (
            <KV label={t("altro.github.cves")}>
              <span className="tabular">
                {t("altro.github.cveLine", { critical: cves.critical, fixableCritical: cves.fixable_critical, high: cves.high, fixableHigh: cves.fixable_high, medium: cves.medium, low: cves.low })}
              </span>
            </KV>
          )}
          {p.sbom && <KV label="SBOM">{t("altro.github.sbom", { format: p.sbom.format, count: p.sbom.components })}</KV>}
          {r.checks && r.checks.length > 0 && (
            <KV label={t("altro.github.releaseChecks")}>
              <span className="flex flex-wrap gap-1">
                {r.checks.map((c) => (
                  <Tag key={c.name} tone={c.outcome === "passed" ? "ok" : c.outcome === "failed" ? "danger" : "muted"}>
                    {c.name}: {c.outcome}
                  </Tag>
                ))}
              </span>
            </KV>
          )}
        </dl>
      ) : (
        <span className="text-muted">{t("altro.github.noRelease")}</span>
      )}
      {p.check_run && (
        <span className="text-[13px]">
          <Trans
            k="altro.github.lastCommit"
            params={{ ref: p.check_run.ref ?? "", count: p.check_run.checks.length }}
            slots={{
              commit: <span className="font-mono text-[12px]">{p.check_run.commit.slice(0, 12)}</span>,
              verdict: <span className={p.check_run.verdict === "passed" ? "font-semibold text-ok" : "font-semibold text-danger"}>{p.check_run.verdict}</span>,
            }}
          />
        </span>
      )}
      {(p.gitops.length > 0 || p.deployments.length > 0) && (
        <ul className="m-0 flex list-none flex-col p-0 text-[13px]">
          {[...p.gitops, ...p.deployments].map((d, i) => (
            <li key={i} className="border-b border-hair py-1">
              <span className="font-medium">{d.environment}</span> <span className="font-mono text-[12px]">{d.version}</span>{" "}
              <span className="text-muted">{/* i18n-ok: nomi delle fonti */}· {d.source === "gitops" ? "GitOps" : "Deployments API"} · {d.status}</span>
            </li>
          ))}
        </ul>
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
