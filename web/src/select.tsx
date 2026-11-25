// Selettore unico dell'interfaccia: sostituisce il <select> nativo ovunque.
//  - Select: stessa forma di <select> (value, onChange, <option>, <optgroup>) per le liste statiche.
//  - OptionSelect: lista statica data come vettore, anche a scelta multipla.
//  - EntitySelect: persone, gruppi, team, unità, applicazioni, fornitori, elementi; cerca sul server.
// Sotto SEARCH_MIN voci statiche è una scelta semplice (nessun campo di ricerca); sopra c'è la ricerca.
// Le liste del server cercano sempre, con un limite di voci e un ritardo, e non chiedono nulla finché
// il pannello non si apre. Tastiera secondo il modello ARIA combobox + listbox; sotto i 640 px il
// pannello è a tutto schermo con il campo di ricerca in alto e bersagli da 44 px.
import {
  Children,
  Fragment,
  isValidElement,
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type ChangeEvent,
  type ComponentPropsWithoutRef,
  type KeyboardEvent,
  type ReactElement,
  type ReactNode,
} from "react";
import { createPortal } from "react-dom";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { api } from "./api";
import { tr } from "./i18n/core";
import type { Key } from "./i18n/core";
import { knownEntity, searchEntities, useEntityLabels, type EntityKind } from "./entities";
import {
  SEARCH_MIN,
  buildSections,
  flatten,
  loadRecents,
  moveActive,
  pushRecent,
  typeahead,
  type Opt,
} from "./selectLogic";

export type { EntityKind } from "./entities";
export type { Opt } from "./selectLogic";

// ---------------------------------------------------------------------
// Dalle <option> alle voci

function textOf(n: ReactNode): string {
  if (n == null || typeof n === "boolean") return "";
  if (typeof n === "string" || typeof n === "number") return String(n);
  if (Array.isArray(n)) return n.map(textOf).join("");
  if (isValidElement(n)) return textOf((n.props as { children?: ReactNode }).children);
  return "";
}

/** Legge <option> e <optgroup> (anche dentro frammenti e vettori) come voci. */
export function parseOptions(children: ReactNode, group?: string, out: Opt[] = []): Opt[] {
  Children.forEach(children, (c) => {
    if (!isValidElement(c)) return;
    const el = c as ReactElement<{ children?: ReactNode; value?: string | number; label?: string; disabled?: boolean }>;
    if (el.type === Fragment) parseOptions(el.props.children, group, out);
    else if (el.type === "optgroup") parseOptions(el.props.children, el.props.label, out);
    else if (el.type === "option") {
      const label = textOf(el.props.children);
      out.push({ value: el.props.value == null ? label : String(el.props.value), label, disabled: el.props.disabled, group });
    }
  });
  return out;
}

// ---------------------------------------------------------------------
// Utilità

function useDebounced<T>(v: T, ms: number): T {
  const [d, setD] = useState(v);
  useEffect(() => {
    if (ms === 0) return setD(v);
    const t = setTimeout(() => setD(v), ms);
    return () => clearTimeout(t);
  }, [v, ms]);
  return ms === 0 ? v : d;
}

const NO_OPTS: Opt[] = [];
const PHONE = "(max-width: 639px)";
const isPhone = () => typeof window !== "undefined" && window.matchMedia?.(PHONE).matches;

function Chevron() {
  return (
    <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true" className="shrink-0 text-muted" fill="none" stroke="currentColor" strokeWidth="1.5">
      <path d="M1.5 3.5 5 7l3.5-3.5" />
    </svg>
  );
}

// ---------------------------------------------------------------------
// Nucleo

type Source = { mode: "static"; opts: Opt[] } | { mode: "server"; kind: EntityKind; types?: string[]; filter?: (o: Opt) => boolean };

