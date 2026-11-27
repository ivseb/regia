// Backlog e sprint (piano, passo 18; ricognizione S06): sprint aperti in cima, backlog sotto,
// tutto nell'ordine manuale. Si trascina per riordinare e per pianificare uno sprint (sul
// telefono si usa «Sposta in»). Avvio e chiusura sono passaggi del processo dello sprint:
// alla chiusura gli incompiuti passano al successivo o tornano nel backlog. Burndown dalla
// storia degli stati, in SVG semplice.
import { useStateName } from "../stateLabels";
import { Select } from "../select";
import { EmptyState } from "../emptyState";
import { lazy, Suspense, memo, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type DragEvent } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError } from "../api";
import { PageHeader, useMe } from "../shell";
import { moduleOn } from "../modules";
import { QueryBar, withLabel } from "../query";
import { TagChips } from "../tags";
import { ErrorNote, Loading } from "../ui";
import { useNarrow } from "./timeline";
import { NewCard } from "./Board";
import { BulkBar, SelectBox, useSelection } from "./BulkBar";
import { Dialog, MineChip, Person, PriorityDot, ReadOnlyBanner, SpacePicker, TypeMark, useSpaces } from "./work-ui";
import { useT } from "../i18n/react";
import { Trans } from "../i18n/Trans";
import { loc } from "../i18n/format";
import { useTypeNames } from "../typesApi";
import { fmtPoints, workApi, type Backlog as BacklogData, type BacklogItem, type Burndown, type CreateType, type SprintView } from "../workApi";
import { crumbs } from "../crumbs";

// «In arrivo dai progetti» (progetti.md, passo 5): caricato solo se il modulo dei progetti è acceso.
const IncomingWork = lazy(() => import("./progetti/IncomingWork").then((m) => ({ default: m.IncomingWork })));

export type BacklogSearch = { spazio?: string; q?: string };

const ROW = 44;
// Sul telefono il titolo va su una seconda riga a tutta larghezza: la riga è più alta.
const ROW_NARROW = 78;
const BACKLOG = "backlog";
/** Voci del backlog caricate per volta: oltre si chiede il resto (nel benchmark un backlog di 1.400 voci pesava 460 KB). */
const BACKLOG_PAGE = 300;

type Drop = { list: string; index: number } | null;

