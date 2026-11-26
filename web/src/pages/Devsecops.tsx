// DevSecOps, fase 1 (piano, passo 21; replica/devsecops.md §3.1): cosa gira in produzione per
// un asset o un'applicazione, con quali CVE, approvato da chi e per quale richiesta.
// - ProductionPanel: scheda «Produzione» dell'asset logico e «Servizi e rilasci» dell'applicazione;
// - DevsecopsPage: /devsecops, il portafoglio;
// - DevsecopsConfigPage: /configurazione/devsecops.
// I dati arrivano dalle pipeline (API di push e connettori): Regia non esegue scansioni.
import { Select } from "../select";
import { lazy, Suspense, useState, type FormEvent, type ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  dsoApi,
  type CVECounts,
  type ChangeGroup,
  type CheckResult,
  type ChecksStatus,
  type Config,
  type Env,
  type KPIs,
  type Overview,
  type Panel,
  type Release,
  type RuntimeEOL,
  type ServiceRow,
  type Trend,
} from "../devsecopsApi";
import { PageHeader, useMe } from "../shell";
import { enterpriseOn } from "../modules";
import { ModuleChip } from "../moduleIcon";
import { Criticality, ErrorNote, SectionTitle, ago, Loading } from "../ui";
import { useNarrow } from "./timeline";
import { GateRecent } from "./GatePanel";
import { tr } from "../i18n/core";
import { Trans } from "../i18n/Trans";
import { loc } from "../i18n/format";
import { crumbs } from "../crumbs";
import { DevsecopsTabs } from "./portafoglio-ui";

// Fase 3: politica della pipeline e adozione guidata, caricate a parte.
const PipelineSection = lazy(() => import("./PipelineAdherence").then((m) => ({ default: m.PipelineSection })));

// ---------------------------------------------------------------------
// Pezzi comuni

/** Parti da azienda (change, approvazioni, asset logici): solo con demand o GRC accesi. */
function useEnterprise(): boolean {
  return enterpriseOn(useMe().data);
}

function when(iso: string | null | undefined): string {
  return iso ? new Date(iso).toLocaleDateString(loc(), { day: "2-digit", month: "2-digit", year: "numeric" }) : "—";
}

/** Numero grande con il confronto a sette giorni (in rosso se peggiora, in verde se migliora). */
function KpiNumber({ label, t, note, i }: { label: string; t: Trend; note?: ReactNode; i?: number }) {
  const diff = t.now - t.week_ago;
  const tone = diff > 0 ? "text-danger" : diff < 0 ? "text-ok" : "text-muted";
  const delta = diff === 0 ? tr("governance.devsecops.uguale_a_7_giorni_fa") : tr("governance.devsecops.rispetto_a_7_giorni_fa", { p: diff > 0 ? "+" : "−", p2: Math.abs(diff) });
  return (
    <div className="entra relative flex min-w-0 flex-col gap-1.5 border border-hair px-3.5 pb-3 pt-3 m-devsecops" style={{ background: "linear-gradient(color-mix(in srgb, var(--mc) 6%, var(--bg)), var(--bg))", animationDelay: `${(i ?? 0) * 60}ms` }}>
      <span aria-hidden="true" className="absolute -left-px -right-px -top-px h-1 bg-mc" />
      <span className="label flex items-center gap-2 text-muted"><ModuleChip module="devsecops" size="sm" />{label}</span>
      <span className={`font-mono text-[34px] leading-none font-semibold tabular ${t.now > 0 ? "text-fg" : "text-muted"}`}>{t.now}</span>
      <span className={`text-[12px] tabular ${tone}`}>
        {delta} <span className="text-muted">({t.week_ago})</span>
      </span>
      {note && <span className="text-[12px] text-muted">{note}</span>}
    </div>
  );
}

function Kpis({ k }: { k: KPIs }) {
  return (
    <section aria-label={tr("governance.compliance.in_sintesi")} className="grid grid-cols-1 gap-3 sm:grid-cols-3">
      <KpiNumber i={0}
        label={tr("governance.devsecops.cve_critiche_in_produzione")}
        t={k.critical_cves}
        note={
          <>{tr("governance.devsecops.ancora_da_correggere_non_in")}{!!k.unscanned && <> · {k.unscanned} {k.unscanned === 1 ? tr("governance.devsecops.servizio_senza_scansione") : tr("governance.devsecops.servizi_senza_scansione")}{" "}{tr("governance.devsecops.di_quella_versione")}</>}
          </>
        }
      />
      <KpiNumber i={1} label={tr("governance.devsecops.controlli_bloccanti_falliti_o_in")} t={k.blocking_checks} note={tr("governance.devsecops.servizi_sul_rilascio_in_produzione")} />
      <KpiNumber i={2}
        label={tr("governance.devsecops.non_rilasciati_da_piu_di", { stale_days: k.stale_days })}
        t={k.stale}
        note={
          <>
            <Trans k="governance.devsecops.di_servizi_sulla_pipeline" slots={{ on: <span className="font-mono tabular text-fg">{k.on_pipeline}</span>, total: <span className="font-mono tabular">{k.services}</span> }} />
          </>
        }
      />
    </section>
  );
}

/** Critiche e alte, con lo strato (immagine o applicazione) se c'è. */
function Cves({ c, layers }: { c: CVECounts | null; layers?: boolean }) {
  if (!c) return <span className="text-muted">—</span>;
  return (
    <span className="inline-flex flex-col gap-0.5">
      <span className="font-mono tabular whitespace-nowrap">
        <span className={c.critical > 0 ? "font-semibold text-danger" : ""} title={tr("governance.devsecops.critiche")}>{c.critical} C</span>
        <span className="text-muted"> · </span>
        <span className={c.high > 0 ? "font-semibold" : ""} title={tr("governance.devsecops.alte_2")}>{c.high} H</span>
      </span>
      {layers && !c.declared && (c.critical + c.high > 0) && (
        <span className="text-[11.5px] whitespace-nowrap text-muted">{tr("governance.devsecops.immagine_app", { critical: c.image.critical, high: c.image.high, critical2: c.app.critical, high2: c.app.high })}</span>
      )}
      {c.declared && <span className="text-[11.5px] text-muted">{tr("governance.devsecops.dichiarate_dal_rilascio")}</span>}
      {!c.declared && c.scanned_at && <span className="text-[11.5px] whitespace-nowrap text-muted">{(c.scanners ?? []).join(", ")} · {ago(c.scanned_at)}</span>}
    </span>
  );
}

const STATUS_TEXT: Record<ChecksStatus, string> = {
  get ok() { return tr("governance.devsecops.superati"); },
  get warn() { return tr("governance.devsecops.avvisi"); },
  get exception() { return tr("governance.devsecops.in_eccezione"); },
  get failed() { return tr("governance.devsecops.bloccante_fallito"); },
  get none() { return tr("governance.devsecops.nessun_dato"); },
};

