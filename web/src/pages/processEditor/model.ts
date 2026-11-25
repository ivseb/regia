// Modello dell'editor visuale dei processi (sdlc.md §10): funzioni pure, senza React,
// provate con `pnpm test` (node:test). Qui stanno il linguaggio di condizioni lato
// client, le modifiche alla definizione, le differenze leggibili e la disposizione
// del diagramma.
import type { Definition, Phase, ProcessTransition } from "../../api";
import { tr, type Key } from "../../i18n/core.ts";

// ---------------------------------------------------------------------------
// Linguaggio di condizioni (lo stesso di internal/engine/condition.go)

type Matcher = { op: "eq" | "present" | "absent" | "bool" | "gt" | "ge" | "lt" | "le"; raw: string; b?: boolean };
type Clause = { key: string; negate: boolean; values: Matcher[] };

const PRESENT = new Set(["presente", "present", "*", "esiste", "exists", "valorizzato"]);
const ABSENT = new Set(["assente", "absent", "vuoto", "empty", "none", "nessuno"]);
const TRUE = new Set(["sì", "si", "yes", "true", "vero"]);
const FALSE = new Set(["no", "false", "falso"]);

/** Numeri come nel motore: 50000, 50.000, 1.5, 50k, 2m. */
export function parseNumber(s: string): number | null {
  let mult = 1;
  let ls = s.toLowerCase();
  if (ls.endsWith("k")) [mult, ls] = [1_000, ls.slice(0, -1)];
  else if (ls.endsWith("m")) [mult, ls] = [1_000_000, ls.slice(0, -1)];
  const parts = ls.split(".");
  if (parts.length > 2 || (parts.length === 2 && parts[1].length === 3 && mult === 1)) ls = ls.replaceAll(".", "");
  if (!/^[+-]?(\d+\.?\d*|\.\d+)(e[+-]?\d+)?$/.test(ls)) return null;
  return Number(ls) * mult;
}

const isDate = (s: string) => /^\d{4}-\d{2}-\d{2}$/.test(s) && !Number.isNaN(Date.parse(s));

function parseMatcher(s: string): Matcher {
  const ls = s.toLowerCase();
  if (PRESENT.has(ls)) return { op: "present", raw: s };
  if (ABSENT.has(ls)) return { op: "absent", raw: s };
  if (TRUE.has(ls)) return { op: "bool", raw: s, b: true };
  if (FALSE.has(ls)) return { op: "bool", raw: s, b: false };
  for (const [p, op] of [[">=", "ge"], ["<=", "le"], [">", "gt"], ["<", "lt"]] as const) {
    if (s.startsWith(p)) {
      const v = s.slice(p.length).trim();
      if (!v) throw new Error(tr("configurazione.processEditor.cond.missingAfter", { op: p }));
      if (parseNumber(v) === null && !isDate(v)) throw new Error(tr("configurazione.processEditor.cond.notNumberOrDate", { value: v }));
      return { op, raw: v };
    }
  }
  return { op: "eq", raw: s };
}

const validKey = (k: string) => k !== "" && /^[\p{L}\p{N}_.]+$/u.test(k);

/** Analizza una condizione come il motore: alternative ("oppure") di clausole (tutte vere). */
export function parseClauses(src: string): Clause[][] {
  const rs = [...src.trim()];
  let pos = 0;
  const eof = () => pos >= rs.length;
  const peek = (c: string) => !eof() && rs[pos] === c;
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
  const alts: Clause[][] = [];
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
        alts.push(cur);
        cur = [];
      } else if (lw === "e" || lw === "and") {
        // congiunzione esplicita: non cambia nulla
      } else if (lw === "non" || lw === "not") {
        neg = !neg;
      } else {
        throw new Error(tr("configurazione.processEditor.cond.expectedClause", { pos: start + 1, word: w }));
      }
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
        cl.values.push(q ? { op: "eq", raw: v } : parseMatcher(v));
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
  if (cur.length === 0 && alts.length > 0) throw new Error(tr("configurazione.processEditor.cond.orWithout"));
  if (cur.length > 0) alts.push(cur);
  return alts;
}

/** Errore di sintassi della condizione, o null se è valida. */
export function conditionError(src: string): string | null {
  try {
    parseClauses(src);
    return null;
  } catch (e) {
    return (e as Error).message;
  }
}

export type Op = "is" | "isnot" | "present" | "absent" | "gt" | "ge" | "lt" | "le" | "yes" | "no";
export type CondRow = { fact: string; op: Op; values: string[] };
export type CondModel = { join: "all" | "any"; rows: CondRow[] };

export const OPS: { op: Op; label: string; values: 0 | 1 | "n" }[] = [
  { op: "is", get label() { return tr("configurazione.processEditor.op.is"); }, values: "n" },
  { op: "isnot", get label() { return tr("configurazione.processEditor.op.isnot"); }, values: "n" },
  { op: "present", get label() { return tr("configurazione.processEditor.op.present"); }, values: 0 },
  { op: "absent", get label() { return tr("configurazione.processEditor.op.absent"); }, values: 0 },
  { op: "gt", get label() { return tr("configurazione.processEditor.op.gt"); }, values: 1 },
  { op: "ge", get label() { return tr("configurazione.processEditor.op.ge"); }, values: 1 },
  { op: "lt", get label() { return tr("configurazione.processEditor.op.lt"); }, values: 1 },
  { op: "le", get label() { return tr("configurazione.processEditor.op.le"); }, values: 1 },
  { op: "yes", get label() { return tr("configurazione.processEditor.op.yes"); }, values: 0 },
  { op: "no", get label() { return tr("configurazione.processEditor.op.no"); }, values: 0 },
];

