// Aderenza della pipeline alla politica e adozione guidata (DevSecOps, fase 3;
// replica/devsecops.md §3.3 e §3.4).
// - AdherencePage: /devsecops/aderenza, il portafoglio per applicazione e per asset;
// - PipelineSection: la sezione «Pipeline» della scheda Produzione di un'applicazione o di un asset,
//   con l'elenco guidato «cosa manca» e il pulsante «Proponi la pipeline»;
// - AdoptionWizard: la procedura che genera i file, ne mostra le differenze e apre la pull request.
import { Select } from "../select";
import { useEffect, useRef, useState } from "react";
import { Link } from "@tanstack/react-router";
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  polApi,
  STATUS_NAME,
  MODE_NAME,
  type AdoptionInput,
  type AdoptionPR,
  type AdoptionRepo,
  type AdoptionSource,
  type AppAdherence,
  type ChecklistItem,
  type ControlState,
  type DiffLine,
  type PipelineApp,
  type PreviewFile,
  type Status,
} from "../policyApi";
import { PageHeader } from "../shell";
import { ErrorNote, SectionTitle, ago, Loading } from "../ui";
import { StatusBadge, StatusCounts, useDebounced } from "./PipelinePolicy";
import { ProblemActions } from "./ProblemActions";
import { tr } from "../i18n/core";
import { Trans } from "../i18n/Trans";
import { crumbs } from "../crumbs";
import { DevsecopsTabs } from "./portafoglio-ui";

const STATUSES: Status[] = ["fuori_politica", "parziale", "non_in_pipeline", "aderente"];

function ControlPills({ controls }: { controls: ControlState[] }) {
  const list = controls.filter((c) => c.status !== "na");
  if (list.length === 0) return <span className="text-[12.5px] text-muted">{tr("governance.pipelineadherence.la_politica_non_richiede_controlli")}</span>;
  return (
    <ul className="m-0 flex list-none flex-wrap gap-1.5 p-0">
      {list.map((c) => {
        /* i18n-ok: classi CSS */ const tone = c.status === "ok" ? "border-hair text-fg" : c.status === "weaker" ? "border-rule font-semibold text-fg" : "border-danger font-semibold text-danger";
        const what = c.status === "ok" ? tr("governance.pipelineadherence.ok") : c.status === "missing" ? tr("governance.pipelineadherence.manca") : tr("governance.pipelineadherence.serve", { p: MODE_NAME[c.observed ?? "off"].toLowerCase(), p2: MODE_NAME[c.required].toLowerCase() });
        return (
          <li key={c.key} className={`border px-1.5 py-0.5 text-[12px] ${tone}`} title={tr("governance.pipelineadherence.richiesto", { name: c.name, p: MODE_NAME[c.required].toLowerCase() })}>
            {c.name}: {what}
          </li>
        );
      })}
    </ul>
  );
}

function ServiceRows({ app }: { app: AppAdherence }) {
  if (app.services.length === 0) {
    return <p className="m-0 py-2 text-[12.5px] text-muted">{tr("governance.pipelineadherence.nessun_repository_o_servizio_collegato", { class: app.class })}{" "}{app.required.map((c) => `${c.name} (${MODE_NAME[c.required].toLowerCase()})`).join(", ") || "nessuno"}.</p>;
  }
  return (
    <ul className="m-0 flex list-none flex-col p-0">
      {app.services.map((s) => (
        <li key={s.id} className="flex flex-col gap-1.5 border-t border-hair py-2.5">
          <span className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
            <span className="font-medium">{s.name}</span>
            {s.repository && <span className="font-mono text-[12px] text-muted">{s.repository}</span>}
            <StatusBadge status={s.status} />
            {s.last_run && <span className="text-[12px] text-muted">{tr("governance.pipelineadherence.ultimo_esito", { ago: ago(s.last_run) })}</span>}
          </span>
          {s.status === "non_in_pipeline" ? <span className="text-[12.5px] text-muted">{tr("governance.pipelineadherence.nessun_esito_dei_controlli_e")}</span> : <ControlPills controls={s.controls} />}
        </li>
      ))}
    </ul>
  );
}

// ---------------------------------------------------------------------
// Portafoglio

