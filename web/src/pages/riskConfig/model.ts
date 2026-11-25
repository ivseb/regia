// Accettazione del rischio e scadenze degli incidenti: funzioni pure, provate con `pnpm test`.
// Il formato salvato verso l'API non cambia (passi con approvatori, termini «da + ore/giorni»);
// il testo grezzo («a + b → c», «detected + 24h») resta solo come vista «Avanzato».
import type { Approver, Due, PolicyRule, Stage } from "../../riskApi.ts";

export type Step = { approvers: Approver[] };

/** «risk_owner + organo:cda → membro:…» (vista avanzata). */
export function stepsText(steps: Step[]): string {
  return steps.map((s) => s.approvers.map((a) => a.role ?? (a.body ? `organo:${a.body}` : `membro:${a.member}`)).join(" + ")).join(" → ");
}

export function parseSteps(s: string): PolicyRule["steps"] {
  return s
    .split("→")
    .map((x) => x.trim())
    .filter(Boolean)
    .map((step) => ({
      approvers: step
        .split("+")
        .map((a) => a.trim())
        .filter(Boolean)
        .map((a) => (a.startsWith("organo:") ? { body: a.slice(7) } : a.startsWith("membro:") ? { member: a.slice(7) } : { role: a })),
    }));
}

export type ApproverKind = "role" | "body" | "member";
export const approverKind = (a: Approver): ApproverKind => (a.body !== undefined ? "body" : a.member !== undefined ? "member" : "role");

/** L'approvatore dello stesso tipo con il valore dato (gli altri campi si azzerano). */
export function approverOf(kind: ApproverKind, value = ""): Approver {
  return kind === "body" ? { body: value } : kind === "member" ? { member: value } : { role: value };
}
export const approverValue = (a: Approver): string => a.role ?? a.body ?? a.member ?? "";

/** Passi non vuoti: un passo senza approvatori o con un approvatore senza valore si scarta. */
export function cleanSteps(steps: Step[]): Step[] {
  return steps
    .map((s) => ({ approvers: s.approvers.filter((a) => approverValue(a).trim() !== "") }))
    .filter((s) => s.approvers.length > 0);
}

export function dueText(d: Due[]): string {
  return d.map((x) => `${x.from} + ${x.days ? `${x.days}g` : `${x.hours}h`}`).join(", ");
}

export function parseDue(s: string): Due[] {
  return s
    .split(",")
    .map((x) => x.trim())
    .filter(Boolean)
    .map((x) => {
      const m = x.match(/^([a-z:_0-9]+)\s*\+\s*(\d+)\s*([hg])$/i);
      if (!m) return { from: x, hours: 0 };
      return m[3].toLowerCase() === "g" ? { from: m[1], days: Number(m[2]) } : { from: m[1], hours: Number(m[2]) };
    });
}

export type DueUnit = "hours" | "days";
export const dueUnit = (d: Due): DueUnit => (d.days ? "days" : "hours");
export const dueAmount = (d: Due): number => (d.days ? d.days : (d.hours ?? 0));
/** Il termine con quantità e unità date (l'altra unità si toglie). */
export function dueOf(from: string, amount: number, unit: DueUnit): Due {
  const n = Math.max(0, Math.round(amount) || 0);
  return unit === "days" ? { from, days: n } : { from, hours: n };
}
export const dueHours = (d: Due): number => (d.days ? d.days * 24 : (d.hours ?? 0));

export type Tick = { key: string; name: string; /** Ore dal rilevamento (il termine più vicino), null se non si calcola. */ hours: number | null };

/**
 * Quando scade ogni tappa contando dal rilevamento: vince il termine più vicino (come nel server).
 * La classificazione si suppone contestuale al rilevamento (nei fatti arriva dopo: le scadenze
 * che ne dipendono slittano di altrettanto); «dalla tappa X» parte dalla scadenza di X.
 */
export function timeline(stages: Stage[]): Tick[] {
  const at = new Map<string, number | null>();
  return stages.map((st) => {
    let best: number | null = null;
    for (const d of st.due) {
      let base: number | null = null;
      if (d.from === "detected" || d.from === "classified") base = 0;
      else if (d.from.startsWith("stage:")) base = at.get(d.from.slice(6)) ?? null;
      if (base === null) continue;
      const h = base + dueHours(d);
      if (best === null || h < best) best = h;
    }
    at.set(st.key, best);
    return { key: st.key, name: st.name, hours: best };
  });
}

/** Le tappe in ordine di scadenza, senza quelle non calcolabili. */
export const ordered = (ticks: Tick[]): Tick[] => ticks.filter((t) => t.hours !== null).sort((a, b) => a.hours! - b.hours!);
