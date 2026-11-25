// Logica pura dell'editor dei questionari (piano, passo 7): operazioni sulla definizione,
// analisi dal vivo, differenze tra versioni, risposte di prova. Niente React, così si prova.
import {
  compiled,
  conditionKeys,
  points,
  scaleRange,
  type Condition,
  type Derivation,
  type QDefinition,
  type QOption,
  type QSection,
  type QType,
  type Question,
} from "../../condition.ts";
import type { FactInfo } from "../../api";
import { tr } from "../../i18n/core.ts";

export const TYPES: { type: QType; name: string; hint: string }[] = [
  { type: "yes_no", get name() { return tr("configurazione.qEditor.model.type.yes_no"); }, get hint() { return tr("configurazione.qEditor.model.type.yes_noHint"); } },
  { type: "single", get name() { return tr("configurazione.qEditor.model.type.single"); }, get hint() { return tr("configurazione.qEditor.model.type.singleHint"); } },
  { type: "multi", get name() { return tr("configurazione.qEditor.model.type.multi"); }, get hint() { return tr("configurazione.qEditor.model.type.multiHint"); } },
  { type: "text", get name() { return tr("configurazione.qEditor.model.type.text"); }, get hint() { return tr("configurazione.qEditor.model.type.textHint"); } },
  { type: "number", get name() { return tr("configurazione.qEditor.model.type.number"); }, get hint() { return tr("configurazione.qEditor.model.type.numberHint"); } },
  { type: "date", get name() { return tr("configurazione.qEditor.model.type.date"); }, get hint() { return tr("configurazione.qEditor.model.type.dateHint"); } },
  { type: "attachment", get name() { return tr("configurazione.qEditor.model.type.attachment"); }, get hint() { return tr("configurazione.qEditor.model.type.attachmentHint"); } },
  { type: "scale", get name() { return tr("configurazione.qEditor.model.type.scale"); }, get hint() { return tr("configurazione.qEditor.model.type.scaleHint"); } },
  { type: "matrix", get name() { return tr("configurazione.qEditor.model.type.matrix"); }, get hint() { return tr("configurazione.qEditor.model.type.matrixHint"); } },
];
export const typeName = (t: string) => TYPES.find((x) => x.type === t)?.name ?? t;
/** Tipi che possono avere un punteggio. */
export const SCORABLE: QType[] = ["yes_no", "single", "multi", "scale", "matrix"];

export const skeleton = (): QDefinition => ({
  name: tr("configurazione.qEditor.model.skeleton.name"),
  description: "",
  sections: [
    {
      key: "generale",
      title: tr("configurazione.qEditor.model.skeleton.general"),
      questions: [
        { key: "descrizione", label: tr("configurazione.qEditor.model.skeleton.describe"), type: "text", multiline: true, required: true },
        { key: "dati_personali", label: tr("configurazione.qEditor.model.skeleton.personal"), type: "yes_no", required: true },
        { key: "quali_dati", label: tr("configurazione.qEditor.model.skeleton.which"), type: "text", visible_when: "dati_personali: sì", required: true }, // i18n-ok: sintassi delle condizioni
      ],
    },
  ],
  derivations: [{ key: "registro", when: "dati_personali: sì", // i18n-ok: sintassi delle condizioni
    text: tr("configurazione.qEditor.model.skeleton.register"), category: tr("configurazione.qEditor.model.skeleton.privacy") }],
});

// ---------------------------------------------------------------------------
// Chiavi

export function keyFrom(text: string, taken: string[], fallback = "voce"): string {
  const base =
    text
      .normalize("NFD")
      .replace(/[̀-ͯ]/g, "")
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "_")
      .replace(/^_+|_+$/g, "")
      .replace(/^[0-9_]+/, "")
      .slice(0, 40)
      .replace(/_+$/, "") || fallback;
  if (!taken.includes(base)) return base;
  for (let i = 2; ; i++) if (!taken.includes(`${base}_${i}`)) return `${base}_${i}`;
}

export const allQuestions = (d: QDefinition): Question[] => (d.sections ?? []).flatMap((s) => s.questions ?? []);
export const allKeys = (d: QDefinition): string[] => [...allQuestions(d).map((q) => q.key), ...(d.sections ?? []).map((s) => s.key)];