export function AdherencePage() {
  const q = useQuery({ queryKey: ["pol-adherence"], queryFn: polApi.adherence });
  const d = q.data;
  const [view, setView] = useState<"app" | "asset">("app");
  const [filter, setFilter] = useState<Status | "">("");
  const [open, setOpen] = useState<string | null>(null);
  const [wizard, setWizard] = useState<AppAdherence | null>(null);
  const apps = (d?.applications ?? []).filter((a) => !filter || a.status === filter);
  return (
    <>
      <PageHeader path={crumbs("governance", "devsecops", "adherence")} title={tr("governance.pipelineadherence.aderenza_della_pipeline")} actions={<Link to="/configurazione/devsecops/politica" search={{ tab: "matrice" }} className="btn no-underline">{tr("governance.pipelineadherence.politica")}</Link>} />
      <DevsecopsTabs />
      <div className="flex max-w-6xl flex-col gap-7 px-4 pb-10 md:px-7">
        <ErrorNote error={q.error} />
        {q.isPending && <Loading className="m-0 text-muted" />}
        {d && !d.configured && (
          <p className="m-0 max-w-2xl text-muted">
            <Trans k="governance.pipelineadherence.non_c_e_ancora_una_politica" slots={{ matrix: <Link to="/configurazione/devsecops/politica" search={{ tab: "matrice" }}>{tr("governance.pipelineadherence.matrice_dei_controlli")}</Link> }} />
          </p>
        )}
        {d?.configured && (
          <>
            <section aria-label={tr("governance.compliance.in_sintesi")} className="grid grid-cols-1 gap-x-10 gap-y-5 border-y-2 border-rule py-4 md:grid-cols-2">
              <StatusCounts c={d.apps} label={tr("governance.compliance.applicazioni")} />
              <StatusCounts c={d.services} label={d.unassigned_services ? tr("governance.pipelineadherence.servizi_piu_senza_applicazione", { n: d.unassigned_services }) : tr("governance.devsecops.servizi")} />
            </section>

            <section className="flex flex-col gap-2" aria-label={tr("governance.compliance.controlli")}>
              <SectionTitle aside={<span className="text-[12px] text-muted">{tr("governance.pipelineadherence.servizi_collegati_ad_applicazioni_per")}</span>}>{tr("governance.pipelineadherence.quali_controlli_mancano")}</SectionTitle>
              <ul className="m-0 flex list-none flex-col p-0">
                {d.gaps.map((g) => {
                  const total = g.ok + g.missing + g.weaker;
                  if (total === 0) return null;
                  return (
                    <li key={g.key} className="grid grid-cols-1 items-center gap-x-4 gap-y-1 border-b border-hair py-2 md:grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)_minmax(0,1fr)]">
                      <span className="font-medium">{g.name}</span>
                      <div className="flex h-2 w-full overflow-hidden bg-hair" role="img" aria-label={tr("governance.pipelineadherence.in_regola_troppo_deboli_mancanti", { ok: g.ok, weaker: g.weaker, missing: g.missing })}>
                        <span className="bg-ok" style={{ width: `${(g.ok / total) * 100}%` }} />
                        <span className="bg-fg" style={{ width: `${(g.weaker / total) * 100}%` }} />
                        <span className="bg-danger" style={{ width: `${(g.missing / total) * 100}%` }} />
                      </div>
                      <span className="text-[12.5px] text-muted tabular">{tr("governance.pipelineadherence.in_regola_piu_deboli", { ok: g.ok, weaker: g.weaker })}{" "}<span className={g.missing ? "font-semibold text-danger" : ""}>{tr("governance.pipelineadherence.mancanti", { missing: g.missing })}</span>
                      </span>
                    </li>
                  );
                })}
              </ul>
            </section>

            <section className="flex flex-col gap-3" aria-label={tr("governance.pipelineadherence.elenco")}>
              <div className="flex flex-wrap items-center gap-x-5 gap-y-2 border-b-2 border-rule pb-2">
                <div role="tablist" aria-label={tr("governance.vulnerabilities.vista")} className="flex gap-4">
                  {([["app", tr("governance.vulnerabilities.per_applicazione")], ["asset", tr("governance.devsecops.per_asset")]] as const).map(([k, n]) => (
                    <button key={k} role="tab" aria-selected={view === k} onClick={() => setView(k)} className={`bg-transparent p-0 pb-1 ${view === k ? "border-b-2 border-fg font-semibold text-fg" : "text-muted hover:text-fg"}`}>{n}</button>
                  ))}
                </div>
                {view === "app" && (
                  <div role="group" aria-label={tr("governance.pipelineadherence.filtro_per_stato")} className="ml-auto flex flex-wrap gap-1.5">
                    <button className={`border px-2 py-0.5 text-[12.5px] ${filter === "" ? "border-accent bg-sel font-semibold text-accent" : "border-hair text-muted"}`} aria-pressed={filter === ""} onClick={() => setFilter("")}>{tr("governance.pipelineadherence.tutte", { total: d.apps.total })}</button>
                    {STATUSES.map((s) => (
                      <button key={s} className={`border px-2 py-0.5 text-[12.5px] ${filter === s ? "border-accent bg-sel font-semibold text-accent" : "border-hair text-muted"}`} aria-pressed={filter === s} onClick={() => setFilter(filter === s ? "" : s)}>
                        {STATUS_NAME[s]} {d.apps[s]}
                      </button>
                    ))}
                  </div>
                )}
              </div>
              {view === "app" && (
                <ul className="m-0 flex list-none flex-col p-0">
                  {apps.length === 0 && <li className="py-3 text-muted">{tr("governance.pipelineadherence.nessuna_applicazione_in_questo_stato")}</li>}
                  {apps.map((a) => (
                    <li key={a.id} className="border-b border-hair py-2.5">
                      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
                        <button className="flex min-w-0 flex-1 basis-[220px] flex-col items-start bg-transparent p-0 text-left" aria-expanded={open === a.id} onClick={() => setOpen(open === a.id ? null : a.id)}>
                          <span className="font-medium">{a.name}{a.supports_critical_function && <span className="ml-2 border border-flag px-1 text-[10.5px] font-semibold text-flag">DORA</span>}</span>
                          <span className="text-[12px] text-muted">{tr("governance.pipelineadherence.classe", { key: a.key, class: a.class, length: a.services.length })}{" "}{tr("governance.pipelineadherence.servizio_n", { count: a.services.length })}</span>
                        </button>
                        <StatusBadge status={a.status} />
                        <Link to="/e/$key" params={{ key: a.key }} search={{ scheda: "produzione" }} className="text-[12.5px]">{tr("governance.pipelineadherence.scheda")}</Link>
                      </div>
                      {a.missing.length > 0 && open !== a.id && (
                        <p className="m-0 mt-1 text-[12.5px] text-muted">{tr("governance.pipelineadherence.da_sistemare")}{" "}{a.missing.map((k) => d.gaps.find((g) => g.key === k)?.name ?? k).join(", ")}</p>
                      )}
                      {open === a.id && (
                        <div className="mt-2 pl-0 md:pl-3">
                          <ServiceRows app={a} />
                          {a.status !== "aderente" && (
                            <div className="pt-1"><button className="btn" onClick={() => setWizard(a)}>{tr("governance.pipelineadherence.proponi_la_pipeline")}</button></div>
                          )}
                          {a.status !== "aderente" && (
                            <div className="pt-3">
                              <ProblemActions kind="adherence" refKey={a.key} control={a.missing.map((k) => d.gaps.find((g) => g.key === k)?.name ?? k).join(", ")} />
                            </div>
                          )}
                        </div>
                      )}
                    </li>
                  ))}
                </ul>
              )}
              {view === "asset" && (
                <ul className="m-0 flex list-none flex-col p-0">
                  {d.assets.length === 0 && <li className="py-3 text-muted">{tr("governance.pipelineadherence.nessun_asset_logico_con_applicazioni")}</li>}
                  {d.assets.map((a) => (
                    <li key={a.id} className="flex flex-col gap-1.5 border-b border-hair py-2.5">
                      <Link to="/e/$key" params={{ key: a.key }} search={{ scheda: "produzione" }} className="font-medium no-underline">{a.name}</Link>
                      <StatusCounts c={a.applications} label={tr("governance.pipelineadherence.n_applicazioni", { count: a.applications.total })} />
                    </li>
                  ))}
                </ul>
              )}
              {d.truncated && <p className="m-0 text-[12.5px] text-muted">{tr("governance.pipelineadherence.elenco_limitato_alle_prime_applicazioni")}</p>}
            </section>
          </>
        )}
      </div>
      {wizard && <AdoptionWizard app={{ id: wizard.id, key: wizard.key, name: wizard.name }} onClose={() => setWizard(null)} />}
    </>
  );
}

