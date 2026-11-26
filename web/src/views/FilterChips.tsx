// Filtri visuali accanto alla query testuale (piano, passo 16): ogni clausola della query è
// un chip che si modifica con le proposte del server (le stesse dell'autocompletamento), e
// ogni modifica a un chip riscrive la query. Chi non conosce il linguaggio filtra qui; chi lo
// conosce scrive nella barra, e i chip si aggiornano.
import { useEffect, useId, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { queryApi, type QueryField, type Suggestion } from "../api";
import { useT } from "../i18n/react";
import { useTypeNames } from "../typesApi";
import { tr } from "../i18n/core";
import { addClause, clausesOf, quoteValue, replaceClause, showValue, unquote, type Clause } from "./filters";

/** Il nome mostrato di un campo: i personalizzati hanno un'etichetta, gli altri il nome della query. */
const TITLES: Record<string, () => string> = {
  unita: () => tr("altro.views.field.unit"),
  unit: () => tr("altro.views.field.unit"),
  scadenza_sla: () => tr("altro.views.field.slaDue"),
  sla: () => tr("altro.views.field.slaDue"),
  sla_due: () => tr("altro.views.field.slaDue"),
  tipo: () => tr("altro.views.field.type"),
  stato: () => tr("altro.views.field.state"),
  // Campi offerti dai moduli: il nome della query è italiano, l'etichetta segue la lingua.
  scadenza: () => tr("tastiera.field.deadline"),
  desiderata: () => tr("tastiera.field.desired"),
  obbligatoria: () => tr("tastiera.field.mandatory"),
  semaforo: () => tr("tastiera.field.rag"),
  prossimo_report: () => tr("tastiera.field.nextReport"),
  valutatore: () => tr("tastiera.field.reviewTeam"),
  budget_approvato: () => tr("tastiera.field.approvedBudget"),
  con_costi: () => tr("tastiera.field.withCosts"),
  applicazione: () => tr("tastiera.field.application"),
  richiesta: () => tr("tastiera.field.request"),
};
const SIGLE = new Set(["sla", "kpi", "cve", "id", "nis2", "dora"]);
export function fieldTitle(f: QueryField): string {
  if (f.source === "custom") return f.label;
  const known = TITLES[f.name] ?? TITLES[f.key];
  if (known) return known();
  const words = f.name.split("_").map((w) => (SIGLE.has(w) ? w.toUpperCase() : w));
  const t = words.join(" ");
  return t.charAt(0).toUpperCase() + t.slice(1);
}

// I campi che quasi tutti cercano per primi, nell'ordine in cui compaiono nel menu.
const MAIN = ["state", "assignee", "type", "space", "updated", "created", "reporter", "watched", "end", "resolved"];

const DATE_PRESETS = [
  { value: "oggi", label: "altro.views.preset.today" },
  { value: "settimana", label: "altro.views.preset.week" },
  { value: "mese", label: "altro.views.preset.month" },
  { value: "-7g", label: "altro.views.preset.last7" },
  { value: "-30g", label: "altro.views.preset.last30" },
  { value: "+7g", label: "altro.views.preset.next7" },
  { value: "+30g", label: "altro.views.preset.next30" },
  { value: "vuoto", label: "altro.views.preset.empty" },
  { value: "presente", label: "altro.views.preset.set" },
] as const;

const isDate = (f?: QueryField) => f?.kind === "date" || f?.data_type === "date" || f?.data_type === "datetime";

function summary(c: Clause): string {
  const vs = c.values.map(showValue);
  return vs.length > 2 ? `${vs.slice(0, 2).join(", ")} +${vs.length - 2}` : vs.join(", ");
}

/** `typeKey`: il tipo della lista; i campi di quel tipo si propongono per primi (e quelli di altri tipi restano nascosti). */
export function FilterChips({ query, onQuery, fields, typeKey }: { query: string; onQuery: (q: string) => void; fields: QueryField[]; typeKey?: string }) {
  const t = useT();
  const clauses = useMemo(() => clausesOf(query), [query]);
  // Chip aperto: per campo e segno (un campo ha un solo chip per segno); "" = nessuno.
  const [open, setOpen] = useState<string>("");
  const byName = useMemo(() => {
    const m = new Map<string, QueryField>();
    for (const f of fields) {
      m.set(f.name.toLowerCase(), f);
      m.set(f.key.toLowerCase(), f);
    }
    // Sinonimi italiani e inglesi dei campi di sistema (come nel linguaggio di query).
    for (const [alias, key] of [["stato", "state"], ["status", "state"], ["progetto", "space"], ["project", "space"], ["assignee", "assignee"], ["aggiornato", "updated"]] as const) {
      const f = fields.find((x) => x.key === key);
      if (f && !m.has(alias)) m.set(alias, f);
    }
    return m;
  }, [fields]);
  const idOf = (c: Pick<Clause, "field" | "neg">) => `${c.neg ? "-" : ""}${c.field.toLowerCase()}`;

  return (
    <div className="mx-4 mt-2 flex flex-wrap items-center gap-1.5 md:mx-7" role="group" aria-label={t("altro.views.filters")}>
      {clauses.map((c) => (
        <Chip
          key={idOf(c)}
          clause={c}
          field={byName.get(c.field.toLowerCase())}
          query={query}
          open={open === idOf(c)}
          onOpen={(on) => setOpen(on ? idOf(c) : "")}
          onQuery={onQuery}
        />
      ))}
      <AddFilter
        fields={fields}
        typeKey={typeKey}
        used={new Set(clauses.filter((c) => !c.neg).map((c) => byName.get(c.field.toLowerCase())?.name.toLowerCase() ?? c.field.toLowerCase()))}
        onPick={(f) => {
          // Un campo già filtrato si modifica sul suo chip.
          const ex = clauses.find((c) => !c.neg && byName.get(c.field.toLowerCase()) === f);
          setOpen(ex ? idOf(ex) : `new:${f.name.toLowerCase()}`);
        }}
      />
      {open.startsWith("new:") && (
        <span className="relative">
          <Editor
            key={open}
            initial={{ kind: "clause", field: open.slice(4), neg: false, values: [] }}
            existing={false}
            field={byName.get(open.slice(4))}
            query={query}
            onQuery={onQuery}
            onClose={() => setOpen("")}
          />
        </span>
      )}
    </div>
  );
}

function Chip({ clause, field, query, open, onOpen, onQuery }: { clause: Clause; field?: QueryField; query: string; open: boolean; onOpen: (on: boolean) => void; onQuery: (q: string) => void }) {
  const t = useT();
  const title = field ? fieldTitle(field) : clause.field;
  const remove = () => {
    const i = clausesOf(query).findIndex((c) => c.field.toLowerCase() === clause.field.toLowerCase() && c.neg === clause.neg);
    onQuery(replaceClause(query, i, null));
    onOpen(false);
  };
  return (
    <span className="relative inline-flex max-w-full items-stretch border-[1.5px] border-rule bg-bg text-[12.5px]">
      <button
        className="flex min-w-0 items-baseline gap-1.5 px-2 py-1 text-left hover:bg-sel"
        aria-expanded={open}
        aria-haspopup="dialog"
        title={t("altro.views.filterEdit", { title })}
        onClick={() => onOpen(!open)}
      >
        <span className="font-semibold">{title}</span>
        <span className="text-muted">{clause.neg ? t("altro.views.isNot") : t("altro.views.is")}</span>
        <span className="min-w-0 [overflow-wrap:anywhere]">{summary(clause)}</span>
      </button>
      <button className="border-l border-hair px-1.5 text-muted hover:bg-sel hover:text-fg" aria-label={t("altro.views.removeFilterOf", { title })} onClick={remove}>
        ×
      </button>
      {open && <Editor initial={clause} existing field={field} query={query} onQuery={onQuery} onClose={() => onOpen(false)} />}
    </span>
  );
}

/** Pannello di un chip: valori proposti dal server, valori liberi, date relative, esclusione. */
function Editor({ initial, existing, field, query, onQuery, onClose }: { initial: Clause; existing: boolean; field?: QueryField; query: string; onQuery: (q: string) => void; onClose: () => void }) {
  const t = useT();
  const id = useId();
  const box = useRef<HTMLDivElement>(null);
  const [text, setText] = useState("");
  // La clausola in modifica; per un chip nuovo vive qui finché non ha un valore.
  const [draft, setDraft] = useState<Clause>(initial);
  const orig = useRef(initial);
  const title = field ? fieldTitle(field) : initial.field;
  const date = isDate(field);

  useEffect(() => {
    const out = (e: MouseEvent) => !box.current?.contains(e.target as Node) && onClose();
    window.addEventListener("mousedown", out);
    return () => window.removeEventListener("mousedown", out);
  }, [onClose]);
  useEffect(() => box.current?.querySelector("input")?.focus(), []);

  // La query senza questo chip: contesto per le proposte (le fasi dipendono dal tipo).
  const base = useMemo(() => {
    const i = clausesOf(query).findIndex((c) => c.field.toLowerCase() === orig.current.field.toLowerCase() && c.neg === orig.current.neg);
    return i >= 0 ? replaceClause(query, i, null) : query;
  }, [query]);
  const fname = /[\s:,"]/.test(initial.field) ? `"${initial.field}"` : initial.field;
  const probe = `${base ? base + " " : ""}${fname}: ${text}`;
  const sugg = useQuery({
    queryKey: ["chip-suggest", probe],
    queryFn: ({ signal }) => queryApi.suggest(probe, probe.length, signal),
    staleTime: 30_000,
    placeholderData: (p) => p,
  });
  const items: Suggestion[] = (sugg.data?.context === "value" ? sugg.data.items : []).filter((s) => s.kind === "value" || s.kind === "keyword");

  // Applica la clausola alla query. Un chip nuovo senza valori non cambia nulla.
  const apply = (next: Clause) => {
    setDraft(next);
    const cs = clausesOf(query);
    const i = cs.findIndex((c) => c.field.toLowerCase() === orig.current.field.toLowerCase() && c.neg === orig.current.neg);
    let q: string;
    if (i >= 0) q = replaceClause(query, i, next);
    else q = next.values.length ? addClause(query, next) : query;
    orig.current = { ...orig.current, neg: next.neg };
    if (q !== query) onQuery(q);
  };
  const has = (v: string) => draft.values.some((x) => unquote(x).toLowerCase() === unquote(v).toLowerCase());
  const toggle = (v: string) => {
    const raw = quoteValue(v);
    apply({ ...draft, values: has(raw) ? draft.values.filter((x) => unquote(x).toLowerCase() !== unquote(raw).toLowerCase()) : [...draft.values, raw] });
  };
  const addText = () => {
    const t = text.trim();
    if (!t) return;
    if (!has(t)) apply({ ...draft, values: [...draft.values, quoteValue(t)] });
    setText("");
  };
  // Le date a intervallo: da / a.
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const range = () => {
    if (!from && !to) return;
    apply({ ...draft, values: [`${from}..${to}`] });
  };

  return (
    <div
      ref={box}
      role="dialog"
      aria-label={t("altro.views.filterTitle", { title })}
      onKeyDown={(e) => e.key === "Escape" && onClose()}
      className={`z-40 flex w-[340px] max-w-[calc(100vw-32px)] flex-col border-[1.5px] border-rule bg-bg max-md:fixed max-md:inset-x-4 md:absolute md:top-full md:left-0 mt-1`}
    >
      <div className="label flex items-center justify-between gap-2 border-b-2 border-rule px-3 py-2">
        <span className="min-w-0 [overflow-wrap:anywhere]">{title}</span>
        <label className="flex items-center gap-1.5 text-[12px] font-normal tracking-normal normal-case">
          <input type="checkbox" checked={draft.neg} onChange={(e) => apply({ ...draft, neg: e.target.checked })} />
          {t("altro.views.exclude")}
        </label>
      </div>
      {draft.values.length > 0 && (
        <ul className="m-0 flex list-none flex-wrap gap-1 border-b border-hair p-2" aria-label={t("altro.views.chosenValues")}>
          {draft.values.map((v) => (
            <li key={v} className="inline-flex items-center border border-hair bg-sel/60 text-[12px]">
              <span className="px-1.5 py-0.5">{showValue(v)}</span>
              <button className="px-1 text-muted hover:text-fg" aria-label={t("altro.views.removeValue", { value: showValue(v) })} onClick={() => toggle(v)}>×</button>
            </li>
          ))}
        </ul>
      )}
      {date && (
        <div className="flex flex-col gap-2 border-b border-hair p-2">
          <div className="flex flex-wrap gap-1">
            {DATE_PRESETS.map((p) => (
              <button key={p.value} className={`border px-1.5 py-0.5 text-[12px] ${has(p.value) ? "border-accent text-accent" : "border-hair hover:border-rule"}`} aria-pressed={has(p.value)} onClick={() => apply({ ...draft, values: has(p.value) ? [] : [p.value] })}>
                {t(p.label)}
              </button>
            ))}
          </div>
          <div className="flex items-end gap-2">
            <label className="flex min-w-0 flex-1 flex-col text-[11.5px] text-muted">
              {t("altro.views.from")}
              <input type="date" className="field py-1 text-[12.5px]" value={from} onChange={(e) => setFrom(e.target.value)} onBlur={range} />
            </label>
            <label className="flex min-w-0 flex-1 flex-col text-[11.5px] text-muted">
              {t("altro.views.to")}
              <input type="date" className="field py-1 text-[12.5px]" value={to} onChange={(e) => setTo(e.target.value)} onBlur={range} />
            </label>
          </div>
        </div>
      )}
      <div className="border-b border-hair p-2">
        <label htmlFor={`${id}-t`} className="sr-only">{t("altro.views.searchOrType")}</label>
        <input
          id={`${id}-t`}
          className="field w-full py-1 text-[12.5px]"
          placeholder={date ? t("altro.views.typeDatePlaceholder") : t("altro.views.typePlaceholder")}
          value={text}
          autoComplete="off"
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              // Invio sceglie l'unica proposta rimasta, altrimenti usa il testo scritto.
              if (items.length === 1) {
                toggle(items[0].insert.trim());
                setText("");
              } else addText();
            }
          }}
        />
      </div>
      {/* Per le date bastano le scelte rapide; le proposte compaiono se si scrive. */}
      <ul className={`m-0 max-h-60 list-none overflow-y-auto p-0 ${date && !text ? "hidden" : ""}`}>
        {items.map((s) => {
          const v = s.insert.trim();
          return (
            <li key={s.kind + s.label}>
              <label className="flex cursor-pointer items-center gap-2 border-b border-hair px-3 py-1.5 last:border-b-0 hover:bg-sel/60">
                <input type="checkbox" checked={has(v)} onChange={() => toggle(v)} />
                <span className="min-w-0 flex-1 [overflow-wrap:anywhere]">{s.label}</span>
                {s.detail && <span className="max-w-[40%] text-right text-[11.5px] [overflow-wrap:anywhere] text-muted">{s.detail}</span>}
              </label>
            </li>
          );
        })}
        {items.length === 0 && !sugg.isPending && (
          <li className="px-3 py-2 text-[12.5px] text-muted">{text ? t("altro.views.noSuggestions") : t("altro.views.typeValue")}</li>
        )}
      </ul>
      <div className="flex items-center justify-between gap-2 border-t border-hair px-3 py-1.5">
        {existing ? (
          <button
            className="text-[12.5px] text-muted underline hover:text-fg"
            onClick={() => {
              apply({ ...draft, values: [] });
              onClose();
            }}
          >
            {t("altro.views.removeFilter")}
          </button>
        ) : (
          <span />
        )}
        <button className="btn py-0.5 text-[12.5px]" onClick={onClose}>
          {t("altro.views.done")}
        </button>
      </div>
    </div>
  );
}

