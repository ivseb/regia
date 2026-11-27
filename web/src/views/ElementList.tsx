// Lista di elementi con barra della query, filtri visuali, viste salvate e tabella generica
// (piano, passo 16). Lo stato sta nell'indirizzo della pagina: `q` (query), `v` (vista
// salvata), `g` (raggruppamento), `vista` (lista o tabella), `sel` (elemento in anteprima).
// Un indirizzo copiato apre la stessa lista con gli stessi filtri.
import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { api, ApiError, queryApi, type Element } from "../api";
import type { RefLabel } from "../fieldsApi";
import { PageHeader, useMe } from "../shell";
import { QueryBar } from "../query";
import { ErrorNote } from "../ui";
import { ElementTable, type Selection, type TitleCell } from "./ElementTable";
import { FilterChips } from "./FilterChips";
import { ViewsBar } from "./ViewsBar";
import type { ListConfig } from "./lists";
import { viewsApi, type SavedView, type ViewKind, type ViewSettings } from "./viewsApi";
import { useTypeNames } from "../typesApi";
import { useT } from "../i18n/react";
import { hasEmptyState, ListaVuota } from "./ListaVuota";

export type ListSearch = { q?: string; sel?: string; vista?: "lista" | "tabella"; v?: string; g?: string };

/** Validazione comune dei parametri dell'indirizzo, per le rotte delle liste. */
export function validateListSearch(s: Record<string, unknown>): ListSearch {
  const str = (x: unknown) => (typeof x === "string" && x ? x : undefined);
  return { q: typeof s.q === "string" ? s.q : undefined, sel: str(s.sel), v: str(s.v), g: str(s.g), vista: s.vista === "tabella" || s.vista === "lista" ? s.vista : undefined };
}

const NO_GROUP = "nessuno";
const MAX_GROUPED = 1000;

type Layout = { cols: string[]; widths: Record<string, number> };

function loadLayout(key: string): Layout | null {
  try {
    const v = JSON.parse(localStorage.getItem(key) ?? "null");
    return v && Array.isArray(v.cols) ? { cols: v.cols, widths: v.widths ?? {} } : null;
  } catch {
    return null;
  }
}
function storeLayout(key: string, l: Layout) {
  try {
    localStorage.setItem(key, JSON.stringify(l));
  } catch {
    // Archiviazione non disponibile: la scelta vale fino al ricaricamento.
  }
}

const sameWidths = (a: Record<string, number>, b: Record<string, number>) => {
  const ka = Object.keys(a).filter((k) => a[k] !== b[k]);
  const kb = Object.keys(b).filter((k) => a[k] !== b[k]);
  return ka.length + kb.length === 0;
};

