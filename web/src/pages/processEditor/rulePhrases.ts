// Frasi delle regole «Quando… allora…» (docs/sviluppo/regole-frase.md): funzioni pure, senza
// React, provate con `pnpm test`. L'editor (RuleSentence.tsx) le usa per l'elenco e per i menu.
// Gli eventi sono quelli del motore (internal/engine/automation.go): create, enter:<fase>,
// exit:<fase>, change:<campo>, at:<data>:<±giorni>, every:day, every:week:<1-7>.
import type { ActionSchema, FactInfo, NamedKey, Rule, RuleAction } from "../../api";
import type { tr } from "../../i18n/core.ts";
import { describeCondition, splitOn } from "./model.ts";

type T = typeof tr;

/** Azioni offerte a frase, nell'ordine del menu. Le altre azioni di una regola restano modificabili a campi. */
export const SENTENCE_ACTIONS = ["open_review", "apply_baseline", "assign_role", "set_field", "notify", "add_comment", "create_linked", "webhook"] as const;
export type SentenceAction = (typeof SENTENCE_ACTIONS)[number];
export const isSentence = (kind: string): kind is SentenceAction => (SENTENCE_ACTIONS as readonly string[]).includes(kind);

export type RuleRefs = {
  reviewTypes: { key: string; name: string; scope: string }[];
  catalogs: { key: string; name: string }[];
  /** I nomi sono arrivati (o non si possono leggere): si può scrivere il nome di una regola nuova. */
  loaded: boolean;
};

/** Nomi per leggere la regola: ruoli, schemi delle azioni, fatti, campi, date, tipi, relazioni, spazi. */
export type Names = {
  roles?: NamedKey[];
  schemas?: ActionSchema[];
  facts?: Record<string, string>;
  fields?: NamedKey[];
  dates?: NamedKey[];
  types?: NamedKey[];
  relations?: NamedKey[];
  spaces?: NamedKey[];
};

// ---------------------------------------------------------------------------
// Eventi

export type RuleEvent =
  | { kind: "create" }
  | { kind: "enter" | "exit"; phase: string }
  | { kind: "change"; field: string }
  | { kind: "at"; date: string; offset: number }
  /** weekday 0 = ogni giorno, 1-7 = lunedì…domenica. */
  | { kind: "every"; weekday: number }
  | { kind: "unknown"; raw: string };

/** Scarto massimo, in giorni, di un evento «at:» (come nel motore). */
export const MAX_OFFSET_DAYS = 3650;
const KEY_RE = /^[a-z][a-z0-9_]*(\.[a-z0-9_]+)*$/;

/** Il campo «on» di una regola, analizzato come fa il motore; ciò che non si riconosce è «unknown». */
export function parseEvent(on: string): RuleEvent {
  const raw = on.trim();
  const i = raw.indexOf(":");
  const kind = i < 0 ? raw : raw.slice(0, i);
  const rest = i < 0 ? "" : raw.slice(i + 1);
  switch (kind) {
    case "create":
      if (i < 0) return { kind };
      break;
    case "enter":
    case "exit":
      return { kind, phase: splitOn(raw)[1] };
    case "change":
      if (KEY_RE.test(rest)) return { kind, field: rest };
      break;
    case "at": {
      const j = rest.lastIndexOf(":");
      if (j <= 0) break;
      const date = rest.slice(0, j);
      const off = rest.slice(j + 1).replace(/^\+/, "");
      const n = /^-?\d+$/.test(off) ? Number(off) : NaN;
      if (KEY_RE.test(date) && Number.isInteger(n) && Math.abs(n) <= MAX_OFFSET_DAYS) return { kind, date, offset: n };
      break;
    }
    case "every": {
      if (rest === "day") return { kind, weekday: 0 };
      const m = /^week:([1-7])$/.exec(rest);
      if (m) return { kind, weekday: Number(m[1]) };
      break;
    }
  }
  return { kind: "unknown", raw: on };
}

/** L'evento torna nel formato salvato. */
export function formatEvent(ev: RuleEvent): string {
  switch (ev.kind) {
    case "create":
      return "create";
    case "enter":
    case "exit":
      return `${ev.kind}:${ev.phase}`;
    case "change":
      return `change:${ev.field}`;
    case "at":
      return `at:${ev.date}:${ev.offset}`;
    case "every":
      return ev.weekday ? `every:week:${ev.weekday}` : "every:day";
  }
  return ev.raw;
}