const CMP: Record<string, Op> = { gt: "gt", ge: "ge", lt: "lt", le: "le" };
const CMP_SYMBOL: Partial<Record<Op, string>> = { gt: ">", ge: ">=", lt: "<", le: "<=" };

export function clauseRow(cl: Clause): CondRow | null {
  const [m] = cl.values;
  if (cl.values.length === 1) {
    if (m.op === "present") return { fact: cl.key, op: cl.negate ? "absent" : "present", values: [] };
    if (m.op === "absent") return { fact: cl.key, op: cl.negate ? "present" : "absent", values: [] };
    if (m.op === "bool") return cl.negate ? null : { fact: cl.key, op: m.b ? "yes" : "no", values: [] };
    if (CMP[m.op]) return cl.negate ? null : { fact: cl.key, op: CMP[m.op], values: [m.raw] };
  }
  if (cl.values.some((v) => v.op !== "eq")) return null;
  return { fact: cl.key, op: cl.negate ? "isnot" : "is", values: cl.values.map((v) => v.raw) };
}

/**
 * Traduce una condizione nelle righe del costruttore. Restituisce null se la condizione
 * non è esprimibile come "tutte" o "almeno una" di righe semplici (si modifica come testo).
 */
export function toModel(src: string): CondModel | null {
  let alts: Clause[][];
  try {
    alts = parseClauses(src);
  } catch {
    return null;
  }
  if (alts.length === 0) return { join: "all", rows: [] };
  let join: CondModel["join"];
  let clauses: Clause[];
  if (alts.length === 1) [join, clauses] = ["all", alts[0]];
  else if (alts.every((a) => a.length === 1)) [join, clauses] = ["any", alts.map((a) => a[0])];
  else return null;
  const rows: CondRow[] = [];
  for (const cl of clauses) {
    const r = clauseRow(cl);
    if (!r) return null;
    rows.push(r);
  }
  return { join, rows };
}

const SPECIAL = (v: string) => {
  const l = v.toLowerCase();
  return PRESENT.has(l) || ABSENT.has(l) || TRUE.has(l) || FALSE.has(l) || /^[<>]/.test(v);
};

/** Un valore nel testo della condizione: tra virgolette se contiene spazi o separatori. */
export function quoteValue(v: string): string {
  const clean = v.replaceAll('"', "").trim();
  if (clean === "" || /[\s:,]/u.test(clean) || SPECIAL(clean) || ["oppure", "or", "e", "and", "non", "not"].includes(clean.toLowerCase())) {
    return `"${clean}"`;
  }
  return clean;
}

export function rowText(r: CondRow): string | null {
  const fact = r.fact.trim().toLowerCase();
  if (!validKey(fact)) return null;
  const vals = r.values.map((v) => v.trim()).filter(Boolean);
  switch (r.op) {
    case "is":
    case "isnot":
      if (vals.length === 0) return null;
      return `${r.op === "isnot" ? "-" : ""}${fact}: ${vals.map(quoteValue).join(", ")}`;
    case "present":
      return `${fact}: presente`;
    case "absent":
      return `${fact}: assente`;
    case "yes":
      return `${fact}: sì`; // i18n-ok: sintassi delle condizioni
    case "no":
      return `${fact}: no`;
    default:
      if (vals.length === 0) return null;
      return `${fact}: ${CMP_SYMBOL[r.op]}${vals[0].replaceAll(/\s/gu, "")}`;
  }
}

/** Testo della condizione dalle righe (le righe incomplete si saltano). */
export function fromModel(m: CondModel): string {
  const parts = m.rows.map(rowText).filter((x): x is string => !!x);
  return parts.join(m.join === "any" ? " oppure " : " ");
}

/** Operatore di partenza per un fatto, secondo il suo tipo. */
export function defaultOp(kind: string | undefined): Op {
  switch (kind) {
    case "number":
    case "date":
      return "gt";
    case "bool":
      return "yes";
    case "presence":
      return "present";
  }
  return "is";
}

// ---------------------------------------------------------------------------
// Definizione

export const CATEGORIES: { key: string; name: string }[] = [
  { key: "todo", get name() { return tr("configurazione.processEditor.category.todo"); } },
  { key: "in_progress", get name() { return tr("configurazione.processEditor.category.in_progress"); } },
  { key: "done", get name() { return tr("configurazione.processEditor.category.done"); } },
  { key: "cancelled", get name() { return tr("configurazione.processEditor.category.cancelled"); } },
];
export const categoryName = (c: string) => CATEGORIES.find((x) => x.key === c)?.name ?? c;

/** Copia con gli elenchi facoltativi sempre presenti. */
export function normalize(d: Definition | undefined | null): Definition {
  const c: Definition = d ? structuredClone(d) : { phases: [], initial: "", transitions: [] };
  c.phases ??= [];
  c.transitions ??= [];
  c.gates ??= [];
  c.rules ??= [];
  return c;
}

