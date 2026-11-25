// Differenze tra la versione pubblicata e la bozza, in parole («Cosa cambia»). Funzioni pure,
// provate con `pnpm test` (web/test/bozza.test.ts).
import { tr } from "../i18n/core.ts";

/** Una modifica leggibile: che cosa, com'era e com'è ora. */
export type Change = { label: string; before: string; after: string };

/** Un valore in parole: sì/no per i booleani, un trattino per il vuoto, l'elenco con virgole. */
export function show(v: unknown): string {
  if (v === undefined || v === null || v === "") return "—";
  if (typeof v === "boolean") return v ? tr("casa.bozza.yes") : tr("casa.bozza.no");
  if (Array.isArray(v)) return v.length ? v.map(show).join(", ") : "—";
  if (typeof v === "object") return JSON.stringify(v);
  return String(v);
}

/** Campi con il loro prima e dopo: restano solo quelli che cambiano. */
export function diffFields(fields: { label: string; before: unknown; after: unknown; format?: (v: unknown) => string }[]): Change[] {
  const out: Change[] = [];
  for (const f of fields) {
    const fmt = f.format ?? show;
    const norm = (v: unknown) => JSON.stringify(v === "" ? null : (v ?? null));
    if (norm(f.before) === norm(f.after)) continue;
    out.push({ label: f.label, before: fmt(f.before), after: fmt(f.after) });
  }
  return out;
}

/**
 * Elenchi di voci con un nome (regole, passi, ambienti): aggiunte, tolte e cambiate, ognuna con
 * una frase. `describe` riassume una voce in una riga; `id` è la sua identità tra le due versioni.
 */
export function diffList<T>(label: string, before: T[], after: T[], id: (x: T, i: number) => string, name: (x: T) => string, describe: (x: T) => string): Change[] {
  const out: Change[] = [];
  const b = new Map(before.map((x, i) => [id(x, i), x]));
  const a = new Map(after.map((x, i) => [id(x, i), x]));
  for (const [k, x] of a) {
    const old = b.get(k);
    if (!old) out.push({ label: `${label}: ${name(x)}`, before: tr("casa.bozza.absent"), after: describe(x) });
    else if (describe(old) !== describe(x)) out.push({ label: `${label}: ${name(x)}`, before: describe(old), after: describe(x) });
  }
  for (const [k, x] of b) if (!a.has(k)) out.push({ label: `${label}: ${name(x)}`, before: describe(x), after: tr("casa.bozza.removed") });
  return out;
}
