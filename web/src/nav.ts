// Voci della barra laterale: dai permessi, dai moduli accesi del tenant e dal profilo di menu
// (revisore) esce l'elenco delle sezioni. La palette ⌘K usa le stesse regole (modules.ts).
import type { Me } from "./api";
import { tr } from "./i18n/core";
import { pathVisible } from "./modules";
import { LISTS } from "./views/lists";
import { newLabel } from "./typesApi";
import type { IconKey, UiModule } from "./moduleIcon";

/**
 * `hot`: il numero chiede un'azione (pillola piena nel colore del modulo). `also`: altri percorsi (e i
 * loro sottopercorsi) in cui la voce è attiva, per le voci che riuniscono più pagine (Portafoglio, Elenchi).
 */
export type NavItem = { to?: string; label: string; count?: number; countLabel?: string; match?: string; more?: boolean; soon?: boolean; hot?: boolean; also?: string[] };
/** Ogni sezione ha un modulo (colore, replica/mockup/interfaccia-colori.html) e un'icona; senza icona quella del modulo. */
export type NavSection = { label: string; module: UiModule; icon?: IconKey; items: NavItem[] };

/** Numeri accanto alle voci (code di lavoro dell'utente). */
export type NavCounts = {
  /** «Da fare»: tutto ciò che chiede un'azione (decisioni, valutazioni, verifiche, impatti, tempo, menzioni). */
  todo?: number;
  todoMore?: boolean;
  /** Attività: avvisi non letti. */
  activity?: number;
  timePending?: number;
};

/** Nomi dei tipi (Configurazione → Tipi): le voci di menu che li citano li prendono da qui. */
export type NavNames = { plural: (key: string, fallback: string) => string; create: (key: string, fallback: string) => string };
const DEFAULT_NAMES: NavNames = { plural: (_k, f) => f, create: (_k, f) => newLabel(f) };

