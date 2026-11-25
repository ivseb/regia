// Pezzi comuni delle pagine dei rischi e degli incidenti (piano, passo 20): heatmap in SVG,
// conto alla rovescia delle notifiche normative, livello di rischio, righe di dati.
import { useEffect, useState, type ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import type { Band, Cell, Matrix, Ref, Score, Tone } from "../riskApi";
import { tr } from "../i18n/core";
import { loc } from "../i18n/format";

/** Colore del tono del tema (le fasce non hanno colori propri: decide il tema attivo). */
const TONE_VAR: Record<Tone, string> = { ok: "var(--ok)", neutral: "var(--muted)", accent: "var(--accent)", danger: "var(--danger)" };

/** Sfondo di una fascia: il tono mescolato al fondo dei campi, più intenso per le fasce alte. */
export function bandFill(m: Matrix, bandKey: string, faint = false): string {
  const idx = Math.max(0, m.bands.findIndex((b) => b.key === bandKey));
  const b: Band | undefined = m.bands[idx];
  const tone = b ? TONE_VAR[b.tone] : "var(--hair)";
  const pct = 14 + Math.round((40 * idx) / Math.max(1, m.bands.length - 1));
  return `color-mix(in srgb, ${tone} ${faint ? Math.round(pct / 2) : pct}%, var(--field))`;
}

/** Livello di rischio: punteggio e nome della fascia, con il quadratino del tono. */
export function ScoreBadge({ score, matrix, compact }: { score: Score | null | undefined; matrix?: Matrix; compact?: boolean }) {
  if (!score) return <span className="text-muted">—</span>;
  return (
    <span className="inline-flex items-center gap-1.5 whitespace-nowrap" title={`${score.likelihood_name} × ${score.impact_name}`}>
      <span
        aria-hidden="true"
        className="inline-block size-2.5 border border-hair"
        style={{ background: matrix ? bandFill(matrix, score.band) : TONE_VAR[score.tone] }}
      />
      <span className="font-mono tabular">{score.score}</span>
      {!compact && <span className={score.tone === "danger" ? "font-semibold" : ""}>{score.band_name}</span>}
      {score.over_appetite && <span className="text-[11px] font-semibold text-danger">{tr("governance.riskui.oltre_soglia")}</span>}
    </span>
  );
}

/**
 * Heatmap della matrice: righe = probabilità (dall'alto la più alta), colonne = impatto.
 * In SVG con viewBox, quindi si adatta alla larghezza; i nomi dei livelli stanno sotto, in
 * HTML, per restare leggibili anche a 375 px.
 */
export type HeatArrow = { key: string; from: { l: number; i: number }; to: { l: number; i: number } };

export function Heatmap({ matrix, cells, selected, onSelect, label, title, legend = true, arrows }: {
  matrix: Matrix;
  cells: Cell[];
  selected?: { l: number; i: number } | null;
  onSelect?: (c: Cell | null) => void;
  label: string;
  title?: string;
  legend?: boolean;
  /** Frecce dal rischio inerente al residuo dei rischi in vista (la selezione). */
  arrows?: HeatArrow[];
}) {
  const L = matrix.likelihood.length;
  const I = matrix.impact.length;
  const size = 64;
  const gap = 3;
  const axis = 26;
  const w = axis + I * (size + gap);
  const h = L * (size + gap) + axis;
  const total = cells.reduce((n, c) => n + c.count, 0);
  const cell = (l: number, i: number) => cells.find((c) => c.likelihood === l && c.impact === i);
  return (
    <figure className="m-0 flex min-w-0 flex-col gap-2">
      {title && <h4 className="label m-0">{title}</h4>}
      <svg
        viewBox={`0 0 ${w} ${h}`}
        className="block h-auto w-full max-w-[460px]"
        role="img"
        aria-label={tr("governance.riskui.rischi_aperti_sulla_matrice_per", { label, count: total, L, I })}
      >
        {Array.from({ length: L }, (_, r) => {
          const l = L - r;
          const y = r * (size + gap);
          return (
            <g key={l}>
              <text x={axis - 8} y={y + size / 2 + 4} textAnchor="end" fontSize="12" fill="var(--muted)" className="tabular">
                {l}
              </text>
              {Array.from({ length: I }, (_, c) => {
                const i = c + 1;
                const x = axis + c * (size + gap);
                const it = cell(l, i);
                const n = it?.count ?? 0;
                const sel = selected?.l === l && selected?.i === i;
                return (
                  <g
                    key={i}
                    role={onSelect ? "button" : undefined}
                    tabIndex={onSelect && n > 0 ? 0 : undefined}
                    aria-label={tr("governance.riskui.probabilita_impatto_rischi", { l, i, count: n })}
                    onClick={() => onSelect?.(n > 0 && !sel && it ? it : null)}
                    onKeyDown={(e) => {
                      if ((e.key === "Enter" || e.key === " ") && it && n > 0) {
                        e.preventDefault();
                        onSelect?.(sel ? null : it);
                      }
                    }}
                    style={{ cursor: onSelect && n > 0 ? "pointer" : "default" }}
                  >
                    <title>{`${matrix.likelihood[l - 1]?.name} × ${matrix.impact[i - 1]?.name} = ${l * i}${n ? ` · ${n} rischi` : ""}`}</title>
                    <rect
                      x={x}
                      y={y}
                      width={size}
                      height={size}
                      fill={bandFill(matrix, it?.band ?? "", n === 0)}
                      stroke={sel ? "var(--fg)" : "none"}
                      strokeWidth={sel ? 3 : 0}
                    />
                    <text x={x + 6} y={y + 14} fontSize="10" fill="var(--muted)" className="tabular">
                      {l * i}
                    </text>
                    {n > 0 && (
                      <text x={x + size / 2} y={y + size / 2 + 9} textAnchor="middle" fontSize="24" fontWeight="700" fill="var(--fg)" className="tabular">
                        {n}
                      </text>
                    )}
                  </g>
                );
              })}
            </g>
          );
        })}
        {arrows && arrows.length > 0 && (
          <g aria-hidden="true" className="pointer-events-none">
            <defs>
              <marker id="hm-arrow" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
                <path d="M0,0 L10,5 L0,10 z" fill="var(--fg)" />
              </marker>
            </defs>
            {arrows.map((a, n) => {
              // Le frecce con la stessa partenza si aprono un poco, perché non si sovrappongano.
              const off = ((n % 5) - 2) * 4;
              const cx = (p: { l: number; i: number }) => axis + (p.i - 1) * (size + gap) + size / 2 + off;
              const cy = (p: { l: number; i: number }) => (L - p.l) * (size + gap) + size / 2 + off;
              return <line key={a.key} x1={cx(a.from)} y1={cy(a.from)} x2={cx(a.to)} y2={cy(a.to)} stroke="var(--fg)" strokeWidth={2} markerEnd="url(#hm-arrow)" />;
            })}
          </g>
        )}
        {Array.from({ length: I }, (_, c) => (
          <text key={c} x={axis + c * (size + gap) + size / 2} y={L * (size + gap) + 16} textAnchor="middle" fontSize="12" fill="var(--muted)" className="tabular">
            {c + 1}
          </text>
        ))}
      </svg>
      {legend && <figcaption className="flex flex-col gap-1 text-[12px] text-muted">
        <span>
          <span className="label mr-1.5">{tr("governance.riskui.probabilita")}</span>
          {matrix.likelihood.map((x) => `${x.value} ${x.name}`).join(" · ")}
        </span>
        <span>
          <span className="label mr-1.5">{tr("governance.riskui.impatto")}</span>
          {matrix.impact.map((x) => `${x.value} ${x.name}`).join(" · ")}
        </span>
        <span className="flex flex-wrap gap-x-3 gap-y-1">
          {matrix.bands.map((b) => (
            <span key={b.key} className="inline-flex items-center gap-1">
              <span aria-hidden="true" className="inline-block size-2.5 border border-hair" style={{ background: bandFill(matrix, b.key) }} />
              {b.name} <span className="font-mono tabular">{b.min}–{b.max}</span>
            </span>
          ))}
          {matrix.appetite_score != null && <span>{tr("governance.riskui.soglia_di_attenzione_oltre", { appetite_score: matrix.appetite_score })}</span>}
        </span>
      </figcaption>}
    </figure>
  );
}

/** Tempo che manca a una scadenza, aggiornato ogni 30 secondi: arancione (obbligo normativo), rosso se passata. */
export function Countdown({ due, done }: { due: string | null; done?: boolean }) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!due || done) return;
    const t = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(t);
  }, [due, done]);
  if (!due) return <span className="text-[12.5px] text-muted">{tr("governance.riskui.in_attesa_della_tappa_precedente")}</span>;
  if (done) return null;
  const ms = new Date(due).getTime() - now;
  const abs = Math.abs(ms);
  const d = Math.floor(abs / 86_400_000);
  const h = Math.floor((abs % 86_400_000) / 3_600_000);
  const m = Math.floor((abs % 3_600_000) / 60_000);
  const txt = d > 0 ? `${d} g ${h} h` : h > 0 ? tr("governance.riskui.h_min", { h, m }) : tr("governance.riskui.min", { m });
  return (
    <span className={`font-mono font-semibold tabular ${ms < 0 ? "text-danger" : "text-flag"}`} role="timer" aria-live="off">
      {ms < 0 ? tr("governance.riskui.scaduta_da", { txt }) : tr("governance.riskui.mancano", { txt })}
    </span>
  );
}

