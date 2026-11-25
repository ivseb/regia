// Editor visuale della scorecard (replica/usabilita.md, proposte 6 e 8): criteri come schede
// con nome, direzione, scala, peso con cursore e regole di proposta scritte con il costruttore
// di condizioni. Il JSON resta nel riquadro «Avanzato». Bozza, pubblicazione e problemi del
// server come prima; i problemi compaiono accanto al criterio a cui si riferiscono.
import { useEffect, useMemo, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ApiError, portfolioApi, type Criterion, type Problem, type Scorecard, type ScorecardDefinition } from "../../api";
import { OptionSelect, Select } from "../../select";
import { ErrorNote, ROLE_NAMES, SectionTitle } from "../../ui";
import { useT } from "../../i18n/react";
import { ConditionBuilder } from "../processEditor/ConditionBuilder";
import { AdvancedJson, RowProblems, RowTools, move, problemsOutside, replaceAt } from "./common";

const slug = (s: string) =>
  s
    .toLowerCase()
    .normalize("NFD")
    .replace(/[̀-ͯ]/g, "")
    .replace(/[^a-z0-9]+/g, "_")
    .replace(/^_+|_+$/g, "")
    .slice(0, 40);

const AUTO_KEY = /^criterio_\d+$/;
const ROW_PATH = /^criteria\[\d+\]/;
const round = (n: number) => Math.round(n * 100) / 100;

const emptyDef = (): ScorecardDefinition => ({ name: "", description: "", method: "weighted", scorer_roles: [], criteria: [] });

/** Toglie i campi vuoti prima di mandare la definizione al server. */
function clean(def: ScorecardDefinition): ScorecardDefinition {
  return {
    ...def,
    description: def.description || undefined,
    criteria: def.criteria.map((c) => ({
      ...c,
      description: c.description || undefined,
      suggest: c.suggest?.length ? c.suggest : undefined,
      suggest_fact: c.suggest_fact || undefined,
    })),
  };
}