// ---------------------------------------------------------------------
// Scheda «Pipeline» di un'applicazione o di un asset

export function PipelineSection({ refKey }: { refKey: string }) {
  const q = useQuery({ queryKey: ["pol-pipeline", refKey], queryFn: () => polApi.pipeline(refKey) });
  if (q.isPending) return null;
  if (!q.data) return <ErrorNote error={q.error} />;
  const { pipeline: v, adoptions } = q.data;
  if (v.applications.length === 0) return null;
  return (
    <section className="flex min-w-0 flex-col gap-4" aria-label={tr("governance.pipelineadherence.pipeline_e_politica")}>
      <SectionTitle
        aside={
          <span className="flex gap-3 text-[12.5px]">
            <Link to="/devsecops/aderenza">{tr("governance.pipelineadherence.aderenza")}</Link>
            {v.can_configure && <Link to="/configurazione/devsecops/politica" search={{ tab: "matrice" }}>{tr("governance.pipelineadherence.politica")}</Link>}
          </span>
        }
      >{tr("governance.pipelineadherence.pipeline_e_politica")}</SectionTitle>
      {v.applications.map((a) => (
        <AppPipelineCard key={a.application.id} a={a} prs={adoptions[a.application.id] ?? []} refKey={refKey} />
      ))}
    </section>
  );
}