// ---------------------------------------------------------------------------
// Creazione

export function newQuestion(type: QType, taken: string[], label = ""): Question {
  const key = keyFrom(label || typeName(type), taken, "domanda");
  const q: Question = { key, label: label || "", type };
  switch (type) {
    case "single":
    case "multi":
      q.options = [
        { value: "prima_scelta", label: tr("configurazione.qEditor.model.choice.first") },
        { value: "seconda_scelta", label: tr("configurazione.qEditor.model.choice.second") },
      ];
      break;
    case "scale":
      q.min = 1;
      q.max = 5;
      q.min_label = tr("configurazione.qEditor.model.scale.min");
      q.max_label = tr("configurazione.qEditor.model.scale.max");
      break;
    case "matrix":
      q.rows = [
        { value: "prima_riga", label: tr("configurazione.qEditor.model.row.first") },
        { value: "seconda_riga", label: tr("configurazione.qEditor.model.row.second") },
      ];
      q.options = [
        { value: "no", label: tr("configurazione.qEditor.model.answer.no") },
        { value: "si", label: tr("configurazione.qEditor.model.answer.yes") },
      ];
      break;
    case "text":
      q.multiline = true;
      break;
  }
  return q;
}

export function newSection(d: QDefinition): QSection {
  const key = keyFrom(tr("configurazione.qEditor.model.newSectionKey"), allKeys(d), "sezione");
  return { key, title: "", questions: [] };
}

/** Cambia il tipo mantenendo ciò che ha senso e scartando il resto. */
export function changeType(q: Question, type: QType): Question {
  if (q.type === type) return q;
  const fresh = newQuestion(type, [], q.label);
  const keep: Question = { key: q.key, label: q.label, type, required: q.required, help: q.help, visible_when: q.visible_when, prefill: q.prefill, weight: q.weight };
  const choice = (t: QType) => t === "single" || t === "multi";
  if ((choice(type) || type === "matrix") && (choice(q.type) || q.type === "matrix") && q.options?.length) keep.options = q.options;
  else if (fresh.options) keep.options = fresh.options;
  if (type === "matrix") keep.rows = q.rows?.length ? q.rows : fresh.rows;
  if (type === "scale") Object.assign(keep, { min: 1, max: 5, min_label: fresh.min_label, max_label: fresh.max_label });
  if (type === "number") Object.assign(keep, { min: q.min, max: q.max, unit: q.unit });
  if (type === "text") keep.multiline = true;
  return JSON.parse(JSON.stringify(keep));
}

/** Rimuove campi vuoti prima di salvare. */
export function clean(d: QDefinition): QDefinition {
  const out = structuredClone(d);
  const drop = (o: Record<string, unknown>) => {
    for (const k of Object.keys(o)) if (o[k] === "" || o[k] === undefined || o[k] === false || (Array.isArray(o[k]) && !(o[k] as unknown[]).length && k !== "questions" && k !== "sections")) delete o[k];
  };
  for (const s of out.sections) {
    for (const q of s.questions) {
      for (const list of [q.options, q.rows]) for (const o of list ?? []) drop(o as unknown as Record<string, unknown>);
      drop(q as unknown as Record<string, unknown>);
      if (q.type !== "scale" && q.type !== "number") {
        delete q.min;
        delete q.max;
      }
    }
    drop(s as unknown as Record<string, unknown>);
  }
  if (!out.derivations?.length) delete out.derivations;
  else for (const r of out.derivations) drop(r as unknown as Record<string, unknown>);
  if (!out.scoring?.thresholds?.length) delete out.scoring;
  drop(out as unknown as Record<string, unknown>);
  return out;
}

export function normalize(d: QDefinition | undefined | null): QDefinition {
  const n = structuredClone(d ?? skeleton()) as QDefinition;
  n.sections ??= [];
  for (const s of n.sections) s.questions ??= [];
  return n;
}

// ---------------------------------------------------------------------------
// Spostamenti (le liste sono modificate sul posto: si lavora su una copia)

export function findQuestion(d: QDefinition, key: string): [number, number] | null {
  for (let i = 0; i < d.sections.length; i++) {
    const j = d.sections[i].questions.findIndex((q) => q.key === key);
    if (j >= 0) return [i, j];
  }
  return null;
}

