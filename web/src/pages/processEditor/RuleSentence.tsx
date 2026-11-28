import { EntitySelect, Select } from "../../select";
import { Link } from "@tanstack/react-router";
import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { processEditorApi, type Definition, type ProcessMeta, type Rule, type RuleAction } from "../../api";
import { casaApi, useDebounced } from "../../casaApi";
import { fmtDate, fmtDateTime } from "../../i18n/format";
import { requirementsApi } from "../../requirementsApi";
import { reviewsApi } from "../../reviewsApi";
import { useT } from "../../i18n/react";
import type { Key } from "../../i18n/core";
import { ConditionBuilder } from "./ConditionBuilder";
import {
  MAX_OFFSET_DAYS,
  SENTENCE_ACTIONS,
  WEEKDAYS,
  changeFacts,
  eventPhrase,
  fieldName,
  formatEvent,
  isSentence,
  isTimed,
  parseEvent,
  type Names,
  type RuleEvent,
  type RuleRefs,
  type SentenceAction,
} from "./rulePhrases";

// Regole a frase (replica/usabilita.md, proposta 6): «Quando [evento] solo se [condizione] allora
// [azioni]». Il formato salvato è quello di sempre (Rule: on, when, actions): qui cambia solo il
// modo di leggerlo e di compilarlo. Le azioni offerte sono quelle che il motore esegue davvero.
// Le frasi (eventi, azioni, regola intera) stanno in rulePhrases.ts, provate con `pnpm test`.

export { SENTENCE_ACTIONS, actionPhrase, eventPhrase, ruleSentence, type RuleRefs } from "./rulePhrases";

/** Nomi dei tipi di valutazione e dei cataloghi di requisiti a cui le azioni rimandano per chiave. */
export function useRuleRefs(): RuleRefs {
  const rev = useQuery({ queryKey: ["review-config"], queryFn: reviewsApi.config, retry: false, staleTime: 60_000 });
  const cat = useQuery({ queryKey: ["requirement-config"], queryFn: requirementsApi.config, retry: false, staleTime: 60_000 });
  return {
    reviewTypes: (rev.data?.types ?? []).map((x) => ({ key: x.key, name: x.name, scope: x.scope })),
    catalogs: (cat.data?.catalogs ?? []).map((x) => ({ key: x.key, name: x.name })),
    loaded: !rev.isPending,
  };
}

/** I nomi dei metadati del processo con cui si leggono eventi e azioni. */
export function metaNames(meta: ProcessMeta): Names {
  return {
    roles: meta.roles,
    schemas: meta.actions,
    facts: Object.fromEntries(meta.facts.map((f) => [f.key, f.name])),
    fields: meta.fields,
    dates: meta.dates,
    types: meta.types,
    relations: meta.relations,
    spaces: meta.spaces,
  };
}

function Part({ word, children }: { word?: string; children: ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-1.5">
      {word && <span className="label">{word}</span>}
      {children}
    </div>
  );
}

