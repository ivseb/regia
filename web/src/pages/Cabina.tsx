import { Fragment, useEffect, useMemo, useRef, useState, type KeyboardEvent, type PointerEvent as RPointerEvent } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { budgetApi, financeApi, orgApi, portfolioApi, type Cabina, type PortfolioScenario, type RankItem, type RequestWindow } from "../api";
import { fmtNumber } from "../i18n/format";
import type { Key } from "../i18n/core";
import { useT } from "../i18n/react";
import { OptionSelect } from "../select";
import { PageHeader } from "../shell";
import { ErrorNote, date, euro, euroRange } from "../ui";
import { crumbs } from "../crumbs";
import { Field, defaultPeriod, periodOptions } from "./budget/budget-ui";
import { ScoreBar } from "./Evaluation";
import { MovePanel, type Pending } from "./Ranking";
import { RoadmapChart } from "./Roadmap";
import { ZoomSwitch, type Zoom } from "./timeline";
import { CabinaSeduta } from "./CabinaSeduta";
import { autoCut, evalCut, occupants, suggestMove, whyHere, type CutEval, type TeamStat } from "./cabina-calc";

// Cabina di portafoglio (ondata 3, proposta 9): graduatoria, budget, capacità dei team, roadmap, scenari
// e pacchetto della seduta sulla stessa pagina, con un solo carico di dati (GET /portfolio/cabina).
// La linea di taglio si sposta (trascinando o con le frecce) e tutto il resto si ricalcola nel browser.

const K = 100_000; // centesimi in un k€
const pct = (r: number) => `${fmtNumber(Math.round(r * 100))}%`;

export function CabinaPage({ unit, period }: { unit?: string; period?: string }) {
  const t = useT();
  const q = useQuery({ queryKey: ["cabina", unit ?? "", period ?? ""], queryFn: () => portfolioApi.cabina(unit, period) });
  return (
    <>
      <PageHeader path={crumbs("demand", "ranking")} title={t("lavoro.cabina.title")} />
      <div role="tablist" aria-label={t("lavoro.roadmap.view")} className="flex gap-6 border-b border-hair px-4 pt-1 md:px-7">
        <Link to="/graduatoria" role="tab" aria-selected="false" className="touch-44 pb-2.5 text-muted no-underline hover:text-fg">
          {t("budget.rank.tab.ranking")}
        </Link>
        <Link to="/roadmap" role="tab" aria-selected="false" className="touch-44 pb-2.5 text-muted no-underline hover:text-fg">
          {t("lavoro.roadmap.tabRoadmap")}
        </Link>
        <span role="tab" aria-selected="true" className="border-b-2 border-fg pb-2.5 font-semibold">
          {t("lavoro.cabina.tab")}
        </span>
      </div>
      <div className="flex flex-col gap-6 px-4 py-5 md:px-7">
        <ErrorNote error={q.error} />
        <AreaPicker unit={unit} period={period} />
        {q.data && <Body key={`${unit}|${period}`} cab={q.data} unit={unit} period={period} />}
        <CabinaSeduta />
      </div>
    </>
  );
}

