// Barra dei comandi in blocco (piano, passo 18): «assegna @anna fase In corso sprint 12».
// Il testo si legge nel browser, con i valori ammessi del tenant (persone, fasi, sprint, campi,
// etichette); il risultato sono i comandi che il server esegue. Le parole chiave sono in
// italiano e in inglese; i valori a più parole («In corso», «Anna Rossi») si riconoscono per
// la corrispondenza più lunga con i valori noti.
import type { BulkCommand, BulkContext } from "./tempoApi";
import { tr } from "./i18n/core.ts";

export type Suggestion = { label: string; hint?: string; insert: string; from: number };
export type Parsed = { commands: BulkCommand[]; errors: string[] };

type Tok = { text: string; start: number; end: number };

const KEYWORDS: Record<string, BulkCommand["kind"]> = {
  assegna: "assign",
  assegnato: "assign",
  assegnatario: "assign",
  assign: "assign",
  fase: "phase",
  stato: "phase",
  state: "phase",
  phase: "phase",
  sprint: "sprint",
  etichetta: "labels",
  etichette: "labels",
  tag: "labels",
  label: "labels",
  labels: "labels",
  elimina: "delete",
  cancella: "delete",
  delete: "delete",
};

export const norm = (s: string) => s.toLowerCase().normalize("NFD").replace(/[̀-ͯ]/g, "").trim();

export function tokenize(text: string): Tok[] {
  const out: Tok[] = [];
  const re = /"([^"]*)"|\S+/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(text))) out.push({ text: m[1] ?? m[0], start: m.index, end: m.index + m[0].length });
  return out;
}

type Kw = { kind: BulkCommand["kind"]; field?: string; len: number };

/** Se a partire dal token i c'è una parola chiave (anche il nome di un campo di più parole). */
function keywordAt(toks: Tok[], i: number, ctx: BulkContext): Kw | null {
  const t = norm(toks[i].text);
  if (KEYWORDS[t]) return { kind: KEYWORDS[t], len: 1 };
  if (t.startsWith("#")) return { kind: "labels", len: 0 };
  for (const f of ctx.fields) {
    for (const name of [f.name, f.key]) {
      const words = norm(name).split(/\s+/);
      if (words.every((w, k) => toks[i + k] && norm(toks[i + k].text) === w)) return { kind: "field", field: f.key, len: words.length };
    }
  }
  return null;
}

/** Token del valore: dal token i fino alla prossima parola chiave. */
function valueTokens(toks: Tok[], i: number, ctx: BulkContext): Tok[] {
  const out: Tok[] = [];
  for (let j = i; j < toks.length; j++) {
    if (keywordAt(toks, j, ctx)) break;
    out.push(toks[j]);
  }
  return out;
}

/** La corrispondenza più lunga fra i token (uniti da spazi) e un elenco di candidati. */
function longest<T>(vals: Tok[], items: T[], names: (x: T) => string[]): { item: T | null; used: number; ambiguous: T[] } {
  for (let n = vals.length; n >= 1; n--) {
    const joined = norm(vals.slice(0, n).map((v) => v.text).join(" ").replace(/^@/, ""));
    const exact = items.filter((x) => names(x).some((nm) => norm(nm) === joined));
    if (exact.length === 1) return { item: exact[0], used: n, ambiguous: [] };
    const pref = items.filter((x) => names(x).some((nm) => norm(nm).startsWith(joined) || norm(nm).split(/\s+/).some((w) => w.startsWith(joined))));
    if (exact.length === 0 && pref.length === 1) return { item: pref[0], used: n, ambiguous: [] };
    if (exact.length > 1 || pref.length > 1) return { item: null, used: n, ambiguous: exact.length > 1 ? exact : pref };
  }
  return { item: null, used: 0, ambiguous: [] };
}

function parseDate(s: string): string | null {
  const t = norm(s);
  const today = new Date();
  const iso = (d: Date) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
  if (t === "oggi") return iso(today);
  if (t === "domani") return iso(new Date(today.getFullYear(), today.getMonth(), today.getDate() + 1));
  if (/^\d{4}-\d{2}-\d{2}$/.test(t)) return t;
  const m = /^(\d{1,2})[/.](\d{1,2})(?:[/.](\d{2,4}))?$/.exec(t);
  if (m) {
    const y = m[3] ? (m[3].length === 2 ? 2000 + Number(m[3]) : Number(m[3])) : today.getFullYear();
    return iso(new Date(y, Number(m[2]) - 1, Number(m[1])));
  }
  return null;
}