/** L'editor di una regola a frase: «Quando… solo se… allora…». */
export function RuleSentence({
  rule,
  def,
  meta,
  refs,
  extraValues,
  setRule,
  renderOther,
  processKey,
}: {
  rule: Rule;
  def: Definition;
  meta: ProcessMeta;
  refs: RuleRefs;
  extraValues: Record<string, string[]>;
  /** Modifica la regola; `key` distingue le modifiche per l'annulla/ripeti. */
  setRule: (fn: (r: Rule) => void, key: string) => void;
  /** Azioni che non si leggono a frase: si modificano a campi. */
  renderOther: (a: RuleAction, j: number) => ReactNode;
  /** Chiave del processo, se la regola è già pubblicata: le regole a tempo mostrano le ultime esecuzioni. */
  processKey?: string;
}) {
  const t = useT();
  const eventId = useId();
  const names = metaNames(meta);
  const kinds = SENTENCE_ACTIONS.filter((k) => meta.actions.some((s) => s.kind === k));
  const ev = parseEvent(rule.on);
  // Il menu dell'evento sceglie il genere; i dettagli (campo, giorni, data, giorno della settimana) stanno accanto.
  const choice = ev.kind === "change" ? "change" : ev.kind === "at" ? "at" : ev.kind === "every" ? (ev.weekday ? "every:week" : "every:day") : rule.on;
  const known = ev.kind === "create" || ev.kind === "change" || ev.kind === "at" || ev.kind === "every" || def.phases.some((p) => rule.on === `enter:${p.name}` || rule.on === `exit:${p.name}`);
  const setOn = (on: string) => setRule((x) => (x.on = on), "on");
  const choose = (v: string) => {
    if (v === choice) return;
    if (v === "change") setOn(formatEvent({ kind: "change", field: meta.fields?.[0]?.key ?? "titolo" }));
    else if (v === "at") setOn(formatEvent({ kind: "at", date: meta.dates?.[0]?.key ?? "scadenza", offset: -7 }));
    else if (v === "every:week") setOn("every:week:1");
    else setOn(v);
  };
  const facts = ev.kind === "change" ? [...meta.facts, ...changeFacts(t, rule.on, meta.facts, names)] : meta.facts;
  return (
    <div className="flex flex-col gap-4" data-testid="regola-frase">
      <Part word={t("configurazione.regole.quando")}>
        <Select id={eventId} aria-label={t("configurazione.regole.evento.label")} className="field" value={choice} onChange={(e) => choose(e.target.value)}>
          <option value="create">{t("configurazione.regole.evento.create")}</option>
          <option value="change">{t("configurazione.regole.evento.sceltaChange")}</option>
          {!known && <option value={rule.on}>{eventPhrase(t, rule.on, names)}</option>}
          <optgroup label={t("configurazione.regole.evento.gruppoEnter")}>
            {def.phases.map((p) => (
              <option key={p.name} value={`enter:${p.name}`}>
                {t("configurazione.regole.evento.enter", { phase: p.name })}
              </option>
            ))}
          </optgroup>
          <optgroup label={t("configurazione.regole.evento.gruppoExit")}>
            {def.phases.map((p) => (
              <option key={p.name} value={`exit:${p.name}`}>
                {t("configurazione.regole.evento.exit", { phase: p.name })}
              </option>
            ))}
          </optgroup>
          <optgroup label={t("configurazione.regole.evento.gruppoTempo")}>
            <option value="at">{t("configurazione.regole.evento.sceltaAt")}</option>
            <option value="every:day">{t("configurazione.regole.evento.sceltaEveryDay")}</option>
            <option value="every:week">{t("configurazione.regole.evento.sceltaEveryWeek")}</option>
          </optgroup>
        </Select>
        <EventDetails ev={ev} meta={meta} setOn={setOn} />
        {(ev.kind === "at" || ev.kind === "every") && <p className="m-0 text-[12px] text-muted">{t("configurazione.regole.evento.tempoHint")}</p>}
      </Part>

      <ConditionBuilder
        label={t("configurazione.regole.soloSe")}
        value={rule.when ?? ""}
        onChange={(v) => setRule((x) => (x.when = v || undefined), "when")}
        facts={facts}
        type={meta.type.key}
        sample={meta.sample}
        empty={t("configurazione.processEditor.laRegolaValeSempre")}
        extraValues={extraValues}
      />
      <Preview on={rule.on} when={rule.when ?? ""} type={meta.type.key} />
      {processKey && rule.key && isTimed(rule.on) && <RuleRuns key={`${processKey}/${rule.key}`} processKey={processKey} ruleKey={rule.key} />}

      <Part word={t("configurazione.regole.allora")}>
        <ul className="m-0 flex list-none flex-col gap-3 p-0">
          {rule.actions.map((a, j) => (
            <li key={j} className="flex flex-col gap-2 border-l-2 border-hair pl-3" data-testid="regola-azione">
              {isSentence(a.kind) && (kinds as readonly string[]).includes(a.kind) ? (
                <SentenceActionRow
                  action={a}
                  on={rule.on}
                  meta={meta}
                  refs={refs}
                  kinds={kinds}
                  onChange={(na) => setRule((x) => (x.actions[j] = na), `a${j}`)}
                  onRemove={() => setRule((x) => x.actions.splice(j, 1), `a${j}-rm`)}
                />
              ) : (
                renderOther(a, j)
              )}
            </li>
          ))}
        </ul>
        <Select
          aria-label={t("configurazione.regole.aggiungiAzione")}
          className="field w-auto self-start"
          value=""
          onChange={(e) => e.target.value && setRule((x) => x.actions.push({ kind: e.target.value }), "a-add")}
        >
          <option value="">{t("configurazione.regole.aggiungiAzione")}</option>
          {kinds.map((k) => (
            <option key={k} value={k}>
              {t(`configurazione.regole.tipo.${k}`)}
            </option>
          ))}
        </Select>
      </Part>
    </div>
  );
}

