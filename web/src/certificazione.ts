// Logica non visiva della certificazione del rilascio e delle impostazioni dei rilasci
// (pages/Certificazione.tsx, pages/ReleaseSettings.tsx): ordine delle sezioni, gruppi degli
// elementi, numeri dei test, impronta abbreviata, corpo del salvataggio. Solo import di tipo:
// il modulo si prova con node:test (web/test/certificazione.test.ts).
import type { CertSection, NoteGroup, ReleaseItem, ReleaseSettingsInput } from "./rilasciApi";

/** Ordine dei gruppi degli elementi nella certificazione. */
export const ITEM_GROUPS: NoteGroup[] = ["new", "fix", "other"];

/** Gli elementi divisi in novità, correzioni e altro, nell'ordine dato; i gruppi vuoti non ci sono. */
export function groupItems(items: ReleaseItem[]): { group: NoteGroup; items: ReleaseItem[] }[] {
  return ITEM_GROUPS.map((group) => ({ group, items: items.filter((i) => (ITEM_GROUPS.includes(i.group) ? i.group : "other") === group) })).filter(
    (g) => g.items.length > 0,
  );
}

/** Le sezioni da mostrare: quelle accese, nell'ordine delle impostazioni, senza doppioni né sconosciute. */
export function visibleSections(sections: readonly string[], all: readonly CertSection[]): CertSection[] {
  const out: CertSection[] = [];
  for (const s of sections) if ((all as readonly string[]).includes(s) && !out.includes(s as CertSection)) out.push(s as CertSection);
  return out;
}

export type SectionRow = { key: CertSection; on: boolean };

/** Le righe dell'editor: prima le sezioni accese nel loro ordine, poi le spente nell'ordine predefinito. */
export function sectionRows(enabled: readonly string[], all: readonly CertSection[]): SectionRow[] {
  const on = visibleSections(enabled, all);
  return [...on.map((key) => ({ key, on: true })), ...all.filter((k) => !on.includes(k)).map((key) => ({ key, on: false }))];
}

/** Sposta la riga i di delta posti (−1 su, +1 giù); fuori dai bordi non cambia niente. */
export function moveRow<T>(rows: T[], i: number, delta: number): T[] {
  const j = i + delta;
  if (i < 0 || i >= rows.length || j < 0 || j >= rows.length) return rows;
  const next = rows.slice();
  [next[i], next[j]] = [next[j], next[i]];
  return next;
}

export type SpaceRow = { key: string; close: boolean };

/** La chiave di uno spazio come la scrive chi configura: maiuscola, senza spazi. */
export function spaceKey(s: string): string {
  return s.replace(/\s+/g, "").toUpperCase();
}

/** Il corpo del salvataggio: sezioni accese in ordine; eccezioni con chiave, senza doppioni (vale l'ultima). */
export function settingsInput(close: boolean, rows: SectionRow[], spaces: SpaceRow[]): ReleaseSettingsInput {
  const byKey = new Map<string, boolean>();
  for (const s of spaces) {
    const k = spaceKey(s.key);
    if (k) {
      byKey.delete(k);
      byKey.set(k, s.close);
    }
  }
  return {
    close_on_keywords: close,
    cert_sections: rows.filter((r) => r.on).map((r) => r.key),
    spaces: [...byKey].map(([key, c]) => ({ key, close_on_keywords: c })),
  };
}

/** I primi 12 caratteri dell'impronta SHA-256 (quella intera resta nel title e nel piè di stampa). */
export function shortHash(sha: string | null | undefined): string {
  return (sha ?? "").slice(0, 12);
}

/** Totali dei test già formattati, per la riga «Eseguiti … · superati …»; nullo se nessuna fonte dà i numeri. */
export function testTotals(
  t: { executed: number; passed: number; failed: number; skipped: number; counted: boolean },
  fmt: (n: number) => string = String,
): { executed: string; passed: string; failed: string; skipped: string } | null {
  if (!t.counted) return null;
  return { executed: fmt(t.executed), passed: fmt(t.passed), failed: fmt(t.failed), skipped: fmt(t.skipped) };
}

/** I numeri di un'esecuzione, «118/120» (superati su eseguiti) o «120» o vuoto se la fonte non li dà. */
export function runNumbers(r: { executed: number | null; passed: number | null }): string {
  if (r.executed == null) return r.passed == null ? "" : String(r.passed);
  return r.passed == null ? String(r.executed) : `${r.passed}/${r.executed}`;
}
