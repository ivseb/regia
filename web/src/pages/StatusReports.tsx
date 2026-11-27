// Scheda «Stato» della richiesta (demand.md §11, D11; piano, passo 13): status report
// periodici con i semafori per ambito, precompilati dai dati (Gantt, budget, valutazioni,
// impatti) e corretti dal PM; storico, confronto con il precedente, presa visione dello
// sponsor, export in PDF (stampa del browser) e Markdown.
import { Select } from "../select";
import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError } from "../api";
import {
  DIMENSIONS,
  FREQUENCIES,
  RAG_LABEL,
  reportApi,
  reportsOf,
  type Comparison,
  type ProjRef,
  type ReportCalls,
  type ReportKind,
  type Frequency,
  type RAG,
  type ReportList,
  type Sections,
  type StatusReport,
} from "../reportApi";
import { Markdown } from "../markdown";
import { ErrorNote, SectionTitle, date, Loading } from "../ui";
import { money, pct, ratio } from "./finance-ui";
import "../print.css";
import { useT } from "../i18n/react";
import { Trans } from "../i18n/Trans";
import { tr, type Key } from "../i18n/core";
import { fmtDate, fmtNumber } from "../i18n/format";

// ---------------------------------------------------------------------
// Semaforo: un quadrato pieno per verde (--ok) e rosso (--danger), mezzo pieno per il
// giallo. L'arancio resta agli obblighi normativi; il nome è sempre scritto accanto.

export function RagMark({ value, size = 10 }: { value: RAG; size?: number }) {
  const cls = value === "green" ? "bg-ok border-ok" : "border-danger";
  return (
    <span aria-hidden="true" className={`relative inline-block shrink-0 border-2 ${cls}`} style={{ width: size, height: size }}>
      {value === "amber" && <span className="absolute inset-y-0 left-0 w-1/2 bg-danger" />}
      {value === "red" && <span className="absolute inset-0 bg-danger" />}
    </span>
  );
}

export function Rag({ value, label = true }: { value: RAG; label?: boolean }) {
  return (
    <span className="inline-flex items-center gap-1.5 whitespace-nowrap" title={RAG_LABEL[value]}>
      <RagMark value={value} />
      {label ? <span className={value === "green" ? "" : "font-semibold"}>{RAG_LABEL[value]}</span> : <span className="sr-only">{RAG_LABEL[value]}</span>}
    </span>
  );
}

const SECTION_LABELS: { key: keyof Omit<Sections, "notes">; label: Key; hint: Key }[] = [
  { key: "summary", label: "lavoro.statusreports.secSummary", hint: "lavoro.statusreports.hintSummary" },
  { key: "done", label: "lavoro.statusreports.secDone", hint: "lavoro.statusreports.hintDone" },
  { key: "next", label: "lavoro.statusreports.secNext", hint: "lavoro.statusreports.hintNext" },
  { key: "risks", label: "lavoro.statusreports.secRisks", hint: "lavoro.statusreports.hintRisks" },
  { key: "decisions", label: "lavoro.statusreports.secDecisions", hint: "lavoro.statusreports.hintDecisions" },
];

function period(r: { period_start: string | null; period_end: string }) {
  return r.period_start ? `${date(r.period_start)} – ${date(r.period_end)}` : tr("lavoro.statusreports.periodUntil", { date: date(r.period_end) });
}

// ---------------------------------------------------------------------

// Le chiamate dipendono da cosa porta i report: la richiesta (predefinita) o il progetto (passo 5).
const ApiCtx = createContext<ReportCalls>(reportApi);
const useReports = () => useContext(ApiCtx);
const KindCtx = createContext<ReportKind>("request");

export function StatusReportsTab({ refKey, kind = "request" }: { refKey: string; kind?: ReportKind }) {
  const api = useMemo(() => reportsOf(kind), [kind]);
  return (
    <ApiCtx.Provider value={api}>
      <KindCtx.Provider value={kind}>
        <ReportsBody refKey={refKey} />
      </KindCtx.Provider>
    </ApiCtx.Provider>
  );
}

