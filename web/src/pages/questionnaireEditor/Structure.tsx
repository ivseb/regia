import { Select } from "../../select";
import { useState, type ReactNode } from "react";
import type { FactInfo } from "../../api";
import { conditionKeys, compiled, type QDefinition, type QOption, type QSection, type QType, type Question } from "../../condition";
import { ConditionBuilder } from "../processEditor/ConditionBuilder";
import {
  SCORABLE,
  TYPES,
  changeType,
  factsBefore,
  findQuestion,
  keyFrom,
  move,
  moveQuestion,
  moveSection,
  newQuestion,
  newSection,
  typeName,
  type Finding,
} from "./model";
import { useT } from "../../i18n/react";
import { tr } from "../../i18n/core";

// Struttura del questionario: sezioni e domande trascinabili (con i pulsanti ↑ ↓ per il
// tocco e la tastiera), editor della domanda a fisarmonica, condizioni "mostra se".

export type Change = (fn: (d: QDefinition) => void, coalesce?: string) => void;

export type Problems = (prefix: string) => Finding[];

type Drag = { kind: "q"; key: string } | { kind: "s"; index: number } | null;
type Over = { section: number; index: number } | null;

export const Field = ({ label, children, hint }: { label: string; children: ReactNode; hint?: string }) => (
  <label className="flex min-w-0 flex-col gap-1">
    <span className="label">{label}</span>
    {children}
    {hint && <span className="text-[12px] text-muted">{hint}</span>}
  </label>
);

export function Notes({ items }: { items: Finding[] }) {
  const t = useT();
  if (!items.length) return null;
  return (
    <ul className="m-0 flex list-none flex-col gap-0.5 p-0 text-[12.5px]">
      {items.map((p, i) => (
        <li key={i} className={p.level === "error" ? "text-danger" : "text-flag"}>
          {p.level === "error" ? t("configurazione.common.errore") : t("configurazione.common.attenzione")}: {p.message}.
        </li>
      ))}
    </ul>
  );
}

const iconBtn = "btn px-2 py-1 text-[13px] leading-none";

/** Una condizione si usa nel testo di qualche altra: serve a non cambiare il valore di una scelta citata. */
function valueUsed(d: QDefinition, value: string): boolean {
  const srcs = [...d.sections.flatMap((s) => [s.visible_when, ...s.questions.map((q) => q.visible_when)]), ...(d.derivations ?? []).map((r) => r.when)];
  const v = value.toLowerCase();
  return srcs.some((s) => !!s && s.toLowerCase().includes(v));
}

