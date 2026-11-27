// «Panoramica» del programma (replica/progetti.md §4): i numeri di tutti i progetti insieme, la tabella dei progetti
// e la roadmap con una riga per progetto. Tutti i numeri li calcola il server con la funzione dei progetti.
import type { ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { projectKeys, projectsApi, type CrossDep, type ProgramProject, type ProgramView, type RoadmapMilestone } from "../../projectsApi";
import { XDepChip } from "./CrossDeps";
import { EmptyState } from "../../emptyState";
import { ErrorNote, Loading, SectionTitle, date, euro } from "../../ui";
import { useT } from "../../i18n/react";
import * as cal from "../../calendar";
import { ItemLink, ProgressBar, SignalMark, SignalName } from "./shared";

export function ProgramOverview({ refKey }: { refKey: string }) {
  const t = useT();
  const q = useQuery({ queryKey: projectKeys.program(refKey), queryFn: () => projectsApi.program(refKey) });
  if (q.error) return <ErrorNote error={q.error} />;
  if (!q.data) return <Loading className="m-0 text-muted" />;
  const { projects } = q.data;
  return (
    <div className="flex max-w-[1100px] flex-col gap-7">
      <Head view={q.data} />
      {projects.length === 0 ? (
        <EmptyState title={t("progetti.programmi.table.title")} text={t("progetti.programmi.empty")} />
      ) : (
        <>
          <Block title={t("progetti.programmi.table.title")}>
            <ProjectsTable projects={projects} />
          </Block>
          <Block title={t("progetti.programmi.roadmap.title")}>
            <Roadmap projects={projects} />
          </Block>
        </>
      )}
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

function Stat({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <dt className="label text-muted">{label}</dt>
      <dd className="m-0 flex flex-col gap-1">{children}</dd>
    </div>
  );
}

function Head({ view }: { view: ProgramView }) {
  const t = useT();
  const n = view.numbers;
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
      <dl className="m-0 grid grid-cols-1 gap-x-8 gap-y-5 sm:grid-cols-2 lg:grid-cols-3">
        <Stat label={t("progetti.overview.progress")}>
          <span className="tabular text-[24px] font-bold leading-none">{Math.round(n.progress)}%</span>
          <ProgressBar percent={n.progress} className="mt-1" />
          <span className="text-[12.5px] text-muted">{t("progetti.programmi.overview.progressSub", { count: view.projects.length })}</span>
        </Stat>
        <Stat label={t("progetti.programmi.overview.dates")}>
          {n.start || n.end ? (
            <span className="tabular text-[18px] font-semibold">{n.start && n.end ? `${date(n.start)} – ${date(n.end)}` : date(n.end ?? n.start)}</span>
          ) : (
            <span className="text-muted">{t("progetti.common.none")}</span>
          )}
          {n.baseline_end && <span className="tabular text-[12.5px] text-muted">{t("progetti.programmi.overview.baselineEnd", { date: date(n.baseline_end) })}</span>}
          {n.variance_days != null && n.variance_days !== 0 && (
            <span className={`tabular text-[12.5px] ${n.end_over_tolerance ? "font-semibold text-danger" : "text-muted"}`}>
              {t("progetti.baseline.variance", { sign: n.variance_days > 0 ? "+" : "−", count: Math.abs(n.variance_days) })}
            </span>
          )}
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
        <Stat label={t("progetti.programmi.overview.lateWp")}>
          {n.late.length > 0 ? <span className="tabular text-[18px] font-semibold text-danger">{n.late.length}</span> : <span className="text-muted">{t("progetti.programmi.onTime")}</span>}
          {n.blocked.length > 0 && (
            <span className="text-[12.5px] text-muted">
              {t("progetti.programmi.overview.blocked")}: <span className="tabular font-semibold">{n.blocked.length}</span>
            </span>
          )}
        </Stat>
        <Stat label={t("progetti.programmi.overview.claims")}>
          {n.claims_pending > 0 ? <span className="tabular text-[18px] font-semibold text-flag">{n.claims_pending}</span> : <span className="text-muted">{t("progetti.programmi.overview.claimsNone")}</span>}
        </Stat>
        <Stat label={t("progetti.programmi.overview.conflicts")}>
          {n.conflicts.length > 0 ? <span className="tabular text-[18px] font-semibold text-flag">{n.conflicts.length}</span> : <span className="text-muted">{t("progetti.programmi.overview.conflictsNone")}</span>}
        </Stat>
        {b && (
          <Stat label={t("progetti.overview.budget")}>
            <span className="tabular text-[18px] font-semibold">{euro(b.spent_cents)}</span>
            <span className="text-[12.5px] text-muted">
              {b.approved_cents != null ? t("progetti.overview.budgetOf", { approved: euro(b.approved_cents) }) : t("progetti.overview.budgetNoApproved")}
            </span>
            {b.approved_cents != null && b.approved_cents > 0 && <ProgressBar percent={(b.spent_cents / b.approved_cents) * 100} />}
          </Stat>
        )}
      </dl>
    </section>
  );
}

const th = "label border-b-2 border-rule px-2 py-2 text-left font-semibold text-muted";
const td = "border-b border-hair px-2 py-2 align-top";

function range(a: string | null, b: string | null, none: string): string {
  if (a && b) return `${date(a)} – ${date(b)}`;
  return a || b ? date(a ?? b) : none;
}

function Variance({ p }: { p: ProgramProject }) {
  const t = useT();
  if (p.variance_days == null) return <span className="text-muted">{t("progetti.common.none")}</span>;
  if (p.variance_days === 0) return <span className="text-muted">{t("progetti.programmi.table.onBaseline")}</span>;
  return (
    <span className={`tabular whitespace-nowrap ${p.variance_days > 0 ? "font-semibold text-danger" : ""}`}>
      {t("progetti.baseline.shift", { sign: p.variance_days > 0 ? "+" : "−", count: Math.abs(p.variance_days) })}
    </span>
  );
}

/** Budget del progetto nel programma (passo 5): speso sul previsto; «—» per chi non lo vede o se manca. */
function BudgetCell({ p }: { p: ProgramProject }) {
  const t = useT();
  const b = p.budget;
  if (!b) return <span className="text-muted">—</span>;
  return (
    <span className="tabular whitespace-nowrap">
      {euro(b.spent_cents)}
      <span className="text-muted"> {b.approved_cents != null ? t("progetti.passo5.budget.ofApproved", { amount: euro(b.approved_cents) }) : t("progetti.overview.budgetNoApproved")}</span>
    </span>
  );
}

function ProjectsTable({ projects }: { projects: ProgramProject[] }) {
  const t = useT();
  const none = t("progetti.common.none");
  const anyBudget = projects.some((p) => p.budget);
  return (
    <>
      <table className="hidden w-full border-collapse text-[13px] md:table" aria-label={t("progetti.programmi.table.title")}>
        <thead>
          <tr>
            <th className={th}>{t("progetti.programmi.table.project")}</th>
            <th className={th}>{t("progetti.programmi.table.pm")}</th>
            <th className={th}>{t("progetti.programmi.table.signal")}</th>
            <th className={th}>{t("progetti.programmi.table.progress")}</th>
            <th className={th}>{t("progetti.programmi.table.forecast")}</th>
            <th className={th}>{t("progetti.programmi.table.baseline")}</th>
            <th className={th}>{t("progetti.programmi.table.variance")}</th>
            <th className={th}>{t("progetti.programmi.table.late")}</th>
            {anyBudget && <th className={th}>{t("progetti.overview.budget")}</th>}
          </tr>
        </thead>
        <tbody>
          {projects.map((p) => (
            <tr key={p.id}>
              <td className={td}>
                <ItemLink itemKey={p.key} />
                <div className="font-semibold">{p.title}</div>
              </td>
              <td className={td}>{p.project_manager ?? <span className="text-muted">{t("progetti.programmi.table.noPm")}</span>}</td>
              <td className={td}>
                <SignalName value={p.signal} />
              </td>
              <td className={`${td} min-w-[120px]`}>
                <div className="flex items-center gap-2">
                  <ProgressBar percent={p.progress} />
                  <span className="tabular w-9 shrink-0 text-right">{Math.round(p.progress)}%</span>
                </div>
              </td>
              <td className={`${td} tabular whitespace-nowrap`}>{range(p.start, p.end, none)}</td>
              <td className={`${td} tabular whitespace-nowrap text-muted`}>{range(p.baseline_start, p.baseline_end, none)}</td>
              <td className={td}>
                <Variance p={p} />
              </td>
              <td className={td}>{p.late > 0 ? <span className="font-semibold text-danger">{t("progetti.programmi.lateCount", { count: p.late })}</span> : <span className="text-muted">{t("progetti.programmi.onTime")}</span>}</td>
              {anyBudget && (
                <td className={td}>
                  <BudgetCell p={p} />
                </td>
              )}
            </tr>
          ))}
        </tbody>
      </table>
      <ul className="m-0 flex list-none flex-col p-0 md:hidden" aria-label={t("progetti.programmi.table.title")}>
        {projects.map((p) => (
          <li key={p.id} className="flex flex-col gap-1.5 border-b border-hair py-3">
            <div className="flex items-baseline justify-between gap-3">
              <ItemLink itemKey={p.key} />
              <SignalName value={p.signal} className="text-[12.5px]" />
            </div>
            <span className="font-semibold">{p.title}</span>
            <div className="flex items-center gap-2">
              <ProgressBar percent={p.progress} />
              <span className="tabular w-9 shrink-0 text-right text-[12.5px]">{Math.round(p.progress)}%</span>
            </div>
            <span className="text-[12.5px] text-muted">
              {p.project_manager ?? t("progetti.programmi.table.noPm")} · <span className="tabular">{range(p.start, p.end, none)}</span>
            </span>
            {(p.baseline_start || p.baseline_end) && (
              <span className="text-[12.5px] text-muted">
                {t("progetti.baseline.phone", { dates: range(p.baseline_start, p.baseline_end, none) })} <Variance p={p} />
              </span>
            )}
            {p.late > 0 && <span className="text-[12.5px] font-semibold text-danger">{t("progetti.programmi.lateCount", { count: p.late })}</span>}
            {p.budget && (
              <span className="text-[12.5px]">
                {t("progetti.overview.budget")}: <BudgetCell p={p} />
              </span>
            )}
          </li>
        ))}
      </ul>
    </>
  );
}

// ---- Roadmap

type Mark = "planned" | "reached" | "late";

function markOf(m: RoadmapMilestone): Mark {
  if (m.state_category === "done") return "reached";
  if (m.days_late > 0) return "late";
  return "planned";
}

/** Rombo: vuoto = prevista, pieno con spunta = raggiunta, pieno con «!» = in ritardo; il doppio contorno = contrattuale. */
function Diamond({ mark, contractual, size = 16 }: { mark: Mark; contractual: boolean; size?: number }) {
  const fill = mark === "reached" ? "bg-ok border-ok text-bg" : mark === "late" ? "bg-danger border-danger text-bg" : "bg-bg border-fg text-fg";
  return (
    <span
      aria-hidden="true"
      className={`relative inline-flex shrink-0 items-center justify-center ${contractual ? "outline outline-2 outline-offset-1 outline-fg" : ""}`}
      style={{ width: size, height: size, transform: "rotate(45deg)" }}
    >
      <span className={`absolute inset-0 border-2 ${fill}`} />
      <span className="relative text-[10px] font-bold leading-none" style={{ transform: "rotate(-45deg)" }}>
        {mark === "reached" ? "✓" : mark === "late" ? "!" : ""}
      </span>
    </span>
  );
}

function monthStart(t: number): number {
  const d = new Date(t);
  return Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), 1);
}
function nextMonth(t: number): number {
  const d = new Date(t);
  return Date.UTC(d.getUTCFullYear(), d.getUTCMonth() + 1, 1);
}