/** Porta la domanda nella sezione `toSection`, prima della posizione `index` (o in fondo). */
export function moveQuestion(d: QDefinition, key: string, toSection: number, index?: number) {
  const at = findQuestion(d, key);
  if (!at || !d.sections[toSection]) return;
  const [q] = d.sections[at[0]].questions.splice(at[1], 1);
  let i = index ?? d.sections[toSection].questions.length;
  if (at[0] === toSection && at[1] < i) i--;
  d.sections[toSection].questions.splice(Math.max(0, Math.min(i, d.sections[toSection].questions.length)), 0, q);
}

export function moveSection(d: QDefinition, from: number, to: number) {
  if (from === to || !d.sections[from]) return;
  const [s] = d.sections.splice(from, 1);
  d.sections.splice(Math.max(0, Math.min(to, d.sections.length)), 0, s);
}

export function move<T>(list: T[], from: number, to: number) {
  if (to < 0 || to >= list.length || from === to) return;
  const [x] = list.splice(from, 1);
  list.splice(to, 0, x);
}

// ---------------------------------------------------------------------------
// Fatti per il costruttore di condizioni

const kindOf = (q: Question): FactInfo["kind"] => {
  switch (q.type) {
    case "yes_no":
      return "bool";
    case "number":
    case "scale":
    case "attachment":
      return "number";
    case "date":
      return "date";
  }
  return "text";
};

/** La domanda come «fatto» per il costruttore: le sue risposte possibili sono i valori suggeriti. */
export function questionFact(q: Question): FactInfo {
  const f: FactInfo = { key: q.key, name: q.label || q.key, kind: kindOf(q), source: "domanda" };
  if (q.type === "single" || q.type === "multi") f.values = (q.options ?? []).map((o) => o.value);
  if (q.type === "matrix") f.values = (q.rows ?? []).flatMap((r) => (q.options ?? []).map((c) => `${r.value}.${c.value}`));
  if (q.type === "scale") f.example = String(scaleRange(q)[1]);
  return f;
}

/** Fatti disponibili alla condizione della domanda `key` (o della sezione, con `section`): le domande che vengono prima. */
export function factsBefore(d: QDefinition, base: FactInfo[], at: { question?: string; section?: number }): FactInfo[] {
  const before: Question[] = [];
  outer: for (let i = 0; i < d.sections.length; i++) {
    if (at.section === i && at.question === undefined) break;
    for (const q of d.sections[i].questions) {
      if (q.key === at.question) break outer;
      before.push(q);
    }
  }
  const taken = new Set(before.map((q) => q.key));
  return [...before.map(questionFact), ...base.filter((f) => !taken.has(f.key))];
}

// ---------------------------------------------------------------------------
// Analisi dal vivo

export type Finding = { level: "error" | "warning"; path: string; message: string; code: string };

type Clause = Condition["alts"][number][number];

/** Valori che una domanda può avere come fatto, in minuscolo; null se non si sa (testo libero, numeri…). */
function possibleValues(q: Question): Set<string> | null {
  switch (q.type) {
    case "single":
    case "multi":
      return new Set((q.options ?? []).flatMap((o) => [o.value.toLowerCase(), o.label.toLowerCase()]));
    case "matrix":
      return new Set((q.rows ?? []).flatMap((r) => (q.options ?? []).map((c) => `${r.value}.${c.value}`.toLowerCase())));
  }
  return null;
}

/** La clausola non può mai essere vera? Solo per i casi certi. */
function impossible(cl: Clause, q: Question | undefined, hidden: Set<string>): string | null {
  if (!q) return null;
  if (!cl.negate && hidden.has(cl.key)) return tr("configurazione.qEditor.model.neverShown", { label: q.label || q.key });
  if (cl.negate) return null;
  const pv = possibleValues(q);
  if (pv && cl.values.length && cl.values.every((m) => m.op === "eq" && !pv.has(m.raw.toLowerCase())))
    return tr("configurazione.qEditor.model.notPossible", { values: cl.values.map((m) => m.raw).join(", "), label: q.label || q.key });
  if (q.type === "yes_no" && cl.values.length && cl.values.every((m) => m.op === "eq")) return tr("configurazione.qEditor.model.yesNoAnswer", { label: q.label || q.key });
  return null;
}