export function Structure({
  def,
  change,
  baseFacts,
  problems,
  open,
  setOpen,
}: {
  def: QDefinition;
  change: Change;
  baseFacts: FactInfo[];
  problems: Problems;
  open: string | null;
  setOpen: (k: string | null) => void;
}) {
  const t = useT();
  const [drag, setDrag] = useState<Drag>(null);
  const [over, setOver] = useState<Over>(null);
  const [adding, setAdding] = useState<number | null>(null);
  const [sectionOpen, setSectionOpen] = useState<Record<string, boolean>>({});
  const taken = () => [...def.sections.flatMap((s) => [s.key, ...s.questions.map((q) => q.key)])];

  const drop = () => {
    if (drag?.kind === "q" && over) change((d) => moveQuestion(d, drag.key, over.section, over.index));
    if (drag?.kind === "s" && over) change((d) => moveSection(d, drag.index, over.section > drag.index ? over.section - 1 : over.section));
    setDrag(null);
    setOver(null);
  };
  const line = (s: number, i: number) => over?.section === s && over.index === i && drag?.kind === "q";

  return (
    <div className="flex flex-col gap-6" onDragEnd={() => (setDrag(null), setOver(null))}>
      {def.sections.map((s, si) => {
        const secProblems = problems(`sections[${si}]`).filter((p) => !p.path.includes(".questions["));
        const collapsed = sectionOpen[s.key] === false;
        return (
          <section
            key={s.key}
            id={`ds-${s.key}`}
            className={`flex flex-col gap-3 border-t-2 border-rule pt-2 ${drag?.kind === "s" && over?.section === si ? "border-t-accent" : ""}`}
            onDragOver={(e) => {
              if (drag?.kind === "s") {
                e.preventDefault();
                const r = e.currentTarget.getBoundingClientRect();
                setOver({ section: e.clientY < r.top + r.height / 2 ? si : si + 1, index: 0 });
              } else if (drag?.kind === "q") {
                e.preventDefault();
                if (!(over?.section === si && over.index < s.questions.length)) setOver({ section: si, index: s.questions.length });
              }
            }}
            onDrop={(e) => (e.preventDefault(), drop())}
          >
            <div className="flex flex-wrap items-center gap-2">
              <span
                draggable
                role="img"
                aria-label={t("configurazione.qStructure.trascinaPerSpostareLa")}
                title={t("configurazione.qStructure.trascinaPerSpostareLa")}
                className="cursor-grab select-none px-1 text-[16px] leading-none text-muted"
                onDragStart={(e) => {
                  e.dataTransfer.setData("text/plain", s.key);
                  e.dataTransfer.effectAllowed = "move";
                  setDrag({ kind: "s", index: si });
                }}
              >
                ⋮⋮
              </span>
              <span className="font-mono text-[12px] text-muted tabular">{si + 1}</span>
              <input
                aria-label={t("configurazione.qEditor.structure.sectionTitleOf", { n: si + 1 })}
                className="field min-w-0 flex-1 border-0 border-b border-hair bg-transparent px-0 text-[17px] font-bold"
                placeholder={t("configurazione.qStructure.titoloDellaSezione")}
                value={s.title}
                onChange={(e) =>
                  change((d) => {
                    d.sections[si].title = e.target.value;
                  }, `s:${s.key}:title`)
                }
              />
              <button type="button" className={iconBtn} aria-label={t("configurazione.qStructure.spostaLaSezioneIn")} disabled={si === 0} onClick={() => change((d) => moveSection(d, si, si - 1))}>
                ↑
              </button>
              <button type="button" className={iconBtn} aria-label={t("configurazione.qStructure.spostaLaSezioneIn2")} disabled={si === def.sections.length - 1} onClick={() => change((d) => moveSection(d, si, si + 1))}>
                ↓
              </button>
              <button type="button" className={iconBtn} aria-expanded={!collapsed} onClick={() => setSectionOpen({ ...sectionOpen, [s.key]: collapsed })}>
                {collapsed ? t("configurazione.qStructure.apri") : t("configurazione.common.chiudi")}
              </button>
              <button
                type="button"
                className={`${iconBtn} hover:text-danger`}
                aria-label={t("configurazione.qStructure.togliLaSezione")}
                onClick={() => {
                  if (s.questions.length && !confirm(t("configurazione.qEditor.structure.removeSection", { title: s.title || s.key, count: s.questions.length }))) return;
                  change((d) => void d.sections.splice(si, 1));
                }}
              >
                ×
              </button>
            </div>
            <Notes items={secProblems} />
            {!collapsed && (
              <>
                <SectionSettings def={def} index={si} section={s} change={change} baseFacts={baseFacts} />
                <ol className="m-0 flex list-none flex-col p-0">
                  {s.questions.map((q, qi) => (
                    <li
                      key={q.key}
                      id={`dq-${q.key}`}
                      className="relative scroll-mt-20"
                      onDragOver={(e) => {
                        if (drag?.kind !== "q" || drag.key === q.key) return;
                        e.preventDefault();
                        e.stopPropagation();
                        const r = e.currentTarget.getBoundingClientRect();
                        setOver({ section: si, index: e.clientY < r.top + r.height / 2 ? qi : qi + 1 });
                      }}
                      onDrop={(e) => (e.preventDefault(), e.stopPropagation(), drop())}
                    >
                      {line(si, qi) && <span className="absolute inset-x-0 -top-px z-10 h-0.5 bg-accent" aria-hidden="true" />}
                      <QuestionRow
                        def={def}
                        q={q}
                        number={def.sections.slice(0, si).reduce((a, x) => a + x.questions.length, 0) + qi + 1}
                        sectionIndex={si}
                        index={qi}
                        count={s.questions.length}
                        isOpen={open === q.key}
                        toggle={() => setOpen(open === q.key ? null : q.key)}
                        change={change}
                        baseFacts={baseFacts}
                        problems={problems(`sections[${si}].questions[${qi}]`)}
                        onDragStart={() => setDrag({ kind: "q", key: q.key })}
                      />
                      {qi === s.questions.length - 1 && line(si, qi + 1) && <span className="absolute inset-x-0 -bottom-px z-10 h-0.5 bg-accent" aria-hidden="true" />}
                    </li>
                  ))}
                  {s.questions.length === 0 && (
                    <li className={`border border-dashed py-6 text-center text-[13px] text-muted ${over?.section === si && drag?.kind === "q" ? "border-accent" : "border-hair"}`}>
                      {t("configurazione.qStructure.nessunaDomandaAggiungineUna")}
                    </li>
                  )}
                </ol>
                {adding === si ? (
                  <div className="flex flex-col gap-2 border border-hair p-3">
                    <span className="label">{t("configurazione.qStructure.cheTipoDiDomanda")}</span>
                    <div className="grid grid-cols-1 gap-1.5 sm:grid-cols-2 lg:grid-cols-3">
                      {TYPES.map((t) => (
                        <button
                          key={t.type}
                          type="button"
                          className="flex min-h-[44px] flex-col items-start justify-center border border-hair px-3 py-1.5 text-left hover:border-fg"
                          onClick={() => {
                            const q = newQuestion(t.type, taken());
                            change((d) => void d.sections[si].questions.push(q));
                            setAdding(null);
                            setOpen(q.key);
                          }}
                        >
                          <span className="font-semibold">{t.name}</span>
                          <span className="text-[12px] text-muted">{t.hint}</span>
                        </button>
                      ))}
                    </div>
                    <button type="button" className="self-start text-[12.5px] text-muted underline" onClick={() => setAdding(null)}>
                      {t("configurazione.common.annulla")}
                    </button>
                  </div>
                ) : (
                  <div>
                    <button type="button" className="btn" onClick={() => setAdding(si)}>
                      {t("configurazione.qStructure.domanda")}
                    </button>
                  </div>
                )}
              </>
            )}
          </section>
        );
      })}
      <div className={`${drag?.kind === "s" && over?.section === def.sections.length ? "border-t-2 border-accent" : ""}`}>
        <button
          type="button"
          className="btn"
          onClick={() => {
            const s = newSection(def);
            change((d) => void d.sections.push(s));
          }}
        >
          {t("configurazione.qStructure.sezione")}
        </button>
      </div>
    </div>
  );
}

