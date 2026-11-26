// Tinte stabili per persone, fasi, tipi e unità (replica/mockup/interfaccia-colori.html).
// Restituiscono un modulo: il colore vero lo dà la classe m-<modulo> (styles.css) o var(--m-<modulo>).
import type { UiModule } from "./moduleIcon";

const PEOPLE: UiModule[] = ["demand", "grc", "knowledge", "arch", "work", "devsecops", "budget"];
const UNITS: UiModule[] = ["work", "platform", "knowledge", "grc", "arch", "demand", "devsecops", "budget"];

function hash(s: string): number {
  let h = 0;
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) | 0;
  return Math.abs(h);
}

/** Colore dell'avatar: sempre lo stesso per lo stesso nome. */
export function personTint(name: string): UiModule {
  return PEOPLE[hash(name.trim().toLowerCase()) % PEOPLE.length];
}

/** Iniziali per l'avatar (al massimo due). */
export function initials(name: string): string {
  return name.split(/\s+/).filter(Boolean).slice(0, 2).map((p) => p[0]).join("").toUpperCase() || "?";
}

/** Colore del quadratino dell'unità organizzativa. */
export function unitTint(unit: string): UiModule {
  return UNITS[hash(unit.trim().toLowerCase()) % UNITS.length];
}

/** Colore del tipo di elemento sulla board: story demand, task knowledge, bug grc, epic arch. */
export function typeTint(type: string): UiModule {
  switch (type) {
    case "story":
      return "demand";
    case "task":
      return "knowledge";
    case "bug":
      return "grc";
    case "epic":
      return "arch";
    default:
      return "platform";
  }
}

// Fasi dello stage-gate per nome (italiano e inglese); le altre per posizione nel percorso.
const PHASES: [RegExp, UiModule][] = [
  [/^(bozza|draft)/i, "platform"],
  [/^(inviat|submitted|nuov|new)/i, "platform"],
  [/^triage/i, "knowledge"],
  [/^(valutazion|assessment|analisi|analysis)/i, "arch"],
  [/^(approvazion|approval|comitato)/i, "budget"],
  [/^(pianific|piano|planning)/i, "demand"],
  [/^(in esecuzione|esecuzione|execution|in corso|in progress|delivery)/i, "devsecops"],
  [/^(respint|rifiutat|rejected|annullat|cancel)/i, "grc"],
  [/^(chius|complet|conclus|closed|done|fatto)/i, "devsecops"],
];
const BY_POSITION: UiModule[] = ["platform", "knowledge", "arch", "budget", "demand", "devsecops"];

/** Colore della fase: dal nome se è una delle fasi note, altrimenti dalla posizione (index) nel percorso. */
export function phaseTint(name: string, index = -1, category?: string): UiModule {
  for (const [re, m] of PHASES) if (re.test(name.trim())) return m;
  if (category === "done") return "devsecops";
  if (index < 0) return "platform";
  return BY_POSITION[Math.min(index, BY_POSITION.length - 1)];
}
