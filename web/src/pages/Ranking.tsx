import { Fragment, useEffect, useMemo, useRef, useState, type KeyboardEvent, type PointerEvent as RPointerEvent } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { budgetApi, financeApi, orgApi, portfolioApi, type RankItem, type Ranking as RankingData } from "../api";
import { fmtNumber } from "../i18n/format";
import { useT } from "../i18n/react";
import { Trans } from "../i18n/Trans";
import { OptionSelect } from "../select";
import { PageHeader } from "../shell";
import { ErrorNote, date, euro, euroRange } from "../ui";
import { Field, periodOptions, defaultPeriod, mny } from "./budget/budget-ui";
import { ScoreBar } from "./Evaluation";
import { ScorecardConfig } from "./ScorecardConfig";
import { crumbs } from "../crumbs";

// Graduatoria del portafoglio (demand.md §6, D04): prima gli obblighi normativi, poi il
// punteggio della scorecard. Il budget disponibile traccia la linea di taglio sulla somma
// cumulata delle stime: sopra le richieste finanziate, sotto le altre. La linea si calcola
// qui mentre si muove il cursore; l'ordine forzato a mano passa dal server con motivazione.

export type Pending = { item: RankItem; from: number; to: number | null };

/** Linea di taglio: quante richieste, nell'ordine dato, stanno nel budget (come il server). */
function cutOf(items: RankItem[], budget: number | null): { cut: number | null; funded: number; cum: number[] } {
  const cum: number[] = [];
  let c = 0;
  for (const it of items) {
    c += it.estimate_cents ?? 0;
    cum.push(c);
  }
  if (budget == null) return { cut: null, funded: 0, cum };
  let n = 0;
  while (n < items.length && cum[n] <= budget) n++;
  return { cut: n, funded: n > 0 ? cum[n - 1] : 0, cum };
}

function kEuro(cents: number): string {
  return fmtNumber(cents / 100_000, { maximumFractionDigits: 0 });
}

