// Logica pura del selettore (web/src/select.tsx): ricerca, ordine delle voci, voci in cima
// (nessuno, io, recenti, attuale) e movimento con la tastiera. Nessuna dipendenza da React o dal
// DOM, così si prova con `node --test` (web/test/select.test.ts).

export type Opt = { value: string; label: string; detail?: string; disabled?: boolean; group?: string;
  /** Un membro esterno che fa solo richieste (dato stabile, indipendente dalla lingua del `detail`). */
  requester?: boolean };
export type Section = { title?: string; opts: Opt[] };

/** Da questo numero di voci statiche in su compare il campo di ricerca. */
export const SEARCH_MIN = 8;
/** Voci chieste al server a ogni ricerca. */
export const SERVER_LIMIT = 20;
/** Recenti ricordati per campo. */
export const RECENT_MAX = 5;

/** Minuscolo senza accenti: «Àlvaro» si trova scrivendo «alvaro». */
export function fold(s: string): string {
  return s.normalize("NFD").replace(/[̀-ͯ]/g, "").toLowerCase();
}

/** Punteggio di una voce per il testo cercato (0 = non corrisponde, più alto = meglio). Ogni parola cercata deve comparire. */
export function score(o: Opt, query: string): number {
  const words = fold(query).split(/\s+/).filter(Boolean);
  if (words.length === 0) return 1;
  const label = fold(o.label);
  const detail = o.detail ? fold(o.detail) : "";
  let total = 0;
  for (const w of words) {
    let s = 0;
    if (label.startsWith(w)) s = 4;
    else if (label.split(/[\s\-_/.(),]+/).some((p) => p.startsWith(w))) s = 3;
    else if (label.includes(w)) s = 2;
    else if (detail.includes(w)) s = 1;
    if (s === 0) return 0;
    total += s;
  }
  return total;
}

/** Voci che corrispondono, le migliori per prime; a pari punteggio resta l'ordine di partenza. */
export function filterOpts(opts: Opt[], query: string): Opt[] {
  if (!query.trim()) return opts;
  return opts
    .map((o, i) => ({ o, i, s: score(o, query) }))
    .filter((x) => x.s > 0)
    .sort((a, b) => b.s - a.s || a.i - b.i)
    .map((x) => x.o);
}

/** Raggruppa le voci per `group` mantenendo l'ordine; le voci senza gruppo vanno in una sezione senza titolo. */
export function groupOpts(opts: Opt[]): Section[] {
  const out: Section[] = [];
  for (const o of opts) {
    const last = out[out.length - 1];
    if (last && last.title === o.group) last.opts.push(o);
    else out.push({ title: o.group, opts: [o] });
  }
  return out;
}

export type BuildInput = {
  /** Voci da mostrare: tutte (elenco statico) oppure il risultato della ricerca sul server. */
  opts: Opt[];
  /** Testo cercato; per le liste del server il filtro l'ha già fatto il server (`serverFiltered`). */
  query: string;
  serverFiltered?: boolean;
  /** Voce «nessuno» (valore vuoto) se il campo è facoltativo. */
  none?: Opt;
  /** «Io», per le persone. */
  me?: Opt;
  /** Recenti (già risolti in voci) e voce attuale: sono in cima quando non si cerca. */
  recents?: Opt[];
  current?: Opt[];
  /** Scelta multipla: le voci già scelte non si ripropongono. */
  multiple?: boolean;
  selected?: string[];
};

