// Linguaggio di condizioni (sdlc.md §2), stesso comportamento di internal/engine/condition.go:
// serve al questionario per mostrare e nascondere le domande mentre si risponde, senza
// aspettare il server. Il server resta l'autorità: ricalcola tutto a ogni salvataggio.
//
//   esposto_internet: sì -autenticazione: nessuna
//   criticita: alta, critica oppure funzione_critica: sì
//   conservazione_mesi: >120

import { tr } from "./i18n/core.ts";
export type Facts = Record<string, unknown[]>;
type Op = "eq" | "present" | "absent" | "gt" | "ge" | "lt" | "le" | "bool";
type Matcher = { op: Op; raw: string; num: number; isNum: boolean; b: boolean };
type Clause = { key: string; negate: boolean; values: Matcher[] };
export type Condition = { src: string; alts: Clause[][] };

const presentWords = new Set(["presente", "present", "*", "esiste", "exists", "valorizzato"]);
const absentWords = new Set(["assente", "absent", "vuoto", "empty", "none", "nessuno"]);
const trueWords = new Set(["sì", "si", "yes", "true", "vero"]);
const falseWords = new Set(["no", "false", "falso"]);

function parseNumber(s: string): number | null {
  let mult = 1;
  let ls = s.toLowerCase();
  if (ls.endsWith("k")) [mult, ls] = [1_000, ls.slice(0, -1)];
  else if (ls.endsWith("m")) [mult, ls] = [1_000_000, ls.slice(0, -1)];
  const parts = ls.split(".");
  if (parts.length > 2 || (parts.length === 2 && parts[1].length === 3 && mult === 1)) ls = ls.replaceAll(".", "");
  if (!/^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$/.test(ls)) return null;
  return Number(ls) * mult;
}

function isDate(s: string): boolean {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(s)) return false;
  const d = new Date(s + "T00:00:00Z");
  return !Number.isNaN(d.getTime()) && d.toISOString().slice(0, 10) === s;
}

function parseMatcher(s: string): Matcher {
  const ls = s.toLowerCase();
  const base = { raw: s, num: 0, isNum: false, b: false };
  if (presentWords.has(ls)) return { ...base, op: "present" };
  if (absentWords.has(ls)) return { ...base, op: "absent" };
  if (trueWords.has(ls)) return { ...base, op: "bool", b: true };
  if (falseWords.has(ls)) return { ...base, op: "bool", b: false };
  for (const [p, op] of [[">=", "ge"], ["<=", "le"], [">", "gt"], ["<", "lt"]] as const) {
    if (s.startsWith(p)) {
      const v = s.slice(p.length).trim();
      if (!v) throw new Error(tr("configurazione.processEditor.cond.missingAfter", { op: p }));
      const n = parseNumber(v);
      if (n == null && !isDate(v)) throw new Error(tr("configurazione.processEditor.cond.notNumberOrDate", { value: v }));
      return { op, raw: v, num: n ?? 0, isNum: n != null, b: false };
    }
  }
  const n = parseNumber(s);
  return { op: "eq", raw: s, num: n ?? 0, isNum: n != null, b: false };
}

const validKey = (k: string) => /^[\p{L}\p{N}_.]+$/u.test(k);