/** Definizione da salvare: senza elenchi vuoti facoltativi né campi vuoti. */
export function clean(d: Definition): Definition {
  const out: Definition = { phases: d.phases.map((p) => strip(p)), initial: d.initial, transitions: d.transitions.map((t) => strip(t)) };
  if (d.gates?.length) out.gates = d.gates.map((g) => strip(g));
  if (d.rules?.length) out.rules = d.rules.map((r) => ({ ...strip(r), actions: r.actions.map((a) => strip(a)) }));
  return out;
}

function strip<T extends object>(o: T): T {
  const out = {} as Record<string, unknown>;
  for (const [k, v] of Object.entries(o)) {
    if (v === undefined || v === "" || v === false || (Array.isArray(v) && v.length === 0 && k !== "from" && k !== "actions")) continue;
    out[k] = v;
  }
  return out as T;
}

const eq = (a: string, b: string) => a.toLowerCase() === b.toLowerCase();

/** Rinomina una fase e tutti i riferimenti: iniziale, passaggi, eventi delle regole. */
export function renamePhase(d: Definition, from: string, to: string) {
  for (const p of d.phases) if (eq(p.name, from)) p.name = to;
  if (eq(d.initial, from)) d.initial = to;
  for (const t of d.transitions) {
    t.from = t.from.map((f) => (eq(f, from) ? to : f));
    if (eq(t.to, from)) t.to = to;
  }
  for (const r of d.rules ?? []) {
    const [ev, ph] = splitOn(r.on);
    if (ph && eq(ph, from)) r.on = `${ev}:${to}`;
  }
}

/** Toglie una fase con i passaggi che arrivano lì e le partenze da lì. */
export function removePhase(d: Definition, name: string) {
  d.phases = d.phases.filter((p) => !eq(p.name, name));
  d.transitions = d.transitions
    .filter((t) => !eq(t.to, name))
    .map((t) => ({ ...t, from: t.from.filter((f) => !eq(f, name)) }))
    .filter((t) => t.from.length > 0);
  d.rules = (d.rules ?? []).filter((r) => {
    const ph = splitOn(r.on)[1];
    return !ph || !eq(ph, name);
  });
  if (eq(d.initial, name)) d.initial = d.phases[0]?.name ?? "";
}

/** Sposta una fase al posto `index` del percorso principale (le fasi fuori percorso restano in fondo). */
export function movePhase(d: Definition, name: string, index: number, offPath = false) {
  const p = d.phases.find((x) => eq(x.name, name));
  if (!p) return;
  const rest = d.phases.filter((x) => x !== p);
  p.off_path = offPath || undefined;
  const main = rest.filter((x) => !x.off_path);
  const off = rest.filter((x) => x.off_path);
  if (offPath) off.splice(Math.max(0, Math.min(index, off.length)), 0, p);
  else main.splice(Math.max(0, Math.min(index, main.length)), 0, p);
  d.phases = [...main, ...off];
}

export function splitOn(on: string): [string, string] {
  const i = on.indexOf(":");
  return i < 0 ? [on, ""] : [on.slice(0, i), on.slice(i + 1)];
}

/** Nome di fase libero: "Nuova fase", "Nuova fase 2"… */
export function freePhaseName(d: Definition, base = tr("configurazione.processEditor.newStage")): string {
  let n = base;
  for (let i = 2; d.phases.some((p) => eq(p.name, n)); i++) n = `${base} ${i}`;
  return n;
}

export type StepInput = {
  /** Fase dopo cui si inserisce il passo. */
  after: string;
  name: string;
  /** Ruoli che possono farlo avanzare (vuoto: chiunque possa modificare). */
  roles: string[];
  /** Condizione «solo se» per entrare nel passo (vuota: sempre). */
  when: string;
  /** Controllo da superare prima di entrare (facoltativo). */
  gate?: { name: string; condition: string; message: string } | null;
};

/**
 * Aggiunge un passo al percorso dopo una fase: la fase nuova, il passaggio che ci entra (con ruoli,
 * condizione e gate scelti) e quello che ne esce verso la fase che seguiva. I passaggi che prima
 * andavano dalla fase scelta alla successiva ora passano dal passo nuovo e ne escono con le stesse
 * regole di prima. Restituisce il nome (unico) della fase creata.
 */
