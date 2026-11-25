import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { portfolioApi, type Criterion, type ElementDetail, type ScorecardDefinition, type Vote } from "../api";
import { ErrorNote, SectionTitle, ago } from "../ui";
import { useT } from "../i18n/react";
import { Trans } from "../i18n/Trans";
import { fmtNumber } from "../i18n/format";

// Valutazione della richiesta con la scorecard del portafoglio (demand.md §6): un voto e
// una nota per criterio, punteggio pesato da 0 a 100. Votano solo i ruoli configurati
// nella scorecard (di default demand manager e PMO); il server lo verifica comunque.

export function fmtScore(v: number | null | undefined): string {
  return v == null ? "—" : fmtNumber(v, { minimumFractionDigits: 1, maximumFractionDigits: 1 });
}

/** Punteggio pesato 0-100, come il server: voto normalizzato secondo la direzione, media pesata. */
export function weightedTotal(def: ScorecardDefinition, values: Record<string, Vote>): number | null {
  let sum = 0;
  let weights = 0;
  for (const c of def.criteria) {
    const v = values[c.key];
    if (!v) return null;
    const lo = c.scale[0].value;
    const hi = c.scale[c.scale.length - 1].value;
    let n = (v.value - lo) / (hi - lo);
    if (c.direction === "lower_better") n = 1 - n;
    sum += c.weight * n;
    weights += c.weight;
  }
  return weights ? Math.round((1000 * sum) / weights) / 10 : null;
}

/** Barra del punteggio: traccia sottile, riempimento in --fg (in --muted se dato con una versione precedente). */
export function ScoreBar({ value, stale, wide, dim }: { value: number | null; stale?: boolean; wide?: boolean; dim?: boolean }) {
  const t = useT();
  return (
    <span className="inline-flex items-center gap-2" title={stale ? t("lavoro.evaluation.staleTitle") : undefined}>
      <span className={`relative hidden h-1.5 sm:block ${wide ? "w-24" : "w-14"} bg-hair`} aria-hidden="true">
        {value != null && (
          <span className={`absolute inset-y-0 left-0 transition-colors duration-300 ${stale || dim ? "bg-muted" : "bg-fg"}`} style={{ width: `${Math.max(0, Math.min(100, value))}%` }} />
        )}
      </span>
      <span className={`w-9 text-right font-mono text-[12.5px] tabular ${stale ? "text-muted" : "font-semibold"}`}>{fmtScore(value)}</span>
    </span>
  );
}

function weightShare(def: ScorecardDefinition, c: Criterion): number {
  const tot = def.criteria.reduce((s, x) => s + x.weight, 0);
  return tot ? Math.round((100 * c.weight) / tot) : 0;
}