/**
 * Controlli sul questionario che il server non fa: domande che non si possono mai vedere,
 * condizioni su domande che vengono dopo o inesistenti, sezioni vuote, soglie senza punteggio.
 * I percorsi sono quelli del server (`sections[0].questions[1].visible_when`).
 */
export function analyze(d: QDefinition, knownFacts: string[] = []): Finding[] {
  const out: Finding[] = [];
  const pos = new Map<string, number>();
  const byKey = new Map<string, Question>();
  let n = 0;
  for (const s of d.sections) for (const q of s.questions) (pos.set(q.key, pos.get(q.key) ?? n), byKey.set(q.key, byKey.get(q.key) ?? q), n++);
  const facts = new Set(knownFacts.map((k) => k.toLowerCase()));
  const hidden = new Set<string>(); // domande che non compaiono mai
  const add = (level: Finding["level"], path: string, message: string, code: string) => out.push({ level, path, message, code });

  /** Esamina una condizione nel punto `at`; restituisce il motivo per cui non può mai essere vera. */
  const check = (path: string, src: string | undefined, at: number): string | null => {
    if (!src?.trim()) return null;
    const c = compiled(src);
    if (!c) return null; // errore di sintassi: lo dice il server
    for (const k of conditionKeys(c)) {
      const p = pos.get(k);
      if (p !== undefined && p >= at) add("error", path, tr("configurazione.qEditor.model.forward", { label: byKey.get(k)?.label || k }), "forward");
      else if (p === undefined && !facts.has(k) && facts.size > 0) add("warning", path, tr("configurazione.qEditor.model.unknownFact", { key: k }), "unknown");
    }
    if (!c.alts.length) return null;
    const why = c.alts.map((alt) => {
      for (const cl of alt) {
        const q = byKey.get(cl.key);
        const p = pos.get(cl.key);
        const r = q && p !== undefined && p < at ? impossible(cl, q, hidden) : null;
        if (r) return r;
      }
      return null;
    });
    return why.every(Boolean) ? why[0] : null;
  };

  if (!d.sections.length) add("error", "sections", tr("configurazione.qEditor.model.needSection"), "empty");
  let at = 0;
  d.sections.forEach((s, i) => {
    const sp = `sections[${i}]`;
    if (!s.questions.length) add("error", `${sp}.questions`, tr("configurazione.qEditor.model.sectionEmpty", { title: s.title || s.key }), "empty");
    const sWhy = check(`${sp}.visible_when`, s.visible_when, at);
    if (sWhy) add("warning", `${sp}.visible_when`, tr("configurazione.qEditor.model.sectionNever", { title: s.title || s.key, why: sWhy }), "unreachable");
    s.questions.forEach((q, j) => {
      const qp = `${sp}.questions[${j}]`;
      const why = check(`${qp}.visible_when`, q.visible_when, at);
      if (sWhy || why) {
        hidden.add(q.key);
        if (why) add("warning", `${qp}.visible_when`, tr("configurazione.qEditor.model.questionNever", { label: q.label || q.key, why }), "unreachable");
        else add("warning", qp, tr("configurazione.qEditor.model.inHiddenSection", { label: q.label || q.key }), "unreachable");
      }
      if (!q.label.trim()) add("error", `${qp}.label`, tr("configurazione.qEditor.model.labelRequired"), "label");
      if (q.required && hidden.has(q.key)) add("warning", `${qp}.required`, tr("configurazione.qEditor.model.requiredNeverShown", { label: q.label || q.key }), "required");
      if ((q.type === "single" || q.type === "multi" || q.type === "matrix") && (q.options?.length ?? 0) < 2) add("error", `${qp}.options`, tr("configurazione.qEditor.model.needTwoChoices"), "options");
      if (q.type === "matrix" && !q.rows?.length) add("error", `${qp}.rows`, tr("configurazione.qEditor.model.needRows"), "options");
      at++;
    });
  });
  (d.derivations ?? []).forEach((r, i) => {
    const why = check(`derivations[${i}].when`, r.when, Number.MAX_SAFE_INTEGER);
    if (why) add("warning", `derivations[${i}].when`, tr("configurazione.qEditor.model.ruleNever", { key: r.key, why }), "unreachable");
  });
  const scored = allQuestions(d).filter((q) => points(q, null)[1] > 0 && (q.weight ?? 1) > 0);
  if (d.scoring?.thresholds?.length && !scored.length) add("warning", "scoring", tr("configurazione.qEditor.model.scoringNoScored"), "scoring");
  if (scored.length && !d.scoring?.thresholds?.length) add("warning", "scoring", tr("configurazione.qEditor.model.scoringNoThresholds"), "scoring");
  return out;
}