export function Backlog({ search, onSearch }: { search: BacklogSearch; onSearch: (s: BacklogSearch) => void }) {
  const t = useT();
  const qc = useQueryClient();
  const narrow = useNarrow();
  const projectsOn = moduleOn(useMe().data, "projects");
  const { spaces, items: spaceList, current } = useSpaces(search.spazio);
  const [draft, setDraft] = useState(search.q ?? "");
  useEffect(() => setDraft(search.q ?? ""), [search.q]);
  const [limit, setLimit] = useState(BACKLOG_PAGE);
  useEffect(() => setLimit(BACKLOG_PAGE), [current, search.q]);
  const key = ["work", "backlog", current, search.q ?? "", limit] as const;
  const data = useQuery({ queryKey: key, queryFn: () => workApi.backlog(current!, search.q, limit), enabled: !!current, placeholderData: (p) => p });
  const [error, setError] = useState<unknown>(null);
  const [creating, setCreating] = useState(false);
  const [newSprint, setNewSprint] = useState(false);
  const [closing, setClosing] = useState<SprintView | null>(null);
  const [editing, setEditing] = useState<SprintView | null>(null);
  const dragged = useRef<BacklogItem | null>(null);
  const [drop, setDrop] = useState<Drop>(null);
  const d = data.data;

  const lists = useMemo(() => {
    const m = new Map<string, BacklogItem[]>([[BACKLOG, []]]);
    for (const s of d?.sprints ?? []) m.set(s.id, []);
    for (const it of d?.items ?? []) m.get(it.sprint || BACKLOG)?.push(it);
    return m;
  }, [d]);

  // Selezione multipla per i comandi in blocco, nell'ordine in cui le liste sono a schermo.
  const order = useMemo(() => [...(d?.sprints ?? []).flatMap((s) => lists.get(s.id) ?? []), ...(lists.get(BACKLOG) ?? [])].map((i) => i.id), [d?.sprints, lists]);
  const selection = useSelection(order);
  const refresh = () => qc.invalidateQueries({ queryKey: ["work"] });
  const place = useMutation({
    mutationFn: async ({ item, list, after, before }: { item: BacklogItem; list: string; after?: string; before?: string }): Promise<unknown> => {
      const sprintKey = list === BACKLOG ? "" : (d?.sprints.find((s) => s.id === list)?.key ?? "");
      if ((item.sprint || BACKLOG) === list) return workApi.rank(item.key, after, before);
      return workApi.plan(item.key, sprintKey, after, before);
    },
    onError: (e) => setError(e),
    onSettled: refresh,
  });

  /** Mette l'elemento nella lista alla posizione index (contando anche l'elemento stesso). */
  const moveTo = useCallback(
    (item: BacklogItem, list: string, index: number) => {
      const all = lists.get(list) ?? [];
      const i = all.findIndex((x) => x.id === item.id);
      if (i >= 0 && index > i) index--;
      if (i === index) return;
      const others = all.filter((x) => x.id !== item.id);
      const after = others[index - 1]?.id;
      const before = others[index]?.id;
      setError(null);
      // Ottimistico: si sposta subito nella cache.
      qc.setQueryData<BacklogData>(key, (old) => {
        if (!old) return old;
        const rest = old.items.filter((x) => x.id !== item.id);
        const moved = { ...item, sprint: list === BACKLOG ? "" : list };
        let at = rest.length;
        if (before) at = rest.findIndex((x) => x.id === before);
        else if (after) at = rest.findIndex((x) => x.id === after) + 1;
        rest.splice(at < 0 ? rest.length : at, 0, moved);
        return { ...old, items: rest };
      });
      place.mutate({ item, list, after, before });
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [lists, qc, current, search.q],
  );

  const start = useMutation({ mutationFn: (s: SprintView) => workApi.startSprint(s.key), onSuccess: refresh, onError: (e) => setError(e) });

  if (spaces.isPending) return <Loading className="px-4 py-6 text-muted md:px-7" />;
  if (spaces.error) return <div className="px-4 py-6 md:px-7"><ErrorNote error={spaces.error} /></div>;
  if (!current)
    return (
      <>
        <PageHeader path={crumbs("delivery", "backlog")} title={t("lavoro.backlog.title")} />
        <div className="px-4 md:px-7">
          <EmptyState
            title={t("lavoro.backlog.noSpace")}
            text={t("lavoro.backlog.noSpaceText")}
            action={<Link to="/architettura" search={{ tab: "team" }} className="btn no-underline">{t("lavoro.backlog.goTeams")}</Link>}
          />
        </div>
      </>
    );

  const active = d?.sprints.find((s) => s.category === "in_progress");
  const dnd: Dnd = {
    sel: selection.sel,
    onSelect: selection.toggle,
    canSelect: !!d?.can_work,
    canDrag: !!d?.can_work && !narrow,
    onTag: (name: string) => onSearch({ spazio: current, q: withLabel(search.q ?? "", name) }),
    onStart: (it: BacklogItem, e: DragEvent) => {
      dragged.current = it;
      e.dataTransfer.effectAllowed = "move";
      e.dataTransfer.setData("text/plain", it.key);
    },
    onEnd: () => {
      dragged.current = null;
      setDrop(null);
    },
    onOver: (list: string, index: number) => setDrop((x) => (x && x.list === list && x.index === index ? x : { list, index })),
    onDrop: (list: string, index: number) => {
      const it = dragged.current;
      dragged.current = null;
      setDrop(null);
      if (it) moveTo(it, list, index);
    },
  };
  const destinations = [{ id: BACKLOG, label: t("lavoro.backlog.name") }, ...(d?.sprints ?? []).map((s) => ({ id: s.id, label: s.title }))];

  return (
    <>
      <PageHeader
        path={`${crumbs("delivery", "backlog")} / ${current}`}
        title={t("lavoro.backlog.title")}
        actions={
          <>
            {d?.can_work && (
              <button className="btn btn-primary" onClick={() => setCreating(true)}>
                {t("lavoro.backlog.newItem")}
              </button>
            )}
            {d?.can_manage && (
              <button className="btn" onClick={() => setNewSprint(true)}>
                {t("lavoro.backlog.newSprint")}
              </button>
            )}
          </>
        }
      />
      <div className="flex flex-wrap items-end gap-x-5 gap-y-2 px-4 pb-3 md:px-7">
        <SpacePicker value={current} items={spaceList} onChange={(k) => onSearch({ spazio: k, q: search.q })} />
        <Link to="/delivery/board" search={{ spazio: current }} className="pb-1.5 text-[12.5px] text-accent underline">
          {t("lavoro.backlog.board")}
        </Link>
        {d && (
          <span className="ml-auto pb-1.5 font-mono text-[11.5px] text-muted tabular">
            {d.more && d.backlog_total
              ? t("lavoro.backlog.countOf", { n: lists.get(BACKLOG)?.length ?? 0, total: d.backlog_total })
              : t("lavoro.backlog.count", { n: lists.get(BACKLOG)?.length ?? 0 })}
            {d.truncated ? t("lavoro.backlog.tooMany") : ""}
          </span>
        )}
      </div>
      <QueryBar
        value={draft}
        onChange={setDraft}
        onSubmit={(v) => onSearch({ spazio: current, q: v.trim() || undefined })}
        submitted={{ query: search.q ?? "", error: data.error }}
        label={t("lavoro.backlog.filter")}
      />
      <div className="flex flex-wrap items-center gap-2 px-4 pt-2 md:px-7">
        <MineChip q={search.q} onChange={(q) => onSearch({ spazio: current, q })} />
      </div>
      {d && !d.can_work && <div className="pt-2"><ReadOnlyBanner what={t("lavoro.backlog.readOnly")} /></div>}
      <div className="px-4 pt-3 md:px-7">
        <ErrorNote error={error ?? (data.error instanceof ApiError && data.error.span ? null : data.error)} />
      </div>
      {d && (
        <div className="grid min-w-0 grid-cols-1 gap-6 px-4 pt-2 pb-10 md:px-7 xl:grid-cols-[minmax(0,1fr)_380px]">
          <div className="flex min-w-0 flex-col gap-6">
            {d.sprints.map((s) => (
              <section key={s.id} aria-label={s.title} className="flex flex-col gap-1.5">
                <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1 border-b-2 border-rule pb-1.5">
                  <h2 className="m-0 text-[15px] font-bold">
                    <Link to="/e/$key" params={{ key: s.key }} className="text-fg no-underline hover:text-accent">
                      {s.title}
                    </Link>
                  </h2>
                  <span className={`text-[12px] font-semibold ${s.category === "in_progress" ? "text-accent" : "text-muted"}`}>{s.state}</span>
                  <span className="font-mono text-[11.5px] text-muted tabular">
                    {s.start ? fmtDay(s.start) : "—"} → {s.end ? fmtDay(s.end) : "—"}
                  </span>
                  <span className="font-mono text-[11.5px] text-muted tabular" title={t("lavoro.backlog.sprintTip")}>
                    {t("lavoro.backlog.sprintStats", { points: fmtPoints(s.points), done: fmtPoints(s.points_done), closed: s.items_done, total: s.items })}
                  </span>
                  {d.can_manage && (
                    <span className="ml-auto flex gap-2">
                      <button className="text-[12px] text-accent underline" onClick={() => setEditing(s)}>
                        {t("lavoro.backlog.edit")}
                      </button>
                      {s.category === "todo" && (
                        <button className="btn py-0.5 text-[12.5px]" disabled={start.isPending} onClick={() => start.mutate(s)}>
                          {t("lavoro.backlog.startSprint")}
                        </button>
                      )}
                      {s.category === "in_progress" && (
                        <button className="btn py-0.5 text-[12.5px]" onClick={() => setClosing(s)}>
                          {t("lavoro.backlog.closeSprint")}
                        </button>
                      )}
                    </span>
                  )}
                </div>
                {s.goal && <p className="m-0 text-[12.5px] text-muted">{t("lavoro.backlog.goal", { goal: s.goal })}</p>}
                <ItemList list={s.id} items={lists.get(s.id) ?? []} d={d} drop={drop} dnd={dnd} narrow={narrow} destinations={destinations} onMove={moveTo} />
                {d.can_work && s.category !== "done" && <QuickAdd spaceKey={current} sprint={s.key} target={s.title} footer types={d.create_types} />}
              </section>
            ))}
            <section aria-label={t("lavoro.backlog.name")} className="flex flex-col gap-1.5">
              <div className="flex items-baseline gap-3 border-b-2 border-rule pb-1.5">
                <h2 className="m-0 text-[15px] font-bold">{t("lavoro.backlog.name")}</h2>
                <span className="font-mono text-[11.5px] text-muted tabular">
                  {lists.get(BACKLOG)?.length ?? 0}
                  {d.more && d.backlog_total ? ` / ${d.backlog_total}` : ""}
                </span>
                {d.can_work && <QuickAdd spaceKey={current} target={t("dafare.backlog.quickTop")} types={d.create_types} />}
              </div>
              <ItemList list={BACKLOG} items={lists.get(BACKLOG) ?? []} d={d} drop={drop} dnd={dnd} narrow={narrow} destinations={destinations} onMove={moveTo} virtual />
              {d.more && (
                <button className="btn self-start py-1 text-[12.5px]" disabled={data.isFetching} onClick={() => setLimit(limit + BACKLOG_PAGE)}>
                  {t("lavoro.backlog.loadMore", { n: (d.backlog_total ?? 0) - (lists.get(BACKLOG)?.length ?? 0) })}
                </button>
              )}
            </section>
          </div>
          <aside className="flex min-w-0 flex-col gap-3 xl:sticky xl:top-4 xl:self-start" aria-label={t("lavoro.backlog.burndown")}>
            {active ? <BurndownPanel sprintKey={active.key} /> : <p className="m-0 text-[12.5px] text-muted">{t("lavoro.backlog.noActive")}</p>}
            {projectsOn && (
              <Suspense fallback={null}>
                <IncomingWork spaceKey={current} />
              </Suspense>
            )}
          </aside>
        </div>
      )}
      {d?.can_work && <BulkBar ids={[...selection.sel]} space={current} onClear={selection.clear} />}
      {creating && d && (
        <NewCard spaceKey={current} b={{ epics: d.epics, sprint: null, create_types: d.create_types }} sprint={undefined} sprints={d.sprints.filter((s) => s.category !== "done")} onClose={() => setCreating(false)} onCreated={() => refresh()} />
      )}
      {newSprint && <SprintDialog spaceKey={current} onClose={() => setNewSprint(false)} />}
      {editing && <SprintDialog spaceKey={current} sprint={editing} onClose={() => setEditing(null)} />}
      {closing && d && <CloseDialog sprint={closing} others={d.sprints.filter((s) => s.id !== closing.id)} onClose={() => setClosing(null)} />}
    </>
  );
}

function fmtDay(s: string) {
  return new Date(s + "T00:00:00").toLocaleDateString(loc(), { day: "numeric", month: "short" });
}

type Dnd = {
  sel: Set<string>;
  onSelect: (id: string, e: { shiftKey?: boolean }) => void;
  canSelect: boolean;
  canDrag: boolean;
  onTag: (name: string) => void;
  onStart: (it: BacklogItem, e: DragEvent) => void;
  onEnd: () => void;
  onOver: (list: string, index: number) => void;
  onDrop: (list: string, index: number) => void;
};

/** Elenco di uno sprint o del backlog: righe ad altezza fissa, solo quelle visibili nel backlog lungo. */
function ItemList({
  list,
  items,
  d,
  drop,
  dnd,
  narrow,
  destinations,
  onMove,
  virtual,
}: {
  list: string;
  items: BacklogItem[];
  d: BacklogData;
  drop: Drop;
  dnd: Dnd;
  narrow: boolean;
  destinations: { id: string; label: string }[];
  onMove: (it: BacklogItem, list: string, index: number) => void;
  virtual?: boolean;
}) {
  const t = useT();
  const ref = useRef<HTMLDivElement>(null);
  const [scroll, setScroll] = useState(0);
  const [height, setHeight] = useState(600);
  const row = narrow ? ROW_NARROW : ROW;
  const own = !!virtual && items.length * row > 640;
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el || !own) return;
    const ro = new ResizeObserver(() => setHeight(el.clientHeight || 600));
    ro.observe(el);
    return () => ro.disconnect();
  }, [own]);
  const start = own ? Math.max(0, Math.floor(scroll / row) - 6) : 0;
  const end = own ? Math.min(items.length, Math.ceil((scroll + height) / row) + 6) : items.length;
  const indexAt = (e: DragEvent) => {
    const el = ref.current!;
    const y = e.clientY - el.getBoundingClientRect().top + el.scrollTop;
    return Math.max(0, Math.min(items.length, Math.round(y / row)));
  };
  const dropIndex = drop && drop.list === list ? drop.index : -1;
  return (
    <div
      ref={ref}
      onScroll={own ? (e) => setScroll(e.currentTarget.scrollTop) : undefined}
      onDragOver={
        dnd.canDrag
          ? (e) => {
              e.preventDefault();
              dnd.onOver(list, indexAt(e));
            }
          : undefined
      }
      onDrop={
        dnd.canDrag
          ? (e) => {
              e.preventDefault();
              dnd.onDrop(list, indexAt(e));
            }
          : undefined
      }
      className={`relative ${own ? "max-h-[70vh] overflow-y-auto overscroll-contain" : ""} ${dropIndex >= 0 ? "bg-sel/40" : ""}`}
    >
      <ul className="relative m-0 list-none p-0" style={{ height: Math.max(items.length * row, row) }}>
        {items.length === 0 && (
          <li className="flex h-[44px] items-center border border-dashed border-hair px-3 text-[12.5px] text-muted">
            {list === BACKLOG ? t("lavoro.backlog.emptyBacklog") : t("lavoro.backlog.emptySprint")}
          </li>
        )}
        {items.slice(start, end).map((it, i) => (
          <li key={it.id} className="absolute inset-x-0" style={{ top: (start + i) * row, height: row }}>
            <Row
              it={it}
              d={d}
              dnd={dnd}
              narrow={narrow}
              list={list}
              destinations={destinations}
              onMove={onMove}
              position={start + i}
              count={items.length}
            />
          </li>
        ))}
        {dropIndex >= 0 && <li aria-hidden="true" className="pointer-events-none absolute inset-x-0 h-[3px] bg-accent" style={{ top: Math.max(0, dropIndex * row - 1) }} />}
      </ul>
    </div>
  );
}