type CoreProps = {
  source: Source;
  values: string[];
  multiple?: boolean;
  /** Etichetta della voce «nessuno» (valore vuoto). Se manca il campo è obbligatorio e la voce non compare. */
  noneLabel?: string;
  placeholder?: string;
  /** Con «io» in cima (solo persone). */
  withMe?: boolean;
  /** Nome del campo per i recenti; senza, nessuna memoria. */
  memory?: string;
  /** Nomi già noti delle voci scelte (evita la richiesta). */
  hints?: Record<string, string>;
  onPick: (values: string[]) => void;
  id?: string;
  className?: string;
  disabled?: boolean;
  required?: boolean;
  name?: string;
  autoFocus?: boolean;
  title?: string;
  "aria-label"?: string;
  "aria-labelledby"?: string;
  "aria-invalid"?: boolean | "true" | "false";
  "aria-describedby"?: string;
  "data-testid"?: string;
};

function Core(p: CoreProps) {
  const listId = useId();
  const triggerRef = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(false);
  const [seed, setSeed] = useState("");
  const server = p.source.mode === "server";
  const kind = p.source.mode === "server" ? p.source.kind : undefined;
  const staticOpts = p.source.mode === "static" ? p.source.opts : undefined;

  // Nomi delle voci scelte per il pulsante e per i chip.
  const ids = p.values.filter(Boolean);
  const fetched = useEntityLabels(kind ?? "member", server ? ids : [], p.hints);
  const labelOf = (id: string): string => {
    if (staticOpts) return staticOpts.find((o) => o.value === id)?.label ?? "";
    return fetched[id] || p.hints?.[id] || "";
  };

  const none = p.noneLabel != null ? p.noneLabel : undefined;
  const single = p.values[0] ?? "";
  let shown: ReactNode;
  if (p.multiple) shown = <span className="text-muted">{ids.length ? tr("altro.select.addAnother") : (p.placeholder ?? tr("altro.select.add"))}</span>;
  else if (single !== "") shown = <span className="min-w-0 flex-1 truncate">{labelOf(single) || (staticOpts ? "—" : "…")}</span>;
  else if (staticOpts?.find((o) => o.value === "")) shown = <span className="min-w-0 flex-1 truncate">{staticOpts.find((o) => o.value === "")!.label}</span>;
  else shown = <span className="min-w-0 flex-1 truncate text-muted">{none ?? p.placeholder ?? tr("altro.select.choose")}</span>;

  const close = useCallback((restore: boolean) => {
    setOpen(false);
    setSeed("");
    if (restore) triggerRef.current?.focus();
  }, []);

  const onTriggerKey = (e: KeyboardEvent<HTMLButtonElement>) => {
    if (p.disabled) return;
    if (e.key === "ArrowDown" || e.key === "ArrowUp" || e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      setOpen(true);
    } else if (e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) {
      e.preventDefault();
      setSeed(e.key);
      setOpen(true);
    }
  };

  const remove = (id: string) => p.onPick(p.values.filter((v) => v !== id));

  return (
    <>
      {p.multiple && ids.length > 0 && (
        <span className="mb-1.5 flex flex-wrap gap-1">
          {ids.map((id) => (
            <span key={id} className="inline-flex max-w-full items-center gap-1 border border-hair px-1.5 py-px text-[12.5px] leading-[18px]">
              <span className="truncate">{labelOf(id) || "…"}</span>
              {!p.disabled && (
                <button
                  type="button"
                  className="inline-flex min-h-6 min-w-6 items-center justify-center text-muted hover:text-danger max-sm:min-h-11 max-sm:min-w-11"
                  aria-label={tr("altro.select.remove", { name: labelOf(id) })}
                  onClick={() => remove(id)}
                >
                  ×
                </button>
              )}
            </span>
          ))}
        </span>
      )}
      <button
        ref={triggerRef}
        type="button"
        id={p.id}
        name={p.name}
        role="combobox"
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={open ? listId : undefined}
        aria-label={p["aria-label"]}
        aria-labelledby={p["aria-labelledby"]}
        aria-invalid={p["aria-invalid"]}
        aria-describedby={p["aria-describedby"]}
        aria-required={p.required || undefined}
        data-testid={p["data-testid"]}
        title={p.title}
        disabled={p.disabled}
        autoFocus={p.autoFocus}
        data-value={p.multiple ? undefined : single}
        className={`inline-flex cursor-pointer items-center justify-between gap-2 text-left disabled:cursor-not-allowed ${p.className ?? "field"}`}
        onClick={() => (open ? close(false) : setOpen(true))}
        onKeyDown={onTriggerKey}
      >
        {shown}
        <Chevron />
      </button>
      {/* Convalida nativa dei campi obbligatori: il pulsante non partecipa ai form. */}
      {p.required && !p.multiple && (
        <input tabIndex={-1} aria-hidden="true" required value={single} onChange={() => undefined} style={{ position: "absolute", width: 1, height: 1, opacity: 0, pointerEvents: "none" }} />
      )}
      {open && (
        <Panel core={p} listId={listId} anchor={triggerRef} seed={seed} onClose={close} />
      )}
    </>
  );
}