export function Ranking({ view, unit, period }: { view: "graduatoria" | "scorecard"; unit?: string; period?: string }) {
  const t = useT();
  const qc = useQueryClient();
  const rk = useQuery({ queryKey: ["ranking", unit ?? "", period ?? ""], queryFn: () => portfolioApi.rankingFor(unit, period) });
  const [budget, setBudget] = useState<number | null>(null); // centesimi; null = non impostato
  const [touched, setTouched] = useState(false);
  const [pending, setPending] = useState<Pending | null>(null);
  const [reason, setReason] = useState("");
  const saved = rk.data?.budget_cents ?? null;
  useEffect(() => {
    if (!touched) setBudget(saved);
  }, [saved, touched]);
  // Cambiando area o periodo il budget torna quello del server.
  useEffect(() => setTouched(false), [unit, period]);

  const saveBudget = useMutation({
    mutationFn: (b: number | null) => portfolioApi.updateSettings(b == null ? { clear_budget: true } : { budget_cents: b }),
    onSuccess: () => {
      setTouched(false);
      qc.invalidateQueries({ queryKey: ["ranking"] });
    },
  });
  const move = useMutation({
    mutationFn: (p: Pending) => portfolioApi.move(p.item.key, p.to, reason),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["ranking"] });
      setPending(null);
      setReason("");
    },
  });

  // Ordine mostrato: quello del server, oppure l'anteprima dello spostamento da confermare.
  const items = useMemo(() => {
    const base = rk.data?.items ?? [];
    if (!pending || pending.to == null) return base;
    const out = base.filter((i) => i.id !== pending.item.id);
    out.splice(pending.to - 1, 0, pending.item);
    return out;
  }, [rk.data, pending]);
  const { cut, funded, cum } = useMemo(() => cutOf(items, budget), [items, budget]);

  return (
    <>
      <PageHeader
        path={crumbs("demand", "ranking")}
        title={t("budget.rank.title")}
        actions={
          view === "graduatoria" ? (
            <a className="btn no-underline" href={portfolioApi.rankingCsvUrl(budget, unit, period)} download>
              {t("budget.rank.exportCsv")}
            </a>
          ) : undefined
        }
      />
      <div role="tablist" aria-label={t("budget.rank.view")} className="flex gap-6 border-b border-hair px-4 pt-1 md:px-7">
        <Link
          to="/graduatoria"
          search={{}}
          role="tab"
          aria-selected={view === "graduatoria"}
          className={view === "graduatoria" ? "border-b-2 border-fg pb-2.5 font-semibold text-fg no-underline" : "pb-2.5 text-muted no-underline hover:text-fg"}
        >
          {t("budget.rank.tab.ranking")}
        </Link>
        <Link
          to="/graduatoria"
          search={{ vista: "scorecard" }}
          role="tab"
          aria-selected={view === "scorecard"}
          className={view === "scorecard" ? "border-b-2 border-fg pb-2.5 font-semibold text-fg no-underline" : "pb-2.5 text-muted no-underline hover:text-fg"}
        >
          {t("budget.rank.tab.scorecard")}{rk.data?.scorecard ? <span className="ml-1 font-mono text-[11.5px] font-normal tabular">v{rk.data.scorecard.version}</span> : null}
        </Link>
        <Link to="/cabina" role="tab" aria-selected="false" className="pb-2.5 text-muted no-underline hover:text-fg">
          {t("lavoro.cabina.tab")}
        </Link>
      </div>
      {view === "scorecard" ? (
        <ScorecardConfig scorecardKey={rk.data?.scorecard?.key ?? "portafoglio"} />
      ) : (
        <div className="flex flex-col gap-5 px-4 py-5 md:px-7">
          <ErrorNote error={rk.error} />
          <AreaPicker unit={unit} period={period} />
          {rk.data && (
            <>
              <BudgetPanel
                data={rk.data}
                items={items}
                budget={budget}
                cut={cut}
                funded={funded}
                cum={cum}
                touched={touched}
                onBudget={(b) => {
                  setTouched(true);
                  setBudget(b);
                }}
                onSave={() => saveBudget.mutate(budget)}
                onReset={() => {
                  setTouched(false);
                  setBudget(saved);
                }}
                saving={saveBudget.isPending}
              />
              <ErrorNote error={saveBudget.error} />
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
              <RankTable
                data={rk.data}
                items={items}
                budget={budget}
                cut={cut}
                cum={cum}
                pendingId={pending?.item.id}
                onMove={(item, from, to) => {
                  move.reset();
                  setPending({ item, from, to });
                }}
              />
              {items.length === 0 && <p className="text-muted">{t("budget.rank.empty")}</p>}
              <p className="m-0 text-[12.5px] text-muted">
                {t(rk.data.settings.mandatory_first ? "budget.rank.order.mandatory" : "budget.rank.order.plain", { basis: t(`budget.rank.basis.${rk.data.settings.estimate_basis}`) })}
              </p>
            </>
          )}
        </div>
      )}
    </>
  );
}

/** Area e periodo: con un'area «cosa entra» confronta le sue richieste con il residuo del suo stanziamento. */
function AreaPicker({ unit, period }: { unit?: string; period?: string }) {
  const t = useT();
  const navigate = useNavigate();
  const units = useQuery({ queryKey: ["org", "units", false], queryFn: () => orgApi.units({}), retry: false });
  const periods = useQuery({ queryKey: ["budget", "periods"], queryFn: () => budgetApi.periods(), retry: false });
  const list = periods.data?.items ?? [];
  if (!units.data || units.data.items.length === 0) return null;
  const go = (u?: string, p?: string) => navigate({ to: "/graduatoria", search: { unita: u || undefined, periodo: u ? p || undefined : undefined } });
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
      <p className="m-0 max-w-[60ch] pb-2 text-[12.5px] text-muted">{unit ? t("budget.rank.area.helpScoped") : t("budget.rank.area.help")}</p>
    </section>
  );
}