function milestoneLabel(m: RoadmapMilestone, t: ReturnType<typeof useT>): string {
  if (m.restricted) return t("progetti.programmi.roadmap.restricted");
  const state = markOf(m) === "reached" ? t("progetti.programmi.roadmap.stateReached") : markOf(m) === "late" ? t("progetti.programmi.roadmap.stateLate", { days: m.days_late }) : t("progetti.programmi.roadmap.statePlanned");
  const base = m.date ? t("progetti.programmi.roadmap.milestone", { title: m.title, date: date(m.date) }) : t("progetti.programmi.roadmap.milestoneNoDate", { title: m.title });
  return `${base} · ${state}${m.contractual ? ` · ${t("progetti.programmi.roadmap.stateContractual")}` : ""}`;
}

/** Roadmap dei progetti con le milestone e (passo 6) le dipendenze da milestone di altri progetti. La usano il
 * programma e la roadmap di portafoglio. */
export function Roadmap({ projects }: { projects: ProgramProject[] }) {
  const t = useT();
  const none = t("progetti.common.none");
  // Intervallo: dal primo giorno del mese del primo punto all'inizio del mese dopo l'ultimo.
  const pts: number[] = [];
  for (const p of projects) {
    for (const d of [p.start, p.end, p.baseline_start, p.baseline_end]) if (d) pts.push(cal.parse(d));
    for (const m of p.milestones) for (const d of [m.date, m.baseline_date]) if (d) pts.push(cal.parse(d));
    for (const x of p.external ?? []) if (x.need_by) pts.push(cal.parse(x.need_by));
  }
  const todayDay = cal.today();
  const hasDates = pts.length > 0;
  const from = hasDates ? monthStart(Math.min(...pts)) : 0;
  const to = hasDates ? nextMonth(Math.max(...pts)) : 1;
  const total = Math.max(1, cal.diffDays(from, to));
  const pct = (d: number) => `${(cal.diffDays(from, d) / total) * 100}%`;
  const widthPct = (a: number, b: number) => `${Math.max(0.6, ((cal.diffDays(a, b) + 1) / total) * 100)}%`;
  // Mesi: una tacca al primo giorno; con molti mesi si etichetta uno ogni «step».
  const months: number[] = [];
  for (let m = from; m < to; m = nextMonth(m)) months.push(m);
  const step = months.length > 30 ? 6 : months.length > 14 ? 3 : months.length > 8 ? 2 : 1;
  const showToday = hasDates && todayDay >= from && todayDay <= to;

  return (
    <div className="flex flex-col gap-3">
      {hasDates && (
        <div className="hidden md:block" role="group" aria-label={t("progetti.programmi.roadmap.aria")}>
          <div className="grid grid-cols-[200px_1fr] gap-x-3">
            <div className="label self-end pb-1 text-muted">{t("progetti.programmi.roadmap.axis")}</div>
            <div className="relative h-7 border-b border-rule" aria-hidden="true">
              {months.map((m, i) => (
                <span key={m} className="absolute top-0 h-full border-l border-hair pl-1 text-[11.5px] text-muted" style={{ left: pct(m) }}>
                  {i % step === 0 && (
                    <>
                      {cal.monthName(m)}
                      {(i === 0 || new Date(m).getUTCMonth() === 0) && <span className="tabular ml-1">{new Date(m).getUTCFullYear()}</span>}
                    </>
                  )}
                </span>
              ))}
              {showToday && (
                <span className="absolute -top-0.5 text-[10.5px] font-semibold text-accent" style={{ left: pct(todayDay), transform: "translateX(-50%)" }}>
                  {t("progetti.programmi.roadmap.today")}
                </span>
              )}
            </div>
            {projects.map((p) => {
              const s = p.start ?? p.end;
              const e = p.end ?? p.start;
              const bs = p.baseline_start ?? p.baseline_end;
              const be = p.baseline_end ?? p.baseline_start;
              return (
                <RoadmapRow key={p.id} p={p}>
                  <div className="relative h-11 border-b border-hair">
                    {months.map((m) => (
                      <span key={m} aria-hidden="true" className="absolute inset-y-0 border-l border-hair/60" style={{ left: pct(m) }} />
                    ))}
                    {showToday && <span aria-hidden="true" className="absolute inset-y-0 border-l-2 border-accent" style={{ left: pct(todayDay) }} />}
                    {s && e && (
                      <span
                        role="img"
                        aria-label={`${p.key} ${range(p.start, p.end, none)}`}
                        title={range(p.start, p.end, none)}
                        className={`absolute top-2 block h-3 border ${p.signal === "red" ? "border-danger bg-danger/70" : "border-accent bg-accent/70"}`}
                        style={{ left: pct(cal.parse(s)), width: widthPct(cal.parse(s), cal.parse(e)) }}
                      />
                    )}
                    {bs && be && (
                      <span
                        role="img"
                        aria-label={t("progetti.programmi.roadmap.baselineBar", { from: date(bs), to: date(be) })}
                        title={t("progetti.programmi.roadmap.baselineBar", { from: date(bs), to: date(be) })}
                        className="absolute top-[22px] block h-1.5 border border-muted bg-muted/25"
                        style={{ left: pct(cal.parse(bs)), width: widthPct(cal.parse(bs), cal.parse(be)) }}
                      />
                    )}
                    {(p.external ?? []).map((d) =>
                      d.need_by ? (
                        <span
                          key={d.id}
                          role="img"
                          aria-label={depLabel(d, t)}
                          title={depLabel(d, t)}
                          data-testid="roadmap-dipendenza"
                          className={`absolute top-0 z-10 text-[11px] leading-none font-bold ${d.late_days > 0 ? "text-danger" : "text-muted"}`}
                          style={{ left: pct(cal.parse(d.need_by)), transform: "translateX(-50%)" }}
                        >
                          ⇠
                        </span>
                      ) : null,
                    )}
                    {p.milestones.map((m) =>
                      m.date ? (
                        <span
                          key={m.id}
                          role="img"
                          aria-label={milestoneLabel(m, t)}
                          title={milestoneLabel(m, t)}
                          className="absolute top-[26px] z-10"
                          style={{ left: pct(cal.parse(m.date)), transform: "translateX(-50%)" }}
                        >
                          <Diamond mark={markOf(m)} contractual={m.contractual} size={13} />
                        </span>
                      ) : null,
                    )}
                  </div>
                </RoadmapRow>
              );
            })}
          </div>
          <ul className="m-0 mt-3 flex list-none flex-wrap gap-x-5 gap-y-1.5 p-0 text-[12.5px] text-muted" aria-label={t("progetti.programmi.roadmap.legend")}>
            <li className="flex items-center gap-2"><Diamond mark="planned" contractual={false} size={11} />{t("progetti.programmi.roadmap.legendPlanned")}</li>
            <li className="flex items-center gap-2"><Diamond mark="reached" contractual={false} size={11} />{t("progetti.programmi.roadmap.legendReached")}</li>
            <li className="flex items-center gap-2"><Diamond mark="late" contractual={false} size={11} />{t("progetti.programmi.roadmap.legendLate")}</li>
            <li className="flex items-center gap-2"><Diamond mark="planned" contractual size={11} />{t("progetti.programmi.roadmap.legendContractual")}</li>
            <li className="flex items-center gap-2"><span aria-hidden="true" className="block h-1.5 w-6 border border-muted bg-muted/25" />{t("progetti.programmi.roadmap.legendBaseline")}</li>
            <li className="flex items-center gap-2"><span aria-hidden="true" className="font-bold">⇠</span>{t("progetti.passo6.dep.legend")}</li>
          </ul>
        </div>
      )}
      {!hasDates && <p className="m-0 hidden text-[13px] text-muted md:block">{t("progetti.programmi.roadmap.noneInRange")}</p>}
      <ul className="m-0 flex list-none flex-col p-0 md:hidden" aria-label={t("progetti.programmi.roadmap.title")}>
        {projects.map((p) => (
          <li key={p.id} className="flex flex-col gap-1 border-b border-hair py-3">
            <div className="flex items-baseline justify-between gap-3">
              <ItemLink itemKey={p.key} />
              <span className="tabular text-[12.5px]">{range(p.start, p.end, t("progetti.programmi.roadmap.noDates"))}</span>
            </div>
            <span className="font-semibold">{p.title}</span>
            {(p.baseline_start || p.baseline_end) && <span className="tabular text-[12.5px] text-muted">{t("progetti.baseline.phone", { dates: range(p.baseline_start, p.baseline_end, none) })}</span>}
            {(p.external ?? []).map((d) => (
              <span key={d.id} className={`text-[12.5px] ${d.late_days > 0 ? "font-semibold text-danger" : "text-muted"}`}>
                ⇠ {depLabel(d, t)}
              </span>
            ))}
            {p.milestones.length === 0 ? (
              <span className="text-[12.5px] text-muted">{t("progetti.programmi.roadmap.noMilestones")}</span>
            ) : (
              <ul className="m-0 flex list-none flex-col gap-1 p-0">
                {p.milestones.map((m) => (
                  <li key={m.id} className="flex items-center gap-2.5 text-[12.5px]">
                    <Diamond mark={markOf(m)} contractual={m.contractual} size={11} />
                    <span>{milestoneLabel(m, t)}</span>
                  </li>
                ))}
              </ul>
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}

/** «Ha bisogno di PRJ-3 IDP pronto entro il 31/03 (prevista il 08/04, in ritardo di 6 giorni)». */
function depLabel(d: CrossDep, t: ReturnType<typeof useT>): string {
  const ms = d.milestone_project ? `${d.milestone_project.key} ${d.milestone?.title ?? t("progetti.restricted")}` : t("progetti.restricted");
  const base = t("progetti.passo6.dep.roadmap", { milestone: ms, need: d.need_by ? date(d.need_by) : "—", date: d.milestone_date ? date(d.milestone_date) : "—" });
  return d.late_days > 0 ? `${base} · ${t("progetti.passo6.dep.late", { count: d.late_days })}` : base;
}

function RoadmapRow({ p, children }: { p: ProgramProject; children: ReactNode }) {
  return (
    <>
      <div className="flex min-w-0 flex-col justify-center border-b border-hair py-1">
        <span className="flex items-center gap-2">
          <ItemLink itemKey={p.key} />
          <XDepChip deps={p.external ?? []} />
        </span>
        <span className="truncate text-[12.5px] font-semibold" title={p.title}>
          {p.title}
        </span>
      </div>
      {children}
    </>
  );
}