/** Menu "+ Filtro": cerca tra i campi e ne apre il chip. */
function AddFilter({ fields, used, onPick, typeKey }: { fields: QueryField[]; used: Set<string>; onPick: (f: QueryField) => void; typeKey?: string }) {
  const t = useT();
  const tn = useTypeNames();
  const [open, setOpen] = useState(false);
  const [text, setText] = useState("");
  const box = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const out = (e: MouseEvent) => !box.current?.contains(e.target as Node) && setOpen(false);
    window.addEventListener("mousedown", out);
    return () => window.removeEventListener("mousedown", out);
  }, [open]);
  const list = useMemo(() => {
    const t = text.trim().toLowerCase();
    const match = (f: QueryField) => !t || fieldTitle(f).toLowerCase().includes(t) || f.name.toLowerCase().includes(t);
    // "sort" e campi senza senso come filtro (titolo: solo per ordinare) restano fuori.
    const usable = fields.filter((f) => f.key !== "title" && match(f));
    // Campi del tipo della lista (associati al tipo o offerti dal suo modulo): in cima.
    const own = typeKey ? usable.filter((f) => f.types?.includes(typeKey)) : [];
    // Campi di altri tipi: fuori dalla vista, a meno che non si stia cercando per nome.
    const foreign = typeKey ? usable.filter((f) => f.types && !f.types.includes(typeKey)) : [];
    const ok = usable.filter((f) => !own.includes(f) && !foreign.includes(f));
    const main = MAIN.map((k) => ok.find((f) => f.key === k)).filter((f): f is QueryField => !!f);
    const custom = ok.filter((f) => f.source === "custom");
    const rest = ok.filter((f) => !main.includes(f) && f.source !== "custom");
    return { own, main, custom, rest, other: t ? foreign : [] };
  }, [fields, text, typeKey]);
  const row = (f: QueryField) => (
    <li key={f.key}>
      <button
        className="flex w-full items-baseline gap-2 border-b border-hair px-3 py-1.5 text-left hover:bg-sel/60"
        onClick={() => {
          setOpen(false);
          setText("");
          onPick(f);
        }}
      >
        <span className="min-w-0 flex-1 [overflow-wrap:anywhere]">{fieldTitle(f)}</span>
        {used.has(f.name.toLowerCase()) && <span className="text-[11px] text-muted">{t("altro.views.inUse")}</span>}
        <span className="font-mono text-[11px] text-muted">{f.name}</span>
      </button>
    </li>
  );
  const section = (label: string, items: QueryField[]) =>
    items.length > 0 && (
      <>
        <li className="label border-b border-hair bg-sel/40 px-3 py-1">{label}</li>
        {items.map(row)}
      </>
    );
  return (
    <div ref={box} className="relative" onKeyDown={(e) => e.key === "Escape" && setOpen(false)}>
      <button className="btn py-1 text-[12.5px]" aria-expanded={open} aria-haspopup="true" onClick={() => setOpen((o) => !o)}>
        {t("altro.views.addFilter")}
      </button>
      {open && (
        <div className="z-40 mt-1 flex w-[320px] max-w-[calc(100vw-32px)] flex-col border-[1.5px] border-rule bg-bg max-md:fixed max-md:inset-x-4 md:absolute md:top-full md:left-0">
          <div className="border-b-2 border-rule p-2">
            <input autoFocus className="field w-full py-1 text-[12.5px]" placeholder={t("altro.views.searchField")} aria-label={t("altro.views.searchFieldLabel")} value={text} onChange={(e) => setText(e.target.value)} />
          </div>
          <ul className="m-0 max-h-72 list-none overflow-y-auto p-0">
            {typeKey && section(t("tastiera.filter.ofType", { type: tn.name(typeKey, typeKey) }), list.own)}
            {section(t("altro.views.main"), list.main)}
            {section(t("altro.views.customFields"), list.custom)}
            {section(t("altro.views.others"), list.rest)}
            {section(t("tastiera.filter.otherTypes"), list.other)}
            {list.own.length + list.main.length + list.custom.length + list.rest.length + list.other.length === 0 && <li className="px-3 py-2 text-[12.5px] text-muted">{t("altro.views.noField")}</li>}
          </ul>
        </div>
      )}
    </div>
  );
}

