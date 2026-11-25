// Costruttore unico di condizioni (replica/usabilita.md, proposta 6): il modello a gruppi.
// Una condizione è una lista di alternative («oppure»); ogni alternativa è un gruppo di righe
// tutte vere («e»). È esattamente la forma del linguaggio di condizioni, quindi ogni testo
// valido e fatto di righe semplici si converte in gruppi e torna uguale, senza perdite.
// Funzioni pure, provate con `pnpm test` (web/test/condizioni.test.ts).
import { clauseRow, describeCondition, parseClauses, rowText, type CondRow } from "../pages/processEditor/model.ts";

export type CondGroups = CondRow[][];

/**
 * Le righe del costruttore, a gruppi. Restituisce null se la condizione non si esprime con
 * righe semplici (negazione di un confronto, valori misti): allora si modifica come testo.
 * La condizione vuota è un solo gruppo vuoto.
 */
export function toGroups(src: string): CondGroups | null {
  let alts;
  try {
    alts = parseClauses(src);
  } catch {
    return null;
  }
  if (alts.length === 0) return [[]];
  const groups: CondGroups = [];
  for (const alt of alts) {
    const rows: CondRow[] = [];
    for (const cl of alt) {
      const r = clauseRow(cl);
      if (!r) return null;
      rows.push(r);
    }
    groups.push(rows);
  }
  return groups;
}

/** Il testo della condizione dai gruppi: righe incomplete e gruppi vuoti si saltano. */
export function fromGroups(groups: CondGroups): string {
  return groups
    .map((g) => g.map(rowText).filter((x): x is string => !!x).join(" "))
    .filter(Boolean)
    .join(" oppure "); // i18n-ok: sintassi delle condizioni
}

/** Il numero di righe complete (quelle che finiscono nel testo). */
export function completeRows(groups: CondGroups): number {
  return groups.reduce((n, g) => n + g.filter((r) => rowText(r)).length, 0);
}

/**
 * La condizione in parole, per l'anteprima («Funzione critica è sì e Criticità è alta o critica»).
 * `names` associa le chiavi ai nomi dei dati; la sintassi non valida resta com'è.
 */
export function phrase(src: string, names?: Record<string, string>): string {
  return describeCondition(src, names);
}
