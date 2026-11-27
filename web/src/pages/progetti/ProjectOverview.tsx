// «Panoramica» del PM (replica/progetti.md §3 e §6): semaforo con i motivi, avanzamento, prossima milestone,
// baseline, budget e margine; sotto, ciò che chiede attenzione. Tutti i numeri li calcola il server.
import { lazy, Suspense, useState, type ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Select } from "../../select";
import type { TabKey } from "../ElementPage";
import { projectKeys, projectsApi, type ItemRef, type Overview } from "../../projectsApi";
import { EmptyState } from "../../emptyState";
import { ErrorNote, Loading, SectionTitle, date, euro } from "../../ui";
import { useT } from "../../i18n/react";
import { ItemLink, ProgressBar, SignalMark, useClaimName, useConflictText } from "./shared";
import { useMe } from "../../shell";
import { reportsOf, type RAG } from "../../reportApi";
import { RagMark } from "../StatusReports";
import { kindLabel, statusLabel } from "../finance-ui";

// «Attinge da» (docs/sviluppo/budget-collegamento.md) per chi vede la finanza: lo stesso componente della richiesta.
const BudgetSourceList = lazy(() => import("../budget/RequestBudgetSource").then((m) => ({ default: m.RequestBudgetSource })));
const projectReports = reportsOf("project");

