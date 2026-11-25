import { EntitySelect } from "../select";
import { useStateName } from "../stateLabels";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { reviewsApi } from "../reviewsApi";
import { EmptyState } from "../emptyState";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ApiError,
  api,
  collab,

  type ReviewAnswer,
  type ReviewOutcome,
  type ReviewPrescription,
  type ReviewSummary,
  type ReviewView,
  type TransitionView,
} from "../api";
import { evaluate, scaleRange, type Derivation, type Facts, type QDefinition, type QEvaluation, type Question, type QSection } from "../condition";
import { PageHeader } from "../shell";
import { Comments } from "./Collaboration";
import { ErrorNote, SectionTitle, StageGate, ago, date, Loading } from "../ui";
import { useT } from "../i18n/react";
import { Trans } from "../i18n/Trans";
import { tr } from "../i18n/core";
import { fmtDateTime, fmtTime } from "../i18n/format";
import { crumbs } from "../crumbs";

// Valutazioni con questionari (piano, passo 4): inbox «Le mie valutazioni», pagina di
// compilazione ed esame, questionario condiviso con l'anteprima della configurazione.

// Etichette lette al momento dell'uso (getter): seguono la lingua corrente.
const STAGES: Record<string, string> = {
  get compile() { return tr("lavoro.reviews.stageCompile"); },
  get submitted() { return tr("lavoro.reviews.stageSubmitted"); },
  get exam() { return tr("lavoro.reviews.stageExam"); },
  get clarification() { return tr("lavoro.reviews.stageClarification"); },
  get concluded() { return tr("lavoro.reviews.stageConcluded"); },
  get cancelled() { return tr("lavoro.reviews.stageCancelled"); },
};

/** Scadenza dello SLA: in rosso se in ritardo, sospesa durante i chiarimenti. */
export function Due({ due, late, paused, stage }: { due: string | null; late: number; paused: boolean; stage: string }) {
  const t = useT();
  if (!due) return <span className="text-muted">—</span>;
  if (stage === "concluded" || stage === "cancelled") return <span className="text-muted tabular">{date(due)}</span>;
  if (paused) return <span className="text-muted tabular" title={t("lavoro.reviews.slaPaused")}>{date(due)} {t("lavoro.reviews.paused")}</span>;
  if (late > 0)
    return (
      <span className="font-semibold text-danger tabular" title={t("lavoro.reviews.lateBy", { n: late })}>
        {date(due)} · +{late} {t("lavoro.reviews.daysShort")}
      </span>
    );
  return <span className="tabular">{date(due)}</span>;
}

export function Outcome({ label, polarity }: { label: string | null; polarity: ReviewOutcome["polarity"] | null }) {
  if (!label) return <span className="text-muted">—</span>;
  const cls = polarity === "negative" ? "text-danger" : polarity === "positive" ? "text-ok" : "text-fg";
  return <span className={`font-semibold ${cls}`}>{label}</span>;
}

function Target({ r }: { r: Pick<ReviewSummary, "request_key" | "request_title" | "application" | "impact_key"> }) {
  const t = useT();
  return (
    <span className="flex min-w-0 flex-col">
      <span className="truncate">
        <span className="font-mono text-[12px] text-muted">{r.request_key}</span> {r.request_title}
      </span>
      {r.application && <span className="truncate text-[12.5px] text-muted">{t("lavoro.reviews.applicationLabel", { name: r.application })}</span>}
    </span>
  );
}

// ---------------------------------------------------------------------
// Inbox
// ---------------------------------------------------------------------

const INBOX_MAX = 5000;

export function ReviewInbox() {
  const t = useT();
  // Le 300 più urgenti per coda; con more si chiede il resto, a blocchi, fino a 5.000.
  const [limit, setLimit] = useState<number | undefined>(undefined);
  const inbox = useQuery({
    queryKey: limit ? ["reviews", "inbox", limit] : ["reviews", "inbox"],
    queryFn: () => reviewsApi.inbox(limit),
    placeholderData: (p) => p,
  });
  return (
    <>
      <PageHeader path={crumbs("demand", "reviews")} title={t("lavoro.reviews.inboxTitle")} />
      <div className="flex flex-col gap-8 px-4 pb-10 md:px-7">
        <ErrorNote error={inbox.error} />
        {inbox.isPending && <Loading className="text-muted" />}
        {inbox.data && (
          <>
            <InboxTable
              title={t("lavoro.reviews.toCompile")}
              hint={t("lavoro.reviews.toCompileHint")}
              items={inbox.data.compile}
              empty={
                <EmptyState
                  title={t("lavoro.reviews.noneToCompile")}
                  text={t("lavoro.reviews.noneToCompileText")}
                  action={
                    <Link to="/richieste" className="btn no-underline">
                      {t("lavoro.reviews.goToRequests")}
                    </Link>
                  }
                />
              }
            />
            <InboxTable
              title={t("lavoro.reviews.toExamine")}
              hint={t("lavoro.reviews.toExamineHint")}
              items={inbox.data.team}
              empty={
                <EmptyState
                  title={t("lavoro.reviews.noneToExamine")}
                  text={t("lavoro.reviews.noneToExamineText")}
                  action={
                    <Link to="/configurazione/valutazioni" className="btn no-underline">
                      {t("lavoro.reviews.configure")}
                    </Link>
                  }
                />
              }
            />
            {inbox.data.more && (
              <span className="flex flex-wrap items-center gap-3 text-[12.5px] text-muted">
                <span>{t("lavoro.reviews.mostUrgent")}</span>
                {Math.max(inbox.data.compile.length, inbox.data.team.length) < INBOX_MAX && (
                  <button
                    className="btn py-1 text-[12.5px]"
                    disabled={inbox.isFetching}
                    onClick={() => setLimit(Math.min(Math.max(inbox.data.compile.length, inbox.data.team.length) + 300, INBOX_MAX))}
                  >
                    {t("lavoro.reviews.showMore")}
                  </button>
                )}
              </span>
            )}
          </>
        )}
      </div>
    </>
  );
}

