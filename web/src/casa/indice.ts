// Indice «Configurazione» (replica/usabilita.md, proposta 7A): tutte le pagine di impostazioni,
// anche quelle sparse nelle pagine operative, organizzate per obiettivo. L'elenco serve alla
// pagina `/configurazione` e alla palette ⌘K (`configEntries`): una sola fonte.
import type { Me } from "../api";
import { tr, type Key } from "../i18n/core";
import { pathVisible } from "../modules";

export type GoalKey = "people" | "organization" | "types" | "processes" | "governance" | "finance" | "devsecops" | "compliance" | "integrations" | "language";

/** Gli obiettivi, nell'ordine in cui compaiono nell'indice. */
export const GOALS: GoalKey[] = ["people", "organization", "types", "processes", "governance", "finance", "devsecops", "compliance", "integrations", "language"];

type Def = {
  id: string;
  goal: GoalKey;
  to: string;
  search?: Record<string, string>;
  /** Basta uno di questi permessi; vuoto = per tutti. Gli stessi della barra laterale. */
  perms?: string[];
};

const DEFS: Def[] = [
  { id: "people", goal: "people", to: "/configurazione/persone", perms: ["tenant.admin"] },
  { id: "audit", goal: "people", to: "/configurazione/audit", perms: ["tenant.admin", "audit.read"] },
  { id: "org", goal: "organization", to: "/configurazione/organizzazione", perms: ["tenant.admin", "finance.write"] },
  { id: "modules", goal: "organization", to: "/configurazione/moduli", perms: ["tenant.admin"] },
  { id: "avvio", goal: "organization", to: "/configurazione/avvio", perms: ["tenant.admin"] },
  { id: "percorsoGuidato", goal: "organization", to: "/configurazione/percorso", perms: ["tenant.admin"] },
  { id: "mappaProcessi", goal: "processes", to: "/mappa-processi" },
  { id: "coinvolgi", goal: "processes", to: "/configurazione/coinvolgi", perms: ["tenant.admin"] },
  { id: "prova", goal: "organization", to: "/configurazione/prova", perms: ["tenant.admin"] },
  { id: "spaces", goal: "organization", to: "/documenti", perms: ["element.read"] },
  { id: "types", goal: "types", to: "/configurazione/tipi", perms: ["tenant.admin"] },
  { id: "fields", goal: "types", to: "/configurazione/campi", perms: ["fields.admin", "tenant.admin"] },
  { id: "processes", goal: "processes", to: "/processi", perms: ["process.admin"] },
  { id: "reviews", goal: "processes", to: "/configurazione/valutazioni", perms: ["process.admin"] },
  { id: "projectsCfg", goal: "processes", to: "/configurazione/progetti", perms: ["tenant.admin"] },
  { id: "progress", goal: "processes", to: "/configurazione/avanzamento", perms: ["portfolio.admin", "tenant.admin"] },
  { id: "portal", goal: "processes", to: "/configurazione/portale", perms: ["portfolio.admin", "tenant.admin"] },
  { id: "bodies", goal: "governance", to: "/governance", perms: ["element.read"] },
  { id: "scorecard", goal: "governance", to: "/graduatoria", search: { vista: "scorecard" }, perms: ["element.read"] },
  { id: "finStructure", goal: "finance", to: "/finanza", search: { tab: "struttura" }, perms: ["finance.read"] },
  { id: "rates", goal: "finance", to: "/finanza", search: { tab: "tariffe" }, perms: ["finance.read"] },
  { id: "budget", goal: "finance", to: "/budget", perms: ["element.read"] },
  { id: "dsoSettings", goal: "devsecops", to: "/configurazione/devsecops", perms: ["architecture.write", "tenant.admin"] },
  { id: "gate", goal: "devsecops", to: "/configurazione/gate", perms: ["architecture.write", "tenant.admin"] },
  { id: "vulns", goal: "devsecops", to: "/configurazione/vulnerabilita", perms: ["architecture.write", "tenant.admin"] },
  { id: "policy", goal: "devsecops", to: "/configurazione/devsecops/politica", perms: ["architecture.write", "tenant.admin"] },
  { id: "dora", goal: "devsecops", to: "/devsecops/metriche", perms: ["element.read"] },
  { id: "rules", goal: "compliance", to: "/conformita", search: { tab: "regole" }, perms: ["grc.read", "grc.admin"] },
  { id: "catalogs", goal: "compliance", to: "/conformita", search: { tab: "cataloghi" }, perms: ["grc.read", "grc.admin"] },
  { id: "requirements", goal: "compliance", to: "/configurazione/requisiti", perms: ["process.admin"] },
  { id: "risks", goal: "compliance", to: "/rischi", search: { tab: "configurazione" }, perms: ["element.read"] },
  { id: "continuity", goal: "compliance", to: "/configurazione/continuita", perms: ["continuity.admin", "tenant.admin"] },
  { id: "webhooks", goal: "integrations", to: "/configurazione/webhook", perms: ["tenant.admin"] },
  { id: "github", goal: "integrations", to: "/configurazione/integrazioni/github", perms: ["tenant.admin"] },
  { id: "gitlab", goal: "integrations", to: "/configurazione/integrazioni/gitlab", perms: ["tenant.admin"] },
  { id: "gitea", goal: "integrations", to: "/configurazione/integrazioni/gitea", perms: ["tenant.admin"] },
  { id: "servicenow", goal: "integrations", to: "/configurazione/integrazioni/servicenow", perms: ["tenant.admin"] },
  { id: "alm", goal: "integrations", to: "/configurazione/integrazioni/alm", perms: ["tenant.admin"] },
  { id: "import", goal: "integrations", to: "/configurazione/import", perms: ["tenant.admin"] },
  { id: "ai", goal: "integrations", to: "/configurazione/ai", perms: ["tenant.admin"] },
  { id: "aiUsage", goal: "integrations", to: "/configurazione/ai/consumo", perms: ["tenant.admin"] },
  { id: "tokens", goal: "integrations", to: "/profilo" },
  { id: "look", goal: "language", to: "/profilo" },
  { id: "lang", goal: "language", to: "/profilo" },
];

