import { useEffect, useMemo, useRef, useState } from "react";
import type { EVProject } from "../api";
import { lookup } from "../i18n/core";
import { loc } from "../i18n/format";
import { fromInput } from "./budget/budget-ui";
import { useT } from "../i18n/react";

// Pezzi comuni di budget e costi: importi, barra consumo/budget, curva S dell'earned value.

const fmtCache = new Map<string, Intl.NumberFormat>();
function nf(currency: string, digits: number): Intl.NumberFormat {
  const k = `${loc()}|${currency}|${digits}`;
  let f = fmtCache.get(k);
  if (!f) {
    try {
      f = new Intl.NumberFormat(loc(), { style: "currency", currency, minimumFractionDigits: digits, maximumFractionDigits: digits });
    } catch {
      f = new Intl.NumberFormat(loc(), { minimumFractionDigits: digits, maximumFractionDigits: digits });
    }
    fmtCache.set(k, f);
  }
  return f;
}

/** Importo in centesimi: compatto (k, M) per i totali, preciso per le voci. */
export function money(cents: number | null | undefined, currency = "EUR", precise = false): string {
  if (cents == null) return "—";
  const v = cents / 100;
  if (precise) return nf(currency, 2).format(v);
  const abs = Math.abs(v);
  if (abs >= 1_000_000) return `${(v / 1_000_000).toLocaleString(loc(), { maximumFractionDigits: 2 })} M${symbol(currency)}`;
  if (abs >= 1000) return `${(v / 1000).toLocaleString(loc(), { maximumFractionDigits: 1 })} k${symbol(currency)}`;
  return nf(currency, 0).format(v);
}

function symbol(currency: string): string {
  return currency === "EUR" ? "€" : ` ${currency}`;
}

/** Legge un importo scritto come "1.234,50", "1,234.50" o "1234.5" in centesimi; null se non valido. */
export function parseMoney(s: string): number | null {
  return fromInput(s);
}

export function centsToInput(c: number | null | undefined): string {
  return c == null ? "" : (c / 100).toLocaleString(loc(), { maximumFractionDigits: 2, useGrouping: false });
}

export function pct(v: number | null | undefined, digits = 0): string {
  return v == null ? "—" : `${v.toLocaleString(loc(), { maximumFractionDigits: digits })}%`;
}

export function ratio(v: number | null | undefined): string {
  return v == null ? "—" : v.toLocaleString(loc(), { minimumFractionDigits: 2, maximumFractionDigits: 2 });
}

/** Etichette dei tipi di budget, degli stati, delle origini e dei metodi: una voce di catalogo per ciascuno. */
export const kindLabel = (k: string) => lookup(`budget.fui.kind.${k}`) ?? k;
export const statusLabel = (k: string) => lookup(`budget.fui.status.${k}`) ?? k;
export const sourceLabel = (k: string) => lookup(`budget.fui.source.${k}`) ?? k;
export const methodLabel = (k: string) => lookup(`budget.fui.method.${k}`) ?? k;
export const SOURCE_KEYS = ["manual", "invoice", "purchase_order", "recurring", "import", "work_log"] as const;
export const METHOD_KEYS = ["percent_complete", "milestones", "work_packages"] as const;

/**
 * Barra consumo/budget: traccia sottile, consuntivo in --fg, impegnato in --muted subito
 * dopo (separato da 2 px), tacche delle soglie di allerta. Oltre il budget la parte in
 * eccesso è in --danger.
 */
