// Metriche di consegna (DevSecOps, fase 5a; replica/devsecops.md §3.6): le quattro chiavi DORA
// dai rilasci, dai deploy, dai controlli e dagli incidenti già in Regia.
// - DoraPage: /devsecops/metriche, cruscotto con trend, grafici, confronto e approfondimento;
// - DoraCard: riquadro compatto nella pagina dell'applicazione e del team;
// - le soglie dei livelli e i criteri di fallimento si modificano con l'anteprima dell'effetto.
import { Select } from "../select";
import { useEffect, useMemo, useRef, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  doraApi,
  type Dashboard,
  type Drill,
  type DoraSettings,
  type Ladder,
  type Level,
  type MetricCard,
  type MetricKey,
  type Period,
  type PreviewRow,
  type Scope,
} from "../doraApi";
import { PageHeader } from "../shell";
import { ErrorNote, SectionTitle, Loading } from "../ui";
import { useNarrow } from "./timeline";
import { tr } from "../i18n/core";
import { Trans } from "../i18n/Trans";
import { loc } from "../i18n/format";
import { crumbs } from "../crumbs";

// ---------------------------------------------------------------------
// Formati e livelli

const LEVELS: Level[] = ["low", "medium", "high", "elite"];
const LEVEL_LABEL: Record<Level, string> = { get elite() { return tr("governance.dora.elite"); }, get high() { return tr("governance.dora.alto"); }, get medium() { return tr("governance.dora.medio"); }, get low() { return tr("governance.dora.basso"); } };
const LEVEL_TONE: Record<Level, string> = { elite: "text-ok", high: "text-ok", medium: "text-fg", low: "text-danger" };
const METRIC_SHORT: Record<MetricKey, string> = {
  get deploy_frequency() { return tr("governance.compliance.frequenza"); },
  get lead_time_days() { return tr("governance.dora.lead_time"); },
  get change_failure_rate() { return tr("governance.dora.fallimenti"); },
  get mttr_hours() { return tr("governance.dora.ripristino"); },
};
const METRIC_KEYS: MetricKey[] = ["deploy_frequency", "lead_time_days", "change_failure_rate", "mttr_hours"];
const PERIODS: { key: Period; label: string; prev: string }[] = [
  { key: "week", get label() { return tr("governance.dora.settimana"); }, get prev() { return tr("governance.dora.settimana_precedente"); } },
  { key: "month", get label() { return tr("governance.dora.mese"); }, get prev() { return tr("governance.dora.mese_precedente"); } },
  { key: "quarter", get label() { return tr("governance.dora.trimestre"); }, get prev() { return tr("governance.dora.trimestre_precedente"); } },
];
const SCOPES: { key: Scope; label: string }[] = [
  { key: "tenant", get label() { return tr("governance.dora.organizzazione"); } },
  { key: "team", get label() { return tr("governance.requirements.team"); } },
  { key: "application", get label() { return tr("governance.requirements.applicazione"); } },
  { key: "asset", get label() { return tr("governance.devsecops.asset"); } },
  { key: "service", get label() { return tr("governance.devsecops.servizio"); } },
];

const DEFAULT_THRESHOLDS: Record<MetricKey, Ladder> = {
  deploy_frequency: { elite: 7, high: 1, medium: 0.25 },
  lead_time_days: { elite: 1, high: 7, medium: 30 },
  change_failure_rate: { elite: 5, high: 10, medium: 15 },
  mttr_hours: { elite: 1, high: 24, medium: 168 },
};

const nf = (v: number, d: number) => v.toLocaleString(loc(), { minimumFractionDigits: 0, maximumFractionDigits: d });

/** Numero e unità di una metrica, nella scala che si legge meglio. */
function fmt(key: MetricKey, v: number | null | undefined): { num: string; unit: string } {
  if (v == null) return { num: "—", unit: "" };
  switch (key) {
    case "deploy_frequency":
      return { num: nf(v, v < 10 ? 1 : 0), unit: tr("governance.dora.a_settimana") };
    case "lead_time_days":
      return v < 1 ? { num: nf(v * 24, 0), unit: tr("governance.dora.ore") } : { num: nf(v, v < 10 ? 1 : 0), unit: v < 1.5 && v >= 1 ? tr("governance.dora.giorno") : tr("governance.dora.giorni") };
    case "change_failure_rate":
      return { num: nf(v, 1), unit: "%" };
    case "mttr_hours":
      return v >= 48 ? { num: nf(v / 24, 1), unit: tr("governance.dora.giorni") } : { num: nf(v, 1), unit: tr("governance.dora.ore") };
  }
}

function fmtOne(key: MetricKey, v: number | null | undefined): string {
  const f = fmt(key, v);
  return v == null ? "—" : `${f.num}${f.unit === "%" ? " %" : ` ${f.unit === tr("governance.dora.a_settimana") ? tr("governance.dora.sett") : f.unit}`}`;
}

const utc = { timeZone: "UTC" } as const;

/** Nome leggibile di un periodo di calendario che comincia a `iso`. */
function periodName(iso: string, period: Period, short = false): string {
  const d = new Date(iso);
  if (period === "month") return d.toLocaleDateString(loc(), { ...utc, month: short ? "short" : "long", year: short ? "2-digit" : "numeric" });
  if (period === "quarter") return `T${Math.floor(d.getUTCMonth() / 3) + 1} ${short ? String(d.getUTCFullYear()).slice(2) : d.getUTCFullYear()}`;
  const end = new Date(d.getTime() + 6 * 86400000);
  if (short) return d.toLocaleDateString(loc(), { ...utc, day: "numeric", month: "short" });
  return `${d.toLocaleDateString(loc(), { ...utc, day: "numeric", month: "short" })} – ${end.toLocaleDateString(loc(), { ...utc, day: "numeric", month: "short", year: "numeric" })}`;
}

