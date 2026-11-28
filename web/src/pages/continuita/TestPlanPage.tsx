// Piano annuale dei test di resilienza (/architettura/continuita/test?anno=): righe = funzioni
// critiche e oggetti con test nell'anno, colonne = mesi (sul telefono un elenco), chi non ha test
// nell'anno è evidenziato; «Pianifica» crea un test dalla riga.
import { useEffect, useRef, useState } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Select } from "../../select";
import { ErrorNote, Loading, SectionTitle, date } from "../../ui";
import { useT } from "../../i18n/react";
import { fmtDate } from "../../i18n/format";
import { Dialog } from "../work-ui";
import { EaPage } from "../ea/tabs";
import { continuityApi, testMonth, type TestBrief, type TestPlanRow, type TestType } from "../../continuityApi";
import { ContinuityError, ContinuityNav, Minutes } from "./ui";

export type TestPlanSearch = { anno?: number; riga?: string };

const MONTHS = Array.from({ length: 12 }, (_, i) => i);
const monthName = (m: number, year: number) => fmtDate(new Date(year, m, 1), { month: "short" });

export function TestPlanPage({ search }: { search: TestPlanSearch }) {
  const t = useT();
  const nav = useNavigate();
  const year = search.anno ?? new Date().getFullYear();
  const plan = useQuery({ queryKey: ["continuity", "tests", year], queryFn: () => continuityApi.tests(year) });
  const [planning, setPlanning] = useState<TestPlanRow | null>(null);
  const go = (y: number) => nav({ to: "/architettura/continuita/test", search: { anno: y }, replace: true });
  const d = plan.data;
  const rows = d?.rows ?? [];
  const missing = rows.filter((r) => !r.planned_in_year).length;
  const isRow = (r: TestPlanRow) => !!search.riga && (search.riga === r.subject.id || search.riga === r.subject.key);
  return (
    <EaPage active="continuita" title={t("continuita.tests.title")}>
      <ContinuityNav active="test" />
      <p className="m-0 max-w-3xl text-muted">{t("continuita.tests.intro")}</p>
      <div className="flex items-center gap-2" role="group" aria-label={t("continuita.tests.year")}>
        <button type="button" className="btn px-2.5 py-1" aria-label={t("continuita.tests.prevYear")} onClick={() => go(year - 1)}>
          ‹
        </button>
        <span className="font-mono text-[16px] font-bold tabular">{year}</span>
        <button type="button" className="btn px-2.5 py-1" aria-label={t("continuita.tests.nextYear")} onClick={() => go(year + 1)}>
          ›
        </button>
      </div>
      {plan.isPending && <Loading />}
      <ContinuityError error={plan.error} />
      {d && (
        <section className="flex flex-col gap-2" aria-label={t("continuita.tests.title")}>
          <SectionTitle aside={<span className="text-[12px] text-muted tabular">{t("continuita.tests.withoutTest", { count: missing })}</span>}>
            {t("continuita.tests.rows", { count: rows.length })}
          </SectionTitle>
          {rows.length === 0 && <p className="m-0 text-muted">{t("continuita.tests.empty")}</p>}

          {/* Da tablet in su: tabella con i mesi, che scorre dentro il riquadro. */}
          {rows.length > 0 && (
            <div className="hidden max-w-full overflow-x-auto md:block">
              <table className="w-full min-w-[900px] border-collapse text-[12.5px]">
                <thead>
                  <tr className="border-b border-rule text-left">
                    <th scope="col" className="w-[16rem] px-2 py-1.5 font-semibold">{t("continuita.tests.subject")}</th>
                    {MONTHS.map((m) => (
                      <th key={m} scope="col" className="px-1 py-1.5 font-semibold capitalize">
                        {monthName(m, year)}
                      </th>
                    ))}
                    <th scope="col" className="px-2 py-1.5" />
                  </tr>
                </thead>
                <tbody>
                  {rows.map((r) => (
                    <PlanTableRow key={r.subject.id} row={r} year={year} highlight={isRow(r)} canWrite={d.can.write} onPlan={() => setPlanning(r)} />
                  ))}
                </tbody>
              </table>
            </div>
          )}

          {/* Telefono: elenco. */}
          <ul className="m-0 flex list-none flex-col p-0 md:hidden">
            {rows.map((r) => (
              <li key={r.subject.id} className={`flex flex-col gap-1 border-b border-hair py-2.5 ${!r.planned_in_year ? "border-l-4 border-l-flag pl-2" : ""} ${isRow(r) ? "bg-sel" : ""}`}>
                <SubjectCell row={r} />
                {r.tests.length === 0 ? (
                  <span className="text-[12.5px] text-flag">{t("continuita.tests.noneInYear")}</span>
                ) : (
                  <ul className="m-0 flex list-none flex-col gap-1 p-0">
                    {r.tests.map((x) => (
                      <li key={x.id} className="text-[12.5px]">
                        <TestChip test={x} long />
                      </li>
                    ))}
                  </ul>
                )}
                {d.can.write && (
                  <button type="button" className="btn min-h-[44px] self-start" onClick={() => setPlanning(r)}>
                    {t("continuita.tests.plan")}
                  </button>
                )}
              </li>
            ))}
          </ul>
        </section>
      )}
      {planning && d && <PlanDialog row={planning} year={year} types={d.types} onClose={() => setPlanning(null)} />}
    </EaPage>
  );
}

