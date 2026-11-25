// Catalogo dei testi e funzione t(), scritti in casa (replica/lingua.md): nessuna libreria.
// Le chiavi sono controllate da TypeScript contro il catalogo italiano (la sorgente); la
// lingua inglese si carica solo se serve, con un import dinamico.
//
// Dentro i componenti si usa useT() (react.ts); fuori da React, tr() qui sotto.
import type { Entry, Lang, Params } from "./types";
import { DEFAULT_LANG, browserLang, isLang } from "./languages.ts";
import type itCatalog from "./it/index.ts";

/** Chiavi note del catalogo: una chiave scritta male o mancante è un errore di compilazione. */
export type Key = keyof typeof itCatalog;
export type { Lang, Params };

type Catalog = Record<string, Entry>;
const loaders: Record<Lang, () => Promise<{ default: Catalog }>> = {
  it: () => import("./it/index.ts"),
  en: () => import("./en/index.ts"),
};
const loaded = new Map<Lang, Catalog>();

/** Scelta della lingua ricordata nel browser: serve prima che arrivi /me (accesso, primo disegno). */
const STORE = "regia.lingua";

let lang: Lang = DEFAULT_LANG;
let catalog: Catalog = {};

/** Cambia a ogni cambio di lingua: è l'istantanea per useSyncExternalStore. */
export type Bound = { lang: Lang; t: typeof tr };
let bound: Bound = { lang, t: tr };
const subs = new Set<() => void>();

export const subscribe = (fn: () => void) => {
  subs.add(fn);
  return () => void subs.delete(fn);
};
export const snapshot = () => bound;

export function getLang(): Lang {
  return lang;
}

/** Sostituisce {nome} con i parametri; un segnaposto senza parametro resta com'è. */
export function interpolate(text: string, params?: Params): string {
  if (!params) return text;
  return text.replace(/\{(\w+)\}/g, (m, k: string) => (k in params ? String(params[k]) : m));
}

function pick(entry: Entry, params?: Params): string {
  if (typeof entry === "string") return interpolate(entry, params);
  const count = Number(params?.count ?? 0);
  const rule = new Intl.PluralRules(lang).select(count);
  // «zero» solo se il catalogo lo prevede, e solo per lo zero vero (in italiano «0» è plurale).
  const form = count === 0 && entry.zero !== undefined ? entry.zero : (entry[rule] ?? entry.other);
  return interpolate(form, params);
}

/** Testo per chiave nota; con una chiave sconosciuta a runtime restituisce la chiave stessa. */
export function tr(key: Key, params?: Params): string {
  const e = catalog[key];
  return e === undefined ? key : pick(e, params);
}

/** Testo per chiave dinamica (es. `errori.${code}`): undefined se non c'è. */
export function lookup(key: string, params?: Params): string | undefined {
  const e = catalog[key];
  return e === undefined ? undefined : pick(e, params);
}

/** Errori di validazione con un testo generico: il detail italiano del server si aggiunge come dettaglio. */
const COARSE = new Set(["validation", "validation.required", "validation.invalid_value", "validation.duplicate"]);

/**
 * Messaggio di un errore dell'API: in italiano vale il `detail` del server (ha i dettagli);
 * nelle altre lingue la traduzione del `code`, e se manca il `detail` italiano come riserva.
 * Se il testo è generico (code «validation» o una validazione senza parametri), segue «(detail: …)»
 * con il detail originale, così la persona non perde l'informazione.
 */
export function errorText(code: string, detail: string, params?: Params): string {
  if (lang === DEFAULT_LANG) return detail;
  // Il genere dell'oggetto (what: "member") si traduce con errors.kind.<what>.
  const what = params?.what ?? (code === "validation.not_found" ? "item" : undefined);
  const p = typeof what === "string" ? { ...params, what: lookup(`errors.kind.${what}`) ?? what } : params;
  const v = lookup(`errors.${code}`, p);
  // Un segnaposto senza valore (l'errore non porta i parametri): meglio il detail del server.
  if (v === undefined || /\{\w+\}/.test(v)) return detail;
  return COARSE.has(code) && detail ? `${v} ${interpolate(lookup("errors.detailSuffix") ?? "({detail})", { detail })}` : v;
}

async function load(l: Lang): Promise<Catalog> {
  let c = loaded.get(l);
  if (!c) {
    c = (await loaders[l]()).default;
    loaded.set(l, c);
  }
  return c;
}

let token = 0;

/** Passa a una lingua: carica il catalogo, poi aggiorna <html lang>, la memoria del browser e i componenti. */
export async function setLang(next: Lang, remember = true): Promise<void> {
  const mine = ++token;
  const c = await load(next);
  if (mine !== token) return; // un cambio più recente ha la precedenza
  const changed = next !== lang || catalog !== c;
  lang = next;
  catalog = c;
  if (typeof document !== "undefined") document.documentElement.lang = next;
  if (remember) {
    try {
      localStorage.setItem(STORE, next);
    } catch {
      /* archivio non disponibile: si perde solo il ricordo */
    }
  }
  if (changed) {
    bound = { lang, t: (key, params) => tr(key, params) };
    subs.forEach((fn) => fn());
  }
}

/** Lingua da usare prima di sapere chi è la persona: quella ricordata, o quella del browser. */
export function startingLang(): Lang {
  try {
    const v = localStorage.getItem(STORE);
    if (isLang(v)) return v;
  } catch {
    /* archivio non disponibile */
  }
  return browserLang();
}

/** Da chiamare una volta prima del primo disegno. */
export function initLanguage(): Promise<void> {
  return setLang(startingLang(), false);
}

/** Segue la lingua effettiva dell'utente (campo `language` di /me). */
export function followServerLanguage(me: unknown) {
  const l = (me as { language?: { effective?: unknown } } | null | undefined)?.language?.effective;
  if (isLang(l) && l !== lang) void setLang(l);
  else if (isLang(l)) {
    try {
      localStorage.setItem(STORE, l);
    } catch {
      /* come sopra */
    }
  }
}
