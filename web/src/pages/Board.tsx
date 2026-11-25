// Board del team (piano, passo 18; ricognizione S05-S06). Colonne = fasi del processo con il
// limite WIP (evidenziato quando è superato), swimlane per assegnatario, epic o priorità,
// card nell'ordine manuale. Trascinando una card si cambia fase (secondo il processo: se un
// gate o un ruolo lo impedisce la card torna al suo posto e si vede il motivo), posizione e
// swimlane. Ogni cella disegna solo le card visibili (altezza fissa), così la board resta
// fluida anche con migliaia di card. Sotto i 768 px una colonna alla volta, a scatti, e le
// card si spostano dal loro menu.
import { EntitySelect, Select } from "../select";
import { EmptyState } from "../emptyState";
import { memo, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type DragEvent, type ReactNode } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError } from "../api";
import { PageHeader, useMe } from "../shell";
import { QueryBar, withLabel } from "../query";
import { TagChips } from "../tags";
import { ErrorNote, Loading } from "../ui";
import { useNarrow } from "./timeline";
import { BulkBar, SelectBox, useSelection } from "./BulkBar";
import { Dialog, MineChip, Person, PriorityDot, ReadOnlyBanner, SpacePicker, TimeLogForm, TypeMark, useSpaces } from "./work-ui";
import { fmtMinutes, fmtPoints, lastSprint, rememberSprint, workApi, type Board as BoardData, type BoardSettings, type Card, type MoveInput, type Swimlane } from "../workApi";
import { useTypeNames } from "../typesApi";
import { useT } from "../i18n/react";
import { useStateName } from "../stateLabels";
import { tr, type Key } from "../i18n/core";
import { crumbs } from "../crumbs";

export type BoardSearch = { spazio?: string; sprint?: string; q?: string; corsie?: Swimlane };

const ROW = 96; // altezza di una card (88) più lo spazio tra le card
// Con le etichette sulle card la riga di chip aggiunge 22 px a ogni card (altezza uniforme per la virtualizzazione).
const TAG_H = 22;
const FOOT_H = 44; // pulsante «Mostra altre» in fondo alla colonna
const SWIMLANES: [Swimlane, Key][] = [
  ["none", "lavoro.board.laneNone"],
  ["assignee", "lavoro.board.laneAssignee"],
  ["epic", "lavoro.board.laneEpic"],
  ["priority", "lavoro.board.lanePriority"],
];

const cellKey = (lane: string, col: number) => `${lane}\u0000${col}`;

type Pending = { card: Card; col: number; lane: string; index: number; comment?: string };
type Notice = { tone: "error" | "ok"; text: string } | null;