// ---------------------------------------------------------------------
// Pannello

type Pos = { left: number; width: number; top?: number; bottom?: number; maxHeight: number };

/** I titoli «Recenti», «Attuale» e «Tutti» li crea selectLogic.ts (in italiano, con i test): qui si traducono. */
function sectionTitle(title: string | undefined): string | undefined {
  if (title === "Recenti") return tr("altro.select.section.recent");
  if (title === "Attuale") return tr("altro.select.section.current");
  if (title === "Tutti") return tr("altro.select.section.all");
  return title;
}

function Panel({ core: p, listId, anchor, seed, onClose }: { core: CoreProps; listId: string; anchor: React.RefObject<HTMLButtonElement | null>; seed: string; onClose: (restore: boolean) => void }) {
  const phone = useMemo(isPhone, []);
  const root = useRef<HTMLDivElement>(null);
  const input = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const server = p.source.mode === "server";
  const staticOpts = p.source.mode === "static" ? p.source.opts : undefined;
  const searchable = server || (staticOpts?.length ?? 0) >= SEARCH_MIN;
  const [query, setQuery] = useState(searchable ? seed : "");
  const [pos, setPos] = useState<Pos | null>(null);
  const dq = useDebounced(query, server ? 200 : 0);
  const typeBuf = useRef({ text: "", at: 0 });
  const kind = p.source.mode === "server" ? p.source.kind : "member";
  const types = p.source.mode === "server" ? p.source.types : undefined;

  const me = useQuery({ queryKey: ["me"], queryFn: api.me, staleTime: 300_000, enabled: !!p.withMe });
  const meId = p.withMe ? me.data?.active_tenant?.member_id : undefined;
  const meLabels = useEntityLabels("member", meId ? [meId] : []);

  const res = useQuery({
    queryKey: ["selettore", kind, dq.trim(), (types ?? []).join(",")],
    queryFn: () => searchEntities(kind, dq, { types }),
    enabled: server,
    staleTime: 30_000,
    placeholderData: keepPreviousData,
  });

  // Recenti e voce attuale come voci.
  const recentIds = useMemo(() => (p.memory ? loadRecents(safeStorage(), p.memory) : []), [p.memory]);
  const resolve = (id: string): Opt | undefined => {
    if (!id) return undefined;
    if (staticOpts) return staticOpts.find((o) => o.value === id);
    const k = knownEntity(kind, id);
    const label = k?.label ?? p.hints?.[id] ?? "";
    return label ? { ...(k ?? { value: id }), value: id, label } : undefined;
  };
  const srvFilter = p.source.mode === "server" ? p.source.filter : undefined;
  const fetchedOpts = res.data ?? NO_OPTS;
  const opts = useMemo(() => staticOpts ?? (srvFilter ? fetchedOpts.filter(srvFilter) : fetchedOpts), [staticOpts, srvFilter, fetchedOpts]);
  const noneOpt: Opt | undefined = p.noneLabel != null && !p.multiple ? { value: "", label: p.noneLabel } : undefined;
  const meOpt: Opt | undefined = meId ? { value: meId, label: tr("altro.select.meSuffix", { name: meLabels[meId] || me.data?.display_name || tr("altro.select.me") }) } : undefined;
  const sections = useMemo(
    () =>
      buildSections({
        opts: staticOpts ? staticOpts.filter((o) => o.value !== "" || !noneOpt) : opts,
        query: dq,
        serverFiltered: server,
        none: noneOpt,
        me: meOpt,
        recents: recentIds.map(resolve).filter((o): o is Opt => !!o),
        current: p.values.map(resolve).filter((o): o is Opt => !!o && o.value !== ""),
        multiple: p.multiple,
        selected: p.values,
      }),
    // resolve dipende solo dai dati qui sotto
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [staticOpts, opts, dq, server, p.noneLabel, p.multiple, p.values.join("|"), meOpt?.label, recentIds.join("|"), res.data],
  );
  const flat = useMemo(() => flatten(sections), [sections]);
  // Voce "nessuno" statica già presente come <option value="">: con noneOpt la si riconosce dal valore vuoto.
  const flatKey = flat.map((o) => o.value).join("|");

  const initialActive = () => {
    const cur = flat.findIndex((o) => !o.disabled && p.values.length > 0 && o.value === p.values[0] && !p.multiple);
    return cur >= 0 ? cur : flat.findIndex((o) => !o.disabled);
  };
  const [active, setActive] = useState(initialActive);
  useEffect(() => {
    // Nuovo elenco (ricerca o risposta del server): la voce attiva riparte dalla prima corrispondenza.
    setActive(query.trim() ? flat.findIndex((o) => !o.disabled) : initialActive());
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [flatKey]);

  // Posizione sul desktop: sotto il pulsante, o sopra se sotto manca spazio.
  useLayoutEffect(() => {
    if (phone) return;
    const place = () => {
      const a = anchor.current;
      if (!a) return;
      const r = a.getBoundingClientRect();
      const vw = window.innerWidth;
      const vh = window.innerHeight;
      const width = Math.min(Math.max(r.width, 260), vw - 16);
      const left = Math.min(Math.max(8, r.left), vw - width - 8);
      const below = vh - r.bottom - 10;
      const above = r.top - 10;
      const down = below >= 240 || below >= above;
      setPos({ left, width, top: down ? r.bottom + 2 : undefined, bottom: down ? undefined : vh - r.top + 2, maxHeight: Math.max(160, Math.min(380, down ? below : above)) });
    };
    place();
    window.addEventListener("resize", place);
    window.addEventListener("scroll", place, true);
    return () => {
      window.removeEventListener("resize", place);
      window.removeEventListener("scroll", place, true);
    };
  }, [phone, anchor]);

  // Sul telefono la pagina sotto non scorre.
  useEffect(() => {
    if (!phone) return;
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = prev;
    };
  }, [phone]);

  // Focus: sul campo di ricerca, o sull'elenco se non c'è; clic fuori chiude.
  const ready = phone || pos !== null;
  useEffect(() => {
    // Il pannello nasce nascosto finché non ha la posizione: il focus si dà solo da visibile.
    if (ready) (searchable ? input.current : listRef.current)?.focus({ preventScroll: true });
  }, [ready, searchable]);
  useEffect(() => {
    const down = (e: PointerEvent) => {
      const t = e.target as Node;
      if (root.current?.contains(t) || anchor.current?.contains(t)) return;
      onClose(false);
    };
    document.addEventListener("pointerdown", down, true);
    return () => document.removeEventListener("pointerdown", down, true);
  }, [searchable, anchor, onClose]);

  const optId = (i: number) => `${listId}-o${i}`;
  useEffect(() => {
    if (active >= 0) document.getElementById(optId(active))?.scrollIntoView({ block: "nearest" });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active, flatKey]);

  const pick = (o: Opt | undefined) => {
    if (!o || o.disabled) return;
    if (o.value !== "" && p.memory) pushRecent(safeStorage(), p.memory, o.value);
    if (server && o.value) void import("./entities").then((m) => m.remember(kind, [o]));
    if (p.multiple) {
      if (!p.values.includes(o.value)) p.onPick([...p.values, o.value]);
      setQuery("");
      input.current?.focus();
    } else {
      p.onPick([o.value]);
      onClose(true);
    }
  };

  const onKey = (e: KeyboardEvent<HTMLElement>) => {
    const k = e.key;
    if (k === "ArrowDown" || k === "ArrowUp" || k === "PageDown" || k === "PageUp" || ((k === "Home" || k === "End") && (!searchable || !query))) {
      e.preventDefault();
      setActive((a) => moveActive(flat, a, k));
    } else if (k === "Enter") {
      e.preventDefault();
      pick(flat[active]);
    } else if (k === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      onClose(true);
    } else if (k === "Tab") {
      onClose(true);
    } else if (!searchable && (k === " " || (k.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey))) {
      e.preventDefault();
      const now = Date.now();
      const b = typeBuf.current;
      if (k === " " && !b.text) return pick(flat[active]);
      b.text = now - b.at > 700 ? k : b.text + k;
      b.at = now;
      const i = typeahead(flat, b.text, active);
      if (i >= 0) setActive(i);
    }
  };

  const loading = server && (res.isFetching || query.trim() !== dq.trim());
  const failed = server && res.isError;
  const noResults = flat.length === 0 && !loading;

  const body = (
    <div
      ref={root}
      onKeyDown={onKey}
      onClick={(e) => e.stopPropagation()}
      role={phone ? "dialog" : undefined}
      aria-modal={phone ? true : undefined}
      aria-label={phone ? (p["aria-label"] ?? p.placeholder ?? tr("altro.select.choice")) : undefined}
      className={
        phone
          ? "fixed inset-0 z-[70] flex flex-col bg-bg text-fg"
          : "fixed z-[70] flex flex-col border border-rule bg-bg text-fg"
      }
      style={phone ? undefined : pos ? { left: pos.left, width: pos.width, top: pos.top, bottom: pos.bottom, maxHeight: pos.maxHeight } : { visibility: "hidden", left: 0, top: 0 }}
    >
      {(searchable || phone) && (
        <div className={`flex items-center gap-2 border-b border-rule ${phone ? "p-2" : ""}`}>
          {searchable ? (
            <input
              ref={input}
              role="combobox"
              aria-expanded="true"
              aria-controls={listId}
              aria-autocomplete="list"
              aria-activedescendant={active >= 0 ? optId(active) : undefined}
              aria-label={p["aria-label"] ? tr("altro.select.searchOf", { label: p["aria-label"] }) : tr("altro.select.search")}
              autoComplete="off"
              spellCheck={false}
              placeholder={p.placeholder ?? tr("altro.select.searchPlaceholder")}
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              className="min-h-9 w-full min-w-0 flex-1 border-0 bg-field px-2.5 py-2 text-fg placeholder:text-muted focus-visible:outline-offset-[-2px] max-sm:min-h-11"
            />
          ) : (
            <span className="min-w-0 flex-1 truncate px-2 font-semibold">{p["aria-label"] ?? p.placeholder ?? tr("altro.select.chooseTitle")}</span>
          )}
          {phone && (
            <button type="button" className="btn min-h-11" onClick={() => onClose(true)}>
              {tr("altro.select.close")}
            </button>
          )}
        </div>
      )}
      <div
        ref={listRef}
        id={listId}
        role="listbox"
        tabIndex={searchable ? -1 : 0}
        aria-multiselectable={p.multiple || undefined}
        aria-label={p["aria-label"]}
        aria-activedescendant={!searchable && active >= 0 ? optId(active) : undefined}
        aria-busy={loading || undefined}
        className="min-h-0 flex-1 overflow-y-auto outline-offset-[-2px]"
      >
        {(() => {
          let n = -1;
          return sections.map((s, si) => (
            <div key={si} role="group" aria-label={sectionTitle(s.title)}>
              {s.title && (
                <div aria-hidden="true" className="label px-2.5 pb-1 pt-2 text-muted">
                  {sectionTitle(s.title)}
                </div>
              )}
              {s.opts.map((o) => {
                n++;
                const i = n;
                const selected = o.value === "" ? !p.multiple && !p.values.some(Boolean) : p.values.includes(o.value);
                return (
                  <div
                    key={`${si}-${o.value}`}
                    id={optId(i)}
                    role="option"
                    data-value={o.value}
                    aria-selected={selected}
                    aria-disabled={o.disabled || undefined}
                    onMouseMove={() => !o.disabled && active !== i && setActive(i)}
                    onMouseDown={(e) => e.preventDefault()}
                    onClick={() => pick(o)}
                    className={`flex min-h-[30px] items-center gap-2 border-l-2 px-2.5 py-1 max-sm:min-h-11 ${o.disabled ? "cursor-not-allowed text-muted" : "cursor-pointer"} ${i === active ? "border-accent bg-sel" : "border-transparent"}`}
                  >
                    <span className="inline-flex w-3 shrink-0 justify-center" aria-hidden="true">
                      {selected && (
                        <svg width="11" height="11" viewBox="0 0 11 11" fill="none" stroke="currentColor" strokeWidth="1.6">
                          <path d="m1.5 5.8 2.6 2.6 5.4-5.8" />
                        </svg>
                      )}
                    </span>
                    <span className={`min-w-0 flex-1 truncate ${selected ? "font-semibold" : ""} ${o.value === "" ? "text-muted" : ""}`}>{o.label}</span>
                    {o.detail && <span className="shrink-0 text-[12px] text-muted">{o.detail}</span>}
                  </div>
                );
              })}
            </div>
          ));
        })()}
        {loading && flat.length === 0 && <div className="px-2.5 py-2 text-[12.5px] text-muted">{tr("altro.select.searching")}</div>}
        {noResults && !failed && <div className="px-2.5 py-2 text-[12.5px] text-muted">{tr("altro.select.noResults")}</div>}
        {failed && <div className="px-2.5 py-2 text-[12.5px] text-danger">{tr("altro.select.failed")}</div>}
        {server && !loading && flat.length >= 20 && !query.trim() && <div className="px-2.5 py-1.5 text-[12px] text-muted">{tr("altro.select.typeToNarrow")}</div>}
      </div>
      <div role="status" aria-live="polite" className="sr-only">
        {loading ? tr("altro.select.searchingSr") : noResults ? tr("altro.select.noResults") : tr("altro.select.results", { count: flat.length })}
      </div>
    </div>
  );
  return createPortal(body, document.body);
}