/** Da dove viene il budget; senza collegamento propone il plafond dell'esercizio in corso. */
function BudgetSourceNote({ data }: { data: RankingData }) {
  const t = useT();
  const qc = useQueryClient();
  const src = data.budget_source;
  const link = useMutation({
    mutationFn: (id: string) => financeApi.saveSettings({ portfolio_fiscal_year_id: id }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["ranking"] });
      qc.invalidateQueries({ queryKey: ["finance"] });
    },
  });
  if (src.kind === "allocation" && data.scope)
    return (
      <p className="m-0 text-[12.5px] text-muted" data-budget-source="allocation">
        <Trans
          k="budget.rank.src.allocation"
          params={{ amount: mny(data.scope.residual_cents), allocated: mny(data.scope.allocated_cents) }}
          slots={{ unit: <span className="font-semibold text-fg">{data.scope.unit}</span>, period: <span className="font-semibold text-fg">{data.scope.period}</span> }}
        />
      </p>
    );
  if (data.scope && !data.scope.has_allocation)
    return (
      <p className="m-0 text-[12.5px] text-flag" data-budget-source="none">
        {data.scope.period_id ? t("budget.rank.src.noAllocation", { unit: data.scope.unit }) : t("budget.rank.src.noPeriod", { unit: data.scope.unit })}
      </p>
    );
  if (src.kind === "fiscal_year")
    return (
      <p className="m-0 text-[12.5px] text-muted" data-budget-source="fiscal_year">
        <Trans
          k="budget.rank.src.fiscalYear"
          params={{ basis: src.basis === "residual" ? t("budget.rank.src.residual") : t("budget.rank.src.envelope") }}
          slots={{
            name: <span className="font-semibold text-fg">{src.fiscal_year}</span>,
            link: <Link to="/finanza" className="text-accent underline">{t("budget.rank.src.finance")}</Link>,
          }}
        />
      </p>
    );
  return (
    <div className="flex flex-col gap-1" data-budget-source={src.kind}>
      <p className="m-0 text-[12.5px] text-muted">
        {src.kind === "manual" ? t("budget.rank.src.manual") : t("budget.rank.src.none")}
        {src.suggested && (
          <>
            {" "}
            <Trans
              k="budget.rank.src.suggested"
              slots={{ name: <span className="font-semibold text-fg">{src.suggested.name}</span>, amount: <span className="tabular">{euro(src.suggested.envelope_cents)}</span> }}
            />{" "}
            <button className="touch-44 text-accent underline" disabled={link.isPending} onClick={() => link.mutate(src.suggested!.id)}>
              {t("budget.rank.src.use")}
            </button>
          </>
        )}
      </p>
      <ErrorNote error={link.error} />
    </div>
  );
}

