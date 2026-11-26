// Cruscotto di portafoglio (demand.md §11, D12; piano, passo 13). Numeri grandi, regole
// sottili, un accento: ogni numero porta alla lista delle richieste con la query che lo
// riproduce. Filtri per unità, driver e periodo (nell'indirizzo, così si condivide);
// pannelli scelti e ordinati da ciascuno.
import { Select } from "../select";
import { useEffect, useState, type ReactNode } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { PageHeader } from "../shell";
import { DRIVERS, ErrorNote, SectionTitle, date } from "../ui";
import { reportApi, type Dashboard as Dash, type DashFilter, type Num, type PanelPref } from "../reportApi";
import { ConsumptionBar, money, pct } from "./finance-ui";
import { Rag, RagMark } from "./StatusReports";
import { useT } from "../i18n/react";
import { Trans } from "../i18n/Trans";
import { fmtNumber } from "../i18n/format";
import type { Params } from "../i18n/core";
import { MyBudget } from "./budget/MyBudget";
import { crumbs } from "../crumbs";
import { ModuleChip, type UiModule } from "../moduleIcon";
import { phaseTint } from "../tinte";

export function Dashboard({ filter }: { filter: DashFilter }) {
  const t = useT();
  const navigate = useNavigate();
  const dash = useQuery({ queryKey: ["dashboard", filter], queryFn: () => reportApi.dashboard(filter), placeholderData: (p) => p });
  const layout = useQuery({ queryKey: ["dashboard-layout"], queryFn: reportApi.layout });
  const [editing, setEditing] = useState(false);
  const setFilter = (f: DashFilter) => navigate({ to: "/cruscotto", search: { ...filter, ...f } });
  const d = dash.data;
  const panels = (layout.data?.panels ?? []).filter((p) => p.visible);
  return (
    <>
      <PageHeader
        path={crumbs("demand", "dashboard")}
        title={t("lavoro.dashboard.title")}
        actions={
          <button className="btn" aria-expanded={editing} onClick={() => setEditing((e) => !e)}>
            {editing ? t("lavoro.dashboard.close") : t("lavoro.dashboard.panels")}
          </button>
        }
      />
      <div className="flex flex-col gap-6 px-4 pb-10 md:px-7">
        <Filters filter={filter} data={d} onChange={setFilter} />
        {editing && layout.data && <PanelEditor panels={layout.data.panels} onDone={() => setEditing(false)} />}
        <ErrorNote error={dash.error ?? layout.error} />
        {!d && dash.isPending && <p className="m-0 text-muted">{t("lavoro.dashboard.calculating")}</p>}
        {d && (
          <>
            <Headline d={d} />
            <MyBudget />
            <div className={`grid grid-cols-1 gap-x-10 gap-y-9 lg:grid-cols-2 ${dash.isFetching ? "opacity-70" : ""}`}>
              {panels.map((p) => (
                <Panel key={p.key} k={p.key} d={d} />
              ))}
            </div>
            {panels.length === 0 && <p className="m-0 text-muted">{t("lavoro.dashboard.noPanels")}</p>}
            <p className="m-0 font-mono text-[11px] text-muted">
              <Trans k="lavoro.dashboard.asOf" params={{ date: date(d.as_of) }} slots={{ query: <span className="text-fg">{d.base_query}</span> }} />
            </p>
          </>
        )}
      </div>
    </>
  );
}

// ---------------------------------------------------------------------

/** Numero che porta alla lista filtrata con la query corrispondente. */
function Count({ n, size = "lg", tone, label }: { n: Num; size?: "xl" | "lg" | "md"; tone?: "flag" | "danger"; label?: string }) {
  const t = useT();
  const cls = size === "xl" ? "text-[44px] leading-none" : size === "lg" ? "text-[30px] leading-none" : "text-[17px] leading-tight";
  const color = n.count > 0 && tone === "flag" ? "text-flag" : n.count > 0 && tone === "danger" ? "text-danger" : "text-fg";
  return (
    <Link
      to="/richieste"
      search={{ q: n.query }}
      title={t("lavoro.dashboard.openList", { query: n.query })}
      aria-label={label ? t("lavoro.dashboard.countAria", { label, count: fmtNumber(n.count) }) : undefined}
      className={`font-mono font-semibold tabular no-underline hover:text-accent hover:underline ${cls} ${color}`}
    >
      {fmtNumber(n.count)}
    </Link>
  );
}

