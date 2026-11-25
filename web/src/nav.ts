// Voci della barra laterale: dai permessi, dai moduli accesi del tenant e dal profilo di menu
// (revisore) esce l'elenco delle sezioni. La palette ⌘K usa le stesse regole (modules.ts).
import type { Me } from "./api";
import { tr } from "./i18n/core";
import { pathVisible } from "./modules";
import { LISTS } from "./views/lists";
import { newLabel } from "./typesApi";

export type NavItem = { to?: string; label: string; count?: number; countLabel?: string; match?: string; more?: boolean; soon?: boolean };
export type NavSection = { label: string; color: string; items: NavItem[] };

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

export function buildNav(me: Me, c: NavCounts, names: NavNames = DEFAULT_NAMES): NavSection[] {
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

  // Work management (piano, passi 10 e 18).
  const delivery: NavItem[] = [
    { to: "/delivery/board", label: tr("shell.nav.board") },
    { to: "/delivery/backlog", label: tr("shell.nav.backlog") },
    { to: "/delivery/mio-lavoro", label: tr("shell.nav.mywork") },
    // Una sola voce: Timesheet, Report e Team sono schede dentro «Tempo» (pages/tempo-ui.tsx).
    { to: has("element.write") ? "/tempo" : "/tempo/report", label: tr("shell.nav.time"), count: c.timePending, countLabel: tr("shell.nav.toApprove"), match: "/tempo" },
  ];

  // «Governance» è il nome della sezione e delle sue pagine (replica/design.md, «Vocabolario e lingua»).
  const readEl = has("element.read");
  const governance: NavItem[] = [
    // Spazio dell'auditor (ondata 3, C6): per chi ha il permesso di audit.
    ...(has("grc.audit") ? [{ to: "/audit-spazio", label: tr("shell.nav.auditSpace") }] : []),
    ...(readEl ? [{ to: "/rischi", label: tr("shell.nav.risks") }] : []),
    // Conformità (piano, passo 20): con grc.read o grc.admin; senza, la voce non compare.
    ...(has("grc.read", "grc.admin") ? [{ to: "/conformita", label: tr("shell.nav.compliance") }] : []),
    // Vista DPO (ondata 3): con privacy.read o privacy.write.
    ...(has("privacy.read", "privacy.write") ? [{ to: "/privacy", label: tr("shell.nav.privacy") }] : []),
    ...(readEl
      ? [
          { to: "/devsecops", label: tr("shell.nav.devsecops") },
          { to: "/devsecops/aderenza", label: tr("shell.nav.adherence") },
          { to: "/devsecops/metriche", label: tr("shell.nav.metrics") },
          { to: "/devsecops/vulnerabilita", label: tr("shell.nav.vulns") },
        ]
      : []),
    { to: "/architettura", label: tr("shell.nav.architecture") },
    { to: "/documenti", label: tr("shell.nav.documents") },
  ];

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
    // Import di progetti da Jira, YouTrack e CSV, connettori GitHub, GitLab e Gitea (tenant.admin).
    ...(isAdmin
      ? [
          { to: "/configurazione/import", label: tr("shell.nav.import") },
          { to: "/configurazione/integrazioni/github", label: tr("shell.nav.github") },
          { to: "/configurazione/integrazioni/gitlab", label: tr("shell.nav.gitlab") },
          { to: "/configurazione/integrazioni/gitea", label: tr("shell.nav.gitea") },
        ]
      : []),
  ];

  // Indice per obiettivo: la casa unica delle impostazioni (compare solo a chi ne ha almeno una).
  if (config.length > 0) config.unshift({ to: "/configurazione", label: tr("casa.nav.config") });
  // Menu personale: «Da fare» è la prima voce (ondata 2, proposta 1), poi le Attività (gli avvisi).
  const personal: NavItem[] = [
    { to: "/da-fare", label: tr("dafare.nav.todo"), count: c.todo, more: c.todoMore },
    { to: "/notifiche", label: tr("dafare.nav.activity"), count: c.activity },
  ];

  const sections: NavSection[] = [
    { label: tr("dafare.nav.section"), color: "bg-accent", items: personal },
    { label: tr("shell.nav.section.demand"), color: "bg-accent", items: demand },
    { label: tr("shell.nav.section.delivery"), color: "bg-ok", items: delivery },
    { label: tr("shell.nav.section.governance"), color: "bg-flag", items: governance },
    { label: tr("shell.nav.section.lists"), color: "bg-muted", items: lists },
    { label: tr("shell.nav.section.config"), color: "bg-fg", items: config },
  ];
  // Un modulo spento o fuori dal profilo di menu non lascia voci; le sezioni vuote spariscono.
  return sections
    .map((s) => ({ ...s, items: s.items.filter((it) => !it.to || pathVisible(me, it.to)) }))
    .filter((s) => s.items.length > 0);
}
