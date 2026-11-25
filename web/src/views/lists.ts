import { tr } from "../i18n/core";
import { crumbs } from "../crumbs";

// Le liste che usano la stessa barra di filtri, le stesse viste e la stessa tabella
// (piano, passo 16). La chiave `page` identifica le viste salvate.
export type ListConfig = {
  page: string;
  /** Percorso nel titolo della pagina. */
  path: string;
  title: string;
  /** Query di partenza (senza vista salvata). */
  baseQuery: string;
  /** Tipo principale, per l'ordine delle fasi nel raggruppamento. */
  typeKey: string;
  /** Modalità disponibili; la prima è la predefinita. */
  modes: ("lista" | "tabella")[];
  defaultCols: string[];
  emptyText: string;
  /** Voce di menu a cui rimanda l'indirizzo della lista. */
  href: string;
};

const cols = ["key", "title", "state", "assignee", "updated"];

export const LISTS: Record<string, ListConfig> = {
  richieste: {
    page: "richieste", get path() { return crumbs("demand", "requests"); }, get title() { return tr("altro.views.list.richieste.title"); }, baseQuery: "tipo: business_request stato: aperta", // i18n-ok: sintassi della query (parole chiave italiane)
    typeKey: "business_request", modes: ["tabella", "lista"], defaultCols: ["key", "title", "unit", "sponsor", "estimate", "deadline", "score", "stage", "assignee"], get emptyText() { return tr("altro.views.list.richieste.empty"); }, href: "/richieste",
  },
  impatti: {
    page: "impatti", get path() { return crumbs("architecture", "impacts"); }, get title() { return tr("altro.views.list.impatti.title"); }, baseQuery: "tipo: impact ordina per: aggiornata desc", // i18n-ok: sintassi della query (parole chiave italiane)
    typeKey: "impact", modes: ["tabella"], defaultCols: ["key", "title", "state", "assignee", "updated"], get emptyText() { return tr("altro.views.list.impatti.empty"); }, href: "/elenco/impatti",
  },
  applicazioni: {
    page: "applicazioni", get path() { return crumbs("architecture", "applications"); }, get title() { return tr("altro.views.list.applicazioni.title"); }, baseQuery: "tipo: application ordina per: titolo asc", // i18n-ok: sintassi della query (parole chiave italiane)
    typeKey: "application", modes: ["tabella"], defaultCols: ["key", "title", "state", "assignee", "updated"], get emptyText() { return tr("altro.views.list.applicazioni.empty"); }, href: "/elenco/applicazioni",
  },
  asset: {
    page: "asset", get path() { return crumbs("architecture", "assets"); }, get title() { return tr("altro.views.list.asset.title"); }, baseQuery: "tipo: logical_asset ordina per: titolo asc", // i18n-ok: sintassi della query (parole chiave italiane)
    typeKey: "logical_asset", modes: ["tabella"], defaultCols: ["key", "title", "state", "updated"], get emptyText() { return tr("altro.views.list.asset.empty"); }, href: "/elenco/asset",
  },
  rischi: {
    page: "rischi", get path() { return crumbs("governance", "risks"); }, get title() { return tr("altro.views.list.rischi.title"); }, baseQuery: "tipo: risk ordina per: aggiornata desc", // i18n-ok: sintassi della query (parole chiave italiane)
    typeKey: "risk", modes: ["tabella"], defaultCols: cols, get emptyText() { return tr("altro.views.list.rischi.empty"); }, href: "/elenco/rischi",
  },
  incidenti: {
    page: "incidenti", get path() { return crumbs("governance", "incidents"); }, get title() { return tr("altro.views.list.incidenti.title"); }, baseQuery: "tipo: incident ordina per: aggiornata desc", // i18n-ok: sintassi della query (parole chiave italiane)
    typeKey: "incident", modes: ["tabella"], defaultCols: cols, get emptyText() { return tr("altro.views.list.incidenti.empty"); }, href: "/elenco/incidenti",
  },
  controlli: {
    page: "controlli", get path() { return crumbs("governance", "compliance"); }, get title() { return tr("altro.views.list.controlli.title"); }, baseQuery: "tipo: control ordina per: chiave asc", // i18n-ok: sintassi della query (parole chiave italiane)
    typeKey: "control", modes: ["tabella"], defaultCols: cols, get emptyText() { return tr("altro.views.list.controlli.empty"); }, href: "/elenco/controlli",
  },
  documenti: {
    page: "documenti", get path() { return crumbs("governance", "documents"); }, get title() { return tr("altro.views.list.documenti.title"); }, baseQuery: "tipo: page ordina per: aggiornata desc", // i18n-ok: sintassi della query (parole chiave italiane)
    typeKey: "page", modes: ["tabella"], defaultCols: cols, get emptyText() { return tr("altro.views.list.documenti.empty"); }, href: "/elenco/documenti",
  },
};

/** Dove porta il nome di una pagina di viste (preferiti nel menu, palette). */
export function listHref(page: string): string {
  return LISTS[page]?.href ?? "/richieste";
}