export type ConfigEntry = {
  id: string;
  goal: GoalKey;
  goalTitle: string;
  title: string;
  description: string;
  to: string;
  search?: Record<string, string>;
};

export const goalTitle = (g: GoalKey): string => tr(`casa.indice.goal.${g}` as Key);
export const goalDescription = (g: GoalKey): string => tr(`casa.indice.goal.${g}.d` as Key);

/**
 * Le pagine di configurazione che la persona può aprire (permessi e moduli accesi), nella lingua
 * corrente e nell'ordine dell'indice. È anche l'elenco per la palette ⌘K.
 */
export function configEntries(me: Me | undefined | null): ConfigEntry[] {
  const perms = me?.active_tenant?.permissions ?? [];
  const has = (ps?: string[]) => !ps || ps.length === 0 || perms.includes("*") || ps.some((p) => perms.includes(p));
  return DEFS.filter((d) => has(d.perms) && pathVisible(me, d.to)).map((d) => ({
    id: d.id,
    goal: d.goal,
    goalTitle: goalTitle(d.goal),
    title: tr(`casa.indice.e.${d.id}.t` as Key),
    description: tr(`casa.indice.e.${d.id}.d` as Key),
    to: d.to,
    search: d.search,
  }));
}

const norm = (s: string) => s.normalize("NFD").replace(/[̀-ͯ]/g, "").toLowerCase();

/** Voci che corrispondono a tutte le parole cercate (accenti e maiuscole ignorati) in titolo, descrizione e obiettivo. */
export function searchEntries(entries: ConfigEntry[], query: string): ConfigEntry[] {
  const words = norm(query).split(/\s+/).filter(Boolean);
  if (words.length === 0) return entries;
  return entries.filter((e) => {
    const hay = norm(`${e.title} ${e.description} ${e.goalTitle}`);
    return words.every((w) => hay.includes(w));
  });
}

/** Il percorso della pagina come indirizzo (per i link fuori dal router, come la palette). */
export function entryHref(e: Pick<ConfigEntry, "to" | "search">): string {
  const q = e.search ? `?${new URLSearchParams(e.search).toString()}` : "";
  return `${e.to}${q}`;
}