function ChecksBadge({ status, checks }: { status: ChecksStatus; checks: CheckResult[] }) {
  const tone = status === "failed" ? "border-danger text-danger" : status === "exception" ? "border-danger text-fg" : status === "warn" ? "border-hair text-fg" : "border-hair text-muted";
  const detail = checks.map((c) => `${c.name} (${c.mode}): ${c.outcome}${c.skip ? `${tr("governance.devsecops.eccezione_di", { by: c.skip.by })}${c.skip.reason ? ": " + c.skip.reason : ""}` : ""}`).join("\n");
  const exc = checks.filter((c) => c.skip);
  return (
    <span className="inline-flex flex-col gap-0.5" title={detail || undefined}>
      <span className={`self-start border px-1.5 text-[11.5px] font-semibold whitespace-nowrap ${tone}`}>{STATUS_TEXT[status]}</span>
      {exc.length > 0 && <span className="text-[11.5px] text-muted">{exc.map((c) => `${c.name}: ${c.skip!.by}`).join(", ")}</span>}
    </span>
  );
}

function Runtimes({ list }: { list: ServiceRow["runtimes"] }) {
  if (list.length === 0) return <span className="text-muted">—</span>;
  return (
    <span className="inline-flex flex-col gap-0.5">
      {list.map((r) => (
        <span key={r.name + r.version} className={`whitespace-nowrap ${r.unsupported ? "font-semibold text-danger" : ""}`}
          title={r.end_of_support ? tr("governance.devsecops.fine_supporto", { when: when(r.end_of_support) }) : tr("governance.devsecops.fine_supporto_non_configurata")}>
          {r.name} <span className="font-mono tabular">{r.version}</span>
          {r.unsupported && <span className="text-[11.5px] font-normal">{tr("governance.devsecops.fuori_supporto")}</span>}
        </span>
      ))}
    </span>
  );
}

function ChangeLink({ r }: { r: Release }) {
  if (!r.change) return <span className="text-muted">{r.change_number ?? "—"}</span>;
  return (
    <span className="inline-flex flex-col gap-0.5">
      <span className="font-mono text-[12px]">{r.change.number}</span>
      {r.change.request_key && r.change.request_key !== r.change.number && (
        <Link to="/e/$key" params={{ key: r.change.request_key }} className="text-[12px] no-underline" title={r.change.request_title ?? undefined}>
          {r.change.request_key}
        </Link>
      )}
    </span>
  );
}

function Released({ r }: { r: Release }) {
  return (
    <span className="inline-flex flex-col gap-0.5">
      <span className="tabular whitespace-nowrap">{when(r.at)}</span>
      {r.requested_by && <span className="text-[12px] text-muted">{tr("governance.devsecops.chiesto_da", { requested_by: r.requested_by })}</span>}
      {r.approved_by && <span className="text-[12px]">{tr("governance.devsecops.approvato_da", { approved_by: r.approved_by })}</span>}
    </span>
  );
}

// ---------------------------------------------------------------------
// Pannello dell'asset o dell'applicazione

export function ProductionPanel({ refKey }: { refKey: string }) {
  const panel = useQuery({ queryKey: ["dso-panel", refKey], queryFn: () => dsoApi.panel(refKey) });
  const enterprise = useEnterprise();
  const p = panel.data;
  if (panel.isPending) return <Loading className="m-0 text-muted" />;
  if (!p) return <ErrorNote error={panel.error} />;
  const isAsset = p.subject.type === "logical_asset";
  if (p.services.length === 0) {
    return (
      <div className="flex flex-col gap-3">
        <p className="m-0 max-w-2xl text-muted">
          {isAsset ? tr("governance.devsecops.nessun_servizio_per_le_applicazioni") : tr("governance.devsecops.nessun_servizio_per_questa_applicazione")}{" "}{tr("governance.devsecops.i_servizi_arrivano_dalle_pipeline")}</p>
        {p.can_configure && <PlannedService panel={p} />}
        <Suspense fallback={null}><PipelineSection refKey={refKey} /></Suspense>
      </div>
    );
  }
  return (
    <div className="flex min-w-0 flex-col gap-7">
      <Kpis k={p.kpis} />
      <ProductionTable panel={p} />
      <Environments services={p.services} envs={p.environments} />
      {enterprise && <Changes groups={p.changes} />}
      <Suspense fallback={null}><PipelineSection refKey={refKey} /></Suspense>
      {p.can_configure && (
        <div className="flex flex-col gap-2">
          <PlannedService panel={p} />
          <Link to="/configurazione/devsecops" className="self-start text-[12.5px]">{tr("governance.devsecops.configurazione_devsecops")}</Link>
        </div>
      )}
    </div>
  );
}