function Stat({ label, n, note, tone, size = "lg" }: { label: string; n: Num; note?: ReactNode; tone?: "flag" | "danger"; size?: "xl" | "lg" }) {
  return (
    <div className="flex min-w-0 flex-col gap-1.5">
      <span className="label text-muted">{label}</span>
      <Count n={n} size={size} tone={tone} label={label} />
      {note && <span className="text-[12px] text-muted">{note}</span>}
    </div>
  );
}

function Kpi({ module, label, n, tone, note, i }: { module: UiModule; label: string; n: Num; tone?: "flag" | "danger"; note?: ReactNode; i: number }) {
  return (
    <div
      className={`entra relative flex min-w-0 flex-col gap-1.5 border border-hair px-3.5 pb-3 pt-3 m-${module}`}
      style={{ background: "linear-gradient(color-mix(in srgb, var(--mc) 6%, var(--bg)), var(--bg))", animationDelay: `${i * 60}ms` }}
    >
      <span aria-hidden="true" className="absolute -left-px -right-px -top-px h-1 bg-mc" />
      <span className="label flex items-center gap-2 text-muted">
        <ModuleChip module={module} size="sm" />
        {label}
      </span>
      <Count n={n} size="xl" tone={tone} label={label} />
      {note && <span className="text-[12px] text-muted">{note}</span>}
    </div>
  );
}

function Headline({ d }: { d: Dash }) {
  const t = useT();
  return (
    <section aria-label={t("lavoro.dashboard.summary")} className="grid grid-cols-1 gap-3 min-[480px]:grid-cols-2 md:grid-cols-4">
      <Kpi i={0} module="demand" label={t("lavoro.dashboard.inScope")} n={d.total} />
      <Kpi i={1} module="demand" label={t("lavoro.dashboard.open")} n={d.open} />
      <Kpi i={2} module="grc" label={t("lavoro.dashboard.lateDeadline")} n={d.late_deadline} tone="flag" />
      <Kpi i={3} module="work" label={t("lavoro.dashboard.reportsLate")} n={d.rag.overdue} tone="danger" note={t("lavoro.dashboard.reportsLateNote")} />
    </section>
  );
}

function Filters({ filter, data, onChange }: { filter: DashFilter; data?: Dash; onChange: (f: DashFilter) => void }) {
  const t = useT();
  const any = !!(filter.unit || filter.driver || filter.from || filter.to);
  return (
    <form className="flex flex-wrap items-end gap-3" aria-label={t("lavoro.dashboard.filters")} onSubmit={(e) => e.preventDefault()}>
      <label className="flex min-w-[150px] flex-col gap-1">
        <span className="label text-muted">{t("lavoro.dashboard.unit")}</span>
        <Select className="field py-1.5" value={filter.unit ?? ""} onChange={(e) => onChange({ unit: e.target.value || undefined })}>
          <option value="">{t("lavoro.dashboard.all")}</option>
          {(data?.units ?? []).map((u) => (
            <option key={u.key} value={u.label}>{u.label}</option>
          ))}
        </Select>
      </label>
      <label className="flex min-w-[150px] flex-col gap-1">
        <span className="label text-muted">{t("lavoro.dashboard.driver")}</span>
        <Select className="field py-1.5" value={filter.driver ?? ""} onChange={(e) => onChange({ driver: e.target.value || undefined })}>
          <option value="">{t("lavoro.dashboard.allMasc")}</option>
          {Object.entries(DRIVERS).map(([k, v]) => (
            <option key={k} value={k}>{v}</option>
          ))}
        </Select>
      </label>
      <label className="flex flex-col gap-1">
        <span className="label text-muted">{t("lavoro.dashboard.openedFrom")}</span>
        <input type="date" className="field py-1.5" value={filter.from ?? ""} onChange={(e) => onChange({ from: e.target.value || undefined })} />
      </label>
      <label className="flex flex-col gap-1">
        <span className="label text-muted">{t("lavoro.dashboard.openedTo")}</span>
        <input type="date" className="field py-1.5" value={filter.to ?? ""} onChange={(e) => onChange({ to: e.target.value || undefined })} />
      </label>
      {any && (
        <button type="button" className="btn" onClick={() => onChange({ unit: undefined, driver: undefined, from: undefined, to: undefined })}>
          {t("lavoro.dashboard.reset")}
        </button>
      )}
    </form>
  );
}

