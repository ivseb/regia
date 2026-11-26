// Schede «Work package» e «Milestone» del progetto, come tabelle (sul telefono, schede), con le date
// della baseline in vigore e lo scostamento calcolato dal server.
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { projectKeys, projectsApi, type DeliveryMode, type PlanItem, type ProjectPlan } from "../../projectsApi";
import { EmptyState } from "../../emptyState";
import { Select } from "../../select";
import { ErrorNote, Loading, date } from "../../ui";
import { useT } from "../../i18n/react";
import { ItemLink, ProgressBar } from "./shared";

function usePlan(refKey: string) {
  return useQuery({ queryKey: projectKeys.plan(refKey), queryFn: () => projectsApi.plan(refKey) });
}

function range(i: { start: string | null; end: string | null }, none: string): string {
  if (i.start && i.end && i.start !== i.end) return `${date(i.start)} – ${date(i.end)}`;
  return i.end || i.start ? date(i.end ?? i.start) : none;
}

/** Date della baseline con lo scostamento della fine («+3 g»); «nuova» se la voce è nata dopo. */
function BaselineCell({ i, hasBaseline }: { i: PlanItem; hasBaseline: boolean }) {
  const t = useT();
  const none = t("progetti.common.none");
  if (!hasBaseline) return <span className="text-muted">{none}</span>;
  if (!i.in_baseline) return <span className="text-muted">{t("progetti.baseline.newItem")}</span>;
  const v = i.variance_days;
  return (
    <span className="tabular whitespace-nowrap">
      {range({ start: i.baseline_start, end: i.baseline_end }, none)}
      {v != null && v !== 0 && <span className={`ml-2 font-semibold ${v > 0 ? "text-danger" : "text-muted"}`}>{t("progetti.baseline.shift", { sign: v > 0 ? "+" : "−", count: Math.abs(v) })}</span>}
    </span>
  );
}

const th = "label border-b-2 border-rule px-2 py-2 text-left font-semibold text-muted";
const td = "border-b border-hair px-2 py-2 align-top";