function PlanTableRow({ row, year, highlight, canWrite, onPlan }: { row: TestPlanRow; year: number; highlight: boolean; canWrite: boolean; onPlan: () => void }) {
  const t = useT();
  const ref = useRef<HTMLTableRowElement>(null);
  useEffect(() => {
    if (highlight) ref.current?.scrollIntoView({ block: "center" });
  }, [highlight]);
  const byMonth = new Map<number, TestBrief[]>();
  for (const x of row.tests) {
    const m = testMonth(x, year);
    if (m != null) (byMonth.get(m) ?? byMonth.set(m, []).get(m)!).push(x);
  }
  return (
    <tr ref={ref} className={`border-b border-hair align-top ${highlight ? "bg-sel" : ""}`}>
      <th scope="row" className={`px-2 py-1.5 text-left font-normal ${!row.planned_in_year ? "border-l-4 border-l-flag" : ""}`}>
        <SubjectCell row={row} />
        {!row.planned_in_year && <span className="block text-[11.5px] text-flag">{t("continuita.tests.noneInYear")}</span>}
      </th>
      {MONTHS.map((m) => (
        <td key={m} className="px-1 py-1.5">
          <div className="flex flex-col gap-1">
            {(byMonth.get(m) ?? []).map((x) => (
              <TestChip key={x.id} test={x} />
            ))}
          </div>
        </td>
      ))}
      <td className="px-2 py-1.5 text-right">
        {canWrite && (
          <button type="button" className="btn py-1 text-[12.5px]" onClick={onPlan}>
            {t("continuita.tests.plan")}
          </button>
        )}
      </td>
    </tr>
  );
}

function SubjectCell({ row }: { row: TestPlanRow }) {
  const t = useT();
  return (
    <span className="flex min-w-0 flex-col">
      <span className="flex flex-wrap items-baseline gap-x-2">
        <Link to="/e/$key" params={{ key: row.subject.key }} className="font-medium text-fg no-underline hover:underline">
          {row.subject.title}
        </Link>
        <span className="text-[11.5px] text-muted">{row.subject.kind === "application" ? t("continuita.kind.application") : t("continuita.kind.capability")}</span>
        {row.critical && <span className="border border-flag px-1 text-[10.5px] font-semibold text-flag">{t("continuita.bia.critical")}</span>}
      </span>
      <span className="text-[11.5px] text-muted">
        {t("continuita.tests.requiredRto")} <Minutes m={row.required_rto} />
        {row.last_done && <> · {t("continuita.tests.lastDone", { date: date(row.last_done.executed_on ?? row.last_done.planned_on) })}</>}
      </span>
    </span>
  );
}

/** Un test nel piano: chiave, esito (o «pianificato») e data. */
function TestChip({ test, long }: { test: TestBrief; long?: boolean }) {
  const t = useT();
  const cls =
    test.outcome === "failed" ? "border-danger text-danger" : test.outcome === "passed_with_findings" ? "border-flag" : test.outcome === "passed" ? "border-ok" : "border-hair border-dashed";
  const status = test.outcome ? t(`continuita.outcome.${test.outcome}`) : test.state_category === "cancelled" ? t("continuita.tests.cancelled") : t("continuita.tests.planned");
  return (
    <Link to="/e/$key" params={{ key: test.key }} className={`inline-flex flex-col border-l-2 ${cls} pl-1.5 text-[11.5px] leading-tight text-fg no-underline hover:underline`} title={test.title}>
      <span className="font-mono">{test.key}</span>
      <span className={test.outcome === "failed" ? "font-semibold text-danger" : "text-muted"}>{status}</span>
      {long && <span className="text-muted">{date(test.executed_on ?? test.planned_on)}</span>}
    </Link>
  );
}

function PlanDialog({ row, year, types, onClose }: { row: TestPlanRow; year: number; types: TestType[]; onClose: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const today = new Date().toISOString().slice(0, 10);
  const [type, setType] = useState(types[0]?.key ?? "");
  const [day, setDay] = useState(today.startsWith(`${year}-`) ? today : `${year}-01-15`);
  const [title, setTitle] = useState("");
  const m = useMutation({
    mutationFn: () => continuityApi.createTest(row.subject.key || row.subject.id, type, day, title.trim() || undefined),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["continuity"] });
      onClose();
    },
  });
  const desc = types.find((x) => x.key === type)?.description;
  return (
    <Dialog title={t("continuita.tests.planTitle", { name: row.subject.title })} onClose={onClose}>
      <form
        className="flex flex-col gap-3"
        onSubmit={(e) => {
          e.preventDefault();
          m.mutate();
        }}
      >
        <label className="flex flex-col gap-1">
          <span className="label">{t("continuita.test.type")}</span>
          <Select className="field" value={type} onChange={(e) => setType(e.target.value)} required>
            {types.map((x) => (
              <option key={x.key} value={x.key}>
                {x.name}
              </option>
            ))}
          </Select>
          {desc && <span className="text-[12px] text-muted">{desc}</span>}
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{t("continuita.test.plannedOn")}</span>
          <input className="field" type="date" required value={day} onChange={(e) => setDay(e.target.value)} />
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{t("continuita.tests.titleOptional")}</span>
          <input className="field" value={title} onChange={(e) => setTitle(e.target.value)} />
        </label>
        <ErrorNote error={m.error} />
        <div className="flex gap-2">
          <button className="btn btn-primary min-h-[44px]" disabled={m.isPending || !type || !day}>
            {t("continuita.tests.plan")}
          </button>
          <button type="button" className="btn min-h-[44px]" onClick={onClose}>
            {t("continuita.common.cancel")}
          </button>
        </div>
      </form>
    </Dialog>
  );
}