const Row = memo(function Row({
  it,
  d,
  dnd,
  narrow,
  list,
  destinations,
  onMove,
  position,
  count,
}: {
  it: BacklogItem;
  d: BacklogData;
  dnd: Dnd;
  narrow: boolean;
  list: string;
  destinations: { id: string; label: string }[];
  onMove: (it: BacklogItem, list: string, index: number) => void;
  position: number;
  count: number;
}) {
  const t = useT();
  const stateName = useStateName();
  const prio = d.priorities.find((p) => p.id === it.priority);
  const epic = it.epic ? d.epics[it.epic] : undefined;
  const done = it.category === "done" || it.category === "cancelled";
  return (
    <div
      draggable={dnd.canDrag}
      onDragStart={(e) => dnd.onStart(it, e)}
      onDragEnd={dnd.onEnd}
      aria-selected={dnd.sel.has(it.id) || undefined}
      onClick={(e) => {
        if (dnd.canSelect && (e.ctrlKey || e.metaKey || e.shiftKey) && !(e.target as HTMLElement).closest("a,button,select,input")) dnd.onSelect(it.id, e);
      }}
      className={`flex h-full items-center gap-2.5 border-b border-hair px-1 ${narrow ? "flex-wrap content-center gap-y-1" : ""} ${dnd.canDrag ? "cursor-grab" : ""} ${dnd.sel.has(it.id) ? "bg-sel" : "hover:bg-sel/50"}`}
    >
      {dnd.canSelect && <SelectBox id={it.id} checked={dnd.sel.has(it.id)} label={t("lavoro.backlog.select", { key: it.key })} onToggle={dnd.onSelect} />}
      <TypeMark type={it.type} />
      <Link to="/e/$key" params={{ key: it.key }} className={`shrink-0 font-mono text-[12px] no-underline hover:underline ${done ? "text-muted line-through" : "text-accent"}`}>
        {it.key}
      </Link>
      <span className={`line-clamp-2 min-w-0 leading-[1.25] break-words ${narrow ? "order-last basis-full" : "flex-1"} ${done ? "text-muted" : ""}`} title={it.title}>
        {it.title}
      </span>
      {it.tags && it.tags.length > 0 && <TagChips tags={it.tags} max={narrow ? 1 : 2} onPick={dnd.onTag} className="max-h-[18px] min-w-0 shrink overflow-hidden" />}
      {!narrow && epic && (
        <span className="hidden max-w-[160px] truncate font-mono text-[11px] text-muted lg:inline" title={`${epic.key} ${epic.title}`}>
          {epic.key}
        </span>
      )}
      {!narrow && <span className="w-[86px] shrink-0 truncate text-[12px] text-muted">{stateName(it.state)}</span>}
      <PriorityDot opt={prio} />
      <span className={`w-8 shrink-0 text-center font-mono text-[11.5px] tabular ${it.points != null ? "tinta m-work text-fg" : "text-muted"}`}>{it.points != null ? fmtPoints(it.points) : ""}</span>
      <Person name={it.assignee ? d.members[it.assignee] : undefined} />
      {d.can_work && (
        <Select
          aria-label={t("lavoro.backlog.move", { key: it.key })}
          className={`field shrink-0 cursor-pointer appearance-none px-0 py-0.5 text-center text-[13px] ${narrow ? "h-[34px] w-[48px]" : "w-[48px]"}`}
          value=""
          onChange={(e) => {
            const v = e.target.value;
            if (v === "^") onMove(it, list, 0);
            else if (v === "v") onMove(it, list, count);
            else if (v) onMove(it, v, 0);
          }}
        >
          <option value="">⋯</option>
          {position > 0 && <option value="^">{t("lavoro.backlog.top")}</option>}
          {position < count - 1 && <option value="v">{t("lavoro.backlog.bottom")}</option>}
          {destinations
            .filter((x) => x.id !== list)
            .map((x) => (
              <option key={x.id} value={x.id}>
                {t("lavoro.backlog.moveTo", { label: x.label })}
              </option>
            ))}
        </Select>
      )}
    </div>
  );
});