/** Analizza una condizione; l'errore dice dove si è fermato. La condizione vuota è sempre vera. */
export function parseCondition(src: string): Condition {
  const text = src.trim();
  const rs = Array.from(text);
  let pos = 0;
  const eof = () => pos >= rs.length;
  const peek = (r: string) => !eof() && rs[pos] === r;
  const skip = () => {
    while (!eof() && /\s/u.test(rs[pos])) pos++;
  };
  const word = (): [string, boolean] => {
    if (peek('"')) {
      const end = rs.indexOf('"', pos + 1);
      if (end < 0) throw new Error(tr("configurazione.processEditor.cond.unclosedQuote"));
      const w = rs.slice(pos + 1, end).join("");
      pos = end + 1;
      return [w, true];
    }
    const start = pos;
    while (!eof() && !/[\s:,"]/u.test(rs[pos])) pos++;
    return [rs.slice(start, pos).join(""), false];
  };
  const c: Condition = { src: text, alts: [] };
  let cur: Clause[] = [];
  let neg = false;
  for (;;) {
    skip();
    if (eof()) break;
    const start = pos;
    const [w, quoted] = word();
    skip();
    if (quoted || !peek(":")) {
      const lw = w.toLowerCase();
      if (quoted) throw new Error(tr("configurazione.processEditor.cond.valueNoField", { word: w }));
      if (lw === "oppure" || lw === "or") {
        if (cur.length === 0 || neg) throw new Error(tr("configurazione.processEditor.cond.noConditionBefore", { word: w }));
        c.alts.push(cur);
        cur = [];
      } else if (lw === "non" || lw === "not") neg = !neg;
      else if (lw !== "e" && lw !== "and") throw new Error(tr("configurazione.processEditor.cond.expectedClause", { pos: start + 1, word: w }));
      continue;
    }
    pos++; // ':'
    const cl: Clause = { key: w.toLowerCase(), negate: neg, values: [] };
    neg = false;
    if (cl.key.startsWith("-")) [cl.key, cl.negate] = [cl.key.slice(1), !cl.negate];
    if (!validKey(cl.key)) throw new Error(tr("configurazione.processEditor.cond.invalidFieldName", { key: cl.key }));
    for (;;) {
      skip();
      if (eof()) throw new Error(tr("configurazione.processEditor.cond.missingValue", { key: cl.key }));
      let [v, q] = word();
      if (v === "" && !q) throw new Error(tr("configurazione.processEditor.cond.missingValue", { key: cl.key }));
      if (!q && (v === ">" || v === ">=" || v === "<" || v === "<=")) {
        skip();
        v += word()[0];
      }
      try {
        cl.values.push(q ? { op: "eq", raw: v, num: 0, isNum: false, b: false } : parseMatcher(v));
      } catch (e) {
        throw new Error(`${cl.key}: ${(e as Error).message}`);
      }
      skip();
      if (!peek(",")) break;
      pos++;
    }
    cur.push(cl);
  }
  if (neg) throw new Error(tr("configurazione.processEditor.cond.notWithout"));
  if (cur.length === 0 && c.alts.length > 0) throw new Error(tr("configurazione.processEditor.cond.orWithout"));
  if (cur.length > 0) c.alts.push(cur);
  return c;
}

const cache = new Map<string, Condition | null>();

/** Condizione analizzata e tenuta in memoria; null se non è valida (allora non è mai vera). */
export function compiled(src: string): Condition | null {
  if (!cache.has(src)) {
    try {
      cache.set(src, parseCondition(src));
    } catch {
      cache.set(src, null);
    }
  }
  return cache.get(src)!;
}

function compare(a: number | string, b: number | string, op: Op): boolean {
  switch (op) {
    case "eq":
      return a === b;
    case "gt":
      return a > b;
    case "ge":
      return a >= b;
    case "lt":
      return a < b;
    case "le":
      return a <= b;
  }
  return false;
}

function match(m: Matcher, vals: unknown[]): boolean {
  if (m.op === "present") return vals.length > 0;
  if (m.op === "absent") return vals.length === 0;
  for (const v of vals) {
    if (typeof v === "boolean") {
      if (m.op === "bool" && v === m.b) return true;
    } else if (typeof v === "number") {
      if (m.isNum && compare(v, m.num, m.op)) return true;
    } else if (typeof v === "string") {
      if (m.op === "eq") {
        if (v.toLowerCase() === m.raw.toLowerCase()) return true;
        if (m.isNum && parseNumber(v) === m.num) return true;
      } else if (m.op !== "bool" && !m.isNum && v.length >= 10 && compare(v.slice(0, 10), m.raw, m.op)) {
        return true;
      }
    }
  }
  return false;
}

/** Valuta la condizione sui fatti. */
export function evalCondition(c: Condition | null, f: Facts): boolean {
  if (!c) return false;
  if (c.alts.length === 0) return true;
  return c.alts.some((alt) => alt.every((cl) => cl.values.some((m) => match(m, f[cl.key] ?? [])) !== cl.negate));
}

/** Chiavi usate dalla condizione. */
export function conditionKeys(c: Condition | null): string[] {
  return c ? [...new Set(c.alts.flatMap((a) => a.map((cl) => cl.key)))] : [];
}

// ---------------------------------------------------------------------------
// Questionario: stessa valutazione di internal/modules/reviews/questionnaire.go.

export type QOption = { value: string; label: string; score?: number };
export type QType = "text" | "single" | "multi" | "yes_no" | "number" | "date" | "attachment" | "scale" | "matrix";
export type Question = {
  key: string;
  label: string;
  type: QType;
  required?: boolean;
  help?: string;
  visible_when?: string;
  prefill?: string;
  options?: QOption[];
  multiline?: boolean;
  min?: number;
  max?: number;
  unit?: string;
  rows?: QOption[];
  min_label?: string;
  max_label?: string;
  reverse?: boolean;
  weight?: number;
  yes_score?: number;
  no_score?: number;
};
export type QSection = {
  key: string;
  title: string;
  description?: string;
  visible_when?: string;
  filled_by?: "" | "team";
  questions: Question[];
};
/** Regola di derivazione: una prescrizione proposta al team oppure un requisito (che alla chiusura diventa un requisito vero). */
export type Derivation = {
  key: string;
  when: string;
  text: string;
  category?: string;
  kind?: "prescription" | "requirement";
  /** Solo per i requisiti: titolo, metodo di verifica e controllo (con metodo «evidence»). */
  title?: string;
  method?: "manual" | "evidence";
  check?: string;
};
export type Threshold = { min: number; outcome: string };
export type QDefinition = {
  name: string;
  description?: string;
  sections: QSection[];
  derivations?: Derivation[];
  scoring?: { thresholds: Threshold[] };
};
export type SectionState = { visible: boolean; total: number; answered: number; missing: number };
export type QEvaluation = {
  sections: Record<string, SectionState>;
  visible: Record<string, boolean>;
  total: number;
  answered: number;
  required: number;
  missing: string[];
  derived: Derivation[];
  derived_requirements: Derivation[];
  scored?: boolean;
  score?: number;
  max_score?: number;
  percent?: number;
  proposed_outcome?: string;
};

/** Estremi della scala (default 1-5). */
export const scaleRange = (q: Question): [number, number] => [q.min ?? 1, q.max ?? 5];

function answerFacts(q: Question, v: unknown): unknown[] {
  if (v == null) return [];
  if (q.type === "matrix" && typeof v === "object" && !Array.isArray(v))
    return Object.entries(v as Record<string, unknown>)
      .map(([r, c]) => `${r}.${c}`)
      .sort();
  if (Array.isArray(v)) return q.type === "attachment" ? [v.length] : v;
  if (typeof v === "string" && q.type === "single") {
    const o = q.options?.find((x) => x.value === v);
    return o ? [v, o.label] : [v];
  }
  return [v];
}

const isEmpty = (v: unknown) => v == null || v === "" || (Array.isArray(v) && v.length === 0);

/** Una risposta c'è; per la matrice servono tutte le righe. */
function complete(q: Question, v: unknown): boolean {
  if (isEmpty(v)) return false;
  if (q.type === "matrix") return !!q.rows?.length && Object.keys(v as object).length >= q.rows.length;
  return true;
}

/** Punti ottenuti e massimo di una domanda, prima del peso. Massimo 0 = senza punteggio. */
export function points(q: Question, v: unknown): [number, number] {
  const best = (os?: QOption[]) => {
    const sc = (os ?? []).filter((o) => o.score != null).map((o) => o.score!);
    return sc.length ? Math.max(...sc) : null;
  };
  const of = (os: QOption[] | undefined, val: unknown) => os?.find((o) => o.value === val)?.score ?? 0;
  switch (q.type) {
    case "yes_no": {
      if (q.yes_score == null && q.no_score == null) return [0, 0];
      const max = Math.max(0, q.yes_score ?? 0, q.no_score ?? 0);
      return [typeof v === "boolean" ? (v ? (q.yes_score ?? 0) : (q.no_score ?? 0)) : 0, max];
    }
    case "single": {
      const b = best(q.options);
      return b == null ? [0, 0] : [typeof v === "string" ? of(q.options, v) : 0, Math.max(0, b)];
    }
    case "multi": {
      if (best(q.options) == null) return [0, 0];
      const max = (q.options ?? []).reduce((a, o) => a + Math.max(0, o.score ?? 0), 0);
      const got = Array.isArray(v) ? v.reduce((a: number, x) => a + of(q.options, x), 0) : 0;
      return [got, max];
    }
    case "scale": {
      const [lo, hi] = scaleRange(q);
      return [typeof v === "number" ? (q.reverse ? hi - v : v - lo) : 0, hi - lo];
    }
    case "matrix": {
      const b = best(q.options);
      if (b == null || !q.rows?.length) return [0, 0];
      const m = (v && typeof v === "object" ? v : {}) as Record<string, string>;
      return [q.rows.reduce((a, r) => a + of(q.options, m[r.value]), 0), Math.max(0, b) * q.rows.length];
    }
  }
  return [0, 0];
}

/** Esito della soglia più alta raggiunta ("" se nessuna). */
export function proposedOutcome(percent: number, th: Threshold[] | undefined): string {
  let best = -1;
  let out = "";
  for (const t of th ?? []) if (percent >= t.min && t.min > best) [best, out] = [t.min, t.outcome];
  return out;
}

/** Valuta il questionario nell'ordine di compilazione: ogni condizione vede i fatti
 * dell'elemento e le risposte visibili che vengono prima. */
export function evaluate(def: QDefinition, facts: Facts, answers: Record<string, unknown>): QEvaluation {
  const f: Facts = { ...facts };
  const ev: QEvaluation = { sections: {}, visible: {}, total: 0, answered: 0, required: 0, missing: [], derived: [], derived_requirements: [], scored: false, score: 0, max_score: 0, percent: 0 };
  for (const s of def.sections ?? []) {
    const st: SectionState = { visible: !s.visible_when || evalCondition(compiled(s.visible_when), f), total: 0, answered: 0, missing: 0 };
    for (const q of s.questions ?? []) {
      const vis = st.visible && (!q.visible_when || evalCondition(compiled(q.visible_when), f));
      ev.visible[q.key] = vis;
      if (!vis) continue;
      st.total++;
      const v = answers[q.key];
      const answered = complete(q, v);
      if (!isEmpty(v)) f[q.key.toLowerCase()] = answerFacts(q, v);
      if (answered) st.answered++;
      const [got, max] = points(q, v);
      const w = q.weight ?? 1;
      if (max > 0 && w > 0) {
        ev.scored = true;
        ev.score! += w * got;
        ev.max_score! += w * max;
      }
      if (q.required) {
        ev.required++;
        if (!answered) {
          st.missing++;
          ev.missing.push(q.key);
        }
      }
    }
    ev.sections[s.key] = st;
    ev.total += st.total;
    ev.answered += st.answered;
  }
  for (const r of def.derivations ?? []) {
    if (!evalCondition(compiled(r.when), f)) continue;
    if (r.kind === "requirement") ev.derived_requirements.push({ ...r, category: r.category || "Requisito", method: r.method ?? "manual" });
    else ev.derived.push(r);
  }
  if (ev.scored && ev.max_score! > 0) {
    ev.percent = Math.round((ev.score! / ev.max_score!) * 1000) / 10;
    ev.proposed_outcome = proposedOutcome(ev.percent, def.scoring?.thresholds);
  }
  return ev;
}
