import { Select } from "../../select";
import { useEffect, useId, useMemo, useRef, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { api, type FactInfo } from "../../api";
import { ErrorNote } from "../../ui";
import { OPS, conditionError, defaultOp, humanFact, type CondRow, type Op } from "./model";
import { completeRows, fromGroups, phrase, toGroups, type CondGroups } from "../../casa/gruppi";
import { casaApi, useDebounced, useTypeFacts, type CondCount } from "../../casaApi";
import { useT } from "../../i18n/react";
import { Trans } from "../../i18n/Trans";
import { tr } from "../../i18n/core";

// Costruttore unico di condizioni (replica/usabilita.md, proposta 6): righe «dato, operatore,
// valore» (E) raccolte in gruppi alternativi (O), la condizione in parole («Solo se…») e,
// se si sa su che cosa contare, quanti elementi la soddisfano oggi. La sintassi testuale resta
// in «avanzato». Genera il testo del linguaggio di condizioni (sdlc.md §10).

const SOURCES: { key: FactInfo["source"]; label: string }[] = [
  { key: "domanda", get label() { return tr("configurazione.conditionBuilder.source.domanda"); } },
  { key: "base", get label() { return tr("configurazione.conditionBuilder.source.base"); } },
  { key: "modulo", get label() { return tr("configurazione.conditionBuilder.source.modulo"); } },
  { key: "campo", get label() { return tr("configurazione.conditionBuilder.source.campo"); } },
  { key: "ruolo", get label() { return tr("configurazione.conditionBuilder.source.ruolo"); } },
  { key: "elemento", get label() { return tr("configurazione.conditionBuilder.source.elemento"); } },
];

const opsFor = (kind: string | undefined): Op[] => {
  switch (kind) {
    case "number":
    case "date":
      return ["gt", "ge", "lt", "le", "is", "isnot", "present", "absent"];
    case "bool":
      return ["yes", "no", "present", "absent"];
    case "presence":
      return ["present", "absent", "is", "isnot"];
  }
  return ["is", "isnot", "present", "absent"];
};

type Props = {
  value: string;
  onChange: (v: string) => void;
  /** Dati scelti dal chiamante; senza, si chiedono al server per `type`. */
  facts?: FactInfo[];
  /** Tipo di elemento su cui la condizione vale: dà i dati (se manca `facts`) e il conteggio. */
  type?: string;
  /** Conteggio proprio (perimetri GRC, applicazioni…); vince su quello per `type`. */
  count?: (condition: string) => Promise<CondCount>;
  /** Testo quando la condizione è vuota (es. "sempre"). */
  empty?: string;
  /** Elemento proposto per la prova (dai metadati). */
  sample?: string;
  /** Valori suggeriti aggiuntivi per fatto (es. le fasi della bozza per "stato"). */
  extraValues?: Record<string, string[]>;
  label?: string;
  /** Come si chiamano gli elementi contati («applicazioni», «richieste»); senza, «elementi». */
  noun?: string;
};

export function ConditionBuilder({ value, onChange, facts: factsProp, type, count, empty = tr("configurazione.conditionBuilder.alwaysTrue"), sample, extraValues, label = tr("configurazione.common.condizione"), noun }: Props) {
  const t = useT();
  const loaded = useTypeFacts(factsProp ? undefined : type);
  const facts = factsProp ?? loaded.facts;
  const sampleKey = sample ?? loaded.sample;
  const parsed = useMemo(() => toGroups(value), [value]);
  const [groups, setGroups] = useState<CondGroups>(() => parsed ?? [[]]);
  const [text, setText] = useState(parsed === null);
  // Modalità avanzata: chiavi accanto ai nomi, anche i dati fuori contesto e il testo generato.
  const [advanced, setAdvanced] = useState(false);
  const hidden = facts.filter((f) => f.advanced).length;
  const emitted = useRef(value);
  const id = useId();

  // Valore cambiato da fuori (annulla, JSON, altra selezione): si riallinea.
  useEffect(() => {
    if (value === emitted.current) return;
    emitted.current = value;
    const g = toGroups(value);
    if (g) setGroups(g);
    else setText(true);
  }, [value]);

  const emit = (g: CondGroups) => {
    setGroups(g);
    const s = fromGroups(g);
    emitted.current = s;
    onChange(s);
  };
  const setRow = (gi: number, i: number, r: Partial<CondRow>) => emit(groups.map((g, a) => (a === gi ? g.map((x, b) => (b === i ? { ...x, ...r } : x)) : g)));
  const factOf = (key: string) => facts.find((f) => f.key === key);
  const error = text ? conditionError(value) : null;
  const names = useMemo(() => Object.fromEntries(facts.map((f) => [f.key, f.name || humanFact(f.key)])), [facts]);
  const newRow = (): CondRow => {
    const f = facts.find((x) => !x.advanced) ?? facts[0];
    return { fact: f?.key ?? "", op: defaultOp(f?.kind), values: [] };
  };
  const many = groups.length > 1;

  return (
    <div className="flex flex-col gap-2" role="group" aria-labelledby={`${id}-l`}>
      <div className="flex items-baseline justify-between gap-2">
        <span id={`${id}-l`} className="label">
          {label}
        </span>
        <button
          type="button"
          className="text-[12px] text-accent hover:underline disabled:text-muted disabled:no-underline"
          disabled={text && toGroups(value) === null}
          title={text && toGroups(value) === null ? t("configurazione.conditionBuilder.laCondizioneETroppo") : undefined}
          onClick={() => {
            if (text) {
              const g = toGroups(value);
              if (g) setGroups(g);
            }
            setText(!text);
          }}
        >
          {text ? t("configurazione.conditionBuilder.usaIlCostruttore") : t("configurazione.conditionBuilder.scriviComeTesto")}
        </button>
      </div>

      {text ? (
        <textarea
          aria-label={t("configurazione.conditionBuilder.asText", { label })}
          className="field min-h-[60px] font-mono text-[12.5px]"
          spellCheck={false}
          value={value}
          placeholder={t("configurazione.conditionBuilder.esStimaMaxDriver")}
          onChange={(e) => {
            emitted.current = e.target.value;
            onChange(e.target.value);
          }}
        />
      ) : (
        <>
          <ul className="m-0 flex list-none flex-col gap-1.5 p-0">
            {groups.map((g, gi) => (
              <li key={gi} className="flex flex-col gap-1.5">
                {gi > 0 && <span className="label text-muted">{t("casa.cond.or")}</span>}
                <div className={many ? "flex flex-col border border-hair px-2.5" : "flex flex-col"} role={many ? "group" : undefined} aria-label={many ? t("casa.cond.groupN", { n: gi + 1 }) : undefined}>
                  {g.length > 0 && (
                    <ul className="m-0 flex list-none flex-col gap-0 p-0">
                      {g.map((r, i) => (
                        <li key={i} className={`flex flex-col gap-1.5 border-b border-hair py-2 ${!many && i === 0 ? "border-t" : ""} last:border-b-0 ${many ? "" : "last:border-b"}`}>
                          {i > 0 && <span className="label text-muted">{t("configurazione.conditionBuilder.and")}</span>}
                          <Row
                            row={r}
                            fact={factOf(r.fact)}
                            facts={facts}
                            advanced={advanced}
                            extra={extraValues?.[r.fact]}
                            onChange={(x) => setRow(gi, i, x)}
                            onRemove={() => {
                              const next = groups.map((gg, a) => (a === gi ? gg.filter((_, b) => b !== i) : gg)).filter((gg, a) => gg.length > 0 || groups.length === 1 || a !== gi);
                              emit(next.length ? next : [[]]);
                            }}
                          />
                        </li>
                      ))}
                    </ul>
                  )}
                  <div className="flex flex-wrap items-baseline justify-between gap-2 py-1.5">
                    <button type="button" className="text-[12.5px] font-semibold text-accent hover:underline" onClick={() => emit(groups.map((gg, a) => (a === gi ? [...gg, newRow()] : gg)))}>
                      {t("configurazione.conditionBuilder.aggiungiUnaRiga")}
                    </button>
                    {many && (
                      <button type="button" className="text-[12px] text-muted underline hover:text-danger" onClick={() => emit(groups.filter((_, a) => a !== gi))}>
                        {t("casa.cond.removeGroup")}
                      </button>
                    )}
                  </div>
                </div>
              </li>
            ))}
          </ul>
          <div className="flex flex-wrap items-baseline justify-between gap-2">
            <label className="inline-flex items-center gap-1.5 text-[12px] text-muted">
              <input type="checkbox" checked={advanced} onChange={(e) => setAdvanced(e.target.checked)} />
              {hidden > 0 ? t("configurazione.conditionBuilder.advancedHidden", { count: hidden }) : t("configurazione.conditionBuilder.advanced")}
            </label>
            {completeRows(groups) > 0 && groups.every((g) => g.length > 0) && (
              <button type="button" className="text-[12.5px] font-semibold text-accent hover:underline" onClick={() => emit([...groups, [newRow()]])}>
                {t("casa.cond.addGroup")}
              </button>
            )}
          </div>
        </>
      )}

      {value.trim() ? (
        !error && (
          <p className="m-0 text-[13px]" data-testid="cond-frase">
            {t("casa.cond.onlyIf", { condition: phrase(value, names) })}
          </p>
        )
      ) : (
        <p className="m-0 text-[12px] text-muted">{t("configurazione.conditionBuilder.noCondition", { empty })}</p>
      )}
      {(advanced || text) && value.trim() && (
        <p className="m-0 text-[12px] text-muted">
          <Trans k="configurazione.conditionBuilder.textOf" slots={{ value: <code className="font-mono text-fg">{value}</code> }} />
        </p>
      )}
      {error && <p className="m-0 text-[12.5px] text-danger">{t("configurazione.conditionBuilder.invalid", { error })}</p>}
      {!error && <Count condition={value} type={type} count={count} noun={noun} />}
      {value.trim() && !error && <Tester condition={value} sample={sampleKey} />}
    </div>
  );
}

/** «Oggi lo soddisfano 5 elementi su 40: DEM-1 …», sempre visibile e aggiornato mentre si scrive. */
function Count({ condition, type, count, noun }: { condition: string; type?: string; count?: (c: string) => Promise<CondCount>; noun?: string }) {
  const t = useT();
  const wanted = useDebounced(condition.trim());
  const active = !!count || type !== undefined;
  const r = useQuery({
    queryKey: ["cond-count", type ?? "", wanted, !!count],
    queryFn: () => (count ? count(wanted) : casaApi.count(wanted, type ?? "")),
    enabled: active && conditionError(wanted) === null,
    staleTime: 30_000,
    placeholderData: (prev) => prev,
  });
  if (!active) return null;
  if (r.isError || !r.data) return <p className="m-0 text-[12.5px] text-muted" data-testid="cond-conteggio">{r.isError ? t("casa.cond.countFailed") : t("casa.cond.counting")}</p>;
  const d = r.data;
  if (!d.ok) return null;
  const word = noun ?? t("casa.cond.items");
  return (
    <p className="m-0 text-[12.5px]" data-testid="cond-conteggio" aria-live="polite">
      <span className="font-semibold">{t("casa.cond.today", { matched: d.matched, total: d.total, noun: word })}</span>
      {d.truncated && <span className="text-muted"> {t("casa.cond.truncated")}</span>}
      {d.items.length > 0 && d.matched > 0 && <span className="text-muted"> {t("casa.cond.examples", { list: d.items.map((i) => (i.key ? `${i.key} ${i.title}` : i.title)).join(" · ") })}</span>}
    </p>
  );
}

function Row({
  row,
  fact,
  facts,
  advanced,
  extra,
  onChange,
  onRemove,
}: {
  row: CondRow;
  fact: FactInfo | undefined;
  facts: FactInfo[];
  advanced: boolean;
  extra?: string[];
  onChange: (r: Partial<CondRow>) => void;
  onRemove: () => void;
}) {
  const t = useT();
  const ops = opsFor(fact?.kind);
  if (!ops.includes(row.op)) ops.push(row.op);
  const need = OPS.find((o) => o.op === row.op)?.values ?? 0;
  const suggestions = [...new Set([...(fact?.values ?? []), ...(extra ?? [])])];
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex gap-1.5">
        <Select
          aria-label={t("configurazione.conditionBuilder.dato")}
          className="field min-w-0 flex-1 py-1.5"
          value={row.fact}
          onChange={(e) => {
            const f = facts.find((x) => x.key === e.target.value);
            onChange({ fact: e.target.value, op: defaultOp(f?.kind), values: [] });
          }}
        >
          {!fact && <option value={row.fact}>{row.fact || t("configurazione.conditionBuilder.scegliUnDato")}</option>}
          {SOURCES.map((s) => {
            const fs = facts.filter((f) => f.source === s.key && (advanced || !f.advanced || f.key === row.fact));
            return fs.length ? (
              <optgroup key={s.key} label={s.label}>
                {fs.map((f) => (
                  <option key={f.key} value={f.key}>
                    {advanced && f.name !== f.key ? `${f.name} · ${f.key}` : f.name}
                  </option>
                ))}
              </optgroup>
            ) : null;
          })}
        </Select>
        <button type="button" className="btn px-2.5 py-1" aria-label={t("configurazione.conditionBuilder.togliLaRiga")} title={t("configurazione.conditionBuilder.togliLaRiga")} onClick={onRemove}>
          ×
        </button>
      </div>
      <div className="flex flex-wrap items-start gap-1.5">
        <Select aria-label={t("configurazione.conditionBuilder.operatore")} className="field w-auto py-1.5" value={row.op} onChange={(e) => onChange({ op: e.target.value as Op, values: [] })}>
          {ops.map((o) => (
            <option key={o} value={o}>
              {OPS.find((x) => x.op === o)?.label}
            </option>
          ))}
        </Select>
        {need === 1 && (
          <input
            aria-label={t("configurazione.conditionBuilder.valore")}
            className="field w-40 py-1.5 font-mono text-[12.5px]"
            type={fact?.kind === "date" ? "date" : "text"}
            inputMode={fact?.kind === "number" ? "decimal" : undefined}
            placeholder={fact?.kind === t("configurazione.conditionBuilder.date") ? "AAAA-MM-GG" : fact?.example ?? "50000"}
            value={row.values[0] ?? ""}
            onChange={(e) => onChange({ values: [e.target.value] })}
          />
        )}
        {need === "n" && <Values values={row.values} suggestions={suggestions} onChange={(values) => onChange({ values })} />}
      </div>
      {fact?.example && <span className="text-[11.5px] text-muted">{t("configurazione.conditionBuilder.example", { example: fact.example })}</span>}
    </div>
  );
}