function safeStorage(): Storage | undefined {
  try {
    return window.localStorage;
  } catch {
    return undefined;
  }
}

// ---------------------------------------------------------------------
// Interfacce pubbliche

type Passthrough = Omit<ComponentPropsWithoutRef<"select">, "value" | "defaultValue" | "onChange" | "multiple" | "size" | "children">;

type SelectProps = Passthrough & {
  value?: string | number | readonly string[] | null;
  onChange?: (e: ChangeEvent<HTMLSelectElement>) => void;
  children?: ReactNode;
  /** Nome del campo per i recenti (facoltativo; per le liste lunghe). */
  memory?: string;
};

function fakeChange(value: string, name?: string): ChangeEvent<HTMLSelectElement> {
  const t = { value, name: name ?? "" };
  return { target: t, currentTarget: t, type: "change", preventDefault() {}, stopPropagation() {} } as unknown as ChangeEvent<HTMLSelectElement>;
}

/** Al posto di <select>: stesse proprietà e stesse <option>/<optgroup>. Con 8 voci o più compare la ricerca. */
export function Select({ value, onChange, children, memory, className, ...rest }: SelectProps) {
  const opts = parseOptions(children);
  const v = value == null ? "" : String(value);
  const emptyOpt = opts.find((o) => o.value === "");
  // La voce a valore vuoto è la voce «nessuno»: si mostra in cima e compare nel pulsante quando è scelta.
  return (
    <Core
      source={{ mode: "static", opts }}
      values={[v]}
      className={className ?? "field"}
      memory={memory}
      noneLabel={emptyOpt?.label}
      onPick={(vals) => onChange?.(fakeChange(vals[0] ?? "", rest.name))}
      {...(rest as Omit<CoreProps, "source" | "values" | "onPick">)}
    />
  );
}

