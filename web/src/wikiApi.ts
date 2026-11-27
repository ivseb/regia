// API della wiki personale (replica/ai.md §2 punto 1, ondata 4). Rispecchia internal/api/wiki.go.
import { call } from "./api";
import type { DiffLine } from "./knowledgeApi";

export type WikiAuthor = "persona" | "assistente";

export type WikiSummary = {
  id: string;
  title: string;
  version: number;
  updated_by: WikiAuthor;
  shared: boolean;
  promoted_key?: string;
  updated_at: string;
  excerpt: string;
  owner?: string;
};

export type WikiPage = {
  id: string;
  title: string;
  body: string;
  version: number;
  updated_by: WikiAuthor;
  shared: boolean;
  promoted_key?: string;
  deleted: boolean;
  created_at: string;
  updated_at: string;
  mine: boolean;
  owner?: string;
};

/** Elemento citato in una pagina, per chiave (DEM-12) o per codice (APP-MOB). */
export type WikiRef = {
  token: string;
  key: string;
  title: string;
  type: string;
  type_name: string;
  state: string;
  state_category: string;
};

export type WikiOp = "crea" | "modifica" | "elimina" | "ripristina" | "annulla";

export type WikiRevision = {
  id: string;
  page_id: string;
  page_title: string;
  version: number;
  op: WikiOp;
  author: WikiAuthor;
  title: string;
  note: string;
  deleted: boolean;
  added: number;
  removed: number;
  restored_version?: number;
  created_at: string;
  latest: boolean;
};

export type WikiState = {
  auto: boolean;
  running: boolean;
  requested_at?: string;
  last_run_at?: string;
  last_outcome?: "ok" | "nulla" | "errore";
  last_error?: string;
  last_pages: number;
  last_note?: string;
};

export type WikiHome = {
  pages: WikiSummary[];
  shared: WikiSummary[];
  state: WikiState;
  ai: { ready: boolean; masked: boolean };
  context_tokens: number;
};

export type WikiPageView = { page: WikiPage; refs: WikiRef[] };

export type WikiChange = {
  revision: WikiRevision;
  body: string;
  lines: DiffLine[];
  stats: { added: number; removed: number; changed: number };
  title_from?: string;
};

export type WikiAddInput = {
  element?: string;
  text?: string;
  page?: string;
  title?: string;
  note?: string;
};

const b = "/wiki";

export const wikiApi = {
  home: () => call<WikiHome>("GET", b),
  log: () => call<{ items: WikiRevision[] }>("GET", `${b}/log`),
  setAuto: (auto: boolean) => call<WikiHome>("PUT", `${b}/settings`, { auto }),
  refresh: () => call<WikiHome>("POST", `${b}/refresh`),
  add: (inp: WikiAddInput) => call<{ page: { id: string; title: string; version: number } }>("POST", `${b}/add`, inp),
  create: (title: string, body: string) => call<WikiPageView>("POST", `${b}/pages`, { title, body }),
  get: (id: string) => call<WikiPageView>("GET", `${b}/pages/${encodeURIComponent(id)}`),
  save: (id: string, title: string, body: string, base_version: number) =>
    call<WikiPageView>("PUT", `${b}/pages/${encodeURIComponent(id)}`, {
      title,
      body,
      base_version,
    }),
  remove: (id: string) => call<void>("DELETE", `${b}/pages/${encodeURIComponent(id)}`),
  history: (id: string) => call<{ items: WikiRevision[] }>("GET", `${b}/pages/${encodeURIComponent(id)}/history`),
  version: (id: string, n: number) => call<WikiChange>("GET", `${b}/pages/${encodeURIComponent(id)}/versions/${n}`),
  restore: (id: string, version: number) => call<{ page: WikiPage }>("POST", `${b}/pages/${encodeURIComponent(id)}/restore`, { version }),
  share: (id: string, shared: boolean) =>
    call<WikiPageView>("POST", `${b}/pages/${encodeURIComponent(id)}/share`, {
      shared,
    }),
  promote: (id: string, space: string) => call<{ key: string }>("POST", `${b}/pages/${encodeURIComponent(id)}/promote`, { space }),
  undo: (revisionId: string) => call<{ page: WikiPage }>("POST", `${b}/revisions/${encodeURIComponent(revisionId)}/undo`),
  exportUrl: "/api/v1/wiki/export",
};

/**
 * Prepara il testo per PageMarkdown: i codici citati (APP-MOB) diventano link alla chiave
 * dell'elemento, fuori dai blocchi di codice e dal codice in linea. Le chiavi (DEM-12) le
 * riconosce già PageMarkdown.
 */
export function linkCodes(md: string, refs: WikiRef[]): string {
  const codes = refs.filter((r) => r.token !== r.key && r.key);
  if (codes.length === 0) return md;
  const byCode = new Map(codes.map((r) => [r.token, r.key]));
  const esc = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const re = new RegExp(
    `(?<![\\w\\[(\`.-])(${[...byCode.keys()]
      .sort((a, b) => b.length - a.length)
      .map(esc)
      .join("|")})(?![\\w\\]\`-])`,
    "g",
  );
  let inFence = false;
  return md
    .split("\n")
    .map((line) => {
      if (/^\s*(```|~~~)/.test(line)) {
        inFence = !inFence;
        return line;
      }
      if (inFence) return line;
      // Il codice in linea resta com'è.
      return line
        .split(/(`[^`]*`)/)
        .map((part) => (part.startsWith("`") ? part : part.replace(re, (c) => `[${c}](${byCode.get(c)})`)))
        .join("");
    })
    .join("\n");
}
