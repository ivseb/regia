// Vulnerabilità governate (DevSecOps, fase 2; replica/devsecops.md §3.2): cruscotto con
// anzianità, scadute e in scadenza per team e applicazione, andamento, elenco filtrabile con
// azioni in blocco (prendi in carico, assegna, chiedi un'eccezione) e registro delle eccezioni.
// I dati vengono dai report delle pipeline: Regia non esegue scansioni.
import { Select } from "../select";
import { useEffect, useMemo, useState, type ReactNode } from "react";
import { Link, useSearch } from "@tanstack/react-router";
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  SEVERITIES,
  SEV_NAME,
  STAGE_NAME,
  vulnApi,
  type Aging,
  type Dashboard,
  type Detail,
  type Exception,
  type Filters,
  type Group,
  type Item,
  type TrendPoint,
} from "../vulnApi";
import { PageHeader } from "../shell";
import { ModuleChip } from "../moduleIcon";
import { Criticality, ErrorNote, SectionTitle, ago } from "../ui";
import { useNarrow } from "./timeline";
import { Dialog } from "./work-ui";
import { ProblemActions } from "./ProblemActions";
import { tr } from "../i18n/core";
import { loc } from "../i18n/format";
import { crumbs } from "../crumbs";

const SEV_MARK: Record<string, string> = { critical: "bg-danger", high: "bg-fg", medium: "bg-muted", low: "bg-hair", info: "bg-hair" };

function day(iso: string | null | undefined): string {
  return iso ? new Date(iso).toLocaleDateString(loc(), { day: "2-digit", month: "2-digit", year: "numeric" }) : "—";
}

function num(n: number | null | undefined, digits = 0): string {
  return n == null ? "—" : n.toLocaleString(loc(), { maximumFractionDigits: digits });
}

function SevBadge({ sev }: { sev: string }) {
  return (
    <span className="inline-flex items-center gap-1.5 whitespace-nowrap font-semibold">
      <span aria-hidden="true" className={`inline-block size-2.5 ${SEV_MARK[sev] ?? "bg-hair"}`} />
      {SEV_NAME[sev] ?? sev}
    </span>
  );
}

/** Scadenza in parole: scaduta da N giorni (rosso), tra N giorni (arancio se vicina). */
function Due({ it, soon }: { it: Item; soon: number }) {
  if (it.stage === "exception") return <span className="text-muted">{tr("governance.vulnerabilities.eccezione_fino_al", { day: day(it.exception_until) })}</span>;
  if (["resolved", "closed", "false_positive"].includes(it.stage)) return <span className="text-muted">{it.closed_at ? tr("governance.vulnerabilities.chiusa_il", { day: day(it.closed_at) }) : tr("governance.vulnerabilities.chiusa")}</span>;
  if (it.due_at == null || it.days_left == null) return <span className="text-muted">{tr("governance.vulnerabilities.nessuno_sla")}</span>;
  const d = it.days_left;
  if (it.overdue) {
    return <span className="font-semibold text-danger tabular">{tr("governance.vulnerabilities.scaduta_da_giorni", { count: -d })}</span>;
  }
  return (
    <span className={`tabular ${d <= soon ? "font-semibold text-flag" : "text-muted"}`}>
      {d === 0 ? tr("governance.vulnerabilities.scade_oggi") : tr("governance.vulnerabilities.tra_giorni", { count: d })}
      <span className="block text-[11.5px] font-normal text-muted">{tr("governance.vulnerabilities.il", { day: day(it.due_at) })}</span>
    </span>
  );
}

// ---------------------------------------------------------------------
// Numeri in alto

function Kpi({ label, value, tone, note, onClick, active, className = "", i }: { i?: number; label: string; value: ReactNode; tone?: string; note?: ReactNode; onClick?: () => void; active?: boolean; className?: string }) {
  const body = (
    <>
      <span aria-hidden="true" className="absolute -left-px -right-px -top-px h-1 bg-mc" />
      <span className="label flex items-center gap-2 text-muted"><ModuleChip module="devsecops" size="sm" />{label}</span>
      <span className={`font-mono text-[30px] leading-none font-semibold tabular ${tone ?? "text-fg"}`}>{value}</span>
      {note && <span className="text-[12px] text-muted">{note}</span>}
    </>
  );
  const tile = "entra relative flex min-w-0 flex-col gap-1.5 border border-hair px-3.5 pb-3 pt-3 m-devsecops";
  const st = { background: "linear-gradient(color-mix(in srgb, var(--mc) 6%, var(--bg)), var(--bg))", animationDelay: `${(i ?? 0) * 60}ms` };
  if (!onClick) return <div className={`${tile} ${className}`} style={st}>{body}</div>;
  return (
    <button type="button" onClick={onClick} aria-pressed={active}
      style={st} className={`${tile} cursor-pointer items-start text-left text-fg ${active ? "outline outline-2 -outline-offset-2 outline-accent" : ""} ${className}`}>
      {body}
    </button>
  );
}

// ---------------------------------------------------------------------
// Anzianità e andamento

const buckets = () => [tr("governance.vulnerabilities.fino_a_7_giorni"), "8–30", "31–90", tr("governance.vulnerabilities.oltre_90")];
const BUCKET_TONE = ["bg-fg/20", "bg-fg/40", "bg-fg/65", "bg-fg"];

function AgingChart({ rows }: { rows: Aging[] }) {
  const max = Math.max(1, ...rows.map((r) => r.total));
  return (
    <section className="flex min-w-0 flex-col gap-2" aria-label={tr("governance.vulnerabilities.anzianita_delle_vulnerabilita_da_correggere")}>
      <SectionTitle>{tr("governance.vulnerabilities.anzianita")}</SectionTitle>
      <div className="flex flex-col gap-2.5">
        {rows.map((r) => (
          <div key={r.severity} className="grid grid-cols-[84px_minmax(0,1fr)_auto] items-center gap-3">
            <SevBadge sev={r.severity} />
            <div className="flex h-4 w-full bg-hair/50" role="img"
              aria-label={`${SEV_NAME[r.severity]}: ${buckets().map((b, i) => `${r.buckets[i]} ${b}`).join(", ")}`}>
              {r.buckets.map((n, i) => (n > 0 ? <span key={i} title={`${n} · ${buckets()[i]}`} className={`h-full ${BUCKET_TONE[i]}`} style={{ width: `${(n / max) * 100}%` }} /> : null))}
            </div>
            <span className="min-w-[70px] text-right text-[12.5px] tabular">
              <span className="font-mono font-semibold">{r.total}</span>
              {r.overdue > 0 && <span className="text-danger">{tr("governance.vulnerabilities.scadute", { overdue: r.overdue })}</span>}
            </span>
          </div>
        ))}
      </div>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-[11.5px] text-muted">
        <span>{tr("governance.vulnerabilities.dalla_prima_rilevazione")}</span>
        {buckets().map((b, i) => (
          <span key={b} className="inline-flex items-center gap-1.5">
            <span aria-hidden="true" className={`inline-block size-2.5 ${BUCKET_TONE[i]}`} />
            {b}
          </span>
        ))}
      </div>
    </section>
  );
}