function SectionSettings({ def, index, section, change, baseFacts }: { def: QDefinition; index: number; section: QSection; change: Change; baseFacts: FactInfo[] }) {
  const t = useT();
  const [cond, setCond] = useState(!!section.visible_when);
  const set = (fn: (s: QSection) => void, co?: string) => change((d) => fn(d.sections[index]), co && `s:${section.key}:${co}`);
  return (
    <div className="flex flex-col gap-3 pl-0 sm:pl-6">
      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
        <Field label={t("configurazione.qStructure.descrizioneFacoltativa")}>
          <input className="field" value={section.description ?? ""} onChange={(e) => set((s) => (s.description = e.target.value), "desc")} />
        </Field>
        <Field label={t("configurazione.qStructure.chiLaCompila")} hint={t("configurazione.qStructure.conSoloIlTeam")}>
          <Select className="field" value={section.filled_by ?? ""} onChange={(e) => set((s) => (s.filled_by = e.target.value as "" | "team"))}>
            <option value="">{t("configurazione.qStructure.chiAvviaIlQuestionario")}</option>
            <option value="team">{t("configurazione.qStructure.soloIlTeamValutatore")}</option>
          </Select>
        </Field>
      </div>
      <div className="flex flex-col gap-2">
        <button type="button" className="self-start text-[12.5px] font-semibold text-accent hover:underline" aria-expanded={cond} onClick={() => setCond(!cond)}>
          {section.visible_when ? t("configurazione.qStructure.saltoDellaSezioneAttivo") : t("configurazione.qStructure.saltoDellaSezione")} {cond ? "▾" : "▸"}
        </button>
        {cond && (
          <ConditionBuilder
            label={t("configurazione.qStructure.mostraLaSezioneSolo")}
            empty={t("configurazione.qStructure.laSezioneSiMostra")}
            value={section.visible_when ?? ""}
            facts={factsBefore(def, baseFacts, { section: index })}
            onChange={(v) => set((s) => (s.visible_when = v), "when")}
          />
        )}
      </div>
    </div>
  );
}