export function dateTime(iso: string | null | undefined): string {
  if (!iso) return "—";
  return new Date(iso).toLocaleString(loc(), { day: "2-digit", month: "2-digit", year: "numeric", hour: "2-digit", minute: "2-digit" });
}

/** Valore di <input type="datetime-local"> da una data ISO, nell'ora locale. */
export function toLocalInput(iso: string | null | undefined): string {
  if (!iso) return "";
  const d = new Date(iso);
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`;
}

/** Da <input type="datetime-local"> (ora locale) a ISO con il fuso. */
export function fromLocalInput(v: string): string | undefined {
  return v ? new Date(v).toISOString() : undefined;
}

/** Riga di una lista di dati: etichetta e valore. */
export function Fact({ k, children }: { k: string; children: ReactNode }) {
  return (
    <>
      <dt className="border-b border-hair py-1.5 pr-3 text-muted">{k}</dt>
      <dd className="m-0 min-w-0 border-b border-hair py-1.5 break-words">{children}</dd>
    </>
  );
}

export function Facts({ children }: { children: ReactNode }) {
  return <dl className="m-0 grid grid-cols-[minmax(96px,36%)_minmax(0,1fr)] text-[13px]">{children}</dl>;
}

/** Elenco di elementi collegati con chiave e titolo; on Remove mostra "scollega". */
export function RefList({ items, empty, onRemove, busy }: { items: Ref[]; empty: string; onRemove?: (r: Ref) => void; busy?: boolean }) {
  if (items.length === 0) return <p className="m-0 text-[12.5px] text-muted">{empty}</p>;
  return (
    <ul className="m-0 flex list-none flex-col p-0">
      {items.map((r) => (
        <li key={r.id} className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5 border-b border-hair py-1.5 text-[13px]">
          <Link to="/e/$key" params={{ key: r.key }} className="w-20 shrink-0 font-mono text-[12px] text-accent no-underline hover:underline">
            {r.key}
          </Link>
          <span className="min-w-[11rem] max-w-full flex-1">
            {r.title}
            <span className="text-muted"> · {r.type_name}</span>
            {r.critical && (
              <span className="ml-1.5 border border-flag px-1 text-[10.5px] font-semibold text-flag" title={tr("governance.riskui.supporta_una_funzione_critica_o")}>
                DORA
              </span>
            )}
          </span>
          <span className="shrink-0 text-[12px] text-muted">{r.state}</span>
          {onRemove && (
            <button className="shrink-0 text-[12px] text-muted underline hover:text-danger" disabled={busy} onClick={() => onRemove(r)}>{tr("governance.riskui.scollega")}</button>
          )}
        </li>
      ))}
    </ul>
  );
}

export function hasPerm(perms: string[], p: string): boolean {
  return perms.includes("*") || perms.includes(p);
}

export function euroCents(c: number | null | undefined): string {
  if (c == null) return "—";
  return (c / 100).toLocaleString(loc(), { style: "currency", currency: "EUR", maximumFractionDigits: 0 });
}
