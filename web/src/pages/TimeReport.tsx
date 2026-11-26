// Report del tempo (piano, passo 18; replica/work.md «Tempo e team»): le ore per persona,
// team, spazio, epic, richiesta, elemento, settimana o mese, con raggruppamento a due livelli,
// confronto con la stima e con la capacità. Ogni numero apre le registrazioni che lo
// compongono; l'export è in CSV. I costi li vede solo chi legge la finanza.
import { Select } from "../select";
import { Fragment, useMemo, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { PageHeader } from "../shell";
import { ErrorNote, date, euro, Loading } from "../ui";
import { Dialog } from "./work-ui";
import { useT } from "../i18n/react";
import { Trans } from "../i18n/Trans";
import type { Key } from "../i18n/core";
import { LoadBar, TempoTabs } from "./tempo-ui";
import { dimList, addDays, hoursText, isoDay, mondayOf, tempoApi, totalText, type ReportGroup, type TimeFilter } from "../tempoApi";
import { crumbs } from "../crumbs";

type Dim = "person" | "team" | "space" | "epic" | "request" | "element" | "week" | "month";
type Extra = Partial<Record<Dim, { key: string; label: string }>>;

const dimName = (d: string) => dimList().find(([k]) => k === d)?.[1] ?? d;

function presets(): [Key, string, string][] {
  const today = new Date();
  const first = new Date(today.getFullYear(), today.getMonth(), 1);
  const lastFirst = new Date(today.getFullYear(), today.getMonth() - 1, 1);
  const lastEnd = new Date(today.getFullYear(), today.getMonth(), 0);
  return [
    ["lavoro.timereport.preset.thisWeek", mondayOf(), isoDay(today)],
    ["lavoro.timereport.preset.lastWeek", addDays(mondayOf(), -7), addDays(mondayOf(), -1)],
    ["lavoro.timereport.preset.thisMonth", isoDay(first), isoDay(today)],
    ["lavoro.timereport.preset.lastMonth", isoDay(lastFirst), isoDay(lastEnd)],
    ["lavoro.timereport.preset.last90", addDays(isoDay(today), -90), isoDay(today)],
  ];
}

export function TimeReport() {
  const t = useT();
  const pre = useMemo(presets, []);
  const [from, setFrom] = useState(pre[2][1]);
  const [to, setTo] = useState(pre[2][2]);
  const [dim1, setDim1] = useState<Dim>("person");
  const [dim2, setDim2] = useState<"" | Dim>("space");
  const [extra, setExtra] = useState<Extra>({});
  const [open, setOpen] = useState<Record<string, boolean>>({});
  const [records, setRecords] = useState<{ title: string; filter: TimeFilter } | null>(null);

  const filter: TimeFilter = { from, to, ...Object.fromEntries(Object.entries(extra).map(([k, v]) => [k, v.key || "none"])) };
  const group = dim2 ? `${dim1},${dim2}` : dim1;
  const rep = useQuery({ queryKey: ["tempo", "report", filter, group], queryFn: () => tempoApi.report(filter, group), placeholderData: (p) => p, enabled: from <= to });
  const r = rep.data;
  const hasCap = r?.groups.some((g) => g.capacity != null) ?? false;

  const addFilter = (dim: Dim, g: ReportGroup) => {
    setExtra((x) => ({ ...x, [dim]: { key: g.key, label: g.label } }));
    if (dim === dim1) {
      if (dim2) {
        setDim1(dim2);
        setDim2("");
      } else setDim1(dim === "space" ? "element" : "space");
    }
  };
  const drill = (title: string, dims: [Dim, ReportGroup][]) => {
    const f: TimeFilter = { ...filter };
    for (const [d, g] of dims) (f as Record<string, string | undefined>)[d] = g.key || "none";
    setRecords({ title, filter: f });
  };

  return (
    <>
      <PageHeader
        path={crumbs("time", "report")}
        title={t("lavoro.timereport.title")}
        actions={
          r && (
            <a className="btn no-underline" href={tempoApi.reportCsvUrl(filter, group)} download>
              {t("lavoro.timereport.exportCsv")}
            </a>
          )
        }
      />
      <TempoTabs active="report" />
      <div className="flex max-w-[1180px] flex-col gap-4 px-4 pt-4 pb-12 md:px-7">
        <div className="flex flex-wrap items-end gap-x-4 gap-y-3">
          <label className="flex flex-col gap-1">
            <span className="label text-muted">{t("lavoro.timereport.from")}</span>
            <input type="date" className="field w-[150px] py-1.5" value={from} onChange={(e) => e.target.value && setFrom(e.target.value)} />
          </label>
          <label className="flex flex-col gap-1">
            <span className="label text-muted">{t("lavoro.timereport.to")}</span>
            <input type="date" className="field w-[150px] py-1.5" value={to} onChange={(e) => e.target.value && setTo(e.target.value)} />
          </label>
          <label className="flex flex-col gap-1">
            <span className="label text-muted">{t("lavoro.timereport.groupBy")}</span>
            <Select className="field w-auto py-1.5" value={dim1} onChange={(e) => setDim1(e.target.value as Dim)}>
              {dimList().filter(([k]) => k !== dim2).map(([k, l]) => (
                <option key={k} value={k}>
                  {l}
                </option>
              ))}
            </Select>
          </label>
          <label className="flex flex-col gap-1">
            <span className="label text-muted">{t("lavoro.timereport.thenBy")}</span>
            <Select className="field w-auto py-1.5" value={dim2} onChange={(e) => setDim2(e.target.value as "" | Dim)}>
              <option value="">{t("lavoro.timereport.none")}</option>
              {dimList().filter(([k]) => k !== dim1).map(([k, l]) => (
                <option key={k} value={k}>
                  {l}
                </option>
              ))}
            </Select>
          </label>
        </div>
        <div className="flex flex-wrap items-center gap-1.5" role="group" aria-label={t("lavoro.timereport.quickPeriods")}>
          {pre.map(([l, a, b]) => (
            <button key={l} className={`border px-2 py-1 text-[12px] ${from === a && to === b ? "border-fg bg-fg text-bg" : "border-hair text-muted hover:text-fg"}`} onClick={() => (setFrom(a), setTo(b))}>
              {t(l)}
            </button>
          ))}
        </div>
        {Object.keys(extra).length > 0 && (
          <div className="flex flex-wrap items-center gap-2 text-[12.5px]" aria-label={t("lavoro.timereport.activeFilters")}>
            <span className="label text-muted">{t("lavoro.timereport.only")}</span>
            {(Object.entries(extra) as [Dim, { key: string; label: string }][]).map(([d, v]) => (
              <button
                key={d}
                className="inline-flex items-center gap-1.5 border border-rule px-2 py-0.5 hover:bg-sel"
                onClick={() => setExtra((x) => Object.fromEntries(Object.entries(x).filter(([k]) => k !== d)))}
                title={t("lavoro.timereport.removeFilter")}
              >
                <span className="text-muted">{dimName(d)}:</span> {v.label}
                <span aria-hidden="true">×</span>
              </button>
            ))}
          </div>
        )}
        {r && r.scope !== "all" && (
          <p className="m-0 text-[12.5px] text-muted">
            {r.scope === "team" ? t("lavoro.timereport.scopeTeam") : t("lavoro.timereport.scopeSelf")}
          </p>
        )}
        <ErrorNote error={rep.error} />
        {from > to && <p className="m-0 text-danger">{t("lavoro.timereport.reversed")}</p>}
        {rep.isPending && <Loading className="m-0 text-muted" />}
        {r && (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[320px] border-collapse text-[13px]">
              <thead>
                <tr className="border-b-2 border-rule text-right align-bottom">
                  <th scope="col" className="label py-1.5 pr-3 text-left">
                    {dimName(r.dim1)}
                    {r.dim2 ? ` › ${dimName(r.dim2)}` : ""}
                  </th>
                  <th scope="col" className="label px-2 py-1.5">
                    {t("lavoro.timereport.recorded")}
                  </th>
                  <th scope="col" className="label hidden px-2 py-1.5 md:table-cell" title={t("lavoro.timereport.estimateTip")}>
                    {t("lavoro.timereport.estimate")}
                  </th>
                  <th scope="col" className="label hidden px-2 py-1.5 md:table-cell">
                    {t("lavoro.timereport.variance")}
                  </th>
                  {hasCap && (
                    <th scope="col" className="label hidden px-2 py-1.5 md:table-cell" title={t("lavoro.timereport.capacityTip")}>
                      {t("lavoro.timereport.capacity")}
                    </th>
                  )}
                  {hasCap && (
                    <th scope="col" className="label hidden w-[140px] px-2 py-1.5 text-left md:table-cell">
                      {t("lavoro.timereport.usage")}
                    </th>
                  )}
                  {r.can_cost && (
                    <th scope="col" className="label hidden px-2 py-1.5 md:table-cell">
                      {t("lavoro.timereport.cost")}
                    </th>
                  )}
                </tr>
              </thead>
              <tbody>
                {r.groups.length === 0 && (
                  <tr>
                    <td colSpan={7} className="py-6 text-left text-muted">
                      <Trans
                        k="lavoro.timereport.noHours"
                        slots={{
                          link: (
                            <Link to="/tempo" className="text-accent underline">
                              {t("lavoro.timereport.timesheetLink")}
                            </Link>
                          ),
                        }}
                      />
                    </td>
                  </tr>
                )}
                {r.groups.map((g) => {
                  const expanded = open[g.key] ?? true;
                  const kids = g.children ?? [];
                  return (
                    <Fragment key={g.key || "∅"}>
                      <tr className="border-b border-hair bg-sel/30 text-right">
                        <th scope="row" className="py-1.5 pr-3 text-left font-semibold">
                          <span className="flex items-center gap-1.5">
                            {kids.length > 0 && (
                              <button aria-expanded={expanded} aria-label={t(expanded ? "lavoro.timereport.collapse" : "lavoro.timereport.expand", { label: g.label })} className="w-4 text-muted" onClick={() => setOpen((o) => ({ ...o, [g.key]: !expanded }))}>
                                <span aria-hidden="true" className={`inline-block ${expanded ? "rotate-90" : ""}`}>
                                  ›
                                </span>
                              </button>
                            )}
                            <button className="min-w-0 text-left break-words hover:underline" title={t("lavoro.timereport.filterOnThis")} onClick={() => g.key && addFilter(r.dim1 as Dim, g)}>
                              {g.label}
                            </button>
                          </span>
                        </th>
                        <Numbers g={g} r={r} hasCap={hasCap} bold onOpen={() => drill(`${dimName(r.dim1)}: ${g.label}`, [[r.dim1 as Dim, g]])} />
                      </tr>
                      {expanded &&
                        kids.map((c) => (
                          <tr key={g.key + "|" + c.key} className="border-b border-hair text-right">
                            <th scope="row" className="py-1.5 pr-3 pl-6 text-left font-normal">
                              <span className="block break-words">{c.label}</span>
                            </th>
                            <Numbers g={c} r={r} hasCap={false} onOpen={() => drill(`${g.label} › ${c.label}`, [[r.dim1 as Dim, g], [r.dim2 as Dim, c]])} />
                            {hasCap && <td className="hidden md:table-cell" colSpan={2} />}
                          </tr>
                        ))}
                    </Fragment>
                  );
                })}
              </tbody>
              {r.groups.length > 0 && (
                <tfoot>
                  <tr className="border-t-2 border-rule text-right">
                    <th scope="row" className="label py-2 pr-3 text-left">
                      {t("lavoro.timereport.total")}
                    </th>
                    <Numbers g={r.total} r={r} hasCap={false} bold onOpen={() => drill(t("lavoro.timereport.allRecords"), [])} />
                    {hasCap && <td className="hidden md:table-cell" colSpan={2} />}
                  </tr>
                </tfoot>
              )}
            </table>
          </div>
        )}
        {r && r.groups.length > 0 && <p className="m-0 text-[12px] text-muted">{t("lavoro.timereport.footnote")}</p>}
      </div>
      {records && (
        <Dialog title={records.title} onClose={() => setRecords(null)} wide>
          <RecordsList filter={records.filter} />
        </Dialog>
      )}
    </>
  );
}

function Numbers({ g, r, hasCap, bold, onOpen }: { g: ReportGroup; r: { can_cost: boolean }; hasCap: boolean; bold?: boolean; onOpen: () => void }) {
  const t = useT();
  const diff = g.estimate > 0 ? g.minutes - g.estimate : null;
  return (
    <>
      <td className="px-2 py-1.5">
        <button className={`font-mono tabular underline decoration-hair underline-offset-2 hover:decoration-fg ${bold ? "font-semibold" : ""}`} onClick={onOpen} title={t("lavoro.timereport.openRecords", { hours: hoursText(g.minutes) })}>
          {totalText(g.minutes)}
        </button>
      </td>
      <td className="hidden px-2 py-1.5 font-mono tabular text-muted md:table-cell">{g.estimate > 0 ? totalText(g.estimate) : "—"}</td>
      <td className={`hidden px-2 py-1.5 font-mono tabular md:table-cell ${diff != null && diff > 0 ? "text-flag" : "text-muted"}`}>
        {diff == null ? "—" : `${diff > 0 ? "+" : diff < 0 ? "−" : ""}${totalText(Math.abs(diff))}`}
      </td>
      {hasCap && <td className="hidden px-2 py-1.5 font-mono tabular text-muted md:table-cell">{g.capacity != null ? totalText(g.capacity) : ""}</td>}
      {hasCap && (
        <td className="hidden px-2 py-1.5 md:table-cell">{g.capacity != null ? <LoadBar minutes={g.minutes} capacity={g.capacity} label={t("lavoro.timereport.usageLabel", { label: g.label, minutes: totalText(g.minutes), capacity: totalText(g.capacity) })} /> : null}</td>
      )}
      {r.can_cost && <td className="hidden px-2 py-1.5 font-mono tabular md:table-cell">{g.cost_cents != null ? euro(g.cost_cents) : ""}</td>}
    </>
  );
}

function RecordsList({ filter }: { filter: TimeFilter }) {
  const t = useT();
  const recs = useQuery({ queryKey: ["tempo", "records", filter], queryFn: () => tempoApi.records(filter) });
  const d = recs.data;
  return (
    <div className="flex flex-col gap-2">
      <ErrorNote error={recs.error} />
      {recs.isPending && <Loading className="m-0 text-muted" />}
      {d && (
        <>
          <p className="m-0 flex flex-wrap items-baseline gap-3 text-[12.5px] text-muted">
            <span className="font-mono tabular">
              {t("lavoro.timereport.records", { count: d.count })} · {totalText(d.total_minutes)}
            </span>
            {d.truncated && <span>{t("lavoro.timereport.mostRecent")}</span>}
            <a className="ml-auto text-accent underline" href={tempoApi.recordsCsvUrl(filter)} download>
              {t("lavoro.timereport.exportCsv")}
            </a>
          </p>
          <ul className="m-0 flex max-h-[55dvh] list-none flex-col overflow-y-auto p-0">
            {d.items.map((x) => (
              <li key={x.id} className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-baseline gap-x-3 gap-y-0.5 border-b border-hair py-1.5">
                <span className="font-mono text-[11.5px] text-muted tabular">{date(x.date)}</span>
                <span className="min-w-0 break-words">
                  <Link to="/e/$key" params={{ key: x.element_key }} className="font-mono text-[12px] text-accent no-underline hover:underline">
                    {x.element_key}
                  </Link>{" "}
                  {x.title}
                </span>
                <span className="text-right font-mono tabular font-semibold">{totalText(x.minutes)}</span>
                <span />
                <span className="min-w-0 break-words text-[12px] text-muted">
                  {x.member}
                  {x.text ? ` · ${x.text}` : ""}
                </span>
                <span />
              </li>
            ))}
          </ul>
        </>
      )}
    </div>
  );
}