export function ElementList({
  config,
  search,
  onSearch,
  actions,
  renderTitle,
  renderList,
  detail,
  extraTabs,
  selection,
  hideHeader,
}: {
  config: ListConfig;
  search: ListSearch;
  onSearch: (s: ListSearch) => void;
  actions?: ReactNode;
  renderTitle?: TitleCell;
  /** Modalità "lista" (se la pagina ne ha una propria); senza, c'è solo la tabella. */
  renderList?: (ctx: { items: Element[]; labels: Record<string, RefLabel>; sel?: string; empty: boolean; query: string; onQuery: (q: string) => void }) => ReactNode;
  /** Pannello di anteprima a destra. */
  detail?: ReactNode;
  extraTabs?: ReactNode;
  selection?: Selection;
  hideHeader?: boolean;
}) {
  const t = useT();
  const page = config.page;
  const me = useMe().data;
  const memberId = me?.active_tenant?.member_id ?? "";
  const perms = me?.active_tenant?.permissions ?? [];
  const isAdmin = perms.includes("*") || perms.includes("tenant.admin");

  // --- Vista attiva: quella nell'indirizzo, oppure la predefinita (mia, poi dell'organizzazione).
  const views = useQuery({ queryKey: ["views", page], queryFn: () => viewsApi.list(page), staleTime: 30_000 });
  const saved = views.data?.items ?? [];
  const pristine = search.q === undefined && search.g === undefined && search.vista === undefined;
  const fromUrl = search.v ? saved.find((v) => v.id === search.v) : undefined;
  const fallback = !search.v && pristine ? (saved.find((v) => v.default_me) ?? saved.find((v) => v.default_tenant)) : undefined;
  const active: SavedView | undefined = fromUrl ?? fallback;
  // Finché non si sa quale vista vale non si lancia la ricerca (evita una query inutile).
  const settled = !views.isPending || (!search.v && !pristine);

  const baseline = active?.query ?? config.baseQuery;
  const query = search.q ?? baseline;
  const modes: readonly ("lista" | "tabella")[] = renderList ? config.modes : ["tabella"];
  const kindOf = (k: ViewKind) => (k === "list" && modes.includes("lista") ? "lista" : "tabella");
  const mode: "lista" | "tabella" = search.vista && modes.includes(search.vista) ? search.vista : active ? kindOf(active.kind) : modes[0];
  const group = search.g === NO_GROUP ? undefined : (search.g ?? active?.settings.group ?? undefined);

  // --- Colonne e larghezze: della vista, altrimenti l'ultima scelta salvata nel browser.
  const storageKey = `regia.tabella.${page}.${memberId}`;
  const [edit, setEdit] = useState<(Layout & { key: string }) | null>(null);
  const viewKey = active?.id ?? "-";
  const base: Layout = useMemo(
    () => (active?.settings.cols?.length ? { cols: active.settings.cols, widths: active.settings.widths ?? {} } : (loadLayout(storageKey) ?? { cols: config.defaultCols, widths: {} })),
    [active?.settings.cols, active?.settings.widths, storageKey, config.defaultCols],
  );
  const layout = edit && edit.key === viewKey ? edit : base;
  const onLayout = (cols: string[], widths: Record<string, number>) => {
    setEdit({ key: viewKey, cols, widths });
    storeLayout(storageKey, { cols, widths });
  };

  const settings: ViewSettings = { cols: layout.cols, widths: layout.widths, ...(group ? { group } : {}) };
  const current = { query, kind: (mode === "lista" ? "list" : "table") as ViewKind, settings };
  const modified =
    !!active &&
    (query !== active.query ||
      current.kind !== active.kind ||
      (group ?? "") !== (active.settings.group ?? "") ||
      JSON.stringify(layout.cols) !== JSON.stringify(active.settings.cols ?? config.defaultCols) ||
      !sameWidths(layout.widths, active.settings.widths ?? {}));

  // --- Ricerca.
  const list = useInfiniteQuery({
    queryKey: ["search", query, config.typeKey === "business_request"],
    // Le richieste portano con sé le colonne della domanda (unità, sponsor, stima, scadenza, punteggio).
    queryFn: ({ pageParam }) => api.search(query, pageParam, undefined, { demand: config.typeKey === "business_request" }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next,
    enabled: settled,
  });
  const items = useMemo(() => list.data?.pages.flatMap((p) => p.items) ?? [], [list.data]);
  const labels = useMemo(() => Object.assign({}, ...(list.data?.pages.map((p) => p.labels ?? {}) ?? [])) as Record<string, RefLabel>, [list.data]);
  // Con il raggruppamento servono tutti i risultati (fino a un tetto) perché i conteggi siano veri.
  const { hasNextPage, isFetching, fetchNextPage } = list;
  useEffect(() => {
    if (group && mode === "tabella" && hasNextPage && !isFetching && items.length < MAX_GROUPED) fetchNextPage();
  }, [group, mode, hasNextPage, isFetching, fetchNextPage, items.length]);

  const tn = useTypeNames();
  // Una lista senza tipo principale (domande sul grafo) non chiede il tipo.
  const type = useQuery({ queryKey: ["type", config.typeKey], queryFn: () => api.type(config.typeKey), staleTime: 300_000, enabled: !!config.typeKey });
  const fields = useQuery({ queryKey: ["query-fields"], queryFn: queryApi.fields, staleTime: 300_000 });

  // --- Indirizzo: ogni cambio tiene agganciata la vista attiva, anche se arrivava dalla predefinita.
  const update = useCallback(
    (patch: Partial<ListSearch>) => {
      const next: ListSearch = { ...search, ...patch };
      if (active && !next.v) next.v = active.id;
      if (next.q !== undefined && next.q.trim() === baseline.trim()) next.q = undefined;
      onSearch(next);
    },
    [search, active, baseline, onSearch],
  );

  const [draft, setDraft] = useState(query);
  useEffect(() => setDraft(query), [query]);
  const run = (v: string) => update({ q: v.trim() });

  const apply = (v: SavedView | null) => onSearch({ v: v?.id });
  const reset = () => onSearch({ v: active?.id });
  const afterSave = (v: SavedView | null) => {
    setEdit(null);
    onSearch({ v: v?.id });
  };
  const copyLink = async () => {
    const u = new URL(window.location.href);
    u.search = "";
    u.searchParams.set("q", query);
    if (active) u.searchParams.set("v", active.id);
    if (group) u.searchParams.set("g", group);
    if (mode !== modes[0]) u.searchParams.set("vista", mode);
    try {
      await navigator.clipboard.writeText(u.toString());
      return true;
    } catch {
      window.prompt(t("altro.views.promptCopy"), u.toString());
      return false;
    }
  };

  // Nemmeno un elemento di questo tipo: niente query in mostra, una frase e l'azione giusta.
  const probe = useQuery({
    queryKey: ["search-vuoto", config.typeKey],
    queryFn: () => api.search(`tipo: ${config.typeKey}`, undefined, 1), // i18n-ok: sintassi della query (parola chiave italiana)
    enabled: hasEmptyState(config.typeKey) && settled && !list.isPending && items.length === 0 && search.q === undefined,
    staleTime: 15_000,
  });
  const nothingAtAll = hasEmptyState(config.typeKey) && search.q === undefined && !!probe.data && probe.data.items.length === 0;
  const count = list.isPending && settled ? "…" : `${items.length}${list.hasNextPage ? "+" : ""}`;
  const apiErr = list.error instanceof ApiError && list.error.span;

  if (nothingAtAll) {
    return (
      <>
        {!hideHeader && <PageHeader path={config.path} title={tn.plural(config.typeKey, config.title)} actions={actions} />}
        <ListaVuota typeKey={config.typeKey} />
      </>
    );
  }
  return (
    <>
      {!hideHeader && <PageHeader path={config.path} title={tn.plural(config.typeKey, config.title)} actions={actions} />}
      <ViewsBar
        page={page}
        views={saved}
        active={active}
        modified={modified}
        current={current}
        isAdmin={isAdmin}
        onApply={apply}
        onReset={reset}
        onSaved={afterSave}
        onCopyLink={copyLink}
      />
      <div className="mt-2" />
      {/* Barra della query con evidenziazione e autocompletamento; i chip sotto sono la stessa query. */}
      <QueryBar
        value={draft}
        onChange={setDraft}
        onSubmit={run}
        submitted={{ query, error: list.error }}
        aside={
          <span className="flex items-center border-l border-hair px-3.5 font-mono text-[11.5px] text-muted tabular" aria-label={t("altro.views.resultsLabel")}>
            {count}
          </span>
        }
      />
      <FilterChips query={query} fields={fields.data?.items ?? []} onQuery={(q) => update({ q })} typeKey={config.typeKey} />
      <div role="tablist" aria-label={t("altro.views.viewTabs")} className="mt-2 flex gap-6 border-b border-hair px-4 pt-2 md:px-7">
        {modes.length > 1 &&
          modes.map((m) => {
            const on = mode === m;
            return (
              <button
                key={m}
                role="tab"
                aria-selected={on}
                className={on ? "border-b-2 border-fg pb-2.5 font-semibold" : "pb-2.5 text-muted hover:text-fg"}
                onClick={() => update({ vista: m, ...(m === "lista" ? { g: NO_GROUP } : {}) })}
              >
                {m === "lista" ? t("altro.views.modeList") : t("altro.views.modeTable")}
              </button>
            );
          })}
        {modes.length === 1 && <span role="tab" aria-selected="true" className="border-b-2 border-fg pb-2.5 font-semibold">{t("altro.views.modeTable")}</span>}
        {extraTabs}
      </div>

      <div className="flex min-h-0 flex-1 flex-wrap">
        <div className="min-w-0 flex-[999_1_560px] overflow-x-auto pl-4 md:pl-7">
          {list.error ? (
            // Gli errori della query con la posizione sono già sottolineati nella barra.
            <div className="py-6">{apiErr ? <p className="text-muted">{t("altro.views.fixQuery")}</p> : <ErrorNote error={list.error} />}</div>
          ) : mode === "lista" && renderList ? (
            renderList({ items, labels, sel: search.sel, empty: !list.isPending && items.length === 0, query, onQuery: run })
          ) : (
            <ElementTable
              items={items}
              labels={labels}
              query={query}
              onQuery={run}
              cols={layout.cols}
              widths={layout.widths}
              onLayout={onLayout}
              defaultCols={config.defaultCols}
              group={group}
              onGroup={(g) => update({ g: g ?? NO_GROUP, ...(renderList && g ? { vista: "tabella" } : {}) })}
              states={type.data?.states}
              sel={search.sel}
              empty={!list.isPending && settled && items.length === 0}
              emptyText={config.emptyText}
              renderTitle={renderTitle}
              selection={selection}
              partial={!!group && list.hasNextPage}
              typeKey={config.typeKey}
            />
          )}
          {list.hasNextPage && (!group || items.length >= MAX_GROUPED) && (
            <button className="btn my-4" onClick={() => list.fetchNextPage()} disabled={list.isFetchingNextPage}>
              {list.isFetchingNextPage ? t("altro.views.loading") : t("altro.views.loadMore")}
            </button>
          )}
        </div>
        {detail}
      </div>
    </>
  );
}