function BudgetPanel(props: {
  data: RankingData;
  items: RankItem[];
  budget: number | null;
  cut: number | null;
  funded: number;
  cum: number[];
  touched: boolean;
  onBudget: (b: number | null) => void;
  onSave: () => void;
  onReset: () => void;
  saving: boolean;
}) {
  const t = useT();
  const { data, items, budget, cut, funded, cum } = props;
  const total = cum.length ? cum[cum.length - 1] : 0;
  // Scala del cursore e della barra: un po' oltre il totale (o il budget, se più alto).
  const scale = Math.max(Math.ceil((Math.max(total, budget ?? 0) * 1.15) / 1_000_000) * 1_000_000, 1_000_000);
  const step = scale > 200_000_000 ? 1_000_000 : 500_000; // 10 k€ o 5 k€
  const [text, setText] = useState(budget != null ? kEuro(budget) : "");
  useEffect(() => setText(budget != null ? kEuro(budget) : ""), [budget]);
  const missing = items.filter((i) => i.estimate_cents == null).length;
  const pct = (c: number) => `${(100 * c) / scale}%`;
  const out = cut != null ? items.length - cut : 0;

  return (
    <section aria-label={t("budget.rank.budget")} className="flex flex-col gap-3 border-b-2 border-rule pb-4">
      <div className="flex flex-wrap items-end gap-x-8 gap-y-3">
        <label className="flex flex-col gap-1">
          <span className="label">{t("budget.rank.budget")}</span>
          <span className="flex items-baseline gap-1.5 border-b-[1.5px] border-rule">
            <input
              className="w-[7ch] border-0 bg-transparent p-0 font-mono text-[30px] leading-tight font-bold tracking-tight text-fg tabular outline-none"
              inputMode="numeric"
              placeholder="—"
              value={text}
              aria-describedby="budget-esito"
              onChange={(e) => {
                setText(e.target.value);
                const v = e.target.value.replace(/\./g, "").replace(",", ".").trim();
                if (v === "") props.onBudget(null);
                else if (!Number.isNaN(Number(v))) props.onBudget(Math.max(0, Math.round(Number(v) * 100_000)));
              }}
            />
            <span className="font-mono text-[15px] text-muted">k€</span>
          </span>
        </label>
        <dl id="budget-esito" className="m-0 flex flex-wrap gap-x-6 gap-y-1 tabular" aria-live="polite">
          <Stat label={t("budget.rank.stat.funded")} value={cut == null ? "—" : t("budget.rank.stat.fundedOf", { n: cut, total: items.length })} />
          <Stat label={t("budget.rank.stat.committed")} value={cut == null ? "—" : euro(funded)} />
          <Stat label={t("budget.rank.stat.margin")} value={budget == null ? "—" : euro(Math.max(0, budget - funded))} />
          <Stat label={t("budget.rank.stat.out")} value={cut == null ? "—" : `${out} · ${euro(total - funded)}`} muted={out === 0} />
        </dl>
        <div className="ml-auto flex flex-wrap items-center gap-2">
          {props.touched && budget !== data.budget_cents && (
            <>
              <span className="text-[12px] text-muted">{t("budget.rank.simulation")}</span>
              {data.can_admin && data.budget_source.kind !== "fiscal_year" && data.budget_source.kind !== "allocation" && (
                <button className="btn btn-primary py-1.5" disabled={props.saving} onClick={props.onSave}>
                  {t("budget.rank.saveBudget")}
                </button>
              )}
              <button className="btn py-1.5" onClick={props.onReset}>
                {data.budget_cents != null ? t("budget.rank.backTo", { amount: euro(data.budget_cents) }) : t("common.cancel")}
              </button>
            </>
          )}
        </div>
      </div>

      <BudgetSourceNote data={data} />

      {/* La barra del portafoglio: ogni segmento è una richiesta in ordine di graduatoria,
          largo quanto la sua stima. Il cursore è il budget: la linea divide finanziate e no. */}
      <div className="flex flex-col gap-1">
        <div className="relative h-9 select-none">
          <div className="absolute inset-x-0 top-3 bottom-0 flex bg-hair/40">
            {items.map((it, i) => {
              const w = it.estimate_cents ?? 0;
              if (w <= 0) return null;
              const inside = cut != null && i < cut;
              return (
                <span
                  key={it.id}
                  title={`${it.position}. ${it.key} ${it.title}: ${euro(it.estimate_cents)}`}
                  className={`h-full border-r border-bg transition-colors duration-300 ${inside || cut == null ? "bg-fg" : "bg-muted/35"} ${
                    it.is_mandatory ? "shadow-[inset_0_3px_0_var(--flag)]" : ""
                  }`}
                  style={{ width: pct(w) }}
                />
              );
            })}
          </div>
          {budget != null && (
            <div className="pointer-events-none absolute top-0 bottom-0 z-10 w-0.5 bg-accent" style={{ left: `min(${pct(budget)}, calc(100% - 2px))` }}>
              <span
                className={`absolute -top-0.5 bg-bg px-1 font-mono text-[11px] font-semibold whitespace-nowrap text-accent ${budget / scale > 0.8 ? "right-1" : "left-1"}`}
              >
                {euro(budget)}
              </span>
            </div>
          )}
        </div>
        <input
          type="range"
          aria-label={t("budget.rank.budgetSlider")}
          min={0}
          max={scale}
          step={step}
          value={budget ?? 0}
          onChange={(e) => props.onBudget(Number(e.target.value))}
          className="w-full accent-[var(--accent)]"
        />
        <div className="flex justify-between font-mono text-[11px] text-muted tabular">
          <span>0</span>
          <span>
            {t("budget.rank.totalEstimates", { amount: euro(total) })}
            {missing > 0 ? ` · ${t("budget.rank.missingEstimates", { count: missing })}` : ""}
          </span>
          <span>{euro(scale)}</span>
        </div>
      </div>
    </section>
  );
}

