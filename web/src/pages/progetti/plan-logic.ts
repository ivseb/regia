// Logica pura del Gantt del progetto: cascata, percorso critico, righe per fase.
// Le regole di slittamento rispecchiano internal/modules/planning/graph.go (earliest, cascade):
// giorni lavorativi, solo in avanti, voci annullate ferme.
import * as cal from "../../calendar";
import type { DepKind, Phase, PlanItem } from "../../projectsApi";

export type Span = { s: number; e: number };

/** Il giorno lavorativo che cade n giorni lavorativi dopo t (t escluso); con n <= 0, t o il primo lavorativo dopo. */
export function addWorkdays(t: number, n: number): number {
  let x = cal.nextWorkday(t);
  for (; n > 0; n--) x = cal.afterEnd(x);
  return x;
}

/** Primo giorno di una voce di n giorni lavorativi che finisce il giorno end. */
function startFor(end: number, n: number): number {
  let t = end;
  while (n > 1) {
    t = cal.addDays(t, -1);
    if (cal.isWorkday(t)) n--;
  }
  return t;
}

/** Dove deve stare il successore d (durata in giorni lavorativi) per rispettare la dipendenza dal predecessore p; null se già a posto. */
export function earliest(p: Span, d: Span, milestone: boolean, kind: DepKind, lag: number): Span | null {
  const dur = milestone ? 1 : Math.max(1, cal.workdays(d.s, d.e));
  if (kind === "ss") {
    const s = addWorkdays(p.s, lag);
    return d.s >= s ? null : { s, e: cal.endAfter(s, dur) };
  }
  if (kind === "ff") {
    const e = addWorkdays(p.e, lag);
    if (d.e >= e) return null;
    return milestone ? { s: e, e } : { s: startFor(e, dur), e };
  }
  const s = addWorkdays(p.e, milestone ? lag : 1 + lag);
  if (d.s >= s) return null;
  return milestone ? { s, e: s } : { s, e: cal.endAfter(s, dur) };
}

/** Slittamento a cascata (solo in avanti) di chi dipende da id, come planning.graph.cascade. */
export function cascadeFrom(items: PlanItem[], spans: Map<string, Span>, id: string): Map<string, Span> {
  const out = new Map(spans);
  const byId = new Map(items.map((i) => [i.id, i]));
  const dependents = new Map<string, { to: string; kind: DepKind; lag: number }[]>();
  for (const i of items) for (const d of i.deps) dependents.set(d.id, [...(dependents.get(d.id) ?? []), { to: i.id, kind: d.kind, lag: d.lag }]);
  const queue = [id];
  for (let steps = 0; queue.length && steps < 10_000; steps++) {
    const cur = queue.shift()!;
    const cs = out.get(cur);
    if (!cs) continue;
    for (const e of dependents.get(cur) ?? []) {
      const ds = out.get(e.to);
      const it = byId.get(e.to);
      if (!ds || !it || it.state_category === "cancelled") continue;
      const next = earliest(cs, ds, it.kind === "milestone", e.kind, e.lag);
      if (!next) continue;
      out.set(e.to, next);
      queue.push(e.to);
    }
  }
  return out;
}

/** Percorso critico: catene a margine zero (nei tre tipi di dipendenza) che finiscono con la fine del piano. */
export function criticalPath(items: PlanItem[], spans: Map<string, Span>): Set<string> {
  const crit = new Set<string>();
  const live = items.filter((i) => i.state_category !== "cancelled" && spans.has(i.id));
  if (live.length === 0) return crit;
  const end = Math.max(...live.map((i) => spans.get(i.id)!.e));
  for (const i of live) if (spans.get(i.id)!.e === end) crit.add(i.id);
  let changed = true;
  while (changed) {
    changed = false;
    for (const i of live) {
      if (!crit.has(i.id)) continue;
      const d = spans.get(i.id)!;
      for (const dep of i.deps) {
        const p = spans.get(dep.id);
        if (!p || crit.has(dep.id)) continue;
        // Il successore sta al minimo (o oltre il vincolo, se violato): la catena è a margine zero.
        if (!hasSlack(p, d, i.kind === "milestone", dep.kind, dep.lag)) {
          crit.add(dep.id);
          changed = true;
        }
      }
    }
  }
  return crit;
}