/** I dettagli dell'evento: il campo che cambia, i giorni prima o dopo una data, il giorno della settimana. */
function EventDetails({ ev, meta, setOn }: { ev: RuleEvent; meta: ProcessMeta; setOn: (on: string) => void }) {
  const t = useT();
  if (ev.kind === "change") {
    const fields = meta.fields ?? [];
    const unknown = ev.field !== "titolo" && !fields.some((f) => f.key === ev.field);
    return (
      <Select aria-label={t("configurazione.regole.evento.campo")} className="field" value={ev.field} onChange={(e) => setOn(formatEvent({ kind: "change", field: e.target.value }))}>
        <option value="titolo">{t("configurazione.regole.campoTitolo")}</option>
        {unknown && <option value={ev.field}>{ev.field}</option>}
        {fields.map((f) => (
          <option key={f.key} value={f.key}>
            {f.name}
          </option>
        ))}
      </Select>
    );
  }
  if (ev.kind === "at") return <AtDetails ev={ev} meta={meta} setOn={setOn} />;
  if (ev.kind === "every" && ev.weekday)
    return (
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-[13px]">{t("configurazione.regole.evento.ilGiorno")}</span>
        <Select
          aria-label={t("configurazione.regole.evento.giornoSettimana")}
          className="field w-auto"
          value={String(ev.weekday)}
          onChange={(e) => setOn(formatEvent({ kind: "every", weekday: Number(e.target.value) }))}
        >
          {WEEKDAYS.map((k, i) => (
            <option key={k} value={String(i + 1)}>
              {t(k)}
            </option>
          ))}
        </Select>
      </div>
    );
  return null;
}

/** «[N] [giorni prima di | giorni dopo | il giorno stesso di] [data]». */
function AtDetails({ ev, meta, setOn }: { ev: Extract<RuleEvent, { kind: "at" }>; meta: ProcessMeta; setOn: (on: string) => void }) {
  const t = useT();
  const dir = ev.offset < 0 ? "prima" : ev.offset > 0 ? "dopo" : "giorno";
  const abs = Math.abs(ev.offset);
  // Il numero si scrive liberamente (anche vuoto mentre si digita); la regola cambia solo con un numero valido.
  const [text, setText] = useState(String(abs || 1));
  useEffect(() => {
    if (abs && Number(text) !== abs) setText(String(abs));
  }, [abs]); // eslint-disable-line react-hooks/exhaustive-deps
  const n = abs || Number.parseInt(text, 10) || 1;
  const set = (d: string, days: number, date: string) => setOn(formatEvent({ kind: "at", date, offset: d === "giorno" ? 0 : d === "prima" ? -days : days }));
  const dates = meta.dates ?? [];
  return (
    <div className="flex flex-wrap items-center gap-2">
      {dir !== "giorno" && (
        <input
          type="number"
          inputMode="numeric"
          min={1}
          max={MAX_OFFSET_DAYS}
          step={1}
          className="field w-24"
          aria-label={t("configurazione.regole.evento.giorni")}
          value={text}
          onChange={(e) => {
            setText(e.target.value);
            const v = Number(e.target.value);
            if (Number.isInteger(v) && v >= 1 && v <= MAX_OFFSET_DAYS) set(dir, v, ev.date);
          }}
        />
      )}
      <Select aria-label={t("configurazione.regole.evento.verso")} className="field w-auto" value={dir} onChange={(e) => set(e.target.value, n, ev.date)}>
        <option value="prima">{t("configurazione.regole.evento.versoPrima")}</option>
        <option value="dopo">{t("configurazione.regole.evento.versoDopo")}</option>
        <option value="giorno">{t("configurazione.regole.evento.versoGiorno")}</option>
      </Select>
      <Select aria-label={t("configurazione.regole.evento.data")} className="field w-auto min-w-0" value={ev.date} onChange={(e) => set(dir, n, e.target.value)}>
        {!dates.some((d) => d.key === ev.date) && <option value={ev.date}>{ev.date}</option>}
        {dates.map((d) => (
          <option key={d.key} value={d.key}>
            {d.name}
          </option>
        ))}
      </Select>
    </div>
  );
}