export function Evaluation({ detail, onDone }: { detail: ElementDetail; onDone: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const key = detail.element.key;
  const ev = useQuery({ queryKey: ["evaluation", key], queryFn: () => portfolioApi.evaluation(key) });
  const [editing, setEditing] = useState(false);
  const [values, setValues] = useState<Record<string, Vote>>({});
  const [comment, setComment] = useState("");
  const def = ev.data?.scorecard?.definition;
  const save = useMutation({
    mutationFn: () => portfolioApi.score(key, ev.data!.scorecard!.id, values, comment),
    onSuccess: () => {
      setEditing(false);
      setComment("");
      qc.invalidateQueries({ queryKey: ["evaluation", key] });
      qc.invalidateQueries({ queryKey: ["ranking"] });
      onDone();
    },
  });
  const preview = useMemo(() => (def ? weightedTotal(def, values) : null), [def, values]);
  if (ev.error) return <ErrorNote error={ev.error} />;
  if (!ev.data || !def) return null;
  const data = ev.data;
  // Si legge il punteggio con la definizione della sua versione (può essere precedente).
  const shownDef = data.current_definition ?? def;

  function start() {
    // Si parte dal punteggio corrente (se è della stessa versione) o dai valori proposti.
    const init: Record<string, Vote> = {};
    for (const c of def!.criteria) {
      const prev = !data.stale ? data.current?.values[c.key] : undefined;
      const prop = data.proposals[c.key];
      if (prev) init[c.key] = { ...prev };
      else if (prop) init[c.key] = { value: prop.value, note: "" };
    }
    setValues(init);
    setEditing(true);
  }

  return (
    <section className="flex flex-col gap-2" aria-label={t("lavoro.evaluation.title")}>
      <SectionTitle
        aside={
          data.can_score && !editing ? (
            <button className="text-[12px] text-accent underline" onClick={start}>
              {data.current ? t("lavoro.evaluation.reevaluate") : t("lavoro.evaluation.evaluate")}
            </button>
          ) : undefined
        }
      >
        {t("lavoro.evaluation.title")}
      </SectionTitle>
      <div className="flex items-baseline gap-3">
        <span className="font-mono text-[30px] leading-none font-bold tracking-tight tabular">{fmtScore(editing ? preview : data.score)}</span>
        <span className="text-[12px] text-muted">
          {t("lavoro.evaluation.outOf", { name: ev.data.scorecard!.definition!.name, version: ev.data.scorecard!.version })}
          {editing && preview == null && ` ${t("lavoro.evaluation.missingVotes")}`}
        </span>
      </div>
      {data.stale && data.current && !editing && (
        <p className="m-0 text-[12.5px] text-muted">
          <Trans
            k={data.can_score ? "lavoro.evaluation.staleCan" : "lavoro.evaluation.stale"}
            slots={{
              old: <span className="font-mono">v{data.current.version}</span>,
              current: <span className="font-mono">v{data.scorecard!.version}</span>,
            }}
          />
        </p>
      )}
      {!data.current && !editing && (
        <p className="m-0 text-[12.5px] text-muted">
          {t("lavoro.evaluation.notYet")}
          {data.can_score ? "" : ` ${t("lavoro.evaluation.whoScores")}`}
        </p>
      )}

      {!editing && data.current && (
        <ul className="m-0 flex list-none flex-col p-0">
          {shownDef.criteria.map((c) => {
            const v = data.current!.values[c.key];
            const lvl = c.scale.find((l) => l.value === v?.value);
            return (
              <li key={c.key} className="flex flex-col gap-0.5 border-b border-hair py-1.5">
                <div className="flex items-center gap-2">
                  <span className="min-w-0 flex-1 truncate">
                    {c.name}
                    <span className="ml-1.5 font-mono text-[11px] text-muted tabular">{weightShare(shownDef, c)}%</span>
                    {c.direction === "lower_better" && <span className="ml-1.5 text-[11px] text-muted">{t("lavoro.evaluation.lowerBetter")}</span>}
                  </span>
                  <Pips c={c} value={v?.value} />
                </div>
                <span className="text-[12px] text-muted">
                  {lvl ? lvl.label : "—"}
                  {v?.note ? ` · ${v.note}` : ""}
                </span>
              </li>
            );
          })}
        </ul>
      )}

      {editing && (
        <form
          className="flex flex-col gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          {def.criteria.map((c) => (
            <fieldset key={c.key} className="m-0 flex flex-col gap-1.5 border-0 border-b border-hair p-0 pb-3">
              <legend className="contents">
                <span className="flex items-baseline gap-2">
                  <span className="font-semibold">{c.name}</span>
                  <span className="font-mono text-[11px] text-muted tabular">{t("lavoro.evaluation.weight", { n: weightShare(def, c) })}</span>
                  {c.direction === "lower_better" && <span className="text-[11px] text-muted">{t("lavoro.evaluation.lowerBetter")}</span>}
                </span>
              </legend>
              {c.description && <span className="text-[12px] text-muted">{c.description}</span>}
              <div role="radiogroup" aria-label={c.name} className="flex">
                {c.scale.map((l) => {
                  const on = values[c.key]?.value === l.value;
                  const proposed = data.proposals[c.key]?.value === l.value;
                  return (
                    <button
                      type="button"
                      role="radio"
                      aria-checked={on}
                      key={l.value}
                      title={l.label + (proposed ? ` ${t("lavoro.evaluation.proposedByData")}` : "")}
                      onClick={() => setValues((s) => ({ ...s, [c.key]: { value: l.value, note: s[c.key]?.note ?? "" } }))}
                      className={`relative -ml-px flex h-9 flex-1 items-center justify-center border font-mono text-[13px] tabular first:ml-0 ${
                        on ? "z-10 border-accent bg-accent text-accent-fg" : "border-hair hover:border-fg"
                      }`}
                    >
                      {l.value}
                      {proposed && <span className={`absolute bottom-1 size-1 rounded-full ${on ? "bg-accent-fg" : "bg-accent"}`} aria-hidden="true" />}
                    </button>
                  );
                })}
              </div>
              <span className="text-[12px]">
                {c.scale.find((l) => l.value === values[c.key]?.value)?.label ?? <span className="text-muted">{t("lavoro.evaluation.chooseLevel")}</span>}
                {data.proposals[c.key] && (
                  <span className="text-muted"> {t("lavoro.evaluation.proposed", { value: data.proposals[c.key].value, reason: data.proposals[c.key].reason })}</span>
                )}
              </span>
              <input
                className="field py-1.5 text-[12.5px]"
                placeholder={t("lavoro.evaluation.notePlaceholder")}
                aria-label={t("lavoro.evaluation.noteOn", { name: c.name })}
                value={values[c.key]?.note ?? ""}
                disabled={!values[c.key]}
                onChange={(e) => setValues((s) => ({ ...s, [c.key]: { ...s[c.key], note: e.target.value } }))}
              />
            </fieldset>
          ))}
          <label className="flex flex-col gap-1">
            <span className="label">{t("lavoro.evaluation.comment")}</span>
            <textarea className="field min-h-14" value={comment} onChange={(e) => setComment(e.target.value)} />
          </label>
          <div className="flex flex-wrap items-center gap-2">
            <button className="btn btn-primary" disabled={save.isPending || preview == null}>
              {t("lavoro.evaluation.record")} {preview != null ? fmtScore(preview) : ""}
            </button>
            <button type="button" className="btn" onClick={() => setEditing(false)}>
              {t("lavoro.evaluation.cancel")}
            </button>
          </div>
          <ErrorNote error={save.error} />
        </form>
      )}

      {data.history.length > 0 && !editing && (
        <details className="text-[12.5px]">
          <summary className="cursor-pointer text-muted">{t("lavoro.evaluation.history", { n: data.history.length })}</summary>
          <ol className="m-0 mt-1.5 flex list-none flex-col gap-1 p-0">
            {data.history.map((h) => (
              <li key={h.id} className="grid grid-cols-[48px_36px_minmax(0,1fr)_auto] items-baseline gap-2">
                <span className="font-mono font-semibold tabular">{fmtScore(h.total)}</span>
                <span className="font-mono text-muted">v{h.version}</span>
                <span className="truncate">
                  {h.scored_by ?? "—"}
                  {h.comment ? <span className="text-muted"> · {h.comment}</span> : null}
                </span>
                <span className="text-muted tabular">{ago(h.scored_at)}</span>
              </li>
            ))}
          </ol>
        </details>
      )}
    </section>
  );
}

/** Livelli della scala come quadratini: pieni fino al voto. */
function Pips({ c, value }: { c: Criterion; value: number | undefined }) {
  const t = useT();
  return (
    <span className="flex shrink-0 gap-0.5" aria-label={value != null ? t("lavoro.evaluation.vote", { value }) : t("lavoro.evaluation.noVote")}>
      {c.scale.map((l) => (
        <span key={l.value} className={`h-2.5 w-2.5 ${value != null && l.value <= value ? "bg-fg" : "bg-hair"}`} />
      ))}
      <span className="ml-1.5 w-3 font-mono text-[12px] tabular">{value ?? "—"}</span>
    </span>
  );
}