/** Fatti non-domanda usati dalle condizioni (per simulare i dati dell'elemento nell'anteprima). */
export function usedFacts(d: QDefinition): string[] {
  const questions = new Set(allKeys(d));
  const srcs = [...d.sections.flatMap((s) => [s.visible_when, ...s.questions.map((q) => q.visible_when)]), ...(d.derivations ?? []).map((r) => r.when)];
  const keys = new Set<string>();
  for (const src of srcs) if (src?.trim()) for (const k of conditionKeys(compiled(src))) if (!questions.has(k)) keys.add(k);
  for (const q of allQuestions(d)) if (q.prefill) keys.add(q.prefill.toLowerCase());
  return [...keys].sort();
}

// ---------------------------------------------------------------------------
// Punteggio

export type ScoreRow = { key: string; label: string; weight: number; max: number; share: number };

/** Le domande con punteggio e quanto pesano sul totale. */
export function scoreTable(d: QDefinition): ScoreRow[] {
  const rows = allQuestions(d)
    .map((q) => ({ key: q.key, label: q.label || q.key, weight: q.weight ?? 1, max: points(q, null)[1] }))
    .filter((r) => r.max > 0 && r.weight > 0)
    .map((r) => ({ ...r, share: 0 }));
  const total = rows.reduce((a, r) => a + r.weight * r.max, 0);
  for (const r of rows) r.share = total ? (r.weight * r.max) / total : 0;
  return rows;
}

/** Risposte di prova che portano al punteggio più alto (o più basso). Le domande senza punteggio restano vuote. */
export function extremeAnswers(d: QDefinition, worst = false): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  const pick = (opts: QOption[] | undefined) => {
    const sc = (opts ?? []).filter((o) => o.score != null);
    if (!sc.length) return undefined;
    return sc.reduce((a, o) => (worst ? o.score! < a.score! : o.score! > a.score!) ? o : a).value;
  };
  for (const q of allQuestions(d)) {
    switch (q.type) {
      case "yes_no":
        if (q.yes_score != null || q.no_score != null) out[q.key] = worst ? (q.yes_score ?? 0) < (q.no_score ?? 0) : (q.yes_score ?? 0) >= (q.no_score ?? 0);
        break;
      case "single": {
        const v = pick(q.options);
        if (v !== undefined) out[q.key] = v;
        break;
      }
      case "multi":
        if (q.options?.some((o) => o.score != null)) out[q.key] = q.options.filter((o) => (worst ? (o.score ?? 0) < 0 : (o.score ?? 0) > 0)).map((o) => o.value);
        if (Array.isArray(out[q.key]) && !(out[q.key] as unknown[]).length) {
          const v = pick(q.options);
          if (v !== undefined && !worst) out[q.key] = [v];
          else delete out[q.key];
        }
        break;
      case "scale": {
        const [lo, hi] = scaleRange(q);
        out[q.key] = (worst ? !q.reverse : !!q.reverse) ? lo : hi;
        break;
      }
      case "matrix": {
        const v = pick(q.options);
        if (v !== undefined) out[q.key] = Object.fromEntries((q.rows ?? []).map((r) => [r.value, v]));
        break;
      }
    }
  }
  return out;
}

// ---------------------------------------------------------------------------
// Differenze tra versioni, in italiano

const qn = (q: Question) => tr("configurazione.processEditor.quoted", { text: q.label || q.key });
const optLabel = (o: QOption) => o.label || o.value;
const quote = (s: string) => tr("configurazione.processEditor.quoted", { text: s });
const same = (a: unknown, b: unknown) => JSON.stringify(a ?? null) === JSON.stringify(b ?? null);