/** Eventi eseguiti dal lavoratore (a tempo o periodici): hanno l'anteprima sui prossimi giorni e le esecuzioni. */
export const isTimed = (on: string) => {
  const k = parseEvent(on).kind;
  return k === "at" || k === "every";
};

export const WEEKDAYS = [
  "configurazione.regole.giorno.1",
  "configurazione.regole.giorno.2",
  "configurazione.regole.giorno.3",
  "configurazione.regole.giorno.4",
  "configurazione.regole.giorno.5",
  "configurazione.regole.giorno.6",
  "configurazione.regole.giorno.7",
] as const;

/** Nome di un campo del tipo («titolo» è il titolo dell'elemento). */
export function fieldName(t: T, key: string | undefined, names: Names = {}): string {
  if (!key) return t("configurazione.regole.chi.nessuno");
  if (key === "titolo") return t("configurazione.regole.campoTitolo");
  return names.fields?.find((f) => f.key === key)?.name ?? key;
}

/** Nome di una data dell'elemento («scadenza», «campo.fine»…). */
function dateName(t: T, key: string, names: Names): string {
  const d = names.dates?.find((x) => x.key === key)?.name;
  if (d) return d;
  if (key.startsWith("campo.")) return fieldName(t, key.slice(6), names);
  return key;
}

const nameOf = (list: NamedKey[] | undefined, key: string | undefined, none: string) => (key ? (list?.find((x) => x.key === key)?.name ?? key) : none);

/** L'evento in parole: «si crea», «entra nella fase «X»», «mancano 30 giorni a «Scadenza»», «ogni lunedì». */
export function eventPhrase(t: T, on: string, names: Names = {}): string {
  const ev = parseEvent(on);
  switch (ev.kind) {
    case "create":
      return t("configurazione.regole.evento.create");
    case "enter":
      return t("configurazione.regole.evento.enter", { phase: ev.phase });
    case "exit":
      return t("configurazione.regole.evento.exit", { phase: ev.phase });
    case "change":
      return t("configurazione.regole.evento.change", { field: fieldName(t, ev.field, names) });
    case "at": {
      const date = dateName(t, ev.date, names);
      if (ev.offset < 0) return t("configurazione.regole.evento.atPrima", { count: -ev.offset, date });
      if (ev.offset > 0) return t("configurazione.regole.evento.atDopo", { count: ev.offset, date });
      return t("configurazione.regole.evento.atGiorno", { date });
    }
    case "every":
      return ev.weekday ? t("configurazione.regole.evento.everyWeek", { day: t(WEEKDAYS[ev.weekday - 1]) }) : t("configurazione.regole.evento.everyDay");
  }
  return ev.raw.trim() || t("configurazione.regole.chi.nessuno");
}

/**
 * Fatti in più per la condizione di un evento «change:<campo>»: il valore precedente
 * (prima.<campo>) e, se manca, il campo stesso (il valore nuovo).
 */
export function changeFacts(t: T, on: string, facts: FactInfo[], names: Names = {}): FactInfo[] {
  const ev = parseEvent(on);
  if (ev.kind !== "change") return [];
  const base = facts.find((f) => f.key === ev.field);
  const name = base?.name ?? fieldName(t, ev.field, names);
  const out: FactInfo[] = [];
  if (!base) out.push({ key: ev.field, name, kind: "text", source: ev.field === "titolo" ? "base" : "campo" });
  out.push({ key: `prima.${ev.field}`, name: t("configurazione.regole.evento.prima", { name }), kind: base?.kind ?? "text", values: base?.values, source: base?.source ?? "campo" });
  return out;
}

// ---------------------------------------------------------------------------
// Azioni

/** Un testo da modello, accorciato per la frase. */
function excerpt(s: string | undefined, n = 60): string {
  const v = (s ?? "").split(/\s+/).filter(Boolean).join(" ");
  if (!v) return "…";
  return v.length <= n ? v : `${v.slice(0, n - 1)}…`;
}

/** I destinatari di una notifica in parole: «il gruppo «X» e chi segue l'elemento». */
function recipients(t: T, a: RuleAction, names: Names): string {
  const parts: string[] = [];
  if (a.member) parts.push(t("configurazione.regole.chi.membro"));
  if (a.group) parts.push(t("configurazione.regole.chi.gruppo", { name: a.group }));
  if (a.role) parts.push(t("configurazione.regole.chi.ruolo", { role: nameOf(names.roles, a.role, "") }));
  if (a.params?.followers === "true") parts.push(t("configurazione.regole.chi.seguaci"));
  if (!parts.length) return t("configurazione.regole.chi.nessuno");
  if (parts.length === 1) return parts[0];
  return t("configurazione.regole.chi.elenco", { first: parts.slice(0, -1).join(", "), last: parts[parts.length - 1] });
}