function TrendChart({ points }: { points: TrendPoint[] }) {
  const W = 480, H = 130, L = 30, B = 20, T = 8, R = 6;
  const max = Math.max(4, ...points.map((p) => p.open));
  const x = (i: number) => L + (i * (W - L - R)) / Math.max(1, points.length - 1);
  const y = (v: number) => T + (1 - v / max) * (H - T - B);
  const line = (f: (p: TrendPoint) => number) => points.map((p, i) => `${i === 0 ? "M" : "L"}${x(i).toFixed(1)} ${y(f(p)).toFixed(1)}`).join(" ");
  const last = points[points.length - 1];
  return (
    <section className="flex min-w-0 flex-col gap-2" aria-label={tr("governance.vulnerabilities.andamento_delle_ultime_dodici_settimane")}>
      <SectionTitle aside={last && <span className="text-[12px] text-muted tabular">{tr("governance.vulnerabilities.ultima_settimana_nuove_chiuse", { opened: last.opened, closed: last.closed })}</span>}>{tr("governance.vulnerabilities.andamento_12_settimane")}</SectionTitle>
      {points.length > 1 ? (
        <svg viewBox={`0 0 ${W} ${H}`} className="h-auto w-full" role="img"
          aria-label={tr("governance.vulnerabilities.aperte_da_a_scadute_da", { p: points[0].open, open: last.open, p2: points[0].overdue, overdue: last.overdue })}>
          {[0, 0.5, 1].map((f) => (
            <g key={f}>
              <line x1={L} x2={W - R} y1={y(max * f)} y2={y(max * f)} className="stroke-hair" strokeWidth="1" />
              <text x={L - 5} y={y(max * f) + 4} textAnchor="end" className="fill-muted font-mono" fontSize="10">{Math.round(max * f)}</text>
            </g>
          ))}
          <path d={line((p) => p.open)} fill="none" className="stroke-fg" strokeWidth="2" />
          <path d={line((p) => p.overdue)} fill="none" className="stroke-danger" strokeWidth="2" strokeDasharray="5 3" />
          {last && <circle cx={x(points.length - 1)} cy={y(last.open)} r="3.5" className="fill-fg" />}
          {last && <circle cx={x(points.length - 1)} cy={y(last.overdue)} r="3.5" className="fill-danger" />}
          <text x={L} y={H - 4} className="fill-muted" fontSize="10">{new Date(points[0].at).toLocaleDateString(loc(), { day: "2-digit", month: "short" })}</text>
          <text x={W - R} y={H - 4} textAnchor="end" className="fill-muted" fontSize="10">{new Date(last.at).toLocaleDateString(loc(), { day: "2-digit", month: "short" })}</text>
        </svg>
      ) : (
        <p className="m-0 text-muted">{tr("governance.vulnerabilities.l_andamento_comparira_con_i")}</p>
      )}
      <div className="flex flex-wrap gap-x-4 gap-y-1 text-[11.5px] text-muted">
        <span className="inline-flex items-center gap-1.5"><span aria-hidden="true" className="inline-block h-0.5 w-5 bg-fg" />{tr("governance.vulnerabilities.da_correggere")}</span>
        <span className="inline-flex items-center gap-1.5"><span aria-hidden="true" className="inline-block h-0.5 w-5 border-t-2 border-dashed border-danger" />{tr("governance.vulnerabilities.oltre_la_scadenza")}</span>
      </div>
    </section>
  );
}

// ---------------------------------------------------------------------
// Team e applicazioni

function Count({ n, tone }: { n: number; tone?: string }) {
  return <span className={`font-mono tabular ${n > 0 ? tone ?? "" : "text-muted"}`}>{n}</span>;
}