/** Scelta singola (valore testo) o multipla (vettore di valori, chip). */
type Choice =
  | { multiple?: false; value: string | null | undefined; onChange: (value: string) => void }
  | { multiple: true; value: string[] | null | undefined; onChange: (value: string[]) => void };

function asValues(value: string | string[] | null | undefined): string[] {
  return Array.isArray(value) ? value : value ? [value] : [];
}

function emit(c: Choice, vals: string[]) {
  if (c.multiple) c.onChange(vals);
  else c.onChange(vals[0] ?? "");
}

type OptionSelectProps = Omit<Passthrough, "name"> & Choice & {
  options: Opt[];
  /** Voce «nessuno» per un campo facoltativo. */
  noneLabel?: string;
  placeholder?: string;
  memory?: string;
};

/** Lista statica data come vettore di voci; con `multiple` i valori diventano chip. */
export function OptionSelect(props: OptionSelectProps) {
  const { options, noneLabel, placeholder, memory, className, ...rest } = props;
  delete (rest as Partial<Choice>).value;
  delete (rest as Partial<Choice>).onChange;
  delete (rest as Partial<Choice>).multiple;
  return (
    <Core
      source={{ mode: "static", opts: options }}
      values={asValues(props.value)}
      multiple={props.multiple}
      noneLabel={noneLabel}
      placeholder={placeholder}
      memory={memory}
      className={className ?? "field"}
      onPick={(vals) => emit(props, vals)}
      {...(rest as Omit<CoreProps, "source" | "values" | "onPick">)}
    />
  );
}