function ReportsBody({ refKey }: { refKey: string }) {
  const t = useT();
  const qc = useQueryClient();
  const reportApi = useReports();
  const list = useQuery({ queryKey: ["status-reports", refKey], queryFn: () => reportApi.list(refKey) });
  const [open, setOpen] = useState<string | null>(null);
  const [compare, setCompare] = useState<string | null>(null);
  const create = useMutation({
    mutationFn: () => reportApi.create(refKey),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["status-reports", refKey] }),
  });
  if (list.error) return <ErrorNote error={list.error} />;
  if (!list.data) return <Loading className="m-0 text-muted" />;
  const { items, can_edit } = list.data;
  const draft = items.find((r) => r.status === "draft");
  const sent = items.filter((r) => r.status === "submitted");
  const shown = open ? items.find((r) => r.id === open) : sent[0];
  return (
    <div className="flex flex-col gap-6">
      <ScheduleBar refKey={refKey} data={list.data} />
      {can_edit && !draft && (
        <div className="flex flex-wrap items-center gap-3">
          <button className="btn btn-primary" disabled={create.isPending} onClick={() => create.mutate()}>
            {create.isPending ? t("lavoro.statusreports.preparing") : t("lavoro.statusreports.new")}
          </button>
          <span className="text-[12.5px] text-muted">{t("lavoro.statusreports.newHint")}</span>
          <ErrorNote error={create.error} />
        </div>
      )}
      {draft && can_edit && <DraftEditor key={draft.id} report={draft} refKey={refKey} />}
      {sent.length === 0 && !draft && <p className="m-0 text-muted">{t("lavoro.statusreports.noneSent")}</p>}
      {compare && shown && <CompareView id={compare} onClose={() => setCompare(null)} />}
      {!compare && shown && shown.status === "submitted" && <ReportView report={shown} refKey={refKey} onCompare={() => setCompare(shown.id)} />}
      {sent.length > 0 && <History items={sent} current={shown?.id} onOpen={(id) => { setOpen(id); setCompare(null); }} />}
    </div>
  );
}

function ScheduleBar({ refKey, data }: { refKey: string; data: ReportList }) {
  const t = useT();
  const kind = useContext(KindCtx);
  const reportApi = useReports();
  const qc = useQueryClient();
  const s = data.schedule;
  const save = useMutation({
    mutationFn: (v: { frequency: string; next_due?: string }) => reportApi.schedule(refKey, v.frequency, v.next_due),
    onSuccess: (d) => qc.setQueryData(["status-reports", refKey], d),
  });
  return (
    <section className="flex flex-col gap-2" aria-label={t("lavoro.statusreports.frequency")}>
      <SectionTitle>{t("lavoro.statusreports.frequency")}</SectionTitle>
      <div className="flex flex-wrap items-end gap-x-6 gap-y-3 text-[13px]">
        <div className="flex flex-col gap-0.5">
          <span className="label text-muted">{t("lavoro.statusreports.every")}</span>
          {data.can_edit ? (
            <Select
              className="field py-1"
              aria-label={t("lavoro.statusreports.frequencyOfReports")}
              value={s.frequency ?? ""}
              onChange={(e) => save.mutate({ frequency: e.target.value })}
            >
              <option value="">{t("lavoro.statusreports.likeOrg", { value: FREQUENCIES[s.tenant_frequency].toLowerCase() })}</option>
              {(Object.keys(FREQUENCIES) as Frequency[]).map((f) => (
                <option key={f} value={f}>{FREQUENCIES[f]}</option>
              ))}
            </Select>
          ) : (
            <span className="font-semibold">{FREQUENCIES[s.effective_frequency]}</span>
          )}
        </div>
        <div className="flex flex-col gap-0.5">
          <span className="label text-muted">{t("lavoro.statusreports.nextReport")}</span>
          {data.can_edit && s.effective_frequency !== "off" ? (
            <input
              type="date"
              className="field py-1"
              aria-label={t("lavoro.statusreports.nextReportDate")}
              value={s.next_due ?? ""}
              onChange={(e) => save.mutate({ frequency: s.frequency ?? "", next_due: e.target.value })}
            />
          ) : (
            <span className="font-mono tabular">{s.next_due ? date(s.next_due) : "—"}</span>
          )}
        </div>
        {s.overdue && <span className="font-semibold text-danger">{t("lavoro.statusreports.reportLate")}</span>}
        {!s.expected && kind === "project" && <span className="text-[12px] text-muted">{t("progetti.passo5.report.reminder", { days: s.reminder_days })}</span>}
        {!s.expected && kind !== "project" && (
          <span className="text-[12px] text-muted">
            {t("lavoro.statusreports.reminder", {
              phases: s.expected_in_phases.map((p) => t("lavoro.statusreports.quoted", { text: p })).join(", ") || t("lavoro.statusreports.configured"),
              days: s.reminder_days,
            })}
          </span>
        )}
      </div>
      <ErrorNote error={save.error} />
    </section>
  );
}

// ---------------------------------------------------------------------
// Bozza