/** Tutte le pagine del menu completo, sezione per sezione, con i permessi e i moduli accesi. */
function allSections(me: Me, c: NavCounts, names: NavNames): NavSection[] {
  const perms = me.active_tenant?.permissions ?? [];
  const has = (...ps: string[]) => perms.includes("*") || ps.some((p) => perms.includes(p));

  const demand: NavItem[] = [
    { to: "/richieste", label: names.plural("business_request", tr("shell.nav.requests")) },
    { to: "/cruscotto", label: tr("shell.nav.dashboard") },
    { to: "/roadmap", label: tr("shell.nav.roadmap") },
    { to: "/graduatoria", label: tr("shell.nav.ranking") },
    { to: "/cabina", label: tr("shell.nav.cabina") },
    { to: "/governance", label: tr("shell.nav.governance") },
    // Budget e costi (piano, passi 11 e 12): solo per finanza e PMO.
    ...(has("finance.read") ? [{ to: "/finanza", label: tr("shell.nav.finance") }] : []),
    // Budget gerarchico (replica/budget.md): l'albero dei budget; ciò che si vede lo decide il database.
    ...(has("element.read") ? [{ to: "/budget", label: tr("budget.nav.tree") }] : []),
    // Le code «Le mie approvazioni», «Le mie valutazioni» e «Da verificare» sono ora in «Da fare»: le pagine
    // restano raggiungibili dall'indirizzo e rimandano lì.
    // Chi non può scrivere (per esempio il revisore) non vede le voci che servono a creare o a registrare.
    ...(has("element.write") ? [{ to: "/richieste/nuova", label: names.create("business_request", "Richiesta") }] : []),
    // Portale del richiedente (piano, passo 14): per vedere ciò che vedono i richiedenti.
    ...(has("portal.submit", "element.write") ? [{ to: "/portale", label: tr("shell.nav.portal", { type: names.plural("business_request", tr("shell.nav.requests")).toLowerCase() }) }] : []),
  ];

  // Progetti (replica/progetti.md): il piano del PM sopra le epiche.
  const projects: NavItem[] = has("element.read")
    ? [
        { to: "/progetti", label: names.plural("project", tr("shell.nav.projects")) },
        ...(has("element.write") ? [{ to: "/progetti/nuovo", label: names.create("project", "Progetto") }] : []),
      ]
    : [];

  // Work management (piano, passi 10 e 18).
  const delivery: NavItem[] = [
    { to: "/delivery/board", label: tr("shell.nav.board") },
    { to: "/delivery/backlog", label: tr("shell.nav.backlog") },
    { to: "/delivery/mio-lavoro", label: tr("shell.nav.mywork") },
    // Una sola voce: Timesheet, Report e Team sono schede dentro «Tempo» (pages/tempo-ui.tsx).
    { to: has("element.write") ? "/tempo" : "/tempo/report", label: tr("shell.nav.time"), count: c.timePending, countLabel: tr("shell.nav.toApprove"), match: "/tempo", hot: true },
  ];

  // L'ex sezione «Governance» è divisa per modulo: rischi e conformità, DevSecOps, documenti, architettura.
  // «Governance» resta il nome della pagina di demand (replica/design.md, «Vocabolario e lingua»).
  const readEl = has("element.read");
  const grc: NavItem[] = [
    // Spazio dell'auditor (ondata 3, C6): per chi ha il permesso di audit.
    ...(has("grc.audit") ? [{ to: "/audit-spazio", label: tr("shell.nav.auditSpace") }] : []),
    ...(readEl ? [{ to: "/rischi", label: tr("shell.nav.risks") }] : []),
    // Conformità (piano, passo 20): con grc.read o grc.admin; senza, la voce non compare.
    ...(has("grc.read", "grc.admin") ? [{ to: "/conformita", label: tr("shell.nav.compliance") }] : []),
    // Vista DPO (ondata 3): con privacy.read o privacy.write.
    ...(has("privacy.read", "privacy.write") ? [{ to: "/privacy", label: tr("shell.nav.privacy") }] : []),
  ];
  const devsecops: NavItem[] = readEl
    ? [
        { to: "/devsecops", label: tr("shell.nav.devsecops") },
        { to: "/devsecops/aderenza", label: tr("shell.nav.adherence") },
        { to: "/devsecops/metriche", label: tr("shell.nav.metrics") },
        { to: "/devsecops/vulnerabilita", label: tr("shell.nav.vulns") },
      ]
    : [];
  const documents: NavItem[] = [{ to: "/documenti", label: tr("shell.nav.documents") }];
  const architecture: NavItem[] = [{ to: "/architettura", label: tr("shell.nav.architecture") }];

  // Elenchi con filtri visuali, viste salvate e raggruppamento (piano, passo 16).
  const lists: NavItem[] = readEl
    ? ["impatti", "applicazioni", "asset", "rischi", "incidenti", "controlli", "documenti"].map((p) => ({ to: LISTS[p].href, label: tr("shell.nav.list", { type: names.plural(LISTS[p].typeKey, LISTS[p].title).toLowerCase() }) }))
    : [];

  const isAdmin = has("tenant.admin");
  const archWrite = has("architecture.write", "tenant.admin");
  const config: NavItem[] = [
    ...(has("process.admin")
      ? [
          { to: "/processi", label: tr("shell.nav.processes") },
          { to: "/configurazione/valutazioni", label: tr("shell.nav.reviewCfg") },
          { to: "/configurazione/requisiti", label: tr("shell.nav.reqCfg") },
        ]
      : []),
    // Campi personalizzati: amministratore dei campi (fields.admin) o del tenant.
    ...(has("fields.admin", "tenant.admin") ? [{ to: "/configurazione/campi", label: tr("shell.nav.fields") }] : []),
    // Organizzazione: albero delle unità, persone con ruolo, import CSV (la visibilità dei budget lo segue).
    ...(has("tenant.admin", "finance.write") ? [{ to: "/configurazione/organizzazione", label: tr("budget.nav.org") }] : []),
    // Progetti: modello predefinito, nascita dalla richiesta, ruoli del progetto (tenant.admin).
    ...(isAdmin ? [{ to: "/configurazione/progetti", label: tr("shell.nav.projectsCfg") }] : []),
    // Tipi di elemento: nomi, icone e colori per tenant (tenant.admin).
    ...(isAdmin ? [{ to: "/configurazione/tipi", label: tr("shell.nav.types") }] : []),
    // Status report e portale (piano, passi 13 e 14): governo del portafoglio o amministratore.
    ...(has("portfolio.admin", "tenant.admin") ? [{ to: "/configurazione/avanzamento", label: tr("shell.nav.progress") }, { to: "/configurazione/portale", label: tr("configurazione.portale.nav") }] : []),
    // DevSecOps: la configurazione per chi gestisce il catalogo (architecture.write) o l'organizzazione.
    ...(archWrite
      ? [
          { to: "/configurazione/devsecops", label: tr("shell.nav.devsecops") },
          { to: "/configurazione/gate", label: tr("shell.nav.gate") },
          { to: "/configurazione/vulnerabilita", label: tr("shell.nav.vulnCfg") },
          { to: "/configurazione/devsecops/politica", label: tr("shell.nav.policy") },
        ]
      : []),
    // Persone e accessi, moduli e webhook (tenant.admin), Registro di audit (audit.read): piano, passo 15.
    ...(isAdmin
      ? [
          { to: "/configurazione/avvio", label: tr("configurazione.avvio.nav") },
          { to: "/configurazione/prova", label: tr("configurazione.prova.nav") },
          { to: "/configurazione/persone", label: tr("shell.nav.people") },
          { to: "/configurazione/moduli", label: tr("shell.nav.modules") },
          { to: "/configurazione/webhook", label: tr("shell.nav.webhook") },
        ]
      : []),
    ...(has("tenant.admin", "audit.read") ? [{ to: "/configurazione/audit", label: tr("shell.nav.audit") }] : []),
    // Import di progetti da Jira, YouTrack e CSV, connettori GitHub, GitLab, Gitea e ServiceNow (tenant.admin).
    ...(isAdmin
      ? [
          { to: "/configurazione/import", label: tr("shell.nav.import") },
          { to: "/configurazione/integrazioni/github", label: tr("shell.nav.github") },
          { to: "/configurazione/integrazioni/gitlab", label: tr("shell.nav.gitlab") },
          { to: "/configurazione/integrazioni/gitea", label: tr("shell.nav.gitea") },
          { to: "/configurazione/integrazioni/servicenow", label: tr("shell.nav.servicenow") },
          { to: "/configurazione/ai", label: tr("ai.nav.settings") },
          { to: "/configurazione/ai/consumo", label: tr("ai.nav.usage") },
        ]
      : []),
  ];

  // Indice per obiettivo: la casa unica delle impostazioni (compare solo a chi ne ha almeno una).
  if (config.length > 0) config.unshift({ to: "/configurazione", label: tr("casa.nav.config") });
  // Menu personale: «Da fare» è la prima voce (ondata 2, proposta 1), poi le Attività (gli avvisi).
  const personal: NavItem[] = [
    { to: "/da-fare", label: tr("dafare.nav.todo"), count: c.todo, more: c.todoMore, hot: true },
    { to: "/notifiche", label: tr("dafare.nav.activity"), count: c.activity },
  ];

  const sections: NavSection[] = [
    { label: tr("dafare.nav.section"), module: "budget", icon: "star", items: personal },
    { label: tr("shell.nav.section.demand"), module: "demand", items: demand },
    { label: tr("shell.nav.section.projects"), module: "work", icon: "list", items: projects },
    { label: tr("shell.nav.section.delivery"), module: "work", items: delivery },
    { label: tr("shell.colori.section.grc"), module: "grc", items: grc },
    // Quando la prima voce ha il nome della sezione, la testata stessa è il collegamento (shell.tsx).
    { label: tr("shell.nav.devsecops"), module: "devsecops", items: devsecops },
    { label: tr("shell.nav.documents"), module: "knowledge", items: documents },
    { label: tr("shell.nav.architecture"), module: "arch", items: architecture },
    { label: tr("shell.nav.section.lists"), module: "platform", icon: "list", items: lists },
    { label: tr("shell.nav.section.config"), module: "platform", icon: "settings", items: config },
  ];
  // Un modulo spento o fuori dal profilo di menu non lascia voci; le sezioni vuote spariscono.
  return sections
    .map((s) => ({ ...s, items: s.items.filter((it) => !it.to || pathVisible(me, it.to)) }))
    .filter((s) => s.items.length > 0);
}