/** Un'azione in parole: «apri la valutazione «Sicurezza»», «imposta «Priorità» a «alta»». */
export function actionPhrase(t: T, a: RuleAction, refs: RuleRefs, names: Names = {}): string {
  const when = a.when ? describeCondition(a.when) : "";
  const none = t("configurazione.regole.chi.nessuno");
  const p = a.params ?? {};
  switch (a.kind) {
    case "open_review": {
      const name = refs.reviewTypes.find((x) => x.key === a.ref)?.name ?? (a.ref || none);
      return when ? t("configurazione.regole.azione.open_reviewImpatti", { name, condition: when }) : t("configurazione.regole.azione.open_review", { name });
    }
    case "apply_baseline": {
      const name = refs.catalogs.find((x) => x.key === a.ref)?.name ?? (a.ref || none);
      return when ? t("configurazione.regole.azione.apply_baselineImpatti", { name, condition: when }) : t("configurazione.regole.azione.apply_baseline", { name });
    }
    case "assign_role": {
      const role = names.roles?.find((r) => r.key === a.role)?.name ?? (a.role || none);
      const who = a.group ? t("configurazione.regole.chi.gruppo", { name: a.group }) : a.member ? t("configurazione.regole.chi.membro") : none;
      return t("configurazione.regole.azione.assign_role", { role, who });
    }
    case "set_field": {
      const field = fieldName(t, p.field, names);
      if (p.mode === "clear") return t("configurazione.regole.azione.set_fieldSvuota", { field });
      if (p.mode === "today") {
        const days = Number.parseInt(p.days ?? "", 10) || 0;
        if (days > 0) return t("configurazione.regole.azione.set_fieldOggiPiu", { field, count: days });
        if (days < 0) return t("configurazione.regole.azione.set_fieldOggiMeno", { field, count: -days });
        return t("configurazione.regole.azione.set_fieldOggi", { field });
      }
      return t("configurazione.regole.azione.set_field", { field, value: excerpt(p.value) });
    }
    case "notify":
      return t("configurazione.regole.azione.notify", { who: recipients(t, a, names), message: excerpt(p.message) });
    case "add_comment":
      return t("configurazione.regole.azione.add_comment", { text: excerpt(p.text) });
    case "create_linked": {
      const args = {
        type: nameOf(names.types, p.type, none),
        title: excerpt(p.title),
        relation: nameOf(names.relations, p.relation, none),
        where: p.space ? " " + t("configurazione.regole.azione.nelloSpazio", { space: nameOf(names.spaces, p.space, none) }) : "",
      };
      return t(p.direction === "in" ? "configurazione.regole.azione.create_linkedIn" : "configurazione.regole.azione.create_linked", args);
    }
    case "webhook":
      return t("configurazione.regole.azione.webhook", { name: p.webhook || none });
  }
  return t("configurazione.regole.azione.altra", { name: (names.schemas?.find((s) => s.kind === a.kind)?.name ?? a.kind).toLowerCase() });
}

/** La regola intera in una frase. Le regole periodiche iniziano con «Ogni giorno…», le altre con «Quando…». */
export function ruleSentence(t: T, r: Pick<Rule, "on" | "when" | "actions">, refs: RuleRefs, names: Names = {}): string {
  const ev = parseEvent(r.on);
  const event = eventPhrase(t, r.on, names);
  const actions = r.actions.length ? r.actions.map((a) => actionPhrase(t, a, refs, names)).join(", ") : t("configurazione.regole.nessunaAzione");
  let facts = names.facts;
  if (ev.kind === "change") facts = { ...facts, [`prima.${ev.field}`]: t("configurazione.regole.evento.prima", { name: facts?.[ev.field] ?? fieldName(t, ev.field, names) }) };
  const when = r.when?.trim() ? describeCondition(r.when, facts) : "";
  if (ev.kind === "every") {
    const Event = event.charAt(0).toUpperCase() + event.slice(1);
    return when ? t("configurazione.regole.frasePeriodicaSe", { event: Event, condition: when, actions }) : t("configurazione.regole.frasePeriodica", { event: Event, actions });
  }
  return when ? t("configurazione.regole.fraseSe", { event, condition: when, actions }) : t("configurazione.regole.frase", { event, actions });
}