function DraftEditor({ report, refKey }: { report: StatusReport; refKey: string }) {
  const t = useT();
  const reportApi = useReports();
  const qc = useQueryClient();
  const [r, setR] = useState(report);
  const [dirty, setDirty] = useState(false);
  const refreshList = () => qc.invalidateQueries({ queryKey: ["status-reports", refKey] });
  const save = useMutation({
    mutationFn: () =>
      reportApi.update(r.id, {
        version: r.version, overall: r.overall, schedule: r.schedule, cost: r.cost, scope: r.scope, risk: r.risk,
        period_start: r.period_start ?? "", period_end: r.period_end, sections: r.sections,
      }),
    onSuccess: (x) => {
      setR(x);
      setDirty(false);
    },
  });
  const submit = useMutation({
    mutationFn: async () => {
      const cur = dirty ? await save.mutateAsync() : r;
      return reportApi.submit(cur.id);
    },
    onSuccess: refreshList,
  });
  const refresh = useMutation({
    mutationFn: async () => {
      if (dirty) await save.mutateAsync();
      return reportApi.refresh(r.id);
    },
    onSuccess: (x) => {
      setR(x);
      setDirty(false);
    },
  });
  const remove = useMutation({ mutationFn: () => reportApi.remove(r.id), onSuccess: refreshList });
  const set = (patch: Partial<StatusReport>) => {
    setR((x) => ({ ...x, ...patch }));
    setDirty(true);
  };
  const setSection = (k: keyof Sections, v: string) => set({ sections: { ...r.sections, [k]: v } });
  const setNote = (k: string, v: string) => set({ sections: { ...r.sections, notes: { ...r.sections.notes, [k]: v } } });
  const sug = r.data.suggested;
  const err = save.error ?? submit.error ?? refresh.error ?? remove.error;
  return (
    <section className="flex flex-col gap-5 border-l-2 border-accent pl-4" aria-label={t("lavoro.statusreports.draftLabel")}>
      <SectionTitle aside={<span className="font-mono text-[11.5px] text-muted">{t("lavoro.statusreports.draftVersion", { version: r.version })}</span>}>{t("lavoro.statusreports.new")}</SectionTitle>
      <div className="flex flex-wrap gap-4">
        <label className="flex flex-col gap-1">
          <span className="label text-muted">{t("lavoro.statusreports.from")}</span>
          <input type="date" className="field py-1" value={r.period_start ?? ""} onChange={(e) => set({ period_start: e.target.value || null })} />
        </label>
        <label className="flex flex-col gap-1">
          <span className="label text-muted">{t("lavoro.statusreports.to")}</span>
          <input type="date" className="field py-1" required value={r.period_end} onChange={(e) => set({ period_end: e.target.value })} />
        </label>
      </div>

      <fieldset className="m-0 flex flex-col gap-0 border-0 p-0">
        <legend className="label mb-2 p-0">{t("lavoro.statusreports.lights")}</legend>
        <p className="m-0 mb-2 text-[12.5px] text-muted">{t("lavoro.statusreports.lightsHint", { date: date(r.data.as_of) })}</p>
        {DIMENSIONS.map((d) => {
          const v = r[d.key];
          const proposed = sug[d.key];
          const why = sug.reasons[d.key] ?? [];
          return (
            <div key={d.key} className="grid grid-cols-1 gap-2 border-b border-hair py-2.5 sm:grid-cols-[130px_minmax(0,1fr)] sm:items-start sm:gap-x-4">
              <span className="font-semibold">{d.label}</span>
              <div role="radiogroup" aria-label={d.label} className="flex flex-wrap self-start justify-self-start border border-hair text-[12.5px]">
                {(["green", "amber", "red"] as RAG[]).map((x) => (
                  <button
                    key={x}
                    type="button"
                    role="radio"
                    aria-checked={v === x}
                    className={`flex items-center gap-1.5 px-2.5 py-1 ${v === x ? "bg-sel font-semibold" : "text-muted hover:text-fg"}`}
                    onClick={() => set({ [d.key]: x } as Partial<StatusReport>)}
                  >
                    <RagMark value={x} />
                    {RAG_LABEL[x]}
                  </button>
                ))}
              </div>
              <div className="flex min-w-0 flex-col gap-1 sm:col-start-2">
                {d.key !== "overall" && (
                  <input
                    className="field py-1 text-[12.5px]"
                    aria-label={t("lavoro.statusreports.noteOn", { name: d.label.toLowerCase() })}
                    placeholder={t("lavoro.statusreports.notePlaceholder")}
                    value={r.sections.notes[d.key] ?? ""}
                    onChange={(e) => setNote(d.key, e.target.value)}
                  />
                )}
                <span className="text-[12px] text-muted">
                  {t("lavoro.statusreports.suggested", { value: RAG_LABEL[proposed].toLowerCase() })}
                  {why.length > 0 && ` · ${why.join("; ")}`}
                  {v !== proposed && <span className="font-semibold text-fg"> {t("lavoro.statusreports.correctedByHand")}</span>}
                </span>
              </div>
            </div>
          );
        })}
      </fieldset>

      <DataPanel report={r} />

      {SECTION_LABELS.map((s) => (
        <label key={s.key} className="flex flex-col gap-1.5">
          <span className="label">{t(s.label)}</span>
          <textarea
            className={`field leading-relaxed ${s.key === "summary" ? "min-h-20" : "min-h-28"}`}
            value={r.sections[s.key]}
            onChange={(e) => setSection(s.key, e.target.value)}
          />
          <span className="text-[12px] text-muted">{t(s.hint)} {t("lavoro.statusreports.markdownHint")}</span>
        </label>
      ))}

      <div className="flex flex-wrap items-center gap-2">
        <button className="btn btn-primary" disabled={submit.isPending || !r.sections.summary.trim()} onClick={() => submit.mutate()}>
          {submit.isPending ? t("lavoro.statusreports.sending") : t("lavoro.statusreports.send")}
        </button>
        <button className="btn" disabled={!dirty || save.isPending} onClick={() => save.mutate()}>
          {save.isPending ? t("lavoro.statusreports.saving") : dirty ? t("lavoro.statusreports.saveDraft") : t("lavoro.statusreports.draftSaved")}
        </button>
        <button className="btn" disabled={refresh.isPending} onClick={() => refresh.mutate()} title={t("lavoro.statusreports.refreshTitle")}>
          {t("lavoro.statusreports.refresh")}
        </button>
        <button className="btn btn-danger ml-auto" disabled={remove.isPending} onClick={() => window.confirm(t("lavoro.statusreports.deleteConfirm")) && remove.mutate()}>
          {t("lavoro.statusreports.deleteDraft")}
        </button>
      </div>
      <p className="m-0 text-[12px] text-muted">{t("lavoro.statusreports.afterSend")}</p>
      {err instanceof ApiError && err.status === 409 ? (
        <p role="alert" className="m-0 text-danger">{t("lavoro.statusreports.conflict")}</p>
      ) : (
        <ErrorNote error={err} />
      )}
      <Link to="/e/$key" params={{ key: refKey }} search={{ scheda: "piano" }} className="self-start text-[12px] text-accent underline">
        {t("lavoro.statusreports.openPlan")}
      </Link>
    </section>
  );
}