export function ScorecardEditor({ sc, scorecardKey, onSaved }: { sc: Scorecard; scorecardKey: string; onSaved: (data: Scorecard) => void }) {
  const t = useT();
  const qc = useQueryClient();
  const source = sc.draft ?? sc.current;
  const [def, setDef] = useState<ScorecardDefinition>(source?.definition ?? emptyDef());
  const [dirty, setDirty] = useState(false);
  const [notes, setNotes] = useState("");
  useEffect(() => {
    setDef(source?.definition ?? emptyDef());
    setDirty(false);
  }, [source?.id]); // eslint-disable-line react-hooks/exhaustive-deps
  const edit = (next: ScorecardDefinition) => {
    setDef(next);
    setDirty(true);
  };
  const setCrit = (i: number, patch: Partial<Criterion>) => edit({ ...def, criteria: replaceAt(def.criteria, i, patch) });

  const save = useMutation({
    mutationFn: () => portfolioApi.saveScorecardDraft(scorecardKey, clean(def), notes),
    onSuccess: (data) => {
      setDirty(false);
      onSaved(data);
    },
  });
  const publish = useMutation({ mutationFn: () => portfolioApi.publishScorecard(scorecardKey, notes), onSuccess: onSaved });
  const discard = useMutation({
    mutationFn: () => portfolioApi.discardScorecardDraft(scorecardKey),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["scorecard", scorecardKey] }),
  });
  const err = save.error ?? publish.error;
  const problems = useMemo(() => (err instanceof ApiError ? (err.problems ?? []) : []), [err]);
  const outside = problemsOutside(problems, ROW_PATH);

  const total = round(def.criteria.reduce((s, c) => s + c.weight, 0));
  const roleOptions = useMemo(() => {
    const keys = [...new Set([...Object.keys(ROLE_NAMES), ...def.scorer_roles])];
    return keys.map((k) => ({ value: k, label: ROLE_NAMES[k] ?? k }));
  }, [def.scorer_roles]);

  const addCriterion = () => {
    const n = def.criteria.length + 1;
    let key = `criterio_${n}`;
    while (def.criteria.some((c) => c.key === key)) key += "_";
    edit({
      ...def,
      criteria: [
        ...def.criteria,
        {
          key,
          name: "",
          weight: total < 100 ? round(100 - total) : 10,
          direction: "higher_better",
          scale: [
            { value: 1, label: t("configurazione.scorecardEditor.levelLow") },
            { value: 3, label: t("configurazione.scorecardEditor.levelHigh") },
          ],
        },
      ],
    });
  };
  const normalize = () => {
    if (!total) return;
    const next = def.criteria.map((c) => ({ ...c, weight: round((c.weight * 100) / total) }));
    const diff = round(100 - next.reduce((s, c) => s + c.weight, 0));
    if (next.length && diff) next[0].weight = round(next[0].weight + diff);
    edit({ ...def, criteria: next });
  };

  return (
    <section className="flex flex-col gap-4" aria-label={t("configurazione.scorecardEditor.editor")}>
      <SectionTitle aside={sc.draft && <span className="font-mono text-[12px] text-flag">{t("configurazione.scorecard.draftVersion", { version: sc.draft.version })}</span>}>
        {t("configurazione.scorecard.modificaNuovaVersione")}
      </SectionTitle>
      <p className="m-0 max-w-3xl text-[12.5px] text-muted">{t("configurazione.scorecardEditor.help")}</p>

      <div className="grid max-w-3xl grid-cols-1 gap-3 md:grid-cols-2">
        <label className="flex flex-col gap-1">
          <span className="label">{t("configurazione.scorecardEditor.name")}</span>
          <input className="field" value={def.name} onChange={(e) => edit({ ...def, name: e.target.value })} />
        </label>
        <div className="flex flex-col gap-1">
          <span className="label" id="sc-roles">
            {t("configurazione.scorecardEditor.scorerRoles")}
          </span>
          <OptionSelect multiple aria-labelledby="sc-roles" options={roleOptions} value={def.scorer_roles} onChange={(v) => edit({ ...def, scorer_roles: v })} placeholder={t("configurazione.scorecardEditor.addRole")} />
        </div>
        <label className="flex flex-col gap-1 md:col-span-2">
          <span className="label">{t("configurazione.scorecardEditor.description")}</span>
          <input className="field" value={def.description ?? ""} onChange={(e) => edit({ ...def, description: e.target.value })} />
        </label>
      </div>
      {outside.length > 0 && (
        <ul className="m-0 list-none border-l-2 border-danger p-0 pl-2.5 text-[12.5px] text-danger">
          {outside.map((p, i) => (
            <li key={i}>
              <span className="font-mono">{p.path}</span> {p.message}
            </li>
          ))}
        </ul>
      )}

      <div className="flex flex-wrap items-baseline justify-between gap-2 border-b border-hair pb-2" role="status" data-testid="pesi-totale">
        <span className="text-[13px]">
          {t("configurazione.scorecardEditor.totalWeight", { total })}
        </span>
        {total !== 100 && def.criteria.length > 0 && (
          <span className="flex flex-wrap items-baseline gap-2 text-[12.5px] text-flag">
            {t("configurazione.scorecardEditor.notHundred")}
            <button type="button" className="text-accent underline" onClick={normalize}>
              {t("configurazione.scorecardEditor.normalize")}
            </button>
          </span>
        )}
      </div>

      <ol className="m-0 flex list-none flex-col gap-4 p-0">
        {def.criteria.map((c, i) => (
          <CriterionCard
            key={i}
            i={i}
            c={c}
            total={total}
            count={def.criteria.length}
            type={sc.type ?? "business_request"}
            problems={problems}
            onChange={(patch) => {
              const p = { ...patch };
              if (patch.name !== undefined && (AUTO_KEY.test(c.key) || c.key === slug(c.name))) {
                const k = slug(patch.name);
                if (k && !def.criteria.some((x, a) => a !== i && x.key === k)) p.key = k;
              }
              setCrit(i, p);
            }}
            onMove={(to) => edit({ ...def, criteria: move(def.criteria, i, to) })}
            onRemove={() => edit({ ...def, criteria: def.criteria.filter((_, a) => a !== i) })}
          />
        ))}
      </ol>
      <div>
        <button type="button" className="btn" onClick={addCriterion}>
          {t("configurazione.scorecardEditor.addCriterion")}
        </button>
      </div>

      <AdvancedJson value={def} label={t("configurazione.scorecard.definizioneDellaScorecardJson")} onApply={edit} />

      <label className="flex flex-col gap-1">
        <span className="label">{t("configurazione.common.noteDellaVersione")}</span>
        <input className="field" value={notes} onChange={(e) => setNotes(e.target.value)} />
      </label>
      <div className="flex flex-wrap items-center gap-2">
        <button className="btn" disabled={save.isPending} onClick={() => save.mutate()}>
          {t("configurazione.common.salvaLaBozza")}
        </button>
        <button className="btn btn-primary" disabled={!sc.draft || dirty || publish.isPending} onClick={() => publish.mutate()}>
          {t("configurazione.scorecard.publishVersion", { version: sc.draft?.version ?? (sc.current ? sc.current.version + 1 : 1) })}
        </button>
        {sc.draft && (
          <button className="btn" disabled={discard.isPending} onClick={() => discard.mutate()}>
            {t("configurazione.common.scartaLaBozza")}
          </button>
        )}
        {dirty && <span className="text-[12.5px] text-flag">{t("configurazione.common.modificheNonSalvate")}</span>}
      </div>
      <ErrorNote error={err} />
    </section>
  );
}