export function addStep(d: Definition, s: StepInput): string {
  const base = s.name.trim() || tr("configurazione.processEditor.newStage");
  const name = d.phases.some((p) => eq(p.name, base)) ? freePhaseName(d, base) : base;
  const after = d.phases.find((p) => eq(p.name, s.after));
  const main = d.phases.filter((p) => !p.off_path);
  const last = main[main.length - 1];
  const atEnd = last && last.category === "done" && main.length > 1 ? main.length - 1 : main.length;
  const at = after && !after.off_path ? main.indexOf(after) + 1 : atEnd;
  const next = at < main.length ? main[at] : undefined;
  d.phases.push({ name, category: "in_progress" });
  movePhase(d, name, at);
  if (!d.initial) d.initial = name;
  d.gates ??= [];
  let gateKey: string | undefined;
  if (s.gate) {
    gateKey = keyFrom(s.gate.name || name, d.gates.map((g) => g.key));
    d.gates.push({ key: gateKey, name: s.gate.name.trim() || name, condition: s.gate.condition, message: s.gate.message });
  }
  const from = after?.name ?? "";
  const entering: ProcessTransition = { from: from ? [from] : [], to: name, label: tr("configurazione.processEditor.passTo", { to: name }) };
  if (s.roles.length) entering.roles = [...s.roles];
  if (s.when.trim()) entering.when = s.when.trim();
  if (gateKey) entering.gates = [gateKey];
  // I passaggi utente che andavano da `after` a `next` passano dal passo nuovo.
  const moved: ProcessTransition[] = [];
  if (after && next) {
    d.transitions = d.transitions.flatMap((t) => {
      if (t.system || !eq(t.to, next.name) || !t.from.some((f) => eq(f, after.name))) return [t];
      moved.push({ ...structuredClone(t), from: [name] });
      const rest = t.from.filter((f) => !eq(f, after.name));
      return rest.length ? [{ ...t, from: rest }] : [];
    });
    if (!moved.length) moved.push({ from: [name], to: next.name, label: tr("configurazione.processEditor.passTo", { to: next.name }) });
  }
  if (entering.from.length) d.transitions.push(entering);
  d.transitions.push(...moved);
  return name;
}

export type TieKind = "all" | "same" | "maybe";
type TieCandidate = { key: string; name: string; type: string; priority: number; applies_when: string; archived: boolean };

/**
 * Altri processi dello stesso tipo con la stessa priorità: a parità, se valgono insieme per un
 * elemento, decide l'ordine delle chiavi, che non è una scelta di nessuno («quale deve vincere?»).
 * `all`: l'altro vale per tutti gli elementi del tipo; `same`: stessa condizione; `maybe`: condizioni
 * diverse che possono valere insieme.
 */
export function priorityTies(others: TieCandidate[], me: { key?: string; type: string; priority: number; when: string }): { name: string; kind: TieKind }[] {
  const mine = me.when.trim();
  return others
    .filter((o) => !o.archived && o.type === me.type && o.key !== me.key && o.priority === me.priority)
    .map((o) => {
      const w = o.applies_when.trim();
      return { name: o.name, kind: (w === "" ? "all" : w === mine ? "same" : "maybe") as TieKind };
    });
}

/** Chiave per gate e regole dal nome: minuscole, cifre e _, unica. */
export function keyFrom(name: string, taken: string[]): string {
  let k = name
    .normalize("NFD")
    .replaceAll(/\p{M}/gu, "")
    .toLowerCase()
    .replaceAll(/[^a-z0-9]+/g, "_")
    .replaceAll(/^_+|_+$/g, "")
    .slice(0, 50);
  if (!/^[a-z]/.test(k)) k = `k_${k}`.replace(/_$/, "");
  let out = k;
  for (let i = 2; taken.includes(out); i++) out = `${k}_${i}`;
  return out;
}

/** Fasi non raggiungibili dalla iniziale (come Validate: "*" vale per le fasi non concluse). */
export function unreachable(d: Definition): Set<string> {
  const byName = new Map(d.phases.map((p) => [p.name.toLowerCase(), p]));
  const out = new Set<string>();
  const start = d.initial.toLowerCase();
  if (!byName.has(start)) return out;
  const reach = new Map<string, string[]>();
  for (const t of d.transitions) for (const f of t.from) reach.set(f.toLowerCase(), [...(reach.get(f.toLowerCase()) ?? []), t.to.toLowerCase()]);
  const seen = new Set([start]);
  const queue = [start];
  while (queue.length) {
    const cur = queue.shift()!;
    const p = byName.get(cur);
    const next = [...(reach.get(cur) ?? [])];
    if (p && p.category !== "done" && p.category !== "cancelled") next.push(...(reach.get("*") ?? []));
    for (const n of next) if (!seen.has(n)) (seen.add(n), queue.push(n));
  }
  for (const p of d.phases) if (!seen.has(p.name.toLowerCase())) out.add(p.name);
  return out;
}

export type Target =
  | { kind: "phase" | "transition" | "gate" | "rule"; index: number; field: string }
  | { kind: "initial" | "definition" | "phases" | "gates"; index: -1; field: string };

/** A quale elemento si riferisce il path di un problema (es. "transitions[3].gates"). */
export function problemTarget(path: string): Target {
  const m = /^(phases|transitions|gates|rules)\[(\d+)\](?:\.(.+))?$/.exec(path);
  if (m) {
    const kind = ({ phases: "phase", transitions: "transition", gates: "gate", rules: "rule" } as const)[m[1] as "phases"];
    return { kind, index: Number(m[2]), field: m[3] ?? "" };
  }
  if (path === "initial" || path === "phases" || path === "gates") return { kind: path, index: -1, field: "" };
  return { kind: "definition", index: -1, field: path };
}

// ---------------------------------------------------------------------------
// Differenze leggibili rispetto alla versione corrente

const trName = (t: ProcessTransition) =>
  t.system ? tr("configurazione.processEditor.diff.trSystem", { from: t.from.join(", "), to: t.to }) : tr("configurazione.processEditor.diff.trUser", { label: t.label || tr("configurazione.processEditor.senzaEtichetta"), to: t.to });