/** Anteprima sotto la condizione: per gli eventi a tempo i prossimi giorni o il prossimo giro, per gli altri oggi. */
function Preview({ on, when, type }: { on: string; when: string; type: string }) {
  return isTimed(on) ? <TimedPreview on={on} when={when} type={type} /> : <CountPreview when={when} type={type} />;
}

/** «Oggi questa regola scatterebbe su n elementi», dal conteggio dal vivo della condizione. */
function CountPreview({ when, type }: { when: string; type: string }) {
  const t = useT();
  const debounced = useDebounced(when.trim());
  const q = useQuery({ queryKey: ["regola-conto", type, debounced], queryFn: () => casaApi.count(debounced, type), enabled: !!debounced, retry: false });
  if (!debounced) return null;
  if (when.trim() !== debounced || q.isFetching) return <p className="m-0 text-[12.5px] text-muted">{t("configurazione.regole.anteprimaConto")}</p>;
  if (q.isError || (q.data && !q.data.ok)) return null;
  if (!q.data) return null;
  return (
    <p className="m-0 text-[12.5px]" data-testid="regola-anteprima">
      {t(q.data.truncated ? "configurazione.regole.anteprimaAlmeno" : "configurazione.regole.anteprima", { count: q.data.matched })}
    </p>
  );
}

/** Giorni dell'anteprima delle regole «at:». */
const PREVIEW_DAYS = 30;

/** Giorno e mese di una data AAAA-MM-GG (nel fuso del tenant: si legge come data locale). */
function dayMonth(d: string | undefined): string {
  const m = d ? /^(\d{4})-(\d{2})-(\d{2})/.exec(d) : null;
  return m ? fmtDate(new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3])), { day: "2-digit", month: "2-digit" }) : fmtDate(d);
}

/** «Nei prossimi 30 giorni scatterebbe su n elementi» (at:), «Al prossimo giro (gg/mm) scatterebbe su n elementi» (every:). */
function TimedPreview({ on, when, type }: { on: string; when: string; type: string }) {
  const t = useT();
  const input = JSON.stringify([on, when.trim()]);
  const debounced = useDebounced(input);
  const [dOn, dWhen] = JSON.parse(debounced) as [string, string];
  const q = useQuery({
    queryKey: ["regola-anteprima", type, dOn, dWhen],
    queryFn: () => processEditorApi.rulePreview({ type, on: dOn, condition: dWhen, days: PREVIEW_DAYS }),
    retry: false,
  });
  if (input !== debounced || q.isFetching) return <p className="m-0 text-[12.5px] text-muted">{t("configurazione.regole.anteprimaConto")}</p>;
  if (q.isError || !q.data || !q.data.ok) return null;
  const d = q.data;
  const text =
    parseEvent(dOn).kind === "every"
      ? t(d.truncated ? "configurazione.regole.anteprimaEveryAlmeno" : "configurazione.regole.anteprimaEvery", { count: d.matched, date: dayMonth(d.from) })
      : t(d.truncated ? "configurazione.regole.anteprimaAtAlmeno" : "configurazione.regole.anteprimaAt", { count: d.matched, days: PREVIEW_DAYS });
  return (
    <p className="m-0 text-[12.5px]" data-testid="regola-anteprima">
      {text}
    </p>
  );
}

const OUTCOMES = { ok: "configurazione.regole.esito.ok", error: "configurazione.regole.esito.error", skipped: "configurazione.regole.esito.skipped" } as const;