function ItemMark({ state }: { state: ChecklistItem["state"] }) {
  const m = { ok: ["✓", "text-ok", tr("governance.pipelineadherence.fatto_2")], todo: ["!", "text-danger", tr("governance.pipelineadherence.da_fare")], info: ["…", "text-muted", tr("governance.devsecops.in_corso")] }[state];
  return <span className={`w-4 shrink-0 text-center font-bold ${m[1]}`} aria-label={m[2]} role="img">{m[0]}</span>;
}

function ItemAction({ item, onPropose, canConfigure }: { item: ChecklistItem; onPropose: () => void; canConfigure: boolean }) {
  switch (item.action) {
    case "propose":
      return canConfigure ? <button className="btn py-1" onClick={onPropose}>{tr("governance.pipelineadherence.proponi_la_pipeline")}</button> : null;
    case "policy":
      return canConfigure ? <Link to="/configurazione/devsecops/politica" search={{ tab: "matrice" }} className="text-[12.5px]">{tr("governance.pipelineadherence.definisci_la_politica")}</Link> : null;
    case "template":
      return canConfigure ? <Link to="/configurazione/devsecops/politica" search={{ tab: "adozione" }} className="text-[12.5px]">{tr("governance.pipelineadherence.configura_i_template")}</Link> : null;
    case "repository":
    case "github":
      return canConfigure ? <Link to="/configurazione/integrazioni/github" search={{ tab: item.action === "github" ? "integrazioni" : "repository" }} className="text-[12.5px]">{item.action === "github" ? tr("governance.pipelineadherence.collega_github") : tr("governance.pipelineadherence.associa_un_repository")}</Link> : null;
  }
  return null;
}

function AppPipelineCard({ a, prs, refKey }: { a: PipelineApp; prs: AdoptionPR[]; refKey: string }) {
  const app = a.application;
  const [wizard, setWizard] = useState(false);
  const qc = useQueryClient();
  const sync = useMutation({
    mutationFn: () => polApi.syncAdoptions(app.id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["pol-pipeline", refKey] }),
  });
  const items = a.items;
  const todo = items.some((i) => i.state === "todo" && i.action === "propose");
  return (
    <div className="flex flex-col gap-3 border-t border-hair pt-3">
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <Link to="/e/$key" params={{ key: app.key }} search={{ scheda: "produzione" }} className="font-semibold no-underline">{app.name}</Link>
        {a.configured && <StatusBadge status={app.status} />}
        {a.configured && <span className="text-[12.5px] text-muted">{tr("governance.pipelineadherence.classe_politica_v", { class: app.class, version: a.version })}</span>}
      </div>
      {a.configured && app.required.length > 0 && (
        <p className="m-0 text-[12.5px] text-muted">{tr("governance.pipelineadherence.richiesti")}{" "}{app.required.map((c) => `${c.name} (${MODE_NAME[c.required].toLowerCase()})`).join(", ")}.
        </p>
      )}
      <div>
        <span className="label text-muted">{tr("governance.pipelineadherence.cosa_manca")}</span>
        <ul className="m-0 mt-1 flex list-none flex-col p-0">
          {items.map((it) => (
            <li key={it.key} className="grid grid-cols-[16px_minmax(0,1fr)] gap-x-2 gap-y-0.5 border-b border-hair py-1.5 md:grid-cols-[16px_minmax(0,1fr)_auto]">
              <ItemMark state={it.state} />
              <span className="min-w-0">
                <span className={it.state === "ok" ? "text-muted" : "font-medium"}>{it.label}</span>
                {it.detail && <span className="block text-[12.5px] text-muted">{it.detail}</span>}
              </span>
              <span className="col-start-2 md:col-start-3"><ItemAction item={it} canConfigure={a.can_configure} onPropose={() => setWizard(true)} /></span>
            </li>
          ))}
        </ul>
      </div>
      {app.services.length > 0 && <ServiceRows app={app} />}
      {prs.length > 0 && (
        <div className="flex flex-col gap-1">
          <span className="flex items-center gap-3">
            <span className="label text-muted">{tr("governance.pipelineadherence.pull_request_di_adozione")}</span>
            {a.can_configure && prs.some((p) => p.state === "open") && (
              <button className="btn py-0.5 text-[12px]" disabled={sync.isPending} onClick={() => sync.mutate()}>{sync.isPending ? tr("governance.pipelineadherence.controllo") : tr("governance.pipelineadherence.aggiorna_lo_stato")}</button>
            )}
          </span>
          <ul className="m-0 flex list-none flex-col p-0">
            {prs.slice(0, 5).map((p) => <PRRow key={p.id} p={p} />)}
          </ul>
          <ErrorNote error={sync.error} />
        </div>
      )}
      {a.can_configure && a.configured && !todo && app.status !== "aderente" && (
        <div><button className="btn" onClick={() => setWizard(true)}>{tr("governance.pipelineadherence.proponi_la_pipeline")}</button></div>
      )}
      {wizard && <AdoptionWizard app={{ id: app.id, key: app.key, name: app.name }} onClose={() => { setWizard(false); qc.invalidateQueries({ queryKey: ["pol-pipeline", refKey] }); }} />}
    </div>
  );
}