const trId = (t: ProcessTransition) => (t.system ? `s|${[...t.from].sort().join(",").toLowerCase()}|${t.to.toLowerCase()}` : `u|${(t.label ?? "").toLowerCase()}|${t.to.toLowerCase()}`);
const list = (xs: string[]) => xs.map((x) => tr("configurazione.processEditor.quoted", { text: x })).join(", ");
const added = (a: string[] = [], b: string[] = []) => b.filter((x) => !a.some((y) => eq(x, y)));

/** Elenco in italiano di ciò che cambia passando da `before` ad `after`. */
export function describeDiff(
  before: Definition | undefined,
  after: Definition,
  names: { role?: (k: string) => string; renames?: Map<string, string> } = {},
): string[] {
  const role = names.role ?? ((k) => k);
  const gname = (k: string) => after.gates?.find((g) => g.key === k)?.name ?? before?.gates?.find((g) => g.key === k)?.name ?? k;
  if (!before) return [tr("configurazione.processEditor.diff.first")];
  const out: string[] = [];
  const bp = before.phases;
  const ap = after.phases;
  const removed = bp.filter((p) => !ap.some((q) => eq(p.name, q.name)));
  const addedP = ap.filter((p) => !bp.some((q) => eq(p.name, q.name)));
  // Rinomine: quelle fatte nell'editor; altrimenti una fase tolta e una aggiunta che sono
  // le uniche con la stessa categoria e lo stesso posto (dentro o fuori percorso).
  const renamed = new Map<string, string>();
  for (const [o, n] of names.renames ?? []) {
    if (removed.some((p) => eq(p.name, o)) && addedP.some((p) => eq(p.name, n))) renamed.set(removed.find((p) => eq(p.name, o))!.name, addedP.find((p) => eq(p.name, n))!.name);
  }
  const sig = (p: Phase) => `${p.category}|${!!p.off_path}`;
  for (const r of removed) {
    if (renamed.has(r.name)) continue;
    const rs = removed.filter((x) => !renamed.has(x.name) && sig(x) === sig(r));
    const as = addedP.filter((x) => ![...renamed.values()].includes(x.name) && sig(x) === sig(r));
    if (rs.length === 1 && as.length === 1) renamed.set(r.name, as[0].name);
  }
  for (const [o, n] of renamed) out.push(tr("configurazione.processEditor.diff.renamedStage", { from: o, to: n }));
  for (const p of addedP) if (![...renamed.values()].includes(p.name)) out.push(p.off_path ? tr("configurazione.processEditor.diff.addedStageOff", { name: p.name }) : tr("configurazione.processEditor.diff.addedStage", { name: p.name }));
  for (const p of removed) if (!renamed.has(p.name)) out.push(tr("configurazione.processEditor.diff.removedStage", { name: p.name }));
  const rn = (n: string) => [...renamed.entries()].find(([, v]) => eq(v, n))?.[0] ?? n;
  for (const p of ap) {
    const o = bp.find((q) => eq(q.name, rn(p.name)));
    if (!o) continue;
    if (o.category !== p.category) out.push(tr("configurazione.processEditor.diff.stageNowCategory", { name: p.name, now: categoryName(p.category), before: categoryName(o.category) }));
    if (!!o.off_path !== !!p.off_path) out.push(p.off_path ? tr("configurazione.processEditor.diff.stageLeavesPath", { name: p.name }) : tr("configurazione.processEditor.diff.stageJoinsPath", { name: p.name }));
    if ((o.description ?? "") !== (p.description ?? "")) out.push(tr("configurazione.processEditor.diff.stageDescription", { name: p.name }));
    if ((o.color ?? "") !== (p.color ?? "")) out.push(tr("configurazione.processEditor.diff.stageColor", { name: p.name }));
  }
  const order = (ps: Phase[]) => ps.filter((p) => !p.off_path).map((p) => rn(p.name).toLowerCase()).join("|");
  const common = (ps: Phase[]) => order(ps.filter((p) => bp.some((q) => eq(q.name, rn(p.name)))));
  if (common(ap) !== order(bp.filter((p) => ap.some((q) => eq(rn(q.name), p.name))))) out.push(tr("configurazione.processEditor.diff.order"));
  if (!eq(rn(after.initial), before.initial)) out.push(tr("configurazione.processEditor.diff.initial", { after: after.initial, before: before.initial }));

  const norm = (t: ProcessTransition) => ({ ...t, from: t.from.map(rn), to: rn(t.to) });
  const bt = before.transitions;
  const at = after.transitions.map(norm);
  const used = new Set<number>();
  at.forEach((t, i) => {
    const shown = after.transitions[i];
    const j = bt.findIndex((x, k) => !used.has(k) && trId(x) === trId(t));
    if (j < 0) {
      const extra = [
        tr("configurazione.processEditor.diff.from", { list: shown.from.map((f) => (f === "*" ? tr("configurazione.processEditor.qualunqueFase") : f)).join(", ") }),
        shown.roles?.length && tr("configurazione.processEditor.diff.forRoles", { list: list(shown.roles.map(role)) }),
        shown.gates?.length && tr("configurazione.processEditor.diff.withGates", { count: shown.gates.length, list: list(shown.gates.map(gname)) }),
        shown.when && tr("configurazione.processEditor.diff.onlyIf", { when: shown.when }),
        shown.approval && tr("configurazione.processEditor.diff.opensApproval"),
      ].filter(Boolean);
      out.push(tr("configurazione.processEditor.diff.addedTransition", { name: trName(shown), details: extra.join("; ") }));
      return;
    }
    used.add(j);
    const o = bt[j];
    const n = trName(shown);
    const fa = added(o.from, t.from), fr = added(t.from, o.from);
    if (fa.length) out.push(tr("configurazione.processEditor.diff.trAlsoFrom", { name: n, list: list(fa.map((f) => (f === "*" ? tr("configurazione.processEditor.qualunqueFase") : f))) }));
    if (fr.length) out.push(tr("configurazione.processEditor.diff.trNoLongerFrom", { name: n, list: list(fr.map((f) => (f === "*" ? tr("configurazione.processEditor.qualunqueFase") : f))) }));
    const ga = added(o.gates, t.gates), gr = added(t.gates, o.gates);
    if (ga.length) out.push(tr("configurazione.processEditor.diff.trNowGates", { name: n, count: ga.length, list: list(ga.map(gname)) }));
    if (gr.length) out.push(tr("configurazione.processEditor.diff.trNoLongerGates", { name: n, count: gr.length, list: list(gr.map(gname)) }));
    const ra = added(o.roles, t.roles), rr = added(t.roles, o.roles);
    if (ra.length) out.push(tr("configurazione.processEditor.diff.trNowRoles", { name: n, list: list(ra.map(role)) }));
    if (rr.length) out.push(tr("configurazione.processEditor.diff.trNoLongerRoles", { name: n, list: list(rr.map(role)) }));
    if ((o.when ?? "") !== (t.when ?? "")) out.push(t.when ? tr("configurazione.processEditor.diff.trNowWhen", { name: n, when: t.when }) : tr("configurazione.processEditor.diff.trNoLongerWhen", { name: n, when: o.when ?? "" }));
    if ((o.approval ?? "") !== (t.approval ?? "")) out.push(t.approval ? tr("configurazione.processEditor.diff.trNowApproval", { name: n }) : tr("configurazione.processEditor.diff.trNoLongerApproval", { name: n }));
    if (!!o.comment_required !== !!t.comment_required) out.push(t.comment_required ? tr("configurazione.processEditor.diff.trNowReason", { name: n }) : tr("configurazione.processEditor.diff.trNoLongerReason", { name: n }));
  });
  bt.forEach((t, k) => {
    if (!used.has(k)) out.push(tr("configurazione.processEditor.diff.removedTransition", { name: trName(t) }));
  });

  const bg = before.gates ?? [], ag = after.gates ?? [];
  for (const g of ag) {
    const o = bg.find((x) => x.key === g.key);
    if (!o) out.push(tr("configurazione.processEditor.diff.newGate", { name: g.name }));
    else if (o.name !== g.name || (o.condition ?? "") !== (g.condition ?? "") || (o.check ?? "") !== (g.check ?? "") || (o.message ?? "") !== (g.message ?? "")) {
      out.push((o.condition ?? "") !== (g.condition ?? "") && g.condition ? tr("configurazione.processEditor.diff.changedGateNow", { name: g.name, condition: g.condition }) : tr("configurazione.processEditor.diff.changedGate", { name: g.name }));
    }
  }
  for (const g of bg) if (!ag.some((x) => x.key === g.key)) out.push(tr("configurazione.processEditor.diff.removedGate", { name: g.name }));
  const br = before.rules ?? [], ar = after.rules ?? [];
  for (const r of ar) {
    const o = br.find((x) => x.key === r.key);
    if (!o) out.push(tr("configurazione.processEditor.diff.newRule", { name: r.name }));
    else if (JSON.stringify(clean({ phases: [], initial: "", transitions: [], rules: [o] }).rules) !== JSON.stringify(clean({ phases: [], initial: "", transitions: [], rules: [r] }).rules)) {
      out.push(tr("configurazione.processEditor.diff.changedRule", { name: r.name }));
    }
  }
  for (const r of br) if (!ar.some((x) => x.key === r.key)) out.push(tr("configurazione.processEditor.diff.removedRule", { name: r.name }));
  return out;
}

