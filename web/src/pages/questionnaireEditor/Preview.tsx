import { useMemo, useState } from "react";
import type { FactInfo, ReviewOutcome } from "../../api";
import { evaluate, type Facts, type QDefinition } from "../../condition";
import { DerivedList, Questionnaire, ScoreBox } from "../Reviews";
import { SectionTitle } from "../../ui";
import { allQuestions, extremeAnswers, usedFacts } from "./model";
import { useT } from "../../i18n/react";

/** Testo scritto dalla persona → valori di un fatto (sì/no, numeri, testo; virgola per più valori). */
export function parseFactValues(raw: string): unknown[] {
  return raw
    .split(",")
    .map((x) => x.trim())
    .filter(Boolean)
    .map((v) => {
      if (["sì", "si", "true"].includes(v.toLowerCase())) return true;
      if (["no", "false"].includes(v.toLowerCase())) return false;
      return Number.isFinite(Number(v)) ? Number(v) : v;
    });
}

/**
 * Il questionario come lo vede chi compila, con risposte e dati dell'elemento simulati:
 * mostra cosa compare, il punteggio, l'esito proposto e i requisiti generati. Nulla si salva.
 */
export function Preview({ def, outcomes, facts: known }: { def: QDefinition; outcomes: ReviewOutcome[]; facts: FactInfo[] }) {
  const t = useT();
  const [answers, setAnswers] = useState<Record<string, unknown>>({});
  const [factText, setFactText] = useState<Record<string, string>>({});
  const used = useMemo(() => usedFacts(def), [def]);
  const facts = useMemo(() => {
    const f: Facts = {};
    for (const k of used) {
      const v = parseFactValues(factText[k] ?? "");
      if (v.length) f[k] = v;
    }
    return f;
  }, [used, factText]);
  // Precompilazioni dai dati simulati, come all'apertura di una valutazione.
  const values = useMemo(() => {
    const out: Record<string, unknown> = {};
    for (const q of allQuestions(def)) {
      const fv = q.prefill ? facts[q.prefill.toLowerCase()] : undefined;
      if (!fv?.length) continue;
      if (q.type === "yes_no" && typeof fv[0] === "boolean") out[q.key] = fv[0];
      else if (q.type === "single") {
        const o = q.options?.find((o) => fv.some((x) => String(x).toLowerCase() === o.value.toLowerCase() || String(x).toLowerCase() === o.label.toLowerCase()));
        if (o) out[q.key] = o.value;
      } else if ((q.type === "number" || q.type === "scale") && typeof fv[0] === "number") out[q.key] = fv[0];
      else if (q.type === "text") out[q.key] = fv.map(String).join(", ");
    }
    return { ...out, ...answers };
  }, [def, facts, answers]);
  let evaluation;
  try {
    evaluation = evaluate(def, facts, values);
  } catch {
    return <p className="m-0 text-danger">{t("configurazione.qPreview.ilQuestionarioNonSi")}</p>;
  }
  const hidden = allQuestions(def).filter((q) => evaluation.visible[q.key] === false).length;
  const pct = evaluation.total ? Math.round((evaluation.answered / evaluation.total) * 100) : 0;
  const suggestions = (k: string) => known.find((f) => f.key === k)?.values ?? [];
  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-2">
        <SectionTitle>{t("configurazione.qPreview.datiDellElementoSimulati")}</SectionTitle>
        {used.length === 0 ? (
          <p className="m-0 text-[12.5px] text-muted">{t("configurazione.qPreview.leCondizioniNonUsano")}</p>
        ) : (
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
            {used.map((k) => (
              <label key={k} className="flex min-w-0 flex-col gap-0.5">
                <span className="truncate font-mono text-[11.5px] text-muted">{k}</span>
                <input
                  className="field py-1.5"
                  list={`pf-${k}`}
                  placeholder={known.find((f) => f.key === k)?.example ?? t("configurazione.common.valore")}
                  value={factText[k] ?? ""}
                  onChange={(e) => setFactText({ ...factText, [k]: e.target.value })}
                />
                <datalist id={`pf-${k}`}>
                  {suggestions(k).map((s) => (
                    <option key={s} value={s} />
                  ))}
                </datalist>
              </label>
            ))}
          </div>
        )}
      </div>

      <div className="flex flex-col gap-2 border-t border-hair pt-4">
        <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1 text-[12.5px]">
          <span className="tabular">
            {t("configurazione.qEditor.preview.summary", { answered: evaluation.answered, total: evaluation.total, missing: evaluation.missing.length })}
            {hidden > 0 ? t("configurazione.qEditor.preview.hidden", { count: hidden }) : ""}
          </span>
          <span className="flex flex-wrap gap-x-3">
            <button type="button" className="text-accent hover:underline" onClick={() => setAnswers(extremeAnswers(def))}>
              {t("configurazione.qPreview.risposteMigliori")}
            </button>
            <button type="button" className="text-accent hover:underline" onClick={() => setAnswers(extremeAnswers(def, true))}>
              {t("configurazione.qPreview.rispostePeggiori")}
            </button>
            <button type="button" className="text-muted underline" onClick={() => setAnswers({})}>
              {t("configurazione.common.azzera")}
            </button>
          </span>
        </div>
        <span className="flex h-1 bg-hair" aria-hidden="true">
          <span className="bg-accent" style={{ width: `${pct}%` }} />
        </span>
      </div>

      {evaluation.scored && <ScoreBox ev={evaluation} def={def} outcomes={outcomes} />}
      <DerivedList derived={evaluation.derived} title={t("configurazione.qPreview.prescrizioniERequisitiGenerati")} />

      <div className="flex flex-col gap-4 border-t-2 border-rule pt-3">
        <h3 className="m-0 text-[20px] font-bold">{def.name || t("configurazione.qPreview.questionario")}</h3>
        {def.description && <p className="m-0 text-muted">{def.description}</p>}
        <Questionnaire
          def={def}
          facts={facts}
          values={values}
          evaluation={evaluation}
          meta={Object.fromEntries(
            Object.keys(values)
              .filter((k) => !(k in answers))
              .map((k) => [k, { value: values[k], by: null, at: new Date().toISOString(), source: "prefill" as const }]),
          )}
          canAnswer={() => true}
          sectionAside={(s) => (s.filled_by === "team" ? <span className="text-[12.5px] text-muted">{t("configurazione.qPreview.laCompilaSoloIl")}</span> : null)}
          onChange={(k, v) => setAnswers((a) => ({ ...a, [k]: v }))}
        />
      </div>
    </div>
  );
}