function diffOptions(what: string, a: QOption[] = [], b: QOption[] = []): string[] {
  const out: string[] = [];
  const add = b.filter((o) => !a.some((x) => x.value === o.value));
  const del = a.filter((o) => !b.some((x) => x.value === o.value));
  if (add.length) out.push(tr("configurazione.qEditor.model.diff.optionsAdded", { what, list: add.map((o) => quote(optLabel(o))).join(", ") }));
  if (del.length) out.push(tr("configurazione.qEditor.model.diff.optionsRemoved", { what, list: del.map((o) => quote(optLabel(o))).join(", ") }));
  for (const o of b) {
    const p = a.find((x) => x.value === o.value);
    if (!p) continue;
    if (p.label !== o.label) out.push(tr("configurazione.qEditor.model.diff.optionRenamed", { what, old: p.label, new: o.label }));
    if ((p.score ?? null) !== (o.score ?? null)) out.push(tr("configurazione.qEditor.model.diff.optionScore", { what, label: optLabel(o), score: o.score ?? tr("configurazione.qEditor.model.diff.noScoreWord"), before: p.score ?? tr("configurazione.qEditor.model.diff.noScoreBefore") }));
  }
  return out;
}

/** Cosa cambia passando da `before` ad `after`, una frase per modifica. */
export function describeDiff(before: QDefinition | undefined | null, after: QDefinition, outcome: (k: string) => string = (k) => k): string[] {
  if (!before) return [tr("configurazione.qEditor.model.diff.first")];
  const out: string[] = [];
  if (before.name !== after.name) out.push(tr("configurazione.qEditor.model.diff.renamed", { after: after.name, before: before.name }));
  if ((before.description ?? "") !== (after.description ?? "")) out.push(tr("configurazione.qEditor.model.diff.description"));
  const bs = before.sections ?? [];
  const as = after.sections ?? [];
  for (const s of as) if (!bs.some((x) => x.key === s.key)) out.push(tr("configurazione.qEditor.model.diff.newSection", { title: s.title, count: s.questions.length }));
  for (const s of bs) if (!as.some((x) => x.key === s.key)) out.push(tr("configurazione.qEditor.model.diff.removedSection", { title: s.title, count: s.questions.length }));
  const common = as.filter((s) => bs.some((x) => x.key === s.key));
  if (!same(common.map((s) => s.key), bs.filter((s) => as.some((x) => x.key === s.key)).map((s) => s.key))) out.push(tr("configurazione.qEditor.model.diff.sectionOrder"));
  for (const s of common) {
    const o = bs.find((x) => x.key === s.key)!;
    if (o.title !== s.title) out.push(tr("configurazione.qEditor.model.diff.sectionRenamed", { title: s.title, new: s.title, old: o.title }));
    if ((o.description ?? "") !== (s.description ?? "")) out.push(tr("configurazione.qEditor.model.diff.sectionDescription", { title: s.title }));
    if ((o.visible_when ?? "") !== (s.visible_when ?? "")) out.push(s.visible_when ? tr("configurazione.qEditor.model.diff.sectionVisible", { title: s.title, when: s.visible_when }) : tr("configurazione.qEditor.model.diff.sectionAlways", { title: s.title }));
    if ((o.filled_by ?? "") !== (s.filled_by ?? "")) out.push(tr("configurazione.qEditor.model.diff.sectionFilledBy", { title: s.title, who: s.filled_by === "team" ? tr("configurazione.qEditor.model.diff.filledTeam") : tr("configurazione.qEditor.model.diff.filledStarterTeam") }));
  }
  const bq = new Map(bs.flatMap((s) => s.questions.map((q) => [q.key, { q, s }] as const)));
  const aq = new Map(as.flatMap((s) => s.questions.map((q) => [q.key, { q, s }] as const)));
  for (const [k, { q, s }] of aq) if (!bq.has(k)) out.push(tr("configurazione.qEditor.model.diff.newQuestion", { label: qn(q), section: s.title, type: typeName(q.type).toLowerCase() }));
  for (const [k, { q, s }] of bq) if (!aq.has(k)) out.push(tr("configurazione.qEditor.model.diff.removedQuestion", { label: qn(q), section: s.title }));
  for (const [k, { q, s }] of aq) {
    const p = bq.get(k);
    if (!p) continue;
    const o = p.q;
    if (p.s.key !== s.key) out.push(tr("configurazione.qEditor.model.diff.movedQuestion", { label: qn(q), from: p.s.title, to: s.title }));
    if (o.label !== q.label) out.push(tr("configurazione.qEditor.model.diff.questionSays", { old: o.label, new: q.label }));
    if (o.type !== q.type) out.push(tr("configurazione.qEditor.model.diff.typeChanged", { label: qn(q), from: typeName(o.type).toLowerCase(), to: typeName(q.type).toLowerCase() }));
    if (!!o.required !== !!q.required) out.push(q.required ? tr("configurazione.qEditor.model.diff.nowRequired", { label: qn(q) }) : tr("configurazione.qEditor.model.diff.noLongerRequired", { label: qn(q) }));
    if ((o.help ?? "") !== (q.help ?? "")) out.push(tr("configurazione.qEditor.model.diff.help", { label: qn(q) }));
    if ((o.visible_when ?? "") !== (q.visible_when ?? "")) out.push(q.visible_when ? tr("configurazione.qEditor.model.diff.questionVisible", { label: qn(q), when: q.visible_when }) : tr("configurazione.qEditor.model.diff.questionAlways", { label: qn(q) }));
    if ((o.weight ?? 1) !== (q.weight ?? 1)) out.push(tr("configurazione.qEditor.model.diff.weight", { label: qn(q), from: o.weight ?? 1, to: q.weight ?? 1 }));
    if ((o.yes_score ?? null) !== (q.yes_score ?? null) || (o.no_score ?? null) !== (q.no_score ?? null)) out.push(tr("configurazione.qEditor.model.diff.yesNoPoints", { label: qn(q) }));
    if ((o.prefill ?? "") !== (q.prefill ?? "")) out.push(tr("configurazione.qEditor.model.diff.prefill", { label: qn(q) }));
    if (o.min !== q.min || o.max !== q.max || !!o.reverse !== !!q.reverse) out.push(tr("configurazione.qEditor.model.diff.ends", { label: qn(q) }));
    out.push(...diffOptions(tr("configurazione.qEditor.model.diff.whatChoices", { label: qn(q) }), o.options, q.options).map((x) => x + "."));
    out.push(...diffOptions(tr("configurazione.qEditor.model.diff.whatRows", { label: qn(q) }), o.rows, q.rows).map((x) => x + "."));
  }
  const bd = before.derivations ?? [];
  const ad = after.derivations ?? [];
  const kind = (r: Derivation) => (r.kind === "requirement" ? tr("configurazione.qEditor.model.diff.kindRequirement") : tr("configurazione.qEditor.model.diff.kindPrescription"));
  for (const r of ad) {
    const o = bd.find((x) => x.key === r.key);
    if (!o) out.push(tr("configurazione.qEditor.model.diff.newRule", { kind: kind(r), text: r.text, when: r.when }));
    else if (o.when !== r.when || o.text !== r.text || (o.category ?? "") !== (r.category ?? "") || (o.kind ?? "") !== (r.kind ?? "")) out.push(o.when !== r.when ? tr("configurazione.qEditor.model.diff.changedRuleNow", { key: r.key, when: r.when }) : tr("configurazione.qEditor.model.diff.changedRule", { key: r.key }));
  }
  for (const r of bd) if (!ad.some((x) => x.key === r.key)) out.push(tr("configurazione.qEditor.model.diff.removedRule", { key: r.key }));
  const bt = before.scoring?.thresholds ?? [];
  const at = after.scoring?.thresholds ?? [];
  if (!same([...bt].sort((x, y) => x.min - y.min), [...at].sort((x, y) => x.min - y.min))) {
    out.push(
      at.length
        ? tr("configurazione.qEditor.model.diff.thresholds", { list: [...at].sort((x, y) => x.min - y.min).map((t) => tr("configurazione.qEditor.model.diff.threshold", { min: t.min, outcome: outcome(t.outcome) })).join(", ") })
        : tr("configurazione.qEditor.model.diff.noThresholds"),
    );
  }
  return out;
}