function Body({ cab, unit, period }: { cab: Cabina; unit?: string; period?: string }) {
  const t = useT();
  const qc = useQueryClient();
  const items = cab.ranking.items;
  const mandatoryFirst = cab.ranking.settings.mandatory_first;
  const [override, setOverride] = useState<number | null>(null);
  const [manualCut, setManualCut] = useState<number | null>(null);
  const [team, setTeam] = useState<string | null>(null);
  const [pending, setPending] = useState<Pending | null>(null);
  const [reason, setReason] = useState("");
  const [zoom, setZoom] = useState<Zoom>("mesi");

  const budget = override ?? cab.ranking.budget_cents;
  const cut = Math.min(manualCut ?? autoCut(items, budget), items.length);
  const ev = useMemo(() => evalCut(cab, items, cut, budget), [cab, items, cut, budget]);

  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["cabina"] });
    qc.invalidateQueries({ queryKey: ["ranking"] });
    qc.invalidateQueries({ queryKey: ["roadmap"] });
  };
  const move = useMutation({
    mutationFn: (p: Pending) => portfolioApi.move(p.item.key, p.to, reason),
    onSuccess: () => {
      refresh();
      setPending(null);
      setReason("");
    },
  });
  const setBudget = (b: number | null) => {
    setOverride(b);
    setManualCut(null); // la linea torna a seguire il budget
  };
  const cutBy = (n: number) => setManualCut(Math.max(0, Math.min(items.length, cut + n)));
  const openMove = (item: RankItem, to: number) => {
    move.reset();
    setPending({ item, from: item.position, to });
  };

  const windows = items.map((i) => cab.windows[i.id]).filter(Boolean) as RequestWindow[];
  const cutAfter = items.slice(0, cut).filter((i) => cab.windows[i.id]).length;

  return (
    <>
      <BudgetPanel cab={cab} budget={budget} override={override} ev={ev} total={items.length} onBudget={setBudget} onCutBy={cutBy} unit={unit} period={period} />
      {pending && (
        <MovePanel
          pending={pending}
          reason={reason}
          setReason={setReason}
          busy={move.isPending}
          error={move.error}
          onConfirm={() => move.mutate(pending)}
          onCancel={() => {
            setPending(null);
            move.reset();
          }}
        />
      )}
      <RankList cab={cab} items={items} ev={ev} cut={cut} mandatoryFirst={mandatoryFirst} onCut={setManualCut} onTeam={setTeam} />
      <CapacityPanel cab={cab} items={items} ev={ev} cut={cut} mandatoryFirst={mandatoryFirst} team={team} onTeam={setTeam} onMove={openMove} />
      <section aria-label={t("lavoro.cabina.roadmap")} className="flex flex-col gap-2">
        <div className="flex flex-wrap items-center justify-between gap-2 border-b-2 border-rule pb-1.5">
          <h2 className="label m-0">{t("lavoro.cabina.roadmap")}</h2>
          <ZoomSwitch value={zoom} options={["settimane", "mesi", "trimestri"]} onChange={setZoom} />
        </div>
        <RoadmapChart items={windows} zoom={zoom} cutAfter={cutAfter} />
      </section>
      <Scenarios cab={cab} items={items} ev={ev} budget={budget} />
    </>
  );
}

/** Area e periodo: con un'area il budget è il residuo del suo stanziamento nel periodo. */
function AreaPicker({ unit, period }: { unit?: string; period?: string }) {
  const t = useT();
  const navigate = useNavigate();
  const units = useQuery({ queryKey: ["org", "units", false], queryFn: () => orgApi.units({}), retry: false });
  const periods = useQuery({ queryKey: ["budget", "periods"], queryFn: () => budgetApi.periods(), retry: false });
  const list = periods.data?.items ?? [];
  if (!units.data || units.data.items.length === 0) return null;
  const go = (u?: string, p?: string) => navigate({ to: "/cabina", search: { unita: u || undefined, periodo: u ? p || undefined : undefined } });
  return (
    <section aria-label={t("budget.rank.area.title")} className="flex flex-wrap items-end gap-x-6 gap-y-3">
      <Field label={t("budget.rank.area.unit")} className="w-72 max-w-full">
        <OptionSelect
          value={unit ?? ""}
          onChange={(u) => go(u, period ?? defaultPeriod(list)?.id)}
          noneLabel={t("budget.rank.area.all")}
          options={units.data.items.filter((u) => !u.archived).map((u) => ({ value: u.id, label: u.path, detail: u.code ?? undefined }))}
        />
      </Field>
      {unit && (
        <Field label={t("budget.rank.area.period")} className="w-56 max-w-full">
          <OptionSelect value={period ?? ""} onChange={(p) => go(unit, p)} noneLabel={t("budget.rank.area.anyPeriod")} options={periodOptions(list)} />
        </Field>
      )}
    </section>
  );
}

function Stat({ label, value, tone }: { label: string; value: string; tone?: "bad" | "muted" }) {
  return (
    <div className="flex flex-col">
      <dt className="label text-muted">{label}</dt>
      <dd className={`m-0 font-mono text-[16px] font-semibold ${tone === "bad" ? "text-danger" : tone === "muted" ? "text-muted" : ""}`}>{value}</dd>
    </div>
  );
}