const prState = (): Record<AdoptionPR["state"], [string, string]> => ({ open: [tr("governance.vulnapi.aperta"), "border-rule text-fg"], merged: [tr("governance.pipelineadherence.unita"), "border-ok text-ok"], closed: [tr("governance.pipelineadherence.chiusa_senza_unire"), "border-hair text-muted"] });

function PRRow({ p }: { p: AdoptionPR }) {
  return (
    <li className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5 border-b border-hair py-1.5">
      <a href={p.url} target="_blank" rel="noreferrer" className="font-mono text-[12.5px]">#{p.number}</a>
      <span className={`border px-1.5 text-[11.5px] font-semibold ${prState()[p.state][1]}`}>{prState()[p.state][0]}</span>
      <span className="min-w-0 text-[12.5px]">{p.repository}</span>
      <span className="text-[12px] text-muted">{p.class ? tr("governance.pipelineadherence.classe_2", { class: p.class }) : ""}{p.policy_version ? tr("governance.pipelineadherence.politica_v", { policy_version: p.policy_version }) : ""} · {ago(p.created_at)}</span>
      {p.last_error && <span className="basis-full text-[12px] text-danger">{p.last_error}</span>}
    </li>
  );
}

// ---------------------------------------------------------------------
// Procedura guidata

function DiffView({ lines }: { lines: DiffLine[] }) {
  // Si mostrano le righe modificate con tre righe di contesto; il resto si piega.
  const keep = new Set<number>();
  lines.forEach((l, i) => {
    if (l.op !== " ") for (let j = Math.max(0, i - 3); j <= Math.min(lines.length - 1, i + 3); j++) keep.add(j);
  });
  const rows: { i: number; l: DiffLine | null; skipped?: number }[] = [];
  let skip = 0;
  lines.forEach((l, i) => {
    if (keep.has(i) || lines.length <= 14) {
      if (skip) rows.push({ i: -i, l: null, skipped: skip });
      skip = 0;
      rows.push({ i, l });
    } else skip++;
  });
  if (skip) rows.push({ i: -1, l: null, skipped: skip });
  return (
    <pre className="m-0 max-h-[320px] overflow-auto border border-hair bg-field font-mono text-[12px] leading-[1.5]" tabIndex={0} aria-label={tr("governance.pipelineadherence.differenze_del_file")}>
      {rows.map((r) =>
        r.l ? (
          <div key={r.i} className={`flex whitespace-pre ${r.l.op === "+" ? "bg-sel" : r.l.op === "-" ? "text-danger" : "text-muted"}`}>
            <span className="w-6 shrink-0 select-none text-center font-bold">{r.l.op === " " ? "" : r.l.op}</span>
            <span>{r.l.text || " "}</span>
          </div>
        ) : (
          <div key={r.i} className="px-6 text-muted">{tr("governance.pipelineadherence.righe_invariate", { count: r.skipped ?? 0 })}</div>
        ),
      )}
    </pre>
  );
}

const FILE_STATUS: Record<PreviewFile["status"], string> = { get new() { return tr("governance.pipelineadherence.nuovo"); }, get changed() { return tr("governance.pipelineadherence.da_aggiornare"); }, get unchanged() { return tr("governance.pipelineadherence.gia_uguale"); } };