export function ConsumptionBar({
  actual,
  committed,
  budget,
  thresholds,
}: {
  actual: number;
  committed: number;
  budget: number;
  thresholds: number[];
}) {
  const t = useT();
  const scale = Math.max(budget, actual + committed, 1);
  const w = (c: number) => `${(100 * Math.max(c, 0)) / scale}%`;
  const inBudget = Math.min(actual, budget);
  const over = Math.max(actual - budget, 0);
  return (
    <div className="flex flex-col gap-1">
      <div className="relative h-2 bg-hair" role="img" aria-label={t("budget.fui.barLabel", { pct: pct((100 * actual) / Math.max(budget, 1)) })}>
        <div className="absolute inset-0 flex gap-[2px]">
          <span className="h-full bg-fg" style={{ width: w(inBudget) }} />
          {over > 0 && <span className="h-full bg-danger" style={{ width: w(over) }} />}
          {committed > 0 && <span className="h-full bg-muted" style={{ width: w(committed) }} />}
        </div>
        {thresholds.map((th) =>
          (th * budget) / 100 <= scale ? (
            <span key={th} className="absolute -top-1 -bottom-1 w-px bg-rule" style={{ left: w((th * budget) / 100) }} title={t("budget.fui.threshold", { pct: th })} />
          ) : null,
        )}
      </div>
      <div className="flex justify-between font-mono text-[10.5px] text-muted tabular">
        <span>0</span>
        <span>{t("budget.fui.ofBudget", { list: thresholds.map((th) => `${th}%`).join(" · ") })}</span>
      </div>
    </div>
  );
}

type Pt = { t: number; v: number };

/**
 * Curva S dell'earned value in SVG: PV tratteggiato in --muted (pianificato), EV in
 * --accent con marcatori quadrati, AC in --fg; BAC come linea sottile, oggi come
 * regola verticale. Etichette dirette a fine linea, legenda sopra, valori al passaggio
 * del puntatore (o del dito) e tabella dei dati sotto.
 */
