// Date, numeri, valute e durate con Intl, nella lingua della persona. Niente «it-IT» scritto
// a mano nei componenti: si usa loc() o una di queste funzioni.
import { getLang, lookup } from "./core.ts";
import { LANGUAGES } from "./languages.ts";

/** Il locale BCP 47 della lingua corrente («it-IT», «en-US»), per Intl e toLocale*String. */
export function loc(): string {
  const l = getLang();
  return LANGUAGES.find((x) => x.key === l)?.locale ?? "it-IT";
}

type Dateish = string | number | Date | null | undefined;
const asDate = (v: Dateish): Date | null => {
  if (v == null || v === "") return null;
  const d = v instanceof Date ? v : new Date(v);
  return Number.isNaN(d.getTime()) ? null : d;
};

/** Data breve: 25/11/2026 oppure 11/25/2026. Vuota o non valida: «—». */
export function fmtDate(v: Dateish, opts?: Intl.DateTimeFormatOptions): string {
  const d = asDate(v);
  return d ? d.toLocaleDateString(loc(), opts) : "—";
}

/** Data e ora con giorno, mese e anno a cifre e ora a due cifre. */
export function fmtDateTime(v: Dateish, opts?: Intl.DateTimeFormatOptions): string {
  const d = asDate(v);
  return d ? d.toLocaleString(loc(), opts ?? { day: "2-digit", month: "2-digit", year: "numeric", hour: "2-digit", minute: "2-digit" }) : "—";
}

export function fmtTime(v: Dateish, opts?: Intl.DateTimeFormatOptions): string {
  const d = asDate(v);
  return d ? d.toLocaleTimeString(loc(), opts ?? { hour: "2-digit", minute: "2-digit" }) : "—";
}

export function fmtNumber(n: number | null | undefined, opts?: Intl.NumberFormatOptions): string {
  return n == null || Number.isNaN(n) ? "—" : n.toLocaleString(loc(), opts);
}

/** Importo in una valuta (EUR se manca): 1.234,50 € oppure €1,234.50. */
export function fmtCurrency(n: number | null | undefined, currency = "EUR", opts?: Intl.NumberFormatOptions): string {
  if (n == null || Number.isNaN(n)) return "—";
  try {
    return n.toLocaleString(loc(), { style: "currency", currency, ...opts });
  } catch {
    return `${n.toLocaleString(loc())} ${currency}`;
  }
}

/** Tempo relativo («2 ore fa», «2 hours ago», «ieri»). Per differenze oltre un mese, la data. */
export function fmtRelative(v: Dateish): string {
  const d = asDate(v);
  if (!d) return "—";
  const diff = (d.getTime() - Date.now()) / 1000;
  const abs = Math.abs(diff);
  if (abs < 60) return lookup("common.now") ?? "now";
  const rtf = new Intl.RelativeTimeFormat(loc(), { numeric: "auto" });
  if (abs < 3600) return rtf.format(Math.round(diff / 60), "minute");
  if (abs < 86400) return rtf.format(Math.round(diff / 3600), "hour");
  if (abs < 86400 * 30) return rtf.format(Math.round(diff / 86400), "day");
  return d.toLocaleDateString(loc());
}

/**
 * Durata in minuti nella forma breve del prodotto: 450 → «7h 30m», 0 → «0m». Le sigle di ore e
 * minuti sono uguali nelle due lingue; i giorni (8 ore di lavoro) seguono il catalogo («g», «d»).
 */
export function fmtDuration(minutes: number, opts: { days?: boolean } = {}): string {
  const dayMin = 8 * 60;
  const dUnit = lookup("common.unit.day") ?? "d";
  let rest = Math.round(minutes);
  const d = opts.days ? Math.floor(rest / dayMin) : 0;
  rest -= d * dayMin;
  const h = Math.floor(rest / 60);
  const m = rest - h * 60;
  return [d ? `${d}${dUnit}` : "", h ? `${h}h` : "", m ? `${m}m` : ""].filter(Boolean).join(" ") || "0m";
}