/**
 * Creazione rapida in linea (ondata 2): tipo, titolo e Invio. In testa al backlog crea nel backlog; in fondo a
 * ogni sprint (`sprint`) crea già dentro lo sprint, senza trascinare l'elemento dopo.
 */
function QuickAdd({ spaceKey, sprint, target, footer, types }: { spaceKey: string; sprint?: string; target: string; footer?: boolean; types: CreateType[] }) {
  const t = useT();
  const tn = useTypeNames();
  const qc = useQueryClient();
  const [title, setTitle] = useState("");
  // Il rapido propone i tipi della board senza l'epic (che non sta negli sprint); il primo è quello di partenza.
  const quick = types.filter((c) => c.key !== "epic");
  const [picked, setType] = useState("");
  const type = quick.some((c) => c.key === picked) ? picked : (quick[0]?.key ?? "story");
  const [done, setDone] = useState("");
  const add = useMutation({
    mutationFn: () => workApi.createItem(spaceKey, { type, title: title.trim(), sprint }),
    onSuccess: (el) => {
      setTitle("");
      setDone(t("dafare.backlog.quickDone", { key: el.key }));
      qc.invalidateQueries({ queryKey: ["work"] });
    },
  });
  return (
    <form
      className={footer ? "flex min-w-0 flex-wrap items-center gap-1.5 pt-1.5" : "ml-auto flex min-w-0 flex-[0_1_420px] items-center gap-1.5"}
      onSubmit={(e) => {
        e.preventDefault();
        if (title.trim()) add.mutate();
      }}
    >
      <Select aria-label={t("lavoro.backlog.type")} className="field w-auto py-1 text-[12.5px]" value={type} onChange={(e) => setType(e.target.value)}>
        {quick.map((c) => (
          <option key={c.key} value={c.key}>
            {tn.name(c.key, c.name)}
          </option>
        ))}
      </Select>
      <input
        aria-label={t("dafare.backlog.quickAria", { target })}
        className="field min-w-0 flex-1 py-1 text-[12.5px]"
        placeholder={t("dafare.backlog.quickPlaceholder")}
        value={title}
        onChange={(e) => {
          setTitle(e.target.value);
          setDone("");
        }}
      />
      {footer && (
        <button className="btn py-1 text-[12.5px]" disabled={add.isPending || !title.trim()}>
          {t("dafare.backlog.quickAdd")}
        </button>
      )}
      {done && <span role="status" className="text-[12px] text-ok">{done}</span>}
      {add.error && <span className="text-[12px] text-danger">{add.error instanceof ApiError ? add.error.message : t("lavoro.backlog.error")}</span>}
    </form>
  );
}

