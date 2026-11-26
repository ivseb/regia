// Moduli accendibili per tenant e profilo di menu del revisore (replica/usabilita.md, sezione 6
// punto 5). Le chiavi coincidono con internal/api/modules.go. Un modulo spento sparisce da
// barra laterale, ⌘K e pagine; i suoi dati restano e le sue API rispondono 404.
import { call, type Me } from "./api";

export type ModuleKey = "demand" | "work" | "knowledge" | "devsecops" | "grc" | "architecture";

export type ModuleInfo = {
  key: ModuleKey;
  name: string;
  description: string;
  effect: string;
  enabled: boolean;
  updated_at: string | null;
  updated_by: string | null;
};

export const modulesApi = {
  list: () => call<{ items: ModuleInfo[] }>("GET", "/tenant/modules"),
  save: (modules: Partial<Record<ModuleKey, boolean>>) => call<{ items: ModuleInfo[] }>("PUT", "/tenant/modules", { modules }),
};

/** Percorsi (e sottopercorsi) che appartengono a ciascun modulo. */
const MODULE_PATHS: Record<ModuleKey, string[]> = {
  demand: [
    "/richieste", "/cruscotto", "/roadmap", "/graduatoria", "/cabina", "/governance", "/approvazioni", "/valutazioni", "/requisiti", "/finanza", "/budget",
    "/portale", "/configurazione/avanzamento", "/configurazione/valutazioni", "/configurazione/requisiti", "/elenco/impatti",
  ],
  work: ["/delivery", "/tempo"],
  knowledge: ["/documenti", "/elenco/documenti"],
  devsecops: ["/devsecops", "/configurazione/devsecops", "/configurazione/gate", "/configurazione/vulnerabilita", "/configurazione/integrazioni"],
  grc: ["/conformita", "/audit-spazio", "/privacy", "/rischi", "/crisi", "/elenco/rischi", "/elenco/incidenti", "/elenco/controlli"],
  architecture: ["/architettura", "/elenco/applicazioni", "/elenco/asset"],
};

const under = (path: string, base: string) => path === base || path.startsWith(base + "/");

/** Modulo a cui appartiene la pagina (undefined = pagina della piattaforma). */
export function moduleOfPath(path: string): ModuleKey | undefined {
  for (const [key, bases] of Object.entries(MODULE_PATHS) as [ModuleKey, string[]][]) {
    if (bases.some((b) => under(path, b))) return key;
  }
  return undefined;
}

/** Tipi di elemento che appartengono a un modulo (la ricerca ⌘K non cerca nei moduli spenti). */
export const MODULE_TYPES: Record<ModuleKey, string[]> = {
  demand: ["business_request", "impact"],
  work: ["epic", "story", "task", "bug"],
  knowledge: ["page"],
  devsecops: [],
  grc: ["risk", "incident", "ict_provider", "control", "evidence"],
  architecture: ["application", "logical_asset", "capability"],
};

/** Il modulo è acceso? Senza informazioni (versione vecchia del server) tutto è acceso. */
export function moduleOn(me: Me | undefined | null, key: ModuleKey): boolean {
  return me?.active_tenant?.modules?.[key] !== false;
}

/** La pagina è raggiungibile: il suo modulo, se c'è, è acceso. */
export function pathOn(me: Me | undefined | null, path: string): boolean {
  const m = moduleOfPath(path);
  return !m || moduleOn(me, m);
}

/**
 * Pagina in cui restare dopo il cambio di organizzazione: la stessa se il suo modulo è acceso
 * anche nella nuova e non riguarda un elemento preciso (le chiavi, come WEB-12, sono dell'altra
 * organizzazione); altrimenti la pagina iniziale, che porta alla prima pagina valida.
 */
export function pathAfterSwitch(me: Me | undefined | null, path: string): string {
  if (!pathOn(me, path)) return "/";
  if (path.startsWith("/e/") || /\/[A-Za-z][A-Za-z0-9]*-\d+(\/|$)/.test(path)) return "/";
  return path;
}

/** Pagine del profilo di menu del revisore: i moduli di controllo, in sola lettura. */
const AUDITOR_PATHS = [
  "/governance", "/conformita", "/audit-spazio", "/rischi", "/crisi", "/devsecops", "/architettura", "/documenti", "/elenco",
  "/configurazione/audit", "/notifiche", "/profilo",
];

export function isAuditorProfile(me: Me | undefined | null): boolean {
  return me?.active_tenant?.menu_profile === "auditor";
}

/** Il profilo di menu dell'utente mostra la pagina? (Il menu normale mostra tutto.) */
export function pathInProfile(me: Me | undefined | null, path: string): boolean {
  if (!isAuditorProfile(me)) return true;
  return AUDITOR_PATHS.some((b) => under(path, b));
}

/** Pagina visibile nel menu e nella palette: modulo acceso e profilo di menu. */
export function pathVisible(me: Me | undefined | null, path: string): boolean {
  return pathOn(me, path) && pathInProfile(me, path);
}