function BudgetPanel(props: {
  cab: Cabina;
  budget: number | null;
  override: number | null;
  ev: CutEval;
  total: number;
  onBudget: (b: number | null) => void;
  onCutBy: (n: number) => void;
  unit?: string;
  period?: string;
}) {
  const t = useT();
  const qc = useQueryClient();
  const { cab, budget, ev } = props;
  const pl = cab.plafond;
  const [text, setText] = useState(budget != null ? fmtNumber(budget / K, { maximumFractionDigits: 0 }) : "");
  useEffect(() => setText(budget != null ? fmtNumber(budget / K, { maximumFractionDigits: 0 }) : ""), [budget]);
  const link = useMutation({
    mutationFn: (id: string) => financeApi.saveSettings({ portfolio_fiscal_year_id: id }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["cabina"] });
      qc.invalidateQueries({ queryKey: ["ranking"] });
      qc.invalidateQueries({ queryKey: ["finance"] });
    },
  });
  const bump = (kEur: number) => props.onBudget(Math.max(0, (budget ?? 0) + kEur * K));
  const worst = ev.overloaded.slice().sort((a, b) => b.peak - a.peak)[0];
  const origin = props.override != null ? "simulation" : cab.budget_origin;
  const source =
    origin === "allocation" && cab.ranking.scope
      ? t("lavoro.cabina.src.allocation", { unit: cab.ranking.scope.unit, period: cab.ranking.scope.period ?? "", amount: euro(cab.ranking.scope.residual_cents) })
      : origin === "fiscal_year" && pl
        ? t("lavoro.cabina.src.fiscalYear", { name: pl.name })
        : origin === "plafond" && pl
          ? t("lavoro.cabina.src.plafond", { name: pl.name, amount: euro(pl.residual_cents) })
          : origin === "manual"
            ? t("lavoro.cabina.src.manual")
            : origin === "simulation"
              ? t("lavoro.cabina.src.simulation")
              : t("lavoro.cabina.src.none");
  return (
    <section aria-label={t("lavoro.cabina.budget")} className="flex flex-col gap-3 border-b-2 border-rule pb-4">
      <div className="flex flex-wrap items-end gap-x-8 gap-y-3">
        <div className="flex flex-col gap-1">
          <label htmlFor="cabina-budget" className="label">{t("lavoro.cabina.budget")}</label>
          <div className="flex flex-wrap items-center gap-2">
            <button type="button" className="btn touch-44" aria-label={t("lavoro.cabina.budgetLess", { amount: 10 })} onClick={() => bump(-10)}>
              −10
            </button>
            <span className="flex items-baseline gap-1.5 border-b-[1.5px] border-rule">
              <input
                id="cabina-budget"
                className="w-[8ch] border-0 bg-transparent p-0 font-mono text-[30px] leading-tight font-bold tracking-tight text-fg tabular outline-none"
                inputMode="numeric"
                placeholder="—"
                value={text}
                onChange={(e) => {
                  setText(e.target.value);
                  const v = e.target.value.replace(/\./g, "").replace(",", ".").trim();
                  if (v === "") props.onBudget(null);
                  else if (!Number.isNaN(Number(v))) props.onBudget(Math.max(0, Math.round(Number(v) * K)));
                }}
              />
              <span className="font-mono text-[15px] text-muted">k€</span>
            </span>
            <button type="button" className="btn touch-44" aria-label={t("lavoro.cabina.budgetMore", { amount: 10 })} onClick={() => bump(10)}>
              +10
            </button>
            <button type="button" className="btn touch-44" aria-label={t("lavoro.cabina.budgetMore", { amount: 100 })} onClick={() => bump(100)}>
              +100
            </button>
          </div>
        </div>
        <dl className="m-0 flex flex-wrap gap-x-6 gap-y-2 tabular" aria-live="polite">
          <Stat label={t("lavoro.cabina.stat.inside")} value={t("lavoro.cabina.stat.insideOf", { n: ev.inside.length, total: props.total })} />
          <Stat label={t("lavoro.cabina.stat.committed")} value={euro(ev.committed)} />
          <Stat
            label={t("lavoro.cabina.stat.residual")}
            value={ev.residual == null ? "—" : euro(ev.residual)}
            tone={ev.residual != null && ev.residual < 0 ? "bad" : undefined}
          />
          <Stat
            label={t("lavoro.cabina.stat.overloaded")}
            value={ev.overloaded.length === 0 ? t("lavoro.cabina.stat.none") : `${ev.overloaded.length} · ${worst.name} ${pct(worst.peak)}`}
            tone={ev.overloaded.length > 0 ? "bad" : "muted"}
          />
          <Stat label={t("lavoro.cabina.stat.end")} value={ev.end ? date(ev.end) : "—"} tone={ev.end ? undefined : "muted"} />
        </dl>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-[12.5px] text-muted" id="cabina-origine">
          {source}
        </span>
        {origin === "plafond" && pl && cab.ranking.can_admin && (
          <button type="button" className="touch-44 text-accent underline" disabled={link.isPending} onClick={() => link.mutate(pl.id)}>
            {t("lavoro.cabina.src.link")}
          </button>
        )}
        {pl && budget !== pl.residual_cents && (
          <button type="button" className="touch-44 text-accent underline" onClick={() => props.onBudget(pl.residual_cents)}>
            {t("lavoro.cabina.src.usePlafond", { amount: euro(pl.residual_cents) })}
          </button>
        )}
        {props.override != null && (
          <button type="button" className="touch-44 text-accent underline" onClick={() => props.onBudget(null)}>
            {cab.ranking.budget_cents != null ? t("budget.rank.backTo", { amount: euro(cab.ranking.budget_cents) }) : t("common.cancel")}
          </button>
        )}
      </div>
      {pl && (
        <p className="m-0 text-[12.5px] text-muted" data-plafond>
          {t("lavoro.cabina.plafond", { name: pl.name, envelope: euro(pl.envelope_cents), allocated: euro(pl.allocated_cents), residual: euro(pl.residual_cents) })}
        </p>
      )}
      <ErrorNote error={link.error} />
      <div className="flex flex-wrap items-center gap-2">
        <span className="label">{t("lavoro.cabina.cutControl")}</span>
        <button type="button" className="btn touch-44" aria-label={t("lavoro.cabina.cutUp")} onClick={() => props.onCutBy(-1)}>
          ↑
        </button>
        <span className="font-mono text-[13px] tabular" aria-live="polite">
          {t("lavoro.cabina.cutAfter", { n: ev.cut })}
        </span>
        <button type="button" className="btn touch-44" aria-label={t("lavoro.cabina.cutDown")} onClick={() => props.onCutBy(1)}>
          ↓
        </button>
        <a className="ml-auto text-[12.5px] text-accent underline" href={portfolioApi.rankingCsvUrl(budget, undefined, undefined)}>
          {t("lavoro.cabina.csv")}
        </a>
      </div>
    </section>
  );
}

