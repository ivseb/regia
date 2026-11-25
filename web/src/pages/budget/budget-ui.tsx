// Pezzi comuni del budget gerarchico: importi, barra di un nodo, periodi e stati (replica/budget.md §6).
import type { ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { useT } from "../../i18n/react";
import { fmtCurrency, fmtNumber, loc } from "../../i18n/format";
import { financeApi, type AllocationNode, type BudgetPeriod, type PeriodStatus } from "../../api";
import { useMe } from "../../shell";

/** Valuta base: la finanza la legge dalle impostazioni, gli altri usano l'euro. */
export function useCurrency(): string {
  const me = useMe().data;
  const perms = me?.active_tenant?.permissions ?? [];
  const finance = perms.includes("*") || perms.includes("finance.read");
  const ov = useQuery({ queryKey: ["finance", "overview"], queryFn: financeApi.overview, enabled: finance, retry: false });
  return ov.data?.settings.base_currency ?? "EUR";
}

/** Importo in centesimi, intero, nella lingua corrente. */
export function mny(cents: number | null | undefined, currency = "EUR"): string {
  if (cents == null) return "—";
  return fmtCurrency(cents / 100, currency, { maximumFractionDigits: 0 });
}

/** Importo preciso (con i centesimi) per le anteprime e la storia. */
export function mnyPrecise(cents: number | null | undefined, currency = "EUR"): string {
  if (cents == null) return "—";
  return fmtCurrency(cents / 100, currency, { minimumFractionDigits: 2, maximumFractionDigits: 2 });
}

/** Differenza con il segno. */
export function signed(cents: number, currency = "EUR"): string {
  return `${cents > 0 ? "+" : ""}${mny(cents, currency)}`;
}

/** Importo da scrivere in un campo: centinaia di euro senza raggruppamento, nella lingua corrente. */
export function toInput(cents: number | null | undefined): string {
  return cents == null ? "" : (cents / 100).toLocaleString(loc(), { maximumFractionDigits: 2, useGrouping: false });
}

/** Legge un importo scritto come «1.234,50» o «1,234.50» o «1234.5» in centesimi; null se non valido. */
export function fromInput(s: string): number | null {
  const t = s.replace(/[\s€$£]/g, "");
  if (!t) return null;
  const lastComma = t.lastIndexOf(",");
  const lastDot = t.lastIndexOf(".");
  let norm = t;
  if (lastComma >= 0 && lastDot >= 0) {
    // L'ultimo separatore è il decimale, l'altro raggruppa.
    norm = lastComma > lastDot ? t.replace(/\./g, "").replace(",", ".") : t.replace(/,/g, "");
  } else if (lastComma >= 0) {
    norm = /,\d{1,2}$/.test(t) ? t.replace(/\./g, "").replace(",", ".") : t.replace(/,/g, "");
  } else if (lastDot >= 0 && /\.\d{3}$/.test(t) && t.split(".").length > 2) {
    norm = t.replace(/\./g, "");
  }
  const n = Number(norm);
  return Number.isFinite(n) ? Math.round(n * 100) : null;
}

export const pctText = (v: number) => `${fmtNumber(v, { maximumFractionDigits: 1 })}%`;

/** Barra di un nodo: speso in --fg, impegnato non ancora speso in --muted, il resto dello stanziato libero; oltre lo stanziato in --danger. */
export function NodeBar({ n, className = "" }: { n: Pick<AllocationNode, "allocated_cents" | "committed_cents" | "spent_cents">; className?: string }) {
  const t = useT();
  const scale = Math.max(n.allocated_cents, n.committed_cents, 1);
  const w = (c: number) => `${(100 * Math.max(c, 0)) / scale}%`;
  const spent = Math.min(n.spent_cents, n.committed_cents);
  const committed = Math.max(n.committed_cents - spent, 0);
  const over = n.committed_cents > n.allocated_cents;
  const usedPct = n.allocated_cents > 0 ? Math.round((100 * n.committed_cents) / n.allocated_cents) : n.committed_cents > 0 ? 100 : 0;
  return (
    <span
      role="img"
      aria-label={t("budget.bar.label", { used: usedPct })}
      className={`relative block h-2 min-w-16 bg-hair ${className}`}
    >
      <span className="absolute inset-0 flex gap-px">
        <span className={`h-full ${over ? "bg-danger" : "bg-fg"}`} style={{ width: w(spent) }} />
        <span className={`h-full ${over ? "bg-danger/60" : "bg-muted"}`} style={{ width: w(committed) }} />
      </span>
    </span>
  );
}

export function PeriodStatusLabel({ status }: { status: PeriodStatus }) {
  const t = useT();
  const cls = status === "open" ? "text-ok" : status === "closing" ? "text-flag" : "text-muted";
  return <span className={`label whitespace-nowrap ${cls}`}>{t(`budget.period.status.${status}`)}</span>;
}

/** Periodi raggruppati per esercizio (per la scelta in testa alle pagine). */
export function periodOptions(periods: BudgetPeriod[]) {
  return periods.map((p) => ({ value: p.id, label: p.fiscal_year === p.name ? p.name : `${p.fiscal_year} · ${p.name}` }));
}

/** Periodo da proporre per primo: quello aperto che contiene oggi, poi il primo aperto, poi l'ultimo. */
export function defaultPeriod(periods: BudgetPeriod[]): BudgetPeriod | undefined {
  const today = new Date().toISOString().slice(0, 10);
  return (
    periods.find((p) => p.status !== "closed" && p.start_date <= today && p.end_date >= today) ??
    periods.find((p) => p.status === "open" || p.status === "closing") ??
    periods[periods.length - 1]
  );
}

/** Campo con etichetta sopra (l'etichetta avvolge il controllo: il nome accessibile viene da lei). */
export function Field({ label, children, wide, className = "" }: { label: string; children: ReactNode; wide?: boolean; className?: string }) {
  return (
    <label className={`flex min-w-0 flex-col gap-1 ${wide ? "sm:col-span-2" : ""} ${className}`}>
      <span className="label text-muted">{label}</span>
      {children}
    </label>
  );
}