/** Sezioni del pannello. Senza ricerca: nessuno, io, recenti, attuale e poi il resto; con la ricerca: solo le corrispondenze. */
export function buildSections(i: BuildInput): Section[] {
  const selected = new Set(i.selected ?? []);
  const hide = (o: Opt) => i.multiple && selected.has(o.value);
  const q = i.query.trim();
  if (q) {
    const base = i.serverFiltered ? i.opts : filterOpts(i.opts, q);
    const list = base.filter((o) => !hide(o));
    return i.serverFiltered ? [{ opts: list }] : groupedIfAny(list);
  }
  const out: Section[] = [];
  const used = new Set<string>();
  const pin = (title: string | undefined, opts: (Opt | undefined)[]) => {
    const list = opts.filter((o): o is Opt => !!o && !used.has(o.value) && !hide(o));
    if (!list.length) return;
    list.forEach((o) => used.add(o.value));
    out.push({ title, opts: list });
  };
  if (i.none) pin(undefined, [i.none]);
  if (i.me) pin(undefined, [i.me]);
  pin("Recenti", i.recents ?? []);
  if (!i.multiple) pin("Attuale", i.current ?? []);
  const rest = i.opts.filter((o) => !used.has(o.value) && !hide(o));
  if (rest.length) out.push(...(out.length && !rest.some((o) => o.group) ? [{ title: i.serverFiltered ? "Tutti" : undefined, opts: rest }] : groupedIfAny(rest)));
  return out;
}

function groupedIfAny(opts: Opt[]): Section[] {
  return opts.some((o) => o.group) ? groupOpts(opts) : [{ opts }];
}

/** Elenco piatto delle voci scegliibili nell'ordine in cui compaiono. */
export function flatten(sections: Section[]): Opt[] {
  return sections.flatMap((s) => s.opts);
}

/** Nuova voce attiva dopo un tasto, saltando le voci disabilitate; -1 se non ce ne sono. */
export function moveActive(opts: Opt[], from: number, key: "ArrowDown" | "ArrowUp" | "Home" | "End" | "PageDown" | "PageUp"): number {
  const ok = (i: number) => i >= 0 && i < opts.length && !opts[i].disabled;
  const first = opts.findIndex((o) => !o.disabled);
  if (first < 0) return -1;
  let last = opts.length - 1;
  while (last > 0 && opts[last].disabled) last--;
  if (key === "Home") return first;
  if (key === "End") return last;
  const step = key === "ArrowDown" || key === "PageDown" ? 1 : -1;
  const jump = key === "PageDown" || key === "PageUp" ? 8 : 1;
  let i = from < 0 ? (step > 0 ? -1 : opts.length) : from;
  let moved = 0;
  while (moved < jump) {
    let n = i + step;
    while (n >= 0 && n < opts.length && !ok(n)) n += step;
    if (n < 0 || n >= opts.length) break;
    i = n;
    moved++;
  }
  return ok(i) ? i : from >= 0 && ok(from) ? from : step > 0 ? first : last;
}

/** Voce che inizia con il testo battuto a pannello chiuso o senza campo di ricerca (ricerca per iniziale). */
export function typeahead(opts: Opt[], buffer: string, from: number): number {
  const b = fold(buffer);
  if (!b) return -1;
  const n = opts.length;
  // Una sola lettera ripetuta scorre tra le voci con quell'iniziale.
  const repeated = b.split("").every((c) => c === b[0]);
  const needle = repeated ? b[0] : b;
  const start = repeated && b.length > 1 ? from + 1 : repeated ? from + 1 : from;
  for (let k = 0; k < n; k++) {
    const i = (((start + k) % n) + n) % n;
    if (!opts[i].disabled && fold(opts[i].label).startsWith(needle)) return i;
  }
  return -1;
}

// ---------------------------------------------------------------------
// Recenti per campo (memoria locale del browser)

type Store = Pick<Storage, "getItem" | "setItem">;

export const recentsKey = (field: string) => `regia.selettore.recenti.${field}`;

export function loadRecents(store: Store | undefined, field: string): string[] {
  try {
    const v: unknown = JSON.parse(store?.getItem(recentsKey(field)) ?? "[]");
    return Array.isArray(v) ? v.filter((x): x is string => typeof x === "string").slice(0, RECENT_MAX) : [];
  } catch {
    return [];
  }
}

/** Mette `id` in testa ai recenti del campo (senza doppioni, al massimo RECENT_MAX) e restituisce il nuovo elenco. */
export function pushRecent(store: Store | undefined, field: string, id: string): string[] {
  const next = [id, ...loadRecents(store, field).filter((x) => x !== id)].slice(0, RECENT_MAX);
  try {
    store?.setItem(recentsKey(field), JSON.stringify(next));
  } catch {
    /* memoria piena o bloccata: i recenti sono solo una comodità */
  }
  return next;
}