// ---------------------------------------------------------------------
// Dati del periodo: avanzamento, costi, valutazioni, impatti

function Fact({ label, value, note, tone }: { label: string; value: ReactNode; note?: ReactNode; tone?: "danger" | "flag" }) {
  return (
    <div className="flex min-w-0 flex-col gap-0.5 border-l-2 border-hair pl-2.5">
      <dt className="label text-muted">{label}</dt>
      <dd className={`m-0 font-mono text-[16px] leading-tight font-semibold tabular break-words ${tone === "danger" ? "text-danger" : tone === "flag" ? "text-flag" : ""}`}>{value}</dd>
      {note && <dd className="m-0 text-[11.5px] text-muted">{note}</dd>}
    </div>
  );
}

/** Lavoro completato contro atteso: due righe sottili sulla stessa scala. */
function Progress({ actual, planned }: { actual: number | null; planned: number | null }) {
  const t = useT();
  if (actual == null || planned == null) return null;
  return (
    <div className="flex flex-col gap-1" role="img" aria-label={t("lavoro.statusreports.progressLabel", { done: pct(actual), expected: pct(planned) })}>
      {(
        [
          [t("lavoro.statusreports.progressDone"), actual, "bg-fg"],
          [t("lavoro.statusreports.progressExpected"), planned, "bg-muted"],
        ] as const
      ).map(([l, v, cls]) => (
        <div key={l} className="grid grid-cols-[76px_minmax(0,1fr)_44px] items-center gap-2 text-[11.5px]">
          <span className="text-muted">{l}</span>
          <span className="relative h-1.5 bg-hair">
            <span className={`absolute inset-y-0 left-0 ${cls}`} style={{ width: `${Math.min(v, 100)}%` }} />
          </span>
          <span className="text-right font-mono tabular">{pct(v)}</span>
        </div>
      ))}
    </div>
  );
}

function DataPanel({ report }: { report: StatusReport }) {
  const d = report.data;
  if (d.project) return <ProjectDataPanel report={report} />;
  return <RequestDataPanel report={report} />;
}