export function ProjectOverview({ refKey, onTab }: { refKey: string; onTab: (t: TabKey) => void }) {
  const t = useT();
  const conflictText = useConflictText();
  const claimName = useClaimName();
  const q = useQuery({ queryKey: projectKeys.overview(refKey), queryFn: () => projectsApi.overview(refKey) });
  if (q.error) return <ErrorNote error={q.error} />;
  if (!q.data) return <Loading className="m-0 text-muted" />;
  const { numbers: n, phases } = q.data;
  const conflicts = n.conflicts ?? [];
  const empty = n.work_packages === 0 && n.milestones === 0;
  return (
    <div className="flex max-w-[1000px] flex-col gap-7">
      {empty ? (
        <EmptyState
          title={t("progetti.overview.emptyTitle")}
          text={t("progetti.overview.emptyText")}
          action={
            <button type="button" className="btn btn-primary" onClick={() => onTab("piano")}>
              {t("progetti.overview.openPlan")}
            </button>
          }
        />
      ) : (
        <>
          <Head data={q.data} refKey={refKey} />
          {n.budget_visible && <BudgetBlock data={q.data} refKey={refKey} />}
          <div className="grid gap-7 md:grid-cols-2">
            <Block title={t("progetti.overview.late")}>
              {n.late.length === 0 ? <None text={t("progetti.overview.lateNone")} /> : <ul className="m-0 list-none p-0">{n.late.map((i) => <LateRow key={i.id} item={i} />)}</ul>}
            </Block>
            <Block title={t("progetti.overview.blocked")}>
              {n.blocked.length === 0 ? (
                n.external_late.length === 0 && <None text={t("progetti.overview.blockedNone")} />
              ) : (
                <ul className="m-0 list-none p-0">
                  {n.blocked.map((b) => (
                    <li key={`${b.item.id}-${b.waits_on.id}`} className="flex flex-col border-b border-hair py-1 md:flex-row md:flex-wrap md:items-center md:gap-x-2">
                      <ItemLink itemKey={b.item.key} title={b.item.title} />
                      <span className="text-muted">
                        {b.reason === "waiting" ? t("progetti.overview.waiting") : t("progetti.overview.violated")} <span className="font-mono text-[11.5px]">({t(`progetti.common.dep.${b.dep_kind}`)})</span>
                      </span>
                      <ItemLink itemKey={b.waits_on.key} title={b.waits_on.title} />
                    </li>
                  ))}
                </ul>
              )}
              {n.external_late.length > 0 && (
                <ul className="m-0 list-none p-0" aria-label={t("progetti.passo6.dep.lateTitle")} data-testid="panoramica-dipendenze">
                  {n.external_late.map((d) => (
                    <li key={d.id} className="flex flex-col border-b border-hair py-1">
                      <span>
                        {d.item ? `${d.item.key} ${d.item.title}` : t("progetti.passo6.dep.wholeProject")} {t("progetti.passo6.dep.needs")}{" "}
                        {d.milestone_project ? `${d.milestone_project.key} · ${d.milestone?.title ?? t("progetti.restricted")}` : t("progetti.restricted")}
                      </span>
                      <span className="tabular text-[12.5px] font-semibold text-danger">
                        {t("progetti.passo6.dep.dates", { need: d.need_by ? date(d.need_by) : "—", date: d.milestone_date ? date(d.milestone_date) : "—" })} ·{" "}
                        {t("progetti.passo6.dep.late", { count: d.late_days })}
                      </span>
                    </li>
                  ))}
                </ul>
              )}
            </Block>
            <Block title={t("progetti.overview.changes")}>
              {n.open_change_requests === 0 ? <None text={t("progetti.overview.changesNone")} /> : <p className="m-0 font-semibold">{t("progetti.overview.changesCount", { count: n.open_change_requests })}</p>}
              {(n.scope_added.length > 0 || n.scope_removed.length > 0) && (
                <ul className="m-0 list-none p-0" aria-label={t("progetti.passo5.scope.title")} data-testid="panoramica-perimetro">
                  {n.scope_added.map((i) => (
                    <li key={"a" + i.id} className="flex flex-wrap items-center justify-between gap-x-3 border-b border-hair py-1">
                      <ItemLink itemKey={i.key} title={i.title} />
                      <span className="text-[12.5px] font-semibold">{t("progetti.passo5.scope.added")}</span>
                    </li>
                  ))}
                  {n.scope_removed.map((i) => (
                    <li key={"r" + i.id} className="flex flex-wrap items-center justify-between gap-x-3 border-b border-hair py-1">
                      <ItemLink itemKey={i.key} title={i.title} />
                      <span className="text-[12.5px] font-semibold">{t("progetti.passo5.scope.removed")}</span>
                    </li>
                  ))}
                </ul>
              )}
              {n.over_tolerance.length > 0 && (
                <ul className="m-0 list-none p-0" aria-label={t("progetti.baseline.overTolerance")}>
                  {n.over_tolerance.map((i) => (
                    <li key={i.id} className="flex flex-wrap items-center justify-between gap-x-3 border-b border-hair py-1">
                      <ItemLink itemKey={i.key} title={i.title} />
                      <span className="tabular text-[12.5px] font-semibold text-danger">{t("progetti.baseline.shift", { sign: "+", count: i.days_late })}</span>
                    </li>
                  ))}
                </ul>
              )}
              {(n.open_change_requests > 0 || n.over_tolerance.length > 0 || n.end_over_tolerance || n.scope_added.length > 0 || n.scope_removed.length > 0) && (
                <button type="button" className="btn btn-quiet self-start px-0 underline" onClick={() => onTab("piano")}>
                  {t("progetti.baseline.openPlan")}
                </button>
              )}
            </Block>
            <Block title={t("progetti.fornitori.conflict.title")}>
              {conflicts.length === 0 ? (
                <None text={t("progetti.fornitori.conflict.none")} />
              ) : (
                <ul className="m-0 list-none p-0" data-testid="panoramica-conflitti">
                  {conflicts.map((c) => (
                    <li key={c.item_id + c.application_id} className="border-b border-hair py-1.5">
                      <span aria-hidden="true" className="text-flag">▲ </span>
                      {conflictText(c)}
                    </li>
                  ))}
                </ul>
              )}
            </Block>
            <Block title={t("progetti.fornitori.pending.title")}>
              {n.claims_pending === 0 ? (
                <None text={t("progetti.fornitori.pending.none")} />
              ) : (
                <p className="m-0 font-semibold" data-testid="panoramica-sal">
                  {t("progetti.fornitori.pending.count", { name: n.claims_pending === 1 ? claimName.one : claimName.many, count: n.claims_pending })}
                </p>
              )}
              {n.claims_pending > 0 && (
                <button type="button" className="btn mt-1 self-start touch-44" onClick={() => onTab("wp")}>
                  {t("progetti.overview.openWp")}
                </button>
              )}
            </Block>
            <Block title={t("progetti.overview.risks")}>
              {n.risks.length === 0 ? <None text={t("progetti.overview.risksNone")} /> : <ul className="m-0 list-none p-0">{n.risks.map((r) => <li key={r.id} className="border-b border-hair py-1"><ItemLink itemKey={r.key} title={r.title} /></li>)}</ul>}
            </Block>
            <Block title={t("progetti.overview.roles")}>
              {n.missing_roles.length === 0 ? (
                <None text={t("progetti.overview.rolesNone")} />
              ) : (
                <ul className="m-0 list-none p-0">
                  {n.missing_roles.map((r) => (
                    <li key={r.key} className="border-b border-hair py-1.5 font-semibold text-flag">
                      {r.name}
                    </li>
                  ))}
                </ul>
              )}
            </Block>
            <ReportsBlock refKey={refKey} onTab={onTab} />
            <Block title={t("progetti.overview.byMode")}>
              {n.by_mode.length === 0 ? (
                <None text={t("progetti.overview.byModeNone")} />
              ) : (
                <ul className="m-0 flex list-none flex-col gap-2.5 p-0">
                  {n.by_mode.map((m) => (
                    <li key={m.mode} className="flex flex-col gap-1">
                      <div className="flex items-baseline justify-between gap-3">
                        <span className="font-semibold">{t(`progetti.common.mode.${m.mode}`)}</span>
                        <span className="tabular text-[12.5px] text-muted">
                          {t("progetti.overview.byModeCount", { count: m.count })} · {Math.round(m.progress)}%
                        </span>
                      </div>
                      <ProgressBar percent={m.progress} />
                    </li>
                  ))}
                </ul>
              )}
              <button type="button" className="btn mt-1 self-start touch-44" onClick={() => onTab("wp")}>
                {t("progetti.overview.openWp")}
              </button>
            </Block>
          </div>
        </>
      )}
      <Block title={t("progetti.overview.phases")}>
        {phases.length === 0 ? (
          <None text={t("progetti.overview.phasesNone")} />
        ) : (
          <ul className="m-0 list-none p-0">
            {phases.map((p) => (
              <li key={p.id} className="grid grid-cols-[1fr_auto] items-center gap-x-4 gap-y-1 border-b border-hair py-2 md:grid-cols-[minmax(0,2fr)_minmax(0,3fr)_auto]">
                <span className="font-semibold">{p.name}</span>
                <span className="tabular col-start-2 row-start-1 text-right text-[12.5px] text-muted md:col-start-3">
                  {p.start && p.end ? `${date(p.start)} – ${date(p.end)}` : t("progetti.common.none")}
                </span>
                <div className="col-span-2 flex items-center gap-3 md:col-span-1 md:col-start-2 md:row-start-1">
                  <ProgressBar percent={p.progress} />
                  <span className="tabular w-10 shrink-0 text-right text-[12.5px]">{Math.round(p.progress)}%</span>
                </div>
                {p.gate_enabled && <span className="col-span-2 text-[12.5px] text-muted md:col-span-3">{t(`progetti.common.gate.${p.gate_status}`)}</span>}
              </li>
            ))}
          </ul>
        )}
      </Block>
    </div>
  );
}