function SprintDialog({ spaceKey, sprint, onClose }: { spaceKey: string; sprint?: SprintView; onClose: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const [title, setTitle] = useState(sprint?.title ?? "");
  const [goal, setGoal] = useState(sprint?.goal ?? "");
  const [start, setStart] = useState(sprint?.start ?? "");
  const [end, setEnd] = useState(sprint?.end ?? "");
  const save = useMutation({
    mutationFn: () =>
      sprint ? workApi.updateSprint(sprint.key, { title, goal, start, end }) : workApi.createSprint(spaceKey, { title, goal, start, end }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["work"] });
      onClose();
    },
  });
  return (
    <Dialog title={sprint ? t("lavoro.backlog.editTitle", { title: sprint.title }) : t("lavoro.backlog.newSprint")} onClose={onClose}>
      <form
        className="flex flex-col gap-3"
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate();
        }}
      >
        <label className="flex flex-col gap-1">
          <span className="label">{t("lavoro.backlog.sprintName")}</span>
          <input className="field" placeholder={t("lavoro.backlog.sprintExample")} required={!!sprint} maxLength={200} value={title} onChange={(e) => setTitle(e.target.value)} />
        </label>
        <label className="flex flex-col gap-1">
          <span className="label text-muted">{t("lavoro.backlog.goalLabel")}</span>
          <textarea className="field min-h-16" value={goal} onChange={(e) => setGoal(e.target.value)} />
        </label>
        <div className="grid grid-cols-2 gap-2">
          <label className="flex flex-col gap-1">
            <span className="label text-muted">{t("lavoro.backlog.start")}</span>
            <input className="field" type="date" value={start} onChange={(e) => setStart(e.target.value)} />
          </label>
          <label className="flex flex-col gap-1">
            <span className="label text-muted">{t("lavoro.backlog.end")}</span>
            <input className="field" type="date" min={start || undefined} value={end} onChange={(e) => setEnd(e.target.value)} />
          </label>
        </div>
        <ErrorNote error={save.error} />
        <button className="btn btn-primary self-start" disabled={save.isPending}>
          {sprint ? t("lavoro.backlog.save") : t("lavoro.backlog.createSprint")}
        </button>
      </form>
    </Dialog>
  );
}