export function WorkPackagesTab({ refKey }: { refKey: string }) {
  const t = useT();
  const q = usePlan(refKey);
  const [mode, setMode] = useState<"" | DeliveryMode>("");
  if (q.error) return <ErrorNote error={q.error} />;
  if (!q.data) return <Loading className="m-0 text-muted" />;
  const plan: ProjectPlan = q.data;
  const all = plan.items.filter((i) => i.kind === "work_package");
  if (all.length === 0) return <EmptyState title={t("progetti.tables.wpEmptyTitle")} text={t("progetti.tables.wpEmptyText")} />;
  const phase = new Map(plan.phases.map((p) => [p.id, p.name]));
  const items = all.filter((i) => !mode || i.delivery_mode === mode);
  const none = t("progetti.common.none");
  const owner = (i: PlanItem) => [i.team, i.assignee, i.supplier].filter(Boolean).join(" · ") || none;
  const modeOf = (i: PlanItem) => (i.delivery_mode ? t(`progetti.common.mode.${i.delivery_mode}`) : none);
  return (
    <div className="flex flex-col gap-4">
      <div className="flex max-w-xs flex-col gap-1">
        <label className="label text-muted" htmlFor="wp-mode">
          {t("progetti.tables.filterMode")}
        </label>
        <Select id="wp-mode" value={mode} onChange={(e) => setMode(e.target.value as "" | DeliveryMode)}>
          <option value="">{t("progetti.tables.allModes")}</option>
          <option value="agile">{t("progetti.common.mode.agile")}</option>
          <option value="waterfall">{t("progetti.common.mode.waterfall")}</option>
          <option value="supplier">{t("progetti.common.mode.supplier")}</option>
        </Select>
      </div>
      {items.length === 0 && <p className="m-0 text-muted">{t("progetti.tables.noMatch")}</p>}
      {items.length > 0 && (
        <>
          <table className="hidden w-full border-collapse text-[13px] md:table" aria-label={t("progetti.tables.wpList")}>
            <thead>
              <tr>
                <th className={th}>{t("progetti.tables.key")}</th>
                <th className={th}>{t("progetti.tables.title")}</th>
                <th className={th}>{t("progetti.tables.phase")}</th>
                <th className={th}>{t("progetti.tables.mode")}</th>
                <th className={th}>{t("progetti.tables.owner")}</th>
                <th className={th}>{t("progetti.tables.progress")}</th>
                <th className={th}>{t("progetti.tables.planned")}</th>
                <th className={th}>{t("progetti.tables.baseline")}</th>
                <th className={th}>{t("progetti.tables.state")}</th>
              </tr>
            </thead>
            <tbody>
              {items.map((i) => (
                <tr key={i.id}>
                  <td className={td}>
                    <ItemLink itemKey={i.key} />
                  </td>
                  <td className={`${td} font-semibold`}>{i.title}</td>
                  <td className={td}>{(i.phase_id && phase.get(i.phase_id)) || t("progetti.common.noPhase")}</td>
                  <td className={td}>{modeOf(i)}</td>
                  <td className={td}>{owner(i)}</td>
                  <td className={`${td} min-w-[110px]`}>
                    <div className="flex items-center gap-2">
                      <ProgressBar percent={i.progress} />
                      <span className="tabular w-9 shrink-0 text-right">{Math.round(i.progress)}%</span>
                    </div>
                  </td>
                  <td className={`${td} tabular whitespace-nowrap`}>{range(i, none)}</td>
                  <td className={td}><BaselineCell i={i} hasBaseline={plan.baseline != null} /></td>
                  <td className={td}>{i.state}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <ul className="m-0 flex list-none flex-col p-0 md:hidden" aria-label={t("progetti.tables.wpList")}>
            {items.map((i) => (
              <li key={i.id} className="flex flex-col gap-1.5 border-b border-hair py-3">
                <div className="flex items-baseline justify-between gap-3">
                  <ItemLink itemKey={i.key} />
                  <span className="text-[12.5px] text-muted">{i.state}</span>
                </div>
                <span className="font-semibold">{i.title}</span>
                <span className="text-[12.5px] text-muted">
                  {modeOf(i)} · {(i.phase_id && phase.get(i.phase_id)) || t("progetti.common.noPhase")} · {owner(i)}
                </span>
                <div className="flex items-center gap-2">
                  <ProgressBar percent={i.progress} />
                  <span className="tabular w-9 shrink-0 text-right text-[12.5px]">{Math.round(i.progress)}%</span>
                </div>
                <span className="tabular text-[12.5px]">
                  {t("progetti.tables.planned")}: {range(i, none)} · {t("progetti.tables.baseline")}: <BaselineCell i={i} hasBaseline={plan.baseline != null} />
                </span>
              </li>
            ))}
          </ul>
        </>
      )}
    </div>
  );
}

export function MilestonesTab({ refKey }: { refKey: string }) {
  const t = useT();
  const q = usePlan(refKey);
  const ov = useQuery({ queryKey: projectKeys.overview(refKey), queryFn: () => projectsApi.overview(refKey) });
  if (q.error) return <ErrorNote error={q.error} />;
  if (!q.data) return <Loading className="m-0 text-muted" />;
  const items = q.data.items.filter((i) => i.kind === "milestone");
  if (items.length === 0) return <EmptyState title={t("progetti.tables.msEmptyTitle")} text={t("progetti.tables.msEmptyText")} />;
  const phase = new Map(q.data.phases.map((p) => [p.id, p.name]));
  const none = t("progetti.common.none");
  // Il ritardo lo dice il server (stessa funzione della Panoramica): qui si legge.
  const late = new Map((ov.data?.numbers.late ?? []).map((l) => [l.id, l.days_late]));
  const delay = (i: PlanItem) => {
    const d = late.get(i.id);
    return d && d > 0 ? <span className="font-semibold text-danger">{t("progetti.common.daysLate", { count: d })}</span> : <span className="text-muted">{i.state_category === "done" ? none : t("progetti.tables.onTime")}</span>;
  };
  const ph = (i: PlanItem) => (i.phase_id && phase.get(i.phase_id)) || t("progetti.common.noPhase");
  return (
    <div className="flex flex-col gap-4">
      <table className="hidden w-full border-collapse text-[13px] md:table" aria-label={t("progetti.tables.msList")}>
        <thead>
          <tr>
            <th className={th}>{t("progetti.tables.key")}</th>
            <th className={th}>{t("progetti.tables.title")}</th>
            <th className={th}>{t("progetti.tables.phase")}</th>
            <th className={th}>{t("progetti.tables.date")}</th>
            <th className={th}>{t("progetti.tables.baseline")}</th>
            <th className={th}>{t("progetti.tables.state")}</th>
            <th className={th}>{t("progetti.tables.late")}</th>
          </tr>
        </thead>
        <tbody>
          {items.map((i) => (
            <tr key={i.id}>
              <td className={td}>
                <ItemLink itemKey={i.key} />
              </td>
              <td className={`${td} font-semibold`}>{i.title}</td>
              <td className={td}>{ph(i)}</td>
              <td className={`${td} tabular whitespace-nowrap`}>{i.end || i.start ? date(i.end ?? i.start) : none}</td>
              <td className={td}><BaselineCell i={i} hasBaseline={q.data.baseline != null} /></td>
              <td className={td}>{i.state}</td>
              <td className={td}>{delay(i)}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <ul className="m-0 flex list-none flex-col p-0 md:hidden" aria-label={t("progetti.tables.msList")}>
        {items.map((i) => (
          <li key={i.id} className="flex flex-col gap-1 border-b border-hair py-3">
            <div className="flex items-baseline justify-between gap-3">
              <ItemLink itemKey={i.key} />
              <span className="text-[12.5px] text-muted">{i.state}</span>
            </div>
            <span className="font-semibold">{i.title}</span>
            <span className="text-[12.5px] text-muted">{ph(i)}</span>
            <span className="tabular text-[12.5px]">
              {i.end || i.start ? date(i.end ?? i.start) : none} · {delay(i)}
            </span>
            {q.data.baseline && (
              <span className="text-[12.5px]">
                {t("progetti.tables.baseline")}: <BaselineCell i={i} hasBaseline />
              </span>
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}
