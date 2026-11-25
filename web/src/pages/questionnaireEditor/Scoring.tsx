import { Select } from "../../select";
import type { FactInfo, ReviewOutcome } from "../../api";
import type { Derivation, QDefinition } from "../../condition";
import { SectionTitle } from "../../ui";
import { ConditionBuilder } from "../processEditor/ConditionBuilder";
import { factsBefore, keyFrom, scoreTable, allQuestions, type Finding } from "./model";
import { Field, Notes, type Change } from "./Structure";
import { useT } from "../../i18n/react";
import { tr } from "../../i18n/core";

const band = (p: ReviewOutcome["polarity"] | undefined) => (p === "positive" ? "bg-ok" : p === "negative" ? "bg-danger" : p === "conditional" ? "bg-flag" : "bg-muted");
const pctText = (n: number) => `${Math.round(n * 1000) / 10}%`;

/** Punteggio ed esiti: pesi, soglie con una barra che mostra le fasce, esito proposto. */
export function ScoringPanel({
  def,
  change,
  outcomes,
  problems,
  goQuestion,
}: {
  def: QDefinition;
  change: Change;
  outcomes: ReviewOutcome[];
  problems: Finding[];
  goQuestion: (key: string) => void;
}) {
  const t = useT();
  const table = scoreTable(def);
  const ths = def.scoring?.thresholds ?? [];
  const sorted = ths.map((t, i) => ({ ...t, i })).sort((a, b) => a.min - b.min);
  const pol = (k: string) => outcomes.find((o) => o.key === k)?.polarity;
  const unscored = allQuestions(def).filter((q) => ["yes_no", "single", "multi", "scale", "matrix"].includes(q.type) && !table.some((r) => r.key === q.key));
  const setTh = (fn: (t: { min: number; outcome: string }[]) => void, co?: string) =>
    change((d) => {
      d.scoring ??= { thresholds: [] };
      fn(d.scoring.thresholds);
    }, co && `th:${co}`);
  const preset = () => {
    const by = (p: string) => outcomes.find((o) => o.polarity === p)?.key;
    const list = [
      { min: 0, outcome: by("negative") },
      { min: 50, outcome: by("conditional") },
      { min: 80, outcome: by("positive") },
    ].filter((t): t is { min: number; outcome: string } => !!t.outcome);
    setTh((t) => t.splice(0, t.length, ...list));
  };
  return (
    <div className="flex flex-col gap-8">
      <section className="flex flex-col gap-3">
        <SectionTitle aside={<span className="font-mono text-[12px] text-muted tabular">{t("configurazione.qEditor.scoring.withScore", { count: table.length })}</span>}>{t("configurazione.qScoring.punteggioDelleRisposte")}</SectionTitle>
        <p className="m-0 text-[12.5px] text-muted">
          {t("configurazione.qScoring.ogniRispostaPuoDare")}
        </p>
        {table.length === 0 ? (
          <p className="m-0 border border-dashed border-hair p-4 text-[13px] text-muted">
            {t("configurazione.qScoring.nessunaDomandaHaUn")}
          </p>
        ) : (
          <ul className="m-0 flex list-none flex-col p-0">
            {table.map((r) => (
              <li key={r.key} className="grid grid-cols-[minmax(0,1fr)_72px_64px] items-center gap-x-3 gap-y-1 border-b border-hair py-2 sm:grid-cols-[minmax(0,1fr)_72px_64px_120px]">
                <button type="button" className="truncate text-left hover:text-accent" onClick={() => goQuestion(r.key)}>
                  {r.label}
                </button>
                <label className="flex items-center gap-1 text-[12px] text-muted">
                  {t("configurazione.qScoring.peso")}
                  <input
                    type="number"
                    min={0}
                    max={100}
                    step="any"
                    aria-label={t("configurazione.qEditor.scoring.weightOf", { label: r.label })}
                    className="field w-14 px-1.5 py-1 tabular"
                    value={r.weight}
                    onChange={(e) =>
                      change((d) => {
                        const q = allQuestions(d).find((x) => x.key === r.key);
                        if (q) e.target.value === "" || Number(e.target.value) === 1 ? delete q.weight : (q.weight = Number(e.target.value));
                      }, `w:${r.key}`)
                    }
                  />
                </label>
                <span className="text-right font-mono text-[12px] text-muted tabular">{pctText(r.share)}</span>
                <span className="col-span-full flex h-1 bg-hair sm:order-none sm:col-span-1" aria-hidden="true">
                  <span className="bg-accent" style={{ width: `${r.share * 100}%` }} />
                </span>
              </li>
            ))}
          </ul>
        )}
        {unscored.length > 0 && table.length > 0 && (
          <p className="m-0 text-[12.5px] text-muted">{t("configurazione.qEditor.scoring.withoutScore", { list: unscored.map((q) => q.label || q.key).join(", ") })}</p>
        )}
      </section>

      <section className="flex flex-col gap-3">
        <SectionTitle>{t("configurazione.qScoring.esitoPropostoDalPunteggio")}</SectionTitle>
        <p className="m-0 text-[12.5px] text-muted">
          {t("configurazione.qScoring.daUnaCertaPercentuale")}
        </p>
        <Notes items={problems.filter((p) => p.path.startsWith("scoring"))} />
        <div className="relative flex h-6 w-full overflow-hidden border border-hair" role="img" aria-label={t("configurazione.qScoring.fasceDelPunteggioDa")}>
          {sorted.length === 0 && <span className="m-auto text-[12px] text-muted">{t("configurazione.qScoring.nessunaSoglia")}</span>}
          {sorted.map((t, k) => {
            const from = Math.max(0, Math.min(100, t.min));
            const to = k + 1 < sorted.length ? Math.max(0, Math.min(100, sorted[k + 1].min)) : 100;
            return <span key={t.i} className={`${band(pol(t.outcome))} absolute inset-y-0 opacity-80`} style={{ left: `${from}%`, width: `${Math.max(0, to - from)}%` }} title={tr("configurazione.qEditor.scoring.fromPercent", { min: t.min })} />;
          })}
        </div>
        <div className="-mt-2 flex justify-between font-mono text-[11px] text-muted tabular">
          <span>0%</span>
          <span>50%</span>
          <span>100%</span>
        </div>
        <ul className="m-0 flex list-none flex-col p-0">
          {sorted.map((t) => (
            <li key={t.i} className="flex flex-wrap items-center gap-2 border-b border-hair py-2">
              <span className="text-muted">Da</span>
              <input
                type="number"
                min={0}
                max={100}
                step="any"
                aria-label={tr("configurazione.qScoring.percentualeMinima")}
                className="field w-20 py-1.5 tabular"
                value={t.min}
                onChange={(e) => setTh((l) => void (l[t.i].min = Number(e.target.value)), `m${t.i}`)}
              />
              <span className="text-muted">{tr("configurazione.qScoring.propone")}</span>
              <Select aria-label={tr("configurazione.qScoring.esitoProposto")} className="field w-auto min-w-0 flex-1 py-1.5" value={t.outcome} onChange={(e) => setTh((l) => void (l[t.i].outcome = e.target.value))}>
                {!outcomes.some((o) => o.key === t.outcome) && <option value={t.outcome}>{tr("configurazione.qEditor.scoring.undefinedOutcome", { key: t.outcome })}</option>}
                {outcomes.map((o) => (
                  <option key={o.key} value={o.key}>
                    {o.label}
                  </option>
                ))}
              </Select>
              <button type="button" className="btn px-2 py-1 hover:text-danger" aria-label={tr("configurazione.qScoring.togliLaSoglia")} onClick={() => setTh((l) => void l.splice(t.i, 1))}>
                ×
              </button>
            </li>
          ))}
        </ul>
        <div className="flex flex-wrap gap-3">
          <button
            type="button"
            className="btn"
            onClick={() => setTh((l) => void l.push({ min: sorted.length ? Math.min(100, Math.max(...sorted.map((t) => t.min)) + 10) : 0, outcome: outcomes[0]?.key ?? "" }))}
          >
            {t("configurazione.qScoring.soglia")}
          </button>
          {ths.length === 0 && (
            <button type="button" className="btn" onClick={preset}>
              {t("configurazione.qScoring.soglieTipiche")}
            </button>
          )}
        </div>
        <p className="m-0 text-[12px] text-muted">{t("configurazione.qScoring.gliEsitiSonoQuelli")}</p>
      </section>
    </div>
  );
}