function CloseDialog({ sprint, others, onClose }: { sprint: SprintView; others: SprintView[]; onClose: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const open = sprint.items - sprint.items_done;
  const planned = others.filter((s) => s.category === "todo");
  const [dest, setDest] = useState(planned[0]?.key ?? "backlog");
  const close = useMutation({
    mutationFn: () => workApi.closeSprint(sprint.key, dest),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["work"] });
      onClose();
    },
  });
  return (
    <Dialog title={t("lavoro.backlog.closeTitle", { title: sprint.title })} onClose={onClose}>
      <p className="m-0">
        {open === 0 ? t("lavoro.backlog.allClosed") : t("lavoro.backlog.openWhere", { count: open })}
      </p>
      {open > 0 && (
        <fieldset className="m-0 flex flex-col gap-1.5 border-0 p-0">
          <legend className="sr-only">{t("lavoro.backlog.destination")}</legend>
          {planned.map((s) => (
            <label key={s.id} className="flex items-center gap-2">
              <input type="radio" name="dest" checked={dest === s.key} onChange={() => setDest(s.key)} />
              {s.title}
            </label>
          ))}
          <label className="flex items-center gap-2">
            <input type="radio" name="dest" checked={dest === "backlog"} onChange={() => setDest("backlog")} />
            {t("lavoro.backlog.backToBacklog")}
          </label>
        </fieldset>
      )}
      <ErrorNote error={close.error} />
      <button className="btn btn-primary self-start" disabled={close.isPending} onClick={() => close.mutate()}>
        {t("lavoro.backlog.closeSprint")}
      </button>
    </Dialog>
  );
}