// ---------------------------------------------------------------------
// Pannelli

function Panel({ k, d }: { k: string; d: Dash }) {
  const t = useT();
  switch (k) {
    case "fasi":
      return <PhasesPanel d={d} />;
    case "ritardi":
      return (
        <Section title={t("lavoro.dashboard.lateRequests")}>
          <div className="grid grid-cols-1 gap-5 sm:grid-cols-3">
            <Stat label={t("lavoro.dashboard.lateDesired")} n={d.late_desired} tone="danger" note={t("lavoro.dashboard.lateDesiredNote")} />
            <Stat label={t("lavoro.dashboard.lateDeadline")} n={d.late_deadline} tone="flag" note={t("lavoro.dashboard.lateDeadlineNote")} />
            <Stat label={t("lavoro.dashboard.deadlineSoon")} n={d.deadline_soon} tone="flag" note={t("lavoro.dashboard.deadlineSoonNote")} />
          </div>
        </Section>
      );
    case "normativi":
      return (
        <Section title={t("lavoro.dashboard.regulatory")} aside={<span className="border border-flag px-1 text-[10.5px] font-semibold text-flag">{t("lavoro.dashboard.mandatoryTag")}</span>}>
          <div className="flex flex-wrap gap-x-10 gap-y-5">
            <Stat label={t("lavoro.dashboard.mandatoryOpen")} n={d.mandatory} tone="flag" />
            {d.regulatory.map((r) => (
              <div key={r.label} className="flex flex-col gap-1.5">
                <span className="self-start border border-flag px-1 text-[11px] font-semibold text-flag">{r.label}</span>
                <span className="flex items-baseline gap-1">
                  <Count n={r} tone="flag" label={r.label} />
                  {r.more && <span className="font-mono text-muted">+</span>}
                </span>
              </div>
            ))}
          </div>
          {d.regulatory.length === 0 && <p className="m-0 text-[12.5px] text-muted">{t("lavoro.dashboard.noIndicators")}</p>}
        </Section>
      );
    case "budget":
      return <BudgetPanel d={d} />;
    case "semafori":
      return <RagPanel d={d} />;
    case "valutazioni":
      return (
        <Section title={t("lavoro.dashboard.reviewsLate")} aside={<Count n={d.reviews_late_total} size="md" tone="danger" label={t("lavoro.dashboard.reviewsLateShort")} />}>
          <Note>{t("lavoro.dashboard.reviewsNote")}</Note>
          {d.reviews_late.length === 0 ? (
            <p className="m-0 text-[12.5px] text-muted">{t("lavoro.dashboard.reviewsNone")}</p>
          ) : (
            <Bars items={d.reviews_late.map((r) => ({ key: r.team, label: r.team, n: r }))} tone="danger" module="grc" />
          )}
        </Section>
      );
    case "carico":
      return <LoadPanel d={d} />;
    case "driver":
      return (
        <Section title={t("lavoro.dashboard.byDriver")}>
          <Bars items={d.by_driver.map((s) => ({ key: s.key, label: s.label, n: s }))} module="arch" />
        </Section>
      );
    case "unita":
      return (
        <Section title={t("lavoro.dashboard.byUnit")}>
          <Bars items={d.by_unit.map((s) => ({ key: s.key || "-", label: s.label, n: s }))} module="platform" />
        </Section>
      );
  }
  return null;
}