const WITH_ME: EntityKind[] = ["member"];
const entityPlaceholder = (kind: EntityKind): string => tr(`altro.select.entity.${kind}` as Key);

type EntitySelectProps = Omit<Passthrough, "name"> & Choice & {
  kind: EntityKind;
  /** Voce «nessuno» per un campo facoltativo (es. «Nessuno», «Non assegnato»). */
  noneLabel?: string;
  placeholder?: string;
  /** Solo per gli elementi: tipi ammessi. */
  types?: string[];
  /** Nomi già noti (id → nome) per non chiederli al server. */
  hints?: Record<string, string>;
  /** Nome del campo per i recenti (predefinito: il tipo di entità). */
  memory?: string;
  /** Voci da non proporre (es. persone già nel gruppo, richiedenti): si applica ai risultati del server. */
  filter?: (o: Opt) => boolean;
};

/** Scelta di una persona, un gruppo, un team, un'unità, un'applicazione, un fornitore o un elemento, con ricerca sul server. */
export function EntitySelect(props: EntitySelectProps) {
  const { kind, noneLabel, placeholder, types, hints, memory, filter, className, ...rest } = props;
  delete (rest as Partial<Choice>).value;
  delete (rest as Partial<Choice>).onChange;
  delete (rest as Partial<Choice>).multiple;
  return (
    <Core
      source={{ mode: "server", kind, types, filter }}
      values={asValues(props.value)}
      multiple={props.multiple}
      noneLabel={noneLabel}
      placeholder={placeholder ?? entityPlaceholder(kind)}
      withMe={WITH_ME.includes(kind)}
      memory={memory ?? kind}
      hints={hints}
      className={className ?? "field"}
      onPick={(vals) => emit(props, vals)}
      {...(rest as Omit<CoreProps, "source" | "values" | "onPick">)}
    />
  );
}