function InboxTable({ title, hint, items, empty }: { title: string; hint: string; items: ReviewSummary[]; empty: ReactNode }) {
  const t = useT();
  return (
    <section className="flex flex-col gap-2">
      <SectionTitle aside={<span className="font-mono text-[12px] text-muted tabular">{items.length}</span>}>{title}</SectionTitle>
      <p className="m-0 text-[12.5px] text-muted">{hint}</p>
      {items.length === 0 ? (
        empty
      ) : (
        <ul className="m-0 list-none p-0">
          {items.map((r) => (
            <li key={r.id} className="border-b border-hair">
              <Link
                to="/valutazioni/$key"
                params={{ key: r.key }}
                className="grid grid-cols-[minmax(0,1fr)_auto] gap-x-4 gap-y-1 py-2.5 text-fg no-underline hover:bg-sel/50 md:grid-cols-[96px_minmax(0,1.3fr)_minmax(0,1fr)_120px_130px] md:items-center"
              >
                <span className="font-mono text-[12px] font-medium text-accent">{r.key}</span>
                <span className="text-right md:order-last md:text-right">
                  <Due due={r.due_on} late={r.late_days} paused={r.paused} stage={r.stage} />
                </span>
                <span className="col-span-2 flex min-w-0 flex-col md:col-span-1">
                  <span className="font-semibold">{r.type_name}</span>
                  <span className="text-[12.5px] text-muted">
                    {r.team}
                    {r.mandatory ? ` ${t("lavoro.reviews.mandatoryDot")}` : ""}
                  </span>
                </span>
                <span className="col-span-2 min-w-0 md:col-span-1">
                  <Target r={r} />
                </span>
                <span className="text-[12.5px]">
                  {r.state}
                  {r.required_missing > 0 && r.stage !== "submitted" && r.stage !== "exam" && (
                    <span className="block text-muted tabular">{t("lavoro.reviews.requiredMissing", { n: r.required_missing })}</span>
                  )}
                </span>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

// ---------------------------------------------------------------------
// Questionario (pagina della valutazione e anteprima della configurazione)
// ---------------------------------------------------------------------

type QuestionnaireProps = {
  def: QDefinition;
  facts: Facts;
  values: Record<string, unknown>;
  evaluation: QEvaluation;
  meta?: Record<string, ReviewAnswer>;
  canAnswer: (section: string) => boolean;
  onChange: (key: string, value: unknown, immediate?: boolean) => void;
  onBlur?: () => void;
  /** Intestazione aggiuntiva della sezione (assegnazione). */
  sectionAside?: (s: QSection) => ReactNode;
  /** Controlli in fondo alla domanda (storia, allegati). */
  questionFooter?: (q: Question) => ReactNode;
  attachment?: (q: Question, disabled: boolean) => ReactNode;
};

export function Questionnaire(p: QuestionnaireProps) {
  const t = useT();
  return (
    <div className="flex flex-col gap-8">
      {p.def.sections.map((s) => {
        const st = p.evaluation.sections[s.key];
        if (st && !st.visible) return null;
        const editable = p.canAnswer(s.key);
        return (
          <section key={s.key} id={`sezione-${s.key}`} className="flex scroll-mt-24 flex-col gap-4" aria-label={s.title}>
            <div className="flex flex-col gap-1 border-b-2 border-rule pb-1.5">
              <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
                <h3 className="m-0 text-[17px] font-bold">{s.title}</h3>
                <span className="font-mono text-[12px] text-muted tabular">
                  {st ? `${st.answered}/${st.total}` : ""}
                  {st && st.missing > 0 ? ` · ${t("lavoro.reviews.requiredCount", { n: st.missing })}` : ""}
                </span>
              </div>
              {s.description && <p className="m-0 text-[12.5px] text-muted">{s.description}</p>}
              {p.sectionAside?.(s)}
            </div>
            {s.questions.map((q) =>
              p.evaluation.visible[q.key] === false ? null : (
                <QuestionField
                  key={q.key}
                  q={q}
                  value={p.values[q.key]}
                  meta={p.meta?.[q.key]}
                  missing={!!q.required && p.evaluation.missing.includes(q.key)}
                  disabled={!editable}
                  onChange={(v, immediate) => p.onChange(q.key, v, immediate)}
                  onBlur={p.onBlur}
                  factValue={q.prefill ? p.facts[q.prefill.toLowerCase()] : undefined}
                  footer={p.questionFooter?.(q)}
                  attachment={p.attachment}
                />
              ),
            )}
          </section>
        );
      })}
    </div>
  );
}

const choiceCls = (on: boolean, disabled: boolean) =>
  `flex min-h-[40px] items-center gap-2.5 border px-3 py-2 text-left ${on ? "border-accent bg-sel font-semibold" : "border-hair"} ${
    disabled ? "cursor-not-allowed opacity-70" : "hover:border-fg"
  }`;

function QuestionField({
  q,
  value,
  meta,
  missing,
  disabled,
  onChange,
  onBlur,
  factValue,
  footer,
  attachment,
}: {
  q: Question;
  value: unknown;
  meta?: ReviewAnswer;
  missing: boolean;
  disabled: boolean;
  onChange: (v: unknown, immediate?: boolean) => void;
  onBlur?: () => void;
  factValue?: unknown[];
  footer?: ReactNode;
  attachment?: QuestionnaireProps["attachment"];
}) {
  const t = useT();
  const id = `q-${q.key}`;
  const prefilled = meta?.source === "prefill";
  const input = () => {
    switch (q.type) {
      case "yes_no":
        return (
          <div role="radiogroup" aria-labelledby={`${id}-label`} className="grid grid-cols-2 gap-2 sm:flex">
            {[
              [true, t("lavoro.reviews.yes")],
              [false, t("lavoro.reviews.no")],
            ].map(([v, l]) => (
              <button
                key={String(v)}
                type="button"
                role="radio"
                aria-checked={value === v}
                disabled={disabled}
                className={`${choiceCls(value === v, disabled)} justify-center sm:min-w-[96px]`}
                onClick={() => onChange(value === v ? null : v, true)}
              >
                {l as string}
              </button>
            ))}
          </div>
        );
      case "single":
        return (
          <div role="radiogroup" aria-labelledby={`${id}-label`} className="flex flex-col gap-1.5">
            {(q.options ?? []).map((o) => (
              <button
                key={o.value}
                type="button"
                role="radio"
                aria-checked={value === o.value}
                disabled={disabled}
                className={choiceCls(value === o.value, disabled)}
                onClick={() => onChange(value === o.value ? null : o.value, true)}
              >
                <span aria-hidden="true" className={`size-3.5 shrink-0 rounded-full border ${value === o.value ? "border-[4px] border-accent" : "border-muted"}`} />
                {o.label}
              </button>
            ))}
          </div>
        );
      case "multi": {
        const sel = Array.isArray(value) ? (value as string[]) : [];
        return (
          <div role="group" aria-labelledby={`${id}-label`} className="flex flex-col gap-1.5">
            {(q.options ?? []).map((o) => {
              const on = sel.includes(o.value);
              return (
                <button
                  key={o.value}
                  type="button"
                  role="checkbox"
                  aria-checked={on}
                  disabled={disabled}
                  className={choiceCls(on, disabled)}
                  onClick={() => onChange(on ? sel.filter((x) => x !== o.value) : [...sel, o.value], true)}
                >
                  <span aria-hidden="true" className={`flex size-3.5 shrink-0 items-center justify-center border ${on ? "border-accent bg-accent text-accent-fg" : "border-muted"} text-[10px]`}>
                    {on ? "✓" : ""}
                  </span>
                  {o.label}
                </button>
              );
            })}
          </div>
        );
      }
      case "scale": {
        const [lo, hi] = scaleRange(q);
        const steps = Array.from({ length: hi - lo + 1 }, (_, i) => lo + i);
        return (
          <div className="flex flex-col gap-1.5">
            <div role="radiogroup" aria-labelledby={`${id}-label`} className="flex flex-wrap gap-1.5">
              {steps.map((n) => (
                <button
                  key={n}
                  type="button"
                  role="radio"
                  aria-checked={value === n}
                  disabled={disabled}
                  className={`${choiceCls(value === n, disabled)} min-w-[44px] justify-center tabular`}
                  onClick={() => onChange(value === n ? null : n, true)}
                >
                  {n}
                </button>
              ))}
            </div>
            {(q.min_label || q.max_label) && (
              <div className="flex justify-between gap-3 text-[12px] text-muted">
                <span>{q.min_label}</span>
                <span className="text-right">{q.max_label}</span>
              </div>
            )}
          </div>
        );
      }
      case "matrix": {
        const m = (value && typeof value === "object" && !Array.isArray(value) ? value : {}) as Record<string, string>;
        const cols = q.options ?? [];
        return (
          <div className="flex flex-col gap-3" role="group" aria-labelledby={`${id}-label`}>
            {(q.rows ?? []).map((r) => (
              <fieldset key={r.value} className="m-0 flex min-w-0 flex-col gap-1.5 border-0 border-t border-hair p-0 pt-2">
                <legend className="float-left w-full p-0 text-[13.5px] font-semibold">{r.label}</legend>
                <div role="radiogroup" aria-label={r.label} className="grid grid-cols-[repeat(auto-fit,minmax(110px,1fr))] gap-1.5">
                  {cols.map((c) => (
                    <button
                      key={c.value}
                      type="button"
                      role="radio"
                      aria-checked={m[r.value] === c.value}
                      disabled={disabled}
                      className={`${choiceCls(m[r.value] === c.value, disabled)} justify-center text-center text-[13px]`}
                      onClick={() => {
                        const next = { ...m };
                        if (next[r.value] === c.value) delete next[r.value];
                        else next[r.value] = c.value;
                        onChange(Object.keys(next).length ? next : null, true);
                      }}
                    >
                      {c.label}
                    </button>
                  ))}
                </div>
              </fieldset>
            ))}
          </div>
        );
      }
      case "number":
        return (
          <div className="flex items-center gap-2">
            <input
              id={id}
              type="number"
              inputMode="decimal"
              className="field max-w-[220px] tabular"
              disabled={disabled}
              min={q.min}
              max={q.max}
              value={typeof value === "number" ? value : ""}
              onChange={(e) => onChange(e.target.value === "" ? null : Number(e.target.value))}
              onBlur={onBlur}
            />
            {q.unit && <span className="text-muted">{q.unit}</span>}
          </div>
        );
      case "date":
        return (
          <input
            id={id}
            type="date"
            className="field max-w-[220px] tabular"
            disabled={disabled}
            value={typeof value === "string" ? value : ""}
            onChange={(e) => onChange(e.target.value || null, true)}
          />
        );
      case "attachment":
        return attachment ? attachment(q, disabled) : <p className="m-0 text-[12.5px] text-muted">{t("lavoro.reviews.attachmentsHint")}</p>;
      default:
        return q.multiline ? (
          <textarea
            id={id}
            className="field min-h-[96px] leading-relaxed"
            disabled={disabled}
            value={typeof value === "string" ? value : ""}
            onChange={(e) => onChange(e.target.value)}
            onBlur={onBlur}
          />
        ) : (
          <input
            id={id}
            className="field"
            disabled={disabled}
            value={typeof value === "string" ? value : ""}
            onChange={(e) => onChange(e.target.value)}
            onBlur={onBlur}
          />
        );
    }
  };
  return (
    <div className={`flex flex-col gap-2 ${prefilled ? "border-l-2 border-accent pl-3" : ""}`}>
      <label id={`${id}-label`} htmlFor={["text", "number", "date"].includes(q.type) ? id : undefined} className="flex flex-col gap-0.5">
        <span className="text-[14.5px] leading-snug font-semibold">
          {q.label}
          {q.required && (
            <span className={missing ? "text-danger" : "text-muted"} title={t("lavoro.reviews.mandatory")}>
              {" "}*
            </span>
          )}
        </span>
        {q.help && <span className="text-[12.5px] text-muted">{q.help}</span>}
      </label>
      {prefilled && (
        <p className="m-0 flex flex-wrap items-center gap-x-2 text-[12px] text-accent">
          <span className="label">{t("lavoro.reviews.prefilled")}</span>
          <span>{t("lavoro.reviews.prefilledFrom", { name: q.prefill ?? "", values: factValue?.length ? ` (${factValue.map(String).join(", ")})` : "" })}</span>
          {!disabled && (
            <button type="button" className="underline" onClick={() => onChange(value, true)}>
              {t("lavoro.reviews.confirm")}
            </button>
          )}
        </p>
      )}
      {input()}
      {footer}
    </div>
  );
}

/** Punteggio ottenuto, soglie e esito proposto: nella valutazione (solo per il team) e nell'anteprima del modello. */
export function ScoreBox({ ev, def, outcomes }: { ev: QEvaluation; def: QDefinition; outcomes?: { key: string; label: string }[] }) {
  const t = useT();
  const th = [...(def.scoring?.thresholds ?? [])].sort((a, b) => a.min - b.min);
  const label = (k: string) => outcomes?.find((o) => o.key === k)?.label ?? k.replaceAll("_", " ");
  const pct = Math.max(0, Math.min(100, ev.percent ?? 0));
  return (
    <section className="flex flex-col gap-2" aria-label={t("lavoro.reviews.score")}>
      <SectionTitle aside={<span className="font-mono text-[12px] text-muted tabular">{t("lavoro.reviews.points", { score: ev.score ?? 0, max: ev.max_score ?? 0 })}</span>}>{t("lavoro.reviews.score")}</SectionTitle>
      <p className="m-0 flex items-baseline gap-2">
        <span className="text-[26px] font-bold leading-none tabular">{pct}%</span>
        {ev.proposed_outcome ? <span className="text-[13px]">{t("lavoro.reviews.proposes", { outcome: label(ev.proposed_outcome) })}</span> : <span className="text-[13px] text-muted">{t("lavoro.reviews.noOutcomeProposed")}</span>}
      </p>
      <div className="relative h-2 bg-hair" aria-hidden="true">
        <span className="absolute inset-y-0 left-0 bg-accent" style={{ width: `${pct}%` }} />
        {th.map((x) => (
          <span key={x.min} className="absolute inset-y-[-3px] w-px bg-fg" style={{ left: `${x.min}%` }} />
        ))}
      </div>
      {th.length > 0 && (
        <ul className="m-0 flex list-none flex-col gap-0.5 p-0 text-[12px] text-muted">
          {th.map((x) => (
            <li key={x.min} className={x.outcome === ev.proposed_outcome ? "font-semibold text-fg" : ""}>
              <span className="tabular">{t("lavoro.reviews.fromPct", { n: x.min })}</span> · {label(x.outcome)}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

/** Le prescrizioni che le risposte propongono, calcolate dal vivo. */
/** Requisiti proposti dalle regole del questionario: alla chiusura (esito favorevole o con prescrizioni) diventano requisiti veri. */
export function RequirementProposals({ proposals }: { proposals: { rule_key: string; title?: string; text: string; category: string; method: string; check?: string | null; requirement?: string | null }[] }) {
  const t = useT();
  return (
    <section className="flex flex-col gap-2" aria-label={t("lavoro.requisitiQ.proposti.titolo")} data-testid="requirement-proposals">
      <SectionTitle aside={<span className="font-mono text-[12px] text-muted tabular">{proposals.length}</span>}>{t("lavoro.requisitiQ.proposti.titolo")}</SectionTitle>
      <p className="m-0 text-[12.5px] text-muted">{t("lavoro.requisitiQ.proposti.guida")}</p>
      {proposals.length === 0 ? (
        <p className="m-0 text-[12.5px] text-muted">{t("lavoro.requisitiQ.proposti.nessuno")}</p>
      ) : (
        <ul className="m-0 list-none p-0">
          {proposals.map((d) => (
            <li key={d.rule_key} className="flex flex-col gap-0.5 border-b border-hair py-2">
              {d.category && <span className="label text-muted">{d.category}</span>}
              {d.title && <span className="font-semibold">{d.title}</span>}
              <span>{d.text}</span>
              <span className="text-[12px] text-muted">
                {d.method === "evidence" ? t("lavoro.requisitiQ.proposti.daEvidenza", { check: d.check ?? "" }) : t("lavoro.requisitiQ.proposti.manuale")}
                {d.requirement && (
                  <>
                    {" · "}
                    <Link to="/e/$key" params={{ key: d.requirement }} className="font-mono text-accent no-underline hover:underline">
                      {d.requirement}
                    </Link>
                  </>
                )}
              </span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

export function DerivedList({ derived, title }: { derived: Derivation[]; title?: string }) {
  const t = useT();
  return (
    <section className="flex flex-col gap-2">
      <SectionTitle aside={<span className="font-mono text-[12px] text-muted tabular">{derived.length}</span>}>{title ?? t("lavoro.reviews.derivedTitle")}</SectionTitle>
      {derived.length === 0 ? (
        <p className="m-0 text-[12.5px] text-muted">{t("lavoro.reviews.derivedNone")}</p>
      ) : (
        <ul className="m-0 list-none p-0">
          {derived.map((d) => (
            <li key={d.key} className="flex flex-col gap-0.5 border-b border-hair py-2">
              {d.category && <span className="label text-muted">{d.category}</span>}
              <span>{d.text}</span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

// ---------------------------------------------------------------------
// Pagina della valutazione
// ---------------------------------------------------------------------

const same = (a: unknown, b: unknown) => JSON.stringify(a ?? null) === JSON.stringify(b ?? null);

type SaveState = { state: "idle" | "saving" | "saved" | "error"; at?: Date; error?: unknown };

export function ReviewPage({ reviewKey }: { reviewKey: string }) {
  const t = useT();
  const stateName = useStateName();
  const qc = useQueryClient();
  const view = useQuery({ queryKey: ["review", reviewKey], queryFn: () => reviewsApi.get(reviewKey) });
  // Modifiche locali non ancora confermate dal server: valgono sulle risposte caricate,
  // così un aggiornamento in tempo reale non cancella ciò che si sta scrivendo.
  const [local, setLocal] = useState<Record<string, unknown>>({});
  const pending = useRef<Record<string, unknown>>({});
  const timer = useRef<number | undefined>(undefined);
  const [save, setSave] = useState<SaveState>({ state: "idle" });

  const flush = async () => {
    window.clearTimeout(timer.current);
    const batch = pending.current;
    if (Object.keys(batch).length === 0) return;
    pending.current = {};
    setSave({ state: "saving" });
    try {
      const out: Record<string, unknown> = {};
      for (const [k, v] of Object.entries(batch)) out[k] = v === "" ? null : v;
      const nv = await reviewsApi.saveAnswers(reviewKey, out);
      qc.setQueryData(["review", reviewKey], nv);
      setLocal((l) => {
        const n = { ...l };
        for (const k of Object.keys(batch)) if (same(n[k], batch[k])) delete n[k];
        return n;
      });
      setSave({ state: "saved", at: new Date() });
      qc.invalidateQueries({ queryKey: ["reviews"] });
    } catch (e) {
      pending.current = { ...batch, ...pending.current };
      setSave({ state: "error", error: e });
    }
  };
  useEffect(() => () => window.clearTimeout(timer.current), []);
  // Uscendo dalla pagina con modifiche in sospeso il browser avvisa.
  useEffect(() => {
    const h = (e: BeforeUnloadEvent) => {
      if (Object.keys(pending.current).length) e.preventDefault();
    };
    window.addEventListener("beforeunload", h);
    return () => window.removeEventListener("beforeunload", h);
  }, []);

  const change = (key: string, value: unknown, immediate = false) => {
    setLocal((l) => ({ ...l, [key]: value }));
    pending.current[key] = value;
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(flush, immediate ? 250 : 900);
  };

  const v = view.data;
  const values = useMemo(() => {
    const base: Record<string, unknown> = {};
    for (const [k, a] of Object.entries(v?.answers ?? {})) base[k] = a.value;
    return { ...base, ...local };
  }, [v?.answers, local]);
  const evaluation = useMemo(() => (v ? evaluate(v.template, v.facts, values) : null), [v, values]);

  if (view.error) return <div className="px-4 py-8 md:px-7"><ErrorNote error={view.error} /></div>;
  if (!v || !evaluation) return <Loading className="px-4 py-8 text-muted md:px-7" />;
  const r = v.review;
  const canAnswer = (s: string) => v.can.answer_all || v.can.sections.includes(s);
  const anyEditable = v.can.answer_all || v.can.sections.length > 0;
  const refresh = (nv?: ReviewView) => {
    if (nv) qc.setQueryData(["review", reviewKey], nv);
    else qc.invalidateQueries({ queryKey: ["review", reviewKey] });
    qc.invalidateQueries({ queryKey: ["reviews"] });
  };
  const pct = evaluation.total ? Math.round((evaluation.answered / evaluation.total) * 100) : 0;

  return (
    <>
      <PageHeader path={`${crumbs("demand", "reviews")} / ${r.key}`} title={r.type_name} />
      <div className="flex flex-col gap-3 px-4 pb-4 md:px-7">
        <p className="m-0 flex flex-wrap gap-x-4 gap-y-1 text-[13px]">
          <span>
            {t("lavoro.reviews.request")}{" "}
            <Link to="/e/$key" params={{ key: r.request.key }} className="font-mono text-[12.5px]">
              {r.request.key}
            </Link>{" "}
            {r.request.title}
          </span>
          {r.application && (
            <span>
              {t("lavoro.reviews.application")} <span className="font-semibold">{r.application}</span>
              {r.impact && <span className="font-mono text-[12px] text-muted"> ({r.impact.key})</span>}
            </span>
          )}
          <span>
            {t("lavoro.reviews.team")} <span className="font-semibold">{r.team}</span>
          </span>
          <span className="text-muted">
            {r.mandatory ? t("lavoro.reviews.mandatoryCap") : t("lavoro.reviews.optional")} · {t("lavoro.reviews.templateVersion", { version: r.template_version })}
            {r.template_current_version && r.template_current_version !== r.template_version ? ` ${t("lavoro.reviews.currentVersion", { version: r.template_current_version })}` : ""}
            {r.opened_by_rule ? ` ${t("lavoro.reviews.openedByRule")}` : ""}
          </span>
        </p>
        {r.superseded_by && (
          <p className="m-0 text-[13px] text-flag">
            {t("lavoro.reviews.supersededBy")}{" "}
            <Link to="/valutazioni/$key" params={{ key: r.superseded_by }} className="font-mono">
              {r.superseded_by}
            </Link>
          </p>
        )}
        {v.phases.length > 0 && (
          <div className="flex max-w-[760px] flex-col gap-1">
            <StageGate states={v.phases} current={v.element.state} size="lg" />
            <span className="text-[12.5px] text-muted">
              {t("lavoro.reviews.phase")} <span className="font-semibold text-fg">{stateName(v.element.state, v.phases.find((p) => p.name === v.element.state)?.labels)}</span>
            </span>
          </div>
        )}
        <Transitions v={v} beforeMove={flush} onDone={() => refresh()} />
      </div>

      <div className="flex flex-col gap-8 px-4 pb-12 md:px-7 lg:flex-row lg:items-start">
        <div className="flex min-w-0 flex-1 flex-col gap-6 lg:max-w-[760px]">
          {/* Avanzamento sempre visibile: sul telefono resta in alto mentre si scorre. */}
          <div className="sticky top-[49px] z-30 -mx-4 flex flex-col gap-1.5 border-b border-hair bg-bg px-4 py-2 md:top-0 md:mx-0 md:px-0">
            <div className="flex items-baseline justify-between gap-3 text-[12.5px]">
              <span className="tabular">
                <span className="font-semibold">{evaluation.answered}</span>/{evaluation.total} {t("lavoro.reviews.answers")}
                {evaluation.missing.length > 0 && <span className="text-muted"> · {t("lavoro.reviews.requiredMissing", { n: evaluation.missing.length })}</span>}
              </span>
              <SaveStatus save={save} />
            </div>
            <span className="flex h-1 bg-hair" aria-hidden="true">
              <span className="bg-accent" style={{ width: `${pct}%` }} />
            </span>
            <nav aria-label={t("lavoro.reviews.sections")} className="-mx-1 flex gap-1 overflow-x-auto pt-0.5 lg:hidden">
              {v.template.sections.map((s) => {
                const st = evaluation.sections[s.key];
                if (!st?.visible) return null;
                return (
                  <a key={s.key} href={`#sezione-${s.key}`} className="shrink-0 border border-hair px-2 py-1 text-[12px] text-fg no-underline">
                    {s.title} <span className="font-mono text-muted tabular">{st.answered}/{st.total}</span>
                  </a>
                );
              })}
            </nav>
          </div>
          {v.can.read_only_reason && !anyEditable && <p className="m-0 text-[13px] text-muted">{t("lavoro.reviews.readOnlyReason", { reason: v.can.read_only_reason })}</p>}
          {!v.can.read_only_reason && !anyEditable && (
            <p className="m-0 text-[13px] text-muted">{t("lavoro.reviews.readOnlyWho")}</p>
          )}
          <Questionnaire
            def={v.template}
            facts={v.facts}
            values={values}
            evaluation={evaluation}
            meta={v.answers}
            canAnswer={canAnswer}
            onChange={change}
            onBlur={flush}
            sectionAside={(s) => <Assignee v={v} section={s} onDone={refresh} />}
            questionFooter={(q) => <QuestionHistory reviewKey={reviewKey} q={q} meta={v.answers[q.key]} />}
            attachment={(q, disabled) => (
              <AttachmentAnswer reviewKey={reviewKey} v={v} value={values[q.key]} disabled={disabled} onChange={(ids) => change(q.key, ids, true)} />
            )}
          />
          <Comments refKey={r.key} />
        </div>

        <aside className="flex w-full flex-col gap-7 lg:sticky lg:top-4 lg:w-[340px] lg:shrink-0">
          <section className="flex flex-col gap-2">
            <SectionTitle>{t("lavoro.reviews.deadline")}</SectionTitle>
            <p className="m-0 text-[13px]">
              {r.due_on ? (
                <>
                  <Trans k="lavoro.reviews.teamHasUntil" slots={{ due: <Due due={r.due_on} late={r.late_days} paused={r.paused} stage={r.stage} /> }} />
                  {r.late_days > 0 && <span className="block text-danger">{t("lavoro.reviews.lateDays", { n: r.late_days })}</span>}
                  {r.paused && <span className="block text-muted">{t("lavoro.reviews.slaPausedClarifications")}</span>}
                </>
              ) : (
                <span className="text-muted">{t("lavoro.reviews.slaStarts")}</span>
              )}
            </p>
            <p className="m-0 text-[12.5px] text-muted">{t("lavoro.reviews.stage", { name: STAGES[r.stage] ?? r.stage })}</p>
          </section>
          <nav aria-label={t("lavoro.reviews.questionnaireSections")} className="hidden flex-col gap-2 lg:flex">
            <SectionTitle>{t("lavoro.reviews.sections")}</SectionTitle>
            <ul className="m-0 list-none p-0">
              {v.template.sections.map((s) => {
                const st = evaluation.sections[s.key];
                if (!st?.visible) return null;
                const who = v.assignments.find((a) => a.section === s.key);
                return (
                  <li key={s.key} className="border-b border-hair">
                    <a href={`#sezione-${s.key}`} className="flex items-baseline justify-between gap-2 py-1.5 text-fg no-underline hover:text-accent">
                      <span className="min-w-0">
                        {s.title}
                        {who && <span className="block truncate text-[12px] text-muted">{who.name}</span>}
                      </span>
                      <span className={`font-mono text-[12px] tabular ${st.missing ? "text-muted" : "text-ok"}`}>
                        {st.answered}/{st.total}
                      </span>
                    </a>
                  </li>
                );
              })}
            </ul>
          </nav>
          {v.can.is_team && evaluation.scored && <ScoreBox ev={evaluation} def={v.template} outcomes={v.outcomes} />}
          {r.stage === "compile" || r.stage === "clarification" ? (
            <>
              <DerivedList derived={evaluation.derived} />
              {evaluation.derived_requirements.length > 0 && <RequirementProposals proposals={evaluation.derived_requirements.map((d) => ({ rule_key: d.key, title: d.title, text: d.text, category: d.category ?? "", method: d.method ?? "manual", check: d.check }))} />}
            </>
          ) : (
            <>
              <Prescriptions v={v} reviewKey={reviewKey} onDone={refresh} />
              {v.requirement_proposals.length > 0 && <RequirementProposals proposals={v.requirement_proposals} />}
            </>
          )}
          <Decision v={v} reviewKey={reviewKey} onDone={refresh} proposedOutcome={v.can.is_team ? evaluation.proposed_outcome : undefined} />
        </aside>
      </div>
    </>
  );
}

function SaveStatus({ save }: { save: SaveState }) {
  const t = useT();
  switch (save.state) {
    case "saving":
      return <span className="text-muted">{t("lavoro.reviews.saving")}</span>;
    case "saved":
      return <span className="text-muted">{t("lavoro.reviews.savedAt", { time: fmtTime(save.at) })}</span>;
    case "error":
      return (
        <span role="alert" className="text-danger">
          {t("lavoro.reviews.notSaved", { reason: save.error instanceof ApiError ? save.error.message : t("lavoro.reviews.networkError") })}
        </span>
      );
  }
  return <span className="text-muted">{t("lavoro.reviews.autosave")}</span>;
}

function Transitions({ v, beforeMove, onDone }: { v: ReviewView; beforeMove: () => Promise<void>; onDone: () => void }) {
  const t = useT();
  const [pending, setPending] = useState<TransitionView | null>(null);
  const [comment, setComment] = useState("");
  const move = useMutation({
    mutationFn: async ({ to, c }: { to: string; c?: string }) => {
      await beforeMove();
      return api.transition(v.element.id, to, c);
    },
    onSuccess: () => {
      setPending(null);
      setComment("");
      onDone();
    },
  });
  if (v.transitions.length === 0) return null;
  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap gap-2">
        {v.transitions.map((x) => (
          <button
            key={x.to + x.label}
            className={`btn ${x.blocked.length ? "" : "btn-primary"}`}
            disabled={x.blocked.length > 0 || move.isPending}
            title={x.blocked.join("; ") || undefined}
            onClick={() => (x.comment_required ? setPending(x) : move.mutate({ to: x.to }))}
          >
            {x.label || x.to}
          </button>
        ))}
      </div>
      {v.transitions.some((x) => x.blocked.length) && (
        <p className="m-0 text-[12.5px] text-muted">{v.transitions.flatMap((x) => x.blocked).join(" · ")}</p>
      )}
      {pending && (
        <form
          className="flex max-w-[560px] flex-col gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            move.mutate({ to: pending.to, c: comment });
          }}
        >
          <label className="flex flex-col gap-1">
            <span className="label">{t("lavoro.reviews.reasonFor", { label: pending.label })}</span>
            <textarea className="field min-h-[72px]" value={comment} onChange={(e) => setComment(e.target.value)} required autoFocus />
          </label>
          <div className="flex gap-2">
            <button className="btn btn-primary" disabled={!comment.trim() || move.isPending}>
              {t("lavoro.reviews.confirm")}
            </button>
            <button type="button" className="btn" onClick={() => setPending(null)}>
              {t("lavoro.reviews.cancel")}
            </button>
          </div>
        </form>
      )}
      <ErrorNote error={move.error} />
    </div>
  );
}

function Assignee({ v, section, onDone }: { v: ReviewView; section: QSection; onDone: (nv?: ReviewView) => void }) {
  const t = useT();
  const who = v.assignments.find((a) => a.section === section.key);
  const assign = useMutation({
    mutationFn: (member: string) => reviewsApi.assign(v.review.key, section.key, member),
    onSuccess: (nv) => onDone(nv),
  });
  if (!v.can.assign) {
    return who ? <span className="text-[12.5px] text-muted">{t("lavoro.reviews.assignedToName", { name: who.name })}</span> : null;
  }
  return (
    <label className="flex flex-wrap items-center gap-2 text-[12.5px] text-muted">
      {t("lavoro.reviews.assignedTo")}
      <EntitySelect
        kind="member"
        className="border-0 border-b border-hair bg-transparent py-0.5 text-[12.5px] text-fg"
        noneLabel={t("lavoro.reviews.nobodyStarter")}
        filter={(o) => !o.requester || o.value === who?.member_id}
        value={who?.member_id ?? ""}
        disabled={assign.isPending}
        onChange={(v) => assign.mutate(v)}
      />
      {assign.error && <span className="text-danger">{assign.error instanceof ApiError ? assign.error.message : t("lavoro.reviews.error")}</span>}
    </label>
  );
}

function formatValue(q: Question, v: unknown): string {
  if (v == null) return tr("lavoro.reviews.deleted");
  if (typeof v === "boolean") return v ? tr("lavoro.reviews.yes") : tr("lavoro.reviews.no");
  if (q.type === "matrix" && typeof v === "object" && !Array.isArray(v))
    return Object.entries(v as Record<string, string>)
      .map(([r, c]) => `${q.rows?.find((x) => x.value === r)?.label ?? r}: ${q.options?.find((o) => o.value === c)?.label ?? c}`)
      .join("; ");
  if (Array.isArray(v)) return q.type === "attachment" ? tr("lavoro.reviews.attachmentsCount", { count: v.length }) : v.map((x) => q.options?.find((o) => o.value === x)?.label ?? String(x)).join(", ");
  if (q.type === "single") return q.options?.find((o) => o.value === v)?.label ?? String(v);
  if (q.type === "date") return date(String(v));
  return String(v);
}

function QuestionHistory({ reviewKey, q, meta }: { reviewKey: string; q: Question; meta?: ReviewAnswer }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const hist = useQuery({ queryKey: ["review-history", reviewKey, q.key], queryFn: () => reviewsApi.history(reviewKey, q.key), enabled: open });
  if (!meta) return null;
  return (
    <div className="flex flex-col gap-1 text-[12px] text-muted">
      <span className="flex flex-wrap gap-x-2">
        <span>
          {meta.source === "prefill" ? t("lavoro.reviews.prefilled") : t("lavoro.reviews.answerBy", { name: meta.by ?? "—" })} {ago(meta.at)}
        </span>
        <button type="button" className="underline hover:text-fg" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
          {open ? t("lavoro.reviews.hideHistory") : t("lavoro.reviews.history")}
        </button>
      </span>
      {open && (
        <ol className="m-0 flex list-none flex-col gap-0.5 border-l border-hair p-0 pl-3">
          {(hist.data?.items ?? []).map((h) => (
            <li key={h.id}>
              <span className="tabular">{fmtDateTime(h.at, { dateStyle: "short", timeStyle: "short" })}</span> ·{" "}
              {h.source === "prefill" ? t("lavoro.reviews.prefill") : h.by ?? "—"}: <span className="text-fg">{formatValue(q, h.value)}</span>
            </li>
          ))}
          {hist.isPending && <li>{t("lavoro.reviews.loading")}</li>}
        </ol>
      )}
    </div>
  );
}

function AttachmentAnswer({
  reviewKey,
  v,
  value,
  disabled,
  onChange,
}: {
  reviewKey: string;
  v: ReviewView;
  value: unknown;
  disabled: boolean;
  onChange: (ids: string[] | null) => void;
}) {
  const t = useT();
  const qc = useQueryClient();
  const ids = Array.isArray(value) ? (value as string[]) : [];
  const [names, setNames] = useState<Record<string, string>>({});
  const [progress, setProgress] = useState<number | null>(null);
  const [error, setError] = useState<unknown>(null);
  const nameOf = (id: string) => v.attachments.find((a) => a.id === id)?.name ?? names[id] ?? t("lavoro.reviews.attachment");
  const upload = async (files: FileList | null) => {
    if (!files?.length) return;
    setError(null);
    const added: string[] = [];
    try {
      for (const f of Array.from(files)) {
        setProgress(0);
        const a = await collab.upload(reviewKey, f, undefined, setProgress);
        added.push(a.id);
        setNames((n) => ({ ...n, [a.id]: a.name }));
      }
      onChange([...ids, ...added]);
      qc.invalidateQueries({ queryKey: ["attachments", reviewKey] });
    } catch (e) {
      setError(e);
    } finally {
      setProgress(null);
    }
  };
  return (
    <div className="flex flex-col gap-1.5">
      {ids.length > 0 && (
        <ul className="m-0 list-none p-0">
          {ids.map((id) => (
            <li key={id} className="flex items-center justify-between gap-2 border-b border-hair py-1.5">
              <a href={collab.contentUrl(id, true)} className="truncate">
                {nameOf(id)}
              </a>
              {!disabled && (
                <button type="button" className="text-[12px] text-muted underline" onClick={() => onChange(ids.filter((x) => x !== id).length ? ids.filter((x) => x !== id) : null)}>
                  {t("lavoro.reviews.remove")}
                </button>
              )}
            </li>
          ))}
        </ul>
      )}
      {!disabled && (
        <label className="btn self-start">
          {progress != null ? t("lavoro.reviews.uploading", { pct: Math.round(progress * 100) }) : t("lavoro.reviews.uploadFile")}
          <input type="file" multiple className="sr-only" onChange={(e) => upload(e.target.files)} />
        </label>
      )}
      <ErrorNote error={error} />
    </div>
  );
}

function Prescriptions({ v, reviewKey, onDone }: { v: ReviewView; reviewKey: string; onDone: (nv?: ReviewView) => void }) {
  const t = useT();
  const [text, setText] = useState("");
  const [category, setCategory] = useState("");
  const update = useMutation({
    mutationFn: ({ p, status }: { p: ReviewPrescription; status: string }) => reviewsApi.updatePrescription(reviewKey, p.id, { status }),
    onSuccess: (nv) => onDone(nv),
  });
  const add = useMutation({
    mutationFn: () => reviewsApi.addPrescription(reviewKey, text, category),
    onSuccess: (nv) => {
      setText("");
      setCategory("");
      onDone(nv);
    },
  });
  const remove = useMutation({ mutationFn: (p: ReviewPrescription) => reviewsApi.removePrescription(reviewKey, p.id), onSuccess: (nv) => onDone(nv) });
  const can = v.can.prescribe;
  const groups: [string, ReviewPrescription[]][] = [
    [t("lavoro.reviews.groupProposed"), v.prescriptions.filter((p) => p.status === "proposed")],
    [t("lavoro.reviews.groupAccepted"), v.prescriptions.filter((p) => p.status === "accepted")],
    [t("lavoro.reviews.groupRejected"), v.prescriptions.filter((p) => p.status === "rejected")],
  ];
  return (
    <section className="flex flex-col gap-3">
      <SectionTitle aside={<span className="font-mono text-[12px] text-muted tabular">{v.prescriptions.filter((p) => p.status === "accepted").length}</span>}>
        {t("lavoro.reviews.prescriptions")}
      </SectionTitle>
      {v.prescriptions.length === 0 && <p className="m-0 text-[12.5px] text-muted">{t("lavoro.reviews.noPrescriptions")}</p>}
      {groups.map(([label, items]) =>
        items.length === 0 ? null : (
          <div key={label} className="flex flex-col">
            <span className="label pb-1 text-muted">{label}</span>
            <ul className="m-0 list-none p-0">
              {items.map((p) => (
                <li key={p.id} className="flex flex-col gap-1 border-b border-hair py-2">
                  {p.category && <span className="label text-muted">{p.category}</span>}
                  <span className={p.status === "rejected" ? "text-muted line-through" : ""}>{p.text}</span>
                  <span className="text-[12px] text-muted">
                    {p.source === "rule" ? t("lavoro.reviews.fromAnswers") : t("lavoro.reviews.addedBy", { name: p.created_by ?? "—" })}
                    {p.decided_by && p.source === "rule" && p.status !== "proposed"
                      ? ` · ${t(p.status === "accepted" ? "lavoro.reviews.acceptedBy" : "lavoro.reviews.rejectedBy", { name: p.decided_by })}`
                      : ""}
                  </span>
                  {can && (
                    <span className="flex flex-wrap gap-3 text-[12.5px]">
                      {p.status !== "accepted" && (
                        <button className="text-accent underline" disabled={update.isPending} onClick={() => update.mutate({ p, status: "accepted" })}>
                          {t("lavoro.reviews.accept")}
                        </button>
                      )}
                      {p.status !== "rejected" && p.source === "rule" && (
                        <button className="text-muted underline" disabled={update.isPending} onClick={() => update.mutate({ p, status: "rejected" })}>
                          {t("lavoro.reviews.reject")}
                        </button>
                      )}
                      {p.source === "manual" && (
                        <button className="text-muted underline" disabled={remove.isPending} onClick={() => remove.mutate(p)}>
                          {t("lavoro.reviews.remove")}
                        </button>
                      )}
                    </span>
                  )}
                </li>
              ))}
            </ul>
          </div>
        ),
      )}
      {can && (
        <form
          className="flex flex-col gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            add.mutate();
          }}
        >
          <label className="flex flex-col gap-1">
            <span className="label">{t("lavoro.reviews.newPrescription")}</span>
            <textarea className="field min-h-[64px]" value={text} onChange={(e) => setText(e.target.value)} />
          </label>
          <div className="flex gap-2">
            <input className="field" placeholder={t("lavoro.reviews.categoryOptional")} value={category} onChange={(e) => setCategory(e.target.value)} />
            <button className="btn" disabled={!text.trim() || add.isPending}>
              {t("lavoro.reviews.add")}
            </button>
          </div>
        </form>
      )}
      <ErrorNote error={update.error ?? add.error ?? remove.error} />
    </section>
  );
}

function Decision({ v, reviewKey, onDone, proposedOutcome }: { v: ReviewView; reviewKey: string; onDone: (nv?: ReviewView) => void; proposedOutcome?: string }) {
  const t = useT();
  const r = v.review;
  const [outcome, setOutcome] = useState("");
  const [note, setNote] = useState("");
  const decide = useMutation({ mutationFn: () => reviewsApi.decide(reviewKey, outcome, note), onSuccess: (nv) => onDone(nv) });
  if (r.outcome_label) {
    return (
      <section className="flex flex-col gap-2">
        <SectionTitle>{t("lavoro.reviews.outcome")}</SectionTitle>
        <p className="m-0 text-[17px]">
          <Outcome label={r.outcome_label} polarity={r.outcome_polarity} />
        </p>
        {r.decision_note && <p className="m-0 whitespace-pre-line">{r.decision_note}</p>}
        <p className="m-0 text-[12.5px] text-muted">
          {r.decided_by ?? "—"} · {r.decided_at ? fmtDateTime(r.decided_at, { dateStyle: "medium", timeStyle: "short" }) : ""}
        </p>
      </section>
    );
  }
  if (!v.can.decide) {
    if (r.stage !== "exam") return null;
    return (
      <section className="flex flex-col gap-2">
        <SectionTitle>{t("lavoro.reviews.outcome")}</SectionTitle>
        <p className="m-0 text-[12.5px] text-muted">
          {t(r.deciders === "lead" ? "lavoro.reviews.decidedByLead" : "lavoro.reviews.decidedByTeam", { team: r.team })}
        </p>
      </section>
    );
  }
  const proposed = v.prescriptions.filter((p) => p.status === "proposed").length;
  const accepted = v.prescriptions.filter((p) => p.status === "accepted").length;
  const sel = v.outcomes.find((o) => o.key === outcome);
  const hint =
    proposed > 0
      ? t("lavoro.reviews.hintProposed", { n: proposed })
      : sel?.polarity === "conditional" && accepted === 0
        ? t("lavoro.reviews.hintConditional")
        : sel?.polarity === "positive" && accepted > 0
          ? t("lavoro.reviews.hintPositive")
          : sel?.polarity === "negative" && !note.trim()
            ? t("lavoro.reviews.hintNegative")
            : "";
  return (
    <section className="flex flex-col gap-2">
      <SectionTitle>{t("lavoro.reviews.outcome")}</SectionTitle>
      {proposedOutcome && v.outcomes.some((o) => o.key === proposedOutcome) && outcome !== proposedOutcome && (
        <p className="m-0 flex flex-wrap items-center gap-x-2 text-[12.5px] text-accent">
          <span>{t("lavoro.reviews.scoreProposes", { outcome: v.outcomes.find((o) => o.key === proposedOutcome)?.label ?? "" })}</span>
          <button type="button" className="underline" onClick={() => setOutcome(proposedOutcome)}>
            {t("lavoro.reviews.chooseThis")}
          </button>
        </p>
      )}
      <div role="radiogroup" aria-label={t("lavoro.reviews.outcome")} className="flex flex-col gap-1.5">
        {v.outcomes.map((o) => (
          <button key={o.key} type="button" role="radio" aria-checked={outcome === o.key} className={choiceCls(outcome === o.key, false)} onClick={() => setOutcome(o.key)}>
            <span aria-hidden="true" className={`size-3.5 shrink-0 rounded-full border ${outcome === o.key ? "border-[4px] border-accent" : "border-muted"}`} />
            <Outcome label={o.label} polarity={o.polarity} />
          </button>
        ))}
      </div>
      <label className="flex flex-col gap-1">
        <span className="label">{t("lavoro.reviews.reason")}</span>
        <textarea className="field min-h-[80px]" value={note} onChange={(e) => setNote(e.target.value)} />
      </label>
      {hint && <p className="m-0 text-[12.5px] text-muted">{hint}</p>}
      <button className="btn btn-primary self-start" disabled={!outcome || !!hint || decide.isPending} onClick={() => decide.mutate()}>
        {t("lavoro.reviews.recordOutcome")}
      </button>
      <ErrorNote error={decide.error} />
    </section>
  );
}