function Section({ title, aside, children, wide }: { title: string; aside?: ReactNode; children: ReactNode; wide?: boolean }) {
  return (
    <section className={`flex min-w-0 flex-col gap-3 ${wide ? "lg:col-span-2" : ""}`} aria-label={title}>
      <SectionTitle aside={aside}>{title}</SectionTitle>
      {children}
    </section>
  );
}

function Note({ children }: { children: ReactNode }) {
  return <p className="m-0 text-[12px] text-muted">{children}</p>;
}

/** Barre orizzontali sottili con il numero cliccabile a destra. */
function Bars({ items, tone, module = "demand" }: { items: { key: string; label: string; n: Num }[]; tone?: "danger"; module?: UiModule }) {
  const t = useT();
  const max = Math.max(1, ...items.map((i) => i.n.count));
  if (items.length === 0) return <p className="m-0 text-[12.5px] text-muted">{t("lavoro.dashboard.nothing")}</p>;
  return (
    <ul className="m-0 flex list-none flex-col p-0">
      {items.map((i) => (
        <li key={i.key} className="grid grid-cols-[minmax(0,40%)_minmax(0,1fr)_48px] items-center gap-3 border-b border-hair py-1.5">
          <span className="min-w-0 break-words" title={i.label}>{i.label}</span>
          <span className={`m-${module} h-3 bg-[color-mix(in_srgb,var(--mc)_22%,transparent)]`}>
            <span className={`cresce block h-full ${tone === "danger" ? "bg-danger" : "bg-mc"}`} style={{ width: `${(100 * i.n.count) / max}%` }} />
          </span>
          <span className="text-right">
            <Count n={i.n} size="md" label={i.label} />
          </span>
        </li>
      ))}
    </ul>
  );
}

function days(v: number | null, t: (key: "lavoro.dashboard.daysAbbr", params?: Params) => string): string {
  return v == null ? "—" : t("lavoro.dashboard.daysAbbr", { value: fmtNumber(v, { maximumFractionDigits: v < 10 ? 1 : 0 }) });
}

function PhasesPanel({ d }: { d: Dash }) {
  const t = useT();
  const main = d.phases.filter((p) => !p.off_path);
  const off = d.phases.filter((p) => p.off_path);
  const max = Math.max(1, ...d.phases.map((p) => p.count));
  const row = (p: Dash["phases"][number], i?: number, idx = -1) => (
    <tr key={p.name} className="border-b border-hair">
      <td className="py-1.5 pr-3">
        {i != null && <span className="mr-1.5 font-mono text-[11px] text-muted tabular">{String(i + 1).padStart(2, "0")}</span>}
        <span className={`chip-fase m-${phaseTint(p.name, idx, p.category)} ${p.off_path ? "text-muted" : ""}`}>{p.name}</span>
      </td>
      <td className="w-[38%] py-1.5 pr-3">
        <span className={`m-${phaseTint(p.name, idx, p.category)} block h-4 bg-[color-mix(in_srgb,var(--mc)_22%,transparent)]`}>
          <span className="cresce block h-full bg-mc" style={{ width: `${(100 * p.count) / max}%`, animationDelay: `${(i ?? 0) * 50}ms` }} />
        </span>
      </td>
      <td className="py-1.5 pr-3 text-right"><Count n={p} size="md" label={p.name} /></td>
      <td className="py-1.5 pr-3 text-right font-mono tabular" title={p.stays ? t("lavoro.dashboard.stays", { count: p.stays }) : t("lavoro.dashboard.noStays")}>{days(p.avg_days, t)}</td>
      <td className="hidden py-1.5 text-right font-mono text-muted tabular sm:table-cell">{days(p.wait_days, t)}</td>
    </tr>
  );
  return (
    <Section title={t("lavoro.dashboard.byPhase")} wide>
      <div className="overflow-x-auto">
        <table className="w-full min-w-[460px] border-collapse text-[13px]">
          <thead>
            <tr className="border-b-2 border-rule text-left">
              <th className="label py-1.5 pr-3">{t("lavoro.dashboard.phase")}</th>
              <th className="label py-1.5 pr-3"><span className="sr-only">{t("lavoro.dashboard.bar")}</span></th>
              <th className="label py-1.5 pr-3 text-right">{t("lavoro.dashboard.requests")}</th>
              <th className="label py-1.5 pr-3 text-right" title={t("lavoro.dashboard.crossingTip")}>{t("lavoro.dashboard.crossing")}</th>
              <th className="label hidden py-1.5 text-right sm:table-cell" title={t("lavoro.dashboard.inPhaseSinceTip")}>{t("lavoro.dashboard.inPhaseSince")}</th>
            </tr>
          </thead>
          <tbody>
            {main.map((p, i) => row(p, i, i))}
            {off.map((p) => row(p))}
          </tbody>
        </table>
      </div>
      <Note>{t("lavoro.dashboard.phaseNote")}</Note>
    </Section>
  );
}