function Stat({ label, value, muted }: { label: string; value: string; muted?: boolean }) {
  return (
    <div className="flex flex-col">
      <dt className="label text-muted">{label}</dt>
      <dd className={`m-0 font-mono text-[16px] font-semibold ${muted ? "text-muted" : ""}`}>{value}</dd>
    </div>
  );
}

export function MovePanel(props: {
  pending: Pending;
  reason: string;
  setReason: (s: string) => void;
  busy: boolean;
  error: unknown;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  const t = useT();
  const { pending } = props;
  return (
    <form
      className="flex flex-col gap-2 border-l-2 border-accent pl-3"
      onSubmit={(e) => {
        e.preventDefault();
        props.onConfirm();
      }}
    >
      <p className="m-0">
        <span className="font-mono text-[12px] text-accent">{pending.item.key}</span> <span className="font-semibold">{pending.item.title}</span>
        {" "}
        {pending.to == null ? (
          t("budget.rank.move.auto")
        ) : (
          <>
            <Trans
              k="budget.rank.move.to"
              slots={{ from: <span className="font-mono tabular">{pending.from}</span>, to: <span className="font-mono font-semibold tabular">{pending.to}</span> }}
            />
            <span className="text-muted"> {t("budget.rank.move.cutHint")}</span>
          </>
        )}
      </p>
      <label className="flex flex-col gap-1">
        <span className="label">{t("budget.rank.move.reason")}</span>
        <textarea className="field min-h-14" required autoFocus value={props.reason} onChange={(e) => props.setReason(e.target.value)} />
      </label>
      <div className="flex gap-2">
        <button className="btn btn-primary" disabled={props.busy || !props.reason.trim()}>
          {t("budget.rank.move.confirm")}
        </button>
        <button type="button" className="btn" onClick={props.onCancel}>
          {t("common.cancel")}
        </button>
      </div>
      <ErrorNote error={props.error} />
    </form>
  );
}

type Drag = { id: string; from: number; y0: number; target: number; moved: boolean };

function RankTable(props: {
  data: RankingData;
  items: RankItem[];
  budget: number | null;
  cut: number | null;
  cum: number[];
  pendingId?: string;
  onMove: (item: RankItem, from: number, to: number | null) => void;
}) {
  const t = useT();
  const { data, items, budget, cut, cum } = props;
  const wrap = useRef<HTMLDivElement>(null);
  const rows = useRef<(HTMLTableRowElement | null)[]>([]);
  const [drag, setDrag] = useState<Drag | null>(null);
  const dragRef = useRef<Drag | null>(null);
  const [hint, setHint] = useState("");
  const total = cum.length ? cum[cum.length - 1] : 0;
  const scale = Math.max(total, budget ?? 0, 1);
  const mandatory = data.settings.mandatory_first ? items.filter((i) => i.is_mandatory).length : 0;

  // La linea di taglio è una riga spaziatrice tra le richieste: quella al taglio si apre,
  // le altre restano chiuse. Spostando il budget una si chiude e l'altra si apre insieme,
  // così la linea scivola tra le righe senza coprirle.
  const spacer = (at: number) => {
    const on = cut === at && items.length > 0;
    return (
      <tr key={`taglio-${at}`} aria-hidden="true">
        <td colSpan={8} className="p-0">
          <div className="relative overflow-hidden transition-[height,opacity] duration-[380ms] ease-[cubic-bezier(.2,.8,.2,1)]" style={{ height: on ? 30 : 0, opacity: on ? 1 : 0 }}>
            <div className="absolute inset-x-0 top-[14px] h-0.5 bg-accent" />
            <span className="absolute top-[6px] left-0 bg-accent px-1.5 font-mono text-[10.5px] leading-[18px] font-semibold whitespace-nowrap text-accent-fg">
              {t("budget.rank.cut.line", { amount: budget != null ? euro(budget) : "" })}
            </span>
            <span className="absolute top-[6px] right-0 bg-bg pl-1.5 font-mono text-[10.5px] leading-[18px] whitespace-nowrap text-accent">
              {at === items.length ? t("budget.rank.cut.all") : t("budget.rank.cut.split", { funded: at, out: items.length - at })}
            </span>
          </div>
        </td>
      </tr>
    );
  };

  // Posizione di destinazione (1-based) dato il puntatore, nel gruppo consentito.
  function targetAt(clientY: number, it: RankItem): number {
    let t = items.length;
    for (let i = 0; i < items.length; i++) {
      const r = rows.current[i]?.getBoundingClientRect();
      if (r && clientY < r.top + r.height / 2) {
        t = i + 1;
        break;
      }
    }
    const cur = items.findIndex((x) => x.id === it.id) + 1;
    if (t > cur) t -= 1; // la riga trascinata libera il suo posto
    return clamp(t, it);
  }
  function clamp(pos: number, it: RankItem): number {
    if (mandatory > 0 && it.is_mandatory && pos > mandatory) {
      setHint(t("budget.rank.hint.top"));
      return mandatory;
    }
    if (mandatory > 0 && !it.is_mandatory && pos <= mandatory) {
      setHint(t("budget.rank.hint.above"));
      return mandatory + 1;
    }
    setHint("");
    return Math.max(1, Math.min(items.length, pos));
  }

  function onDown(e: RPointerEvent<HTMLButtonElement>, it: RankItem) {
    e.currentTarget.setPointerCapture(e.pointerId);
    const d = { id: it.id, from: it.position, y0: e.clientY, target: it.position, moved: false };
    dragRef.current = d;
    setDrag(d);
  }
  function onPMove(e: RPointerEvent<HTMLButtonElement>, it: RankItem) {
    const d = dragRef.current;
    if (!d) return;
    if (Math.abs(e.clientY - d.y0) > 4) d.moved = true;
    const nd = { ...d, target: targetAt(e.clientY, it) };
    dragRef.current = nd;
    setDrag(nd);
  }
  function onUp(it: RankItem) {
    const d = dragRef.current;
    dragRef.current = null;
    setDrag(null);
    if (d && d.moved && d.target !== d.from) props.onMove(it, d.from, d.target);
  }
  function onKey(e: KeyboardEvent<HTMLButtonElement>, it: RankItem) {
    if (e.key !== "ArrowUp" && e.key !== "ArrowDown") return;
    e.preventDefault();
    const t = clamp(it.position + (e.key === "ArrowUp" ? -1 : 1), it);
    if (t !== it.position) props.onMove(it, it.position, t);
  }

  // Indicatore di inserimento durante il trascinamento: sopra la riga di destinazione se si
  // sale, sotto se si scende.
  const active = drag && drag.moved && drag.target !== drag.from ? drag : null;
  const markAbove = active && active.target < active.from ? active.target - 1 : -1;
  const markBelow = active && active.target > active.from ? active.target - 1 : -1;

  return (
    <div ref={wrap} className="relative overflow-x-auto">
      <table className="w-full border-collapse tabular">
        <thead>
          <tr className="label h-9 text-left">
            <th scope="col" className="w-14 border-b-2 border-rule pr-2">#</th>
            <th scope="col" className="min-w-28 border-b-2 border-rule px-2 sm:min-w-40">{t("budget.rank.col.request")}</th>
            <th scope="col" className="border-b-2 border-rule px-2">{t("budget.rank.col.score")}</th>
            <th scope="col" className="hidden border-b-2 border-rule px-2 text-right lg:table-cell">{t("budget.rank.col.estimate")}</th>
            <th scope="col" className="hidden border-b-2 border-rule px-2 text-right xl:table-cell" title={t("budget.rank.col.valueHint")}>
              {t("budget.rank.col.value")}
            </th>
            <th scope="col" className="hidden border-b-2 border-rule px-2 xl:table-cell">{t("budget.rank.col.phase")}</th>
            <th scope="col" className="hidden border-b-2 border-rule px-2 lg:table-cell">{t("budget.rank.col.due")}</th>
            <th scope="col" className="w-[22%] min-w-[84px] border-b-2 border-rule pl-2">{t("budget.rank.col.cumulative")}</th>
          </tr>
        </thead>
        <tbody>
          {items.map((it, i) => {
            const out = cut != null && i >= cut;
            const prev = i > 0 ? cum[i - 1] : 0;
            const dragging = drag?.id === it.id;
            return (
              <Fragment key={it.id}>
              {spacer(i)}
              <tr
                ref={(el) => {
                  rows.current[i] = el;
                }}
                className={`h-12 border-b border-hair transition-[color,opacity,background-color] duration-300 ${out ? "text-muted" : ""} ${
                  dragging || props.pendingId === it.id ? "bg-sel" : ""
                } ${markBelow === i ? "shadow-[inset_0_-3px_0_var(--accent)]" : ""} ${markAbove === i ? "shadow-[inset_0_3px_0_var(--accent)]" : ""}`}
              >
                <td className="pr-2 align-middle">
                  <span className="flex items-center gap-1">
                    {it.can_move ? (
                      <button
                        type="button"
                        aria-label={t("budget.rank.row.moveLabel", { key: it.key, n: i + 1 })}
                        title={t("budget.rank.row.moveHint")}
                        className="flex h-8 w-4 cursor-grab items-center justify-center text-muted hover:text-fg active:cursor-grabbing"
                        style={{ touchAction: "none" }}
                        onPointerDown={(e) => onDown(e, it)}
                        onPointerMove={(e) => onPMove(e, it)}
                        onPointerUp={() => onUp(it)}
                        onPointerCancel={() => {
                          dragRef.current = null;
                          setDrag(null);
                        }}
                        onKeyDown={(e) => onKey(e, it)}
                      >
                        <Grip />
                      </button>
                    ) : (
                      <span className="w-4" />
                    )}
                    <span className={`font-mono text-[13px] font-semibold ${out ? "" : "text-fg"}`}>{i + 1}</span>
                  </span>
                </td>
                <td className="max-w-0 px-2 py-1.5">
                  <span className="flex min-w-0 flex-col">
                    <Link
                      to="/e/$key"
                      params={{ key: it.key }}
                      className={`line-clamp-2 font-medium no-underline hover:underline sm:line-clamp-1 ${out ? "text-muted" : "text-fg"}`}
                    >
                      {it.title}
                    </Link>
                    <span className="flex items-center gap-1.5 text-[11.5px]">
                      <span className="font-mono whitespace-nowrap text-accent">{it.key}</span>
                      {it.is_mandatory && <span className="border border-flag px-1 text-[10.5px] font-semibold text-flag">{t("budget.rank.row.mandatory")}</span>}
                      {it.pinned && (
                        <button
                          type="button"
                          className="border border-muted px-1 text-[10.5px] text-muted hover:border-fg hover:text-fg"
                          title={`${t("budget.rank.row.pinnedBy", { name: it.pinned_by ?? "—", reason: it.pin_reason ?? "" })}${it.can_move ? `\n${t("budget.rank.row.pinnedUndo")}` : ""}`}
                          onClick={() => it.can_move && props.onMove(it, it.position, null)}
                        >
                          {t("budget.rank.row.pinned")}
                        </button>
                      )}
                      <span className="truncate text-muted xl:hidden">
                        {it.state}
                        {it.regulatory_deadline ? <span className="text-flag lg:hidden"> · {date(it.regulatory_deadline)}</span> : null}
                      </span>
                    </span>
                  </span>
                </td>
                <td className="px-2 whitespace-nowrap">
                  <ScoreBar value={it.score} stale={it.score_stale} dim={out} />
                  {it.score_stale && <span className="ml-1 font-mono text-[10.5px] text-muted">v{it.score_version}</span>}
                </td>
                <td className="hidden px-2 text-right font-mono text-[12.5px] whitespace-nowrap lg:table-cell">
                  {euroRange(it.estimate_low_cents, it.estimate_high_cents)}
                </td>
                <td className="hidden px-2 text-right font-mono text-[12.5px] whitespace-nowrap xl:table-cell">
                  {it.value_per_cost != null ? fmtNumber(it.value_per_cost, { maximumFractionDigits: 1 }) : "—"}
                </td>
                <td className="hidden px-2 text-[12.5px] whitespace-nowrap xl:table-cell">{it.state}</td>
                <td className="hidden px-2 text-[12.5px] whitespace-nowrap lg:table-cell">
                  {it.regulatory_deadline ? (
                    <span className={out ? "" : "font-semibold text-flag"}>{date(it.regulatory_deadline)}</span>
                  ) : it.desired_date ? (
                    <span className="text-muted">{date(it.desired_date)}</span>
                  ) : (
                    "—"
                  )}
                </td>
                <td className="pl-2">
                  <span className="flex flex-col gap-1">
                    <span className="relative block h-2 bg-hair/50" aria-hidden="true">
                      {it.estimate_cents != null && (
                        <span
                          className={`absolute inset-y-0 transition-colors duration-300 ${out ? "bg-muted/40" : "bg-fg"}`}
                          style={{ left: `${(100 * prev) / scale}%`, width: `${Math.max((100 * (it.estimate_cents ?? 0)) / scale, 0.6)}%` }}
                        />
                      )}
                      {budget != null && (
                        <span className="absolute -top-1 -bottom-1 w-0.5 bg-accent" style={{ left: `min(${(100 * budget) / scale}%, calc(100% - 2px))` }} />
                      )}
                    </span>
                    <span className="font-mono text-[11px] text-muted">
                      {it.estimate_cents == null ? t("budget.rank.row.noEstimate") : euro(cum[i])}
                    </span>
                  </span>
                </td>
              </tr>
              </Fragment>
            );
          })}
          {spacer(items.length)}
        </tbody>
      </table>
      {hint && drag && <p className="m-0 pt-1 text-[12px] text-flag">{hint}</p>}
      <p className="sr-only" aria-live="polite">
        {cut != null ? t("budget.rank.cut.sr", { cut, total: items.length }) : ""}
      </p>
    </div>
  );
}

function Grip() {
  return (
    <svg width="8" height="14" viewBox="0 0 8 14" aria-hidden="true">
      {[2, 7, 12].flatMap((y) => [1.5, 6.5].map((x) => <circle key={`${x}-${y}`} cx={x} cy={y} r="1.2" fill="currentColor" />))}
    </svg>
  );
}
