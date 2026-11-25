// Campi personalizzati nell'interfaccia d'uso: valore in lettura, controllo adatto al tipo
// (data, scelta con colori, persona con ricerca, multipli come chip), modifica sul posto,
// pannello per sezioni nel dettaglio e campi del form di creazione.
import { useRef, useState, type ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { type ElementDetail } from "./api";
import { loc } from "./i18n/format";
import { tr } from "./i18n/core";
import { localName } from "./i18n/localName";
import { useLang } from "./i18n/react";
import { compiled, evalCondition, type Facts } from "./condition";
import { fieldsApi, type FieldView, type Money, type OptionView, type RefLabel } from "./fieldsApi";
import { useMe } from "./shell";
import { EntitySelect, type EntityKind, type Opt } from "./select";
import { ErrorNote } from "./ui";

/** Classe del quadratino di colore di un'opzione (token del tema). */
export const COLOR_DOT: Record<string, string> = {
  accent: "bg-accent",
  ok: "bg-ok",
  flag: "bg-flag",
  danger: "bg-danger",
  muted: "bg-muted",
  fg: "bg-fg",
};

export function valuesOf(v: unknown): unknown[] {
  if (Array.isArray(v)) return v;
  return v == null || v === "" ? [] : [v];
}

export function isEmpty(v: unknown): boolean {
  return valuesOf(v).length === 0;
}

export function fmtDuration(mins: number, hoursPerDay = 8): string {
  if (!Number.isFinite(mins)) return "—";
  const day = hoursPerDay * 60;
  const d = Math.floor(mins / day);
  const h = Math.floor((mins - d * day) / 60);
  const m = Math.round(mins - d * day - h * 60);
  const parts = [d ? `${d}g` : "", h ? `${h}h` : "", m ? `${m}m` : ""].filter(Boolean);
  return parts.join(" ") || "0m";
}

export function fmtMoney(m: Money | null | undefined): string {
  if (!m || typeof m.amount !== "number") return "—";
  try {
    return m.amount.toLocaleString(loc(), { style: "currency", currency: m.currency || "EUR" });
  } catch {
    return `${m.amount.toLocaleString(loc())} ${m.currency}`;
  }
}

function fmtDateTime(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString(loc(), { dateStyle: "medium", timeStyle: "short" });
}

/** Da RFC 3339 al valore di <input type="datetime-local"> (ora locale), e ritorno. */
function toLocalInput(iso: unknown): string {
  if (typeof iso !== "string" || !iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`;
}
function fromLocalInput(v: string): string | null {
  if (!v) return null;
  const d = new Date(v);
  return Number.isNaN(d.getTime()) ? null : d.toISOString();
}

function shortUrl(u: string): string {
  try {
    const x = new URL(u);
    const path = x.pathname === "/" ? "" : x.pathname;
    const s = x.host + path;
    return s.length > 48 ? s.slice(0, 46) + "…" : s;
  } catch {
    return u;
  }
}

export function OptionChip({ o, onRemove }: { o: OptionView; onRemove?: () => void }) {
  const lang = useLang();
  const shown = localName(o.name, o.labels, lang);
  return (
    <span className="inline-flex max-w-full items-center gap-1.5 border border-hair px-1.5 py-px text-[12.5px] leading-[18px]">
      <span className={`size-2 shrink-0 ${COLOR_DOT[o.color ?? ""] ?? "bg-hair"}`} aria-hidden="true" />
      <span className={`truncate ${o.archived ? "text-muted" : ""}`}>{shown}</span>
      {o.archived && <span className="sr-only">{tr("altro.fields.archivedSr")}</span>}
      {onRemove && (
        <button type="button" className="text-muted hover:text-danger" aria-label={tr("altro.fields.remove", { name: shown })} onClick={onRemove}>
          ×
        </button>
      )}
    </span>
  );
}

function Chip({ children, onRemove, label }: { children: ReactNode; onRemove?: () => void; label?: string }) {
  return (
    <span className="inline-flex max-w-full items-center gap-1.5 border border-hair px-1.5 py-px text-[12.5px] leading-[18px]">
      <span className="truncate">{children}</span>
      {onRemove && (
        <button type="button" className="text-muted hover:text-danger" aria-label={tr("altro.fields.remove", { name: label ?? "" })} onClick={onRemove}>
          ×
        </button>
      )}
    </span>
  );
}

/** Valore di un campo in lettura. */
export function FieldValue({ f, value = f.value }: { f: FieldView; value?: unknown }) {
  const vals = valuesOf(value);
  if (vals.length === 0) return <span className="text-muted">—</span>;
  const label = (id: unknown): RefLabel | undefined => f.labels?.[String(id)];
  switch (f.data_type) {
    case "enum":
    case "version":
    case "state":
      return (
        <span className="flex flex-wrap gap-1">
          {vals.map((id) => {
            const o = f.options?.find((x) => x.id === id) ?? { id: String(id), name: tr("altro.fields.deletedOption") };
            return <OptionChip key={String(id)} o={o} />;
          })}
        </span>
      );
    case "member":
    case "group":
      return <span>{vals.map((id) => label(id)?.label ?? "—").join(", ")}</span>;
    case "element":
      return (
        <span className="flex flex-col gap-0.5">
          {vals.map((id) => {
            const l = label(id);
            return l?.key ? (
              <Link key={String(id)} to="/e/$key" params={{ key: l.key }} className="min-w-0 text-fg no-underline hover:underline">
                <span className="font-mono text-[12px] text-accent">{l.key}</span> <span className="break-words">{l.label}</span>
              </Link>
            ) : (
              <span key={String(id)} className="text-muted">{tr("altro.fields.unavailableItem")}</span>
            );
          })}
        </span>
      );
    case "url":
      return (
        <span className="flex flex-col gap-0.5">
          {vals.map((u) => (
            <a key={String(u)} href={String(u)} target="_blank" rel="noopener noreferrer nofollow" className="break-all text-accent underline">
              {shortUrl(String(u))}
            </a>
          ))}
        </span>
      );
    case "date":
      return <span className="tabular">{new Date(String(vals[0]) + "T00:00:00").toLocaleDateString(loc())}</span>;
    case "datetime":
      return <span className="tabular">{fmtDateTime(String(vals[0]))}</span>;
    case "boolean":
      return <span>{vals[0] ? tr("altro.fields.yes") : tr("altro.fields.no")}</span>;
    case "money":
      return <span className="tabular">{fmtMoney(vals[0] as Money)}</span>;
    case "duration":
      return <span className="tabular">{fmtDuration(Number(vals[0]), f.settings.hours_per_day)}</span>;
    case "integer":
    case "decimal":
      return <span className="tabular">{Number(vals[0]).toLocaleString(loc())}</span>;
    case "text":
      return <span className="break-words whitespace-pre-line">{String(vals[0])}</span>;
    default:
      return f.multi ? (
        <span className="flex flex-wrap gap-1">
          {vals.map((v) => (
            <Chip key={String(v)}>{String(v)}</Chip>
          ))}
        </span>
      ) : (
        <span className="break-words">{String(vals[0])}</span>
      );
  }
}

// ---------------------------------------------------------------------
// Controlli di modifica

/** Nomi già noti dei riferimenti del campo (id → nome), per non chiederli al server. */
function refHints(f: FieldView): Record<string, string> {
  return Object.fromEntries(Object.entries(f.labels ?? {}).map(([k, l]) => [k, `${l.key ?? ""} ${l.label}`.trim()]));
}

/** Persone, gruppi ed elementi: selettore comune con ricerca sul server; i multipli diventano chip. */
function RefPicker({ f, value, onChange, autoFocus, id, kind }: EditorProps & { kind: EntityKind }) {
  const types = kind === "element" ? (f.settings.types ?? []) : undefined;
  const allowed = kind === "group" ? (f.settings.groups ?? []).map((g) => g.toLowerCase()) : [];
  const placeholder =
    kind === "element" ? (types?.length ? tr("altro.fields.searchKeyOrTitle") : tr("altro.fields.keyOrTitle")) : kind === "member" && f.settings.group ? tr("altro.fields.searchGroup", { group: f.settings.group }) : undefined;
  const common = {
    kind,
    id,
    autoFocus,
    types,
    placeholder,
    hints: refHints(f),
    memory: `campo.${f.key ?? f.name}`,
    "aria-label": f.name,
    filter: allowed.length ? (o: Opt) => allowed.includes(o.label.toLowerCase()) : undefined,
  };
  if (f.multi) return <EntitySelect {...common} multiple value={valuesOf(value).map(String)} onChange={(v) => onChange(v)} />;
  return <EntitySelect {...common} noneLabel="—" value={valuesOf(value).map(String)[0] ?? ""} onChange={(v) => onChange(v || null)} />;
}

/** Scelta tra opzioni con colori: pulsanti a chip (i multipli si accendono e spengono). */
function ChoicePicker({ f, value, onChange }: EditorProps) {
  const lang = useLang();
  const selected = valuesOf(value).map(String);
  const opts = (f.options ?? []).filter((o) => !o.archived || selected.includes(o.id));
  return (
    <div role="group" aria-label={f.name} className="flex flex-wrap gap-1.5">
      {opts.map((o) => {
        const on = selected.includes(o.id);
        return (
          <button
            key={o.id}
            type="button"
            aria-pressed={on}
            className={`inline-flex items-center gap-1.5 border px-2 py-1 text-[12.5px] ${on ? "border-fg bg-sel font-semibold" : "border-hair hover:border-rule"}`}
            onClick={() => (f.multi ? onChange(on ? selected.filter((x) => x !== o.id) : [...selected, o.id]) : onChange(on ? null : o.id))}
          >
            <span className={`size-2 ${COLOR_DOT[o.color ?? ""] ?? "bg-hair"}`} aria-hidden="true" />
            {localName(o.name, o.labels, lang)}
          </button>
        );
      })}
      {opts.length === 0 && <span className="text-[12.5px] text-muted">{tr("altro.fields.noOptions")}</span>}
    </div>
  );
}

/** Testi multipli (etichette, link): si aggiungono con Invio e diventano chip. */
function MultiText({ f, value, onChange, autoFocus, id }: EditorProps) {
  const [text, setText] = useState("");
  const vals = valuesOf(value).map(String);
  const add = () => {
    const t = text.trim();
    if (t && !vals.includes(t)) onChange([...vals, t]);
    setText("");
  };
  return (
    <div className="flex flex-col gap-1.5">
      {vals.length > 0 && (
        <span className="flex flex-wrap gap-1">
          {vals.map((v) => (
            <Chip key={v} label={v} onRemove={() => onChange(vals.filter((x) => x !== v))}>
              {f.data_type === "url" ? shortUrl(v) : v}
            </Chip>
          ))}
        </span>
      )}
      <input
        id={id}
        className="field"
        type={f.data_type === "url" ? "url" : "text"}
        value={text}
        autoFocus={autoFocus}
        // i18n-ok: tipo di campo, non un testo
        placeholder={f.data_type === "url" ? tr("altro.fields.urlPlaceholder") : tr("altro.fields.textPlaceholder")}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === ",") {
            e.preventDefault();
            add();
          }
        }}
        onBlur={add}
      />
    </div>
  );
}

type EditorProps = { f: FieldView; value: unknown; onChange: (v: unknown) => void; autoFocus?: boolean; id?: string; baseCurrency?: string };

/** Controllo adatto al tipo del campo. Il valore è nella forma accettata dall'API. */
export function FieldEditor(p: EditorProps) {
  const { f, value, onChange, autoFocus, id } = p;
  const num = (s: string) => (s.trim() === "" ? null : Number(s.replace(",", ".")));
  switch (f.data_type) {
    case "enum":
    case "version":
      return <ChoicePicker {...p} />;
    case "member":
      return <RefPicker {...p} kind="member" />;
    case "group":
      return <RefPicker {...p} kind="group" />;
    case "element":
      return <RefPicker {...p} kind="element" />;
    case "boolean":
      return (
        <div role="radiogroup" aria-label={f.name} className="flex gap-1.5">
          {[true, false].map((b) => (
            <button
              key={String(b)}
              type="button"
              role="radio"
              aria-checked={value === b}
              className={`border px-3 py-1 ${value === b ? "border-fg bg-sel font-semibold" : "border-hair hover:border-rule"}`}
              onClick={() => onChange(value === b ? null : b)}
            >
              {b ? tr("altro.fields.yes") : tr("altro.fields.no")}
            </button>
          ))}
        </div>
      );
    case "date":
      return <input id={id} className="field max-w-[200px] tabular" type="date" autoFocus={autoFocus} value={typeof value === "string" ? value : ""} onChange={(e) => onChange(e.target.value || null)} />;
    case "datetime":
      return (
        <input id={id} className="field max-w-[240px] tabular" type="datetime-local" autoFocus={autoFocus} value={toLocalInput(value)} onChange={(e) => onChange(fromLocalInput(e.target.value))} />
      );
    case "integer":
    case "decimal":
      return (
        <input
          id={id}
          className="field max-w-[200px] tabular"
          inputMode={f.data_type === "integer" ? "numeric" : "decimal"}
          autoFocus={autoFocus}
          value={value == null ? "" : String(value)}
          onChange={(e) => onChange(e.target.value === "" ? null : e.target.value)}
        />
      );
    case "duration":
      return (
        <input
          id={id}
          className="field max-w-[200px] tabular"
          autoFocus={autoFocus}
          placeholder={tr("altro.fields.durationPlaceholder")}
          value={typeof value === "number" ? fmtDuration(value, f.settings.hours_per_day) : typeof value === "string" ? value : ""}
          onChange={(e) => onChange(e.target.value || null)}
        />
      );
    case "money": {
      const m = (value && typeof value === "object" ? value : {}) as Partial<Money>;
      const currency = m.currency ?? f.settings.currency ?? p.baseCurrency ?? "EUR";
      return (
        <span className="flex max-w-[280px] gap-1.5">
          <input
            id={id}
            className="field tabular"
            inputMode="decimal"
            autoFocus={autoFocus}
            aria-label={tr("altro.fields.amount", { name: f.name })}
            value={m.amount == null ? "" : String(m.amount)}
            onChange={(e) => {
              const a = num(e.target.value);
              onChange(a == null || Number.isNaN(a) ? (e.target.value ? { amount: e.target.value, currency } : null) : { amount: a, currency });
            }}
          />
          <input
            className="field w-[76px] shrink-0 font-mono uppercase"
            aria-label={tr("altro.fields.currency", { name: f.name })}
            maxLength={3}
            value={currency}
            onChange={(e) => onChange({ amount: m.amount ?? 0, currency: e.target.value.toUpperCase() })}
          />
        </span>
      );
    }
    case "text":
      return <textarea id={id} className="field min-h-24" autoFocus={autoFocus} value={typeof value === "string" ? value : ""} onChange={(e) => onChange(e.target.value)} />;
    case "url":
    case "string":
      if (f.multi) return <MultiText {...p} />;
      return (
        <input
          id={id}
          className="field"
          type={f.data_type === "url" ? "url" : "text"}
          autoFocus={autoFocus}
          // i18n-ok: tipo di campo, non un testo
          placeholder={f.data_type === "url" ? "https://" : undefined}
          value={typeof value === "string" ? value : ""}
          onChange={(e) => onChange(e.target.value)}
        />
      );
    default:
      return <span className="text-muted">{tr("altro.fields.notEditable")}</span>;
  }
}

// ---------------------------------------------------------------------
// Modifica sul posto e pannello del dettaglio

export function useCanWrite(detail: ElementDetail): boolean {
  const me = useMe().data?.active_tenant;
  const perms = me?.permissions ?? [];
  if (perms.includes("*") || perms.includes("element.write")) return true;
  return detail.element.reporter_id === me?.member_id && detail.element.state_category === "todo";
}

/** Salva un campo dell'elemento e aggiorna le viste. */
export function useSaveField(detail: ElementDetail) {
  const qc = useQueryClient();
  const refKey = detail.element.key || detail.element.id;
  return useMutation({
    mutationFn: (patch: { title?: string; body_text?: string; fields?: Record<string, unknown> }) =>
      fieldsApi.updateElement(detail.element.id, detail.element.version, patch),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["element", refKey] });
      qc.invalidateQueries({ queryKey: ["search"] });
      qc.invalidateQueries({ queryKey: ["activity"] });
    },
  });
}

/** Un campo con la modifica sul posto: si apre con un clic, Invio o Salva conferma, Esc annulla. */
export function InlineField({ f, detail, canEdit }: { f: FieldView; detail: ElementDetail; canEdit: boolean }) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState<unknown>(null);
  const save = useSaveField(detail);
  const box = useRef<HTMLFormElement>(null);
  const editable = canEdit && !f.archived;
  const open = () => {
    setDraft(f.value ?? (f.multi ? [] : null));
    save.reset();
    setEditing(true);
  };
  if (!editing) {
    return editable ? (
      <button
        type="button"
        className="-mx-1 flex min-h-7 w-[calc(100%+0.5rem)] items-center px-1 text-left hover:bg-sel/60"
        title={tr("altro.fields.edit")}
        onClick={open}
      >
        {/* Il nome accessibile contiene anche il valore visibile (WCAG 2.5.3, etichetta nel nome). */}
        <span className="sr-only">{tr("altro.fields.editName", { name: f.name })}</span>
        <FieldValue f={f} />
      </button>
    ) : (
      <span className="flex min-h-7 items-center">
        <FieldValue f={f} />
      </span>
    );
  }
  return (
    <form
      ref={box}
      className="flex flex-col gap-1.5 py-1"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate({ fields: { [f.key]: isEmpty(draft) ? null : draft } }, { onSuccess: () => setEditing(false) });
      }}
      onKeyDown={(e) => {
        if (e.key === "Escape") {
          e.stopPropagation();
          setEditing(false);
        }
      }}
    >
      <FieldEditor f={f} value={draft} onChange={setDraft} autoFocus />
      {f.help && <span className="text-[12px] text-muted">{f.help}</span>}
      <span className="flex gap-2">
        <button className="btn btn-primary py-1" disabled={save.isPending}>{tr("altro.fields.save")}</button>
        <button type="button" className="btn py-1" onClick={() => setEditing(false)}>{tr("altro.fields.cancel")}</button>
      </span>
      <ErrorNote error={save.error} />
    </form>
  );
}

/** Etichetta del campo con gli indicatori di obbligo. */
export function FieldLabel({ f }: { f: FieldView }) {
  return (
    <span className="flex flex-col">
      <span>
        {f.name}
        {(f.required_now || f.required) && (
          <span className="text-flag" title={tr("altro.fields.required")} aria-label={tr("altro.fields.requiredLower")}>
            {" "}*
          </span>
        )}
      </span>
      {f.required_from && !f.required_now && (
        <span className="text-[11.5px] text-muted">{tr("altro.fields.requiredFrom", { from: f.required_from })}</span>
      )}
      {f.archived && <span className="text-[11.5px] text-muted">{tr("altro.fields.archived")}</span>}
    </span>
  );
}

export function groupBySection(fields: FieldView[]): [string, FieldView[]][] {
  const out = new Map<string, FieldView[]>();
  for (const f of fields) {
    const k = f.section || "";
    out.set(k, [...(out.get(k) ?? []), f]);
  }
  return [...out.entries()];
}

/** Campi personalizzati dell'elemento, raggruppati per sezione, con la modifica sul posto. */
export function CustomFields({ detail, only, emptyNote = true }: { detail: ElementDetail; only?: number; emptyNote?: boolean }) {
  const canEdit = useCanWrite(detail);
  let fields = (detail.custom_fields ?? []).filter((f) => f.visible);
  if (only != null) fields = fields.filter((f) => !isEmpty(f.value) || f.required_now).slice(0, only);
  if (fields.length === 0) return emptyNote ? <p className="m-0 text-[12.5px] text-muted">{tr("altro.fields.none")}</p> : null;
  const groups = groupBySection(fields);
  return (
    <div className="flex flex-col gap-3">
      {groups.map(([section, list]) => (
        <div key={section} className="flex flex-col">
          {section && groups.length > 1 && <span className="label pb-1 text-muted">{section}</span>}
          <dl className="m-0 grid grid-cols-[minmax(84px,38%)_minmax(0,1fr)]">
            {list.map((f) => {
              const missing = f.required_now && isEmpty(f.value);
              return (
                <div key={f.key} className="contents">
                  <dt className="border-b border-hair py-1.5 pr-2 text-muted">
                    <FieldLabel f={f} />
                  </dt>
                  <dd className="m-0 min-w-0 border-b border-hair py-1">
                    {only != null ? <span className="flex min-h-7 items-center"><FieldValue f={f} /></span> : <InlineField f={f} detail={detail} canEdit={canEdit} />}
                    {missing && <span className="block text-[11.5px] font-semibold text-flag">{tr("altro.fields.toFill")}</span>}
                  </dd>
                </div>
              );
            })}
          </dl>
        </div>
      ))}
    </div>
  );
}

// ---------------------------------------------------------------------
// Form di creazione

/** Fatti per la visibilità nel form: i valori dei campi (opzioni per nome) più quelli dati. */
export function formFacts(fields: FieldView[], values: Record<string, unknown>, extra: Facts = {}): Facts {
  const out: Facts = { ...extra };
  for (const f of fields) {
    const vals = valuesOf(values[f.key]);
    if (!vals.length) continue;
    out[f.key] =
      f.data_type === "enum" || f.data_type === "version"
        ? vals.map((id) => f.options?.find((o) => o.id === id)?.name ?? id)
        : f.data_type === "money"
          ? vals.map((m) => (m as Money).amount)
          : vals;
  }
  return out;
}

export function visibleIn(f: FieldView, facts: Facts): boolean {
  return !f.visible_when || evalCondition(compiled(f.visible_when), facts);
}

/** Campi personalizzati del tipo nel form di creazione, per sezione, con la visibilità condizionale. */
export function CustomFieldsForm({
  fields,
  values,
  onChange,
  facts,
  exclude = [],
}: {
  fields: FieldView[];
  values: Record<string, unknown>;
  onChange: (key: string, v: unknown) => void;
  facts: Facts;
  exclude?: string[];
}) {
  const shown = fields.filter((f) => !exclude.includes(f.key) && visibleIn(f, facts));
  if (shown.length === 0) return null;
  return (
    <>
      {groupBySection(shown).map(([section, list]) => (
        <fieldset key={section} className="m-0 grid grid-cols-1 gap-x-8 gap-y-5 border-0 border-t border-hair p-0 pt-4 md:col-span-2 md:grid-cols-2">
          {section && <legend className="label float-left mb-1 w-full p-0 md:col-span-2">{section}</legend>}
          {list.map((f) => {
            const id = `campo-${f.key}`;
            const wide = f.data_type === "text" || f.multi || f.data_type === "enum";
            return (
              <div key={f.key} className={`flex flex-col gap-1.5 ${wide ? "md:col-span-2" : ""}`}>
                <label htmlFor={id} className="label">
                  {f.name}
                  {f.required_now && <span className="text-flag"> *</span>}
                </label>
                <FieldEditor f={f} value={values[f.key]} onChange={(v) => onChange(f.key, v)} id={id} />
                {(f.help || f.required_from) && (
                  <span className="text-[12px] text-muted">
                    {f.help}
                    {f.help && f.required_from ? " · " : ""}
                    {f.required_from && tr("altro.fields.requiredFrom", { from: f.required_from })}
                  </span>
                )}
              </div>
            );
          })}
        </fieldset>
      ))}
    </>
  );
}

/** Valori iniziali del form: i predefiniti dei campi. */
export function defaultsOf(fields: FieldView[]): Record<string, unknown> {
  return Object.fromEntries(fields.filter((f) => !isEmpty(f.default)).map((f) => [f.key, f.default]));
}

/** Valori da inviare: senza i vuoti e senza i campi nascosti dalla condizione. */
export function payloadOf(fields: FieldView[], values: Record<string, unknown>, facts: Facts): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const f of fields) {
    const v = values[f.key];
    if (!visibleIn(f, facts)) continue;
    if (isEmpty(v)) {
      // Il predefinito svuotato di proposito: lo si dice al server, che altrimenti lo applica.
      if (!isEmpty(f.default)) out[f.key] = null;
      continue;
    }
    out[f.key] = v;
  }
  return out;
}