function BudgetPanel({ d }: { d: Dash }) {
  const t = useT();
  const f = d.finance;
  const cur = f.currency;
  const link = (q: string, v: ReactNode, label: string) => (
    <Link to="/richieste" search={{ q }} title={t("lavoro.dashboard.openList", { query: q })}
      className="font-mono text-[24px] leading-none whitespace-nowrap font-semibold tabular text-fg no-underline hover:text-accent hover:underline">
      {/* Il nome accessibile include l'importo visibile (prima c'era solo l'etichetta). */}
      <span className="sr-only">{label}: </span>
      {v}
      <span className="sr-only">{t("lavoro.dashboard.openListSr")}</span>
    </Link>
  );
  return (
    <Section
      title={t("lavoro.dashboard.budgetCosts")}
      aside={f.can_see_details ? <Link to="/finanza" className="text-[12px] text-accent underline">{t("lavoro.dashboard.finance")}</Link> : undefined}
    >
      <div className="grid grid-cols-2 gap-5 sm:grid-cols-3">
        <div className="flex flex-col gap-1.5">
          <span className="label text-muted">{t("lavoro.dashboard.approvedBudget")}</span>
          {link(f.budget_query, money(f.approved_cents, cur), t("lavoro.dashboard.approvedBudget"))}
          <span className="text-[12px] text-muted">{t("lavoro.dashboard.withBudget", { count: f.with_budget })}</span>
        </div>
        <div className="flex flex-col gap-1.5">
          <span className="label text-muted">{t("lavoro.dashboard.actual")}</span>
          {link(f.cost_query, money(f.actual_cents, cur), t("lavoro.dashboard.actual"))}
          <span className="text-[12px] text-muted">{f.consumption_pct != null ? t("lavoro.dashboard.ofBudget", { pct: pct(f.consumption_pct) }) : "—"}</span>
        </div>
        <div className="flex flex-col gap-1.5">
          <span className="label text-muted">{t("lavoro.dashboard.committed")}</span>
          {link(f.cost_query, money(f.committed_cents, cur), t("lavoro.dashboard.committed"))}
          <span className="text-[12px] text-muted">{t("lavoro.dashboard.ordersContracts")}</span>
        </div>
      </div>
      {f.approved_cents > 0 && <ConsumptionBar actual={f.actual_cents} committed={f.committed_cents} budget={f.approved_cents} thresholds={[]} />}
      {f.can_see_details ? (
        <>
          {f.by_request && f.by_request.length > 0 && (
            <div className="overflow-x-auto">
              <table className="w-full min-w-[420px] border-collapse text-[12.5px]">
                <thead>
                  <tr className="border-b-2 border-rule text-left">
                    <th className="label py-1.5 pr-3">{t("lavoro.dashboard.request")}</th>
                    <th className="label py-1.5 pr-3 text-right">{t("lavoro.dashboard.budget")}</th>
                    <th className="label py-1.5 pr-3 text-right">{t("lavoro.dashboard.actual")}</th>
                    <th className="label py-1.5 text-right">{t("lavoro.dashboard.consumption")}</th>
                  </tr>
                </thead>
                <tbody>
                  {f.by_request.map((r) => (
                    <tr key={r.key} className="border-b border-hair">
                      <td className="py-1.5 pr-3 break-words">
                        <Link to="/e/$key" params={{ key: r.key }} className="font-mono text-accent no-underline hover:underline">{r.key}</Link> {r.title}
                      </td>
                      <td className="py-1.5 pr-3 text-right font-mono tabular">{money(r.approved_cents, cur)}</td>
                      <td className="py-1.5 pr-3 text-right font-mono tabular">{money(r.actual_cents, cur)}</td>
                      <td className={`py-1.5 text-right font-mono tabular ${r.consumption_pct != null && r.consumption_pct > 100 ? "text-danger" : ""}`}>{pct(r.consumption_pct)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          {f.by_category && f.by_category.length > 0 && (
            <p className="m-0 text-[12px] text-muted">
              {t("lavoro.dashboard.byCategory", { list: f.by_category.map((c) => `${c.category} ${money(c.actual_cents, cur)}`).join(" · ") })}
            </p>
          )}
        </>
      ) : (
        <Note>{t("lavoro.dashboard.detailRestricted")}</Note>
      )}
    </Section>
  );
}

function RagPanel({ d }: { d: Dash }) {
  const t = useT();
  const r = d.rag;
  return (
    <Section title={t("lavoro.dashboard.ragLatest")}>
      <div className="flex flex-wrap gap-x-8 gap-y-4">
        {(
          [
            ["red", r.red],
            ["amber", r.amber],
            ["green", r.green],
          ] as const
        ).map(([k, n]) => (
          <div key={k} className="flex flex-col gap-1.5">
            <span className="label text-muted">
              <Rag value={k} />
            </span>
            <Count n={n} label={t("lavoro.dashboard.ragLight", { color: k })} />
          </div>
        ))}
        <div className="flex flex-col gap-1.5">
          <span className="label text-muted">{t("lavoro.dashboard.reportsLate")}</span>
          <Count n={r.overdue} tone="danger" label={t("lavoro.dashboard.reportsLate")} />
        </div>
      </div>
      {r.items.length > 0 && (
        <ul className="m-0 flex list-none flex-col p-0">
          {r.items.slice(0, 10).map((it) => (
            <li key={it.key} className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 border-b border-hair py-1.5 text-[12.5px]">
              <span className="min-w-0 break-words">
                <Link to="/e/$key" params={{ key: it.key }} search={{ scheda: "stato" }} className="font-mono text-accent no-underline hover:underline">
                  {it.key}
                </Link>{" "}
                {it.title}
                {it.overdue && <span className="font-semibold text-danger">{t("lavoro.dashboard.reportLate")}</span>}
              </span>
              <span className="flex items-center gap-1.5">
                {(["schedule", "cost", "scope", "risk"] as const).map((k) => (
                  <span key={k} className="hidden sm:inline-flex" title={k}>
                    <RagMark value={it[k]} size={8} />
                  </span>
                ))}
                <Rag value={it.overall} label={false} />
                <span className="font-mono text-[11px] text-muted tabular">{date(it.period_end)}</span>
              </span>
            </li>
          ))}
        </ul>
      )}
      {r.items.length === 0 && <Note>{t("lavoro.dashboard.noReports")}</Note>}
    </Section>
  );
}

/** Carico dei team: per settimana, colonne sottili; oltre la capacità in --danger. */
function LoadPanel({ d }: { d: Dash }) {
  const tr = useT();
  return (
    <Section title={tr("lavoro.dashboard.teamLoad")} aside={<span className="font-mono text-[11px] text-muted">{date(d.load_from)} – {date(d.load_to)}</span>}>
      <Note>{tr("lavoro.dashboard.teamLoadNote")}</Note>
      {d.team_load.length === 0 ? (
        <p className="m-0 text-[12.5px] text-muted">{tr("lavoro.dashboard.noTeams")}</p>
      ) : (
        <ul className="m-0 flex list-none flex-col p-0">
          {d.team_load.map((t) => (
            <li key={t.team} className="grid grid-cols-[minmax(0,1fr)_auto_52px] items-center gap-3 border-b border-hair py-1.5">
              <Link to="/richieste" search={{ q: t.query }} className="min-w-0 break-words text-fg no-underline hover:text-accent hover:underline" title={tr("lavoro.dashboard.openImpacts", { query: t.query })}>
                {t.team}
              </Link>
              <svg width={8 * 9} height={22} role="img" aria-label={tr("lavoro.dashboard.weekLoad", { list: t.weeks.map((w) => `${w}%`).join(", ") })}>
                <line x1={0} x2={8 * 9} y1={22 - 22 / 1.5} y2={22 - 22 / 1.5} className="stroke-rule" strokeWidth={1} strokeDasharray="2 2" />
                {t.weeks.map((w, i) => {
                  const h = Math.max(1, (Math.min(w, 150) / 150) * 22);
                  return <rect key={i} x={i * 9} y={22 - h} width={7} height={h} className={w > 100 ? "fill-danger" : "fill-m-work"} />;
                })}
              </svg>
              <span className={`text-right font-mono tabular ${t.peak_pct > 100 ? "font-semibold text-danger" : ""}`} title={tr("lavoro.dashboard.weeklyPeak")}>
                {pct(t.peak_pct)}
              </span>
            </li>
          ))}
        </ul>
      )}
    </Section>
  );
}

// ---------------------------------------------------------------------
// Scelta e ordine dei pannelli (salvati per persona)

function PanelEditor({ panels, onDone }: { panels: PanelPref[]; onDone: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const [list, setList] = useState(panels);
  useEffect(() => setList(panels), [panels]);
  const save = useMutation({
    mutationFn: () => reportApi.saveLayout(list),
    onSuccess: (r) => {
      qc.setQueryData(["dashboard-layout"], r);
      onDone();
    },
  });
  const move = (i: number, by: number) => {
    const j = i + by;
    if (j < 0 || j >= list.length) return;
    const next = [...list];
    [next[i], next[j]] = [next[j], next[i]];
    setList(next);
  };
  return (
    <section className="flex max-w-xl flex-col gap-2 border-l-2 border-accent pl-4" aria-label={t("lavoro.dashboard.editorLabel")}>
      <SectionTitle>{t("lavoro.dashboard.panels")}</SectionTitle>
      <ol className="m-0 flex list-none flex-col p-0">
        {list.map((p, i) => (
          <li key={p.key} className="flex items-center gap-2 border-b border-hair py-1.5">
            <label className="flex flex-1 items-center gap-2">
              <input type="checkbox" className="size-4 accent-[var(--accent)]" checked={p.visible}
                onChange={(e) => setList(list.map((x) => (x.key === p.key ? { ...x, visible: e.target.checked } : x)))} />
              <span className={p.visible ? "" : "text-muted"}>{p.label}</span>
            </label>
            <button className="btn px-2 py-0.5" aria-label={t("lavoro.dashboard.moveUp", { label: p.label })} disabled={i === 0} onClick={() => move(i, -1)}>↑</button>
            <button className="btn px-2 py-0.5" aria-label={t("lavoro.dashboard.moveDown", { label: p.label })} disabled={i === list.length - 1} onClick={() => move(i, 1)}>↓</button>
          </li>
        ))}
      </ol>
      <div className="flex gap-2">
        <button className="btn btn-primary" disabled={save.isPending} onClick={() => save.mutate()}>{t("lavoro.dashboard.save")}</button>
        <button className="btn" onClick={onDone}>{t("lavoro.dashboard.cancel")}</button>
      </div>
      <ErrorNote error={save.error} />
    </section>
  );
}