export function Board({ search, onSearch }: { search: BoardSearch; onSearch: (s: BoardSearch) => void }) {
  const t = useT();
  const tn = useTypeNames();
  const qc = useQueryClient();
  const narrow = useNarrow();
  const { spaces, items, current } = useSpaces(search.spazio);
  const [draft, setDraft] = useState(search.q ?? "");
  useEffect(() => setDraft(search.q ?? ""), [search.q]);
  // Lo sprint scelto l'ultima volta per questo team: senza uno nell'indirizzo, la board riparte da lì.
  const last = lastSprint(current);
  const remembered = !search.sprint && last && last !== "attivo" ? last : undefined;
  const sprintSel = search.sprint ?? remembered;
  const key = ["work", "board", current, sprintSel ?? "", search.q ?? "", search.corsie ?? ""] as const;
  const board = useQuery({
    queryKey: key,
    queryFn: () => workApi.board(current!, { q: search.q, sprint: sprintSel, swimlane: search.corsie }),
    enabled: !!current,
    placeholderData: (prev) => prev,
  });
  // Uno sprint ricordato che non c'è più (chiuso, cancellato) non deve rompere la board.
  useEffect(() => {
    if (remembered && board.error) rememberSprint(current, undefined);
  }, [remembered, board.error, current]);
  const [notice, setNotice] = useState<Notice>(null);
  const [menu, setMenu] = useState<Card | null>(null);
  const [askComment, setAskComment] = useState<Pending | null>(null);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [creating, setCreating] = useState(false);
  // Selezione multipla per i comandi in blocco (Maiusc, Ctrl/Cmd o la casella; sul telefono «Seleziona»).
  const order = useMemo(() => (board.data?.cards ?? []).map((c) => c.id), [board.data?.cards]);
  const selection = useSelection(order);
  const [selectMode, setSelectMode] = useState(false);
  useEffect(() => {
    if (!notice) return;
    const timer = setTimeout(() => setNotice(null), notice.tone === "error" ? 9000 : 3500);
    return () => clearTimeout(timer);
  }, [notice]);

  const b = board.data;
  const swimlane: Swimlane = (search.corsie ?? b?.settings.swimlane ?? "none") as Swimlane;

  // Celle (swimlane × colonna) nell'ordine manuale delle card.
  const cells = useMemo(() => {
    const m = new Map<string, Card[]>();
    for (const c of b?.cards ?? []) {
      const k = cellKey(swimlane === "none" ? "" : (c.lane ?? ""), c.col);
      let list = m.get(k);
      if (!list) m.set(k, (list = []));
      list.push(c);
    }
    return m;
  }, [b?.cards, swimlane]);
  // Card caricate per colonna e totali: con la board paginata i totali sono quelli esatti del server.
  const loaded = useMemo(() => {
    const n = new Array(b?.columns.length ?? 0).fill(0) as number[];
    for (const c of b?.cards ?? []) n[c.col]++;
    return n;
  }, [b?.cards, b?.columns.length]);
  const counts = useMemo(() => (b?.paged ? b.columns.map((c) => c.count) : loaded), [b?.paged, b?.columns, loaded]);
  const hasTags = useMemo(() => (b?.cards ?? []).some((c) => !!c.tags?.length), [b?.cards]);
  const row = ROW + (hasTags ? TAG_H : 0);
  const [loadingCol, setLoadingCol] = useState<number | null>(null);
  // «Mostra altre»: le card successive della colonna, aggiunte in coda alla cache (senza doppioni).
  const loadMore = useCallback(
    async (col: number) => {
      if (!b || !current || loadingCol != null) return;
      const last = [...b.cards].reverse().find((c) => c.col === col);
      setLoadingCol(col);
      try {
        const r = await workApi.board(current, { q: search.q, sprint: sprintSel, swimlane: search.corsie, col, after: last?.id });
        qc.setQueryData<BoardData>(key, (old) => {
          if (!old) return old;
          const have = new Set(old.cards.map((c) => c.id));
          return { ...old, cards: [...old.cards, ...r.cards.filter((c) => !have.has(c.id))], members: { ...old.members, ...r.members }, epics: { ...old.epics, ...r.epics }, columns: r.columns };
        });
      } catch (e) {
        setNotice({ tone: "error", text: t("lavoro.board.moreFailed", { reason: reason(e) }) });
        qc.invalidateQueries({ queryKey: ["work", "board", current] });
      } finally {
        setLoadingCol(null);
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [b, current, loadingCol, search.q, search.corsie, sprintSel, qc, key],
  );
  const pickTag = useCallback((name: string) => onSearch({ ...search, spazio: current, q: withLabel(search.q ?? "", name) }), [onSearch, search, current]);

  const move = useMutation({
    mutationFn: (m: MoveInput) => workApi.move(current!, m),
  });

  /** Sposta una card nella cella (lane, col) alla posizione index (senza contare la card). */
  const doMove = useCallback(
    (p: Pending) => {
      if (!b || !current) return;
      const laneChanged = swimlane !== "none" && (p.card.lane ?? "") !== p.lane;
      const colChanged = p.card.col !== p.col;
      if (!laneChanged && !colChanged) {
        // Stessa cella: l'indice arriva contando anche la card trascinata.
        const all = cells.get(cellKey(p.lane, p.col)) ?? [];
        const i = all.findIndex((c) => c.id === p.card.id);
        if (i >= 0 && p.index > i) p = { ...p, index: p.index - 1 };
        if (i === p.index) return;
      }
      const target = (cells.get(cellKey(p.lane, p.col)) ?? []).filter((c) => c.id !== p.card.id);
      const after = target[p.index - 1]?.id;
      const before = target[p.index]?.id;
      const snapshot = qc.getQueryData<BoardData>(key);
      qc.setQueryData<BoardData>(key, (old) => (old ? applyMove(old, p.card, p.col, p.lane, swimlane, after, before) : old));
      move.mutate(
        {
          element: p.card.id,
          column: colChanged ? p.col : undefined,
          lane: laneChanged ? p.lane : undefined,
          swimlane: laneChanged ? swimlane : undefined,
          after,
          before,
          comment: p.comment,
        },
        {
          onSuccess: (r) => {
            if (colChanged) setNotice({ tone: "ok", text: `${p.card.key} → ${r.phase}` });
          },
          onError: (e) => {
            qc.setQueryData(key, snapshot); // la card torna al suo posto
            if (e instanceof ApiError && e.field === "comment" && !p.comment) {
              setAskComment(p);
              return;
            }
            setNotice({ tone: "error", text: t("lavoro.board.notMoved", { key: p.card.key, reason: reason(e) }) });
          },
          // La board paginata tiene le pagine già caricate: si ricarica solo se lo spostamento fallisce.
          onSettled: (_r, err) => {
            if (!b.paged || err) qc.invalidateQueries({ queryKey: ["work", "board", current] });
          },
        },
      );
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [b, cells, current, swimlane, qc, move],
  );

  if (spaces.isPending) return <Loading className="px-4 py-6 text-muted md:px-7" />;
  if (spaces.error) return <div className="px-4 py-6 md:px-7"><ErrorNote error={spaces.error} /></div>;
  if (!current) return <NoSpaces />;

  const lanes = swimlane === "none" || !b ? [{ key: "", name: "" }] : b.lanes;
  const canDrag = !!b?.can_work && !narrow;
  return (
    <div className="flex h-[calc(100dvh-49px)] min-h-0 flex-col md:h-dvh">
      <PageHeader
        path={`${crumbs("delivery", "board")} / ${current}`}
        title={b ? t("lavoro.board.titleOf", { name: b.team ?? b.space.title }) : t("lavoro.board.title")}
        actions={
          <>
            {b?.can_work && (
              <button className="btn btn-primary" onClick={() => setCreating(true)}>
                {t("lavoro.board.newItem")}
              </button>
            )}
            {b?.can_work && (
              <button
                className="btn"
                aria-pressed={selectMode}
                onClick={() => {
                  if (selectMode) selection.clear();
                  setSelectMode(!selectMode);
                }}
              >
                {selectMode ? t("lavoro.board.endSelect") : t("lavoro.board.select")}
              </button>
            )}
            {b?.can_manage && (
              <button className="btn" onClick={() => setSettingsOpen(true)}>
                {t("lavoro.board.settings")}
              </button>
            )}
          </>
        }
      />
      <div className="flex flex-wrap items-end gap-x-5 gap-y-2 px-4 pb-3 md:px-7">
        <SpacePicker value={current} items={items} onChange={(k) => onSearch({ spazio: k, sprint: undefined, q: search.q, corsie: search.corsie })} />
        {!b?.settings.no_sprint && (
        <label className="flex items-center gap-2">
          <span className="label text-muted">{t("lavoro.board.sprint")}</span>
          <Select
            className="field w-auto max-w-[220px] py-1.5"
            value={sprintSel ?? (b?.sprint ? "" : "tutti")}
            onChange={(e) => {
              rememberSprint(current, e.target.value || "attivo");
              onSearch({ ...search, spazio: current, sprint: e.target.value || undefined });
            }}
          >
            <option value="">{b?.sprints.some((s) => s.category === "in_progress") ? t("lavoro.board.activeSprint") : t("lavoro.board.wholeSpace")}</option>
            <option value="tutti">{t("lavoro.board.wholeSpace")}</option>
            {b?.sprints.map((s) => (
              <option key={s.id} value={s.key}>
                {s.title} ({s.state.toLowerCase()})
              </option>
            ))}
          </Select>
        </label>
        )}
        <label className="flex items-center gap-2">
          <span className="label text-muted">{t("lavoro.board.swimlane")}</span>
          <Select className="field w-auto py-1.5" value={swimlane} onChange={(e) => onSearch({ ...search, spazio: current, corsie: e.target.value as Swimlane })}>
            {SWIMLANES.map(([v, l]) => (
              <option key={v} value={v}>
                {v === "epic" ? tn.name("epic", t(l)) : t(l)}
              </option>
            ))}
          </Select>
        </label>
        <Link to="/delivery/backlog" search={{ spazio: current }} className="pb-1.5 text-[12.5px] text-accent underline">
          {t("lavoro.board.backlog")}
        </Link>
        {b && (
          <span className="ml-auto pb-1.5 font-mono text-[11.5px] text-muted tabular">
            {t("lavoro.board.items", { count: b.paged ? counts.reduce((s, n) => s + n, 0) : b.cards.length })}{b.hidden ? ` · ${t("lavoro.board.hidden", { count: b.hidden })}` : ""}
            {b.truncated ? t("lavoro.board.tooMany") : ""}
          </span>
        )}
      </div>
      <QueryBar
        value={draft}
        onChange={setDraft}
        onSubmit={(v) => onSearch({ ...search, spazio: current, q: v.trim() || undefined })}
        submitted={{ query: search.q ?? "", error: board.error }}
        label={t("lavoro.board.filter")}
      />
      <div className="flex flex-wrap items-center gap-2 px-4 pt-2 pb-1 md:px-7">
        <MineChip q={search.q} onChange={(q) => onSearch({ ...search, spazio: current, q })} />
      </div>
      {b && !b.can_work && <ReadOnlyBanner what={t("lavoro.board.readOnly")} />}
      {board.error &&!(board.error instanceof ApiError && board.error.span) && (
        <div className="px-4 pt-3 md:px-7">
          <ErrorNote error={board.error} />
        </div>
      )}
      {b && (
        <BoardGrid b={b} lanes={lanes} cells={cells} counts={counts} loaded={loaded} row={row} loadingCol={loadingCol} onMore={loadMore} onTag={pickTag} swimlane={swimlane} narrow={narrow} canDrag={canDrag} onMove={doMove} onMenu={setMenu}
          sel={selection.sel} selectMode={selectMode} canSelect={b.can_work} onSelect={selection.toggle} />
      )}
      {b?.can_work && <BulkBar ids={[...selection.sel]} space={current} onClear={selection.clear} />}
      {notice && (
        <div
          role={notice.tone === "error" ? "alert" : "status"}
          className={`fixed right-4 bottom-4 left-4 z-[70] flex items-start gap-3 border-2 bg-bg px-3 py-2.5 text-[13px] md:left-auto md:max-w-[440px] ${notice.tone === "error" ? "border-danger" : "border-rule"}`}
        >
          <span className={`flex-1 ${notice.tone === "error" ? "text-danger" : ""}`}>{notice.text}</span>
          <button className="text-[12px] text-muted underline" onClick={() => setNotice(null)}>
            {t("lavoro.board.close")}
          </button>
        </div>
      )}
      {menu && b && (
        <CardMenu
          card={menu}
          b={b}
          onClose={() => setMenu(null)}
          onMove={(col) => {
            const target = cells.get(cellKey(swimlane === "none" ? "" : (menu.lane ?? ""), col)) ?? [];
            doMove({ card: menu, col, lane: swimlane === "none" ? "" : (menu.lane ?? ""), index: target.length });
            setMenu(null);
          }}
        />
      )}
      {askComment && (
        <Dialog title={t("lavoro.board.reasonFor", { key: askComment.card.key })} onClose={() => setAskComment(null)}>
          <CommentForm
            phase={b?.columns[askComment.col]?.name ?? ""}
            onSubmit={(comment) => {
              doMove({ ...askComment, comment });
              setAskComment(null);
            }}
          />
        </Dialog>
      )}
      {settingsOpen && b && <SettingsDialog spaceKey={current} b={b} onClose={() => setSettingsOpen(false)} />}
      {creating && b && <NewCard spaceKey={current} b={b} onClose={() => setCreating(false)} onCreated={(k) => setNotice({ tone: "ok", text: t("lavoro.board.created", { key: k }) })} />}
    </div>
  );
}

/** Motivo leggibile di un rifiuto: senza il nome tecnico del campo ("gate: …", "column: …"). */
function reason(e: unknown): string {
  if (!(e instanceof ApiError)) return tr("lavoro.board.networkError");
  if (e.status === 403) return tr("lavoro.board.forbidden");
  return e.field && e.message.startsWith(e.field + ": ") ? e.message.slice(e.field.length + 2) : e.message;
}

/** Applica lo spostamento ai dati in cache (ottimistico): fase, swimlane e posizione. */
function applyMove(b: BoardData, card: Card, col: number, lane: string, swimlane: Swimlane, after?: string, before?: string): BoardData {
  const cards = b.cards.filter((c) => c.id !== card.id);
  const moved: Card = { ...card, col, state: col !== card.col ? (b.columns[col]?.phases[0] ?? card.state) : card.state };
  if (swimlane !== "none") {
    moved.lane = lane;
    if (swimlane === "assignee") moved.assignee = lane || undefined;
    if (swimlane === "priority") moved.priority = lane || undefined;
    if (swimlane === "epic") moved.epic = lane || undefined;
  }
  let at = cards.length;
  if (before) at = cards.findIndex((c) => c.id === before);
  else if (after) at = cards.findIndex((c) => c.id === after) + 1;
  if (at < 0) at = cards.length;
  cards.splice(at, 0, moved);
  // Board paginata: i totali delle colonne seguono lo spostamento (le card non caricate non si toccano).
  const columns = b.paged && col !== card.col ? b.columns.map((c, i) => (i === card.col ? { ...c, count: c.count - 1 } : i === col ? { ...c, count: c.count + 1 } : c)) : b.columns;
  return { ...b, cards, columns };
}

function NoSpaces() {
  const t = useT();
  return (
    <>
      <PageHeader path={crumbs("delivery", "board")} title={t("lavoro.board.teamsTitle")} />
      <div className="px-4 md:px-7">
        <EmptyState
          title={t("lavoro.board.noSpace")}
          text={t("lavoro.board.noSpaceText")}
          action={<Link to="/architettura" search={{ tab: "team" }} className="btn no-underline">{t("lavoro.board.goTeams")}</Link>}
        />
      </div>
    </>
  );
}

// ---------------------------------------------------------------------
// Griglia

type GridProps = {
  b: BoardData;
  lanes: { key: string; name: string; color?: string }[];
  cells: Map<string, Card[]>;
  counts: number[];
  /** Card caricate per colonna (con la board paginata sono meno dei totali in counts). */
  loaded: number[];
  row: number;
  loadingCol: number | null;
  onMore: (col: number) => void;
  onTag: (name: string) => void;
  swimlane: Swimlane;
  narrow: boolean;
  canDrag: boolean;
  onMove: (p: Pending) => void;
  onMenu: (c: Card) => void;
  sel: Set<string>;
  selectMode: boolean;
  canSelect: boolean;
  onSelect: (id: string, e: { shiftKey?: boolean }) => void;
};

function BoardGrid({ b, lanes, cells, counts, loaded, row, loadingCol, onMore, onTag, swimlane, narrow, canDrag, onMove, onMenu, sel, selectMode, canSelect, onSelect }: GridProps) {
  const t = useT();
  const stateName = useStateName();
  const scroller = useRef<HTMLDivElement>(null);
  const [activeCol, setActiveCol] = useState(0);
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({});
  const dragged = useRef<Card | null>(null);
  const [drop, setDrop] = useState<{ cell: string; index: number } | null>(null);
  const colW = narrow ? Math.round(window.innerWidth * 0.86) : 276;
  const single = swimlane === "none";

  // Sul telefono: la colonna visibile (scorrimento a scatti) e i pulsanti per saltare.
  const onScroll = () => {
    const el = scroller.current;
    if (el && narrow) setActiveCol(Math.round(el.scrollLeft / (colW + 12)));
  };
  const goTo = (i: number) => scroller.current?.scrollTo({ left: i * (colW + 12), behavior: "smooth" });

  // Sul telefono la lavagna si apre sulla prima colonna che ha schede, non su una colonna vuota
  // («Da fare 0»); una volta per team e sprint, poi resta dove l'ha messa chi la usa.
  const opened = useRef("");
  const firstFull = counts.findIndex((n) => n > 0);
  useLayoutEffect(() => {
    const id = `${b.space.key}/${b.sprint?.key ?? ""}`;
    if (!narrow || opened.current === id || firstFull < 0) return;
    opened.current = id;
    if (firstFull > 0) {
      scroller.current?.scrollTo({ left: firstFull * (colW + 12) });
      setActiveCol(firstFull);
    }
  }, [narrow, firstFull, colW, b.space.key, b.sprint?.key]);

  const startDrag = useCallback((c: Card, e: DragEvent) => {
    dragged.current = c;
    e.dataTransfer.effectAllowed = "move";
    e.dataTransfer.setData("text/plain", c.key);
  }, []);
  const endDrag = useCallback(() => {
    dragged.current = null;
    setDrop(null);
  }, []);
  const over = useCallback((cell: string, index: number) => {
    setDrop((d) => (d && d.cell === cell && d.index === index ? d : { cell, index }));
  }, []);
  const dropOn = useCallback(
    (lane: string, col: number, index: number) => {
      const c = dragged.current;
      dragged.current = null;
      setDrop(null);
      if (c) onMove({ card: c, col, lane, index });
    },
    [onMove],
  );

  const remaining = (i: number) => (b.paged ? Math.max(0, (counts[i] ?? 0) - (loaded[i] ?? 0)) : 0);
  const header = (
    <div className="flex gap-3" style={{ minWidth: "min-content" }}>
      {b.columns.map((col, i) => {
        const n = counts[i] ?? 0;
        const overWip = col.wip > 0 && n > col.wip;
        return (
          <div
            key={col.name}
            className={`flex shrink-0 snap-start items-baseline gap-2 border-b-2 pb-1.5 ${overWip ? "border-flag" : "border-rule"}`}
            style={{ width: colW, ...(col.color && !overWip ? { borderColor: col.color } : {}) }}
          >
            <span className="label truncate">{stateName(col.name)}</span>
            <span className={`font-mono text-[11.5px] tabular ${overWip ? "font-semibold text-flag" : "text-muted"}`}>
              {n}
              {col.wip > 0 && ` / ${col.wip}`}
            </span>
            {overWip && (
              <span data-testid="wip-superato" className="ml-auto text-[11px] font-semibold text-flag">
                {t("lavoro.boardTeam.overWip", { n, wip: col.wip })}
              </span>
            )}
            {!single && remaining(i) > 0 && (
              <MoreButton name={stateName(col.name)} count={remaining(i)} loading={loadingCol === i} onClick={() => onMore(i)} className="ml-auto" />
            )}
          </div>
        );
      })}
    </div>
  );

  return (
    <div className="flex min-h-0 flex-1 flex-col pt-3">
      {narrow && (
        <div role="tablist" aria-label={t("lavoro.board.columns")} className="flex gap-1 overflow-x-auto px-4 pb-2">
          {b.columns.map((c, i) => (
            <button
              key={c.name}
              role="tab"
              aria-selected={activeCol === i}
              className={`shrink-0 border px-2 py-1 text-[12px] ${activeCol === i ? "border-fg bg-fg text-bg" : "border-hair text-muted"}`}
              onClick={() => goTo(i)}
            >
              {stateName(c.name)} <span className="font-mono tabular">{counts[i] ?? 0}</span>
            </button>
          ))}
        </div>
      )}
      <div
        ref={scroller}
        onScroll={onScroll}
        className={`min-h-0 flex-1 px-4 pb-4 md:px-7 ${single ? "flex flex-col overflow-x-auto overflow-y-hidden" : "overflow-auto"} ${narrow ? "snap-x snap-mandatory" : ""}`}
        style={narrow ? { scrollPaddingLeft: 16 } : undefined}
      >
        <div className="sticky top-0 z-20 w-max min-w-full shrink-0 bg-bg pb-2">{header}</div>
        {lanes.map((lane) => {
          const total = b.columns.reduce((s, _c, i) => s + (cells.get(cellKey(lane.key, i))?.length ?? 0), 0);
          const closed = !!collapsed[lane.key];
          return (
            <section
              key={lane.key || "∅"}
              aria-label={single ? t("lavoro.board.itemsLabel") : t("lavoro.board.swimlaneOf", { name: lane.name })}
              className={single ? "flex min-h-0 w-max min-w-full flex-1 flex-col" : "w-max min-w-full pb-2"}
            >
              {!single && (
                <button
                  className="sticky left-0 flex items-center gap-2 py-2 text-left text-[13px] font-semibold"
                  aria-expanded={!closed}
                  onClick={() => setCollapsed((m) => ({ ...m, [lane.key]: !closed }))}
                >
                  <span aria-hidden="true" className={`inline-block w-2 text-muted ${closed ? "" : "rotate-90"}`}>›</span>
                  {lane.color !== undefined && swimlane === "priority" && <PriorityDot opt={{ id: lane.key, name: lane.name, color: lane.color }} />}
                  <span className={lane.key ? "" : "text-muted"}>{lane.name}</span>
                  <span className="font-mono text-[11.5px] font-normal text-muted tabular">{total}</span>
                </button>
              )}
              {!closed && (
                <div className={`flex gap-3 ${single ? "min-h-0 flex-1" : ""}`}>
                  {b.columns.map((col, i) => {
                    const ck = cellKey(lane.key, i);
                    return (
                      <Cell
                        key={col.name}
                        cellId={ck}
                        lane={lane.key}
                        col={i}
                        width={colW}
                        cards={cells.get(ck) ?? EMPTY}
                        b={b}
                        row={row}
                        onTag={onTag}
                        more={single ? remaining(i) : 0}
                        loadingMore={loadingCol === i}
                        onMore={onMore}
                        fill={single}
                        canDrag={canDrag}
                        dropIndex={drop && drop.cell === ck ? drop.index : -1}
                        onStart={startDrag}
                        onEnd={endDrag}
                        onOver={over}
                        onDrop={dropOn}
                        onMenu={onMenu}
                        sel={sel}
                        selectMode={selectMode}
                        canSelect={canSelect}
                        onSelect={onSelect}
                      />
                    );
                  })}
                </div>
              )}
            </section>
          );
        })}
        {b.cards.length === 0 && (
          <div className="px-4 py-6 md:px-7">
            <EmptyState
              title={t("lavoro.vuoti.board.titolo")}
              text={t("lavoro.vuoti.board.testo")}
              action={<Link to="/delivery/backlog" className="btn btn-primary no-underline">{t("lavoro.vuoti.board.azione")}</Link>}
            />
          </div>
        )}
      </div>
    </div>
  );
}

const EMPTY: Card[] = [];

/** «Mostra altre (N)»: carica le card successive di una colonna della board paginata. */
function MoreButton({ name, count, loading, onClick, className = "" }: { name: string; count: number; loading: boolean; onClick: () => void; className?: string }) {
  const t = useT();
  return (
    <button
      type="button"
      data-testid="mostra-altre"
      disabled={loading}
      aria-label={t("lavoro.board.showMoreFor", { count, name })}
      className={`btn min-h-8 px-2 py-1 text-[12px] ${className}`}
      onClick={onClick}
    >
      {loading ? t("lavoro.board.loadingMore") : t("lavoro.board.showMore", { count })}
    </button>
  );
}

type CellProps = {
  cellId: string;
  lane: string;
  col: number;
  width: number;
  cards: Card[];
  b: BoardData;
  row: number;
  onTag: (name: string) => void;
  /** Card della colonna ancora da caricare (pulsante «Mostra altre» in fondo). */
  more: number;
  loadingMore: boolean;
  onMore: (col: number) => void;
  fill: boolean;
  canDrag: boolean;
  dropIndex: number;
  onStart: (c: Card, e: DragEvent) => void;
  onEnd: () => void;
  onOver: (cell: string, index: number) => void;
  onDrop: (lane: string, col: number, index: number) => void;
  onMenu: (c: Card) => void;
  sel: Set<string>;
  selectMode: boolean;
  canSelect: boolean;
  onSelect: (id: string, e: { shiftKey?: boolean }) => void;
};

/** Cella della board: disegna solo le card nella parte visibile (altezza fissa ROW). */
const Cell = memo(function Cell({ cellId, lane, col, width, cards, b, row: ROW, onTag, more, loadingMore, onMore, fill, canDrag, dropIndex, onStart, onEnd, onOver, onDrop, onMenu, sel, selectMode, canSelect, onSelect }: CellProps) {
  const t = useT();
  const stateName = useStateName();
  const ref = useRef<HTMLDivElement>(null);
  const [scroll, setScroll] = useState(0);
  const [height, setHeight] = useState(800);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setHeight(el.clientHeight || 800));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  // Nelle swimlane la cella non scorre da sola fino a 480 px; nella vista semplice occupa la colonna.
  const foot = more > 0 ? FOOT_H : 0;
  const own = fill || cards.length * ROW + foot > 480;
  const viewTop = own ? scroll : 0;
  const viewH = own ? height : cards.length * ROW;
  const start = Math.max(0, Math.floor(viewTop / ROW) - 4);
  const end = Math.min(cards.length, Math.ceil((viewTop + viewH) / ROW) + 4);
  const indexAt = (e: DragEvent) => {
    const el = ref.current!;
    const y = e.clientY - el.getBoundingClientRect().top + el.scrollTop;
    return Math.max(0, Math.min(cards.length, Math.round(y / ROW)));
  };
  return (
    <div
      ref={ref}
      onScroll={own ? (e) => setScroll(e.currentTarget.scrollTop) : undefined}
      onDragOver={
        canDrag
          ? (e) => {
              e.preventDefault();
              e.dataTransfer.dropEffect = "move";
              onOver(cellId, indexAt(e));
            }
          : undefined
      }
      onDrop={
        canDrag
          ? (e) => {
              e.preventDefault();
              onDrop(lane, col, indexAt(e));
            }
          : undefined
      }
      className={`relative shrink-0 snap-start ${own ? "overflow-y-auto overscroll-contain" : ""} ${fill ? "max-h-full" : "max-h-[480px]"} ${dropIndex >= 0 ? "bg-sel/40" : ""}`}
      style={{ width, height: fill ? "100%" : undefined, minHeight: 64 }}
      role="group"
      aria-label={t("lavoro.board.cellLabel", { name: stateName(b.columns[col]?.name ?? ""), count: cards.length })}
    >
      <div className="relative" style={{ height: Math.max(cards.length * ROW + foot, 64) }}>
        {cards.slice(start, end).map((c, i) => (
          <div key={c.id} className="absolute inset-x-0" style={{ top: (start + i) * ROW, height: ROW - 8 }}>
            <CardView card={c} b={b} draggable={canDrag} onStart={onStart} onEnd={onEnd} onMenu={onMenu} onTag={onTag} selected={sel.has(c.id)} selecting={selectMode || sel.size > 0} canSelect={canSelect} onSelect={onSelect} />
          </div>
        ))}
        {more > 0 && (
          <div className="absolute inset-x-0 flex items-center" style={{ top: cards.length * ROW, height: FOOT_H }}>
            <MoreButton name={stateName(b.columns[col]?.name ?? "")} count={more} loading={loadingMore} onClick={() => onMore(col)} />
          </div>
        )}
        {dropIndex >= 0 && <div className="pointer-events-none absolute inset-x-0 h-[3px] bg-accent" style={{ top: Math.max(0, dropIndex * ROW - 6) }} />}
      </div>
    </div>
  );
});

const CardView = memo(function CardView({
  card,
  b,
  draggable,
  onStart,
  onEnd,
  onMenu,
  onTag,
  selected,
  selecting,
  canSelect,
  onSelect,
}: {
  card: Card;
  b: BoardData;
  draggable: boolean;
  onStart: (c: Card, e: DragEvent) => void;
  onEnd: () => void;
  onMenu: (c: Card) => void;
  onTag: (name: string) => void;
  selected: boolean;
  selecting: boolean;
  canSelect: boolean;
  onSelect: (id: string, e: { shiftKey?: boolean }) => void;
}) {
  const t = useT();
  const nav = useNavigate();
  const prio = b.priorities.find((p) => p.id === card.priority);
  const epic = card.epic ? b.epics[card.epic] : undefined;
  const closed = !!card.resolved;
  return (
    <article
      draggable={draggable}
      onDragStart={(e) => onStart(card, e)}
      onDragEnd={onEnd}
      onClick={(e) => {
        if ((e.target as HTMLElement).closest("a,button,input")) return;
        // Ctrl/Cmd, Maiusc o la modalità selezione scelgono la card invece di aprirla.
        if (canSelect && (e.ctrlKey || e.metaKey || e.shiftKey || selecting)) {
          e.preventDefault();
          onSelect(card.id, e);
          return;
        }
        nav({ to: "/e/$key", params: { key: card.key } });
      }}
      aria-label={`${card.key} ${card.title}`}
      aria-selected={selected || undefined}
      className={`group flex h-full cursor-pointer flex-col gap-1 border bg-bg px-2.5 py-2 hover:border-rule ${selected ? "border-accent bg-sel" : "border-hair"} ${draggable ? "active:cursor-grabbing" : ""}`}
    >
      <div className="flex items-center gap-1.5">
        {canSelect && <SelectBox id={card.id} checked={selected} label={t("lavoro.board.selectCard", { key: card.key })} onToggle={onSelect} className={selected || selecting ? "" : "opacity-40 group-hover:opacity-100"} />}
        <TypeMark type={card.type} />
        <Link
          to="/e/$key"
          params={{ key: card.key }}
          className={`font-mono text-[11.5px] font-medium no-underline hover:underline ${closed ? "text-muted line-through" : "text-accent"}`}
        >
          {card.key}
        </Link>
        {epic && (
          <span className="min-w-0 truncate font-mono text-[11px] text-muted" title={`${epic.key} ${epic.title}`}>
            {epic.key}
          </span>
        )}
        <button
          type="button"
          className="ml-auto -mr-1 px-1 text-[15px] leading-none text-muted hover:text-fg"
          aria-label={t("lavoro.board.actionsOn", { key: card.key })}
          aria-haspopup="dialog"
          onClick={() => onMenu(card)}
        >
          ⋯
        </button>
      </div>
      <p className={`m-0 line-clamp-2 text-[13px] leading-[1.3] ${closed ? "text-muted" : ""}`}>{card.title}</p>
      {card.tags && card.tags.length > 0 && <TagChips tags={card.tags} max={2} onPick={onTag} className="max-h-[18px] overflow-hidden" />}
      <div className="mt-auto flex items-center gap-2 text-[11.5px] text-muted">
        <PriorityDot opt={prio} />
        {card.points != null && <span className="font-mono tabular" title={t("lavoro.board.points")}>{t("lavoro.board.pts", { n: fmtPoints(card.points) })}</span>}
        {card.points == null && card.estimate != null && <span className="font-mono tabular" title={t("lavoro.board.estimate")}>{fmtMinutes(card.estimate)}</span>}
        {!!card.children && (
          <span className="font-mono tabular" title={t("lavoro.board.childrenDone")}>
            {card.children_done ?? 0}/{card.children}
          </span>
        )}
        <span className="ml-auto">
          <Person name={card.assignee ? b.members[card.assignee] : undefined} />
        </span>
      </div>
    </article>
  );
});

// ---------------------------------------------------------------------
// Menu della card, motivazione, impostazioni, nuova card

function CardMenu({ card, b, onClose, onMove }: { card: Card; b: BoardData; onClose: () => void; onMove: (col: number) => void }) {
  const t = useT();
  const stateName = useStateName();
  const qc = useQueryClient();
  const [tab, setTab] = useState<"sposta" | "ore">("sposta");
  const assign = useMutation({
    mutationFn: (m: string) => workApi.assign(card.key, m),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["work", "board"] }),
  });
  return (
    <Dialog title={`${card.key} · ${card.title}`} onClose={onClose}>
      <div role="tablist" className="flex gap-4 border-b border-hair">
        {(
          [
            ["sposta", t("lavoro.board.moveAssign")],
            ["ore", t("lavoro.board.logHours")],
          ] as const
        ).map(([k, l]) => (
          <button key={k} role="tab" aria-selected={tab === k} className={`pb-2 ${tab === k ? "border-b-2 border-fg font-semibold" : "text-muted"}`} onClick={() => setTab(k)}>
            {l}
          </button>
        ))}
      </div>
      {tab === "sposta" ? (
        <>
          {b.can_work && (
            <div className="flex flex-col gap-1">
              <span className="label text-muted">{t("lavoro.board.phase")}</span>
              <div className="flex flex-wrap gap-1.5">
                {b.columns.map((c, i) => (
                  <button key={c.name} className={`btn py-1 ${i === card.col ? "btn-primary" : ""}`} disabled={i === card.col} onClick={() => onMove(i)}>
                    {stateName(c.name)}
                  </button>
                ))}
              </div>
            </div>
          )}
          {b.can_work && (
            <label className="flex flex-col gap-1">
              <span className="label text-muted">{t("lavoro.board.assignee")}</span>
              <EntitySelect kind="member" noneLabel={t("lavoro.board.none")} className="field" value={card.assignee ?? ""} onChange={(v) => assign.mutate(v, { onSuccess: onClose })} />
            </label>
          )}
          <ErrorNote error={assign.error} />
          <Link to="/e/$key" params={{ key: card.key }} className="btn self-start no-underline">
            {t("lavoro.board.openDetail")}
          </Link>
        </>
      ) : (
        <TimeLogForm refKey={card.key} compact onDone={() => qc.invalidateQueries({ queryKey: ["work", "time", card.key] })} />
      )}
    </Dialog>
  );
}