/** Riga «Programma»: nome (porta alla pagina del programma) e, per chi pianifica, il selettore. */
function ProgramRow({ data, refKey }: { data: Overview; refKey: string }) {
  const t = useT();
  const qc = useQueryClient();
  const settings = useQuery({ queryKey: ["project-settings"], queryFn: () => projectsApi.settings(), staleTime: 60_000, retry: false });
  const program = data.project.program;
  const canPick = data.can_edit && (settings.data?.programs_enabled ?? false);
  const programs = useQuery({ queryKey: projectKeys.programs, queryFn: () => projectsApi.programs(), enabled: canPick });
  const [err, setErr] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  if (!program && !canPick) return null;
  const set = async (id: string) => {
    setBusy(true);
    setErr(null);
    try {
      await projectsApi.setProgram(refKey, id);
      await Promise.all([qc.invalidateQueries({ queryKey: ["project-overview"] }), qc.invalidateQueries({ queryKey: projectKeys.programs }), qc.invalidateQueries({ queryKey: ["program"] }), qc.invalidateQueries({ queryKey: ["element"] })]);
    } catch (e) {
      setErr(e);
    } finally {
      setBusy(false);
    }
  };
  const items = programs.data?.items ?? [];
  return (
    <div className="flex flex-col gap-1" data-testid="riga-programma">
      <span className="label text-muted">{t("progetti.fornitori.program.label")}</span>
      <div className="flex flex-wrap items-center gap-3">
        {program ? (
          <Link to="/e/$key" params={{ key: program.key }} className="min-h-[44px] content-center text-accent no-underline hover:underline md:min-h-0">{program.key} · {program.title}</Link>
        ) : (
          <span className="text-muted">{t("progetti.fornitori.program.none")}</span>
        )}
        {canPick && (
          <Select className="field w-full sm:w-72" aria-label={t("progetti.fornitori.program.pick")} disabled={busy} value={program?.id ?? ""} onChange={(e) => void set(e.target.value)}>
            <option value="">{t("progetti.fornitori.program.none")}</option>
            {program && !items.some((p) => p.id === program.id) && <option value={program.id}>{program.key} · {program.title}</option>}
            {items.map((p) => <option key={p.id} value={p.id}>{p.key} · {p.title}</option>)}
          </Select>
        )}
      </div>
      <ErrorNote error={err} />
    </div>
  );
}