// ---------------------------------------------------------------------------
// Disposizione del diagramma

export const LAYOUT = { W: 140, H: 50, P: 176, PAD: 24, LEVEL: 22 };
export const ANY = "*";

export type NodeBox = { name: string; x: number; y: number; w: number; h: number; offPath: boolean; pseudo?: boolean };
export type EdgeGroup = {
  id: string;
  transition: number;
  lane: "top" | "mid";
  d: string;
  arrow: string; // poligono della punta
  label: { x: number; y: number; text: string; full: string };
  system: boolean;
};
export type Layout = { width: number; height: number; nodes: NodeBox[]; edges: EdgeGroup[]; mainY: number; offY: number };

const short = (s: string, n: number) => (s.length > n ? `${s.slice(0, n - 1)}…` : s);
const labelWidth = (s: string) => s.length * 6.4 + 14;

/**
 * Dispone fasi e passaggi: il percorso principale in fila, le fasi fuori percorso (e
 * "qualunque fase") su una riga sotto. I passaggi in avanti corrono sopra la fila,
 * quelli all'indietro e quelli verso o da fuori percorso nella fascia in mezzo, su
 * livelli diversi perché etichette e linee non si sovrappongano.
 */
export function layout(d: Definition): Layout {
  const { W, H, P, PAD, LEVEL } = LAYOUT;
  const main = d.phases.filter((p) => !p.off_path);
  const off = d.phases.filter((p) => p.off_path);
  const mainIdx = new Map(main.map((p, i) => [p.name.toLowerCase(), i]));
  const offSet = new Set(off.map((p) => p.name.toLowerCase()));
  const hasAny = d.transitions.some((t) => t.from.includes(ANY));
  const cx = (i: number) => PAD + i * P + W / 2;

  type Group = { id: string; t: number; lane: "top" | "mid"; sources: string[]; target: string };
  const groups: Group[] = [];
  d.transitions.forEach((t, ti) => {
    const to = t.to.toLowerCase();
    const toMain = mainIdx.get(to);
    if (toMain === undefined && !offSet.has(to)) return;
    const fwd: string[] = [];
    const mid: string[] = [];
    for (const f of new Set(t.from.map((x) => x.toLowerCase()))) {
      if (f === to) continue;
      const fi = mainIdx.get(f);
      if (fi !== undefined && toMain !== undefined && fi < toMain) fwd.push(f);
      else if (fi !== undefined || offSet.has(f) || f === ANY) mid.push(f);
    }
    if (fwd.length) groups.push({ id: `${ti}f`, t: ti, lane: "top", sources: fwd, target: to });
    if (mid.length) groups.push({ id: `${ti}m`, t: ti, lane: "mid", sources: mid, target: to });
  });

  // Posto delle fasi fuori percorso: sotto la media delle fasi collegate, senza sovrapporsi.
  const want = new Map<string, number>();
  const offNames = [...(hasAny ? [ANY] : []), ...off.map((p) => p.name.toLowerCase())];
  for (const n of offNames) {
    const xs: number[] = [];
    for (const g of groups) {
      if (g.target === n) g.sources.forEach((s) => mainIdx.has(s) && xs.push(mainIdx.get(s)!));
      if (g.sources.includes(n) && mainIdx.has(g.target)) xs.push(mainIdx.get(g.target)!);
    }
    want.set(n, n === ANY ? -1 : xs.length ? xs.reduce((a, b) => a + b, 0) / xs.length : main.length);
  }
  const slot = new Map<string, number>();
  let next = 0;
  for (const n of [...offNames].sort((a, b) => want.get(a)! - want.get(b)!)) {
    const s = Math.max(next, Math.round(want.get(n)!));
    slot.set(n, s);
    next = s + 1;
  }
  const cols = Math.max(main.length, next, 1);

  // Porte: punti di attacco sui lati dei blocchi, distribuiti in larghezza.
  type Port = { node: string; side: "top" | "bottom"; key: string; toward: number };
  const ports: Port[] = [];
  const nodeX = (n: string) => (mainIdx.has(n) ? cx(mainIdx.get(n)!) : cx(slot.get(n) ?? 0));
  const sideOf = (n: string, lane: "top" | "mid"): "top" | "bottom" => (lane === "top" ? "top" : mainIdx.has(n) ? "bottom" : "top");
  for (const g of groups) {
    const tx = nodeX(g.target);
    const sx = g.sources.reduce((a, s) => a + nodeX(s), 0) / g.sources.length;
    ports.push({ node: g.target, side: sideOf(g.target, g.lane), key: `${g.id}>`, toward: sx });
    for (const s of g.sources) ports.push({ node: s, side: sideOf(s, g.lane), key: `${g.id}<${s}`, toward: tx });
  }
  const portX = new Map<string, number>();
  const bySide = new Map<string, Port[]>();
  for (const p of ports) bySide.set(`${p.node}|${p.side}`, [...(bySide.get(`${p.node}|${p.side}`) ?? []), p]);
  for (const ps of bySide.values()) {
    ps.sort((a, b) => a.toward - b.toward);
    const c = nodeX(ps[0].node);
    const span = Math.min(W - 24, (ps.length - 1) * 14);
    ps.forEach((p, i) => portX.set(p.key, ps.length === 1 ? c : c - span / 2 + (span * i) / (ps.length - 1)));
  }

  // Livelli: intervalli orizzontali (linee ed etichetta) che non si sovrappongono.
  type Placed = { g: Group; xs: number[]; lo: number; hi: number; mid: number; text: string; full: string; level: number };
  const placed: Placed[] = groups.map((g) => {
    const t = d.transitions[g.t];
    const xs = [portX.get(`${g.id}>`)!, ...g.sources.map((s) => portX.get(`${g.id}<${s}`)!)];
    const lo = Math.min(...xs), hi = Math.max(...xs);
    const noLabel = tr("configurazione.processEditor.senzaEtichetta");
    const full = t.system ? tr("configurazione.processEditor.layout.outcomeSystem", { label: t.label || tr("configurazione.processEditor.layout.outcome") }) : t.label || noLabel;
    const text = short(t.label || (t.system ? tr("configurazione.processEditor.layout.outcome") : noLabel), 22) + (t.gates?.length ? ` · ${t.gates.length}g` : "");
    const mid = (lo + hi) / 2;
    const lw = labelWidth(text);
    return { g, xs, lo: Math.min(lo, mid - lw / 2), hi: Math.max(hi, mid + lw / 2), mid, text, full, level: 0 };
  });
  const levels = { top: [] as [number, number][][], mid: [] as [number, number][][] };
  for (const p of [...placed].sort((a, b) => a.hi - a.lo - (b.hi - b.lo))) {
    const lv = levels[p.g.lane];
    let k = 0;
    while (lv[k]?.some(([a, b]) => p.lo < b + 10 && p.hi > a - 10)) k++;
    (lv[k] ??= []).push([p.lo, p.hi]);
    p.level = k;
  }
  const topN = levels.top.length;
  const midN = levels.mid.length;
  const mainY = topN ? 26 + (topN - 1) * LEVEL + 18 : 18;
  const busY = (lane: "top" | "mid", k: number) => (lane === "top" ? mainY - 18 - k * LEVEL : mainY + H + 18 + k * LEVEL);
  const offY = midN ? busY("mid", midN - 1) + 26 : mainY + H + 44;
  const hasOffRow = offNames.length > 0;
  const height = (hasOffRow ? offY + H : midN ? busY("mid", midN - 1) + 14 : mainY + H) + 18;

  const nodes: NodeBox[] = [
    ...main.map((p, i) => ({ name: p.name, x: PAD + i * P, y: mainY, w: W, h: H, offPath: false })),
    ...offNames.map((n) => ({
      name: n === ANY ? ANY : off.find((p) => p.name.toLowerCase() === n)!.name,
      x: PAD + slot.get(n)! * P,
      y: offY,
      w: W,
      h: H,
      offPath: true,
      pseudo: n === ANY,
    })),
  ];
  const portY = (n: string, side: "top" | "bottom") => (mainIdx.has(n) ? mainY : offY) + (side === "bottom" ? H : 0);

  const edges: EdgeGroup[] = placed.map((p) => {
    const { g } = p;
    const by = busY(g.lane, p.level);
    const segs: string[] = [];
    for (const s of g.sources) {
      const x = portX.get(`${g.id}<${s}`)!;
      segs.push(`M${x} ${portY(s, sideOf(s, g.lane))}V${by}`);
    }
    const lo = Math.min(...p.xs), hi = Math.max(...p.xs);
    if (hi > lo) segs.push(`M${lo} ${by}H${hi}`);
    const tx = portX.get(`${g.id}>`)!;
    const side = sideOf(g.target, g.lane);
    const ty = portY(g.target, side);
    const dir = side === "top" ? 1 : -1; // 1: la punta scende sul lato superiore
    segs.push(`M${tx} ${by}V${ty - dir * 7}`);
    const arrow = `${tx},${ty} ${tx - 4.5},${ty - dir * 8} ${tx + 4.5},${ty - dir * 8}`;
    return {
      id: g.id,
      transition: g.t,
      lane: g.lane,
      d: segs.join(""),
      arrow,
      label: { x: p.mid, y: by, text: p.text, full: p.full },
      system: !!d.transitions[g.t].system,
    };
  });
  return { width: PAD * 2 + cols * P - (P - W), height, nodes, edges, mainY, offY };
}