function QuestionRow({
  def,
  q,
  number,
  sectionIndex,
  index,
  count,
  isOpen,
  toggle,
  change,
  baseFacts,
  problems,
  onDragStart,
}: {
  def: QDefinition;
  q: Question;
  number: number;
  sectionIndex: number;
  index: number;
  count: number;
  isOpen: boolean;
  toggle: () => void;
  change: Change;
  baseFacts: FactInfo[];
  problems: Finding[];
  onDragStart: () => void;
}) {
  const t = useT();
  const errors = problems.filter((p) => p.level === "error").length;
  const warnings = problems.length - errors;
  const scored = SCORABLE.includes(q.type);
  return (
    <div
      className={`border-b border-hair ${isOpen ? "bg-sel/40" : ""}`}
      draggable={!isOpen}
      onDragStart={(e) => {
        e.dataTransfer.setData("text/plain", q.key);
        e.dataTransfer.effectAllowed = "move";
        e.stopPropagation();
        onDragStart();
      }}
    >
      <button type="button" className="flex min-h-[44px] w-full items-center gap-2.5 px-1 py-2 text-left" aria-expanded={isOpen} aria-controls={`dqe-${q.key}`} onClick={toggle}>
        <span aria-hidden="true" className="cursor-grab select-none text-[15px] leading-none text-muted">
          ⋮⋮
        </span>
        <span className="w-6 shrink-0 font-mono text-[12px] text-muted tabular">{number}</span>
        <span className={`min-w-0 flex-1 truncate ${q.label ? "" : "text-muted"}`}>{q.label || t("configurazione.qStructure.domandaSenzaTesto")}</span>
        <span className="hidden shrink-0 flex-wrap items-center justify-end gap-x-2 text-[12px] text-muted sm:flex">
          {q.required && <span>{t("configurazione.qStructure.obbligatoria")}</span>}
          {q.visible_when && <span title={q.visible_when}>{t("configurazione.qStructure.condizionata")}</span>}
          {scored && (q.weight ?? 1) !== 1 && <span className="tabular">{t("configurazione.qEditor.structure.weightLabel", { weight: q.weight ?? 1 })}</span>}
        </span>
        <span className="shrink-0 border border-hair px-1.5 py-0.5 text-[11.5px] text-muted">{typeName(q.type)}</span>
        {(errors > 0 || warnings > 0) && (
          <span className={`shrink-0 font-mono text-[12px] ${errors ? "text-danger" : "text-flag"}`} title={problems.map((p) => p.message).join("\n")}>
            {errors || warnings}!
          </span>
        )}
      </button>
      {isOpen && (
        <div id={`dqe-${q.key}`} className="flex flex-col gap-4 border-t border-hair px-1 py-4 sm:pl-9">
          <QuestionEditor def={def} q={q} sectionIndex={sectionIndex} index={index} count={count} change={change} baseFacts={baseFacts} problems={problems} />
        </div>
      )}
    </div>
  );
}