/** Regole: una risposta fa nascere una prescrizione o un requisito, già proposti nella valutazione. */
export function RulesPanel({ def, change, baseFacts, problems }: { def: QDefinition; change: Change; baseFacts: FactInfo[]; problems: (p: string) => Finding[] }) {
  const t = useT();
  const rules = def.derivations ?? [];
  const facts = factsBefore(def, baseFacts, { question: "\u0000fine" });
  const upd = (i: number, fn: (r: Derivation) => void, co?: string) =>
    change((d) => {
      fn(d.derivations![i]);
    }, co && `r:${i}:${co}`);
  return (
    <div className="flex flex-col gap-4">
      <p className="m-0 text-[12.5px] text-muted">
        {t("configurazione.qScoring.quandoLeRisposteO")}
      </p>
      <ul className="m-0 flex list-none flex-col gap-5 p-0">
        {rules.map((r, i) => (
          <li key={r.key} className="flex flex-col gap-3 border-t-2 border-rule pt-2">
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-mono text-[12px] text-muted">{r.key}</span>
              <Select aria-label={t("configurazione.qScoring.cheCosaGenera")} className="field ml-auto w-auto py-1" value={r.kind ?? "prescription"} onChange={(e) => upd(i, (x) => (x.kind = e.target.value as Derivation["kind"]))}>
                <option value="prescription">{t("configurazione.qScoring.prescrizione")}</option>
                <option value="requirement">{t("configurazione.qScoring.requisito")}</option>
              </Select>
              <button type="button" className="btn px-2 py-1 hover:text-danger" aria-label={t("configurazione.common.togliLaRegola")} onClick={() => change((d) => void d.derivations!.splice(i, 1))}>
                ×
              </button>
            </div>
            <Notes items={problems(`derivations[${i}]`)} />
            <ConditionBuilder label={t("configurazione.vuln.quando")} empty={t("configurazione.processEditor.vaAggiuntaAlmenoUna")} value={r.when} facts={facts} onChange={(v) => upd(i, (x) => (x.when = v), "when")} />
            <p className="m-0 text-[12.5px] text-muted">{r.kind === "requirement" ? t("configurazione.requisitiQ.regola.guidaRequisito") : t("configurazione.requisitiQ.regola.guidaPrescrizione")}</p>
            {r.kind === "requirement" && (
              <Field label={t("configurazione.requisitiQ.regola.titolo")}>
                <input className="field" maxLength={140} placeholder={t("configurazione.requisitiQ.regola.titoloPlaceholder")} value={r.title ?? ""} onChange={(e) => upd(i, (x) => (x.title = e.target.value), "title")} />
              </Field>
            )}
            <Field label={r.kind === "requirement" ? t("configurazione.qScoring.requisitoProposto") : t("configurazione.qScoring.prescrizioneProposta")}>
              <textarea className="field min-h-[64px]" value={r.text} onChange={(e) => upd(i, (x) => (x.text = e.target.value), "text")} />
            </Field>
            <Field label={t("configurazione.qScoring.categoriaFacoltativa")}>
              <input className="field md:max-w-[320px]" placeholder={t(r.kind === "requirement" ? "configurazione.qScoring.requisito" : "configurazione.qScoring.esPrivacy")} value={r.category ?? ""} onChange={(e) => upd(i, (x) => (x.category = e.target.value), "cat")} />
            </Field>
            {r.kind === "requirement" && (
              <div className="flex flex-wrap gap-3">
                <Field label={t("configurazione.requisitiQ.regola.metodo")}>
                  <Select className="field w-auto" value={r.method ?? "manual"} onChange={(e) => upd(i, (x) => (x.method = e.target.value as Derivation["method"]))}>
                    <option value="manual">{t("configurazione.requisitiQ.regola.manuale")}</option>
                    <option value="evidence">{t("configurazione.requisitiQ.regola.evidenza")}</option>
                  </Select>
                </Field>
                {r.method === "evidence" && (
                  <Field label={t("configurazione.requisitiQ.regola.controllo")}>
                    <input className="field font-mono" value={r.check ?? ""} onChange={(e) => upd(i, (x) => (x.check = e.target.value), "check")} />
                  </Field>
                )}
              </div>
            )}
          </li>
        ))}
      </ul>
      <div>
        <button
          type="button"
          className="btn"
          onClick={() =>
            change((d) => {
              d.derivations ??= [];
              d.derivations.push({ key: keyFrom("regola", d.derivations.map((r) => r.key), "regola"), when: "", text: "" });
            })
          }
        >
          {t("configurazione.qScoring.regola")}
        </button>
      </div>
    </div>
  );
}