/** «Ultime esecuzioni» di una regola a tempo pubblicata: si caricano quando si apre il riquadro. */
function RuleRuns({ processKey, ruleKey }: { processKey: string; ruleKey: string }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const q = useQuery({ queryKey: ["regola-esecuzioni", processKey, ruleKey], queryFn: () => processEditorApi.ruleRuns(processKey, ruleKey, 20), enabled: open, retry: false });
  const items = q.data?.items ?? [];
  return (
    <details className="text-[13px]" data-testid="regola-esecuzioni" onToggle={(e) => setOpen(e.currentTarget.open)}>
      <summary className="min-h-11 cursor-pointer py-2 text-muted">{t("configurazione.regole.esecuzioni")}</summary>
      {q.isPending ? (
        <p className="m-0 text-muted">{t("configurazione.regole.esecuzioniCarico")}</p>
      ) : q.isError ? (
        <p className="m-0 text-danger">{t("configurazione.regole.esecuzioniNo")}</p>
      ) : items.length === 0 ? (
        <p className="m-0 text-muted">{t("configurazione.regole.esecuzioniVuote")}</p>
      ) : (
        <ul className="m-0 flex list-none flex-col p-0">
          {items.map((r, i) => (
            <li key={i} className="flex flex-col gap-0.5 border-b border-hair py-1.5" data-testid="regola-esecuzione">
              <span className="flex min-w-0 flex-wrap items-baseline gap-x-2">
                <Link to="/e/$key" params={{ key: r.element_key }} className="font-mono text-[12.5px] text-accent">
                  {r.element_key}
                </Link>
                <span className="min-w-0 [overflow-wrap:anywhere]">{r.element_title}</span>
              </span>
              <span className="flex flex-wrap gap-x-3 text-[12px] text-muted">
                <span>
                  {t("configurazione.regole.esecuzioniOccorrenza")}: {r.occurrence}
                </span>
                <span className={r.status === "error" ? "text-danger" : undefined}>
                  {t("configurazione.regole.esecuzioniEsito")}: {t(OUTCOMES[r.status] ?? OUTCOMES.ok)}
                </span>
                <span>
                  {t("configurazione.regole.esecuzioniQuando")}: {fmtDateTime(r.fired_at)}
                </span>
              </span>
              {r.note && (
                <span className="text-[12px] [overflow-wrap:anywhere]">
                  {t("configurazione.regole.esecuzioniNota")}: {r.note}
                </span>
              )}
            </li>
          ))}
        </ul>
      )}
    </details>
  );
}

const TEMPLATE_LABELS: Record<string, Key> = {
  titolo: "configurazione.regole.segnaposto.titolo",
  chiave: "configurazione.regole.segnaposto.chiave",
  fase: "configurazione.regole.segnaposto.fase",
  tipo: "configurazione.regole.segnaposto.tipo",
  spazio: "configurazione.regole.segnaposto.spazio",
  data: "configurazione.regole.segnaposto.data",
  regola: "configurazione.regole.segnaposto.regola",
  link: "configurazione.regole.segnaposto.link",
};

/** Un testo da modello con accanto «Inserisci un dato», che aggiunge un segnaposto {{…}} dove sta il cursore. */
function TemplateField({ label, value, onChange, meta, on, multiline }: { label: string; value: string; onChange: (v: string) => void; meta: ProcessMeta; on: string; multiline?: boolean }) {
  const t = useT();
  const id = useId();
  const ref = useRef<HTMLInputElement & HTMLTextAreaElement>(null);
  const caret = useRef<[number, number] | null>(null);
  const keep = () => {
    const el = ref.current;
    if (el) caret.current = [el.selectionStart ?? el.value.length, el.selectionEnd ?? el.value.length];
  };
  const insert = (name: string) => {
    const tag = `{{${name}}}`;
    const [a, b] = caret.current ?? [value.length, value.length];
    onChange(value.slice(0, a) + tag + value.slice(b));
    caret.current = [a + tag.length, a + tag.length];
  };
  const ev = parseEvent(on);
  const fields = meta.fields ?? [];
  const changed = ev.kind === "change" ? ev.field : "";
  const props = { id, ref, className: "field", value, onChange: (e: { target: { value: string } }) => onChange(e.target.value), onSelect: keep, onKeyUp: keep, onClick: keep };
  return (
    <div className="flex flex-col gap-1">
      <label htmlFor={id} className="label">
        {label}
      </label>
      {multiline ? <textarea rows={3} {...props} /> : <input {...props} />}
      <Select aria-label={t("configurazione.regole.inserisciDato")} className="field w-auto self-start" value="" onChange={(e) => e.target.value && insert(e.target.value)}>
        <option value="">{t("configurazione.regole.inserisciDato")}</option>
        {(meta.templates ?? []).map((n) => (
          <option key={n} value={n}>
            {TEMPLATE_LABELS[n] ? t(TEMPLATE_LABELS[n]) : `{{${n}}}`}
          </option>
        ))}
        {fields.length > 0 && (
          <optgroup label={t("configurazione.regole.campiDelTipo")}>
            {fields.map((f) => (
              <option key={f.key} value={`campo.${f.key}`}>
                {f.name}
              </option>
            ))}
          </optgroup>
        )}
        {changed && (
          <optgroup label={t("configurazione.regole.valoriPrima")}>
            <option value={`prima.${changed}`}>{t("configurazione.regole.evento.prima", { name: fieldName(t, changed, { fields }) })}</option>
          </optgroup>
        )}
      </Select>
    </div>
  );
}