function OptionList({
  title,
  list,
  scored,
  ownerValues,
  def,
  onChange,
  min,
}: {
  title: string;
  list: QOption[];
  scored: boolean;
  ownerValues: string[];
  def: QDefinition;
  onChange: (fn: (l: QOption[]) => void, coalesce?: string) => void;
  min: number;
}) {
  const t = useT();
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-baseline justify-between">
        <span className="label">{title}</span>
        {scored && <span className="label text-muted">{t("configurazione.qStructure.punti")}</span>}
      </div>
      <ul className="m-0 flex list-none flex-col gap-1.5 p-0">
        {list.map((o, i) => (
          <li key={o.value} className="flex items-center gap-1.5">
            <input
              aria-label={t("configurazione.qEditor.structure.entry", { title, n: i + 1 })}
              className="field min-w-0 flex-1 py-1.5"
              value={o.label}
              onChange={(e) =>
                onChange((l) => {
                  const others = l.filter((_, j) => j !== i).map((x) => x.value);
                  const derived = l[i].value === keyFrom(l[i].label, others);
                  l[i].label = e.target.value;
                  if (derived && e.target.value.trim() && !valueUsed(def, l[i].value)) l[i].value = keyFrom(e.target.value, others);
                }, `o:${o.value}`)
              }
            />
            {scored && (
              <input
                aria-label={t("configurazione.qEditor.structure.pointsOf", { label: o.label })}
                type="number"
                inputMode="decimal"
                className="field w-20 py-1.5 tabular"
                placeholder="—"
                value={o.score ?? ""}
                onChange={(e) =>
                  onChange((l) => {
                    if (e.target.value === "") delete l[i].score;
                    else l[i].score = Number(e.target.value);
                  }, `sc:${o.value}`)
                }
              />
            )}
            <button type="button" className={iconBtn} aria-label={t("configurazione.qStructure.spostaInSu")} disabled={i === 0} onClick={() => onChange((l) => move(l, i, i - 1))}>
              ↑
            </button>
            <button type="button" className={iconBtn} aria-label={t("configurazione.qStructure.spostaInGiu")} disabled={i === list.length - 1} onClick={() => onChange((l) => move(l, i, i + 1))}>
              ↓
            </button>
            <button
              type="button"
              className={`${iconBtn} hover:text-danger`}
              aria-label={t("configurazione.qEditor.structure.removeOption", { label: o.label })}
              disabled={list.length <= min}
              title={valueUsed(def, o.value) ? t("configurazione.qStructure.unaCondizioneUsaQuesta") : undefined}
              onClick={() => onChange((l) => void l.splice(i, 1))}
            >
              ×
            </button>
          </li>
        ))}
      </ul>
      <div>
        <button
          type="button"
          className="text-[12.5px] font-semibold text-accent hover:underline"
          onClick={() => onChange((l) => void l.push({ value: keyFrom("", [...l.map((x) => x.value), ...ownerValues], "voce"), label: "" }))}
        >
          {t("configurazione.qStructure.aggiungi")}
        </button>
      </div>
    </div>
  );
}

