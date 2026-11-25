// Nomi per lingua di fasi e opzioni dei campi (dati del tenant). Il nome resta canonico: valori inviati
// all'API, condizioni e processi lo citano sempre; l'etichetta della lingua corrente è solo testo mostrato.
import { getLang } from "./core";

export type Labels = Record<string, string> | null | undefined;

/** Il nome da mostrare: l'etichetta della lingua corrente, altrimenti il nome stesso. */
export function localName(name: string, labels?: Labels, lang: string = getLang()): string {
  const l = labels?.[lang]?.trim();
  return l ? l : name;
}