export function AdoptionWizard({ app, onClose }: { app: { id: string; key: string; name: string }; onClose: () => void }) {
  const qc = useQueryClient();
  const box = useRef<HTMLDivElement>(null);
  useEffect(() => {
    box.current?.focus();
  }, []);
  const opts = useQuery({ queryKey: ["pol-adoption", app.id], queryFn: () => polApi.options(app.id) });
  const [srcIdx, setSrcIdx] = useState(0);
  const [picked, setPicked] = useState<string[] | null>(null);
  const [params, setParams] = useState<Record<string, string>>({});
  const [repoId, setRepoId] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  const delayedSearch = useDebounced(search.trim(), 300);
  const found = useQuery({ queryKey: ["pol-repos", delayedSearch], queryFn: () => polApi.repositories(delayedSearch), enabled: delayedSearch.length >= 2 });
  const o = opts.data;
  const repos: AdoptionRepo[] = [...(o?.repositories ?? []), ...(found.data?.items ?? []).filter((r) => !(o?.repositories ?? []).some((x) => x.id === r.id))];
  // Il primo repository associato all'applicazione è già scelto.
  const repo = repoId === null ? (o?.repositories.find((r) => r.mapped || r.suggested)?.id ?? "") : repoId;
  const repoRow = repos.find((r) => r.id === repo);
  // Un repository decide l'integrazione e quindi il profilo dei template.
  const src: AdoptionSource | undefined = (repoRow && o?.sources.find((x) => x.integration_id === repoRow.integration_id)) || o?.sources[srcIdx];
  const templates = picked ?? src?.templates.filter((t) => t.default).map((t) => t.key) ?? [];
  const input: AdoptionInput = {
    application_id: app.id,
    templates,
    params,
    ...(repo ? { repository_id: repo } : src?.integration_id ? { integration_id: src.integration_id } : { profile_id: src?.profile_id }),
  };
  const delayedInput = useDebounced(input, 300);
  const preview = useQuery({
    queryKey: ["pol-preview", JSON.stringify(delayedInput)],
    queryFn: () => polApi.preview(delayedInput),
    enabled: !!src && templates.length > 0,
    placeholderData: keepPreviousData,
    retry: false,
  });
  const pv = preview.data;
  const open = useMutation({
    mutationFn: () => polApi.openPR(input),
    onSuccess: () => {
      for (const k of ["pol-pipeline", "pol-adoption", "pol-adherence"]) qc.invalidateQueries({ queryKey: [k] });
    },
  });
  const [downloadError, setDownloadError] = useState<unknown>(null);
  const download = async () => {
    setDownloadError(null);
    try {
      const { blob, name } = await polApi.download(input);
      const a = document.createElement("a");
      a.href = URL.createObjectURL(blob);
      a.download = name;
      a.click();
      setTimeout(() => URL.revokeObjectURL(a.href), 2000);
    } catch (e) {
      setDownloadError(e);
    }
  };
  const changed = pv?.files.some((f) => f.status !== "unchanged") ?? false;
  const openPR = pv ? o?.adoptions.find((a) => a.state === "open" && a.repository.toLowerCase() === pv.repository.toLowerCase()) : undefined;
  const canPR = !!pv && !!repo && pv.can_open_pr && changed && !openPR;
  const created = open.data;

  return (
    <div className="fixed inset-0 z-[70] flex items-start justify-center bg-black/40 px-0 pt-0 md:px-4 md:pt-[4vh]" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div
        ref={box}
        tabIndex={-1}
        role="dialog"
        aria-modal="true"
        aria-label={tr("governance.pipelineadherence.proponi_la_pipeline_per", { name: app.name })}
        onKeyDown={(e) => e.key === "Escape" && onClose()}
        className="flex max-h-screen w-full max-w-[920px] flex-col overflow-y-auto border-[1.5px] border-rule bg-bg outline-none md:max-h-[92vh]"
      >
        <header className="flex items-start gap-3 border-b-2 border-rule px-4 py-3 md:px-5">
          <div className="min-w-0 flex-1">
            <span className="font-mono text-[11.5px] text-muted">{app.key}</span>
            <h2 className="m-0 text-[20px] leading-tight font-bold">{tr("governance.pipelineadherence.proponi_la_pipeline_a", { name: app.name })}</h2>
          </div>
          <button className="btn" onClick={onClose} aria-label={tr("governance.compliance.chiudi")}>{tr("governance.compliance.chiudi")}</button>
        </header>

        <div className="flex flex-col gap-6 px-4 py-4 md:px-5">
          <ErrorNote error={opts.error} />
          {opts.isPending && <Loading className="m-0 text-muted" />}
          {o && o.sources.length === 0 && (
            <p className="m-0 max-w-xl">
              <Trans k="governance.pipelineadherence.nessun_profilo_di_pipeline_ha_template" slots={{ link: <Link to="/configurazione/devsecops/politica" search={{ tab: "adozione" }} onClick={onClose}>{tr("governance.pipelineadherence.politica_della_pipeline_adozione")}</Link> }} />
            </p>
          )}

          {created && (
            <div className="flex flex-col gap-2 border-l-2 border-ok pl-3" role="status">
              <p className="m-0 text-[15px] font-semibold">{tr("governance.pipelineadherence.pull_request_aperta_su", { number: created.number, repository: created.repository })}</p>
              <p className="m-0">
                <a href={created.url} target="_blank" rel="noreferrer">{tr("governance.pipelineadherence.apri_su_github")}</a>{tr("governance.pipelineadherence.quando_la_unisci_lo_stato")}</p>
              <div><button className="btn btn-primary" onClick={onClose}>{tr("governance.pipelineadherence.fatto")}</button></div>
            </div>
          )}

          {o && src && !created && (
            <>
              <section className="flex flex-col gap-3" aria-label={tr("governance.pipelineadherence.template")}>
                <SectionTitle>{tr("governance.pipelineadherence.1_cosa_proporre")}</SectionTitle>
                {o.sources.length > 1 && (
                  <label className="flex max-w-md flex-col gap-1">
                    <span className="label">{tr("governance.pipelineadherence.profilo_dei_template")}</span>
                    <Select className="field" value={srcIdx} onChange={(e) => { setSrcIdx(Number(e.target.value)); setPicked(null); setRepoId(null); }}>
                      {o.sources.map((s, i) => <option key={i} value={i}>{s.profile}{s.integration ? ` · ${s.integration}` : tr("governance.pipelineadherence.solo_file")}</option>)}
                    </Select>
                  </label>
                )}
                <ul className="m-0 flex list-none flex-col p-0">
                  {src.templates.map((t) => (
                    <li key={t.key} className="border-b border-hair py-2">
                      <label className="flex cursor-pointer items-start gap-2.5">
                        <input
                          type="checkbox"
                          className="mt-1"
                          checked={templates.includes(t.key)}
                          onChange={(e) => setPicked(e.target.checked ? [...templates, t.key] : templates.filter((k) => k !== t.key))}
                        />
                        <span className="min-w-0">
                          <span className="font-medium">{t.name}</span>
                          {t.description && <span className="block text-[12.5px] text-muted">{t.description}</span>}
                          <span className="block break-all font-mono text-[12px] text-muted">{t.uses}</span>
                          {t.needs.length > 0 && <span className="block text-[12px] text-muted">{tr("governance.pipelineadherence.richiede")}{" "}{t.needs.join(", ")}</span>}
                        </span>
                      </label>
                    </li>
                  ))}
                </ul>
                {src.params.length > 0 && (
                  <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
                    {src.params.map((p) => (
                      <label key={p.key} className="flex flex-col gap-1">
                        <span className="label">{p.label}</span>
                        {p.options && p.options.length > 0 ? (
                          <Select className="field" value={params[p.key] ?? p.default ?? ""} onChange={(e) => setParams({ ...params, [p.key]: e.target.value })}>
                            {p.options.map((x) => <option key={x}>{x}</option>)}
                          </Select>
                        ) : (
                          <input className="field" value={params[p.key] ?? p.default ?? ""} onChange={(e) => setParams({ ...params, [p.key]: e.target.value })} />
                        )}
                      </label>
                    ))}
                  </div>
                )}
              </section>

              <section className="flex flex-col gap-3" aria-label={tr("governance.devsecops.repository")}>
                <SectionTitle>{tr("governance.pipelineadherence.2_repository")}</SectionTitle>
                <div role="radiogroup" aria-label={tr("governance.devsecops.repository")} className="flex flex-col">
                  {repos.map((r) => (
                    <label key={r.id} className="flex cursor-pointer items-baseline gap-2.5 border-b border-hair py-2">
                      <input type="radio" name="repo" checked={repo === r.id} onChange={() => setRepoId(r.id)} />
                      <span className="font-mono text-[12.5px]">{r.full_name}</span>
                      <span className="text-[12px] text-muted">{r.mapped ? tr("governance.pipelineadherence.associato_a_questa_applicazione") : r.suggested ? tr("governance.pipelineadherence.proposto_per_questa_applicazione") : tr("governance.pipelineadherence.altro_repository")}</span>
                    </label>
                  ))}
                  <label className="flex cursor-pointer items-baseline gap-2.5 border-b border-hair py-2">
                    <input type="radio" name="repo" checked={repo === ""} onChange={() => setRepoId("")} />
                    <span>{tr("governance.pipelineadherence.nessun_repository_solo_i_file")}</span>
                  </label>
                </div>
                <label className="flex max-w-sm flex-col gap-1">
                  <span className="label">{tr("governance.pipelineadherence.cerca_un_altro_repository")}</span>
                  <input className="field" value={search} onChange={(e) => setSearch(e.target.value)} placeholder={tr("governance.pipelineadherence.nome_del_repository")} />
                </label>
                {delayedSearch.length >= 2 && found.data && found.data.items.length === 0 && <p className="m-0 text-[12.5px] text-muted">{tr("governance.pipelineadherence.nessun_repository_scoperto_con_quel")}</p>}
              </section>

              <section className="flex flex-col gap-3" aria-label={tr("governance.pipelineadherence.anteprima")} aria-busy={preview.isFetching}>
                <SectionTitle aside={pv && <span className="text-[12px] text-muted">{tr("governance.pipelineadherence.classe_politica_v_2", { class: pv.class, policy_version: pv.policy_version })}</span>}>{tr("governance.pipelineadherence.3_anteprima_dei_file")}</SectionTitle>
                <ErrorNote error={preview.error} />
                {pv && (
                  <div className={`flex flex-col gap-4 ${preview.isFetching ? "opacity-60" : ""}`}>
                    <p className="m-0 text-[12.5px] text-muted">{tr("governance.pipelineadherence.controlli_richiesti")}{" "}{pv.controls.filter((c) => c.mode !== "off").map((c) => `${c.name} (${MODE_NAME[c.mode].toLowerCase()})`).join(", ") || "nessuno"}.
                    </p>
                    {pv.uncovered.length > 0 && (
                      <p className="m-0 border-l-2 border-rule pl-3 text-[12.5px]">
                        <Trans k="governance.pipelineadherence.i_template_scelti_non_dichiarano_di" slots={{ checks: <strong>{pv.uncovered.join(", ")}</strong> }} />
                      </p>
                    )}
                    {pv.warnings.map((w, i) => <p key={i} className="m-0 border-l-2 border-rule pl-3 text-[12.5px]">{w}</p>)}
                    {pv.files.map((f) => (
                      <div key={f.path} className="flex min-w-0 flex-col gap-1.5">
                        <div className="flex flex-wrap items-baseline gap-x-3">
                          <span className="font-mono text-[12.5px] font-semibold break-all">{f.path}</span>
                          <span className="text-[12px] text-muted">{FILE_STATUS[f.status]}</span>
                          {f.status !== "unchanged" && <span className="text-[12px] tabular text-muted">+{f.added} −{f.removed}</span>}
                        </div>
                        {f.status !== "unchanged" ? <DiffView lines={f.diff} /> : <p className="m-0 text-[12.5px] text-muted">{tr("governance.pipelineadherence.il_repository_ha_gia_questo")}</p>}
                      </div>
                    ))}
                  </div>
                )}
              </section>

              {pv && (
                <footer className="flex flex-col gap-2 border-t-2 border-rule pt-3">
                  <ErrorNote error={open.error} />
                  <ErrorNote error={downloadError} />
                  <div className="flex flex-wrap items-center gap-3">
                    {canPR && <button className="btn btn-primary" disabled={open.isPending} onClick={() => open.mutate()}>{open.isPending ? tr("governance.pipelineadherence.apro_la_pull_request") : tr("governance.pipelineadherence.apri_la_pull_request_su", { repository: pv.repository })}</button>}
                    <button className="btn" onClick={download}>{tr("governance.pipelineadherence.scarica_i_file_zip")}</button>
                  </div>
                  {openPR && <p className="m-0 border-l-2 border-rule pl-3 text-[12.5px]"><Trans k="governance.pipelineadherence.c_e_gia_una_pull_request" slots={{ link: <a href={openPR.url} target="_blank" rel="noreferrer">#{openPR.number}</a> }} /></p>}
                  {!canPR && !openPR && <p className="m-0 text-[12.5px] text-muted">{!repo ? tr("governance.pipelineadherence.scegli_un_repository_per_aprire") : !changed ? tr("governance.pipelineadherence.il_repository_e_gia_allineato") : pv.can_open_reason}</p>}
                  {canPR && pv.can_open_reason && <p className="m-0 text-[12.5px] text-muted">{pv.can_open_reason}</p>}
                  <p className="m-0 text-[12px] text-muted">{tr("governance.pipelineadherence.regia_crea_un_branch_e")}</p>
                </footer>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  );
}