// ---------------------------------------------------------------------------------------------
// Menu snello: le pagine restano tutte (allSections, navPages), il menu ne mostra una ventina.
// Le azioni («Nuova richiesta», «Nuovo progetto», il portale) stanno nelle testate delle pagine e
// in ⌘K; le pagine sorelle diventano una voce sola con le schede; Elenchi e Configurazione sono indici.

const under = (path: string, base: string) => path === base || path.startsWith(base + "/");

/** Pagine che escono dal menu: si raggiungono dalle testate delle pagine e da ⌘K. */
const MENU_OUT = new Set(["/richieste/nuova", "/portale", "/progetti/nuovo"]);
const PORTFOLIO = ["/graduatoria", "/cabina", "/governance"];
const FINANCE = ["/finanza", "/budget"];
const CONFIG = ["/configurazione", "/processi"];
/** Le pagine di DevSecOps e di Rischi e conformità: una voce sola con le schede (portafoglio-ui.tsx). */
export const DEVSECOPS_TABS = ["/devsecops", "/devsecops/aderenza", "/devsecops/metriche", "/devsecops/vulnerabilita"];
export const GRC_TABS = ["/rischi", "/conformita", "/privacy", "/audit-spazio"];

/**
 * Un gruppo di pagine che nel menu diventa una voce sola. `make` riceve le pagine raggiungibili del
 * gruppo e restituisce le voci da mostrare: con una sola pagina resta la sua voce (il revisore, che vede
 * solo «Governance» o solo «Registro di audit», non riceve un indice fuori dal suo profilo).
 */