function Warn() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true" className="shrink-0">
      <path d="M8 1.5 15 14H1z" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round" />
      <path d="M8 6v4M8 11.6v.8" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
    </svg>
  );
}

function RankList(props: {
  cab: Cabina;
  items: RankItem[];
  ev: CutEval;
  cut: number;
  mandatoryFirst: boolean;
  onCut: (n: number) => void;
  onTeam: (id: string) => void;
}) {
  const t = useT();
  const { cab, items, ev, cut } = props;
  const rows = useRef<(HTMLTableRowElement | null)[]>([]);
  const drag = useRef(false);
  const overById = new Map(ev.overloaded.map((s) => [s.id, s]));

  // La riga è «in sovraccarico» se sta sopra la linea e carica un team che ha settimane oltre la capacità.
  function overloadOf(it: RankItem, i: number): TeamStat | null {
    if (i >= cut) return null;
    const row = cab.rows[it.id];
    let best: TeamStat | null = null;
    for (const e of row?.efforts ?? []) {
      const st = overById.get(e.team_id);
      if (st && st.over.some((w) => (e.weeks[w] ?? 0) > 0) && (!best || st.peak > best.peak)) best = st;
    }
    return best;
  }

  function targetAt(clientY: number): number {
    let n = 0;
    for (let i = 0; i < items.length; i++) {
      const r = rows.current[i]?.getBoundingClientRect();
      if (r && clientY > r.top + r.height / 2) n = i + 1;
    }
    return n;
  }
  function onKey(e: KeyboardEvent<HTMLButtonElement>) {
    const step = e.key === "ArrowUp" ? -1 : e.key === "ArrowDown" ? 1 : e.key === "PageUp" ? -5 : e.key === "PageDown" ? 5 : 0;
    if (step) {
      e.preventDefault();
      props.onCut(Math.max(0, Math.min(items.length, cut + step)));
    } else if (e.key === "Home") {
      e.preventDefault();
      props.onCut(0);
    } else if (e.key === "End") {
      e.preventDefault();
      props.onCut(items.length);
    }
  }
  const cutRow = (key: string) => (
    <tr key={key} data-cut-row className="h-11">
      <td colSpan={6} className="p-0">
        <div className="relative flex h-11 items-center">
          <div className="pointer-events-none absolute inset-x-0 top-1/2 h-0.5 -translate-y-1/2 bg-accent" />
          <button
            type="button"
            role="slider"
            aria-label={t("lavoro.cabina.cutSlider")}
            aria-orientation="vertical"
            aria-valuemin={0}
            aria-valuemax={items.length}
            aria-valuenow={cut}
            aria-valuetext={t("lavoro.cabina.cutAfter", { n: cut })}
            title={t("lavoro.cabina.cutHelp")}
            className="relative z-10 flex min-h-[44px] min-w-[44px] cursor-grab items-center gap-1.5 bg-accent px-2 font-mono text-[11px] font-semibold whitespace-nowrap text-accent-fg active:cursor-grabbing"
            style={{ touchAction: "none" }}
            onKeyDown={onKey}
            onPointerDown={(e: RPointerEvent<HTMLButtonElement>) => {
              e.currentTarget.setPointerCapture(e.pointerId);
              drag.current = true;
            }}
            onPointerMove={(e) => {
              if (drag.current) props.onCut(targetAt(e.clientY));
            }}
            onPointerUp={() => (drag.current = false)}
            onPointerCancel={() => (drag.current = false)}
          >
            <span aria-hidden="true">⇅</span>
            {t("lavoro.cabina.cutLabel")} · {cut}/{items.length}
          </button>
        </div>
      </td>
    </tr>
  );

  if (items.length === 0) return <p className="text-muted">{t("budget.rank.empty")}</p>;
  return (
    <section aria-label={t("lavoro.cabina.ranking")} className="flex flex-col gap-1">
      <h2 className="label m-0 border-b-2 border-rule pb-1.5">{t("lavoro.cabina.ranking")}</h2>
      <table className="w-full border-collapse tabular" data-cabina-ranking>
        <thead>
          <tr className="label h-9 text-left">
            <th scope="col" className="w-10 pr-2">#</th>
            <th scope="col" className="px-2">{t("budget.rank.col.request")}</th>
            <th scope="col" className="hidden px-2 md:table-cell">{t("budget.rank.col.score")}</th>
            <th scope="col" className="hidden px-2 text-right md:table-cell">{t("budget.rank.col.estimate")}</th>
            <th scope="col" className="hidden px-2 lg:table-cell">{t("lavoro.cabina.col.dates")}</th>
            <th scope="col" className="w-12 pl-2"><span className="sr-only">{t("lavoro.cabina.col.load")}</span></th>
          </tr>
        </thead>
        <tbody>
          {items.map((it, i) => {
            const out = i >= cut;
            const row = cab.rows[it.id];
            const win = cab.windows[it.id];
            const why = whyHere(items, i, props.mandatoryFirst, { score: (n) => fmtNumber(n, { maximumFractionDigits: 1 }), date: (d) => date(d) });
            const over = overloadOf(it, i);
            return (
              <Fragment key={it.id}>
                {i === cut && cutRow(`cut-${i}`)}
                <tr
                  ref={(el) => {
                    rows.current[i] = el;
                  }}
                  data-pos={it.position}
                  className={`border-b border-hair align-top ${out ? "text-muted" : ""}`}
                >
                  <td className="py-2 pr-2 font-mono text-[13px] font-semibold">{it.position}</td>
                  <td className="max-w-0 px-2 py-2">
                    <span className="flex min-w-0 flex-col gap-0.5">
                      <Link to="/e/$key" params={{ key: it.key }} className={`line-clamp-2 font-medium no-underline hover:underline ${out ? "text-muted" : "text-fg"}`}>
                        {it.title}
                      </Link>
                      <span className="flex flex-wrap items-center gap-x-1.5 gap-y-0.5 text-[11.5px]">
                        <span className="font-mono whitespace-nowrap text-accent">{it.key}</span>
                        {it.is_mandatory && <span className="border border-flag px-1 text-[10.5px] font-semibold text-flag">{t("budget.rank.row.mandatory")}</span>}
                        {it.pinned && <span className="border border-muted px-1 text-[10.5px]">{t("budget.rank.row.pinned")}</span>}
                        <span data-why className="text-muted">{t(`lavoro.cabina.why.${why.k}` as Key, why.params)}</span>
                      </span>
                      <span className="text-[11.5px] text-muted">
                        {[row?.sponsor && t("lavoro.cabina.row.sponsor", { name: row.sponsor }), row?.unit].filter(Boolean).join(" · ") || t("lavoro.cabina.row.noSponsor")}
                      </span>
                      {row && row.risk.length > 0 && (
                        <span className="flex flex-wrap gap-1 text-[10.5px]">
                          {row.risk.map((r) => (
                            <span key={r} className="border border-flag px-1 text-flag">{t(`lavoro.cabina.risk.${r}`)}</span>
                          ))}
                        </span>
                      )}
                      <span className="text-[11.5px] text-muted md:hidden">
                        {it.score != null ? `${fmtNumber(it.score, { maximumFractionDigits: 1 })} · ` : ""}
                        {euroRange(it.estimate_low_cents, it.estimate_high_cents)}
                        {win?.start && win.end ? ` · ${date(win.start)} – ${date(win.end)}` : ""}
                      </span>
                    </span>
                  </td>
                  <td className="hidden px-2 py-2 whitespace-nowrap md:table-cell">
                    <ScoreBar value={it.score} stale={it.score_stale} dim={out} />
                  </td>
                  <td className="hidden px-2 py-2 text-right font-mono text-[12.5px] whitespace-nowrap md:table-cell">{euroRange(it.estimate_low_cents, it.estimate_high_cents)}</td>
                  <td className="hidden px-2 py-2 font-mono text-[12px] whitespace-nowrap lg:table-cell">
                    {win?.start && win.end ? `${date(win.start)} – ${date(win.end)}` : "—"}
                  </td>
                  <td className="py-0.5 pl-2">
                    {over && (
                      <button
                        type="button"
                        data-overload
                        aria-label={t("lavoro.cabina.row.overload", { team: over.name, pct: pct(over.peak) })}
                        title={t("lavoro.cabina.row.overload", { team: over.name, pct: pct(over.peak) })}
                        className="touch-44 flex items-center justify-center text-danger"
                        onClick={() => props.onTeam(over.id)}
                      >
                        <Warn />
                      </button>
                    )}
                  </td>
                </tr>
              </Fragment>
            );
          })}
          {cut === items.length && cutRow("cut-end")}
        </tbody>
      </table>
      <p className="sr-only" aria-live="polite">
        {t("budget.rank.cut.sr", { cut, total: items.length })}
      </p>
    </section>
  );
}

