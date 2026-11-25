import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { api } from "../api";
import { PageHeader } from "../shell";
import { ErrorNote, ago, euro } from "../ui";
import { useT } from "../i18n/react";
import { useTypeNames } from "../typesApi";
import { weekLabel } from "../tempoApi";

export function Inbox() {
  const t = useT();
  const tn = useTypeNames();
  const inbox = useQuery({ queryKey: ["inbox"], queryFn: api.inbox });
  const items = inbox.data?.items ?? [];
  return (
    <>
      <PageHeader path={t("lavoro.inbox.path")} title={t("lavoro.inbox.title")} />
      <div className="overflow-x-auto px-4 md:px-7 pb-8">
        <ErrorNote error={inbox.error} />
        <table className="w-full border-collapse tabular">
          <thead>
            <tr className="label h-9 text-left">
              <th scope="col" className="border-b-2 border-rule pr-3">ID</th>
              <th scope="col" className="border-b-2 border-rule px-3">{tn.name("business_request", t("lavoro.inbox.col.request"))}</th>
              <th scope="col" className="hidden border-b-2 border-rule px-3 sm:table-cell">{t("lavoro.inbox.col.stage")}</th>
              <th scope="col" className="border-b-2 border-rule px-3 text-right">{t("lavoro.inbox.col.amount")}</th>
              <th scope="col" className="hidden border-b-2 border-rule pl-3 text-right sm:table-cell">{t("lavoro.inbox.col.waiting")}</th>
            </tr>
          </thead>
          <tbody>
            {items.map((it) => (
              <tr key={it.step_id} className="border-b border-hair hover:bg-sel/50">
                {it.approval_subject === "timesheet" ? (
                  <>
                    <td className="py-2.5 pr-3 font-mono text-[12px] font-medium whitespace-nowrap text-accent">{t("lavoro.inbox.timesheetStage")}</td>
                    <td className="px-3 py-2.5">
                      <Link to="/tempo" search={{ settimana: it.timesheet_week ?? undefined, persona: it.timesheet_member ?? undefined }}
                        className="touch-44 inline-flex flex-wrap items-center gap-x-1.5 font-medium text-fg no-underline hover:underline">
                        {t("lavoro.inbox.timesheetOpen", { name: it.title })}
                        {it.timesheet_week && <span className="font-normal text-muted">· {t("lavoro.inbox.timesheetWeek", { week: weekLabel(it.timesheet_week) })}</span>}
                      </Link>
                    </td>
                    <td className="hidden px-3 py-2.5 sm:table-cell">{t("lavoro.inbox.timesheetStage")}</td>
                  </>
                ) : (
                  <>
                    <td className="py-2.5 pr-3 font-mono text-[12px] font-medium whitespace-nowrap text-accent">{it.element_key}</td>
                    <td className="px-3 py-2.5">
                      <Link to="/e/$key" params={{ key: it.element_key }} search={{ scheda: "approvazioni" }} className="touch-44 inline-flex items-center font-medium text-fg no-underline hover:underline">
                        {it.title}
                      </Link>
                    </td>
                    <td className="hidden px-3 py-2.5 sm:table-cell">{it.stage}</td>
                  </>
                )}
                <td className="px-3 py-2.5 text-right font-mono text-[12px]">{euro(it.amount_cents)}</td>
                <td className="hidden py-2.5 pl-3 text-right text-[12px] text-muted sm:table-cell">{ago(it.requested_at)}</td>
              </tr>
            ))}
            {!inbox.isPending && items.length === 0 && (
              <tr>
                <td colSpan={5} className="py-8 text-muted">{t("lavoro.inbox.empty")}</td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </>
  );
}
