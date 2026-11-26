// Tabella generica degli elementi (piano, passo 16): la stessa per richieste, impatti,
// applicazioni, asset, rischi, incidenti, controlli e documenti. Colonne dai campi di sistema
// e da quelli personalizzati, ordinamento dall'intestazione che si riflette nella query,
// larghezza regolabile, raggruppamento con sezioni comprimibili e conteggi.
// Sotto i 768 px restano le colonne essenziali.
import { useStateName } from "../stateLabels";
import { Select } from "../select";
import { memo, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { api, queryApi, type Element, type QueryField, type State } from "../api";
import { sortOf, withLabel, withSort } from "../query";
import { TagChips } from "../tags";
import { ago, date, ElementTitle, euroRange } from "../ui";
import { COLOR_DOT, fmtDuration, fmtMoney } from "../fields";
import type { Money, RefLabel } from "../fieldsApi";
import { useT } from "../i18n/react";
import { tr } from "../i18n/core";
import { loc } from "../i18n/format";
import { useTypeNames } from "../typesApi";
import { FaseChip, IconaOrologio, Persona, Punteggio, Unita } from "../faseChip";

export type Column = {
  id: string;
  label: string;
  /** Nome del campo per "ordina per:" (assente = non ordinabile). */
  sort?: string;
  /** Direzione al primo clic. */
  first?: "asc" | "desc";
  width: number;
  essential?: boolean;
  align?: "right";
  cell: (el: Element) => ReactNode;
};

export const ESSENTIAL = new Set(["key", "title", "state", "stage"]);
export const BASE_COLS = ["key", "title", "state", "assignee", "updated"];

export function useMedia(q: string): boolean {
  const [m, setM] = useState(() => typeof window !== "undefined" && window.matchMedia(q).matches);
  useEffect(() => {
    const mq = window.matchMedia(q);
    const on = () => setM(mq.matches);
    mq.addEventListener("change", on);
    return () => mq.removeEventListener("change", on);
  }, [q]);
  return m;
}

function customCell(f: QueryField, names: Map<string, string>, labels: Record<string, RefLabel>) {
  const one = (v: unknown): string => {
    if (v == null || v === "") return "";
    switch (f.data_type) {
      case "enum":
      case "state":
      case "version":
        return f.options?.find((o) => o.id === v)?.name ?? String(v);
      case "member":
        return names.get(String(v)) ?? "—";
      case "group":
        return labels[String(v)]?.label ?? "—";
      case "element":
        return labels[String(v)]?.key ?? "—";
      case "date":
        return date(String(v));
      case "datetime":
        return new Date(String(v)).toLocaleString(loc(), { dateStyle: "short", timeStyle: "short" });
      case "boolean":
        return v ? tr("altro.views.yesLower") : tr("altro.views.noLower");
      case "money":
        return fmtMoney(v as Money);
      case "duration":
        return fmtDuration(Number(v));
      case "integer":
      case "decimal":
        return Number(v).toLocaleString(loc());
      default:
        return String(v);
    }
  };
  return (el: Element) => {
    const v = el.fields?.[f.key];
    const vals = Array.isArray(v) ? v : v == null || v === "" ? [] : [v];
    if (vals.length === 0) return <span className="text-muted">—</span>;
    if (f.data_type === "enum" || f.data_type === "version") {
      // Scelte con il quadratino del colore dell'opzione (token del tema).
      return (
        <span className="inline-flex flex-wrap gap-x-2">
          {vals.map((id) => {
            const o = f.options?.find((x) => x.id === id);
            return (
              <span key={String(id)} className="inline-flex items-center gap-1">
                <span className={`size-2 ${COLOR_DOT[o?.color ?? ""] ?? "bg-hair"}`} aria-hidden="true" />
                {o?.name ?? "—"}
              </span>
            );
          })}
        </span>
      );
    }
    if (f.data_type === "url") {
      return (
        <span className="truncate">
          {vals.map((u) => (
            <a key={String(u)} href={String(u)} target="_blank" rel="noopener noreferrer nofollow" className="mr-2 text-accent underline">
              {hostOf(String(u))}
            </a>
          ))}
        </span>
      );
    }
    return vals.map(one).filter(Boolean).join(", ") || <span className="text-muted">—</span>;
  };
}

function hostOf(u: string): string {
  try {
    return new URL(u).host;
  } catch {
    return u;
  }
}

const EMPTY_LABELS: Record<string, RefLabel> = {};
const NO_GROUP = "\u0000vuoto";

/** Titolo dell'elemento: per ogni pagina si sceglie dove porta (anteprima o pagina completa). */
export type TitleCell = (el: Element) => ReactNode;
const defaultTitle: TitleCell = (el) => (
  <Link to="/e/$key" params={{ key: el.key }} className="font-medium text-fg no-underline hover:underline">
    <ElementTitle type={el.type} title={el.title} />
  </Link>
);

/** Colonne disponibili e campi di raggruppamento, dai campi di sistema e personalizzati del tenant. */
export function useListColumns(renderTitle: TitleCell = defaultTitle, labels: Record<string, RefLabel> = EMPTY_LABELS, withDemand = false, onPickTag?: (name: string) => void) {
  const t = useT();
  const tn = useTypeNames();
  const fields = useQuery({ queryKey: ["query-fields"], queryFn: queryApi.fields, staleTime: 300_000 });
  const members = useQuery({ queryKey: ["members"], queryFn: api.members, staleTime: 300_000 });
  // Fasi delle richieste: servono ai quadratini di avanzamento nel chip di fase.
  const reqType = useQuery({ queryKey: ["type", "business_request"], queryFn: () => api.type("business_request"), staleTime: 300_000, enabled: withDemand });
  const reqStates = withDemand ? reqType.data?.states : undefined;
  const names = useMemo(() => new Map((members.data?.items ?? []).map((m) => [m.id, m.display_name])), [members.data]);
  return useMemo(() => {
    const person = (id: string | null | undefined) => {
      const n = id ? names.get(id) : undefined;
      return n ? <Persona name={n} /> : <span className="text-muted">—</span>;
    };
    const day = (v: string | null | undefined) => (v ? date(v) : <span className="text-muted">—</span>);
    const sys: Column[] = [
      {
        id: "key", label: t("altro.views.col.key"), sort: "chiave", first: "asc", width: 96, essential: true,
        cell: (el) => (
          <Link to="/e/$key" params={{ key: el.key }} className="font-mono text-[12px] font-medium whitespace-nowrap text-accent no-underline hover:underline">
            {el.key}
          </Link>
        ),
      },
      { id: "title", label: t("altro.views.col.title"), sort: "titolo", first: "asc", width: 340, essential: true, cell: renderTitle },
      { id: "state", label: t("altro.views.col.state"), sort: "fase", first: "asc", width: 220, essential: true, cell: (el) => (el.state ? <FaseChip states={reqStates} current={el.state} /> : <span className="text-muted">—</span>) },
      { id: "type", label: t("altro.views.col.type"), width: 140, cell: (el) => tn.name(el.type, el.type_name) },
      { id: "space", label: t("altro.views.col.space"), width: 80, cell: (el) => <span className="font-mono text-[12px]">{el.space_key}</span> },
      { id: "labels", label: t("lavoro.etichette.col"), width: 200, cell: (el) => (el.tags?.length ? <TagChips tags={el.tags} onPick={onPickTag} /> : <span className="text-muted">—</span>) },
      { id: "assignee", label: t("altro.views.col.assignee"), width: 160, cell: (el) => person(el.assignee_id) },
      { id: "reporter", label: t("altro.views.col.reporter"), width: 160, cell: (el) => person(el.reporter_id) },
      { id: "updated", label: t("altro.views.col.updated"), sort: "aggiornata", first: "desc", width: 120, align: "right", cell: (el) => <span className="text-muted">{ago(el.updated_at)}</span> },
      { id: "created", label: t("altro.views.col.created"), sort: "creata", first: "desc", width: 110, align: "right", cell: (el) => date(el.created_at) },
      { id: "start", label: t("altro.views.col.start"), sort: "inizio", first: "asc", width: 110, align: "right", cell: (el) => day(el.start_at) },
      { id: "end", label: t("altro.views.col.end"), sort: "fine", first: "asc", width: 110, align: "right", cell: (el) => day(el.due_at) },
      { id: "resolved", label: t("altro.views.col.resolved"), sort: "risolta", first: "desc", width: 110, align: "right", cell: (el) => day(el.resolved_at) },
    ];
    // Colonne della domanda, solo per la lista delle richieste (l'API le manda con demand=1).
    const dash = <span className="text-muted">—</span>;
    const demandCols: Column[] = withDemand
      ? [
          { id: "unit", label: t("tastiera.col.unit"), width: 150, cell: (el) => (el.demand?.unit ? <Unita name={el.demand.unit} /> : dash) },
          { id: "sponsor", label: t("tastiera.col.sponsor"), width: 150, cell: (el) => (el.demand?.sponsor ? <Persona name={el.demand.sponsor} /> : dash) },
          {
            id: "estimate", label: t("tastiera.col.estimate"), width: 110, align: "right",
            cell: (el) => (el.demand && (el.demand.estimate_low_cents != null || el.demand.estimate_high_cents != null) ? euroRange(el.demand.estimate_low_cents, el.demand.estimate_high_cents) : dash),
          },
          {
            // Scadenza normativa: in evidenza (colore e testo) quando c'è un obbligo.
            id: "deadline", label: t("tastiera.col.deadline"), sort: "scadenza", first: "asc", width: 140, align: "right",
            cell: (el) =>
              el.demand?.regulatory_deadline ? (
                <span className={el.demand.is_mandatory ? "font-bold whitespace-nowrap text-flag" : ""} title={el.demand.is_mandatory ? t("tastiera.col.mandatory") : undefined}>
                  {el.demand.is_mandatory && <IconaOrologio />}
                  {date(el.demand.regulatory_deadline)}
                  {el.demand.is_mandatory && <span className="sr-only"> · {t("tastiera.col.mandatory")}</span>}
                </span>
              ) : (
                dash
              ),
          },
          { id: "score", label: t("tastiera.col.score"), width: 130, align: "right", cell: (el) => (el.demand?.score != null ? <Punteggio value={Number(el.demand.score)} text={Number(el.demand.score).toLocaleString(loc(), { maximumFractionDigits: 1 })} /> : dash) },
          {
            // Fase con i giorni trascorsi nella fase.
            id: "stage", label: t("tastiera.col.stage"), sort: "fase", first: "asc", width: 280, essential: true,
            cell: (el) => (
              <span>
                {el.state ? <FaseChip states={reqStates} current={el.state} /> : dash}
                {el.demand && <span className="ml-1.5 text-[12px] text-muted tabular">· {t("tastiera.col.stageDays", { count: el.demand.days_in_state })}</span>}
              </span>
            ),
          },
        ]
      : [];
    const custom: Column[] = (fields.data?.items ?? [])
      .filter((f) => f.source === "custom")
      .map((f) => {
        const numeric = ["integer", "decimal", "money", "duration"].includes(f.data_type ?? "");
        return {
          id: `f:${f.key}`,
          label: f.label,
          sort: f.sortable ? f.key : undefined,
          first: numeric || f.data_type === "date" ? "desc" : "asc",
          width: f.data_type === "text" ? 260 : 140,
          align: numeric ? "right" : undefined,
          cell: customCell(f, names, labels),
        } satisfies Column;
      });
    // Raggruppabili: campi con pochi valori distinti.
    const groups: { id: string; label: string }[] = [
      { id: "state", label: t("altro.views.col.state") },
      { id: "type", label: t("altro.views.col.type") },
      { id: "assignee", label: t("altro.views.col.assignee") },
      { id: "reporter", label: t("altro.views.col.reporter") },
      { id: "space", label: t("altro.views.col.space") },
      ...(fields.data?.items ?? [])
        .filter((f) => f.source === "custom" && ["enum", "member", "boolean", "version"].includes(f.data_type ?? ""))
        .map((f) => ({ id: `f:${f.key}`, label: f.label })),
    ];
    return { all: [...sys, ...demandCols, ...custom], groups, fields: fields.data?.items ?? [], names };
  }, [fields.data, names, renderTitle, labels, t, tn, withDemand, reqStates, onPickTag]);
}

/** Il valore di raggruppamento di un elemento (testo da mostrare; NO_GROUP = senza valore). */
function groupLabel(id: string, el: Element, ctx: { names: Map<string, string>; fields: QueryField[]; labels: Record<string, RefLabel> }): string {
  switch (id) {
    case "state":
      return el.state || NO_GROUP;
    case "type":
      return el.type_name || NO_GROUP;
    case "assignee":
      return (el.assignee_id && ctx.names.get(el.assignee_id)) || NO_GROUP;
    case "reporter":
      return (el.reporter_id && ctx.names.get(el.reporter_id)) || NO_GROUP;
    case "space":
      return el.space_key || NO_GROUP;
  }
  const f = ctx.fields.find((x) => `f:${x.key}` === id);
  if (!f) return NO_GROUP;
  const v = el.fields?.[f.key];
  const vals = Array.isArray(v) ? v : v == null || v === "" ? [] : [v];
  if (vals.length === 0) return NO_GROUP;
  return vals
    .map((x) => {
      if (f.data_type === "member") return ctx.names.get(String(x)) ?? "—";
      if (f.data_type === "boolean") return x ? tr("altro.views.yes") : tr("altro.views.no");
      return f.options?.find((o) => o.id === x)?.name ?? String(x);
    })
    .join(", ");
}

export type Selection = { ids: Set<string>; onChange: (ids: Set<string>) => void };

export type ElementTableProps = {
  items: Element[];
  /** Etichette di elementi e gruppi citati dai campi personalizzati (dalla ricerca). */
  labels?: Record<string, RefLabel>;
  query: string;
  onQuery: (q: string) => void;
  /** Colonne mostrate, larghezze e campo di raggruppamento (controllati da chi usa la tabella). */
  cols: string[];
  widths: Record<string, number>;
  onLayout: (cols: string[], widths: Record<string, number>) => void;
  defaultCols: string[];
  group?: string;
  onGroup?: (id: string | undefined) => void;
  /** Fasi del tipo, per ordinare le sezioni del raggruppamento per fase. */
  states?: State[];
  sel?: string;
  empty: boolean;
  emptyText?: string;
  renderTitle?: TitleCell;
  /** Selezione multipla (facoltativa): chi la passa gestisce le azioni in blocco. */
  selection?: Selection;
  /** Elementi in più da caricare: i conteggi dei gruppi sono parziali. */
  partial?: boolean;
  toolbar?: ReactNode;
  /** Tipo della lista: le richieste hanno in più le colonne della domanda. */
  typeKey?: string;
};

export function ElementTable(p: ElementTableProps) {
  const t = useT();
  const { items, labels = EMPTY_LABELS, query, onQuery, cols: colIds, widths, onLayout, defaultCols, group, onGroup, states, sel, empty, emptyText = tr("altro.views.emptyDefault"), renderTitle, selection, partial, toolbar } = p;
  const wide = useMedia("(min-width: 768px)");
  // Clic su un chip: filtra la lista per quell'etichetta (il gestore resta stabile, legge la query corrente).
  const queryRef = useRef(query);
  queryRef.current = query;
  const onQueryRef = useRef(onQuery);
  onQueryRef.current = onQuery;
  const pickTag = useCallback((name: string) => onQueryRef.current(withLabel(queryRef.current, name)), []);
  const { all, groups, fields, names } = useListColumns(renderTitle, labels, p.typeKey === "business_request", pickTag);
  const stateName = useStateName();

  const cols = useMemo(() => {
    const byId = new Map(all.map((c) => [c.id, c]));
    const chosen = colIds.map((id) => byId.get(id)).filter((c): c is Column => !!c);
    return wide ? chosen : chosen.filter((c) => ESSENTIAL.has(c.id));
  }, [all, colIds, wide]);
  const width = (c: Column) => widths[c.id] ?? c.width;
  const setWidth = (id: string, w: number) => onLayout(colIds, { ...widths, [id]: w });

  const current = sortOf(query);
  const headerSort = (c: Column) => {
    if (!c.sort) return;
    const same = current?.field === c.sort.toLowerCase();
    const dir = same ? (current?.dir === "asc" ? "desc" : current?.dir === "desc" ? "asc" : c.first === "asc" ? "desc" : "asc") : (c.first ?? "asc");
    onQuery(withSort(query, c.sort, dir));
  };

  // Ridimensionamento con il puntatore (mouse, penna o dito) sul bordo dell'intestazione.
  const drag = useRef<{ id: string; x: number; w: number } | null>(null);
  const startResize = (e: React.PointerEvent, c: Column) => {
    e.preventDefault();
    e.stopPropagation();
    drag.current = { id: c.id, x: e.clientX, w: width(c) };
    const move = (ev: PointerEvent) => {
      const d = drag.current;
      if (!d) return;
      setWidth(d.id, Math.max(56, Math.min(900, Math.round(d.w + ev.clientX - d.x))));
    };
    const up = () => {
      drag.current = null;
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
  };
  const keyResize = (e: React.KeyboardEvent, c: Column) => {
    const step = e.shiftKey ? 40 : 10;
    if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
    e.preventDefault();
    setWidth(c.id, Math.max(56, Math.min(900, width(c) + (e.key === "ArrowRight" ? step : -step))));
  };

  // Raggruppamento: sezioni nell'ordine delle fasi, delle opzioni o alfabetico; "senza valore" in fondo.
  const [closed, setClosed] = useState<Set<string>>(() => new Set());
  const sections = useMemo(() => {
    if (!group) return null;
    const ctx = { names, fields, labels };
    const map = new Map<string, Element[]>();
    for (const el of items) {
      const k = groupLabel(group, el, ctx);
      const arr = map.get(k);
      if (arr) arr.push(el);
      else map.set(k, [el]);
    }
    const f = fields.find((x) => `f:${x.key}` === group);
    const rank = (k: string): number => {
      if (k === NO_GROUP) return 1e9;
      if (group === "state") {
        const i = states?.findIndex((s) => s.name === k) ?? -1;
        return i >= 0 ? i : 1e6;
      }
      const i = f?.options?.findIndex((o) => o.name === k) ?? -1;
      return i >= 0 ? i : 1e6;
    };
    return [...map.entries()]
      .sort((a, b) => rank(a[0]) - rank(b[0]) || a[0].localeCompare(b[0], "it"))
      .map(([k, list]) => ({ key: k, label: k === NO_GROUP ? t("altro.views.noValue") : group === "state" ? stateName(k) : k, items: list }));
  }, [group, items, names, fields, labels, states, stateName, t]);

  const selectedAll = !!selection && items.length > 0 && items.every((el) => selection.ids.has(el.id));
  const toggleAll = () => {
    if (!selection) return;
    selection.onChange(selectedAll ? new Set() : new Set(items.map((el) => el.id)));
  };
  const toggleOne = (id: string) => {
    if (!selection) return;
    const next = new Set(selection.ids);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    selection.onChange(next);
  };
  const toggleGroup = (list: Element[], on: boolean) => {
    if (!selection) return;
    const next = new Set(selection.ids);
    for (const el of list) {
      if (on) next.add(el.id);
      else next.delete(el.id);
    }
    selection.onChange(next);
  };

  const span = cols.length + (selection ? 1 : 0);
  const rows = (list: Element[]) =>
    list.map((el) => (
      <Row key={el.id} el={el} cols={cols} wide={wide} active={sel === el.key} selected={selection?.ids.has(el.id)} selectable={!!selection} onToggle={toggleOne} chipsUnderTitle={!cols.some((c) => c.id === "labels")} onPickTag={pickTag} />
    ));

  return (
    <div className="flex flex-col">
      <div className="flex flex-wrap items-center gap-3 py-2 pr-4">
        <ColumnMenu all={all} chosen={colIds} onChange={(ids) => onLayout(ids, widths)} onReset={() => onLayout(defaultCols, {})} />
        {onGroup && (
          <label className="flex items-center gap-2 text-[12.5px] whitespace-nowrap">
            <span className="text-muted">{t("altro.views.groupBy")}</span>
            <Select className="field py-1 text-[12.5px]" value={group ?? ""} onChange={(e) => onGroup(e.target.value || undefined)} aria-label={t("altro.views.groupBy")}>
              <option value="">{t("altro.views.noGroupField")}</option>
              {groups.map((g) => (
                <option key={g.id} value={g.id}>{g.label}</option>
              ))}
            </Select>
          </label>
        )}
        {sections && sections.length > 1 && (
          <button className="text-[12.5px] text-accent underline" onClick={() => setClosed(closed.size ? new Set() : new Set(sections.map((s) => s.key)))}>
            {closed.size ? t("altro.views.openAll") : t("altro.views.closeAll")}
          </button>
        )}
        {!wide && <span className="text-[12px] text-muted">{t("altro.views.narrow")}</span>}
        {toolbar}
      </div>
      <table className="w-full border-collapse tabular" style={{ tableLayout: wide ? "fixed" : "auto", minWidth: wide ? cols.reduce((n, c) => n + width(c), 0) + (selection ? 36 : 0) : undefined }}>
        {wide && (
          <colgroup>
            {selection && <col style={{ width: 36 }} />}
            {cols.map((c) => (
              <col key={c.id} style={{ width: width(c) }} />
            ))}
          </colgroup>
        )}
        <thead>
          <tr className="label h-9 text-left">
            {selection && (
              <th scope="col" className="w-9 border-b-2 border-rule pr-2">
                <input type="checkbox" aria-label={t("altro.views.selectAll")} checked={selectedAll} onChange={toggleAll} />
              </th>
            )}
            {cols.map((c) => {
              const on = current?.field === c.sort?.toLowerCase();
              const dir = on ? (current?.dir ?? c.first) : undefined;
              return (
                <th
                  key={c.id}
                  scope="col"
                  aria-sort={on ? (dir === "asc" ? "ascending" : "descending") : undefined}
                  className={`relative border-b-2 border-rule px-3 ${selection ? "" : "first:pl-0"} last:pr-4 md:last:pr-6 ${c.align === "right" ? "text-right" : ""}`}
                >
                  {c.sort ? (
                    <button
                      className={`label inline-flex items-center gap-1 ${on ? "text-accent" : "text-fg hover:text-accent"}`}
                      title={t("altro.views.sortBy", { name: c.label.toLowerCase() })}
                      onClick={() => headerSort(c)}
                    >
                      {c.label}
                      <span aria-hidden="true" className="font-mono">{on ? (dir === "asc" ? "↑" : "↓") : ""}</span>
                    </button>
                  ) : (
                    c.label
                  )}
                  {wide && (
                    <span
                      role="separator"
                      aria-orientation="vertical"
                      aria-label={t("altro.views.colWidth", { name: c.label })}
                      aria-valuenow={width(c)}
                      tabIndex={0}
                      onPointerDown={(e) => startResize(e, c)}
                      onKeyDown={(e) => keyResize(e, c)}
                      className="absolute top-1.5 -right-1 bottom-1.5 z-10 w-2 cursor-col-resize touch-none border-r border-hair hover:border-accent focus-visible:border-accent"
                    />
                  )}
                </th>
              );
            })}
          </tr>
        </thead>
        {sections ? (
          sections.map((s) => {
            const open = !closed.has(s.key);
            const allIn = !!selection && s.items.every((el) => selection.ids.has(el.id));
            return (
              <tbody key={s.key}>
                <tr className="border-b border-rule bg-sel/40">
                  <th scope="rowgroup" colSpan={span} className="px-0 py-0 text-left font-normal">
                    <div className="flex items-center gap-2 py-1.5 pr-4">
                      {selection && <input type="checkbox" className="ml-0" aria-label={t("altro.views.selectGroup", { name: s.label })} checked={allIn} onChange={() => toggleGroup(s.items, !allIn)} />}
                      <button
                        className="flex min-w-0 flex-1 items-center gap-2 text-left font-semibold"
                        aria-expanded={open}
                        onClick={() =>
                          setClosed((c) => {
                            const next = new Set(c);
                            if (next.has(s.key)) next.delete(s.key);
                            else next.add(s.key);
                            return next;
                          })
                        }
                      >
                        <span aria-hidden="true" className="inline-block w-3 font-mono text-[11px] text-muted">{open ? "▾" : "▸"}</span>
                        <span className="truncate">{s.label}</span>
                        <span className="font-mono text-[11.5px] font-normal text-muted tabular">{s.items.length}{partial ? "+" : ""}</span>
                      </button>
                    </div>
                  </th>
                </tr>
                {open && rows(s.items)}
              </tbody>
            );
          })
        ) : (
          <tbody>{rows(items)}</tbody>
        )}
        {empty && (
          <tbody>
            <tr>
              <td colSpan={span} className="py-8 text-muted">{emptyText}</td>
            </tr>
          </tbody>
        )}
      </table>
    </div>
  );
}

const Row = memo(function Row({ el, cols, wide, active, selected, selectable, onToggle, chipsUnderTitle, onPickTag }: { el: Element; cols: Column[]; wide: boolean; active: boolean; selected?: boolean; selectable: boolean; onToggle: (id: string) => void; chipsUnderTitle: boolean; onPickTag: (name: string) => void }) {
  const t = useT();
  return (
    <tr className={`border-b border-hair ${active ? "bg-sel" : selected ? "bg-sel/70" : "hover:bg-sel/50"}`} aria-current={active ? "true" : undefined}>
      {selectable && (
        <td className="w-9 py-2 pr-2 align-top">
          <input type="checkbox" aria-label={t("altro.views.select", { key: el.key })} checked={!!selected} onChange={() => onToggle(el.id)} />
        </td>
      )}
      {cols.map((c) => (
        <td key={c.id} className={`overflow-hidden px-3 py-2 align-top text-ellipsis ${selectable ? "" : "first:pl-0"} last:pr-4 md:last:pr-6 ${c.align === "right" ? "text-right whitespace-nowrap" : ""} ${c.id === "title" || !wide ? "" : "whitespace-nowrap"}`}>
          {c.cell(el)}
          {c.id === "title" && chipsUnderTitle && el.tags && el.tags.length > 0 && <TagChips tags={el.tags} max={wide ? 3 : 2} onPick={onPickTag} className="mt-1 block" />}
        </td>
      ))}
    </tr>
  );
});

function ColumnMenu({ all, chosen, onChange, onReset }: { all: Column[]; chosen: string[]; onChange: (ids: string[]) => void; onReset: () => void }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const box = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const close = (e: MouseEvent) => !box.current?.contains(e.target as Node) && setOpen(false);
    window.addEventListener("mousedown", close);
    return () => window.removeEventListener("mousedown", close);
  }, [open]);
  const toggle = (id: string) => {
    if (chosen.includes(id)) onChange(chosen.filter((x) => x !== id));
    else onChange([...chosen, id]);
  };
  const move = (id: string, d: -1 | 1) => {
    const i = chosen.indexOf(id);
    const j = i + d;
    if (i < 0 || j < 0 || j >= chosen.length) return;
    const next = [...chosen];
    [next[i], next[j]] = [next[j], next[i]];
    onChange(next);
  };
  const ordered = [...chosen.map((id) => all.find((c) => c.id === id)).filter((c): c is Column => !!c), ...all.filter((c) => !chosen.includes(c.id))];
  return (
    <div ref={box} className="relative" onKeyDown={(e) => e.key === "Escape" && setOpen(false)}>
      <button className="btn py-1 text-[12.5px]" aria-expanded={open} aria-haspopup="true" onClick={() => setOpen((o) => !o)}>
        {t("altro.views.columns")} <span className="font-mono text-muted tabular">{chosen.length}</span>
      </button>
      {open && (
        <div className="absolute top-full left-0 z-30 mt-1 w-[300px] max-w-[calc(100vw-32px)] border-[1.5px] border-rule bg-bg">
          <div className="label flex items-center justify-between border-b-2 border-rule px-3 py-2">
            <span>{t("altro.views.visibleColumns")}</span>
            <button className="text-[12px] font-normal tracking-normal text-accent normal-case underline" onClick={onReset}>
              {t("altro.views.reset")}
            </button>
          </div>
          <ul className="m-0 max-h-80 list-none overflow-y-auto p-0">
            {ordered.map((c) => {
              const on = chosen.includes(c.id);
              return (
                <li key={c.id} className="flex items-center gap-2 border-b border-hair px-3 py-1.5 last:border-b-0">
                  <label className="flex min-w-0 flex-1 items-center gap-2">
                    <input type="checkbox" checked={on} disabled={c.id === "key"} onChange={() => toggle(c.id)} />
                    <span className="truncate">{c.label}</span>
                    {c.id.startsWith("f:") && <span className="font-mono text-[10.5px] text-muted">{t("altro.views.fieldTag")}</span>}
                  </label>
                  {on && (
                    <span className="flex gap-1">
                      <button className="px-1 text-muted hover:text-fg" aria-label={t("altro.views.moveBefore", { name: c.label })} onClick={() => move(c.id, -1)}>↑</button>
                      <button className="px-1 text-muted hover:text-fg" aria-label={t("altro.views.moveAfter", { name: c.label })} onClick={() => move(c.id, 1)}>↓</button>
                    </span>
                  )}
                </li>
              );
            })}
          </ul>
        </div>
      )}
    </div>
  );
}