function QuestionEditor({
  def,
  q,
  sectionIndex,
  index,
  count,
  change,
  baseFacts,
  problems,
}: {
  def: QDefinition;
  q: Question;
  sectionIndex: number;
  index: number;
  count: number;
  change: Change;
  baseFacts: FactInfo[];
  problems: Finding[];
}) {
  const t = useT();
  const [cond, setCond] = useState(!!q.visible_when);
  const upd = (fn: (x: Question) => void, co?: string) =>
    change((d) => {
      const at = findQuestion(d, q.key);
      if (at) fn(d.sections[at[0]].questions[at[1]]);
    }, co && `q:${q.key}:${co}`);
  const scorable = SCORABLE.includes(q.type);
  const opts = (field: "options" | "rows") => (fn: (l: QOption[]) => void, co?: string) =>
    upd((x) => {
      x[field] ??= [];
      fn(x[field]!);
    }, co);
  return (
    <>
      <Notes items={problems} />
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-mono text-[12px] text-muted" title={t("configurazione.qStructure.chiaveDellaDomandaLa")}>
          {q.key}
        </span>
        <span className="ml-auto flex gap-1.5">
          <button type="button" className={iconBtn} aria-label={t("configurazione.qStructure.spostaLaDomandaIn")} disabled={index === 0 && sectionIndex === 0} onClick={() => change((d) => moveQuestion(d, q.key, index === 0 ? sectionIndex - 1 : sectionIndex, index === 0 ? undefined : index - 1))}>
            ↑
          </button>
          <button
            type="button"
            className={iconBtn}
            aria-label={t("configurazione.qStructure.spostaLaDomandaIn2")}
            disabled={index === count - 1 && sectionIndex === def.sections.length - 1}
            onClick={() => change((d) => moveQuestion(d, q.key, index === count - 1 ? sectionIndex + 1 : sectionIndex, index === count - 1 ? 0 : index + 2))}
          >
            ↓
          </button>
          <button
            type="button"
            className={iconBtn}
            onClick={() =>
              change((d) => {
                const at = findQuestion(d, q.key);
                if (!at) return;
                const taken = d.sections.flatMap((s) => [s.key, ...s.questions.map((x) => x.key)]);
                const copy = structuredClone(q);
                copy.key = keyFrom(q.label || q.key, taken);
                copy.label = q.label ? tr("configurazione.qEditor.structure.copySuffix", { label: q.label }) : "";
                d.sections[at[0]].questions.splice(at[1] + 1, 0, copy);
              })
            }
          >
            {t("configurazione.common.duplica")}
          </button>
          <button
            type="button"
            className={`${iconBtn} hover:text-danger`}
            onClick={() => {
              if (valueUsed(def, q.key) && !confirm(t("configurazione.qEditor.structure.confirmRemoveQuestion"))) return;
              change((d) => {
                const at = findQuestion(d, q.key);
                if (at) d.sections[at[0]].questions.splice(at[1], 1);
              });
            }}
          >
            {t("configurazione.common.togli")}
          </button>
        </span>
      </div>
      <Field label={t("configurazione.qStructure.testoDellaDomanda")}>
        <input className="field" value={q.label} autoFocus={!q.label} onChange={(e) => upd((x) => (x.label = e.target.value), "label")} />
      </Field>
      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
        <Field label={t("configurazione.common.tipo")}>
          <Select
            className="field"
            value={q.type}
            onChange={(e) =>
              change((d) => {
                const at = findQuestion(d, q.key);
                if (at) d.sections[at[0]].questions[at[1]] = changeType(d.sections[at[0]].questions[at[1]], e.target.value as QType);
              })
            }
          >
            {TYPES.map((t) => (
              <option key={t.type} value={t.type}>
                {t.name}
              </option>
            ))}
          </Select>
        </Field>
        <div className="flex flex-wrap items-end gap-x-5 gap-y-2 pb-1.5">
          <label className="inline-flex items-center gap-2">
            <input type="checkbox" checked={!!q.required} onChange={(e) => upd((x) => (x.required = e.target.checked))} />
            {t("configurazione.common.obbligatoria")}
          </label>
          {scorable && (
            <label className="inline-flex items-center gap-2" title={t("configurazione.qStructure.quantoContaLaDomanda")}>
              {t("configurazione.common.peso")}
              <input
                type="number"
                min={0}
                max={100}
                step="any"
                aria-label={t("configurazione.qStructure.pesoNelPunteggio")}
                className="field w-20 py-1 tabular"
                value={q.weight ?? 1}
                onChange={(e) => upd((x) => (e.target.value === "" || Number(e.target.value) === 1 ? delete x.weight : (x.weight = Number(e.target.value))), "w")}
              />
            </label>
          )}
        </div>
      </div>
      <Field label={t("configurazione.qStructure.aiutoPerChiCompila")}>
        <input className="field" value={q.help ?? ""} onChange={(e) => upd((x) => (x.help = e.target.value), "help")} />
      </Field>

      {q.type === "yes_no" && (
        <div className="grid grid-cols-2 gap-3 md:max-w-[360px]">
          {(["yes_score", "no_score"] as const).map((k) => (
            <Field key={k} label={k === "yes_score" ? t("configurazione.qStructure.puntiSeSi") : t("configurazione.qStructure.puntiSeNo")}>
              <input
                type="number"
                inputMode="decimal"
                className="field tabular"
                placeholder="—"
                value={q[k] ?? ""}
                onChange={(e) => upd((x) => (e.target.value === "" ? delete x[k] : (x[k] = Number(e.target.value))), k)}
              />
            </Field>
          ))}
        </div>
      )}
      {(q.type === "single" || q.type === "multi") && <OptionList title={t("configurazione.qStructure.scelte")} list={q.options ?? []} scored def={def} ownerValues={[]} min={2} onChange={opts("options")} />}
      {q.type === "matrix" && (
        <div className="grid grid-cols-1 gap-5 md:grid-cols-2">
          <OptionList title={t("configurazione.qStructure.righe")} list={q.rows ?? []} scored={false} def={def} ownerValues={[]} min={1} onChange={opts("rows")} />
          <OptionList title={t("configurazione.qStructure.colonneLeRisposte")} list={q.options ?? []} scored def={def} ownerValues={[]} min={2} onChange={opts("options")} />
        </div>
      )}
      {q.type === "scale" && (
        <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
          <Field label={t("configurazione.processEditor.from")}>
            <input type="number" className="field tabular" value={q.min ?? 1} onChange={(e) => upd((x) => (x.min = Number(e.target.value)), "min")} />
          </Field>
          <Field label={t("configurazione.processEditor.to")}>
            <input type="number" className="field tabular" value={q.max ?? 5} onChange={(e) => upd((x) => (x.max = Number(e.target.value)), "max")} />
          </Field>
          <Field label={t("configurazione.qStructure.etichettaDelMinimo")}>
            <input className="field" value={q.min_label ?? ""} onChange={(e) => upd((x) => (x.min_label = e.target.value), "ml")} />
          </Field>
          <Field label={t("configurazione.qStructure.etichettaDelMassimo")}>
            <input className="field" value={q.max_label ?? ""} onChange={(e) => upd((x) => (x.max_label = e.target.value), "xl")} />
          </Field>
          <label className="col-span-full inline-flex items-center gap-2">
            <input type="checkbox" checked={!!q.reverse} onChange={(e) => upd((x) => (x.reverse = e.target.checked))} />
            {t("configurazione.qStructure.piuAltoEPeggio")}
          </label>
        </div>
      )}
      {q.type === "number" && (
        <div className="grid grid-cols-3 gap-3 md:max-w-[460px]">
          <Field label={t("configurazione.fields.minimo")}>
            <input type="number" className="field tabular" value={q.min ?? ""} onChange={(e) => upd((x) => (e.target.value === "" ? delete x.min : (x.min = Number(e.target.value))), "min")} />
          </Field>
          <Field label={t("configurazione.fields.massimo")}>
            <input type="number" className="field tabular" value={q.max ?? ""} onChange={(e) => upd((x) => (e.target.value === "" ? delete x.max : (x.max = Number(e.target.value))), "max")} />
          </Field>
          <Field label={t("configurazione.qStructure.unita")}>
            <input className="field" placeholder={t("configurazione.qStructure.esMesi")} value={q.unit ?? ""} onChange={(e) => upd((x) => (x.unit = e.target.value), "unit")} />
          </Field>
        </div>
      )}
      {q.type === "text" && (
        <label className="inline-flex items-center gap-2">
          <input type="checkbox" checked={!!q.multiline} onChange={(e) => upd((x) => (x.multiline = e.target.checked))} />
          {t("configurazione.qStructure.suPiuRighe")}
        </label>
      )}
      {q.type !== "attachment" && (
        <Field label={t("configurazione.qStructure.precompilaDalDatoFacoltativo")} hint={t("configurazione.qStructure.unDatoDellaRichiesta")}>
          <input
            className="field font-mono text-[12.5px]"
            list="quest-facts"
            placeholder={t("configurazione.qStructure.esClassificazioneDati")}
            value={q.prefill ?? ""}
            onChange={(e) => upd((x) => (x.prefill = e.target.value), "pf")}
          />
        </Field>
      )}

      <div className="flex flex-col gap-2">
        <button type="button" className="self-start text-[12.5px] font-semibold text-accent hover:underline" aria-expanded={cond} onClick={() => setCond(!cond)}>
          {q.visible_when ? t("configurazione.qStructure.mostraSeAttivo") : t("configurazione.qStructure.mostraSe")} {cond ? "▾" : "▸"}
        </button>
        {cond && (
          <ConditionBuilder
            label={t("configurazione.qStructure.mostraLaDomandaSolo")}
            empty={t("configurazione.qStructure.laDomandaSiMostra")}
            value={q.visible_when ?? ""}
            facts={factsBefore(def, baseFacts, { question: q.key })}
            onChange={(v) => upd((x) => (x.visible_when = v), "when")}
          />
        )}
      </div>
    </>
  );
}

/** Chi cita questa domanda nelle condizioni (per avvisare prima di toglierla). */
export function citedBy(d: QDefinition, key: string): string[] {
  const out: string[] = [];
  const hit = (src?: string) => !!src && conditionKeys(compiled(src)).includes(key);
  for (const s of d.sections) {
    if (hit(s.visible_when)) out.push(s.title);
    for (const q of s.questions) if (hit(q.visible_when)) out.push(q.label || q.key);
  }
  for (const r of d.derivations ?? []) if (hit(r.when)) out.push(r.key);
  return out;
}