type Group = { of: (to: string) => boolean; make: (members: NavItem[]) => NavItem[] };
const single = (m: NavItem[], hub: () => NavItem): NavItem[] => (m.length === 1 ? m : [hub()]);
/** La prima pagina del gruppo nell'ordine delle schede (la voce porta lì). */
const firstOf = (m: NavItem[], order: string[]) => order.find((to) => m.some((x) => x.to === to)) ?? m[0].to;
const GROUPS: Group[] = [
  { of: (to) => PORTFOLIO.includes(to), make: (m) => single(m, () => ({ to: m[0].to, label: tr("shell.menu.portfolio"), also: PORTFOLIO })) },
  { of: (to) => FINANCE.includes(to), make: (m) => single(m, () => ({ to: m[0].to, label: tr("shell.menu.financeBudget"), also: FINANCE })) },
  // DevSecOps: Panoramica, Aderenza, Metriche e Vulnerabilità sono schede; la voce ha il nome della sezione,
  // quindi la testata stessa è il collegamento (shell.tsx).
  { of: (to) => DEVSECOPS_TABS.includes(to), make: (m) => single(m, () => ({ to: firstOf(m, DEVSECOPS_TABS), label: tr("shell.nav.devsecops"), also: DEVSECOPS_TABS })) },
  // Rischi e conformità: le schede sono le pagine che i permessi lasciano (grc.audit, grc.read, privacy.read).
  { of: (to) => GRC_TABS.includes(to), make: (m) => single(m, () => ({ to: firstOf(m, GRC_TABS), label: tr("shell.colori.section.grc"), also: GRC_TABS })) },
  { of: (to) => to.startsWith("/elenco/"), make: () => [{ to: "/elenco", label: tr("shell.nav.section.lists"), also: ["/elenco"] }] },
  // L'indice /configurazione c'è solo se il profilo di menu lo mostra; senza, le voci restano com'erano.
  { of: (to) => CONFIG.some((b) => under(to, b)), make: (m) => (m.some((x) => x.to === "/configurazione") ? [{ to: "/configurazione", label: tr("shell.nav.section.config"), also: CONFIG }] : m) },
];

/** Una sezione del menu snello: le azioni escono, ogni gruppo prende il posto del suo primo membro. */
function compact(items: NavItem[]): NavItem[] {
  const out: NavItem[] = [];
  const done = new Set<Group>();
  for (const it of items) {
    if (!it.to) {
      out.push(it);
      continue;
    }
    if (MENU_OUT.has(it.to)) continue;
    const g = GROUPS.find((x) => x.of(it.to!));
    if (!g) {
      out.push(it);
      continue;
    }
    if (done.has(g)) continue;
    done.add(g);
    out.push(...g.make(items.filter((x) => x.to && g.of(x.to))));
  }
  return out;
}

/** Le sezioni della barra laterale. */
export function buildNav(me: Me, c: NavCounts, names: NavNames = DEFAULT_NAMES): NavSection[] {
  return allSections(me, c, names)
    .map((s) => ({ ...s, items: compact(s.items) }))
    .filter((s) => s.items.length > 0);
}

/**
 * Tutte le pagine raggiungibili dal menu, anche quelle dentro un indice o una scheda: servono a
 * «g» + lettera e all'elenco di «?» (`allowed` in shell.tsx), a ⌘K e al titolo della scheda del browser.
 */
export function navPages(me: Me, c: NavCounts = {}, names: NavNames = DEFAULT_NAMES): NavItem[] {
  const items = allSections(me, c, names).flatMap((s) => s.items);
  if (items.some((it) => it.to?.startsWith("/elenco/"))) items.push({ to: "/elenco", label: tr("shell.nav.section.lists") });
  return items;
}

/** La voce è attiva sul percorso: la pagina stessa, i sottopercorsi dove previsto, le pagine riunite. */
export function navItemActive(it: NavItem, path: string, prefix: ReadonlySet<string> = new Set()): boolean {
  if (!it.to) return false;
  if (it.also?.some((b) => under(path, b))) return true;
  if (it.match) return under(path, it.match);
  if (prefix.has(it.to)) return under(path, it.to);
  return path === it.to;
}

/** Chiave stabile di una sezione (per ricordare quali sono chiuse): modulo e icona la distinguono. */
export const sectionKey = (s: NavSection) => (s.icon ? `${s.module}-${s.icon}` : s.module);