export function SCurve({ project, currency }: { project: EVProject; currency: string }) {
  const t = useT();
  // Il viewBox segue la larghezza reale: il testo resta leggibile anche sul telefono.
  const box = useRef<HTMLDivElement>(null);
  const [W, setW] = useState(720);
  useEffect(() => {
    const el = box.current;
    if (!el) return;
    const ro = new ResizeObserver(([e]) => setW(Math.max(300, Math.round(e.contentRect.width))));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  const H = W < 500 ? 220 : 280;
  const pad = { l: 52, r: 34, t: 16, b: 28 };
  const ref = useRef<SVGSVGElement>(null);
  const [hover, setHover] = useState<number | null>(null);
  const day = (s: string) => Date.parse(s + "T00:00:00Z");
  const data = useMemo(() => {
    const pv: Pt[] = (project.curve ?? []).map((p) => ({ t: day(p.date), v: p.pv_cents }));
    const snaps = project.snapshots ?? [];
    const ev: Pt[] = snaps.map((s) => ({ t: day(s.period_end), v: s.ev_cents }));
    const ac: Pt[] = snaps.map((s) => ({ t: day(s.period_end), v: s.ac_cents }));
    const ts = [...pv, ...ev].map((p) => p.t);
    const now = Date.now();
    const t0 = Math.min(...ts, now);
    const t1 = Math.max(...ts, now);
    const vmax = Math.max(project.bac_cents, ...pv.map((p) => p.v), ...ev.map((p) => p.v), ...ac.map((p) => p.v), 1) * 1.08;
    return { pv, ev, ac, t0, t1: t1 === t0 ? t0 + 86_400_000 : t1, vmax, now };
  }, [project]);
  const x = (t: number) => pad.l + ((t - data.t0) / (data.t1 - data.t0)) * (W - pad.l - pad.r);
  const y = (v: number) => H - pad.b - (v / data.vmax) * (H - pad.t - pad.b);
  const path = (pts: Pt[]) => pts.map((p, i) => `${i ? "L" : "M"}${x(p.t).toFixed(1)},${y(p.v).toFixed(1)}`).join("");

  // Tacche: una per trimestre se il piano è lungo, altrimenti una per mese.
  const ticks = useMemo(() => {
    const out: number[] = [];
    const months = (data.t1 - data.t0) / (30 * 86_400_000);
    const fit = Math.max(2, Math.floor((W - 86) / 48)); // etichette di mese che stanno nella larghezza
    const step = [1, 2, 3, 6, 12].find((s) => months / s <= fit) ?? 12;
    const d = new Date(data.t0);
    let m = Date.UTC(d.getUTCFullYear(), d.getUTCMonth() + 1, 1);
    while (m <= data.t1 && out.length < 40) {
      if (new Date(m).getUTCMonth() % step === 0) out.push(m);
      const n = new Date(m);
      m = Date.UTC(n.getUTCFullYear(), n.getUTCMonth() + 1, 1);
    }
    return out;
  }, [data, W]);
  const yTicks = [0, 0.25, 0.5, 0.75, 1].map((f) => f * project.bac_cents);
  const snaps = project.snapshots ?? [];
  const hv = hover != null ? snaps[hover] : null;
  const pvAt = (t: number) => {
    const pv = data.pv;
    if (!pv.length) return null;
    if (t <= pv[0].t) return pv[0].v;
    for (let i = 1; i < pv.length; i++) {
      if (t <= pv[i].t) {
        const a = pv[i - 1];
        const b = pv[i];
        return a.v + ((b.v - a.v) * (t - a.t)) / Math.max(b.t - a.t, 1);
      }
    }
    return pv[pv.length - 1].v;
  };

  function onMove(clientX: number) {
    const svg = ref.current;
    if (!svg || !snaps.length) return;
    const r = svg.getBoundingClientRect();
    const sx = ((clientX - r.left) / r.width) * W;
    let best = 0;
    let dist = Infinity;
    snaps.forEach((s, i) => {
      const dd = Math.abs(x(day(s.period_end)) - sx);
      if (dd < dist) {
        dist = dd;
        best = i;
      }
    });
    setHover(best);
  }

  const last = (pts: Pt[]) => (pts.length ? pts[pts.length - 1] : null);
  const labels = [
    { key: "PV", p: last(data.pv), cls: "fill-muted" },
    { key: "EV", p: last(data.ev), cls: "fill-accent" },
    { key: "AC", p: last(data.ac), cls: "fill-fg" },
  ]
    .filter((l) => l.p)
    .sort((a, b) => y(a.p!.v) - y(b.p!.v));
  // Le etichette dirette non si sovrappongono: almeno 12 px tra l'una e l'altra.
  let prevY = -Infinity;
  const placed = labels.map((l) => {
    const yy = Math.max(y(l.p!.v) + 4, prevY + 12);
    prevY = yy;
    return { ...l, yy };
  });

  return (
    <figure className="m-0 flex flex-col gap-2">
      <figcaption className="flex flex-wrap items-center gap-x-4 gap-y-1 text-[12px]">
        <span className="flex items-center gap-1.5">
          <svg width="22" height="8" aria-hidden="true"><line x1="0" y1="4" x2="22" y2="4" className="stroke-muted" strokeWidth="2" strokeDasharray="4 3" /></svg>
          {t("budget.fui.pv")}
        </span>
        <span className="flex items-center gap-1.5">
          <svg width="22" height="8" aria-hidden="true"><line x1="0" y1="4" x2="22" y2="4" className="stroke-accent" strokeWidth="2" /><rect x="8" y="1" width="6" height="6" className="fill-accent" /></svg>
          {t("budget.fui.ev")}
        </span>
        <span className="flex items-center gap-1.5">
          <svg width="22" height="8" aria-hidden="true"><line x1="0" y1="4" x2="22" y2="4" className="stroke-fg" strokeWidth="2" /></svg>
          {t("budget.fui.ac")}
        </span>
        <span className="text-muted">BAC {money(project.bac_cents, currency)}</span>
      </figcaption>
      <div className="relative" ref={box}>
        <svg
          ref={ref}
          viewBox={`0 0 ${W} ${H}`}
          className="block h-auto w-full touch-pan-y select-none"
          role="img"
          aria-label={t("budget.fui.sCurve", { key: project.key })}
          onPointerMove={(e) => onMove(e.clientX)}
          onPointerDown={(e) => onMove(e.clientX)}
          onPointerLeave={() => setHover(null)}
        >
          {yTicks.map((v, i) => (
            <g key={i}>
              <line x1={pad.l} x2={W - pad.r} y1={y(v)} y2={y(v)} className={i === 4 ? "stroke-rule" : "stroke-hair"} strokeWidth="1" strokeDasharray={i === 4 ? "2 3" : undefined} />
              <text x={pad.l - 6} y={y(v) + 3.5} textAnchor="end" className="fill-muted font-mono text-[10.5px]">
                {i === 4 ? "BAC" : money(v, currency)}
              </text>
            </g>
          ))}
          {ticks.map((t) => (
            <g key={t}>
              <line x1={x(t)} x2={x(t)} y1={H - pad.b} y2={H - pad.b + 4} className="stroke-rule" />
              <text x={x(t)} y={H - pad.b + 16} textAnchor="middle" className="fill-muted font-mono text-[10.5px]">
                {new Date(t).toLocaleDateString(loc(), { month: "short", timeZone: "UTC" })}
                {new Date(t).getUTCMonth() === 0 ? ` ${String(new Date(t).getUTCFullYear()).slice(2)}` : ""}
              </text>
            </g>
          ))}
          <line x1={pad.l} x2={W - pad.r} y1={H - pad.b} y2={H - pad.b} className="stroke-rule" strokeWidth="1" />
          {data.now >= data.t0 && data.now <= data.t1 && (
            <g>
              <line x1={x(data.now)} x2={x(data.now)} y1={pad.t} y2={H - pad.b} className="stroke-rule" strokeWidth="1" strokeDasharray="1 3" />
              <text x={x(data.now) + 4} y={pad.t + 9} className="fill-muted font-mono text-[10px]">{t("budget.fui.today")}</text>
            </g>
          )}
          <path d={path(data.pv)} fill="none" className="stroke-muted" strokeWidth="2" strokeDasharray="5 4" />
          <path d={path(data.ac)} fill="none" className="stroke-fg" strokeWidth="2" />
          <path d={path(data.ev)} fill="none" className="stroke-accent" strokeWidth="2" />
          {data.ev.map((p, i) => (
            <rect key={i} x={x(p.t) - 4} y={y(p.v) - 4} width="8" height="8" className="fill-accent stroke-bg" strokeWidth="2" />
          ))}
          {data.ac.map((p, i) => (
            <circle key={i} cx={x(p.t)} cy={y(p.v)} r="3.5" className="fill-fg stroke-bg" strokeWidth="2" />
          ))}
          {placed.map((l) => (
            <text key={l.key} x={Math.min(x(l.p!.t) + 8, W - pad.r + 8)} y={l.yy} className={`${l.cls} font-mono text-[11px] font-semibold`}>
              {l.key}
            </text>
          ))}
          {hv && (
            <line x1={x(day(hv.period_end))} x2={x(day(hv.period_end))} y1={pad.t} y2={H - pad.b} className="stroke-accent" strokeWidth="1" />
          )}
        </svg>
        {hv && (
          <div
            className="pointer-events-none absolute top-2 z-10 min-w-44 border border-rule bg-bg px-2.5 py-2 text-[12px] tabular"
            style={{ left: `${(x(day(hv.period_end)) / W) * 100 > 60 ? 4 : 56}%` }}
          >
            <div className="font-semibold">{new Date(day(hv.period_end)).toLocaleDateString(loc(), { month: "long", year: "numeric", timeZone: "UTC" })}</div>
            <dl className="m-0 mt-1 grid grid-cols-[auto_1fr] gap-x-3">
              <dt className="text-muted">PV</dt><dd className="m-0 text-right font-mono">{money(hv.pv_cents, currency)}</dd>
              <dt className="text-muted">EV</dt><dd className="m-0 text-right font-mono">{money(hv.ev_cents, currency)}</dd>
              <dt className="text-muted">AC</dt><dd className="m-0 text-right font-mono">{money(hv.ac_cents, currency)}</dd>
              <dt className="text-muted">CPI · SPI</dt><dd className="m-0 text-right font-mono">{ratio(hv.cpi)} · {ratio(hv.spi)}</dd>
            </dl>
          </div>
        )}
      </div>
      {pvAt(data.now) == null && <p className="m-0 text-[12px] text-muted">{t("budget.fui.noPv")}</p>}
    </figure>
  );
}