function ProductionTable({ panel }: { panel: Panel }) {
  const narrow = useNarrow();
  const enterprise = useEnterprise();
  const multiApp = panel.applications.length > 1;
  const active = panel.services.filter((s) => s.on_pipeline);
  const pending = panel.services.filter((s) => !s.on_pipeline);
  return (
    <section className="flex min-w-0 flex-col gap-2" aria-label={tr("governance.devsecops.produzione")}>
      <SectionTitle aside={<span className="text-[12px] text-muted">{tr("governance.devsecops.sulla_pipeline_da_portare", { length: active.length, length2: pending.length })}</span>}>{tr("governance.devsecops.produzione")}</SectionTitle>
      {narrow ? (
        <ul className="m-0 flex list-none flex-col p-0">
          {[...active, ...pending].map((s) => <ServiceCard key={s.id} s={s} multiApp={multiApp} enterprise={enterprise} />)}
        </ul>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[980px] border-collapse">
            <thead>
              <tr className="label h-9 text-left">
                <th scope="col" className="border-b-2 border-rule pr-3">{tr("governance.devsecops.servizio")}</th>
                <th scope="col" className="border-b-2 border-rule px-3">{tr("governance.compliance.versione")}</th>
                <th scope="col" className="border-b-2 border-rule px-3">{tr("governance.devsecops.rilasciato")}</th>
                {enterprise && <th scope="col" className="border-b-2 border-rule px-3">{tr("governance.devsecops.change")}</th>}
                <th scope="col" className="border-b-2 border-rule px-3">{tr("governance.devsecops.cve_oggi")}</th>
                <th scope="col" className="border-b-2 border-rule px-3">{tr("governance.devsecops.al_rilascio")}</th>
                <th scope="col" className="border-b-2 border-rule px-3">{tr("governance.compliance.controlli")}</th>
                <th scope="col" className="border-b-2 border-rule pl-3">{tr("governance.devsecops.runtime")}</th>
              </tr>
            </thead>
            <tbody>
              {active.map((s) => <ServiceTr key={s.id} s={s} multiApp={multiApp} enterprise={enterprise} />)}
              {pending.map((s) => (
                <tr key={s.id} className="border-b border-hair text-muted">
                  <td className="py-2 pr-3">
                    <span className="font-medium">{s.name}</span>
                    {multiApp && s.application && <span className="block text-[12px]">{s.application}</span>}
                  </td>
                  <td colSpan={enterprise ? 7 : 6} className="px-3 py-2 text-[12.5px]">{tr("governance.devsecops.non_ancora_sulla_pipeline")}{s.repository ? ` · ${s.repository}` : ""}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

function ServiceName({ s, multiApp }: { s: ServiceRow; multiApp: boolean }) {
  return (
    <span className="inline-flex min-w-0 flex-col gap-0.5">
      {s.url ? (
        <a href={s.url} target="_blank" rel="noreferrer" className="font-medium whitespace-nowrap text-fg">{s.name}</a>
      ) : (
        <span className="font-medium whitespace-nowrap">{s.name}</span>
      )}
      {multiApp && s.application_key && (
        <Link to="/e/$key" params={{ key: s.application_key }} className="text-[12px] text-muted no-underline hover:text-accent">{s.application}</Link>
      )}
      {s.stale && <span className="text-[12px] font-semibold">{tr("governance.devsecops.fermo_da_giorni", { days_since_release: s.days_since_release ?? "" })}</span>}
    </span>
  );
}

function ServiceTr({ s, multiApp, enterprise }: { s: ServiceRow; multiApp: boolean; enterprise: boolean }) {
  const [open, setOpen] = useState(false);
  const r = s.release;
  return (
    <>
      <tr className="border-b border-hair align-top">
        <td className="py-2 pr-3">
          <div className="flex items-start gap-2">
            <button className="mt-0.5 w-4 shrink-0 font-mono text-[12px] text-muted hover:text-fg" aria-expanded={open}
              aria-label={tr(open ? "governance.devsecops.nascondi_le_versioni_precedenti_di" : "governance.devsecops.mostra_le_versioni_precedenti_di", { name: s.name })} onClick={() => setOpen((o) => !o)}>
              {open ? "−" : "+"}
            </button>
            <ServiceName s={s} multiApp={multiApp} />
          </div>
        </td>
        <td className="px-3 py-2 font-mono text-[12.5px] tabular">
          {s.prod_version || <span className="font-sans text-muted">{tr("governance.devsecops.mai_in_produzione")}</span>}
          {r?.image && <span className="block max-w-[180px] [overflow-wrap:anywhere] text-[11px] text-muted" title={`${r.image}${r.digest ? "@" + r.digest : ""}`}>{r.digest?.slice(0, 19) ?? r.image}</span>}
        </td>
        <td className="px-3 py-2">{r ? <Released r={r} /> : <span className="text-muted">{s.prod_since ? when(s.prod_since) : "—"}</span>}</td>
        {enterprise && <td className="px-3 py-2">{r ? <ChangeLink r={r} /> : <span className="text-muted">—</span>}</td>}
        <td className="px-3 py-2"><Cves c={s.cves_today} layers /></td>
        <td className="px-3 py-2"><Cves c={s.cves_at_release} /></td>
        <td className="px-3 py-2">{r ? <ChecksBadge status={r.checks_status} checks={r.checks} /> : <span className="text-muted">—</span>}</td>
        <td className="py-2 pl-3"><Runtimes list={s.runtimes} /></td>
      </tr>
      {open && (
        <tr className="border-b border-hair bg-sel/30">
          <td colSpan={enterprise ? 8 : 7} className="px-3 py-3">
            <History id={s.id} />
          </td>
        </tr>
      )}
    </>
  );
}

function ServiceCard({ s, multiApp, enterprise }: { s: ServiceRow; multiApp: boolean; enterprise: boolean }) {
  const [open, setOpen] = useState(false);
  const r = s.release;
  if (!s.on_pipeline) {
    return (
      <li className="border-b border-hair py-3 text-muted">
        <span className="font-medium">{s.name}</span> <span className="text-[12.5px]">{tr("governance.devsecops.non_ancora_sulla_pipeline_2")}</span>
      </li>
    );
  }
  const row = (k: string, v: ReactNode) => (
    <div className="grid grid-cols-[96px_minmax(0,1fr)] gap-2 py-0.5">
      <dt className="label pt-0.5 text-muted">{k}</dt>
      <dd className="m-0 min-w-0">{v}</dd>
    </div>
  );
  return (
    <li className="flex flex-col gap-2 border-b border-hair py-3">
      <div className="flex items-baseline justify-between gap-3">
        <ServiceName s={s} multiApp={multiApp} />
        <span className="font-mono text-[13px] tabular">{s.prod_version || "—"}</span>
      </div>
      <dl className="m-0 flex flex-col">
        {row(tr("governance.devsecops.rilasciato"), r ? <Released r={r} /> : "—")}
        {enterprise && row(tr("governance.devsecops.change"), r ? <ChangeLink r={r} /> : "—")}
        {row(tr("governance.devsecops.cve_oggi"), <Cves c={s.cves_today} layers />)}
        {row(tr("governance.devsecops.al_rilascio"), <Cves c={s.cves_at_release} />)}
        {row(tr("governance.compliance.controlli"), r ? <ChecksBadge status={r.checks_status} checks={r.checks} /> : "—")}
        {row(tr("governance.devsecops.runtime"), <Runtimes list={s.runtimes} />)}
      </dl>
      <button className="self-start text-[12.5px] text-accent underline" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
        {open ? tr("governance.devsecops.nascondi_le_versioni_precedenti") : tr("governance.devsecops.versioni_precedenti")}
      </button>
      {open && <History id={s.id} />}
    </li>
  );
}

/** Storico delle versioni di un servizio, caricato all'apertura. */
function History({ id }: { id: string }) {
  const h = useQuery({ queryKey: ["dso-service", id], queryFn: () => dsoApi.service(id) });
  if (h.isPending) return <Loading className="m-0 text-muted" />;
  if (!h.data) return <ErrorNote error={h.error} />;
  const rels = h.data.history;
  if (rels.length === 0) return <p className="m-0 text-muted">{tr("governance.devsecops.nessun_rilascio_registrato")}</p>;
  return (
    <ol className="m-0 flex list-none flex-col p-0 text-[12.5px]">
      {rels.map((r) => (
        <li key={r.id} className="grid grid-cols-[minmax(0,1fr)_auto] gap-x-3 gap-y-0.5 border-b border-hair py-1.5 last:border-b-0 sm:grid-cols-[110px_90px_minmax(0,1fr)_auto]">
          <span className="font-mono tabular">{r.version}</span>
          <span className={r.production ? "font-semibold" : "text-muted"}>{r.environment}</span>
          <span className="col-span-2 text-muted sm:col-span-1">
            {when(r.at)}
            {r.approved_by ? tr("governance.devsecops.approvato_da_2", { approved_by: r.approved_by }) : ""}
            {r.change ? ` · ${r.change.number}` : ""}
            {r.change?.request_key && r.change.request_key !== r.change.number ? ` (${r.change.request_key})` : ""}
          </span>
          <span className="col-span-2 sm:col-span-1"><ChecksBadge status={r.checks_status} checks={r.checks} /></span>
        </li>
      ))}
    </ol>
  );
}

function Environments({ services, envs }: { services: ServiceRow[]; envs: Env[] }) {
  const active = services.filter((s) => Object.keys(s.environments).length > 0);
  if (envs.length === 0 || active.length === 0) return null;
  return (
    <section className="flex min-w-0 flex-col gap-2" aria-label={tr("governance.devsecops.ambienti")}>
      <SectionTitle aside={<span className="text-[12px] text-muted">{tr("governance.devsecops.in_grassetto_le_versioni_diverse")}</span>}>{tr("governance.devsecops.ambienti")}</SectionTitle>
      <div className="overflow-x-auto">
        <table className="w-full border-collapse" style={{ minWidth: 160 + envs.length * 130 }}>
          <thead>
            <tr className="label h-9 text-left">
              <th scope="col" className="border-b-2 border-rule pr-3">{tr("governance.devsecops.servizio")}</th>
              {envs.map((e) => (
                <th key={e.id} scope="col" className="border-b-2 border-rule px-3">
                  {e.name}
                  {e.production && !/prod/i.test(e.name) && <span className="ml-1 font-mono text-[10px] text-muted normal-case">{tr("governance.devsecops.produzione_2")}</span>}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {active.map((s) => (
              <tr key={s.id} className="border-b border-hair">
                <td className="py-1.5 pr-3 font-medium">{s.name}</td>
                {envs.map((e) => {
                  const c = s.environments[e.name];
                  if (!c) return <td key={e.id} className="px-3 py-1.5 text-muted">—</td>;
                  const differs = !e.production && !!c.version && c.version !== s.prod_version;
                  return (
                    <td key={e.id} className="px-3 py-1.5" title={tr("governance.devsecops.ultimo_tentativo_il", { last_status: c.last_status, p: new Date(c.last_at).toLocaleString(loc()) })}>
                      <span className={`font-mono text-[12.5px] tabular ${differs ? "font-semibold" : e.production ? "" : "text-muted"}`}>{c.version || "—"}</span>
                      {(c.last_status === "failed" || c.last_status === "rolled_back") && (
                        <span className="block text-[11.5px] text-danger">{c.last_status === "failed" ? tr("governance.devsecops.ultimo_deploy_fallito") : tr("governance.devsecops.ritorno_indietro")}</span>
                      )}
                      {c.last_status === "running" && <span className="block text-[11.5px] text-muted">{tr("governance.devsecops.in_corso")}</span>}
                    </td>
                  );
                })}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function Changes({ groups }: { groups: ChangeGroup[] }) {
  if (groups.length === 0) return null;
  return (
    <section className="flex min-w-0 flex-col gap-2" aria-label={tr("governance.devsecops.rilasci_per_change")}>
      <SectionTitle>{tr("governance.devsecops.rilasci_per_change")}</SectionTitle>
      <ul className="m-0 flex list-none flex-col p-0">
        {groups.map((g) => (
          <li key={g.change?.id ?? "senza"} className="flex flex-col gap-1 border-b border-hair py-2.5">
            <div className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5">
              {g.change ? (
                <>
                  <span className="font-mono text-[12.5px] font-semibold">{g.change.number}</span>
                  {g.change.aliases.length > 0 && <span className="font-mono text-[11.5px] text-muted">{tr("governance.devsecops.gia")}{" "}{g.change.aliases.join(", ")}</span>}
                  {g.change.request_key ? (
                    <Link to="/e/$key" params={{ key: g.change.request_key }} className="no-underline">
                      {g.change.request_key !== g.change.number && <span className="font-mono text-[12px]">{g.change.request_key} </span>}
                      {g.change.request_title}
                    </Link>
                  ) : (
                    <span className="text-[12.5px] text-muted">{g.change.title || tr("governance.devsecops.nessuna_richiesta_collegata")}</span>
                  )}
                  {g.change.request_state && <span className="text-[12px] text-muted">{g.change.request_state}</span>}
                </>
              ) : (
                <span className="text-[12.5px] font-semibold text-muted">{tr("governance.devsecops.senza_numero_di_change")}</span>
              )}
            </div>
            <ul className="m-0 flex list-none flex-col gap-0.5 p-0 text-[12.5px]">
              {g.releases.map((r) => (
                <li key={r.id} className="flex flex-wrap gap-x-2">
                  <span className="font-medium">{r.service}</span>
                  <span className="font-mono tabular">{r.version}</span>
                  <span className={r.production ? "" : "text-muted"}>{r.environment}</span>
                  <span className="text-muted tabular">{when(r.at)}</span>
                  {r.approved_by && <span className="text-muted">{tr("governance.devsecops.approvato_da", { approved_by: r.approved_by })}</span>}
                </li>
              ))}
            </ul>
          </li>
        ))}
      </ul>
    </section>
  );
}

/** Censisce un servizio non ancora sulla pipeline (la migrazione così ha un numero). */
function PlannedService({ panel }: { panel: Panel }) {
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [repo, setRepo] = useState("");
  const [app, setApp] = useState(panel.applications[0]?.key ?? "");
  const create = useMutation({
    mutationFn: () => dsoApi.createService(name.trim(), app, repo.trim() || undefined),
    onSuccess: () => {
      setName("");
      setRepo("");
      setOpen(false);
      qc.invalidateQueries({ queryKey: ["dso-panel"] });
    },
  });
  if (panel.applications.length === 0) return null;
  if (!open) {
    return (
      <button className="self-start text-[12.5px] text-accent underline" onClick={() => setOpen(true)}>{tr("governance.devsecops.censisci_un_servizio_non_ancora")}</button>
    );
  }
  return (
    <form className="flex flex-wrap items-end gap-3" onSubmit={(e: FormEvent) => { e.preventDefault(); create.mutate(); }}>
      <label className="flex min-w-0 flex-[1_1_200px] flex-col gap-1">
        <span className="label">{tr("governance.devsecops.servizio")}</span>
        <input className="field" required maxLength={200} value={name} onChange={(e) => setName(e.target.value)} placeholder={tr("governance.devsecops.es_orders_reporting")} />
      </label>
      <label className="flex min-w-0 flex-[1_1_200px] flex-col gap-1">
        <span className="label">{tr("governance.devsecops.repository")}</span>
        <input className="field" maxLength={300} value={repo} onChange={(e) => setRepo(e.target.value)} placeholder={tr("governance.devsecops.facoltativo")} />
      </label>
      {panel.applications.length > 1 && (
        <label className="flex min-w-0 flex-[1_1_200px] flex-col gap-1">
          <span className="label">{tr("governance.requirements.applicazione")}</span>
          <Select className="field" value={app} onChange={(e) => setApp(e.target.value)}>
            {panel.applications.map((a) => <option key={a.id} value={a.key}>{a.name}</option>)}
          </Select>
        </label>
      )}
      <button className="btn btn-primary" disabled={create.isPending}>{tr("governance.devsecops.censisci")}</button>
      <button type="button" className="btn" onClick={() => setOpen(false)}>{tr("governance.compliance.annulla")}</button>
      <div className="basis-full"><ErrorNote error={create.error} /></div>
    </form>
  );
}

// ---------------------------------------------------------------------
// /devsecops: il portafoglio

export function DevsecopsPage() {
  const o = useQuery({ queryKey: ["dso-overview"], queryFn: dsoApi.overview });
  const enterprise = useEnterprise();
  const d = o.data;
  const narrow = useNarrow();
  return (
    <>
      <PageHeader path={crumbs("governance", "devsecops")} title={tr("governance.devsecops.devsecops")} />
      <DevsecopsTabs />
      <div className="flex flex-col gap-8 px-4 pb-10 md:px-7">
        <p className="m-0 max-w-3xl text-muted">{tr("governance.devsecops.cosa_gira_in_produzione_con")}</p>
        <ErrorNote error={o.error} />
        {o.isPending && <Loading className="m-0 text-muted" />}
        {d && (
          <>
            <Kpis k={d.kpis} />
            {d.unmapped_services > 0 && (
              <p className="m-0 border-l-2 border-rule pl-3">
                <span className="font-semibold">{d.unmapped_services}</span>{" "}{tr("governance.devsecops.servizi_senza_applicazione_del_catalogo")}{" "}<Link to="/configurazione/devsecops">{tr("governance.devsecops.associali")}</Link>.
              </p>
            )}
            {/* Senza demand né GRC gli asset logici non servono: niente riga «Senza asset», e la tabella solo se ce ne sono. */}
            {(enterprise || d.assets.length > 0) && (
              <AssetsTable assets={enterprise && d.unassigned.services > 0 ? [...d.assets, d.unassigned] : d.assets} narrow={narrow} />
            )}
            <CriticalTable rows={d.critical} narrow={narrow} />
            <LatestReleases rels={d.releases} enterprise={enterprise} />
            <GateRecent />
          </>
        )}
      </div>
    </>
  );
}

function AssetsTable({ assets, narrow }: { assets: Overview["assets"]; narrow: boolean }) {
  return (
    <section className="flex min-w-0 flex-col gap-2" aria-label={tr("governance.devsecops.per_asset")}>
      <SectionTitle>{tr("governance.devsecops.per_asset")}</SectionTitle>
      {assets.length === 0 && <p className="m-0 text-muted">{tr("governance.devsecops.nessun_asset_logico_nel_catalogo")}</p>}
      <div className="overflow-x-auto">
        <table className={`w-full border-collapse ${narrow ? "" : "min-w-[760px]"}`}>
          <thead>
            <tr className="label h-9 text-left">
              <th scope="col" className="border-b-2 border-rule pr-3">{tr("governance.devsecops.asset")}</th>
              <th scope="col" className="border-b-2 border-rule px-3 text-right">{tr("governance.devsecops.servizi")}</th>
              <th scope="col" className="border-b-2 border-rule px-3 text-right">{tr("governance.devsecops.cve_critiche")}</th>
              {!narrow && <th scope="col" className="border-b-2 border-rule px-3 text-right">{tr("governance.devsecops.alte")}</th>}
              {!narrow && <th scope="col" className="border-b-2 border-rule px-3 text-right">{tr("governance.compliance.controlli")}</th>}
              {!narrow && <th scope="col" className="border-b-2 border-rule px-3 text-right">{tr("governance.devsecops.fermi")}</th>}
              {!narrow && <th scope="col" className="border-b-2 border-rule px-3 text-right">{tr("governance.devsecops.runtime")}</th>}
              {!narrow && <th scope="col" className="border-b-2 border-rule pl-3">{tr("governance.devsecops.ultimo_rilascio")}</th>}
            </tr>
          </thead>
          <tbody>
            {assets.map((a) => (
              <tr key={a.id || "senza"} className="border-b border-hair">
                <td className="py-2 pr-3">
                  {a.key ? (
                    <Link to="/e/$key" params={{ key: a.key }} search={{ scheda: "produzione" }} className="font-medium no-underline">{a.name}</Link>
                  ) : (
                    <span className="font-medium text-muted">{a.name}</span>
                  )}
                  {a.dora && <span className="ml-2 border border-flag px-1 text-[10.5px] font-semibold text-flag" title={tr("governance.devsecops.supporta_una_funzione_essenziale_o")}>DORA</span>}
                </td>
                <td className="px-3 py-2 text-right font-mono tabular" title={tr("governance.devsecops.sulla_pipeline_su", { on_pipeline: a.on_pipeline, services: a.services })}>
                  {a.on_pipeline}<span className="text-muted">/{a.services}</span>
                </td>
                <td className={`px-3 py-2 text-right font-mono tabular ${a.critical > 0 ? "font-semibold text-danger" : "text-muted"}`}>{a.critical}</td>
                {!narrow && <td className={`px-3 py-2 text-right font-mono tabular ${a.high > 0 ? "" : "text-muted"}`}>{a.high}</td>}
                {!narrow && <td className={`px-3 py-2 text-right font-mono tabular ${a.blocking > 0 ? "font-semibold" : "text-muted"}`}>{a.blocking}</td>}
                {!narrow && <td className={`px-3 py-2 text-right font-mono tabular ${a.stale > 0 ? "font-semibold" : "text-muted"}`}>{a.stale}</td>}
                {!narrow && <td className={`px-3 py-2 text-right font-mono tabular ${a.unsupported_runtimes > 0 ? "font-semibold text-danger" : "text-muted"}`}>{a.unsupported_runtimes}</td>}
                {!narrow && <td className="py-2 pl-3 tabular text-muted">{a.last_release ? ago(a.last_release) : "—"}</td>}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}


function CriticalTable({ rows, narrow }: { rows: Overview["critical"]; narrow: boolean }) {
  return (
    <section className="flex min-w-0 flex-col gap-2" aria-label={tr("governance.devsecops.vulnerabilita_critiche_in_produzione")}>
      <SectionTitle aside={<span className="text-[12px] text-muted">{tr("governance.devsecops.prima_le_applicazioni_con_funzioni")}</span>}>{tr("governance.devsecops.vulnerabilita_critiche_in_produzione")}</SectionTitle>
      {rows.length === 0 && <p className="m-0 text-muted">{tr("governance.devsecops.nessuna_vulnerabilita_critica_nelle_versioni")}</p>}
      {rows.length > 0 && (
        <ul className="m-0 flex list-none flex-col p-0">
          {rows.map((v) => (
            <li key={v.id + v.service_id + (v.package ?? "")}
              className="grid grid-cols-1 gap-x-4 gap-y-0.5 border-b border-hair py-2 md:grid-cols-[170px_minmax(0,1fr)_minmax(0,1fr)_120px]">
              <span className="flex items-baseline gap-2">
                {v.url ? <a href={v.url} target="_blank" rel="noreferrer" className="font-mono text-[12.5px] font-semibold">{v.id}</a>
                  : <span className="font-mono text-[12.5px] font-semibold">{v.id}</span>}
                {v.supports_critical_function && <span className="border border-flag px-1 text-[10.5px] font-semibold text-flag">DORA</span>}
              </span>
              <span className="min-w-0">
                <span className="font-medium">{v.package}</span> <span className="font-mono text-[12px] tabular text-muted">{v.version}</span>
                <span className="text-[12px] text-muted"> · {v.layer === "image" ? tr("governance.devsecops.immagine") : tr("governance.requirements.applicazione_3")}</span>
                {v.fixed_in ? <span className="block text-[12px]">{tr("governance.devsecops.corretta_in")}{" "}<span className="font-mono">{v.fixed_in}</span></span>
                  : <span className="block text-[12px] text-muted">{tr("governance.devsecops.nessuna_correzione_disponibile")}</span>}
              </span>
              <span className="min-w-0">
                <span className="font-medium">{v.service}</span> <span className="font-mono text-[12px] text-muted">{v.prod_version}</span>
                {v.application_key && (
                  <span className="block text-[12px]">
                    <Link to="/e/$key" params={{ key: v.application_key }} search={{ scheda: "produzione" }} className="no-underline">{v.application}</Link>
                    {!narrow && <span className="ml-2"><Criticality value={v.criticality} /></span>}
                  </span>
                )}
              </span>
              <span className="text-[12px] text-muted tabular md:text-right">{tr("governance.devsecops.dal", { when: when(v.first_seen) })}</span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function LatestReleases({ rels, enterprise }: { rels: Release[]; enterprise: boolean }) {
  return (
    <section className="flex min-w-0 flex-col gap-2" aria-label={tr("governance.devsecops.ultimi_rilasci")}>
      <SectionTitle>{tr("governance.devsecops.ultimi_rilasci")}</SectionTitle>
      {rels.length === 0 && <p className="m-0 text-muted">{tr("governance.devsecops.nessun_rilascio_ricevuto_collega_una")}</p>}
      <ul className="m-0 flex list-none flex-col p-0">
        {rels.map((r) => (
          <li key={r.id} className="grid grid-cols-[minmax(0,1fr)_auto] gap-x-3 gap-y-0.5 border-b border-hair py-2 md:grid-cols-[minmax(0,1.3fr)_minmax(0,1fr)_minmax(0,1fr)_auto]">
            <span className="min-w-0">
              <span className="font-medium">{r.service}</span> <span className="font-mono text-[12.5px] tabular">{r.version}</span>
              <span className={`ml-2 text-[12px] ${r.production ? "font-semibold" : "text-muted"}`}>{r.environment}</span>
              {r.application && <span className="block text-[12px] text-muted">{r.application}</span>}
            </span>
            <span className="text-right text-[12px] text-muted tabular md:order-last">{ago(r.at)}</span>
            {/* Change e approvazione sono parole da azienda: senza demand né GRC si mostrano solo se il rilascio le dichiara. */}
            <span className="min-w-0 text-[12.5px]">{(enterprise || r.change || r.change_number) && <ChangeLink r={r} />}</span>
            <span className="min-w-0 text-[12.5px]">{r.approved_by ? tr("governance.devsecops.approvato_da", { approved_by: r.approved_by }) : enterprise && <span className="text-muted">{tr("governance.devsecops.approvazione_non_dichiarata")}</span>}</span>
          </li>
        ))}
      </ul>
    </section>
  );
}

// ---------------------------------------------------------------------
// /configurazione/devsecops

export function DevsecopsConfigPage() {
  const cfg = useQuery({ queryKey: ["dso-config"], queryFn: dsoApi.config });
  const c = cfg.data;
  return (
    <>
      <PageHeader path={crumbs("configuration", "devsecops")} title={tr("governance.devsecops.devsecops")} />
      <div className="flex max-w-5xl flex-col gap-9 px-4 pb-10 md:px-7">
        <p className="m-0 max-w-3xl text-muted">
          <Trans k="governance.devsecops.le_pipeline_inviano_servizi_controlli_rilasci" slots={{ profile: <Link to="/profilo">{tr("governance.devsecops.profilo")}</Link> }} />
        </p>
        <ErrorNote error={cfg.error} />
        {c && (
          <>
            <SettingsForm key={c.settings.updated_at ?? "default"} c={c} />
            <EnvironmentsConfig c={c} />
            <ServicesConfig c={c} />
            <ChangesConfig c={c} />
          </>
        )}
      </div>
    </>
  );
}

function SettingsForm({ c }: { c: Config }) {
  const qc = useQueryClient();
  const [days, setDays] = useState(String(c.settings.stale_days));
  const [field, setField] = useState(c.settings.change_field);
  const [rts, setRts] = useState<RuntimeEOL[]>(c.settings.runtimes);
  const save = useMutation({
    mutationFn: () =>
      dsoApi.saveSettings({
        production_envs: c.settings.production_envs, // si cambiano nella sezione «Ambienti di produzione»
        stale_days: Number(days),
        change_field: field,
        runtimes: rts.filter((r) => r.name || r.version || r.end_of_support),
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["dso-config"] });
      qc.invalidateQueries({ queryKey: ["dso-panel"] });
      qc.invalidateQueries({ queryKey: ["dso-overview"] });
    },
  });
  const set = (i: number, patch: Partial<RuntimeEOL>) => setRts((l) => l.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  const ro = !c.can_edit;
  return (
    <form className="flex flex-col gap-4" onSubmit={(e) => { e.preventDefault(); save.mutate(); }}>
      <SectionTitle>{tr("governance.devsecops.impostazioni")}</SectionTitle>
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
        <label className="flex flex-col gap-1">
          <span className="label">{tr("governance.devsecops.non_rilasciato_dopo_giorni")}</span>
          <input className="field font-mono" type="number" min={1} max={3650} disabled={ro} value={days} onChange={(e) => setDays(e.target.value)} />
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{tr("governance.devsecops.campo_della_richiesta_con_il")}</span>
          <Select className="field" disabled={ro} value={field} onChange={(e) => setField(e.target.value)}>
            <option value="">{tr("governance.devsecops.solo_la_chiave_dem_12")}</option>
            {c.request_fields.map((f) => <option key={f.key} value={f.key}>{f.name}</option>)}
          </Select>
          <span className="text-[12px] text-muted">{tr("governance.devsecops.il_numero_di_change_si")}</span>
        </label>
      </div>
      <div className="flex flex-col gap-1.5">
        <span className="label">{tr("governance.devsecops.fine_supporto_dei_runtime")}</span>
        <span className="text-[12px] text-muted">{tr("governance.devsecops.si_confrontano_con_i_componenti")}</span>
        <div className="flex max-w-xl flex-col gap-1.5">
          {rts.map((r, i) => (
            <div key={i} className="grid grid-cols-[minmax(0,1fr)_80px_150px_auto] items-center gap-2">
              <input aria-label={tr("governance.devsecops.runtime")} className="field py-1 font-mono" disabled={ro} value={r.name} onChange={(e) => set(i, { name: e.target.value })} />
              <input aria-label={tr("governance.compliance.versione")} className="field py-1 font-mono" disabled={ro} value={r.version} onChange={(e) => set(i, { version: e.target.value })} />
              <input aria-label={tr("governance.devsecops.fine_supporto_2")} type="date" className="field py-1" disabled={ro} value={r.end_of_support} onChange={(e) => set(i, { end_of_support: e.target.value })} />
              {!ro && <button type="button" className="text-[12px] text-muted underline hover:text-danger" onClick={() => setRts((l) => l.filter((_, j) => j !== i))}>{tr("governance.compliance.togli_2")}</button>}
            </div>
          ))}
        </div>
        {!ro && (
          <button type="button" className="self-start text-[12.5px] text-accent underline" onClick={() => setRts((l) => [...l, { name: "", version: "", end_of_support: "" }])}>{tr("governance.devsecops.aggiungi_un_runtime")}</button>
        )}
      </div>
      {!ro && (
        <span className="flex items-center gap-3">
          <button className="btn btn-primary" disabled={save.isPending}>{tr("governance.compliancesections.salva")}</button>
          {save.isSuccess && <span className="text-[12.5px] text-ok">{tr("governance.devsecops.salvato")}</span>}
          {c.settings.default && <span className="text-[12.5px] text-muted">{tr("governance.devsecops.valori_predefiniti")}</span>}
        </span>
      )}
      <ErrorNote error={save.error} />
    </form>
  );
}

function EnvironmentsConfig({ c }: { c: Config }) {
  const qc = useQueryClient();
  const upd = useMutation({
    mutationFn: ({ id, body }: { id: string; body: { production?: boolean; ordinal?: number } }) => dsoApi.updateEnv(id, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["dso-config"] }),
  });
  // Un solo posto per dire quali ambienti sono di produzione: la tabella per quelli già comparsi e,
  // sotto, i nomi attesi per quelli che compariranno (prima erano un campo di testo a parte).
  const [name, setName] = useState("");
  const known = new Set(c.environments.map((e) => e.name.toLowerCase()));
  const expected = c.settings.production_envs.filter((n) => !known.has(n.toLowerCase()));
  const saveNames = useMutation({
    mutationFn: (names: string[]) =>
      dsoApi.saveSettings({ production_envs: names, stale_days: c.settings.stale_days, change_field: c.settings.change_field, runtimes: c.settings.runtimes }),
    onSuccess: () => {
      setName("");
      qc.invalidateQueries({ queryKey: ["dso-config"] });
      qc.invalidateQueries({ queryKey: ["dso-panel"] });
      qc.invalidateQueries({ queryKey: ["dso-overview"] });
    },
  });
  const addName = () => {
    const n = name.trim();
    if (n && ![...known, ...c.settings.production_envs.map((x) => x.toLowerCase())].includes(n.toLowerCase())) saveNames.mutate([...c.settings.production_envs, n]);
    else setName("");
  };
  return (
    <section className="flex flex-col gap-2" aria-label={tr("governance.devsecops.ambienti_di_produzione")}>
      <SectionTitle>{tr("governance.devsecops.ambienti_di_produzione")}</SectionTitle>
      <p className="m-0 max-w-3xl text-[12.5px] text-muted">{tr("governance.devsecops.spunta_gli_ambienti_che_sono")}</p>
      {c.environments.length === 0 && <p className="m-0 text-muted">{tr("governance.devsecops.nessun_ambiente_ancora")}</p>}
      {c.environments.length > 0 && (
        <table className="w-full border-collapse">
          <thead>
            <tr className="label h-9 text-left">
              <th scope="col" className="border-b-2 border-rule pr-3">{tr("governance.devsecops.ambiente")}</th>
              <th scope="col" className="border-b-2 border-rule px-3">{tr("governance.devsecops.produzione")}</th>
              <th scope="col" className="border-b-2 border-rule pl-3">{tr("governance.devsecops.ordine")}</th>
            </tr>
          </thead>
          <tbody>
            {c.environments.map((e) => (
              <tr key={e.id} className="border-b border-hair">
                <td className="py-1.5 pr-3 font-mono">{e.name}</td>
                <td className="px-3 py-1.5">
                  <input type="checkbox" aria-label={tr("governance.devsecops.e_di_produzione", { name: e.name })} disabled={!c.can_edit || upd.isPending} checked={e.production}
                    onChange={(ev) => upd.mutate({ id: e.id, body: { production: ev.target.checked } })} />
                </td>
                <td className="py-1.5 pl-3">
                  <input type="number" aria-label={tr("governance.devsecops.ordine_di", { name: e.name })} className="field w-24 py-1 font-mono" min={0} max={1000} disabled={!c.can_edit}
                    defaultValue={e.ordinal} onBlur={(ev) => Number(ev.target.value) !== e.ordinal && upd.mutate({ id: e.id, body: { ordinal: Number(ev.target.value) } })} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {(expected.length > 0 || c.can_edit) && (
        <div className="flex flex-col gap-1.5 pt-2">
          <span className="label">{tr("governance.devsecops.nomi_di_produzione_attesi")}</span>
          <span className="text-[12px] text-muted">{tr("governance.devsecops.ambienti_che_non_sono_ancora")}</span>
          <ul className="m-0 flex list-none flex-wrap gap-1.5 p-0">
            {expected.map((n) => (
              <li key={n} className="inline-flex items-center gap-1 border border-rule px-1.5 py-0.5 font-mono text-[12.5px]">
                {n}
                {c.can_edit && (
                  <button
                    type="button"
                    className="text-muted hover:text-danger"
                    aria-label={tr("governance.devsecops.togli", { n })}
                    disabled={saveNames.isPending}
                    onClick={() => saveNames.mutate(c.settings.production_envs.filter((x) => x !== n))}
                  >
                    ×
                  </button>
                )}
              </li>
            ))}
            {expected.length === 0 && <li className="text-[12.5px] text-muted">{tr("governance.devsecops.nessuno")}</li>}
          </ul>
          {c.can_edit && (
            <form
              className="flex max-w-sm gap-2"
              onSubmit={(e) => {
                e.preventDefault();
                addName();
              }}
            >
              <input aria-label={tr("governance.devsecops.nome_di_un_ambiente_di")} className="field py-1 font-mono" placeholder={tr("governance.devsecops.es_prod_eu")} value={name} onChange={(e) => setName(e.target.value)} />
              <button className="btn" disabled={!name.trim() || saveNames.isPending}>{tr("governance.compliance.aggiungi")}</button>
            </form>
          )}
        </div>
      )}
      <ErrorNote error={upd.error ?? saveNames.error} />
    </section>
  );
}

function ServicesConfig({ c }: { c: Config }) {
  const qc = useQueryClient();
  const map = useMutation({
    mutationFn: ({ id, app }: { id: string; app: string }) => dsoApi.mapService(id, app),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["dso-config"] });
      qc.invalidateQueries({ queryKey: ["dso-panel"] });
    },
  });
  const unmapped = c.services.filter((s) => !s.application_id).length;
  return (
    <section className="flex min-w-0 flex-col gap-2">
      <SectionTitle aside={<span className="text-[12px] text-muted">{tr("governance.devsecops.da_associare", { unmapped })}</span>}>{tr("governance.devsecops.servizi_e_applicazioni")}</SectionTitle>
      <p className="m-0 max-w-3xl text-[12.5px] text-muted">{tr("governance.devsecops.la_pipeline_dichiara_l_applicazione")}</p>
      {c.services.length === 0 && <p className="m-0 text-muted">{tr("governance.devsecops.nessun_servizio_ricevuto")}</p>}
      <ul className="m-0 flex list-none flex-col p-0">
        {c.services.map((s) => (
          <li key={s.id} className="grid grid-cols-1 items-center gap-x-4 gap-y-1 border-b border-hair py-2 md:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_280px]">
            <span className="min-w-0">
              <span className="font-medium">{s.name}</span>
              {s.repository && <span className="block break-words font-mono text-[11.5px] text-muted">{s.repository}</span>}
            </span>
            <span className="text-[12.5px] text-muted">
              {s.declared_application ? <>{tr("governance.devsecops.dichiarata")}{" "}<span className="font-mono text-fg">{s.declared_application}</span></> : tr("governance.devsecops.nessuna_dichiarata")}
              {s.mapping_source === "manual" && tr("governance.devsecops.associata_a_mano")}
              {!s.first_seen_at && tr("governance.devsecops.non_ancora_sulla_pipeline_3")}
            </span>
            <Select aria-label={tr("governance.devsecops.applicazione_di", { name: s.name })} className={`field py-1 ${s.application_id ? "" : "border-danger"}`} disabled={!c.can_edit || map.isPending}
              value={s.application_id ?? ""} onChange={(e) => map.mutate({ id: s.id, app: e.target.value })}>
              <option value="">{tr("governance.devsecops.nessuna_applicazione")}</option>
              {c.applications.map((a) => <option key={a.id} value={a.id}>{a.name}{a.code ? ` (${a.code})` : ""}</option>)}
            </Select>
          </li>
        ))}
      </ul>
      <ErrorNote error={map.error} />
    </section>
  );
}

function ChangesConfig({ c }: { c: Config }) {
  if (c.changes.length === 0) return null;
  return (
    <section className="flex min-w-0 flex-col gap-2">
      <SectionTitle>{tr("governance.devsecops.change_recenti")}</SectionTitle>
      <p className="m-0 max-w-3xl text-[12.5px] text-muted">{tr("governance.devsecops.un_numero_provvisorio_sostituito_da")}</p>
      <ul className="m-0 flex list-none flex-col p-0">
        {c.changes.map((ch) => <ChangeRow key={ch.id} ch={ch} canEdit={c.can_edit} />)}
      </ul>
    </section>
  );
}

function ChangeRow({ ch, canEdit }: { ch: Config["changes"][number]; canEdit: boolean }) {
  const qc = useQueryClient();
  const [req, setReq] = useState(ch.request_key ?? "");
  const [num, setNum] = useState(ch.number);
  const save = useMutation({
    mutationFn: () => dsoApi.updateChange(ch.id, { request: req.trim(), ...(num.trim() !== ch.number ? { number: num.trim() } : {}) }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["dso-config"] });
      qc.invalidateQueries({ queryKey: ["dso-panel"] });
    },
  });
  const dirty = req.trim() !== (ch.request_key ?? "") || num.trim().toUpperCase() !== ch.number;
  return (
    <li className="flex flex-col gap-1 border-b border-hair py-2">
      <form className="grid grid-cols-1 items-center gap-2 md:grid-cols-[180px_minmax(0,1fr)_160px_auto]" onSubmit={(e) => { e.preventDefault(); save.mutate(); }}>
        <input aria-label={tr("governance.devsecops.numero_del_change")} className="field py-1 font-mono" disabled={!canEdit} value={num} onChange={(e) => setNum(e.target.value)} />
        <span className="min-w-0 text-[12.5px] text-muted">
          {ch.aliases.length > 0 && <span className="font-mono">{tr("governance.devsecops.gia")}{" "}{ch.aliases.join(", ")} · </span>}
          {ch.request_key ? (
            <>
              <Link to="/e/$key" params={{ key: ch.request_key }}>{ch.request_key}</Link> {ch.request_title}
              {ch.linked_by && <span> · {ch.linked_by === "key" ? tr("governance.devsecops.dalla_chiave") : ch.linked_by === "field" ? tr("governance.devsecops.dal_campo") : tr("governance.devsecops.a_mano")}</span>}
            </>
          ) : (
            ch.title || tr("governance.devsecops.nessuna_richiesta")
          )}
        </span>
        <input aria-label={tr("governance.devsecops.richiesta")} className="field py-1 font-mono" placeholder="DEM-12" disabled={!canEdit} value={req} onChange={(e) => setReq(e.target.value)} />
        {canEdit && <button className="btn py-1" disabled={!dirty || save.isPending}>{tr("governance.compliancesections.salva")}</button>}
      </form>
      <ErrorNote error={save.error} />
    </li>
  );
}