// ---------------------------------------------------------------------
// Burndown

export function BurndownPanel({ sprintKey }: { sprintKey: string }) {
  const bd = useQuery({ queryKey: ["work", "burndown", sprintKey], queryFn: () => workApi.burndown(sprintKey) });
  if (bd.error) return <ErrorNote error={bd.error} />;
  if (!bd.data) return <Loading className="m-0 text-muted" />;
  return <BurndownChart b={bd.data} />;
}

const DAY = 86_400_000;
const dayNum = (s: string) => Date.parse(s + "T00:00:00Z") / DAY;

export function BurndownChart({ b }: { b: Burndown }) {
  const t = useT();
  const unit = b.unit === "points" ? t("lavoro.backlog.unitPoints") : t("lavoro.backlog.unitItems");
  const W = 360;
  const H = 180;
  const pad = { l: 34, r: 10, t: 12, b: 26 };
  const x0 = dayNum(b.start);
  const x1 = Math.max(dayNum(b.end), x0 + 1);
  const ymax = Math.max(1, b.committed, b.scope, ...b.actual.map((p) => p.value));
  const sx = (d: number) => pad.l + ((d - x0) / (x1 - x0)) * (W - pad.l - pad.r);
  const sy = (v: number) => pad.t + (1 - v / ymax) * (H - pad.t - pad.b);
  const ideal = b.ideal.map((p) => `${sx(dayNum(p.day))},${sy(p.value)}`).join(" ");
  // Gradini: il valore resta fino al giorno dopo.
  const steps: string[] = [];
  b.actual.forEach((p, i) => {
    const d = dayNum(p.day);
    if (i > 0) steps.push(`${sx(d)},${sy(b.actual[i - 1].value)}`);
    steps.push(`${sx(d)},${sy(p.value)}`);
  });
  if (b.actual.length) {
    const last = b.actual[b.actual.length - 1];
    steps.push(`${sx(Math.min(dayNum(last.day) + 1, x1))},${sy(last.value)}`);
  }
  const todayX = sx(Math.floor(Date.now() / DAY));
  const summary = t("lavoro.backlog.summary", { title: b.sprint.title, remaining: fmtPoints(b.remaining), unit, scope: fmtPoints(b.scope), committed: fmtPoints(b.committed) });
  return (
    <section className="flex flex-col gap-2" aria-label={t("lavoro.backlog.burndownSprint")}>
      <div className="flex items-baseline justify-between border-b-2 border-rule pb-1.5">
        <h3 className="label m-0">{t("lavoro.backlog.burndownOf", { title: b.sprint.title })}</h3>
        <span className="font-mono text-[11.5px] text-muted tabular">{unit}</span>
      </div>
      {b.goal && <p className="m-0 text-[12.5px] text-muted">{t("lavoro.backlog.goal", { goal: b.goal })}</p>}
      <svg viewBox={`0 0 ${W} ${H}`} className="h-auto w-full" role="img" aria-label={summary}>
        {[0, 0.5, 1].map((f) => (
          <g key={f}>
            <line x1={pad.l} x2={W - pad.r} y1={sy(ymax * f)} y2={sy(ymax * f)} stroke="var(--hair)" strokeWidth="1" />
            <text x={pad.l - 6} y={sy(ymax * f) + 4} textAnchor="end" fontSize="10" fill="var(--muted)" className="font-mono">
              {fmtPoints(Math.round(ymax * f * 10) / 10)}
            </text>
          </g>
        ))}
        {todayX > pad.l && todayX < W - pad.r && !b.closed && (
          <line x1={todayX} x2={todayX} y1={pad.t} y2={H - pad.b} stroke="var(--flag)" strokeWidth="1" strokeDasharray="2 3" />
        )}
        <polyline points={ideal} fill="none" stroke="var(--muted)" strokeWidth="1.5" strokeDasharray="5 4" />
        {steps.length > 0 && <polyline points={steps.join(" ")} fill="none" stroke="var(--accent)" strokeWidth="2.25" strokeLinejoin="round" />}
        <text x={pad.l} y={H - 8} fontSize="10" fill="var(--muted)" className="font-mono">
          {fmtDay(b.start)}
        </text>
        <text x={W - pad.r} y={H - 8} fontSize="10" fill="var(--muted)" textAnchor="end" className="font-mono">
          {fmtDay(b.end)}
        </text>
      </svg>
      <p className="m-0 flex flex-wrap gap-x-4 text-[12.5px]">
        <span>
          <Trans k="lavoro.backlog.remaining" slots={{ n: <span className="font-mono font-semibold tabular">{fmtPoints(b.remaining)}</span> }} />
        </span>
        <span className="text-muted">
          <Trans k="lavoro.backlog.completed" slots={{ n: <span className="font-mono tabular">{fmtPoints(b.completed)}</span> }} />
        </span>
        <span className="text-muted">
          <Trans k="lavoro.backlog.committed" slots={{ n: <span className="font-mono tabular">{fmtPoints(b.committed)}</span> }} />
        </span>
        {b.scope > b.committed && (
          <span className="text-flag">
            <Trans k="lavoro.backlog.added" slots={{ n: <span className="font-mono tabular">{fmtPoints(b.scope - b.committed)}</span> }} />
          </span>
        )}
      </p>
      <p className="m-0 flex items-center gap-3 text-[11.5px] text-muted" aria-hidden="true">
        <span className="inline-flex items-center gap-1">
          <span className="inline-block h-[2px] w-4 bg-accent" /> {t("lavoro.backlog.actual")}
        </span>
        <span className="inline-flex items-center gap-1">
          <span className="inline-block w-4 border-t border-dashed border-muted" /> {t("lavoro.backlog.ideal")}
        </span>
      </p>
      <details className="text-[12px]">
        <summary className="cursor-pointer text-muted">{t("lavoro.backlog.daily")}</summary>
        <table className="mt-1 w-full border-collapse">
          <tbody>
            {b.actual.map((p) => (
              <tr key={p.day} className="border-b border-hair">
                <td className="py-0.5">{fmtDay(p.day)}</td>
                <td className="text-right font-mono tabular">{fmtPoints(p.value)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </details>
    </section>
  );
}