function CriterionCard({
  i,
  c,
  total,
  count,
  type,
  problems,
  onChange,
  onMove,
  onRemove,
}: {
  i: number;
  c: Criterion;
  total: number;
  count: number;
  type: string;
  problems: Problem[];
  onChange: (patch: Partial<Criterion>) => void;
  onMove: (to: number) => void;
  onRemove: () => void;
}) {
  const t = useT();
  const name = c.name || c.key;
  const pct = total > 0 ? Math.round((100 * c.weight) / total) : 0;
  const suggest = c.suggest ?? [];
  const levelOpts = (cur: number) => {
    const o = c.scale.map((l) => ({ value: String(l.value), label: `${l.value} · ${l.label}` }));
    return o.some((x) => x.value === String(cur)) ? o : [...o, { value: String(cur), label: String(cur) }];
  };
  return (
    <li className="flex flex-col gap-3 border border-hair p-3" data-testid="criterio" aria-label={name}>
      <div className="flex flex-wrap items-start justify-between gap-2">
        <span className="font-mono text-[12px] text-muted tabular">{i + 1}</span>
        <RowTools i={i} count={count} name={name} onMove={onMove} onRemove={onRemove} />
      </div>
      <div className="grid grid-cols-1 gap-3 md:grid-cols-[minmax(0,2fr)_minmax(0,1fr)_minmax(0,1fr)]">
        <label className="flex flex-col gap-1">
          <span className="label">{t("configurazione.scorecardEditor.criterionName")}</span>
          <input className="field" value={c.name} onChange={(e) => onChange({ name: e.target.value })} />
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{t("configurazione.scorecardEditor.key")}</span>
          <input className="field font-mono" value={c.key} onChange={(e) => onChange({ key: e.target.value })} />
        </label>
        <div className="flex flex-col gap-1">
          <span className="label" id={`sc-dir-${i}`}>
            {t("configurazione.scorecardEditor.direction")}
          </span>
          <Select aria-labelledby={`sc-dir-${i}`} className="field" value={c.direction} onChange={(e) => onChange({ direction: e.target.value as Criterion["direction"] })}>
            <option value="higher_better">{t("configurazione.scorecardEditor.higherBetter")}</option>
            <option value="lower_better">{t("configurazione.scorecardEditor.lowerBetter")}</option>
          </Select>
        </div>
        <label className="flex flex-col gap-1 md:col-span-3">
          <span className="label">{t("configurazione.scorecardEditor.description")}</span>
          <input className="field" value={c.description ?? ""} onChange={(e) => onChange({ description: e.target.value })} />
        </label>
      </div>

      <div className="flex flex-col gap-1">
        <span className="label">{t("configurazione.common.peso")}</span>
        <div className="flex items-center gap-3">
          <input
            type="range"
            className="min-w-0 flex-1"
            min={0}
            max={Math.max(100, Math.ceil(c.weight))}
            step={1}
            value={c.weight}
            aria-label={t("configurazione.scorecardEditor.weightOf", { name })}
            onChange={(e) => onChange({ weight: Number(e.target.value) })}
          />
          <input
            type="number"
            className="field w-20 text-right font-mono"
            min={0}
            step="any"
            value={c.weight}
            aria-label={t("configurazione.scorecardEditor.weightValueOf", { name })}
            onChange={(e) => onChange({ weight: e.target.valueAsNumber || 0 })}
          />
          <span className="w-24 shrink-0 text-right font-mono text-[12.5px] tabular" data-testid="peso-percento">
            {t("configurazione.scorecardEditor.share", { pct })}
          </span>
        </div>
      </div>

      <div className="flex flex-col gap-1.5">
        <span className="label">{t("configurazione.scorecard.scala")}</span>
        <ul className="m-0 flex list-none flex-col gap-1.5 p-0">
          {c.scale.map((l, j) => (
            <li key={j} className="flex items-center gap-1.5">
              <input
                type="number"
                className="field w-20 font-mono"
                value={l.value}
                aria-label={t("configurazione.scorecardEditor.levelValue", { name })}
                onChange={(e) => onChange({ scale: replaceAt(c.scale, j, { value: e.target.valueAsNumber || 0 }) })}
              />
              <input className="field min-w-0 flex-1" value={l.label} aria-label={t("configurazione.scorecardEditor.levelLabel", { name })} onChange={(e) => onChange({ scale: replaceAt(c.scale, j, { label: e.target.value }) })} />
              <button
                type="button"
                className="btn px-2 py-0.5"
                aria-label={t("configurazione.scorecardEditor.removeLevel", { name })}
                disabled={c.scale.length <= 2}
                onClick={() => onChange({ scale: c.scale.filter((_, a) => a !== j) })}
              >
                ×
              </button>
            </li>
          ))}
        </ul>
        <div>
          <button type="button" className="text-[12.5px] font-semibold text-accent hover:underline" onClick={() => onChange({ scale: [...c.scale, { value: (c.scale[c.scale.length - 1]?.value ?? 0) + 1, label: "" }] })}>
            {t("configurazione.scorecardEditor.addLevel")}
          </button>
        </div>
      </div>

      <div className="flex flex-col gap-2">
        <span className="label">{t("configurazione.scorecard.valoreProposto")}</span>
        {suggest.length === 0 && <p className="m-0 text-[12.5px] text-muted">{t("configurazione.scorecardEditor.noRules")}</p>}
        <ul className="m-0 flex list-none flex-col gap-3 p-0">
          {suggest.map((s, j) => (
            <li key={j} className="flex flex-col gap-2 border-l-2 border-accent pl-3">
              <ConditionBuilder
                label={t("configurazione.scorecardEditor.proposeWhen", { n: j + 1 })}
                value={s.when}
                onChange={(when) => onChange({ suggest: replaceAt(suggest, j, { when }) })}
                type={type}
                noun={t("configurazione.scorecardEditor.noun")}
                empty={t("configurazione.scorecardEditor.alwaysWhen")}
              />
              <div className="flex flex-wrap items-center gap-2">
                <span className="text-[12.5px]">{t("configurazione.scorecardEditor.proposeValue")}</span>
                <Select
                  aria-label={t("configurazione.scorecardEditor.proposedValue", { n: j + 1 })}
                  className="field w-auto min-w-40"
                  value={String(s.value)}
                  onChange={(e) => onChange({ suggest: replaceAt(suggest, j, { value: Number(e.target.value) }) })}
                >
                  {levelOpts(s.value).map((o) => (
                    <option key={o.value} value={o.value}>
                      {o.label}
                    </option>
                  ))}
                </Select>
                <button type="button" className="text-[12px] text-muted underline hover:text-danger" onClick={() => onChange({ suggest: suggest.filter((_, a) => a !== j) })}>
                  {t("configurazione.scorecardEditor.removeRule")}
                </button>
              </div>
            </li>
          ))}
        </ul>
        <div>
          <button type="button" className="text-[12.5px] font-semibold text-accent hover:underline" onClick={() => onChange({ suggest: [...suggest, { when: "", value: c.scale[0]?.value ?? 0 }] })}>
            {t("configurazione.scorecardEditor.addRule")}
          </button>
        </div>
        <label className="flex flex-col gap-1">
          <span className="label">{t("configurazione.scorecardEditor.suggestFact")}</span>
          <input className="field font-mono md:max-w-sm" value={c.suggest_fact ?? ""} onChange={(e) => onChange({ suggest_fact: e.target.value })} />
        </label>
      </div>
      <RowProblems problems={problems} prefix={`criteria[${i}]`} />
    </li>
  );
}