function Head({ data, refKey }: { data: Overview; refKey: string }) {
  const t = useT();
  const n = data.numbers;
  const nm = n.next_milestone;
  const b = n.budget;
  return (
    <section aria-label={t("progetti.overview.signal")} className="flex flex-col gap-5">
      <div className="flex flex-col gap-1.5 border-l-2 border-rule pl-3" data-testid="semaforo">
        <span className="label text-muted">{t("progetti.overview.signal")}</span>
        <span className="flex items-center gap-2 text-[18px] font-semibold">
          <SignalMark value={n.signal} size={14} />
          {t(`progetti.common.signal.${n.signal}`)}
        </span>
        {n.reasons.length === 0 ? (
          <span className="text-[13px] text-muted">{t("progetti.overview.noReasons")}</span>
        ) : (
          <ul className="m-0 list-none p-0 text-[13px]">
            {n.reasons.map((r) => (
              <li key={r}>{t(`progetti.overview.reason.${r}`)}</li>
            ))}
          </ul>
        )}
      </div>
      <ProgramRow data={data} refKey={refKey} />
      <dl className="m-0 grid grid-cols-1 gap-x-8 gap-y-5 sm:grid-cols-2 lg:grid-cols-3">
        <Stat label={t("progetti.overview.progress")}>
          <span className="tabular text-[24px] font-bold leading-none">{Math.round(n.progress)}%</span>
          <ProgressBar percent={n.progress} className="mt-1" />
          <span className="text-[12.5px] text-muted">{t("progetti.overview.progressSub", { done: n.work_packages_done, total: n.work_packages })}</span>
        </Stat>
        <Stat label={t("progetti.overview.nextMilestone")}>
          {nm ? (
            <>
              <ItemLink itemKey={nm.key} title={nm.title} />
              <span className="tabular text-[13px]">
                {date(nm.date)}
                {nm.days_late > 0 && <span className="ml-2 font-semibold text-danger">{t("progetti.common.daysLate", { count: nm.days_late })}</span>}
              </span>
            </>
          ) : (
            <span className="text-muted">{t("progetti.overview.noNextMilestone")}</span>
          )}
        </Stat>
        <Stat label={t("progetti.overview.baseline")}>
          {n.baseline_end ? (
            <>
              {n.variance_days != null && (
                <span className={`tabular text-[18px] font-semibold ${n.end_over_tolerance ? "text-danger" : ""}`}>
                  {t("progetti.baseline.variance", { sign: n.variance_days > 0 ? "+" : n.variance_days < 0 ? "−" : "", count: Math.abs(n.variance_days) })}
                  {n.variance_pct != null && n.variance_pct !== 0 && <span className="ml-2 text-[13px] font-normal text-muted">{t("progetti.baseline.variancePct", { pct: n.variance_pct > 0 ? `+${n.variance_pct}` : `${n.variance_pct}` })}</span>}
                </span>
              )}
              <span className="tabular text-[12.5px] text-muted">{t("progetti.baseline.endOf", { number: n.baseline ?? 0, date: date(n.baseline_end) })}</span>
              {n.budget_variance_pct != null && (
                <span className={`tabular text-[12.5px] ${n.budget_over_tolerance ? "font-semibold text-danger" : "text-muted"}`}>
                  {t("progetti.baseline.budgetVariance", { pct: n.budget_variance_pct > 0 ? `+${n.budget_variance_pct}` : `${n.budget_variance_pct}` })}
                </span>
              )}
            </>
          ) : (
            <span className="text-muted">{t("progetti.overview.baselineNone")}</span>
          )}
          {n.end && (
            <span className="tabular text-[12.5px] text-muted">
              {t("progetti.overview.forecast")}: {date(n.end)}
            </span>
          )}
        </Stat>
        <Stat label={t("progetti.overview.budget")}>
          {b ? (
            <>
              <span className="tabular text-[18px] font-semibold">{euro(b.spent_cents)}</span>
              <span className="text-[12.5px] text-muted">
                {b.approved_cents != null ? t("progetti.overview.budgetOf", { approved: euro(b.approved_cents) }) : t("progetti.overview.budgetNoApproved")}
              </span>
              {b.approved_cents != null && b.approved_cents > 0 && <ProgressBar percent={(b.spent_cents / b.approved_cents) * 100} />}
              {b.committed_cents > 0 && <span className="tabular text-[12.5px] text-muted">{t("progetti.overview.budgetCommitted", { amount: euro(b.committed_cents) })}</span>}
            </>
          ) : (
            <span className="text-muted">{t("progetti.overview.budgetNone")}</span>
          )}
        </Stat>
        <Stat label={t("progetti.overview.margin")}>
          {n.margin_days != null ? (
            <>
              <span className={`tabular font-semibold ${n.margin_days < 0 ? "text-danger" : ""}`}>
                {n.margin_days < 0 ? t("progetti.overview.marginMinus", { count: Math.abs(n.margin_days) }) : t("progetti.overview.marginPlus", { count: n.margin_days })}
              </span>
              {n.target && n.target_kind && <span className="tabular text-[12.5px] text-muted">{t(`progetti.overview.target.${n.target_kind}`, { date: date(n.target) })}</span>}
            </>
          ) : (
            <span className="text-muted">{t("progetti.overview.marginNone")}</span>
          )}
        </Stat>
      </dl>
    </section>
  );
}