/** Valori di "è" / "non è": quelli scelti, più i suggeriti da aggiungere con un clic. */
function Values({ values, suggestions, onChange }: { values: string[]; suggestions: string[]; onChange: (v: string[]) => void }) {
  const t = useT();
  const [draft, setDraft] = useState("");
  const listId = useId();
  const add = (v: string) => {
    const t = v.trim();
    if (t && !values.some((x) => x.toLowerCase() === t.toLowerCase())) onChange([...values, t]);
    setDraft("");
  };
  const rest = suggestions.filter((s) => !values.some((v) => v.toLowerCase() === s.toLowerCase())).slice(0, 10);
  return (
    <div className="flex min-w-0 flex-1 flex-col gap-1.5">
      <div className="flex flex-wrap gap-1">
        {values.map((v) => (
          <span key={v} className="inline-flex items-center gap-1 border border-rule px-1.5 py-0.5 text-[12.5px]">
            {v}
            <button type="button" className="text-muted hover:text-danger" aria-label={t("configurazione.common.removeNamed", { name: v })} onClick={() => onChange(values.filter((x) => x !== v))}>
              ×
            </button>
          </span>
        ))}
        <input
          aria-label={t("configurazione.conditionBuilder.aggiungiUnValore")}
          className="field w-32 flex-1 py-1 text-[12.5px]"
          list={listId}
          value={draft}
          placeholder={values.length ? t("configurazione.conditionBuilder.altroValore") : t("configurazione.common.valore")}
          onChange={(e) => {
            if (e.target.value.endsWith(",")) add(e.target.value.slice(0, -1));
            else setDraft(e.target.value);
          }}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              add(draft);
            }
          }}
          onBlur={() => draft && add(draft)}
        />
        <datalist id={listId}>
          {suggestions.map((s) => (
            <option key={s} value={s} />
          ))}
        </datalist>
      </div>
      {rest.length > 0 && (
        <div className="flex flex-wrap gap-x-2 gap-y-0.5 text-[12px]">
          {rest.map((s) => (
            <button key={s} type="button" className="text-accent hover:underline" onClick={() => add(s)}>
              + {s}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

/** Prova della condizione sui fatti di un elemento (POST /conditions/check). */
function Tester({ condition, sample }: { condition: string; sample?: string }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const [el, setEl] = useState(sample ?? "");
  const check = useMutation({ mutationFn: () => api.checkCondition(condition, el.trim() || undefined) });
  // Una condizione diversa invalida l'esito della prova precedente.
  useEffect(() => check.reset(), [condition]);
  if (!open) {
    return (
      <div>
        <button type="button" className="text-[12px] text-accent hover:underline" onClick={() => setOpen(true)}>
          {t("configurazione.conditionBuilder.provaSuUnElemento")}
        </button>
      </div>
    );
  }
  const r = check.data;
  const used = r?.facts ? r.keys.map((k) => [k, r.facts![k] ?? []] as const) : [];
  return (
    <div className="flex flex-col gap-1.5 border-l-2 border-hair pl-2.5">
      <div className="flex items-center gap-1.5">
        <input
          aria-label={t("configurazione.conditionBuilder.elementoSuCuiProvare")}
          className="field w-28 py-1 font-mono text-[12.5px]"
          value={el}
          placeholder="DEM-12"
          onChange={(e) => setEl(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              check.mutate();
            }
          }}
        />
        <button type="button" className="btn px-2.5 py-1" disabled={!el.trim() || check.isPending} onClick={() => check.mutate()}>
          {t("configurazione.common.prova")}
        </button>
      </div>
      <ErrorNote error={check.error} />
      {r && !r.ok && <p className="m-0 text-[12.5px] text-danger">{r.error}</p>}
      {r?.result !== undefined && (
        <p className="m-0 text-[12.5px]">
          Su <span className="font-mono">{el.toUpperCase()}</span> è{" "}
          <span className={`font-semibold ${r.result ? "text-ok" : "text-danger"}`}>{r.result ? t("configurazione.processes.true") : t("configurazione.processes.false")}</span>
        </p>
      )}
      {used.length > 0 && (
        <dl className="m-0 grid grid-cols-[auto_1fr] gap-x-3 text-[12px]">
          {used.map(([k, v]) => (
            <div key={k} className="contents">
              <dt className="font-mono text-muted">{k}</dt>
              <dd className="m-0">{v.length ? v.map(String).join(", ") : <span className="text-muted">{t("configurazione.common.assente")}</span>}</dd>
            </div>
          ))}
        </dl>
      )}
    </div>
  );
}