function dateTime(iso: string): string {
  return new Date(iso).toLocaleString(loc(), { day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit" });
}

/** Quattro tacche e il nome del livello: il colore non è l'unico segnale. */
function LevelMark({ level, compact }: { level: Level | "" | undefined; compact?: boolean }) {
  if (!level) return <span className="text-muted">—</span>;
  const n = LEVELS.indexOf(level) + 1;
  return (
    <span className={`inline-flex items-center gap-1.5 font-semibold ${LEVEL_TONE[level]}`}>
      <svg width="22" height="10" viewBox="0 0 22 10" aria-hidden="true" className="shrink-0">
        {LEVELS.map((_, i) => (
          <rect key={i} x={i * 6} y={0} width={4} height={10} className={i < n ? "fill-current" : "fill-hair"} />
        ))}
      </svg>
      <span className={compact ? "text-[12px]" : ""}>{LEVEL_LABEL[level]}</span>
    </span>
  );
}

/** Differenza con il periodo precedente, in parole: migliora o peggiora. */
function Trend({ card, prevLabel }: { card: MetricCard; prevLabel: string }) {
  if (card.value == null || card.prev == null) return <span className="text-[12px] text-muted">{tr("governance.dora.nessun_confronto_con_il", { prevLabel })}</span>;
  const diff = card.value - card.prev;
  if (Math.abs(diff) < 0.005) return <span className="text-[12px] text-muted">{tr("governance.dora.uguale_al", { prevLabel })}</span>;
  const better = card.higher_better ? diff > 0 : diff < 0;
  const f = fmt(card.key, Math.abs(diff));
  return (
    <span className={`text-[12px] tabular ${better ? "text-ok" : "text-danger"}`}>
      {better ? tr("governance.dora.migliora") : tr("governance.dora.peggiora")}{" "}{tr("governance.dora.di", { num: f.num })}{" "}{f.unit === tr("governance.dora.a_settimana") ? "" : f.unit}
      <span className="text-muted">{tr("governance.dora.sul", { prevLabel })}</span>
    </span>
  );
}

// ---------------------------------------------------------------------
// Grafici

const W = 320;
const H = 132;
const PAD = { l: 6, r: 6, t: 10, b: 20 };

/** Serie di una metrica con le soglie dei livelli; ogni punto apre l'approfondimento. */
function Chart({ card, period, selected, onPick }: { card: MetricCard; period: Period; selected: string | null; onPick: (at: string) => void }) {
  const pts = card.series;
  const vals = pts.map((p) => p.value).filter((v): v is number => v != null);
  const { elite, high, medium } = card.ladder;
  const top = Math.max(1e-6, ...vals, card.higher_better ? elite * 0.6 : medium * 1.15);
  const iw = W - PAD.l - PAD.r;
  const ih = H - PAD.t - PAD.b;
  const x = (i: number) => PAD.l + (pts.length <= 1 ? iw / 2 : (i * iw) / (pts.length - 1));
  const y = (v: number) => PAD.t + ih - (Math.min(v, top) / top) * ih;
  const bars = card.key === "deploy_frequency";
  const bw = Math.max(3, Math.min(14, (iw / pts.length) * 0.62));
  const lines = (
    [
      [LEVEL_LABEL.elite.toLowerCase(), elite],
      [LEVEL_LABEL.high.toLowerCase(), high],
      [LEVEL_LABEL.medium.toLowerCase(), medium],
    ] as [string, number][]
  ).filter(([, v]) => v > 0 && v <= top);
  const segments: string[] = [];
  let cur: string[] = [];
  pts.forEach((p, i) => {
    if (p.value == null) {
      if (cur.length) segments.push(cur.join(" "));
      cur = [];
    } else cur.push(`${x(i).toFixed(1)},${y(p.value).toFixed(1)}`);
  });
  if (cur.length) segments.push(cur.join(" "));
  const labelIdx = [0, Math.floor((pts.length - 1) / 2), pts.length - 1].filter((v, i, a) => a.indexOf(v) === i && pts.length > 0);
  const summary = tr("governance.dora.ultimi_periodi", { name: card.name, length: pts.length, p: pts.map((p) => `${periodName(p.at, period, true)} ${fmtOne(card.key, p.value)}`).join("; ") });
  return (
    <svg viewBox={`0 0 ${W} ${H}`} className="block h-auto w-full" role="group" aria-label={summary}>
      {lines.map(([name, v]) => (
        <g key={name}>
          <line x1={PAD.l} x2={W - PAD.r} y1={y(v)} y2={y(v)} className="stroke-hair" strokeWidth={1} strokeDasharray="3 3" />
          <text x={W - PAD.r} y={y(v) - 2} textAnchor="end" className="fill-muted" fontSize="8.5">
            {name}
          </text>
        </g>
      ))}
      <line x1={PAD.l} x2={W - PAD.r} y1={PAD.t + ih} y2={PAD.t + ih} className="stroke-rule" strokeWidth={1} />
      {!bars && segments.map((s, i) => <polyline key={i} points={s} fill="none" className="stroke-fg" strokeWidth={1.6} strokeLinejoin="round" />)}
      {pts.map((p, i) => {
        const sel = selected === p.at;
        const label = `${periodName(p.at, period)}: ${fmtOne(card.key, p.value)}${p.level ? ` (${LEVEL_LABEL[p.level]})` : ""}${p.partial ? tr("governance.dora.in_corso_2") : ""}`;
        return (
          <g
            key={p.at}
            role="button"
            tabIndex={0}
            aria-label={label}
            className="cursor-pointer outline-none [&:focus-visible>.ring]:stroke-accent"
            onClick={() => onPick(p.at)}
            onKeyDown={(e) => {
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                onPick(p.at);
              }
            }}
          >
            <title>{label}</title>
            <rect className="ring" x={x(i) - 9} y={PAD.t - 4} width={18} height={ih + 8} fill="transparent" strokeWidth={1.5} stroke="transparent" />
            {p.value != null &&
              (bars ? (
                <rect
                  x={x(i) - bw / 2}
                  y={y(p.value)}
                  width={bw}
                  height={Math.max(1, PAD.t + ih - y(p.value))}
                  className={sel ? "fill-accent" : p.partial ? "fill-hair stroke-fg" : "fill-fg"}
                  strokeWidth={p.partial ? 1 : 0}
                />
              ) : (
                <circle cx={x(i)} cy={y(p.value)} r={sel ? 4.5 : 3} className={sel ? "fill-accent stroke-accent" : p.partial ? "fill-bg stroke-fg" : "fill-fg stroke-fg"} strokeWidth={1.4} />
              ))}
            {sel && <line x1={x(i)} x2={x(i)} y1={PAD.t} y2={PAD.t + ih} className="stroke-accent" strokeWidth={1} strokeDasharray="2 2" />}
          </g>
        );
      })}
      {labelIdx.map((i) => (
        <text key={i} x={x(i)} y={H - 5} textAnchor={i === 0 ? "start" : i === pts.length - 1 ? "end" : "middle"} className="fill-muted" fontSize="9.5">
          {periodName(pts[i].at, period, true)}
        </text>
      ))}
    </svg>
  );
}

/** Toglie dai grafici i periodi vuoti prima del primo dato (almeno quattro punti restano). */
function trimSeries(cards: MetricCard[], period: Period): MetricCard[] {
  const n = cards[0]?.series.length ?? 0;
  const last = { week: 13, month: 12, quarter: 8 }[period]; // periodi mostrati
  const floor = Math.max(0, n - last);
  let first = n;
  for (const c of cards)
    c.series.forEach((p, i) => {
      if (i < first && p.value != null && (c.key !== "deploy_frequency" || p.value > 0)) first = i;
    });
  const from = Math.max(floor, Math.min(first, n - 4));
  return from === 0 ? cards : cards.map((c) => ({ ...c, series: c.series.slice(from) }));
}

// ---------------------------------------------------------------------
// Numeri

function MetricBlock({ card, period, selected, onPick, openIncidents }: { card: MetricCard; period: Period; selected: string | null; onPick: (at: string, metric: MetricKey) => void; openIncidents?: number }) {
  const f = fmt(card.key, card.value);
  const prevLabel = PERIODS.find((p) => p.key === period)!.prev;
  return (
    <section className="flex min-w-0 flex-col gap-2 border-t border-hair pt-3" aria-label={card.name}>
      <div className="flex items-baseline justify-between gap-2">
        <h3 className="label m-0">{card.name}</h3>
        <LevelMark level={card.level} compact />
      </div>
      <p className="m-0 flex items-baseline gap-1.5">
        <span className={`font-mono text-[38px] leading-none font-semibold tabular ${card.value == null ? "text-muted" : ""}`}>{f.num}</span>
        <span className="text-[13px] text-muted">{f.unit}</span>
      </p>
      <Trend card={card} prevLabel={prevLabel} />
      {card.key === "mttr_hours" && card.value == null && (
        <p className="m-0 text-[12px] text-fg">{tr("governance.dora.nessun_incidente_risolto_nel_periodo")}{openIncidents ? tr("governance.dora.il_tempo_si_calcola_alla", { openIncidents, p: tr("governance.dora.aperto") }) : ""}.
        </p>
      )}
      <p className="m-0 text-[12px] text-muted">{card.hint}</p>
      <Chart card={card} period={period} selected={selected} onPick={(at) => onPick(at, card.key)} />
    </section>
  );
}

// ---------------------------------------------------------------------
// Confronto

function CompareTable({ rows, scope, onOpen, narrow }: { rows: Dashboard["compare"]; scope: string; onOpen: (id: string) => void; narrow: boolean }) {
  const subject = SCOPES.find((s) => s.key === scope)?.label.toLowerCase() ?? "";
  if (rows.length === 0) return <p className="m-0 text-muted">{tr("governance.dora.nessun_dato_per_nell_ultimo", { subject })}</p>;
  if (narrow)
    return (
      <ul className="m-0 flex list-none flex-col p-0">
        {rows.map((r) => (
          <li key={r.id} className="flex flex-col gap-1.5 border-b border-hair py-2.5">
            <div className="flex items-baseline justify-between gap-3">
              <button type="button" className="min-w-0 truncate bg-transparent p-0 text-left font-medium text-accent underline" onClick={() => onOpen(r.id)}>
                {r.name}
              </button>
              <LevelMark level={r.level} compact />
            </div>
            <dl className="m-0 grid grid-cols-2 gap-x-3 gap-y-0.5 text-[12px]">
              {METRIC_KEYS.map((k) => (
                <div key={k} className="flex justify-between gap-2">
                  <dt className="text-muted">{METRIC_SHORT[k]}</dt>
                  <dd className="m-0 font-mono tabular">{fmtOne(k, r.values[k])}</dd>
                </div>
              ))}
            </dl>
          </li>
        ))}
      </ul>
    );
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[720px] border-collapse">
        <thead>
          <tr className="label h-9 text-left">
            <th scope="col" className="border-b-2 border-rule pr-3">{SCOPES.find((s) => s.key === scope)?.label}</th>
            {METRIC_KEYS.map((k) => (
              <th key={k} scope="col" className="border-b-2 border-rule px-3 text-right">{METRIC_SHORT[k]}</th>
            ))}
            <th scope="col" className="border-b-2 border-rule pl-3">{tr("governance.risks.livello")}</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.id} className="border-b border-hair">
              <td className="py-2 pr-3">
                <button type="button" className="bg-transparent p-0 text-left font-medium text-accent underline" onClick={() => onOpen(r.id)}>
                  {r.name}
                </button>
              </td>
              {METRIC_KEYS.map((k) => (
                <td key={k} className="px-3 py-2 text-right">
                  <span className="font-mono tabular">{fmtOne(k, r.values[k])}</span>
                  {r.levels[k] && <span className={`ml-1.5 text-[11px] ${LEVEL_TONE[r.levels[k]!]}`}>{LEVEL_LABEL[r.levels[k]!]}</span>}
                </td>
              ))}
              <td className="py-2 pl-3"><LevelMark level={r.level} /></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

// ---------------------------------------------------------------------
// Approfondimento: i rilasci e gli incidenti che compongono i numeri

const SOURCE: Record<string, string> = { get commit() { return tr("governance.dora.primo_commit"); }, get check_run() { return tr("governance.dora.primo_controllo_del_commit"); }, get change() { return tr("governance.dora.creazione_del_change"); }, get request() { return tr("governance.dora.creazione_della_richiesta"); } };

function DrillPanel({ drill, period, metric, narrow }: { drill: Drill; period: Period; metric: MetricKey | null; narrow: boolean }) {
  const v = drill.values;
  const show = (k: MetricKey) => !metric || metric === k;
  return (
    <section className="flex flex-col gap-4" aria-label={tr("governance.dora.rilasci_e_incidenti_del_periodo")}>
      <SectionTitle aside={<span className="font-mono text-[11.5px] text-muted">{periodName(drill.start, period)}{v.partial ? tr("governance.dora.in_corso") : ""}</span>}>{tr("governance.dora.cosa_compone_i_numeri")}</SectionTitle>
      <p className="m-0 max-w-3xl text-[13px] text-muted">
        <Trans
          k="governance.dora.drilldown_riepilogo"
          params={{ name: drill.subject.name }}
          slots={{
            deploys: <span className="tabular text-fg">{v.deploys}</span>,
            attempts: <span className="tabular text-fg">{v.attempts}</span>,
            failed: <span className="tabular text-fg">{v.failed}</span>,
            leadN: <span className="tabular text-fg">{v.lead_time_n}</span>,
            mttrN: <span className="tabular text-fg">{v.mttr_n}</span>,
          }}
        />
      </p>
      {(show("deploy_frequency") || show("lead_time_days") || show("change_failure_rate")) && (
        <div className="flex flex-col gap-1">
          <h4 className="label m-0">{tr("governance.dora.rilasci_in_produzione")}</h4>
          {drill.deploys.length === 0 && <p className="m-0 text-muted">{tr("governance.dora.nessun_rilascio_in_questo_periodo")}</p>}
          {narrow ? (
            <ul className="m-0 flex list-none flex-col p-0">
              {drill.deploys.map((d) => (
                <li key={d.id} className="flex flex-col gap-0.5 border-b border-hair py-2">
                  <div className="flex items-baseline justify-between gap-2">
                    <span className="font-medium">{d.service} <span className="font-mono text-[12px]">{d.version}</span></span>
                    <span className={`text-[12px] font-semibold ${d.failed ? "text-danger" : "text-ok"}`}>{d.failed ? tr("governance.dora.fallito_2") : d.status === "succeeded" ? tr("governance.dora.riuscito_2") : d.status}</span>
                  </div>
                  <span className="text-[12px] text-muted">{dateTime(d.at)}{d.lead_days != null ? tr("governance.dora.lead_time_2", { fmtOne: fmtOne("lead_time_days", d.lead_days) }) : ""}</span>
                  {d.reasons.map((r, i) => (
                    <span key={i} className="text-[12px] text-danger">{r.text}</span>
                  ))}
                </li>
              ))}
            </ul>
          ) : (
            drill.deploys.length > 0 && (
              <div className="overflow-x-auto">
                <table className="w-full min-w-[720px] border-collapse">
                  <thead>
                    <tr className="label h-9 text-left">
                      <th scope="col" className="border-b-2 border-rule pr-3">{tr("governance.dora.quando")}</th>
                      <th scope="col" className="border-b-2 border-rule px-3">{tr("governance.dora.servizio_e_versione")}</th>
                      <th scope="col" className="border-b-2 border-rule px-3">{tr("governance.devsecops.change")}</th>
                      <th scope="col" className="border-b-2 border-rule px-3 text-right">{tr("governance.dora.lead_time")}</th>
                      <th scope="col" className="border-b-2 border-rule pl-3">{tr("governance.dora.esito")}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {drill.deploys.map((d) => (
                      <tr key={d.id} className="border-b border-hair align-top">
                        <td className="py-2 pr-3 font-mono text-[12px] whitespace-nowrap">{dateTime(d.at)}</td>
                        <td className="px-3 py-2">
                          {d.release_url ? (
                            <a href={d.release_url} target="_blank" rel="noreferrer" className="font-medium">{d.service}</a>
                          ) : (
                            <span className="font-medium">{d.service}</span>
                          )}{" "}
                          <span className="font-mono text-[12px]">{d.version}</span>
                        </td>
                        <td className="px-3 py-2 font-mono text-[12px]">{d.change ?? "—"}</td>
                        <td className="px-3 py-2 text-right">
                          <span className="font-mono tabular">{d.lead_days != null ? fmtOne("lead_time_days", d.lead_days) : "—"}</span>
                          {d.lead_source && <span className="block text-[11px] text-muted">{tr("governance.dora.da", { p: SOURCE[d.lead_source] })}</span>}
                        </td>
                        <td className="py-2 pl-3">
                          <span className={`font-semibold ${d.failed ? "text-danger" : "text-ok"}`}>{d.failed ? tr("governance.dora.fallito") : d.status === "succeeded" ? tr("governance.dora.riuscito") : d.status}</span>
                          {d.reasons.map((r, i) => (
                            <span key={i} className="block text-[12px] text-muted">{r.text}</span>
                          ))}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )
          )}
        </div>
      )}
      {show("mttr_hours") && (
        <div className="flex flex-col gap-1">
          <h4 className="label m-0">{tr("governance.dora.incidenti_collegati")}</h4>
          {drill.incidents.length === 0 && <p className="m-0 text-muted">{tr("governance.dora.nessun_incidente_collegato_in_questo")}</p>}
          <ul className="m-0 flex list-none flex-col p-0">
            {drill.incidents.map((i) => (
              <li key={i.id} className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5 border-b border-hair py-2">
                <Link to="/e/$key" params={{ key: i.key }} className="font-mono text-[12px]">{i.key}</Link>
                <span className="font-medium">{i.title}</span>
                <span className="text-[12px] text-muted">{i.applications.join(", ")}</span>
                <span className="ml-auto text-[12px] tabular">
                  {dateTime(i.detected_at)} →{" "}
                  {i.resolved_at ? (
                    <>
                      {dateTime(i.resolved_at)} <span className="font-semibold">({fmtOne("mttr_hours", i.hours)})</span>
                    </>
                  ) : (
                    <span className="text-danger">{tr("governance.dora.non_ancora_ripristinato")}</span>
                  )}
                </span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </section>
  );
}

// ---------------------------------------------------------------------
// Soglie e criteri, con l'anteprima

type Draft = {
  window: string;
  words: string;
  start: DoraSettings["lead_time_start"];
  factDays: string;
  th: Record<MetricKey, { elite: string; high: string; medium: string }>;
};

function toDraft(s: DoraSettings): Draft {
  const th = {} as Draft["th"];
  const str = (v: number) => String(v).replace(".", ",");
  for (const k of METRIC_KEYS) th[k] = { elite: str(s.thresholds[k].elite), high: str(s.thresholds[k].high), medium: str(s.thresholds[k].medium) };
  return { window: String(s.failure_window_hours), words: s.hotfix_words.join(", "), start: s.lead_time_start, factDays: String(s.fact_days), th };
}

const num = (s: string) => Number(s.replace(",", ".").trim());

function fromDraft(d: Draft): { body: Omit<DoraSettings, "updated_at" | "default"> | null; error: string | null } {
  const w = num(d.window);
  const fd = num(d.factDays);
  if (!Number.isInteger(w) || w < 1 || w > 720) return { body: null, error: tr("governance.dora.la_finestra_va_da_1") };
  if (!Number.isInteger(fd) || fd < 7 || fd > 365) return { body: null, error: tr("governance.dora.i_giorni_dei_fatti_vanno") };
  const th = {} as Record<MetricKey, Ladder>;
  for (const k of METRIC_KEYS) {
    const l = { elite: num(d.th[k].elite), high: num(d.th[k].high), medium: num(d.th[k].medium) };
    if ([l.elite, l.high, l.medium].some((x) => !Number.isFinite(x) || x < 0)) return { body: null, error: tr("governance.dora.servono_numeri_positivi", { p: METRIC_SHORT[k] }) };
    const ok = k === "deploy_frequency" ? l.elite > l.high && l.high > l.medium && l.medium > 0 : l.elite < l.high && l.high < l.medium;
    if (!ok)
      return { body: null, error: k === "deploy_frequency" ? tr("governance.dora.frequenza_le_soglie_devono_scendere") : tr("governance.dora.le_soglie_devono_salire_da", { p: METRIC_SHORT[k] }) };
    th[k] = l;
  }
  return {
    body: {
      failure_window_hours: w,
      hotfix_words: d.words.split(",").map((x) => x.trim()).filter(Boolean),
      lead_time_start: d.start,
      thresholds: th,
      fact_days: fd,
    },
    error: null,
  };
}

const START_HELP: Record<DoraSettings["lead_time_start"], string> = {
  get auto() { return tr("governance.dora.il_primo_commit"); },
  get commit() { return tr("governance.dora.il_primo_commit_solo"); },
  get check_run() { return tr("governance.dora.il_primo_controllo_della_pipeline"); },
  get change() { return tr("governance.dora.la_creazione_del_change_in"); },
  get request() { return tr("governance.dora.la_creazione_della_richiesta_collegata"); },
};

function NumField({ label, value, onChange, width = "w-[88px]" }: { label: string; value: string; onChange: (v: string) => void; width?: string }) {
  return (
    <label className="flex flex-col gap-1">
      <span className="label">{label}</span>
      <input className={`field ${width} text-right font-mono`} inputMode="decimal" value={value} onChange={(e) => onChange(e.target.value)} />
    </label>
  );
}

function SettingsPanel({ settings, period, onSaved }: { settings: DoraSettings; period: Period; onSaved: () => void }) {
  const [draft, setDraft] = useState<Draft>(() => toDraft(settings));
  const parsed = useMemo(() => fromDraft(draft), [draft]);
  const [debounced, setDebounced] = useState(parsed.body);
  useEffect(() => {
    const t = setTimeout(() => setDebounced(parsed.body), 450);
    return () => clearTimeout(t);
  }, [parsed.body]);
  const preview = useQuery({
    queryKey: ["dora-preview", period, debounced],
    queryFn: () => doraApi.preview(period, debounced!),
    enabled: !!debounced,
    placeholderData: (p) => p,
  });
  const save = useMutation({ mutationFn: () => doraApi.saveSettings(parsed.body!), onSuccess: onSaved });
  const set = (k: MetricKey, f: keyof Ladder, v: string) => setDraft((d) => ({ ...d, th: { ...d.th, [k]: { ...d.th[k], [f]: v } } }));
  const dirty = JSON.stringify(draft) !== JSON.stringify(toDraft(settings));
  const items = preview.data?.items ?? [];
  const changed = items.filter((r) => r.changed || METRIC_KEYS.some((k) => r.current_levels[k] !== r.proposed_levels[k]));
  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-3">
        <h4 className="label m-0">{tr("governance.dora.livelli_dove_cade_ogni_soglia")}</h4>
        <p className="m-0 max-w-3xl text-[13px] text-muted">{tr("governance.dora.un_valore_e_a_un")}</p>
        {METRIC_KEYS.map((k) => {
          const unit = k === "deploy_frequency" ? tr("governance.dora.a_settimana") : k === "lead_time_days" ? tr("governance.dora.giorni") : k === "change_failure_rate" ? "%" : tr("governance.dora.ore");
          const op = k === "deploy_frequency" ? tr("governance.dora.da_2") : tr("governance.dora.fino_a");
          return (
            <fieldset key={k} className="m-0 flex min-w-0 flex-wrap items-end gap-x-4 gap-y-2 border-0 border-t border-hair p-0 pt-3">
              <legend className="sr-only">{METRIC_SHORT[k]}</legend>
              <span className="w-full text-[13px] font-semibold md:w-40">{METRIC_SHORT[k]} <span className="font-normal text-muted">({unit})</span></span>
              <NumField label={tr("governance.dora.elite_2", { op })} value={draft.th[k].elite} onChange={(v) => set(k, "elite", v)} />
              <NumField label={tr("governance.dora.alto_2", { op })} value={draft.th[k].high} onChange={(v) => set(k, "high", v)} />
              <NumField label={tr("governance.dora.medio_2", { op })} value={draft.th[k].medium} onChange={(v) => set(k, "medium", v)} />
            </fieldset>
          );
        })}
      </div>
      <div className="flex flex-col gap-3">
        <h4 className="label m-0">{tr("governance.dora.quando_un_rilascio_e_fallito")}</h4>
        <div className="flex flex-wrap items-end gap-x-4 gap-y-3">
          <NumField label={tr("governance.dora.finestra_ore")} value={draft.window} onChange={(v) => setDraft((d) => ({ ...d, window: v }))} />
          <label className="flex min-w-0 flex-[1_1_260px] flex-col gap-1">
            <span className="label">{tr("governance.dora.parole_di_hotfix_e_rollback")}</span>
            <input className="field" value={draft.words} onChange={(e) => setDraft((d) => ({ ...d, words: e.target.value }))} /* i18n-ok: esempio di parole */ placeholder="hotfix, rollback, revert" />
          </label>
        </div>
        <p className="m-0 max-w-3xl text-[12.5px] text-muted">{tr("governance.dora.un_rilascio_in_produzione_e")}</p>
      </div>
      <div className="flex flex-col gap-3">
        <h4 className="label m-0">{tr("governance.dora.da_dove_parte_il_lead")}</h4>
        <div className="flex flex-wrap items-end gap-x-4 gap-y-3">
          <label className="flex flex-col gap-1">
            <span className="label">{tr("governance.dora.inizio_della_modifica")}</span>
            <Select className="field" value={draft.start} onChange={(e) => setDraft((d) => ({ ...d, start: e.target.value as Draft["start"] }))}>
              <option value="auto">{tr("governance.dora.automatico")}</option>
              <option value="commit">{tr("governance.dora.primo_commit_2")}</option>
              <option value="check_run">{tr("governance.dora.primo_controllo_del_commit_2")}</option>
              <option value="change">{tr("governance.dora.creazione_del_change_2")}</option>
              <option value="request">{tr("governance.dora.creazione_della_richiesta_2")}</option>
            </Select>
          </label>
          <NumField label={tr("governance.dora.giorni_per_i_fatti")} value={draft.factDays} onChange={(v) => setDraft((d) => ({ ...d, factDays: v }))} />
        </div>
        <p className="m-0 max-w-3xl text-[12.5px] text-muted">
          <Trans
            k="governance.dora.start_help_nota"
            params={{ help: START_HELP[draft.start] }}
            slots={{
              a: /* i18n-ok: nome di un campo dei fatti */ <span className="font-mono">frequenza_rilascio</span>,
              b: /* i18n-ok: nome di un campo dei fatti */ <span className="font-mono">lead_time_giorni</span>,
              c: /* i18n-ok: nome di un campo dei fatti */ <span className="font-mono">change_failure_rate</span>,
              d: /* i18n-ok: nome di un campo dei fatti */ <span className="font-mono">mttr_ore</span>,
            }}
          />
        </p>
      </div>
      <div className="flex flex-col gap-2" aria-live="polite">
        <h4 className="label m-0">{tr("governance.dora.anteprima_sull_ultimo_concluso", { p: PERIODS.find((p) => p.key === period)!.label.toLowerCase() })}</h4>
        {parsed.error && <p className="m-0 text-danger">{parsed.error}</p>}
        {!parsed.error && preview.isPending && <p className="m-0 text-muted">{tr("governance.compliance.calcolo")}</p>}
        <ErrorNote error={preview.error} />
        {!parsed.error && preview.data && changed.length === 0 && <p className="m-0 text-muted">{tr("governance.dora.con_queste_impostazioni_nessun_livello")}</p>}
        {!parsed.error && changed.length > 0 && (
          <ul className="m-0 flex list-none flex-col p-0">
            {changed.slice(0, 20).map((r) => (
              <PreviewLine key={`${r.scope}-${r.id}`} r={r} />
            ))}
          </ul>
        )}
        {!parsed.error && changed.length > 20 && <p className="m-0 text-[12px] text-muted">{tr("governance.dora.e_altri", { p: changed.length - 20 })}</p>}
      </div>
      <div className="flex flex-wrap items-center gap-3">
        <button className="btn btn-primary" disabled={!parsed.body || !dirty || save.isPending} onClick={() => save.mutate()}>{tr("governance.compliance.salva_e_ricalcola")}</button>
        <button className="btn" type="button" onClick={() => setDraft(toDraft({ ...settings, thresholds: DEFAULT_THRESHOLDS, failure_window_hours: 24, hotfix_words: ["hotfix", "rollback", "revert"], lead_time_start: "auto", fact_days: 30 }))}>{tr("governance.dora.ripristina_le_predefinite")}</button>
        {save.isSuccess && !dirty && <span className="text-ok">{tr("governance.dora.salvato_i_punti_sono_stati")}</span>}
      </div>
      <ErrorNote error={save.error} />
    </div>
  );
}

function PreviewLine({ r }: { r: PreviewRow }) {
  const tag = r.scope === "tenant" ? tr("governance.dora.tag_tenant") : r.scope === "team" ? tr("governance.dora.tag_team") : tr("governance.dora.tag_application");
  const diffs = METRIC_KEYS.filter((k) => r.current_levels[k] !== r.proposed_levels[k]);
  return (
    <li className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5 border-b border-hair py-2">
      <span className="font-medium">{r.name}</span>
      <span className="text-[11px] text-muted">{tag}</span>
      <span className="ml-auto flex items-center gap-2 text-[12.5px]">
        {r.current_level !== r.proposed_level && (
          <>
            <LevelMark level={r.current_level} compact />
            <span aria-hidden="true">→</span>
            <span className="sr-only">{tr("governance.dora.diventa")}</span>
            <LevelMark level={r.proposed_level} compact />
          </>
        )}
      </span>
      <span className="w-full text-[12px] text-muted">
        {diffs.map((k) => `${METRIC_SHORT[k]}: ${r.current_levels[k] ? LEVEL_LABEL[r.current_levels[k]!] : tr("governance.dora.senza_dati")} → ${r.proposed_levels[k] ? LEVEL_LABEL[r.proposed_levels[k]!] : tr("governance.dora.senza_dati")}`).join(" · ")}
      </span>
    </li>
  );
}

// ---------------------------------------------------------------------
// Pagina

export type DoraSearch = { periodo?: Period; ambito?: Scope; soggetto?: string; confronto?: Exclude<Scope, "tenant"> };

export function DoraPage({ search, onSearch }: { search: DoraSearch; onSearch: (s: DoraSearch) => void }) {
  const qc = useQueryClient();
  const narrow = useNarrow();
  const period: Period = search.periodo ?? "month";
  const scope: Scope = search.ambito ?? "tenant";
  const compare: Exclude<Scope, "tenant"> | "" = scope === "tenant" ? (search.confronto ?? "team") : "";
  const dash = useQuery({
    queryKey: ["dora", period, scope, search.soggetto ?? "", compare],
    queryFn: () => doraApi.dashboard({ period, scope, subject: search.soggetto, compare }),
    placeholderData: (p) => p,
  });
  const [pick, setPick] = useState<{ at: string; metric: MetricKey | null } | null>(null);
  useEffect(() => setPick(null), [period, scope, search.soggetto]);
  const drill = useQuery({
    queryKey: ["dora-drill", period, scope, search.soggetto ?? "", pick?.at],
    queryFn: () => doraApi.drill(period, scope, search.soggetto ?? dash.data?.subject.id, pick!.at),
    enabled: !!pick && !!dash.data,
  });
  const drillRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (pick) drillRef.current?.scrollIntoView({ behavior: "smooth", block: "nearest" });
  }, [pick, drill.data]);
  const recompute = useMutation({ mutationFn: doraApi.recompute, onSuccess: () => qc.invalidateQueries({ queryKey: ["dora"] }) });
  const d = dash.data;
  const set = (s: Partial<DoraSearch>) => onSearch({ periodo: period, ambito: scope, soggetto: search.soggetto, confronto: search.confronto, ...s });
  return (
    <>
      <PageHeader
        path={crumbs("governance", "devsecops", "metrics")}
        title={tr("governance.dora.metriche_di_consegna")}
        actions={
          d?.can_configure ? (
            <button className="btn" disabled={recompute.isPending} onClick={() => recompute.mutate()}>
              {recompute.isPending ? tr("governance.compliance.calcolo") : tr("governance.compliance.ricalcola_ora")}
            </button>
          ) : undefined
        }
      />
      <div className="flex flex-col gap-8 px-4 pb-10 md:px-7">
        <p className="m-0 max-w-3xl text-muted">{tr("governance.dora.le_quattro_misure_dora_dai")}</p>
        <div className="flex flex-wrap items-end gap-x-4 gap-y-3">
          <div role="group" aria-label={tr("governance.dora.periodo")} className="flex">
            {PERIODS.map((p, i) => (
              <button
                key={p.key}
                type="button"
                aria-pressed={period === p.key}
                onClick={() => set({ periodo: p.key })}
                className={`border border-rule px-3 py-2 font-semibold ${i > 0 ? "-ml-px" : ""} ${period === p.key ? "bg-fg text-bg" : "bg-transparent text-fg"}`}
              >
                {p.label}
              </button>
            ))}
          </div>
          <label className="flex flex-col gap-1">
            <span className="label">{tr("governance.requirements.ambito")}</span>
            <Select className="field" value={scope} onChange={(e) => onSearch({ periodo: period, ambito: e.target.value as Scope })}>
              {SCOPES.map((s) => (
                <option key={s.key} value={s.key}>{s.label}</option>
              ))}
            </Select>
          </label>
          {scope !== "tenant" && (
            <label className="flex min-w-0 flex-[1_1_220px] flex-col gap-1 md:max-w-sm">
              <span className="label">{SCOPES.find((s) => s.key === scope)!.label}</span>
              <Select className="field" value={d?.subject.id ?? search.soggetto ?? ""} onChange={(e) => set({ soggetto: e.target.value })}>
                {d && !d.subjects.some((s) => s.id === d.subject.id) && <option value={d.subject.id}>{d.subject.name}</option>}
                {d?.subjects.map((s) => (
                  <option key={s.id} value={s.id}>{s.name}</option>
                ))}
              </Select>
            </label>
          )}
          {scope === "tenant" && (
            <label className="flex flex-col gap-1">
              <span className="label">{tr("governance.dora.confronta_per")}</span>
              <Select className="field" value={compare} onChange={(e) => set({ confronto: e.target.value as Exclude<Scope, "tenant"> })}>
                <option value="team">{tr("governance.requirements.team")}</option>
                <option value="application">{tr("governance.requirements.applicazione")}</option>
                <option value="asset">{tr("governance.devsecops.asset")}</option>
                <option value="service">{tr("governance.devsecops.servizio")}</option>
              </Select>
            </label>
          )}
        </div>
        <ErrorNote error={dash.error ?? recompute.error} />
        {dash.isPending && <Loading className="m-0 text-muted" />}
        {d && !d.computed && (
          <p className="m-0 max-w-2xl border-l-2 border-rule pl-3">
            {d.can_configure ? (
              <Trans k="governance.dora.metriche_non_calcolate_oppure" slots={{ now: <button type="button" className="bg-transparent p-0 text-accent underline" onClick={() => recompute.mutate()}>{tr("governance.dora.calcolale_adesso")}</button> }} />
            ) : (
              tr("governance.dora.metriche_non_calcolate")
            )}{" "}
            {tr("governance.dora.servono_rilasci_in_produzione")}
          </p>
        )}
        {d && d.computed && (
          <>
            <section aria-label={tr("governance.compliance.in_sintesi")} className="flex flex-col gap-4">
              <div className="flex flex-wrap items-baseline gap-x-4 gap-y-1 border-b-2 border-rule pb-2">
                <h2 className="m-0 text-[20px] leading-tight font-bold">{d.subject.name}</h2>
                <span className="text-[13px] text-muted">{periodName(d.bucket, period)}</span>
                <span className="ml-auto flex items-center gap-2 text-[13px]">
                  <span className="text-muted">{tr("governance.risks.livello")}</span>
                  <LevelMark level={d.level} />
                  {d.prev_level && d.prev_level !== d.level && <span className="text-[12px] text-muted">{tr("governance.dora.prima", { p: LEVEL_LABEL[d.prev_level] })}</span>}
                </span>
              </div>
              <div className="grid grid-cols-1 gap-x-8 gap-y-7 md:grid-cols-2 xl:grid-cols-4">
                {trimSeries(d.metrics, period).map((m) => (
                  <MetricBlock key={m.key} card={m} period={period} selected={pick?.at ?? null} onPick={(at, metric) => setPick({ at, metric })} openIncidents={d.open_incidents} />
                ))}
              </div>
              <p className="m-0 text-[12px] text-muted">{tr("governance.dora.l_ultimo_punto_vuoto_o")}</p>
            </section>
            {pick && (
              <div ref={drillRef}>
                {drill.isPending && <Loading className="m-0 text-muted" />}
                <ErrorNote error={drill.error} />
                {drill.data && <DrillPanel drill={drill.data} period={period} metric={pick.metric} narrow={narrow} />}
              </div>
            )}
            {scope === "tenant" && (
              <section className="flex min-w-0 flex-col gap-2" aria-label={tr("governance.dora.confronto")}>
                <SectionTitle aside={<span className="font-mono text-[11.5px] text-muted">{periodName(d.bucket, period)}</span>}>{tr("governance.dora.confronto_per", { p: SCOPES.find((s) => s.key === compare)?.label.toLowerCase() ?? "" })}</SectionTitle>
                <CompareTable
                  rows={d.compare}
                  scope={compare}
                  narrow={narrow}
                  onOpen={(id) => onSearch({ periodo: period, ambito: compare as Scope, soggetto: id })}
                />
              </section>
            )}
          </>
        )}
        {d?.can_configure && (
          <details className="border-t-2 border-rule pt-3" open={false}>
            <summary className="cursor-pointer py-1 font-semibold">{tr("governance.dora.soglie_e_criteri")}</summary>
            <div className="pt-4">
              <SettingsPanel
                key={d.settings.updated_at ?? "default"}
                settings={d.settings}
                period={period}
                onSaved={() => {
                  qc.invalidateQueries({ queryKey: ["dora"] });
                  qc.invalidateQueries({ queryKey: ["dora-drill"] });
                }}
              />
            </div>
          </details>
        )}
      </div>
    </>
  );
}

// ---------------------------------------------------------------------
// Riquadro nella pagina dell'applicazione e del team

/** I quattro numeri dell'ultimo mese concluso di un'applicazione, di un asset o di un team. */
export function DoraCard({ scope, subject }: { scope: Scope; subject: string }) {
  const q = useQuery({ queryKey: ["dora-card", scope, subject], queryFn: () => doraApi.dashboard({ period: "month", scope, subject }) });
  const d = q.data;
  const hasData = d?.metrics.some((m) => m.value != null && (m.key !== "deploy_frequency" || m.value > 0 || m.series.some((p) => (p.value ?? 0) > 0)));
  return (
    <section className="flex min-w-0 flex-col gap-3 border-y-2 border-rule py-3" aria-label={tr("governance.dora.metriche_di_consegna")}>
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <h3 className="label m-0">{tr("governance.dora.metriche_di_consegna")}</h3>
        {d && hasData && <span className="text-[12px] text-muted">{periodName(d.bucket, "month")}</span>}
        {d && hasData && (
          <span className="ml-auto">
            <LevelMark level={d.level} compact />
          </span>
        )}
      </div>
      <ErrorNote error={q.error} />
      {q.isPending && <Loading className="m-0 text-muted" />}
      {d && !hasData && <p className="m-0 text-muted">{tr("governance.dora.nessun_rilascio_in_produzione_ne")}</p>}
      {d && hasData && (
        <div className="grid grid-cols-2 gap-x-6 gap-y-4 md:grid-cols-4">
          {d.metrics.map((m) => {
            const f = fmt(m.key, m.value);
            return (
              <div key={m.key} className="flex min-w-0 flex-col gap-0.5">
                <span className="label">{METRIC_SHORT[m.key]}</span>
                <span className="flex items-baseline gap-1">
                  <span className="font-mono text-[26px] leading-none font-semibold tabular">{f.num}</span>
                  <span className="text-[12px] text-muted">{f.unit}</span>
                </span>
                <LevelMark level={m.level} compact />
              </div>
            );
          })}
        </div>
      )}
      {d && hasData && (
        <Link to="/devsecops/metriche" search={{ periodo: "month", ambito: scope, soggetto: d.subject.id }} className="text-[13px]">{tr("governance.dora.apri_le_metriche_con_i")}</Link>
      )}
    </section>
  );
}