function GroupTable({ title, rows, kind, onPick, active, narrow }: { title: string; rows: Group[]; kind: "team" | "app"; onPick: (g: Group) => void; active?: string; narrow: boolean }) {
  return (
    <section className="flex min-w-0 flex-col gap-2" aria-label={title}>
      <SectionTitle aside={<span className="text-[12px] text-muted">{tr("governance.vulnerabilities.tocca_una_riga_per_filtrare")}</span>}>{title}</SectionTitle>
      {rows.length === 0 && <p className="m-0 text-muted">{tr("governance.vulnerabilities.nessuna_vulnerabilita_da_correggere")}</p>}
      {rows.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-[13px]">
            <thead>
              <tr className="label h-8 text-left text-muted">
                <th scope="col" className="border-b-2 border-rule pr-3 font-semibold">{kind === "team" ? tr("governance.requirements.team") : tr("governance.requirements.applicazione")}</th>
                <th scope="col" className="border-b-2 border-rule px-2 text-right font-semibold">{tr("governance.vulnerabilities.aperte")}</th>
                <th scope="col" className="border-b-2 border-rule px-2 text-right font-semibold">{tr("governance.vulnerabilities.scadute_2")}</th>
                <th scope="col" className="border-b-2 border-rule px-2 text-right font-semibold">{tr("governance.vulnerabilities.in_scad")}</th>
                {!narrow && <th scope="col" className="border-b-2 border-rule px-2 text-right font-semibold" title={tr("governance.vulnerabilities.critiche_e_alte")}>C+A</th>}
                {!narrow && kind === "app" && <th scope="col" className="border-b-2 border-rule pl-2 text-right font-semibold">{tr("governance.vulnerabilities.ecc")}</th>}
              </tr>
            </thead>
            <tbody>
              {rows.map((g) => (
                <tr key={g.id || g.name} className={`cursor-pointer border-b border-hair hover:bg-sel ${active && active === (g.id || "none") ? "bg-sel" : ""}`} onClick={() => onPick(g)}>
                  <td className="py-2 pr-3">
                    <button type="button" className="cursor-pointer border-0 bg-transparent p-0 text-left font-medium text-fg" onClick={(e) => { e.stopPropagation(); onPick(g); }}>
                      {g.name}
                    </button>
                    {kind === "app" && (
                      <span className="block text-[11.5px] text-muted">
                        <span className="font-mono">{g.key}</span>
                        {g.team ? ` · ${g.team}` : ""}
                        {g.criticality ? <> · <Criticality value={g.criticality} critical={g.dora} /></> : ""}
                      </span>
                    )}
                    {g.oldest_overdue_days > 0 && <span className="block text-[11.5px] text-danger">{tr("governance.vulnerabilities.la_piu_vecchia_scaduta_da", { oldest_overdue_days: g.oldest_overdue_days })}</span>}
                  </td>
                  <td className="px-2 py-2 text-right"><Count n={g.open} /></td>
                  <td className="px-2 py-2 text-right"><Count n={g.overdue} tone="font-semibold text-danger" /></td>
                  <td className="px-2 py-2 text-right"><Count n={g.soon} tone="font-semibold text-flag" /></td>
                  {!narrow && <td className="px-2 py-2 text-right"><Count n={g.critical} /></td>}
                  {!narrow && kind === "app" && <td className="py-2 pl-2 text-right"><Count n={g.exceptions} /></td>}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

// ---------------------------------------------------------------------
// Dettaglio di una vulnerabilità

const EVENT_TEXT: Record<string, string> = {
  get take() { return tr("governance.vulnerabilities.presa_in_carico"); },
  get start() { return tr("governance.vulnerabilities.messa_in_correzione"); },
  get resolve() { return tr("governance.vulnerabilities.segnata_come_risolta"); },
  get reopen() { return tr("governance.vulnerabilities.riaperta"); },
  get assign() { return tr("governance.vulnerabilities.assegnata"); },
  get false_positive() { return tr("governance.vulnerabilities.segnata_come_falso_positivo"); },
  get closed_auto() { return tr("governance.vulnerabilities.chiusa_non_compare_piu_nei"); },
  get reopened_auto() { return tr("governance.vulnerabilities.riaperta_e_ricomparsa_nei_report"); },
  get escalation() { return tr("governance.vulnerabilities.avviso_di_escalation"); },
  get exception_requested() { return tr("governance.vulnerabilities.eccezione_richiesta"); },
  get exception_approved() { return tr("governance.vulnerabilities.eccezione_approvata"); },
  get exception_rejected() { return tr("governance.vulnerabilities.eccezione_non_approvata"); },
  get exception_expired() { return tr("governance.vulnerabilities.eccezione_scaduta"); },
  get exception_revoked() { return tr("governance.vulnerabilities.eccezione_revocata"); },
};

function DetailPanel({ id }: { id: string }) {
  const d = useQuery({ queryKey: ["vuln-item", id], queryFn: () => vulnApi.item(id) });
  const x: Detail | undefined = d.data;
  return (
    <div className="flex flex-col gap-3 border-t border-hair bg-sel/40 px-3 py-3 md:px-4">
      <ErrorNote error={d.error} />
      {!x && !d.error && <p className="m-0 text-muted">{tr("governance.vulnerabilities.carico")}</p>}
      {x && (
        <div className="grid grid-cols-1 gap-x-8 gap-y-4 md:grid-cols-2">
          <div className="flex min-w-0 flex-col gap-2 text-[13px]">
            <div className="flex flex-wrap gap-x-5 gap-y-1">
              <span><span className="label text-muted">{tr("governance.devsecops.servizio")}</span>{x.service ?? "—"}</span>
              <span><span className="label text-muted">{tr("governance.vulnerabilities.strato")}</span>{x.layer === "image" ? "immagine" : x.layer === "app" ? "applicazione" : "—"}</span>
              <span><span className="label text-muted">{tr("governance.vulnerabilities.prima_rilevazione")}</span>{day(x.first_seen)}</span>
              {x.sla_days != null && <span><span className="label text-muted">SLA </span>{tr("governance.vulnerabilities.sla_giorni", { count: x.sla_days })}</span>}
            </div>
            {x.aliases.length > 0 && <span className="text-[12.5px] text-muted">{tr("governance.vulnerabilities.anche")}{" "}<span className="font-mono">{x.aliases.join(", ")}</span></span>}
            {x.application_key && (
              <Link to="/e/$key" params={{ key: x.application_key }} search={{ scheda: "produzione" }} className="self-start text-[12.5px]">{tr("governance.vulnerabilities.apri_in_produzione", { application: x.application ?? "" })}</Link>
            )}
            {x.exceptions.length > 0 && (
              <div className="flex flex-col gap-1">
                <span className="label text-muted">{tr("governance.vulnerabilities.eccezioni")}</span>
                {x.exceptions.map((e) => (
                  <span key={e.id} className="text-[12.5px]">
                    <span className="font-mono font-semibold">{e.key}</span>{" "}{tr("governance.vulnerabilities.fino_al", { p: EXC_STATUS[e.status], day: day(e.until) })}{e.risk_key && <>{" "}{tr("governance.vulnerabilities.rischio")}{" "}<Link to="/e/$key" params={{ key: e.risk_key }}>{e.risk_key}</Link></>}
                    <span className="block text-muted">{e.reason}</span>
                  </span>
                ))}
              </div>
            )}
            {x.others.length > 0 && (
              <div className="flex flex-col gap-1">
                <span className="label text-muted">{tr("governance.vulnerabilities.stessa_vulnerabilita_altrove")}</span>
                {x.others.map((o) => (
                  <span key={o.id} className="text-[12.5px]">{o.application ?? o.service ?? "—"} <span className="text-muted">· {o.package} · {STAGE_NAME[o.stage]}</span></span>
                ))}
              </div>
            )}
            <ProblemActions kind="finding" refKey={id} actions={["work_item", "exception"]} />
          </div>
          <div className="flex min-w-0 flex-col gap-1.5">
            <span className="label text-muted">{tr("governance.vulnerabilities.storia")}</span>
            {x.events.length === 0 && <span className="text-[12.5px] text-muted">{tr("governance.vulnerabilities.nessun_passaggio_e_aperta_dalla")}</span>}
            <ol className="m-0 flex list-none flex-col gap-1 p-0">
              {x.events.map((e, i) => (
                <li key={i} className="grid grid-cols-[88px_minmax(0,1fr)] gap-2 text-[12.5px]">
                  <span className="text-muted tabular">{day(e.at)}</span>
                  <span>
                    {EVENT_TEXT[e.kind] ?? e.kind}
                    {e.actor && <span className="text-muted"> · {e.actor}</span>}
                    {e.note && <span className="block text-muted">{e.note}</span>}
                  </span>
                </li>
              ))}
            </ol>
          </div>
        </div>
      )}
    </div>
  );
}

const EXC_STATUS: Record<string, string> = { get pending() { return tr("governance.vulnerabilities.in_approvazione"); }, get approved() { return tr("governance.vulnerabilities.in_vigore"); }, get rejected() { return tr("governance.vulnerabilities.non_approvata"); }, get expired() { return tr("governance.vulnerabilities.scaduta"); }, get revoked() { return tr("governance.vulnerabilities.revocata"); } };

// ---------------------------------------------------------------------
// Elenco

function Row({ it, soon, selected, onSelect, canAct, narrow, open, onOpen }: { it: Item; soon: number; selected: boolean; onSelect: () => void; canAct: boolean; narrow: boolean; open: boolean; onOpen: () => void }) {
  return (
    <li className={`border-b border-hair ${selected ? "bg-sel" : ""}`}>
      <div className="grid grid-cols-[22px_minmax(0,1fr)] items-start gap-x-2 gap-y-1 px-1 py-2.5 md:grid-cols-[22px_minmax(0,1.2fr)_minmax(0,1.3fr)_minmax(0,1fr)_150px] md:gap-x-4">
        {canAct ? (
          <input type="checkbox" aria-label={tr("governance.vulnerabilities.seleziona", { cve: it.cve })} className="mt-1 size-4" checked={selected} onChange={onSelect} />
        ) : (
          <span />
        )}
        <div className="min-w-0">
          <button type="button" aria-expanded={open} onClick={onOpen} className="flex cursor-pointer flex-wrap items-baseline gap-x-2 border-0 bg-transparent p-0 text-left text-fg">
            <span className="font-mono text-[13px] font-semibold text-accent">{it.cve}</span>
            <SevBadge sev={it.severity} />
          </button>
          <span className="block min-w-0 text-[12.5px]">
            <span className="font-medium">{it.package ?? "—"}</span> <span className="font-mono tabular text-muted">{it.version}</span>
            {it.fixed_in ? <span className="block text-ok">{tr("governance.vulnerabilities.correggi_con")}{" "}<span className="font-mono">{it.fixed_in}</span></span> : <span className="block text-muted">{tr("governance.devsecops.nessuna_correzione_disponibile")}</span>}
          </span>
        </div>
        <div className={`min-w-0 text-[12.5px] ${narrow ? "col-start-2" : ""}`}>
          {it.application_key ? (
            <>
              <Link to="/e/$key" params={{ key: it.application_key }} search={{ scheda: "produzione" }} className="font-medium no-underline">{it.application}</Link>
              <span className="block text-muted">
                <Criticality value={it.criticality} critical={it.dora} /> · {it.service}
              </span>
            </>
          ) : (
            <span className="text-muted">{it.service ?? "—"}{" "}{tr("governance.vulnerabilities.senza_applicazione")}</span>
          )}
        </div>
        <div className={`min-w-0 text-[12.5px] ${narrow ? "col-start-2" : ""}`}>
          <span className="font-medium">{STAGE_NAME[it.stage] ?? it.stage}</span>
          <span className="block text-muted">
            {it.team ?? tr("governance.vulnerabilities.senza_team")}{it.team_manual ? tr("governance.vulnerabilities.a_mano") : ""}
            {it.assignee ? ` · ${it.assignee}` : ""}
          </span>
          {it.exception && it.stage !== "exception" && <span className="block text-flag">{tr("governance.vulnerabilities.eccezione_in_approvazione", { exception: it.exception })}</span>}
        </div>
        <div className={`text-[12.5px] md:text-right ${narrow ? "col-start-2" : ""}`}><Due it={it} soon={soon} /></div>
      </div>
      {open && <DetailPanel id={it.id} />}
    </li>
  );
}

type StageTab = "open" | "exception" | "closed";
const STAGE_TABS: { key: StageTab; label: string }[] = [
  { key: "open", get label() { return tr("governance.vulnerabilities.da_correggere_2"); } },
  { key: "exception", get label() { return tr("governance.vulnerabilities.in_eccezione"); } },
  { key: "closed", get label() { return tr("governance.vulnerabilities.chiuse_e_risolte"); } },
];

// ---------------------------------------------------------------------
// Azioni in blocco

type Panel = null | "assign" | "exception" | "false_positive" | "resolve";

function BulkBar({ ids, canAct, onDone, onClear }: { ids: string[]; canAct: boolean; onDone: (msg: string) => void; onClear: () => void }) {
  const qc = useQueryClient();
  const [panel, setPanel] = useState<Panel>(null);
  const done = (r: { changed: number; skipped: number; notes: string[] }) => {
    qc.invalidateQueries({ queryKey: ["vuln"] });
    onDone(tr("governance.vulnerabilities.n_cambiate", { count: r.changed, extra: r.skipped ? tr("governance.vulnerabilities.no", { skipped: r.skipped, notes: r.notes.join(" ") }) : "" }));
  };
  const act = useMutation({ mutationFn: (action: string) => vulnApi.bulk({ ids, action }), onSuccess: done });
  if (ids.length === 0 || !canAct) return null;
  return (
    <div className="sticky bottom-0 z-20 flex flex-col gap-2 border-t-2 border-rule bg-bg px-3 py-3 md:px-4" role="region" aria-label={tr("governance.vulnerabilities.azioni_sulle_vulnerabilita_selezionate")}>
      <div className="flex flex-wrap items-center gap-2">
        <span className="mr-1 text-[13px] font-semibold tabular">{tr("governance.vulnerabilities.n_selezionate", { count: ids.length })}</span>
        <button type="button" className="btn btn-primary py-1.5" disabled={act.isPending} onClick={() => act.mutate("take")}>{tr("governance.vulnerabilities.prendi_in_carico")}</button>
        <button type="button" className="btn py-1.5" disabled={act.isPending} onClick={() => act.mutate("start")}>{tr("governance.vulnerabilities.in_correzione")}</button>
        <button type="button" className="btn py-1.5" onClick={() => setPanel("assign")}>{tr("governance.vulnerabilities.assegna")}</button>
        <button type="button" className="btn py-1.5" onClick={() => setPanel("exception")}>{tr("governance.vulnerabilities.chiedi_eccezione")}</button>
        <button type="button" className="btn py-1.5" onClick={() => setPanel("resolve")}>{tr("governance.vulnerabilities.risolta")}</button>
        <button type="button" className="btn py-1.5" onClick={() => setPanel("false_positive")}>{tr("governance.vulnerabilities.falso_positivo")}</button>
        <button type="button" className="ml-auto text-[12.5px] text-muted underline hover:text-fg" onClick={onClear}>{tr("governance.vulnerabilities.deseleziona")}</button>
      </div>
      <ErrorNote error={act.error} />
      {panel === "assign" && <AssignDialog ids={ids} onClose={() => setPanel(null)} onDone={(r) => { setPanel(null); done(r); }} />}
      {panel === "exception" && <ExceptionDialog ids={ids} onClose={() => setPanel(null)} onDone={(msg) => { setPanel(null); qc.invalidateQueries({ queryKey: ["vuln"] }); onDone(msg); }} />}
      {(panel === "false_positive" || panel === "resolve") && (
        <NoteDialog ids={ids} action={panel} onClose={() => setPanel(null)} onDone={(r) => { setPanel(null); done(r); }} />
      )}
    </div>
  );
}

function AssignDialog({ ids, onClose, onDone }: { ids: string[]; onClose: () => void; onDone: (r: { changed: number; skipped: number; notes: string[] }) => void }) {
  const cfg = useQuery({ queryKey: ["vuln-config"], queryFn: vulnApi.config });
  const [team, setTeam] = useState("");
  const [who, setWho] = useState("");
  const m = useMutation({ mutationFn: () => vulnApi.bulk({ ids, action: "assign", team, assignee: who }), onSuccess: onDone });
  return (
    <Dialog title={tr("governance.vulnerabilities.assegna_n", { count: ids.length })} onClose={onClose}>
      <form className="flex flex-col gap-3" onSubmit={(e) => { e.preventDefault(); m.mutate(); }}>
        <label className="flex flex-col gap-1">
          <span className="label">{tr("governance.requirements.team")}</span>
          <Select className="field" value={team} onChange={(e) => setTeam(e.target.value)}>
            <option value="">{tr("governance.vulnerabilities.non_cambiare")}</option>
            <option value="auto">{tr("governance.vulnerabilities.quello_proprietario_dell_applicazione_automatico")}</option>
            {cfg.data?.teams.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
          </Select>
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{tr("governance.vulnerabilities.persona")}</span>
          <Select className="field" value={who} onChange={(e) => setWho(e.target.value)}>
            <option value="">{tr("governance.vulnerabilities.non_cambiare")}</option>
            {cfg.data?.members.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
          </Select>
          <span className="text-[12px] text-muted">{tr("governance.vulnerabilities.una_vulnerabilita_aperta_a_cui")}</span>
        </label>
        <ErrorNote error={m.error} />
        <div className="flex gap-2"><button className="btn btn-primary" disabled={m.isPending || (!team && !who)}>{tr("governance.vulnerabilities.assegna_3")}</button><button type="button" className="btn" onClick={onClose}>{tr("governance.compliance.annulla")}</button></div>
      </form>
    </Dialog>
  );
}

function NoteDialog({ ids, action, onClose, onDone }: { ids: string[]; action: "false_positive" | "resolve"; onClose: () => void; onDone: (r: { changed: number; skipped: number; notes: string[] }) => void }) {
  const [note, setNote] = useState("");
  const fp = action === "false_positive";
  const m = useMutation({ mutationFn: () => vulnApi.bulk({ ids, action, note }), onSuccess: onDone });
  return (
    <Dialog title={fp ? tr("governance.vulnerabilities.falso_positivo_2") : tr("governance.vulnerabilities.segna_come_risolta")} onClose={onClose}>
      <form className="flex flex-col gap-3" onSubmit={(e) => { e.preventDefault(); m.mutate(); }}>
        <label className="flex flex-col gap-1">
          <span className="label">{fp ? tr("governance.vulnerabilities.perche_e_un_falso_positivo") : tr("governance.requirements.cosa_e_stato_fatto_facoltativo")}</span>
          <textarea className="field min-h-20" required={fp} value={note} onChange={(e) => setNote(e.target.value)} />
        </label>
        {!fp && <p className="m-0 text-[12.5px] text-muted">{tr("governance.vulnerabilities.resta_risolta_finche_compare_nei")}</p>}
        <ErrorNote error={m.error} />
        <div className="flex gap-2"><button className="btn btn-primary" disabled={m.isPending || (fp && !note.trim())}>{tr("governance.vulnerabilities.conferma")}</button><button type="button" className="btn" onClick={onClose}>{tr("governance.compliance.annulla")}</button></div>
      </form>
    </Dialog>
  );
}

function plusDays(n: number): string {
  const d = new Date();
  d.setDate(d.getDate() + n);
  return d.toISOString().slice(0, 10);
}

function ExceptionDialog({ ids, onClose, onDone }: { ids: string[]; onClose: () => void; onDone: (msg: string) => void }) {
  const cfg = useQuery({ queryKey: ["vuln-config"], queryFn: vulnApi.config });
  const risks = useQuery({ queryKey: ["vuln-risks"], queryFn: () => vulnApi.risks() });
  const maxDays = cfg.data?.policy.max_exception_days ?? 180;
  const [reason, setReason] = useState("");
  const [comp, setComp] = useState("");
  const [until, setUntil] = useState(plusDays(30));
  const [risk, setRisk] = useState("");
  const m = useMutation({
    mutationFn: () => vulnApi.requestException({ ids, reason, until, compensating: comp, risk }),
    onSuccess: (r) => onDone(tr("governance.vulnerabilities.eccezioni_richieste_ora_attendono", { count: r.items.length, keys: r.items.map((e) => e.key).join(", ") })),
  });
  return (
    <Dialog title={tr("governance.vulnerabilities.chiedi_un_eccezione")} onClose={onClose} wide>
      <form className="flex flex-col gap-3" onSubmit={(e) => { e.preventDefault(); m.mutate(); }}>
        <p className="m-0 text-[12.5px] text-muted">
          {tr("governance.vulnerabilities.l_eccezione_accetta_il_rischio", { count: ids.length })}
        </p>
        <label className="flex flex-col gap-1">
          <span className="label">{tr("governance.requirements.motivazione")}</span>
          <textarea className="field min-h-24" required value={reason} onChange={(e) => setReason(e.target.value)} placeholder={tr("governance.vulnerabilities.perche_non_si_corregge_ora")} />
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{tr("governance.vulnerabilities.misure_compensative_facoltative")}</span>
          <textarea className="field min-h-16" value={comp} onChange={(e) => setComp(e.target.value)} placeholder={tr("governance.vulnerabilities.per_esempio_componente_non_raggiungibile")} />
        </label>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <label className="flex flex-col gap-1">
            <span className="label">{tr("governance.compliance.valida_fino_al")}</span>
            <input className="field" type="date" required min={plusDays(1)} max={plusDays(maxDays)} value={until} onChange={(e) => setUntil(e.target.value)} />
            <span className="text-[12px] text-muted">{tr("governance.vulnerabilities.al_massimo_giorni", { maxDays })}</span>
          </label>
          <label className="flex flex-col gap-1">
            <span className="label">{tr("governance.vulnerabilities.rischio_del_registro_facoltativo")}</span>
            <Select className="field" value={risk} onChange={(e) => setRisk(e.target.value)}>
              <option value="">{tr("governance.devsecops.nessuno")}</option>
              {risks.data?.items.map((r) => <option key={r.id} value={r.key}>{r.key} · {r.name}</option>)}
            </Select>
          </label>
        </div>
        <ErrorNote error={m.error} />
        <div className="flex gap-2"><button className="btn btn-primary" disabled={m.isPending || !reason.trim()}>{tr("governance.requirements.chiedi_l_approvazione")}</button><button type="button" className="btn" onClick={onClose}>{tr("governance.compliance.annulla")}</button></div>
      </form>
    </Dialog>
  );
}

// ---------------------------------------------------------------------
// Registro delle eccezioni

function ExceptionsRegistry({ canAct }: { canAct: boolean }) {
  const qc = useQueryClient();
  const [status, setStatus] = useState("");
  const q = useQuery({ queryKey: ["vuln", "exceptions", status], queryFn: () => vulnApi.exceptions(status) });
  const [link, setLink] = useState<Exception | null>(null);
  const revoke = useMutation({
    mutationFn: (e: Exception) => vulnApi.revoke(e.id, ""),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["vuln"] }),
  });
  const items = q.data?.items ?? [];
  return (
    <section className="flex min-w-0 flex-col gap-2" aria-label={tr("governance.vulnerabilities.eccezioni")}>
      <SectionTitle aside={
        <Select aria-label={tr("governance.vulnerabilities.stato_delle_eccezioni")} className="field w-auto py-1 text-[12.5px]" value={status} onChange={(e) => setStatus(e.target.value)}>
          <option value="">{tr("governance.risks.tutte")}</option>
          {Object.entries(EXC_STATUS).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
        </Select>
      }>{tr("governance.vulnerabilities.eccezioni_sulle_vulnerabilita")}</SectionTitle>
      <ErrorNote error={q.error || revoke.error} />
      {q.data && items.length === 0 && <p className="m-0 text-muted">{tr("governance.vulnerabilities.nessuna_eccezione_si_chiedono_dall")}</p>}
      <ul className="m-0 flex list-none flex-col p-0">
        {items.map((e) => (
          <li key={e.id} className="grid grid-cols-1 gap-x-4 gap-y-1 border-b border-hair py-3 md:grid-cols-[90px_minmax(0,1.2fr)_minmax(0,1.5fr)_170px]">
            <span className="font-mono text-[13px] font-semibold">{e.key}</span>
            <span className="min-w-0 text-[12.5px]">
              <Link to="/e/$key" params={{ key: e.application_key }} search={{ scheda: "approvazioni" }} className="font-medium">{e.application}</Link>
              <span className="block text-muted">{tr("governance.vulnerabilities.n_vulnerabilita", { count: e.findings })}{" "}{tr("governance.vulnerabilities.la_piu_grave", { p: SEV_NAME[e.worst] ?? e.worst })}</span>
              <span className="block text-muted">{tr("governance.vulnerabilities.chiesta_da")}{" "}{e.requested_by ?? "—"} · {ago(e.requested_at)}</span>
            </span>
            <span className="min-w-0 text-[12.5px]">
              {e.reason}
              {e.compensating && <span className="block text-muted">{tr("governance.vulnerabilities.misure", { compensating: e.compensating })}</span>}
              <span className="block">
                {e.risk_key ? <>{tr("governance.vulnerabilities.rischio_2")}{" "}<Link to="/e/$key" params={{ key: e.risk_key }}>{e.risk_key}</Link> <span className="text-muted">{e.risk_title}</span></> : <span className="text-muted">{tr("governance.vulnerabilities.nessun_rischio_collegato")}</span>}
                {canAct && ["pending", "approved"].includes(e.status) && (
                  <button type="button" className="ml-2 text-[12px] text-accent underline" onClick={() => setLink(e)}>{e.risk_key ? tr("governance.vulnerabilities.cambia") : tr("governance.vulnerabilities.collega")}</button>
                )}
              </span>
            </span>
            <span className="text-[12.5px] md:text-right">
              <span className={`font-semibold ${e.status === "approved" ? "text-ok" : e.status === "pending" ? "text-flag" : "text-muted"}`}>{EXC_STATUS[e.status]}</span>
              <span className="block text-muted tabular">{tr("governance.vulnerabilities.fino_al_2", { day: day(e.until) })}{e.status === "approved" && e.days_left >= 0 ? tr("governance.vulnerabilities.giorni", { count: e.days_left }) : ""}</span>
              {e.status === "pending" && e.waiting.length > 0 && <span className="block text-muted">{tr("governance.vulnerabilities.attende")}{" "}{e.waiting.join(", ")}</span>}
              {canAct && ["pending", "approved"].includes(e.status) && (
                <button type="button" className="text-[12px] text-muted underline hover:text-danger" disabled={revoke.isPending} onClick={() => confirm(tr("governance.vulnerabilities.revocare_l_eccezione_le_vulnerabilita", { key: e.key })) && revoke.mutate(e)}>{tr("governance.vulnerabilities.revoca")}</button>
              )}
            </span>
          </li>
        ))}
      </ul>
      {link && <RiskLinkDialog e={link} onClose={() => setLink(null)} />}
    </section>
  );
}

function RiskLinkDialog({ e, onClose }: { e: Exception; onClose: () => void }) {
  const qc = useQueryClient();
  const risks = useQuery({ queryKey: ["vuln-risks"], queryFn: () => vulnApi.risks() });
  const [risk, setRisk] = useState(e.risk_key ?? "");
  const m = useMutation({ mutationFn: () => vulnApi.setRisk(e.id, risk), onSuccess: () => { qc.invalidateQueries({ queryKey: ["vuln"] }); onClose(); } });
  return (
    <Dialog title={tr("governance.vulnerabilities.rischio_collegato_a", { key: e.key })} onClose={onClose}>
      <form className="flex flex-col gap-3" onSubmit={(ev) => { ev.preventDefault(); m.mutate(); }}>
        <label className="flex flex-col gap-1">
          <span className="label">{tr("governance.vulnerabilities.rischio_del_registro")}</span>
          <Select className="field" value={risk} onChange={(ev) => setRisk(ev.target.value)}>
            <option value="">{tr("governance.devsecops.nessuno")}</option>
            {risks.data?.items.map((r) => <option key={r.id} value={r.key}>{r.key} · {r.name}</option>)}
          </Select>
          <span className="text-[12px] text-muted">{tr("governance.vulnerabilities.il_collegamento_unisce_il_rischio")}</span>
        </label>
        <ErrorNote error={m.error} />
        <div className="flex gap-2"><button className="btn btn-primary" disabled={m.isPending}>{tr("governance.compliancesections.salva")}</button><button type="button" className="btn" onClick={onClose}>{tr("governance.compliance.annulla")}</button></div>
      </form>
    </Dialog>
  );
}

// ---------------------------------------------------------------------
// La pagina

const PAGE = 50;

export function VulnerabilitiesPage() {
  const narrow = useNarrow();
  const [view, setView] = useState<"vulnerabilita" | "eccezioni">("vulnerabilita");
  const [team, setTeam] = useState("");
  const [app, setApp] = useState("");
  const [sevs, setSevs] = useState<string[]>([]);
  const initialQ = (useSearch({ strict: false }) as { q?: string }).q ?? "";
  const [text, setText] = useState(initialQ);
  const [typed, setTyped] = useState(initialQ);
  const [mine, setMine] = useState(false);
  const [stage, setStage] = useState<StageTab>("open");
  const [due, setDue] = useState("");
  const [sort, setSort] = useState("");
  const [limit, setLimit] = useState(PAGE);
  const [sel, setSel] = useState<string[]>([]);
  const [openId, setOpenId] = useState<string | null>(null);
  const [msg, setMsg] = useState("");
  const [showFilters, setShowFilters] = useState(false);
  useEffect(() => {
    const t = setTimeout(() => setText(typed), 300);
    return () => clearTimeout(t);
  }, [typed]);
  const base: Filters = { team, application: app, severity: sevs.join(","), q: text, mine };
  const dash = useQuery({ queryKey: ["vuln", "dashboard", base], queryFn: () => vulnApi.dashboard(base), placeholderData: keepPreviousData });
  const listF: Filters = { ...base, stage: stage === "closed" ? "closed" : stage, due: stage === "open" ? due : "", sort, limit };
  const list = useQuery({ queryKey: ["vuln", "items", listF], queryFn: () => vulnApi.items(listF), placeholderData: keepPreviousData });
  const d: Dashboard | undefined = dash.data;
  const soon = d?.kpis.soon_days ?? 7;
  useEffect(() => setLimit(PAGE), [team, app, sevs, text, mine, stage, due, sort]);
  useEffect(() => setSel([]), [team, app, sevs, text, mine, stage, due, sort]);
  const items = list.data?.items ?? [];
  const toggle = (id: string) => setSel((s) => (s.includes(id) ? s.filter((x) => x !== id) : [...s, id]));
  const allSel = items.length > 0 && items.every((i) => sel.includes(i.id));
  const filtered = !!(team || app || sevs.length || text || mine);
  const pickGroup = (kind: "team" | "app", g: Group) => {
    if (kind === "team") setTeam((t) => (t === (g.id || "none") ? "" : g.id || "none"));
    else setApp((a) => (a === g.id ? "" : g.id));
    setView("vulnerabilita");
  };
  const k = d?.kpis;
  const sevName = useMemo(() => SEVERITIES.map((s) => ({ key: s, label: SEV_NAME[s] })), []);
  return (
    <>
      <PageHeader path={crumbs("devsecops", "vulnerabilities")} title={tr("governance.vulnerabilities.vulnerabilita")} actions={
        <>
          <Link to="/devsecops" className="btn no-underline">{tr("governance.vulnerabilities.pannello_devsecops")}</Link>
          {d?.can_configure && <Link to="/configurazione/vulnerabilita" className="btn no-underline">{tr("governance.vulnerabilities.politica_e_sla")}</Link>}
        </>
      } />
      <div className="flex max-w-[1400px] flex-col gap-7 px-4 pb-24 md:px-7">
        <p className="m-0 max-w-3xl text-muted max-md:hidden">{tr("governance.vulnerabilities.le_vulnerabilita_che_le_pipeline")}</p>
        <ErrorNote error={dash.error} />

        {/* Sul telefono i filtri stanno dietro un pulsante e vengono dopo gli indicatori. */}
        <button type="button" className="btn max-md:order-[-5] min-h-[44px] justify-center self-start md:hidden" aria-expanded={showFilters} aria-controls="filtri-vulnerabilita" onClick={() => setShowFilters((v) => !v)}>{tr("governance.vulnerabilities.filtri")}{filtered ? tr("governance.vulnerabilities.attivi") : ""}
        </button>
        <section id="filtri-vulnerabilita" aria-label={tr("governance.vulnerabilities.filtri")} className={`${showFilters ? "flex" : "max-md:hidden md:flex"} flex-col gap-2.5 border-b border-hair pb-4 max-md:order-[-4]`}>
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_minmax(0,1.2fr)_auto]">
            <label className="flex flex-col gap-1">
              <span className="label text-muted">{tr("governance.requirements.team")}</span>
              <Select className="field py-1.5" value={team} onChange={(e) => setTeam(e.target.value)}>
                <option value="">{tr("governance.vulnerabilities.tutti_i_team")}</option>
                <option value="none">{tr("governance.vulnerabilities.senza_team")}</option>
                {d?.filters.teams.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
              </Select>
            </label>
            <label className="flex flex-col gap-1">
              <span className="label text-muted">{tr("governance.requirements.applicazione")}</span>
              <Select className="field py-1.5" value={app} onChange={(e) => setApp(e.target.value)}>
                <option value="">{tr("governance.vulnerabilities.tutte_le_applicazioni")}</option>
                {d?.filters.applications.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
              </Select>
            </label>
            <label className="flex flex-col gap-1">
              <span className="label text-muted">{tr("governance.risks.cerca")}</span>
              <input className="field py-1.5" type="search" placeholder={tr("governance.vulnerabilities.cve_pacchetto_servizio")} value={typed} onChange={(e) => setTyped(e.target.value)} />
            </label>
            <label className="flex items-end gap-2 pb-2 text-[13px]">
              <input type="checkbox" className="size-4" checked={mine} onChange={(e) => setMine(e.target.checked)} />{tr("governance.vulnerabilities.solo_quelle_assegnate_a_me")}</label>
          </div>
          <div className="flex flex-wrap items-center gap-2" role="group" aria-label={tr("governance.vulnerabilities.gravita")}>
            <span className="label text-muted">{tr("governance.vulnerabilities.gravita")}</span>
            {sevName.map((s) => {
              const on = sevs.includes(s.key);
              return (
                <button key={s.key} type="button" aria-pressed={on} onClick={() => setSevs((v) => (on ? v.filter((x) => x !== s.key) : [...v, s.key]))}
                  className={`inline-flex cursor-pointer items-center gap-1.5 border px-2 py-1 text-[12.5px] font-semibold ${on ? "border-accent bg-sel text-accent" : "border-hair bg-transparent text-fg"}`}>
                  <span aria-hidden="true" className={`inline-block size-2.5 ${SEV_MARK[s.key]}`} />
                  {s.label}
                </button>
              );
            })}
            {filtered && (
              <button type="button" className="ml-1 text-[12.5px] text-muted underline hover:text-fg" onClick={() => { setTeam(""); setApp(""); setSevs([]); setTyped(""); setMine(false); }}>{tr("governance.vulnerabilities.azzera_i_filtri")}</button>
            )}
          </div>
        </section>

        {k && (
          <section aria-label={tr("governance.compliance.in_sintesi")} className="grid grid-cols-2 gap-3 max-md:order-first md:grid-cols-3 lg:grid-cols-5">
            <Kpi i={0} label={tr("governance.vulnerabilities.da_correggere_2")} value={k.open} note={tr("governance.vulnerabilities.critiche_di_cui_sulla_versione", { critical_open: k.critical_open, p: k.critical_prod ?? 0 })} onClick={() => { setView("vulnerabilita"); setStage("open"); setDue(""); }} active={view === "vulnerabilita" && stage === "open" && due === ""} />
            <Kpi i={1} className="max-md:order-first" label={tr("governance.vulnerabilities.oltre_la_scadenza_2")} value={k.overdue} tone={k.overdue > 0 ? "text-danger" : "text-muted"} note={k.no_sla > 0 ? tr("governance.vulnerabilities.senza_sla", { no_sla: k.no_sla }) : tr("governance.vulnerabilities.rispetto_allo_sla")}
              onClick={() => { setView("vulnerabilita"); setStage("open"); setDue("overdue"); }} active={view === "vulnerabilita" && stage === "open" && due === "overdue"} />
            <Kpi i={2} label={tr("governance.vulnerabilities.in_scadenza_giorni", { soon_days: k.soon_days })} value={k.soon} tone={k.soon > 0 ? "text-flag" : "text-muted"}
              onClick={() => { setView("vulnerabilita"); setStage("open"); setDue("soon"); }} active={view === "vulnerabilita" && stage === "open" && due === "soon"} />
            <Kpi i={3} label={tr("governance.vulnerabilities.eccezioni_in_vigore")} value={k.exceptions} note={k.exceptions_pending > 0 ? tr("governance.vulnerabilities.in_approvazione_2", { exceptions_pending: k.exceptions_pending }) : tr("governance.vulnerabilities.nessuna_in_attesa")} onClick={() => setView("eccezioni")} active={view === "eccezioni"} />
            <Kpi i={4} label={tr("governance.vulnerabilities.tempo_di_correzione")} value={k.mttr_days == null ? "—" : tr("governance.vulnerabilities.gg", { days: num(k.mttr_days, 0) })} note={tr("governance.vulnerabilities.chiuse_in_30_giorni", { resolved_30d: k.resolved_30d })} />
          </section>
        )}

        {d && (
          <div className="grid grid-cols-1 gap-x-10 gap-y-7 lg:grid-cols-2">
            <AgingChart rows={d.aging} />
            <TrendChart points={d.trend} />
            <GroupTable title={tr("governance.vulnerabilities.per_team")} rows={d.teams} kind="team" narrow={narrow} onPick={(g) => pickGroup("team", g)} active={team} />
            <GroupTable title={tr("governance.vulnerabilities.per_applicazione")} rows={d.applications} kind="app" narrow={narrow} onPick={(g) => pickGroup("app", g)} active={app} />
          </div>
        )}

        <div role="tablist" aria-label={tr("governance.vulnerabilities.vista")} className="flex gap-1 border-b-2 border-rule">
          {([["vulnerabilita", tr("governance.vulnerabilities.vulnerabilita")], ["eccezioni", tr("governance.vulnerabilities.eccezioni")]] as const).map(([key, label]) => (
            <button key={key} role="tab" aria-selected={view === key} type="button" onClick={() => setView(key)}
              className={`-mb-0.5 cursor-pointer border-0 border-b-2 bg-transparent px-3 py-2 text-[13.5px] font-semibold ${view === key ? "border-accent text-accent" : "border-transparent text-muted"}`}>
              {label}
            </button>
          ))}
        </div>

        {view === "eccezioni" && <ExceptionsRegistry canAct={!!d?.can_act} />}

        {view === "vulnerabilita" && (
          <section className="flex min-w-0 flex-col gap-2" aria-label={tr("governance.vulnerabilities.elenco_delle_vulnerabilita")}>
            <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
              <div role="group" aria-label={tr("governance.vulnerabilities.stadio")} className="flex flex-wrap gap-1">
                {STAGE_TABS.map((t) => (
                  <button key={t.key} type="button" aria-pressed={stage === t.key} onClick={() => setStage(t.key)}
                    className={`cursor-pointer border px-2.5 py-1 text-[12.5px] font-semibold ${stage === t.key ? "border-accent bg-sel text-accent" : "border-hair bg-transparent text-fg"}`}>
                    {t.label}
                  </button>
                ))}
              </div>
              {stage === "open" && (
                <label className="flex items-center gap-2 text-[12.5px]">
                  <span className="label text-muted">{tr("governance.requirements.scadenza")}</span>
                  <Select className="field w-auto py-1" value={due} onChange={(e) => setDue(e.target.value)}>
                    <option value="">{tr("governance.risks.tutte")}</option>
                    <option value="overdue">{tr("governance.vulnerabilities.oltre_la_scadenza")}</option>
                    <option value="soon">{tr("governance.vulnerabilities.in_scadenza")}</option>
                    <option value="nosla">{tr("governance.vulnerabilities.senza_sla_2")}</option>
                  </Select>
                </label>
              )}
              <label className="flex items-center gap-2 text-[12.5px]">
                <span className="label text-muted">{tr("governance.vulnerabilities.ordina")}</span>
                <Select className="field w-auto py-1" value={sort} onChange={(e) => setSort(e.target.value)}>
                  <option value="">{tr("governance.vulnerabilities.per_scadenza")}</option>
                  <option value="severity">{tr("governance.vulnerabilities.per_gravita")}</option>
                  <option value="age">{tr("governance.vulnerabilities.per_anzianita")}</option>
                  <option value="application">{tr("governance.vulnerabilities.per_applicazione_2")}</option>
                </Select>
              </label>
              <span className="ml-auto text-[12.5px] text-muted tabular" aria-live="polite">{list.data ? tr("governance.vulnerabilities.n_vulnerabilita", { count: list.data.total }) : ""}</span>
            </div>
            <ErrorNote error={list.error} />
            {msg && <p role="status" className="m-0 border-l-2 border-accent bg-sel px-3 py-2 text-[13px]">{msg} <button type="button" className="ml-2 text-[12px] text-muted underline" onClick={() => setMsg("")}>{tr("governance.vulnerabilities.chiudi")}</button></p>}
            {d?.can_act && items.length > 0 && (
              <label className="flex items-center gap-2 border-b-2 border-rule pb-1.5 text-[12.5px]">
                <input type="checkbox" className="size-4" checked={allSel} onChange={() => setSel(allSel ? [] : items.map((i) => i.id))} />
                <span className="label text-muted">{tr("governance.vulnerabilities.seleziona_le_in_vista", { length: items.length })}</span>
              </label>
            )}
            {list.data && items.length === 0 && (
              <p className="m-0 py-6 text-muted">
                {stage === "open" && !filtered && !due ? tr("governance.vulnerabilities.nessuna_vulnerabilita_da_correggere_i") : tr("governance.vulnerabilities.nessuna_vulnerabilita_con_questi_filtri")}
              </p>
            )}
            <ul className="m-0 flex list-none flex-col p-0">
              {items.map((it) => (
                <Row key={it.id} it={it} soon={soon} selected={sel.includes(it.id)} onSelect={() => toggle(it.id)} canAct={!!d?.can_act} narrow={narrow}
                  open={openId === it.id} onOpen={() => setOpenId((o) => (o === it.id ? null : it.id))} />
              ))}
            </ul>
            {list.data && list.data.total > items.length && (
              <button type="button" className="btn self-start" onClick={() => setLimit((l) => l + PAGE)}>{tr("governance.vulnerabilities.mostra_altre", { p: list.data.total - items.length })}</button>
            )}
          </section>
        )}
      </div>
      <BulkBar ids={view === "vulnerabilita" ? sel : []} canAct={!!d?.can_act} onClear={() => setSel([])} onDone={(m) => { setMsg(m); setSel([]); }} />
    </>
  );
}