function CapacityPanel(props: {
  cab: Cabina;
  items: RankItem[];
  ev: CutEval;
  cut: number;
  mandatoryFirst: boolean;
  team: string | null;
  onTeam: (id: string | null) => void;
  onMove: (item: RankItem, to: number) => void;
}) {
  const t = useT();
  const { cab, ev, items, cut } = props;
  if (cab.teams.length === 0) return null;
  const sorted = ev.teams.slice().sort((a, b) => b.peak - a.peak);
  const focus = sorted.find((s) => s.id === props.team) ?? null;
  return (
    <section aria-label={t("lavoro.cabina.capacity")} className="flex flex-col gap-2">
      <h2 className="label m-0 border-b-2 border-rule pb-1.5">{t("lavoro.cabina.capacity")}</h2>
      <p className="m-0 text-[12.5px] text-muted">{t("lavoro.cabina.capacityHelp")}</p>
      <ul className="m-0 flex list-none flex-col p-0">
        {sorted.map((s) => (
          <li key={s.id} className="border-b border-hair/60">
            <button
              type="button"
              aria-expanded={props.team === s.id}
              aria-controls="cabina-chi-occupa"
              className="touch-44 flex w-full items-center gap-3 py-1 text-left"
              onClick={() => props.onTeam(props.team === s.id ? null : s.id)}
            >
              <span className="w-32 shrink-0 truncate font-medium sm:w-48">{s.name}</span>
              <span className="relative h-2 min-w-0 flex-1 bg-hair/50" aria-hidden="true">
                <span className={`absolute inset-y-0 left-0 ${s.overloaded ? "bg-danger" : "bg-fg"}`} style={{ width: `${Math.min(100, (100 * s.peak) / Math.max(s.peak, 1))}%`, opacity: s.overloaded ? 1 : 0.6 }} />
              </span>
              <span className={`w-14 shrink-0 text-right font-mono text-[13px] font-semibold tabular ${s.overloaded ? "text-danger" : ""}`}>{pct(s.peak)}</span>
              <span className="hidden w-40 shrink-0 text-[12px] text-muted sm:block">
                {s.overloaded ? t("lavoro.cabina.overWeeks", { count: s.over.length }) : t("lavoro.cabina.withinCapacity")}
              </span>
            </button>
          </li>
        ))}
      </ul>
      {focus && <WhoOccupies cab={cab} items={items} cut={cut} stat={focus} mandatoryFirst={props.mandatoryFirst} onMove={props.onMove} onClose={() => props.onTeam(null)} />}
    </section>
  );
}