/** Nome leggibile di un fatto che l'elenco dei dati non descrive: la chiave senza trattini bassi. */
export function humanFact(key: string): string {
  const k = key.replace(/^ruolo\./, "ruolo ").replaceAll("_", " ").replaceAll(".", " ");
  return k.charAt(0).toUpperCase() + k.slice(1);
}

/**
 * La condizione in parole («Valutazioni obbligatorie aperte maggiore di 0») al posto della
 * sintassi della query. `names` associa le chiavi dei dati ai loro nomi; senza, la chiave
 * si rende leggibile da sé. Se la condizione non si esprime a righe, resta il testo.
 */
export function describeCondition(src: string | undefined | null, names?: Record<string, string>): string {
  const text = (src ?? "").trim();
  if (!text) return "";
  let alts: Clause[][];
  try {
    alts = parseClauses(text);
  } catch {
    return text;
  }
  if (alts.length === 0) return text;
  const clause = (c: Clause) => {
    const name = names?.[c.key] ?? humanFact(c.key);
    const [m] = c.values;
    if (c.values.length === 1 && m.op === "present") return tr(c.negate ? "configurazione.processEditor.describe.absent" : "configurazione.processEditor.describe.present", { name });
    if (c.values.length === 1 && m.op === "absent") return tr(c.negate ? "configurazione.processEditor.describe.present" : "configurazione.processEditor.describe.absent", { name });
    if (c.values.length === 1 && m.op === "bool") return tr(`configurazione.processEditor.describe.${m.b ? "yes" : "no"}${c.negate ? "Not" : ""}` as Key, { name });
    if (c.values.length === 1 && (m.op === "gt" || m.op === "ge" || m.op === "lt" || m.op === "le")) return tr(`configurazione.processEditor.describe.${m.op}${c.negate ? "Not" : ""}` as Key, { name, value: m.raw });
    return tr(c.negate ? "configurazione.processEditor.describe.isNot" : "configurazione.processEditor.describe.is", { name, values: c.values.map((v) => v.raw).join(", ") });
  };
  return alts.map((a) => a.map(clause).join(tr("configurazione.processEditor.describe.and"))).join(tr("configurazione.processEditor.describe.or"));
}