/** I numeri del progetto fotografati nel report (passo 5): gli stessi della Panoramica. */
function ProjectDataPanel({ report }: { report: StatusReport }) {
  const t = useT();
  const d = report.data;
  const pd = d.project!;
  const c = d.cost;
  const sc = d.schedule;
  const ref = (r: ProjRef) => (
    <>
      <Link to="/e/$key" params={{ key: r.key }} className="font-mono text-accent no-underline hover:underline">{r.key}</Link>{" "}
      {r.restricted ? <span className="text-muted">{t("progetti.restricted")}</span> : r.title}
    </>
  );
  const rows: { key: string; body: ReactNode }[] = [
    ...pd.late.map((r) => ({ key: "l" + r.id, body: <>{ref(r)} — <span className="text-danger">{t("progetti.passo5.report.late", { count: r.days_late })}</span></> })),
    ...pd.over_tolerance.map((r) => ({ key: "o" + r.id, body: <>{ref(r)} — <span className="text-danger">{t("progetti.passo5.report.overTolerance", { count: r.days_late })}</span></> })),
    ...pd.scope_added.map((r) => ({ key: "a" + r.id, body: <>{ref(r)} — <span className="font-semibold">{t("progetti.passo5.scope.added")}</span></> })),
    ...pd.scope_removed.map((r) => ({ key: "r" + r.id, body: <>{ref(r)} — <span className="font-semibold">{t("progetti.passo5.scope.removed")}</span></> })),
    ...pd.risks.map((r) => ({ key: "k" + r.id, body: <>{t("progetti.passo5.report.risk")} {ref(r)}</> })),
  ];
  const counters = [
    pd.blocked > 0 && t("progetti.passo5.report.blocked", { count: pd.blocked }),
    pd.claims_pending > 0 && t("progetti.passo5.report.claims", { count: pd.claims_pending }),
    pd.conflicts > 0 && t("progetti.passo5.report.conflicts", { count: pd.conflicts }),
    pd.open_change_requests > 0 && t("progetti.passo5.report.changes", { count: pd.open_change_requests }),
    pd.missing_roles.length > 0 && t("progetti.passo5.report.roles", { roles: pd.missing_roles.join(", ") }),
  ].filter(Boolean) as string[];
  return (
    <section className="flex flex-col gap-4" aria-label={t("lavoro.statusreports.periodData")} data-testid="report-dati-progetto">
      <SectionTitle aside={<span className="font-mono text-[11.5px] text-muted">{t("lavoro.statusreports.asOfPhase", { date: date(d.as_of), phase: d.phase })}</span>}>{t("lavoro.statusreports.data")}</SectionTitle>
      <dl className="m-0 grid grid-cols-2 gap-x-4 gap-y-3 2xl:grid-cols-4">
        <Fact
          label={t("progetti.passo5.report.workPackages")}
          value={`${pd.work_packages_done}/${pd.work_packages}`}
          note={t("progetti.passo5.report.milestones", { done: pd.milestones_done, total: pd.milestones })}
          tone={sc.tasks_late ? "danger" : undefined}
        />
        <Fact
          label={t("lavoro.statusreports.plannedEnd")}
          value={sc.end ? date(sc.end) : "—"}
          note={
            pd.baseline != null && pd.variance_days != null
              ? t("progetti.passo5.report.variance", { number: pd.baseline, sign: pd.variance_days > 0 ? "+" : pd.variance_days < 0 ? "−" : "", count: Math.abs(pd.variance_days) })
              : t("progetti.passo5.report.noBaseline")
          }
          tone={pd.variance_days != null && pd.variance_days > pd.tolerance_days ? "danger" : undefined}
        />
        {c.hidden ? (
          <Fact label={t("progetti.overview.budget")} value="—" note={t("progetti.passo5.report.budgetHidden")} />
        ) : (
          <>
            <Fact
              label={t("lavoro.statusreports.approvedBudget")}
              value={money(c.approved_cents, c.currency)}
              note={
                pd.budget_variance_pct != null
                  ? t("progetti.baseline.budgetVariance", { pct: pd.budget_variance_pct > 0 ? `+${pd.budget_variance_pct}` : `${pd.budget_variance_pct}` })
                  : c.forecast_cents != null
                    ? t("lavoro.statusreports.forecast", { value: money(c.forecast_cents, c.currency) })
                    : undefined
              }
              tone={pd.budget_variance_pct != null && pd.budget_variance_pct > pd.tolerance_budget_pct ? "danger" : undefined}
            />
            <Fact
              label={t("lavoro.statusreports.actual")}
              value={money(c.actual_cents, c.currency)}
              note={t("lavoro.statusreports.committed", { value: money(c.committed_cents, c.currency) })}
              tone={c.consumption_pct != null && c.consumption_pct > 100 ? "danger" : undefined}
            />
          </>
        )}
      </dl>
      <Progress actual={sc.actual_pct} planned={sc.planned_pct} />
      {counters.length > 0 && <p className="m-0 text-[12.5px]">{counters.join(" · ")}</p>}
      {rows.length > 0 && (
        <ul className="m-0 flex list-none flex-col p-0 text-[12.5px]">
          {rows.map((r) => (
            <li key={r.key} className="border-b border-hair py-1.5">{r.body}</li>
          ))}
        </ul>
      )}
    </section>
  );
}

