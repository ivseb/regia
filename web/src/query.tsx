// Barra della query (piano, passo 16): evidenziazione della sintassi, autocompletamento
// dal server (/api/v1/query/suggest) navigabile da tastiera, errori sottolineati.
import { useEffect, useId, useLayoutEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import { tr } from "./i18n/core";
import { ApiError, queryApi, type QueryErrorInfo, type Suggestion, type Suggestions } from "./api";

// ---------------------------------------------------------------------
// Evidenziazione: un lexer che rispecchia la grammatica di internal/engine/query_lang.go.
// ---------------------------------------------------------------------

type Tok = { kind: "colon" | "comma" | "quoted" | "word"; text: string; from: number; to: number };
type Paint = "field" | "value" | "keyword" | "punct" | "text";
export type Segment = { from: number; to: number; paint?: Paint; error?: boolean };

function lex(s: string): Tok[] {
  const out: Tok[] = [];
  let i = 0;
  while (i < s.length) {
    const c = s[i];
    if (/\s/.test(c)) i++;
    else if (c === ":" || c === ",") {
      out.push({ kind: c === ":" ? "colon" : "comma", text: c, from: i, to: i + 1 });
      i++;
    } else if (c === '"') {
      const j = s.indexOf('"', i + 1);
      const to = j < 0 ? s.length : j + 1;
      out.push({ kind: "quoted", text: s.slice(i, to), from: i, to });
      i = to;
    } else {
      let j = i;
      while (j < s.length && !/[\s:,"]/.test(s[j])) j++;
      out.push({ kind: "word", text: s.slice(i, j), from: i, to: j });
      i = j;
    }
  }
  return out;
}

const SORT = new Set(["ordina", "ordinamento", "sort", "order"]);
const BY = new Set(["per", "by"]);
const DIR = new Set(["asc", "desc", "crescente", "decrescente", "ascendente", "discendente", "ascending", "descending"]);
const OR = new Set(["oppure", "or"]);
const KEYWORDS = new Set([
  "me", "io", "mine", "oggi", "ieri", "domani", "settimana", "mese", "anno", "today", "yesterday", "tomorrow", "week", "month", "year",
  "aperta", "aperte", "aperto", "aperti", "chiusa", "chiuse", "chiuso", "chiusi", "open", "closed", "resolved", "unresolved",
  "vuoto", "vuota", "assente", "nessuno", "nessuna", "empty", "none", "null", "presente", "qualsiasi", "present", "any", "set",
  "sì", "si", "no", "vero", "falso", "true", "false",
]);
const OFFSET = /^[+-]\d{1,4}[gdswmay]$/i;

function paintValue(t: Tok, out: Segment[]) {
  if (t.kind === "quoted") {
    out.push({ from: t.from, to: t.to, paint: "value" });
    return;
  }
  let from = t.from;
  let text = t.text;
  const op = /^(>=|<=|>|<|=)/.exec(text);
  if (op) {
    out.push({ from, to: from + op[0].length, paint: "punct" });
    from += op[0].length;
    text = text.slice(op[0].length);
  }
  const parts = text.split("..");
  parts.forEach((p, i) => {
    if (i > 0) {
      out.push({ from, to: from + 2, paint: "punct" });
      from += 2;
    }
    if (p) out.push({ from, to: from + p.length, paint: KEYWORDS.has(p.toLowerCase()) || OFFSET.test(p) ? "keyword" : "value" });
    from += p.length;
  });
}

/** Colori della query: campi, valori, parole chiave e punteggiatura. */
export function paint(s: string): Segment[] {
  const toks = lex(s);
  const out: Segment[] = [];
  let mode: "text" | "values" | "sortField" | "sortDir" = "text";
  let expect = false;
  for (let i = 0; i < toks.length; i++) {
    const t = toks[i];
    const next = toks[i + 1];
    const lw = t.text.toLowerCase();
    const fieldStart = (t.kind === "word" || t.kind === "quoted") && next?.kind === "colon";
    let head = 0;
    if (t.kind === "word" && SORT.has(lw)) {
      if (next?.kind === "colon") head = 2;
      else if (next?.kind === "word" && BY.has(next.text.toLowerCase()) && toks[i + 2]?.kind === "colon") head = 3;
    }
    if (head) {
      for (let k = 0; k < head; k++) out.push({ from: toks[i + k].from, to: toks[i + k].to, paint: k === head - 1 ? "punct" : "keyword" });
      i += head - 1;
      mode = "sortField";
      continue;
    }
    if (mode === "sortField" && (t.kind === "word" || t.kind === "quoted") && !fieldStart) {
      out.push({ from: t.from, to: t.to, paint: "field" });
      mode = "sortDir";
      continue;
    }
    if (mode === "sortDir" && t.kind === "word" && DIR.has(lw)) {
      out.push({ from: t.from, to: t.to, paint: "keyword" });
      mode = "text";
      continue;
    }
    if (fieldStart) {
      if (t.kind === "word" && t.text.startsWith("-") && t.text.length > 1) {
        out.push({ from: t.from, to: t.from + 1, paint: "punct" });
        out.push({ from: t.from + 1, to: t.to, paint: "field" });
      } else out.push({ from: t.from, to: t.to, paint: "field" });
      out.push({ from: next.from, to: next.to, paint: "punct" });
      i++;
      mode = "values";
      expect = true;
      continue;
    }
    if (mode === "values") {
      if (t.kind === "comma") {
        out.push({ from: t.from, to: t.to, paint: "punct" });
        expect = true;
        continue;
      }
      if (t.kind === "word" && OR.has(lw) && !expect) {
        out.push({ from: t.from, to: t.to, paint: "keyword" });
        expect = true;
        continue;
      }
      if (expect && (t.kind === "word" || t.kind === "quoted")) {
        if (t.kind === "word" && /^(>=|<=|>|<|=)$/.test(t.text) && next && next.kind !== "colon" && next.kind !== "comma") {
          out.push({ from: t.from, to: t.to, paint: "punct" });
          continue;
        }
        paintValue(t, out);
        expect = false;
        continue;
      }
      mode = "text";
    }
    out.push({ from: t.from, to: t.to, paint: t.kind === "colon" || t.kind === "comma" ? "punct" : "text" });
    mode = mode === "sortDir" ? "text" : mode;
  }
  return out;
}

/** Copre tutta la stringa con i segmenti colorati e marca l'intervallo dell'errore. */
export function segments(s: string, err?: { pos: number; end: number } | null): Segment[] {
  const painted = paint(s);
  const full: Segment[] = [];
  let at = 0;
  for (const p of painted) {
    if (p.from > at) full.push({ from: at, to: p.from });
    full.push(p);
    at = p.to;
  }
  if (at < s.length) full.push({ from: at, to: s.length });
  if (!err) return full;
  const pos = Math.max(0, Math.min(err.pos, s.length));
  const end = Math.max(pos + 1, Math.min(err.end, s.length));
  const out: Segment[] = [];
  for (const seg of full) {
    const cuts = [seg.from, ...[pos, end].filter((c) => c > seg.from && c < seg.to), seg.to];
    for (let k = 0; k < cuts.length - 1; k++) {
      const a = cuts[k];
      const b = cuts[k + 1];
      out.push({ ...seg, from: a, to: b, error: a >= pos && b <= end });
    }
  }
  // Errore a fine testo (es. "manca il valore"): un segnaposto visibile.
  if (pos >= s.length) out.push({ from: s.length, to: s.length, error: true });
  return out;
}

const PAINT: Record<Paint, string> = {
  field: "text-accent",
  value: "text-fg",
  keyword: "text-muted",
  punct: "text-muted",
  text: "text-fg",
};

/** Testo della query colorato (anche per mostrare una query in sola lettura). */
export function Highlighted({ text, error }: { text: string; error?: { pos: number; end: number } | null }) {
  return (
    <>
      {segments(text, error).map((seg, i) => (
        <span
          key={i}
          className={`${seg.paint ? PAINT[seg.paint] : ""} ${seg.error ? "underline decoration-danger decoration-wavy decoration-[1.5px] underline-offset-[3px]" : ""}`}
        >
          {seg.from === seg.to ? " " : text.slice(seg.from, seg.to)}
        </span>
      ))}
    </>
  );
}

// ---------------------------------------------------------------------
// Barra della query
// ---------------------------------------------------------------------

const KIND_LABEL: Record<Suggestion["kind"], () => string> = {
  field: () => tr("altro.query.kind.field"),
  value: () => tr("altro.query.kind.value"),
  keyword: () => tr("altro.query.kind.keyword"),
  operator: () => tr("altro.query.kind.operator"),
  sort: () => tr("altro.query.kind.sort"),
  direction: () => tr("altro.query.kind.direction"),
};

type Props = {
  value: string;
  onChange: (v: string) => void;
  onSubmit: (v: string) => void;
  /** Errore della ricerca eseguita (con la posizione), da sottolineare se la query non è cambiata. */
  submitted?: { query: string; error: unknown };
  aside?: ReactNode;
  label?: string;
};

export function QueryBar({ value, onChange, onSubmit, submitted, aside, label = "QUERY" }: Props) {
  const id = useId();
  const input = useRef<HTMLInputElement>(null);
  const [caret, setCaret] = useState(value.length);
  const [focused, setFocused] = useState(false);
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const [sugg, setSugg] = useState<{ for: string; data: Suggestions } | null>(null);
  const [scroll, setScroll] = useState(0);
  const pendingCaret = useRef<number | null>(null);

  // Richiesta dei suggerimenti (con attesa breve e annullamento della precedente).
  useEffect(() => {
    if (!focused) return;
    const ctl = new AbortController();
    const t = setTimeout(() => {
      queryApi
        .suggest(value, caret, ctl.signal)
        .then((data) => {
          setSugg({ for: value, data });
          setActive(0);
        })
        .catch(() => {});
    }, 70);
    return () => {
      clearTimeout(t);
      ctl.abort();
    };
  }, [value, caret, focused]);

  // Dopo un completamento il cursore va alla fine del frammento inserito.
  useLayoutEffect(() => {
    const el = input.current;
    if (el && pendingCaret.current != null) {
      el.setSelectionRange(pendingCaret.current, pendingCaret.current);
      setCaret(pendingCaret.current);
      pendingCaret.current = null;
    }
    if (el) setScroll(el.scrollLeft);
  }, [value]);

  const items = sugg && sugg.for === value ? (sugg.data.items ?? []) : [];
  const show = focused && open && items.length > 0;
  // Un errore che arriva a fine testo con il cursore lì (es. "fase:" appena scritto) è una
  // clausola ancora da completare: non si segnala finché si scrive.
  const typing = (e?: QueryErrorInfo) => !!e && focused && caret >= value.length && e.end >= value.trimEnd().length;
  const liveError: QueryErrorInfo | undefined = sugg && sugg.for === value && !typing(sugg.data.error) ? sugg.data.error : undefined;
  const runError =
    submitted && submitted.query === value && submitted.error instanceof ApiError && submitted.error.span
      ? { pos: submitted.error.span.pos, end: submitted.error.span.end, message: submitted.error.message }
      : undefined;
  const err = runError ?? liveError;

  function accept(s: Suggestion) {
    if (!sugg) return;
    const { from, to } = sugg.data;
    const next = value.slice(0, from) + s.insert + value.slice(to);
    pendingCaret.current = from + s.insert.length;
    onChange(next);
    setOpen(true);
  }

  function syncCaret() {
    const el = input.current;
    if (!el) return;
    setCaret(el.selectionStart ?? el.value.length);
    setScroll(el.scrollLeft);
  }

  function submit(e: FormEvent) {
    e.preventDefault();
    setOpen(false);
    onSubmit(value);
  }

  const listId = `${id}-suggerimenti`;
  return (
    <form onSubmit={submit} className="relative mx-4 flex flex-wrap border-[1.5px] border-rule md:mx-7">
      <label htmlFor={id} className="flex items-center bg-rule px-3 font-mono text-[11.5px] text-bg">
        {label}
      </label>
      <div className="relative min-w-0 flex-[1_1_200px] bg-bg">
        <div aria-hidden="true" className="pointer-events-none absolute inset-0 overflow-hidden px-3 font-mono text-[12.5px] leading-[38px] whitespace-pre">
          <div style={{ transform: `translateX(${-scroll}px)` }}>
            <Highlighted text={value} error={err} />
          </div>
        </div>
        <input
          ref={input}
          id={id}
          value={value}
          role="combobox"
          aria-expanded={show}
          aria-controls={listId}
          aria-autocomplete="list"
          aria-activedescendant={show ? `${listId}-${active}` : undefined}
          aria-invalid={err ? true : undefined}
          aria-describedby={err ? `${id}-errore` : undefined}
          autoComplete="off"
          autoCapitalize="off"
          spellCheck={false}
          className="relative h-[38px] w-full border-0 bg-transparent px-3 font-mono text-[12.5px] text-transparent caret-fg outline-none"
          onChange={(e) => {
            onChange(e.target.value);
            setCaret(e.target.selectionStart ?? e.target.value.length);
            setOpen(true);
          }}
          onFocus={() => setFocused(true)}
          onBlur={() => {
            setFocused(false);
            setOpen(false);
          }}
          onSelect={syncCaret}
          onScroll={syncCaret}
          onKeyDown={(e) => {
            if ((e.ctrlKey || e.metaKey) && e.key === " ") {
              e.preventDefault();
              setOpen(true);
              syncCaret();
              return;
            }
            if (!show) {
              if (e.key === "ArrowDown") {
                e.preventDefault();
                setOpen(true);
              }
              return;
            }
            switch (e.key) {
              case "ArrowDown":
                e.preventDefault();
                setActive((a) => (a + 1) % items.length);
                break;
              case "ArrowUp":
                e.preventDefault();
                setActive((a) => (a - 1 + items.length) % items.length);
                break;
              case "Enter":
              case "Tab":
                e.preventDefault();
                accept(items[Math.min(active, items.length - 1)]);
                break;
              case "Escape":
                e.preventDefault();
                setOpen(false);
                break;
            }
          }}
        />
        {show && (
          <ul
            id={listId}
            role="listbox"
            aria-label={tr("altro.query.suggestions")}
            className="absolute top-full right-0 left-0 z-30 m-0 max-h-72 list-none overflow-y-auto border-[1.5px] border-t-0 border-rule bg-bg p-0 md:right-auto md:min-w-[420px]"
          >
            {items.map((s, i) => (
              <li
                key={s.kind + s.label + i}
                id={`${listId}-${i}`}
                role="option"
                aria-selected={i === active}
                // mousedown: il campo non perde il fuoco prima del completamento.
                onMouseDown={(e) => {
                  e.preventDefault();
                  accept(s);
                }}
                onMouseEnter={() => setActive(i)}
                className={`flex cursor-pointer items-baseline gap-3 border-b border-hair px-3 py-1.5 last:border-b-0 ${i === active ? "bg-sel" : ""}`}
              >
                <span className={`font-mono text-[12.5px] ${s.kind === "field" || s.kind === "sort" ? "text-accent" : "text-fg"}`}>{s.label}</span>
                <span className="min-w-0 flex-1 truncate text-[12px] text-muted">{s.detail}</span>
                <span className="font-mono text-[10.5px] text-muted uppercase">{KIND_LABEL[s.kind]()}</span>
              </li>
            ))}
          </ul>
        )}
      </div>
      {aside}
      {err && (
        <p id={`${id}-errore`} role="status" className="m-0 w-full border-t border-hair px-3 py-1.5 text-[12.5px] text-danger">
          {err.message}
        </p>
      )}
    </form>
  );
}

/** Aggiunge alla query il filtro per un'etichetta (se non c'è già): lo usa il clic su un chip. */
export function withLabel(query: string, name: string): string {
  const value = /^[\p{L}\p{N}_-]+$/u.test(name) ? name : `"${name.replace(/"/g, "")}"`;
  const clause = `etichetta:${value}`; // i18n-ok: sintassi della query
  if (query.toLowerCase().includes(clause.toLowerCase())) return query;
  return `${query.trim()} ${clause}`.trim();
}

/** Sostituisce (o aggiunge) l'ordinamento nella query: lo usa l'intestazione della tabella. */
export function withSort(query: string, field: string, dir: "asc" | "desc"): string {
  const re = /(?:^|\s)(?:ordina\s+per|ordina|ordinamento|sort\s+by|sort)\s*:\s*("[^"]*"|\S+)(?:\s+(?:asc|desc|crescente|decrescente|ascendente|discendente))?/i;
  const clause = `ordina per: ${field} ${dir}`; // i18n-ok: sintassi della query
  if (re.test(query)) return query.replace(re, (m) => (m.startsWith(" ") ? " " : "") + clause).trim();
  return `${query.trim()} ${clause}`.trim();
}

/** Legge l'ordinamento scritto nella query (campo in minuscolo e direzione, se indicata). */
export function sortOf(query: string): { field: string; dir?: "asc" | "desc" } | null {
  const m = /(?:^|\s)(?:ordina\s+per|ordina|ordinamento|sort\s+by|sort)\s*:\s*("[^"]*"|\S+)(?:\s+(asc|desc|crescente|decrescente|ascendente|discendente))?/i.exec(query);
  if (!m) return null;
  const d = m[2]?.toLowerCase();
  return { field: m[1].replace(/"/g, "").toLowerCase(), dir: d ? (d === "asc" || d === "crescente" || d === "ascendente" ? "asc" : "desc") : undefined };
}