function WhoOccupies(props: {
  cab: Cabina;
  items: RankItem[];
  cut: number;
  stat: TeamStat;
  mandatoryFirst: boolean;
  onMove: (item: RankItem, to: number) => void;
  onClose: () => void;
}) {
  const t = useT();
  const { cab, items, cut, stat } = props;
  const occ = occupants(cab, items, cut, stat.id);
  const sug = suggestMove(cab, items, cut, stat.id, props.mandatoryFirst);
  return (
    <div id="cabina-chi-occupa" role="region" aria-label={t("lavoro.cabina.who", { team: stat.name })} className="flex flex-col gap-2 border-l-2 border-accent pl-3">
      <div className="flex items-center justify-between gap-2">
        <h3 className="m-0 text-[15px] font-semibold">{t("lavoro.cabina.who", { team: stat.name })}</h3>
        <button type="button" className="btn touch-44" onClick={props.onClose}>
          {t("common.close")}
        </button>
      </div>
      {occ.length === 0 ? (
        <p className="m-0 text-muted">{t("lavoro.cabina.whoNone")}</p>
      ) : (
        <ul className="m-0 flex list-none flex-col p-0">
          {occ.map((o) => (
            <li key={o.item.id} className="flex flex-wrap items-baseline gap-x-3 border-b border-hair/60 py-1.5 text-[13px]">
              <span className="font-mono text-accent">{o.item.key}</span>
              <span className="min-w-0 flex-1">{o.item.title}</span>
              <span className="font-mono text-[12px] text-muted tabular">
                {t("lavoro.cabina.days", { days: fmtNumber(o.days, { maximumFractionDigits: 1 }) })}
                {o.overDays > 0 ? ` · ${t("lavoro.cabina.daysOver", { days: fmtNumber(o.overDays, { maximumFractionDigits: 1 }) })}` : ""}
              </span>
            </li>
          ))}
        </ul>
      )}
      {sug ? (
        <div className="flex flex-wrap items-center gap-3">
          <p className="m-0" data-suggestion>
            {sug.after
              ? t(sug.frees ? "lavoro.cabina.suggest.after" : "lavoro.cabina.suggest.afterPartial", { key: sug.item.key, after: sug.after.key, team: stat.name })
              : t(sug.frees ? "lavoro.cabina.suggest.end" : "lavoro.cabina.suggest.endPartial", { key: sug.item.key, team: stat.name })}
          </p>
          {sug.item.can_move ? (
            <button type="button" className="btn btn-primary touch-44" onClick={() => props.onMove(sug.item, sug.to)}>
              {t("lavoro.cabina.suggest.apply")}
            </button>
          ) : (
            <span className="text-[12px] text-muted">{t("lavoro.cabina.suggest.cannot")}</span>
          )}
        </div>
      ) : (
        stat.overloaded && <p className="m-0 text-muted">{t("lavoro.cabina.suggest.none")}</p>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------
// Scenari
// ---------------------------------------------------------------------

const CURRENT = "__current";

function Scenarios({ cab, items, ev, budget }: { cab: Cabina; items: RankItem[]; ev: CutEval; budget: number | null }) {
  const t = useT();
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [a, setA] = useState(CURRENT);
  const [b, setB] = useState("");
  const save = useMutation({
    mutationFn: () => portfolioApi.saveScenario({ name: name.trim(), cut: ev.cut, budget_cents: budget, item_keys: ev.inside.map((i) => i.key) }),
    onSuccess: (s) => {
      setName("");
      setB(s.id);
      qc.invalidateQueries({ queryKey: ["cabina"] });
    },
  });
  const del = useMutation({
    mutationFn: (id: string) => portfolioApi.deleteScenario(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["cabina"] }),
  });
  const list = cab.scenarios;
  const resolve = (id: string): { label: string; cut: number; budget: number | null; ev: CutEval } | null => {
    if (id === CURRENT) return { label: t("lavoro.cabina.sc.current"), cut: ev.cut, budget, ev };
    const s = list.find((x) => x.id === id);
    return s ? { label: s.name, cut: s.cut, budget: s.budget_cents, ev: evalCut(cab, items, s.cut, s.budget_cents) } : null;
  };
  const A = resolve(a);
  const B = resolve(b);
  const options = [{ value: CURRENT, label: t("lavoro.cabina.sc.current") }, ...list.map((s: PortfolioScenario) => ({ value: s.id, label: s.name }))];
  return (
    <section aria-label={t("lavoro.cabina.scenarios")} className="flex flex-col gap-3">
      <h2 className="label m-0 border-b-2 border-rule pb-1.5">{t("lavoro.cabina.scenarios")}</h2>
      {cab.ranking.can_admin && (
        <form
          className="flex flex-wrap items-end gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (name.trim()) save.mutate();
          }}
        >
          <Field label={t("lavoro.cabina.sc.name")} className="w-64 max-w-full">
            <input className="field" maxLength={80} value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <button className="btn btn-primary touch-44" disabled={!name.trim() || save.isPending}>
            {t("lavoro.cabina.sc.save", { n: ev.cut })}
          </button>
        </form>
      )}
      <ErrorNote error={save.error ?? del.error} />
      {list.length === 0 ? (
        <p className="m-0 text-muted">{t("lavoro.cabina.sc.empty")}</p>
      ) : (
        <ul className="m-0 flex list-none flex-wrap gap-2 p-0">
          {list.map((s) => (
            <li key={s.id} className="flex items-center gap-1 border border-hair pl-2 text-[12.5px]">
              <span className="font-medium">{s.name}</span>
              <span className="font-mono text-muted">{t("lavoro.cabina.sc.cut", { n: s.cut })}</span>
              {cab.ranking.can_admin && (
                <button type="button" className="touch-44 text-muted hover:text-danger" aria-label={t("lavoro.cabina.sc.delete", { name: s.name })} onClick={() => del.mutate(s.id)}>
                  ×
                </button>
              )}
            </li>
          ))}
        </ul>
      )}
      {list.length > 0 && (
        <>
          <div className="flex flex-wrap items-end gap-4">
            <Field label={t("lavoro.cabina.sc.a")} className="w-56 max-w-full">
              <OptionSelect value={a} onChange={(v) => setA(v || CURRENT)} options={options} />
            </Field>
            <Field label={t("lavoro.cabina.sc.b")} className="w-56 max-w-full">
              <OptionSelect value={b} onChange={setB} noneLabel={t("lavoro.cabina.sc.pick")} options={options} />
            </Field>
          </div>
          {A && B && <Compare A={A} B={B} total={items.length} />}
        </>
      )}
    </section>
  );
}

type Side = { label: string; cut: number; budget: number | null; ev: CutEval };

function Compare({ A, B, total }: { A: Side; B: Side; total: number }) {
  const t = useT();
  const teams = (s: Side) => (s.ev.overloaded.length === 0 ? t("lavoro.cabina.stat.none") : s.ev.overloaded.map((x) => `${x.name} ${pct(x.peak)}`).join(", "));
  const inA = new Set(A.ev.inside.map((i) => i.key));
  const inB = new Set(B.ev.inside.map((i) => i.key));
  const onlyA = A.ev.inside.filter((i) => !inB.has(i.key)).map((i) => i.key);
  const onlyB = B.ev.inside.filter((i) => !inA.has(i.key)).map((i) => i.key);
  const rows: [string, string, string, boolean?][] = [
    [t("lavoro.cabina.cmp.cut"), t("lavoro.cabina.cutAfter", { n: A.cut }), t("lavoro.cabina.cutAfter", { n: B.cut })],
    [t("lavoro.cabina.budget"), A.budget != null ? euro(A.budget) : "—", B.budget != null ? euro(B.budget) : "—"],
    [t("lavoro.cabina.stat.committed"), euro(A.ev.committed), euro(B.ev.committed)],
    [t("lavoro.cabina.stat.residual"), A.ev.residual != null ? euro(A.ev.residual) : "—", B.ev.residual != null ? euro(B.ev.residual) : "—"],
    [t("lavoro.cabina.cmp.inside"), t("lavoro.cabina.stat.insideOf", { n: A.ev.inside.length, total }), t("lavoro.cabina.stat.insideOf", { n: B.ev.inside.length, total })],
    [t("lavoro.cabina.cmp.outside"), String(A.ev.outside.length), String(B.ev.outside.length)],
    [t("lavoro.cabina.stat.overloaded"), teams(A), teams(B)],
    [t("lavoro.cabina.stat.end"), A.ev.end ? date(A.ev.end) : "—", B.ev.end ? date(B.ev.end) : "—"],
  ];
  return (
    <div className="overflow-x-auto" data-cabina-compare>
      <table className="w-full border-collapse tabular">
        <caption className="sr-only">{t("lavoro.cabina.cmp.caption")}</caption>
        <thead>
          <tr className="label h-9 text-left">
            <th scope="col" className="pr-3"><span className="sr-only">{t("lavoro.cabina.cmp.metric")}</span></th>
            <th scope="col" className="px-3">{A.label}</th>
            <th scope="col" className="px-3">{B.label}</th>
          </tr>
        </thead>
        <tbody>
          {rows.map(([k, x, y]) => (
            <tr key={k} className="border-b border-hair">
              <th scope="row" className="py-1.5 pr-3 text-left text-[12.5px] font-normal text-muted">{k}</th>
              <td className="px-3 py-1.5 font-mono text-[13px]">{x}</td>
              <td className="px-3 py-1.5 font-mono text-[13px]">{y}</td>
            </tr>
          ))}
          <tr className="border-b border-hair align-top">
            <th scope="row" className="py-1.5 pr-3 text-left text-[12.5px] font-normal text-muted">{t("lavoro.cabina.cmp.diff")}</th>
            <td className="px-3 py-1.5 font-mono text-[12.5px]">{onlyA.length ? t("lavoro.cabina.cmp.onlyHere", { keys: onlyA.join(", ") }) : "—"}</td>
            <td className="px-3 py-1.5 font-mono text-[12.5px]">{onlyB.length ? t("lavoro.cabina.cmp.onlyHere", { keys: onlyB.join(", ") }) : "—"}</td>
          </tr>
        </tbody>
      </table>
    </div>
  );
}