function RequestDataPanel({ report }: { report: StatusReport }) {
  const t = useT();
  const d = report.data;
  const sc = d.schedule;
  const c = d.cost;
  const late = sc.end && sc.regulatory_deadline && sc.end > sc.regulatory_deadline;
  return (
    <section className="flex flex-col gap-4" aria-label={t("lavoro.statusreports.periodData")}>
      <SectionTitle aside={<span className="font-mono text-[11.5px] text-muted">{t("lavoro.statusreports.asOfPhase", { date: date(d.as_of), phase: d.phase })}</span>}>{t("lavoro.statusreports.data")}</SectionTitle>
      <dl className="m-0 grid grid-cols-2 gap-x-4 gap-y-3 2xl:grid-cols-4">
        <Fact label={t("lavoro.statusreports.impacts")} value={`${sc.tasks_done}/${sc.tasks}`} note={sc.tasks_late ? t("lavoro.statusreports.nLate", { n: sc.tasks_late }) : t("lavoro.statusreports.released")} tone={sc.tasks_late ? "danger" : undefined} />
        <Fact
          label={t("lavoro.statusreports.plannedEnd")}
          value={sc.end ? date(sc.end) : "—"}
          note={sc.regulatory_deadline ? t("lavoro.statusreports.regDeadline", { date: date(sc.regulatory_deadline) }) : sc.desired_date ? t("lavoro.statusreports.desired", { date: date(sc.desired_date) }) : undefined}
          tone={late ? "danger" : undefined}
        />
        <Fact label={t("lavoro.statusreports.approvedBudget")} value={money(c.approved_cents, c.currency)} note={c.forecast_cents != null ? t("lavoro.statusreports.forecast", { value: money(c.forecast_cents, c.currency) }) : undefined} />
        <Fact
          label={t("lavoro.statusreports.actual")}
          value={money(c.actual_cents, c.currency)}
          note={
            c.consumption_pct != null
              ? t("lavoro.statusreports.consumptionPct", { pct: pct(c.consumption_pct), value: money(c.committed_cents, c.currency) })
              : t("lavoro.statusreports.committed", { value: money(c.committed_cents, c.currency) })
          }
          tone={c.consumption_pct != null && c.consumption_pct > 100 ? "danger" : undefined}
        />
      </dl>
      <Progress actual={sc.actual_pct} planned={sc.planned_pct} />
      {(c.cpi != null || c.spi != null) && (
        <p className="m-0 text-[12.5px]">
          <Trans
            k="lavoro.statusreports.earnedValue"
            params={{ date: c.ev_as_of ? date(c.ev_as_of) : "—" }}
            slots={{ cpi: <span className="font-mono tabular">{ratio(c.cpi)}</span>, spi: <span className="font-mono tabular">{ratio(c.spi)}</span> }}
          />
        </p>
      )}
      {(sc.late.length > 0 || d.reviews_late.length > 0 || d.impacts_blocked.length > 0) && (
        <ul className="m-0 flex list-none flex-col p-0 text-[12.5px]">
          {sc.late.map((x) => (
            <li key={"l" + x.key} className="border-b border-hair py-1.5">
              <Link to="/e/$key" params={{ key: x.key }} className="font-mono text-accent no-underline hover:underline">{x.key}</Link> {x.application}
              {x.team && <span className="text-muted"> · {x.team}</span>} — <span className="text-danger">{t("lavoro.statusreports.expectedEnd", { date: date(x.end) })}</span>
            </li>
          ))}
          {d.reviews_late.map((r) => (
            <li key={"r" + r.key} className="border-b border-hair py-1.5">
              <Link to="/valutazioni/$key" params={{ key: r.key }} className="font-mono text-accent no-underline hover:underline">{r.key}</Link> {r.team}
              {" — "}
              <span className="text-danger">
                {t(r.mandatory ? "lavoro.statusreports.reviewLateMandatory" : "lavoro.statusreports.reviewLate", { n: r.late_days })}
              </span>
            </li>
          ))}
          {d.impacts_blocked.map((b, i) => (
            <li key={"b" + b.key + i} className="border-b border-hair py-1.5">
              <Link to="/e/$key" params={{ key: b.key }} className="font-mono text-accent no-underline hover:underline">{b.key}</Link> {b.application} —{" "}
              <span className="font-semibold">{t("lavoro.statusreports.blocked")}</span> {b.reason}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

// ---------------------------------------------------------------------
// Report inviato

function ReportBody({ report }: { report: StatusReport }) {
  const t = useT();
  return (
    <>
      <table className="w-full border-collapse text-[13px]">
        <thead>
          <tr className="border-b-2 border-rule text-left">
            <th className="label py-1.5 pr-3">{t("lavoro.statusreports.colScope")}</th>
            <th className="label py-1.5 pr-3">{t("lavoro.statusreports.colStatus")}</th>
            <th className="label py-1.5">{t("lavoro.statusreports.colNote")}</th>
          </tr>
        </thead>
        <tbody>
          {DIMENSIONS.map((d) => (
            <tr key={d.key} className="border-b border-hair align-top">
              <td className={`py-2 pr-3 ${d.key === "overall" ? "font-semibold" : ""}`}>{d.label}</td>
              <td className="py-2 pr-3"><Rag value={report[d.key]} /></td>
              <td className="py-2 text-muted">{report.sections.notes[d.key] ?? ""}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {SECTION_LABELS.map((s) =>
        report.sections[s.key].trim() ? (
          <section key={s.key} className="flex flex-col gap-1.5 break-inside-avoid">
            <h4 className="label m-0 border-b border-hair pb-1">{t(s.label)}</h4>
            <div className="max-w-3xl leading-relaxed">
              <Markdown text={report.sections[s.key]} />
            </div>
          </section>
        ) : null,
      )}
      <DataPanel report={report} />
    </>
  );
}

function ReportView({ report, refKey, onCompare }: { report: StatusReport; refKey: string; onCompare: () => void }) {
  const t = useT();
  const reportApi = useReports();
  const qc = useQueryClient();
  const [printing, setPrinting] = useState(false);
  const ack = useMutation({
    mutationFn: () => reportApi.acknowledge(report.id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["status-reports", refKey] }),
  });
  return (
    <article className="flex flex-col gap-5" aria-label={t("lavoro.statusreports.reportOf", { period: period(report) })}>
      <header className="flex flex-col gap-2 border-b-2 border-rule pb-3">
        <div className="flex flex-wrap items-baseline gap-x-4 gap-y-1">
          <h3 className="m-0 text-[20px] font-bold">{t("lavoro.statusreports.reportOf", { period: period(report) })}</h3>
          <Rag value={report.overall} />
        </div>
        <p className="m-0 text-[12.5px] text-muted">
          {report.submitted_at ? t("lavoro.statusreports.submittedOn", { date: date(report.submitted_at) }) : t("lavoro.statusreports.submitted")}
          {report.author ? ` ${t("lavoro.statusreports.by", { name: report.author })}` : ""}
          {report.acknowledged_at && ` ${t("lavoro.statusreports.acked", { name: report.acknowledged_by_name ?? "—", date: date(report.acknowledged_at) })}`}
        </p>
        <div className="flex flex-wrap gap-2">
          {report.can_acknowledge && (
            <button className="btn btn-primary" disabled={ack.isPending} onClick={() => ack.mutate()}>
              {t("lavoro.statusreports.acknowledge")}
            </button>
          )}
          <button className="btn" onClick={() => setPrinting(true)}>{t("lavoro.statusreports.print")}</button>
          <a className="btn no-underline" href={reportApi.markdownUrl(report.id)} download>
            {/* i18n-ok: nome del formato */}
            Markdown
          </a>
          {report.prev_id && (
            <button className="btn" onClick={onCompare}>
              {t("lavoro.statusreports.compare")}
            </button>
          )}
        </div>
        <ErrorNote error={ack.error} />
      </header>
      <ReportBody report={report} />
      {printing && <PrintReport report={report} onDone={() => setPrinting(false)} />}
    </article>
  );
}

/**
 * Stampa: il report finisce in #print-root, fuori dall'app; il CSS di stampa (print.css)
 * nasconde tutto il resto e usa sempre la versione chiara del tema.
 */
function PrintReport({ report, onDone }: { report: StatusReport; onDone: () => void }) {
  const t = useT();
  const [root] = useState(() => {
    const el = document.createElement("div");
    el.id = "print-root";
    el.dataset.skin = document.documentElement.dataset.skin ?? "atlante";
    el.dataset.font = document.documentElement.dataset.font ?? "atkinson";
    el.dataset.theme = "light";
    return el;
  });
  useEffect(() => {
    document.body.appendChild(root);
    const done = () => onDone();
    window.addEventListener("afterprint", done);
    const timer = window.setTimeout(() => window.print(), 50);
    return () => {
      window.clearTimeout(timer);
      window.removeEventListener("afterprint", done);
      root.remove();
    };
  }, [root, onDone]);
  return createPortal(
    <div className="print-page flex flex-col gap-5">
      <header className="flex flex-col gap-1 border-b-2 border-rule pb-2">
        <span className="font-mono text-[11px] text-muted">{t("lavoro.statusreports.printHeader", { key: report.element_key })}</span>
        <h1 className="m-0 text-[24px] leading-tight font-bold">{report.element_title}</h1>
        <p className="m-0 text-[12px]">
          {t("lavoro.statusreports.printPeriod", { period: period(report), date: report.submitted_at ? date(report.submitted_at) : "—" })}
          {report.author ? ` ${t("lavoro.statusreports.by", { name: report.author })}` : ""} {t("lavoro.statusreports.printOverall")} <Rag value={report.overall} />
        </p>
      </header>
      <ReportBody report={report} />
      <footer className="border-t border-hair pt-1 font-mono text-[10px] text-muted">
        {t("lavoro.statusreports.printFooter", { key: report.element_key, date: fmtDate(new Date()) })}
      </footer>
    </div>,
    root,
  );
}

function History({ items, current, onOpen }: { items: StatusReport[]; current?: string; onOpen: (id: string) => void }) {
  const t = useT();
  return (
    <section className="flex flex-col gap-2" aria-label={t("lavoro.statusreports.history")}>
      <SectionTitle aside={<span className="font-mono text-[11.5px] text-muted tabular">{items.length}</span>}>{t("lavoro.statusreports.history")}</SectionTitle>
      <ol className="m-0 flex list-none flex-col p-0">
        {items.map((r) => (
          <li key={r.id}>
            <button
              className={`grid w-full grid-cols-[minmax(0,1fr)_auto] items-center gap-3 border-b border-hair py-2 text-left hover:bg-sel/60 ${r.id === current ? "bg-sel" : ""}`}
              aria-current={r.id === current}
              onClick={() => onOpen(r.id)}
            >
              <span className="flex min-w-0 flex-col">
                <span className="font-semibold">{period(r)}</span>
                <span className="min-w-0 text-[12px] break-words text-muted">
                  {r.author ?? "—"} · {r.acknowledged_at ? t("lavoro.statusreports.ackedShort") : t("lavoro.statusreports.ackPending")}
                </span>
              </span>
              <span className="flex items-center gap-2">
                {DIMENSIONS.slice(1).map((d) => (
                  <span key={d.key} title={`${d.label}: ${RAG_LABEL[r[d.key]]}`} className="hidden sm:inline-flex">
                    <RagMark value={r[d.key]} size={8} />
                  </span>
                ))}
                <Rag value={r.overall} />
              </span>
            </button>
          </li>
        ))}
      </ol>
    </section>
  );
}

// ---------------------------------------------------------------------
// Confronto

const TREND: Record<string, { sign: string; cls: string; label: Key }> = {
  better: { sign: "▲", cls: "text-ok", label: "lavoro.statusreports.trendBetter" },
  worse: { sign: "▼", cls: "text-danger", label: "lavoro.statusreports.trendWorse" },
  same: { sign: "=", cls: "text-muted", label: "lavoro.statusreports.trendSame" },
};

function fmtChange(field: string, v: unknown, currency: string): ReactNode {
  if (v == null) return "—";
  if (typeof v === "string") return <Rag value={v as RAG} />;
  const n = Number(v);
  if (field.endsWith("_cents")) return money(n, currency);
  if (field.endsWith("_pct")) return pct(n);
  if (field === "cpi" || field === "spi") return ratio(n);
  return fmtNumber(n);
}

function CompareView({ id, onClose }: { id: string; onClose: () => void }) {
  const t = useT();
  const reportApi = useReports();
  const cmp = useQuery({ queryKey: ["status-report-compare", id], queryFn: () => reportApi.compare(id) });
  if (cmp.error) return <ErrorNote error={cmp.error} />;
  if (!cmp.data) return <Loading className="m-0 text-muted" />;
  const c: Comparison = cmp.data;
  const cur = c.to.data.cost.currency;
  return (
    <section className="flex flex-col gap-3" aria-label={t("lavoro.statusreports.comparison")}>
      <SectionTitle aside={<button className="text-[12px] text-accent underline" onClick={onClose}>{t("lavoro.statusreports.closeComparison")}</button>}>
        {t("lavoro.statusreports.comparisonRange", { from: period(c.from), to: period(c.to) })}
      </SectionTitle>
      <div className="overflow-x-auto">
        <table className="w-full min-w-[420px] border-collapse text-[13px]">
          <thead>
            <tr className="border-b-2 border-rule text-left">
              <th className="label py-1.5 pr-3">{t("lavoro.statusreports.colItem")}</th>
              <th className="label py-1.5 pr-3">{t("lavoro.statusreports.colBefore")}</th>
              <th className="label py-1.5 pr-3">{t("lavoro.statusreports.colNow")}</th>
              <th className="label py-1.5">{t("lavoro.statusreports.colTrend")}</th>
            </tr>
          </thead>
          <tbody>
            {c.changes.map((ch) => {
              const tn = TREND[ch.trend];
              return (
                <tr key={ch.field} className="border-b border-hair">
                  <td className="py-1.5 pr-3">{ch.label}</td>
                  <td className="py-1.5 pr-3 font-mono tabular">{fmtChange(ch.field, ch.from, cur)}</td>
                  <td className="py-1.5 pr-3 font-mono tabular">{fmtChange(ch.field, ch.to, cur)}</td>
                  <td className={`py-1.5 whitespace-nowrap ${tn.cls}`}>
                    <span aria-hidden="true">{tn.sign}</span> {t(tn.label)}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      <div className="grid grid-cols-1 gap-6 md:grid-cols-2">
        {[c.from, c.to].map((r) => (
          <div key={r.id} className="flex min-w-0 flex-col gap-2">
            <h4 className="label m-0 border-b border-hair pb-1">{t("lavoro.statusreports.summaryOf", { period: period(r) })}</h4>
            <div className="leading-relaxed">
              <Markdown text={r.sections.summary || "—"} />
            </div>
          </div>
        ))}
      </div>
    </section>
  );
}