function Stat({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <dt className="label text-muted">{label}</dt>
      <dd className="m-0 flex flex-col gap-1">{children}</dd>
    </div>
  );
}

function Block({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="flex min-w-0 flex-col gap-2" aria-label={title}>
      <SectionTitle>{title}</SectionTitle>
      {children}
    </section>
  );
}

function None({ text }: { text: string }) {
  return <p className="m-0 text-[13px] text-muted">{text}</p>;
}

function LateRow({ item }: { item: ItemRef }) {
  const t = useT();
  return (
    <li className="flex flex-wrap items-center justify-between gap-x-3 border-b border-hair py-1">
      <ItemLink itemKey={item.key} title={item.title} />
      <span className="tabular text-[12.5px] font-semibold text-danger">{t("progetti.common.daysLate", { count: item.days_late })}</span>
    </li>
  );
}

/**
 * «Budget e costi» del progetto (passo 5): i budget del progetto e delle sue richieste, dalla Finanza.
 * Previsto (approvato), in lavorazione, impegnato, speso e previsione a finire; per chi vede la finanza
 * il campo «Attinge da» di ogni budget e il collegamento per crearne uno nuovo in Finanza.
 */
function BudgetBlock({ data, refKey }: { data: Overview; refKey: string }) {
  const t = useT();
  const perms = useMe().data?.active_tenant?.permissions ?? [];
  const finance = perms.includes("*") || perms.includes("finance.read");
  const b = data.numbers.budget;
  const list = b?.budgets ?? [];
  return (
    <section className="flex min-w-0 flex-col gap-3" aria-label={t("progetti.passo5.budget.title")} data-testid="panoramica-budget">
      <SectionTitle
        aside={
          finance ? (
            <Link to="/finanza" search={{ richiesta: refKey }} className="text-[12px] text-accent underline">
              {t("progetti.passo5.budget.openFinance")}
            </Link>
          ) : undefined
        }
      >
        {t("progetti.passo5.budget.title")}
      </SectionTitle>
      {!b ? (
        <None text={t("progetti.passo5.budget.none")} />
      ) : (
        <>
          <dl className="m-0 grid grid-cols-2 gap-x-6 gap-y-3 sm:grid-cols-4">
            <Stat label={t("progetti.passo5.budget.approved")}>
              <span className="tabular text-[16px] font-semibold">{b.approved_cents != null ? euro(b.approved_cents) : "—"}</span>
              {b.pending_cents != null && <span className="tabular text-[12px] text-muted">{t("progetti.passo5.budget.pending", { amount: euro(b.pending_cents) })}</span>}
            </Stat>
            <Stat label={t("progetti.passo5.budget.committed")}>
              <span className="tabular text-[16px] font-semibold">{euro(b.committed_cents)}</span>
            </Stat>
            <Stat label={t("progetti.passo5.budget.spent")}>
              <span className={`tabular text-[16px] font-semibold ${b.consumption_pct != null && b.consumption_pct > 100 ? "text-danger" : ""}`}>{euro(b.spent_cents)}</span>
              {b.consumption_pct != null && <span className="tabular text-[12px] text-muted">{t("progetti.passo5.budget.consumption", { pct: Math.round(b.consumption_pct) })}</span>}
            </Stat>
            <Stat label={t("progetti.passo5.budget.forecast")}>
              <span className="tabular text-[16px] font-semibold">{b.forecast_cents != null ? euro(b.forecast_cents) : "—"}</span>
            </Stat>
          </dl>
          {list.length > 0 && (
            <ul className="m-0 list-none p-0 text-[13px]" aria-label={t("progetti.passo5.budget.versions")}>
              {list.map((v) => (
                <li key={v.id} className="flex flex-wrap items-baseline gap-x-3 border-b border-hair py-1">
                  <ItemLink itemKey={v.element_key} />
                  <span>{t("progetti.passo5.budget.version", { year: v.fiscal_year, version: v.version, kind: kindLabel(v.kind) })}</span>
                  <span className="text-muted">{statusLabel(v.status)}</span>
                  <span className="tabular ml-auto">{euro(v.total_cents)}</span>
                </li>
              ))}
            </ul>
          )}
        </>
      )}
      {finance && (
        <Suspense fallback={null}>
          <BudgetSourceList elementKey={refKey} />
        </Suspense>
      )}
    </section>
  );
}