function CommentForm({ phase, onSubmit }: { phase: string; onSubmit: (c: string) => void }) {
  const t = useT();
  const [text, setText] = useState("");
  return (
    <form
      className="flex flex-col gap-2"
      onSubmit={(e) => {
        e.preventDefault();
        onSubmit(text);
      }}
    >
      <label className="flex flex-col gap-1">
        <span className="label">{t("lavoro.board.needsReason", { phase })}</span>
        <textarea className="field min-h-20" required value={text} onChange={(e) => setText(e.target.value)} />
      </label>
      <button className="btn btn-primary self-start">{t("lavoro.board.confirmMove")}</button>
    </form>
  );
}

function SettingsDialog({ spaceKey, b, onClose }: { spaceKey: string; b: BoardData; onClose: () => void }) {
  const t = useT();
  const tn = useTypeNames();
  const qc = useQueryClient();
  const [s, setS] = useState<BoardSettings>(() => ({ ...b.settings, columns: b.settings.columns.map((c) => ({ ...c })) }));
  const save = useMutation({
    mutationFn: () => workApi.saveSettings(spaceKey, s),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["work", "board", spaceKey] });
      onClose();
    },
  });
  return (
    <Dialog title={t("lavoro.board.settingsOf", { key: spaceKey })} onClose={onClose} wide>
      <p className="m-0 text-[12.5px] text-muted">
        {t("lavoro.board.settingsHelp")}
      </p>
      <table className="w-full border-collapse">
        <thead>
          <tr className="label h-8 text-left">
            <th className="border-b-2 border-rule pr-3">{t("lavoro.board.column")}</th>
            <th className="border-b-2 border-rule px-3">{t("lavoro.board.phases")}</th>
            <th className="border-b-2 border-rule pl-3 text-right">WIP</th>
          </tr>
        </thead>
        <tbody>
          {s.columns.map((c, i) => (
            <tr key={c.name} className="border-b border-hair">
              <td className="py-1.5 pr-3 font-medium">{c.name}</td>
              <td className="px-3 text-[12px] text-muted">{c.phases.join(", ")}</td>
              <td className="py-1.5 pl-3 text-right">
                <input
                  aria-label={t("lavoro.board.wipOf", { name: c.name })}
                  className="field w-20 py-1 text-right font-mono"
                  type="number"
                  min={0}
                  max={999}
                  value={c.wip}
                  onChange={(e) => {
                    const v = Math.max(0, Math.min(999, Number(e.target.value) || 0));
                    setS((x) => ({ ...x, columns: x.columns.map((cc, j) => (j === i ? { ...cc, wip: v } : cc)) }));
                  }}
                />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <div className="flex flex-wrap gap-3">
        <label className="flex flex-col gap-1">
          <span className="label text-muted">{t("lavoro.board.defaultSwimlane")}</span>
          <Select className="field w-auto" value={s.swimlane} onChange={(e) => setS({ ...s, swimlane: e.target.value as Swimlane })}>
            {SWIMLANES.map(([v, l]) => (
              <option key={v} value={v}>
                {v === "epic" ? tn.name("epic", t(l)) : t(l)}
              </option>
            ))}
          </Select>
        </label>
        <label className="flex flex-col gap-1">
          <span className="label text-muted">{t("lavoro.board.doneDays")}</span>
          <input className="field w-24 font-mono" type="number" min={0} max={365} value={s.done_days} onChange={(e) => setS({ ...s, done_days: Number(e.target.value) || 0 })} />
        </label>
      </div>
      <ErrorNote error={save.error} />
      <div className="flex gap-2">
        <button className="btn btn-primary" disabled={save.isPending} onClick={() => save.mutate()}>
          {t("lavoro.board.save")}
        </button>
        <button className="btn" onClick={onClose}>
          {t("lavoro.board.cancel")}
        </button>
      </div>
    </Dialog>
  );
}

const NEW_TYPES: [string, string][] = [
  ["story", "Story"],
  ["task", "Task"],
  ["bug", "Bug"],
  ["epic", "Epic"],
];

export function NewCard({ spaceKey, b, onClose, onCreated, sprint, sprints }: { spaceKey: string; b: Pick<BoardData, "epics" | "sprint">; onClose: () => void; onCreated: (key: string) => void; sprint?: string; sprints?: { key: string; title: string }[] }) {
  const t = useT();
  const qc = useQueryClient();
  const me = useMe().data?.active_tenant?.member_id ?? "";
  const tn = useTypeNames();
  const [type, setType] = useState("story");
  const [title, setTitle] = useState("");
  const [parent, setParent] = useState("");
  const [points, setPoints] = useState("");
  const [mine, setMine] = useState(false);
  const target = sprint ?? b.sprint?.key;
  const [inSprint, setInSprint] = useState(!!target);
  // Dal backlog il dialogo ha il selettore dello sprint (ondata 2): si sceglie dove nasce l'elemento.
  const [sprintPick, setSprintPick] = useState(sprint ?? "");
  const create = useMutation({
    mutationFn: () =>
      workApi.createItem(spaceKey, {
        type,
        title,
        parent: parent || undefined,
        assignee_id: mine ? me : undefined,
        sprint: sprints ? (type !== "epic" && sprintPick ? sprintPick : undefined) : inSprint && target && type !== "epic" ? target : undefined,
        fields: points && (type === "story" || type === "bug") ? { punti: Number(points.replace(",", ".")) } : undefined,
      }),
    onSuccess: (el) => {
      qc.invalidateQueries({ queryKey: ["work"] });
      onCreated(el.key);
      onClose();
    },
  });
  const epics = Object.entries(b.epics);
  return (
    <Dialog title={t("lavoro.board.newItem")} onClose={onClose}>
      <form
        className="flex flex-col gap-3"
        onSubmit={(e) => {
          e.preventDefault();
          create.mutate();
        }}
      >
        <div role="radiogroup" aria-label={t("lavoro.board.type")} className="flex self-start border border-hair text-[12.5px]">
          {NEW_TYPES.map(([k, l]) => [k, tn.name(k, l)] as const).map(([k, l]) => (
            <button type="button" key={k} role="radio" aria-checked={type === k} className={`px-3 py-1 ${type === k ? "bg-fg text-bg" : "text-muted"}`} onClick={() => setType(k)}>
              {l}
            </button>
          ))}
        </div>
        <label className="flex flex-col gap-1">
          <span className="label">{t("lavoro.board.titleLabel")}</span>
          <input className="field" required maxLength={500} value={title} onChange={(e) => setTitle(e.target.value)} />
        </label>
        {(type === "story" || type === "bug") && epics.length > 0 && (
          <label className="flex flex-col gap-1">
            {/* i18n-ok: nome del tipo, riserva se la configurazione non è ancora arrivata */}
            <span className="label text-muted">{tn.name("epic", "Epic")}</span>
            <Select className="field" value={parent} onChange={(e) => setParent(e.target.value)}>
              <option value="">{t("lavoro.board.none")}</option>
              {epics.map(([id, e]) => (
                <option key={id} value={e.key}>
                  {e.key} {e.title}
                </option>
              ))}
            </Select>
          </label>
        )}
        {type === "task" && (
          <label className="flex flex-col gap-1">
            <span className="label text-muted">{t("lavoro.board.storyKey")}</span>
            <input className="field font-mono" placeholder={`${spaceKey}-12`} value={parent} onChange={(e) => setParent(e.target.value.toUpperCase())} />
          </label>
        )}
        {(type === "story" || type === "bug") && (
          <label className="flex flex-col gap-1">
            <span className="label text-muted">{t("lavoro.board.pointsLabel")}</span>
            <input className="field w-24 font-mono" inputMode="decimal" value={points} onChange={(e) => setPoints(e.target.value)} />
          </label>
        )}
        <label className="flex items-center gap-2">
          <input type="checkbox" checked={mine} onChange={(e) => setMine(e.target.checked)} />
          {t("lavoro.board.assignMe")}
        </label>
        {sprints && type !== "epic" && (
          <label className="flex flex-col gap-1">
            <span className="label text-muted">{t("dafare.backlog.sprintField")}</span>
            <Select className="field" value={sprintPick} onChange={(e) => setSprintPick(e.target.value)}>
              <option value="">{t("dafare.backlog.sprintNone")}</option>
              {sprints.map((s) => (
                <option key={s.key} value={s.key}>
                  {s.key} {s.title}
                </option>
              ))}
            </Select>
          </label>
        )}
        {!sprints && target && type !== "epic" && (
          <label className="flex items-center gap-2">
            <input type="checkbox" checked={inSprint} onChange={(e) => setInSprint(e.target.checked)} />
            {t("lavoro.board.inSprint", { key: target })}
          </label>
        )}
        <ErrorNote error={create.error} />
        <button className="btn btn-primary self-start" disabled={create.isPending}>
          {t("lavoro.board.create")}
        </button>
      </form>
    </Dialog>
  );
}

export function Hint({ children }: { children: ReactNode }) {
  return <p className="m-0 text-[12.5px] text-muted">{children}</p>;
}