function SentenceActionRow({
  action,
  on,
  meta,
  refs,
  kinds,
  onChange,
  onRemove,
}: {
  action: RuleAction;
  /** Evento della regola: con «change:» i modelli offrono anche il valore precedente. */
  on: string;
  meta: ProcessMeta;
  refs: RuleRefs;
  kinds: readonly SentenceAction[];
  onChange: (a: RuleAction) => void;
  onRemove: () => void;
}) {
  const t = useT();
  const kind = action.kind as SentenceAction;
  const withKey = (k: string, v: unknown) => {
    const n: Record<string, unknown> = { ...action };
    if (v === "" || v === undefined || v === false) delete n[k];
    else n[k] = v;
    onChange(n as RuleAction);
  };
  // Parametri in `params`: un valore vuoto toglie la voce.
  const p = action.params ?? {};
  const setParams = (patch: Record<string, string | undefined>) => {
    const params: Record<string, string> = { ...p };
    for (const [k, v] of Object.entries(patch)) {
      if (v === undefined || v === "") delete params[k];
      else params[k] = v;
    }
    const n: RuleAction = { ...action, params };
    if (!Object.keys(params).length) delete n.params;
    onChange(n);
  };
  const reviewType = refs.reviewTypes.find((x) => x.key === action.ref);
  const impactScope = reviewType?.scope === "impact";
  const unknownRef = (list: { key: string }[]) => !!action.ref && !list.some((x) => x.key === action.ref);
  const unknownIn = (list: { key: string }[] | undefined, v: string | undefined) => !!v && !(list ?? []).some((x) => x.key === v);
  const fields = meta.fields ?? [];
  const field = fields.find((f) => f.key === p.field);
  const mode = p.mode === "today" || p.mode === "clear" ? p.mode : "value";
  const datelike = !field || field.kind === "date" || field.kind === "datetime";
  const templ = { meta, on };
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-start gap-2">
        <Select
          aria-label={t("configurazione.regole.tipo.label")}
          className="field min-w-0 flex-1"
          value={kind}
          onChange={(e) => onChange({ kind: e.target.value })}
        >
          {kinds.map((k) => (
            <option key={k} value={k}>
              {t(`configurazione.regole.tipo.${k}`)}
            </option>
          ))}
        </Select>
        <button type="button" className="btn min-h-11 shrink-0 px-3" aria-label={t("configurazione.regole.togliAzione")} title={t("configurazione.regole.togliAzione")} onClick={onRemove}>
          ×
        </button>
      </div>

      {kind === "open_review" && (
        <>
          <Select aria-label={t("configurazione.regole.tipoValutazione")} className="field" value={action.ref ?? ""} onChange={(e) => withKey("ref", e.target.value)}>
            <option value="">{t("configurazione.regole.scegli")}</option>
            {unknownRef(refs.reviewTypes) && <option value={action.ref}>{action.ref}</option>}
            {refs.reviewTypes.map((x) => (
              <option key={x.key} value={x.key}>
                {x.name}
              </option>
            ))}
          </Select>
          {impactScope && <ImpactCondition value={action.when ?? ""} onChange={(v) => withKey("when", v)} />}
        </>
      )}

      {kind === "apply_baseline" && (
        <>
          <Select aria-label={t("configurazione.regole.catalogo")} className="field" value={action.ref ?? ""} onChange={(e) => withKey("ref", e.target.value)}>
            <option value="">{t("configurazione.regole.scegli")}</option>
            {unknownRef(refs.catalogs) && <option value={action.ref}>{action.ref}</option>}
            {refs.catalogs.map((x) => (
              <option key={x.key} value={x.key}>
                {x.name}
              </option>
            ))}
          </Select>
          <ImpactCondition value={action.when ?? ""} onChange={(v) => withKey("when", v)} />
        </>
      )}

      {kind === "assign_role" && (
        <>
          <Select aria-label={t("configurazione.regole.ruolo")} className="field" value={action.role ?? ""} onChange={(e) => withKey("role", e.target.value)}>
            <option value="">{t("configurazione.regole.scegli")}</option>
            {meta.roles.map((r) => (
              <option key={r.key} value={r.key}>
                {r.name}
              </option>
            ))}
          </Select>
          <Select
            aria-label={t("configurazione.regole.assegnaA")}
            className="field"
            value={action.member ? "member" : "group"}
            onChange={(e) => {
              const n: RuleAction = { ...action };
              delete n.group;
              delete n.member;
              onChange(e.target.value === "member" ? { ...n, member: "" } : n);
            }}
          >
            <option value="group">{t("configurazione.regole.aUnGruppo")}</option>
            <option value="member">{t("configurazione.regole.aUnMembro")}</option>
          </Select>
          {action.member !== undefined ? (
            <EntitySelect kind="member" noneLabel={t("configurazione.regole.scegli")} aria-label={t("configurazione.regole.membro")} className="field" value={action.member} onChange={(v) => withKey("member", v)} />
          ) : (
            <GroupSelect meta={meta} value={action.group} none={t("configurazione.regole.scegli")} onChange={(v) => withKey("group", v)} />
          )}
          <label className="inline-flex min-h-11 items-center gap-2 text-[13px]">
            <input type="checkbox" className="size-5" checked={!!action.replace} onChange={(e) => withKey("replace", e.target.checked)} />
            {t("configurazione.regole.sostituisce")}
          </label>
        </>
      )}

      {kind === "set_field" && (
        <>
          <Select aria-label={t("configurazione.regole.campo")} className="field" value={p.field ?? ""} onChange={(e) => setParams({ field: e.target.value })}>
            <option value="">{t("configurazione.regole.scegli")}</option>
            <option value="titolo">{t("configurazione.regole.campoTitolo")}</option>
            {p.field !== "titolo" && unknownIn(fields, p.field) && <option value={p.field}>{p.field}</option>}
            {fields.map((f) => (
              <option key={f.key} value={f.key}>
                {f.name}
              </option>
            ))}
          </Select>
          <Select
            aria-label={t("configurazione.regole.modo")}
            className="field"
            value={mode}
            onChange={(e) => {
              const m = e.target.value;
              setParams({ mode: m === "value" ? undefined : m, value: m === "value" ? p.value : undefined, days: m === "today" ? p.days : undefined });
            }}
          >
            <option value="value">{t("configurazione.regole.modoValore")}</option>
            {(datelike || mode === "today") && <option value="today">{t("configurazione.regole.modoOggi")}</option>}
            <option value="clear">{t("configurazione.regole.modoSvuota")}</option>
          </Select>
          {mode === "value" && <TemplateField label={t("configurazione.regole.valore")} value={p.value ?? ""} onChange={(v) => setParams({ value: v })} {...templ} />}
          {mode === "today" && (
            <label className="flex flex-col gap-1">
              <span className="label">{t("configurazione.regole.giorniDaOggi")}</span>
              <input
                type="number"
                step={1}
                min={-MAX_OFFSET_DAYS}
                max={MAX_OFFSET_DAYS}
                className="field w-28"
                placeholder="0"
                value={p.days ?? ""}
                onChange={(e) => setParams({ days: e.target.value === "0" ? undefined : e.target.value })}
              />
              <span className="text-[11.5px] text-muted">{t("configurazione.regole.giorniDaOggiHint")}</span>
            </label>
          )}
        </>
      )}

      {kind === "notify" && (
        <>
          <TemplateField label={t("configurazione.regole.testoNotifica")} multiline value={p.message ?? ""} onChange={(v) => setParams({ message: v })} {...templ} />
          <fieldset className="m-0 flex min-w-0 flex-col gap-2 border-0 p-0">
            <legend className="label mb-1.5">{t("configurazione.regole.destinatari")}</legend>
            <EntitySelect kind="member" noneLabel={t("configurazione.regole.nessuno")} aria-label={t("configurazione.regole.membro")} className="field" value={action.member ?? ""} onChange={(v) => withKey("member", v)} />
            <GroupSelect meta={meta} value={action.group} none={t("configurazione.regole.nessuno")} onChange={(v) => withKey("group", v)} />
            <Select aria-label={t("configurazione.regole.ruoloElemento")} className="field" value={action.role ?? ""} onChange={(e) => withKey("role", e.target.value)}>
              <option value="">{t("configurazione.regole.nessuno")}</option>
              {unknownIn(meta.roles, action.role) && <option value={action.role}>{action.role}</option>}
              {meta.roles.map((r) => (
                <option key={r.key} value={r.key}>
                  {r.name}
                </option>
              ))}
            </Select>
            <label className="inline-flex min-h-11 items-center gap-2 text-[13px]">
              <input type="checkbox" className="size-5" checked={p.followers === "true"} onChange={(e) => setParams({ followers: e.target.checked ? "true" : undefined })} />
              {t("configurazione.regole.seguaci")}
            </label>
          </fieldset>
        </>
      )}

      {kind === "add_comment" && <TemplateField label={t("configurazione.regole.testoCommento")} multiline value={p.text ?? ""} onChange={(v) => setParams({ text: v })} {...templ} />}

      {kind === "create_linked" && (
        <>
          <Select aria-label={t("configurazione.regole.tipoElemento")} className="field" value={p.type ?? ""} onChange={(e) => setParams({ type: e.target.value })}>
            <option value="">{t("configurazione.regole.scegli")}</option>
            {unknownIn(meta.types, p.type) && <option value={p.type}>{p.type}</option>}
            {(meta.types ?? []).map((x) => (
              <option key={x.key} value={x.key}>
                {x.name}
              </option>
            ))}
          </Select>
          <TemplateField label={t("configurazione.regole.titoloNuovo")} value={p.title ?? ""} onChange={(v) => setParams({ title: v })} {...templ} />
          <Select aria-label={t("configurazione.regole.relazione")} className="field" value={p.relation ?? ""} onChange={(e) => setParams({ relation: e.target.value })}>
            <option value="">{t("configurazione.regole.scegli")}</option>
            {unknownIn(meta.relations, p.relation) && <option value={p.relation}>{p.relation}</option>}
            {(meta.relations ?? []).map((x) => (
              <option key={x.key} value={x.key}>
                {x.name}
              </option>
            ))}
          </Select>
          <Select aria-label={t("configurazione.regole.verso")} className="field" value={p.direction === "in" ? "in" : "out"} onChange={(e) => setParams({ direction: e.target.value === "in" ? "in" : undefined })}>
            <option value="out">{t("configurazione.regole.versoOut")}</option>
            <option value="in">{t("configurazione.regole.versoIn")}</option>
          </Select>
          <Select aria-label={t("configurazione.regole.spazio")} className="field" value={p.space ?? ""} onChange={(e) => setParams({ space: e.target.value })}>
            <option value="">{t("configurazione.regole.spazioStesso")}</option>
            {unknownIn(meta.spaces, p.space) && <option value={p.space}>{p.space}</option>}
            {(meta.spaces ?? []).map((x) => (
              <option key={x.key} value={x.key}>
                {x.name}
              </option>
            ))}
          </Select>
        </>
      )}

      {kind === "webhook" && (
        <>
          <Select aria-label={t("configurazione.regole.webhook")} className="field" value={p.webhook ?? ""} onChange={(e) => setParams({ webhook: e.target.value })}>
            <option value="">{t("configurazione.regole.scegli")}</option>
            {!!p.webhook && !(meta.webhooks ?? []).includes(p.webhook) && <option value={p.webhook}>{p.webhook}</option>}
            {(meta.webhooks ?? []).map((w) => (
              <option key={w} value={w}>
                {w}
              </option>
            ))}
          </Select>
          {!(meta.webhooks ?? []).length && <p className="m-0 text-[12px] text-muted">{t("configurazione.regole.nessunWebhook")}</p>}
        </>
      )}
    </div>
  );
}

/** Un gruppo per nome (le regole lo salvano così). */
function GroupSelect({ meta, value, none, onChange }: { meta: ProcessMeta; value: string | undefined; none: string; onChange: (v: string) => void }) {
  const t = useT();
  return (
    <Select aria-label={t("configurazione.regole.gruppo")} className="field" value={value ?? ""} onChange={(e) => onChange(e.target.value)}>
      <option value="">{none}</option>
      {!!value && !meta.groups.some((g) => g.name === value) && <option value={value}>{value}</option>}
      {meta.groups.map((g) => (
        <option key={g.key} value={g.name}>
          {g.name}
        </option>
      ))}
    </Select>
  );
}

/** Condizione propria dell'azione, sui dati di ciascun impatto. */
function ImpactCondition({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const t = useT();
  return <ConditionBuilder label={t("configurazione.regole.soloImpatti")} value={value} onChange={onChange} type="impact" />;
}