/** C'è margine tra predecessore e successore (il successore potrebbe partire prima senza violare la dipendenza). */
function hasSlack(p: Span, d: Span, milestone: boolean, kind: DepKind, lag: number): boolean {
  if (kind === "ss") return d.s > addWorkdays(p.s, lag);
  if (kind === "ff") return d.e > addWorkdays(p.e, lag);
  return d.s > addWorkdays(p.e, milestone ? lag : 1 + lag);
}

/** La dipendenza non è rispettata dalle date mostrate. */
export function violated(p: Span, d: Span, milestone: boolean, kind: DepKind, lag: number): boolean {
  return earliest(p, d, milestone, kind, lag) !== null;
}

export type Row =
  | { k: "phase"; phase: Phase | null; id: string }
  | { k: "item"; it: PlanItem; depth: 0 | 1 }
  | { k: "add"; phaseId: string | null; id: string }
  | { k: "addact"; wp: PlanItem };

export const H = { phase: 32, wp: 48, act: 32, ms: 36, add: 36, addact: 30 };

export function rowHeight(r: Row): number {
  if (r.k === "phase") return H.phase;
  if (r.k === "add") return H.add;
  if (r.k === "addact") return H.addact;
  return r.depth === 1 ? H.act : r.it.kind === "milestone" ? H.ms : H.wp;
}

/** Voci di una fase (o «senza fase»): work package e milestone in ordine di inizio, con le loro attività sotto. */
export function itemsByPhase(phases: Phase[], items: PlanItem[], spans: Map<string, Span>, ghost: Map<string, Span>, withAdd: boolean): Row[] {
  const known = new Set(phases.map((p) => p.id));
  const byId = new Map(items.map((i) => [i.id, i]));
  const startOf = (i: PlanItem) => (spans.get(i.id) ?? ghost.get(i.id))?.s ?? Infinity;
  const kids = new Map<string, PlanItem[]>();
  const tops: PlanItem[] = [];
  for (const i of items) {
    const parent = i.parent_id ? byId.get(i.parent_id) : undefined;
    if (i.kind === "plan_activity" && parent) kids.set(parent.id, [...(kids.get(parent.id) ?? []), i]);
    else tops.push(i);
  }
  const rows: Row[] = [];
  const group = (phase: Phase | null) => {
    const id = phase?.id ?? null;
    const mine = tops.filter((i) => (i.phase_id && known.has(i.phase_id) ? i.phase_id : null) === id).sort((a, b) => startOf(a) - startOf(b));
    rows.push({ k: "phase", phase, id: id ?? "none" });
    for (const w of mine) {
      rows.push({ k: "item", it: w, depth: 0 });
      const ks = (kids.get(w.id) ?? []).sort((a, b) => startOf(a) - startOf(b));
      for (const a of ks) rows.push({ k: "item", it: a, depth: 1 });
      if (withAdd && w.kind === "work_package" && w.delivery_mode === "waterfall") rows.push({ k: "addact", wp: w });
    }
    if (withAdd) rows.push({ k: "add", phaseId: id, id: id ?? "none" });
    return mine.length;
  };
  for (const p of [...phases].sort((a, b) => a.ordinal - b.ordinal)) group(p);
  const loose = tops.filter((i) => !(i.phase_id && known.has(i.phase_id)));
  if (loose.length > 0 || phases.length === 0) group(null);
  return rows;
}

/** Inviluppo delle date delle voci di un gruppo (con l'anteprima del trascinamento). */
export function envelope(items: PlanItem[], spans: Map<string, Span>): Span | null {
  let s = Infinity;
  let e = -Infinity;
  for (const i of items) {
    const x = spans.get(i.id);
    if (!x || i.state_category === "cancelled") continue;
    s = Math.min(s, x.s);
    e = Math.max(e, x.e);
  }
  return s === Infinity ? null : { s, e };
}

/** Durata proposta per una voce senza date, in giorni lavorativi. */
export function proposedDuration(i: PlanItem): number {
  if (i.kind === "milestone") return 1;
  return i.estimate_effort_days && i.estimate_effort_days > 0 ? Math.max(1, Math.ceil(i.estimate_effort_days / ((i.team_capacity > 0 ? i.team_capacity : 5) / 5))) : 5;
}