/** Legge il testo e produce i comandi; gli errori dicono cosa non si capisce. */
export function parseCommand(text: string, ctx: BulkContext, meId?: string): Parsed {
  const toks = tokenize(text);
  const commands: BulkCommand[] = [];
  const errors: string[] = [];
  let i = 0;
  const people = ctx.members;
  const resolvePerson = (vals: Tok[]): { id: string; name: string } | null => {
    if (vals.length === 0) {
      errors.push(tr("lavoro.bulkparse.whoPerson"));
      return null;
    }
    const first = norm(vals[0].text.replace(/^@/, ""));
    if (vals.length === 1 && (first === "me" || first === "io") && meId) {
      const me = people.find((p) => p.id === meId);
      return { id: meId, name: me?.name ?? "me" };
    }
    const r = longest(vals, people, (p) => [p.name]);
    if (r.item) return { id: r.item.id, name: r.item.name };
    errors.push(r.ambiguous.length ? tr("lavoro.bulkparse.personAmbiguous", { text: vals.map((v) => v.text).join(" "), options: r.ambiguous.slice(0, 4).map((p) => p.name).join(", ") }) : tr("lavoro.bulkparse.noPerson", { text: vals.map((v) => v.text).join(" ") }));
    return null;
  };
  while (i < toks.length) {
    const t = toks[i];
    // «@anna» da solo vale «assegna @anna».
    if (t.text.startsWith("@")) {
      const vals = valueTokens(toks, i, ctx);
      const p = resolvePerson(vals);
      if (p) commands.push({ kind: "assign", value: p.id, label: p.name });
      i += Math.max(vals.length, 1);
      continue;
    }
    // «#urgente» da solo vale «etichetta +urgente».
    if (t.text.startsWith("#")) {
      const add: string[] = [];
      while (i < toks.length && toks[i].text.startsWith("#")) add.push(toks[i++].text.slice(1));
      if (add.some(Boolean)) commands.push({ kind: "labels", add: add.filter(Boolean) });
      continue;
    }
    const kw = keywordAt(toks, i, ctx);
    if (!kw) {
      // Una fase scritta da sola («In corso») vale «fase In corso».
      const vals = valueTokens(toks, i, ctx);
      const r = longest(vals, ctx.phases, (p) => [p]);
      if (r.item) {
        commands.push({ kind: "phase", value: r.item, label: r.item });
        i += r.used;
        continue;
      }
      errors.push(tr("lavoro.bulkparse.unknownWord", { text: t.text }));
      i++;
      continue;
    }
    const start = i + kw.len;
    const vals = kw.kind === "delete" ? [] : valueTokens(toks, start, ctx);
    switch (kw.kind) {
      case "delete":
        commands.push({ kind: "delete" });
        i = start;
        continue;
      case "assign": {
        if (vals.length === 1 && ["nessuno", "-", "nessun", "vuoto", "none"].includes(norm(vals[0].text))) {
          commands.push({ kind: "assign", value: "", label: tr("lavoro.bulkparse.nobody") });
        } else {
          const p = resolvePerson(vals);
          if (p) commands.push({ kind: "assign", value: p.id, label: p.name });
        }
        break;
      }
      case "phase": {
        if (vals.length === 0) errors.push(tr("lavoro.bulkparse.whoPhase"));
        else {
          const r = longest(vals, ctx.phases, (p) => [p]);
          if (r.item) {
            commands.push({ kind: "phase", value: r.item, label: r.item });
            i = start + r.used;
            continue;
          }
          errors.push(r.ambiguous.length ? tr("lavoro.bulkparse.phaseAmbiguous", { options: r.ambiguous.slice(0, 4).join(", ") }) : tr("lavoro.bulkparse.noPhase", { text: vals.map((v) => v.text).join(" ") }));
        }
        break;
      }
      case "sprint": {
        const v = norm(vals.map((x) => x.text).join(" "));
        if (!v) errors.push(tr("lavoro.bulkparse.whoSprint"));
        else if (v === "backlog" || v === "nessuno" || v === "-") commands.push({ kind: "sprint", value: "", label: "backlog" });
        else {
          let hit = ctx.sprints.filter((s) => norm(s.key) === v || norm(s.title) === v);
          if (hit.length === 0 && /^\d+$/.test(v)) hit = ctx.sprints.filter((s) => norm(s.key).endsWith("-" + v) || norm(s.title).split(/\s+/).includes(v));
          if (hit.length === 0) hit = ctx.sprints.filter((s) => norm(s.title).includes(v));
          if (hit.length === 1) commands.push({ kind: "sprint", value: hit[0].key, label: hit[0].title });
          else errors.push(hit.length ? tr("lavoro.bulkparse.sprintAmbiguous", { options: hit.slice(0, 4).map((s) => s.title).join(", ") }) : tr("lavoro.bulkparse.noSprint", { text: vals.map((x) => x.text).join(" ") }));
        }
        break;
      }
      case "labels": {
        const add: string[] = [];
        const remove: string[] = [];
        for (const x of vals.length ? vals : []) {
          const s = x.text;
          if (s.startsWith("-") && s.length > 1) remove.push(s.slice(1));
          else add.push(s.replace(/^[+#]/, ""));
        }
        const clean = (a: string[]) => a.filter(Boolean);
        if (clean(add).length + clean(remove).length === 0) errors.push(tr("lavoro.bulkparse.whoLabels"));
        else commands.push({ kind: "labels", add: clean(add), remove: clean(remove) });
        break;
      }
      case "field": {
        const f = ctx.fields.find((x) => x.key === kw.field)!;
        if (vals.length === 0) {
          errors.push(tr("lavoro.bulkparse.whoValue", { name: f.name }));
          break;
        }
        const raw = vals.map((x) => x.text).join(" ");
        if (["vuoto", "nessuno", "-", "svuota"].includes(norm(raw))) {
          commands.push({ kind: "field", field: f.key, raw: null, label: f.name });
          break;
        }
        const one = (r: unknown, used = vals.length) => {
          commands.push({ kind: "field", field: f.key, raw: r, label: f.name });
          i = start + used;
        };
        if (f.data_type === "enum" || f.data_type === "version") {
          const r = longest(vals, f.options ?? [], (o) => [o.name]);
          if (r.item) {
            one(r.item.name, r.used);
            continue;
          }
          errors.push(r.ambiguous.length ? tr("lavoro.bulkparse.valueAmbiguous", { name: f.name }) : tr("lavoro.bulkparse.notInOptions", { name: f.name, text: raw, options: (f.options ?? []).slice(0, 6).map((o) => o.name).join(", ") }));
        } else if (f.data_type === "member") {
          const p = resolvePerson(vals);
          if (p) {
            one(p.id);
            continue;
          }
        } else if (f.data_type === "integer" || f.data_type === "decimal") {
          const n = Number(raw.replace(",", "."));
          if (Number.isNaN(n)) errors.push(tr("lavoro.bulkparse.needNumber", { name: f.name }));
          else {
            one(n);
            continue;
          }
        } else if (f.data_type === "date") {
          const d = parseDate(raw);
          if (!d) errors.push(tr("lavoro.bulkparse.badDate", { name: f.name }));
          else {
            one(d);
            continue;
          }
        } else if (f.data_type === "boolean") {
          const yes = ["si", "sì", "vero", "true", "yes", "1"].includes(norm(raw)) || norm(raw) === "sì";
          const no = ["no", "falso", "false", "0"].includes(norm(raw));
          if (!yes && !no) errors.push(tr("lavoro.bulkparse.yesNo", { name: f.name }));
          else {
            one(yes);
            continue;
          }
        } else {
          one(raw);
          continue;
        }
        break;
      }
    }
    i = start + vals.length;
  }
  return { commands, errors };
}

const KEYWORD_HINTS: [string, () => string, string][] = [
  ["assegna", () => tr("lavoro.bulkparse.hintAssign"), "assign"],
  ["fase", () => tr("lavoro.bulkparse.hintPhase"), "phase"],
  ["sprint", () => tr("lavoro.bulkparse.hintSprint"), "sprint"],
  ["etichetta", () => tr("lavoro.bulkparse.hintLabels"), "labels"],
  ["elimina", () => tr("lavoro.bulkparse.hintDelete"), "delete"],
];

/** Proposte di completamento per il testo scritto fin qui. */
export function suggest(text: string, ctx: BulkContext): Suggestion[] {
  const endsSpace = /\s$/.test(text) || text === "";
  const toks = tokenize(text);
  const partial = endsSpace ? null : toks[toks.length - 1];
  const done = partial ? toks.slice(0, -1) : toks;
  // Parola chiave attiva: l'ultima prima del testo in corso.
  let active: Kw | null = null;
  let activeAt = -1;
  for (let i = done.length - 1; i >= 0; i--) {
    const k = keywordAt(done, i, ctx);
    if (k && (k.kind !== "labels" || k.len > 0)) {
      active = k;
      activeAt = i + k.len;
      break;
    }
  }
  const valToks = active ? done.slice(activeAt) : [];
  const typed = [...valToks.map((v) => v.text), ...(partial ? [partial.text] : [])].join(" ");
  const from = valToks[0]?.start ?? partial?.start ?? text.length;
  const q = norm(typed.replace(/^@/, ""));
  const out: Suggestion[] = [];
  const match = (s: string) => !q || norm(s).startsWith(q) || norm(s).split(/\s+/).some((w) => w.startsWith(q));
  const fullMatch = (s: string) => norm(s) === q;
  const quote = (s: string) => s;
  const wantsValue = active && (active.kind !== "delete") && (valToks.length === 0 || !!partial);
  if (partial?.text.startsWith("@") || (active?.kind === "assign" && wantsValue)) {
    for (const p of ctx.members.filter((m) => match(m.name) && !fullMatch(m.name)).slice(0, 8)) out.push({ label: p.name, hint: tr("lavoro.bulkparse.person"), insert: `@${quote(p.name)}`, from });
    if (active?.kind === "assign" && !q) out.unshift({ label: tr("lavoro.bulkparse.nobody"), hint: tr("lavoro.bulkparse.unassigned"), insert: "nessuno", from });
  } else if (active?.kind === "phase" && wantsValue) {
    for (const p of ctx.phases.filter((x) => match(x) && !fullMatch(x))) out.push({ label: p, hint: tr("lavoro.bulkparse.phase"), insert: p, from });
  } else if (active?.kind === "sprint" && wantsValue) {
    if (match("backlog")) out.push({ label: "backlog", hint: tr("lavoro.bulkparse.removeFromSprint"), insert: "backlog", from });
    for (const s of ctx.sprints.filter((x) => match(x.title) || match(x.key))) out.push({ label: s.title, hint: s.key, insert: s.key, from });
  } else if (active?.kind === "labels" && (partial || valToks.length === 0)) {
    const last = partial?.text ?? "";
    const sign = last.startsWith("-") ? "-" : last.startsWith("+") ? "+" : "+";
    const word = norm(last.replace(/^[+\-#]/, ""));
    for (const l of ctx.labels.filter((x) => !word || norm(x).startsWith(word)).slice(0, 8)) out.push({ label: l, hint: sign === "-" ? tr("lavoro.bulkparse.remove") : tr("lavoro.bulkparse.add"), insert: sign + l, from: partial ? partial.start : text.length });
  } else if (active?.kind === "field" && wantsValue) {
    const f = ctx.fields.find((x) => x.key === active!.field);
    if (f?.options) for (const o of f.options.filter((x) => match(x.name) && !fullMatch(x.name))) out.push({ label: o.name, hint: f.name, insert: o.name, from });
    else if (f?.data_type === "member") for (const p of ctx.members.filter((m) => match(m.name) && !fullMatch(m.name)).slice(0, 8)) out.push({ label: p.name, hint: f.name, insert: `@${p.name}`, from });
    else if (f?.data_type === "boolean") for (const v of ["sì", "no"]) out.push({ label: v, hint: f.name, insert: v, from });
    else if (f?.data_type === "date") for (const v of ["oggi", "domani"]) out.push({ label: v, hint: f.name, insert: v, from });
  }
  // Parole chiave: all'inizio, dopo un comando completo, o mentre se ne scrive una.
  const kwFrom = partial ? partial.start : text.length;
  const typedKw = partial ? norm(partial.text) : "";
  const showKw = !active || !wantsValue || valToks.length > 0 || partial;
  if (showKw && !(partial && partial.text.startsWith("@"))) {
    const kws: [string, string][] = [...KEYWORD_HINTS.map(([k, h]) => [k, h()] as [string, string]), ...ctx.fields.map((f) => [f.name, tr("lavoro.bulkparse.field")] as [string, string])];
    for (const [k, h] of kws) {
      if ((!typedKw || norm(k).startsWith(typedKw)) && norm(k) !== typedKw && (partial || out.length === 0 || !active)) out.push({ label: k, hint: h, insert: k, from: kwFrom });
    }
  }
  return out.slice(0, 10);
}

/** Applica una proposta al testo. */
export function applySuggestion(text: string, s: Suggestion): string {
  return text.slice(0, s.from) + s.insert + " ";
}