/** Ultimi status report del progetto (passo 5): lo storico si apre dalla scheda «Stato». */
function ReportsBlock({ refKey, onTab }: { refKey: string; onTab: (t: TabKey) => void }) {
  const t = useT();
  const q = useQuery({ queryKey: ["status-reports", refKey], queryFn: () => projectReports.list(refKey), retry: false });
  const items = q.data?.items ?? [];
  const sent = items.filter((r) => r.status === "submitted").slice(0, 3);
  const draft = items.some((r) => r.status === "draft");
  const sc = q.data?.schedule;
  return (
    <Block title={t("progetti.passo5.report.title")}>
      <div data-testid="panoramica-report" className="flex flex-col gap-2">
        {sent.length === 0 ? (
          <None text={t("progetti.passo5.report.none")} />
        ) : (
          <ul className="m-0 list-none p-0">
            {sent.map((r) => (
              <li key={r.id} className="flex flex-wrap items-center gap-x-3 border-b border-hair py-1.5 text-[13px]">
                <RagMark value={r.overall as RAG} />
                <span className="tabular">{date(r.period_end)}</span>
                <span className="min-w-0 flex-1 truncate text-muted">{r.sections.summary}</span>
                <span className="text-[12px] text-muted">{r.acknowledged_at ? t("progetti.passo5.report.seen") : t("progetti.passo5.report.unseen")}</span>
              </li>
            ))}
          </ul>
        )}
        {draft && <p className="m-0 text-[12.5px] font-semibold">{t("progetti.passo5.report.draft")}</p>}
        {sc?.overdue && <p className="m-0 text-[12.5px] font-semibold text-danger">{t("lavoro.statusreports.reportLate")}</p>}
        {sc?.next_due && !sc.overdue && <p className="m-0 text-[12.5px] text-muted">{t("progetti.passo5.report.next", { date: date(sc.next_due) })}</p>}
        <ErrorNote error={q.error} />
        <button type="button" className="btn mt-1 self-start touch-44" onClick={() => onTab("stato")}>
          {q.data?.can_edit ? t("progetti.passo5.report.write") : t("progetti.passo5.report.open")}
        </button>
      </div>
    </Block>
  );
}
