// Importazione delle voci di un catalogo da CSV (colonne key,title,text,category,method).
// Funzioni pure, senza interfaccia: l'anteprima e l'applicazione alla bozza stanno in CatalogEditor.
import type { CatalogItem } from "../../requirementsApi";

export type CsvRow = { line: number; item: CatalogItem; error?: "key" | "title" | "method" };
export const CSV_COLUMNS = ["key", "title", "text", "category", "method"] as const;
type Col = (typeof CSV_COLUMNS)[number];

/** Legge un CSV con virgolette; il separatore (virgola, punto e virgola o tab) si riconosce dalla prima riga. */
export function parseCsv(src: string): string[][] {
  const text = src.replace(/^﻿/, "");
  const first = text.split(/\r?\n/, 1)[0] ?? "";
  const sep = [",", ";", "\t"].map((s) => [s, first.split(s).length] as const).sort((a, b) => b[1] - a[1])[0][0];
  const rows: string[][] = [];
  let row: string[] = [];
  let cell = "";
  let quoted = false;
  for (let i = 0; i < text.length; i++) {
    const ch = text[i];
    if (quoted) {
      if (ch === '"' && text[i + 1] === '"') {
        cell += '"';
        i++;
      } else if (ch === '"') quoted = false;
      else cell += ch;
    } else if (ch === '"') quoted = true;
    else if (ch === sep) {
      row.push(cell);
      cell = "";
    } else if (ch === "\n" || ch === "\r") {
      if (ch === "\r" && text[i + 1] === "\n") i++;
      row.push(cell);
      cell = "";
      if (row.some((c) => c.trim() !== "")) rows.push(row);
      row = [];
    } else cell += ch;
  }
  row.push(cell);
  if (row.some((c) => c.trim() !== "")) rows.push(row);
  return rows;
}

/** Le voci del CSV. La prima riga può essere l'intestazione (se contiene «key» o «title»); senza, l'ordine è quello delle colonne. */
export function csvToItems(src: string): CsvRow[] {
  const rows = parseCsv(src);
  if (!rows.length) return [];
  const head = rows[0].map((c) => c.trim().toLowerCase());
  const hasHead = head.includes("key") || head.includes("title");
  const col = Object.fromEntries(CSV_COLUMNS.map((c, i) => [c, hasHead ? head.indexOf(c) : i])) as Record<Col, number>;
  return rows.slice(hasHead ? 1 : 0).map((r, i) => {
    const get = (c: Col) => (col[c] >= 0 ? (r[col[c]] ?? "").trim() : "");
    const method = get("method").toLowerCase();
    const key = get("key");
    const title = get("title");
    const item: CatalogItem = { key, title, verification: { method: method === "evidence" ? "evidence" : "manual" } };
    if (get("text")) item.text = get("text");
    if (get("category")) item.category = get("category");
    const error = !key ? "key" : !title ? "title" : method && method !== "manual" && method !== "evidence" ? "method" : undefined;
    return { line: i + (hasHead ? 2 : 1), item, error };
  });
}

/** Chi verifica nell'ultima voce che lo indica: le voci nuove partono da lì (il server vuole un team o un ruolo). */
export function lastVerifier(items: CatalogItem[]): { team?: string; role?: string } {
  for (let i = items.length - 1; i >= 0; i--) {
    const { team, role } = items[i].verification;
    if (team) return { team };
    if (role) return { role };
  }
  return {};
}

/** Applica le righe valide alla bozza: la chiave già presente aggiorna titolo, testo, categoria e metodo (il resto resta); le altre si aggiungono in coda. */
export function mergeItems(items: CatalogItem[], rows: CsvRow[]): CatalogItem[] {
  const who = lastVerifier(items);
  const out = [...items];
  for (const { item, error } of rows) {
    if (error) continue;
    const at = out.findIndex((x) => x.key === item.key);
    if (at < 0) {
      out.push({ ...item, verification: { ...who, ...item.verification } });
      continue;
    }
    const cur = out[at];
    out[at] = { ...cur, title: item.title, text: item.text ?? cur.text, category: item.category ?? cur.category, verification: { ...cur.verification, method: item.verification.method } };
  }
  return out;
}
