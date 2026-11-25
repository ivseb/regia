import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { financeApi, type ElementDetail } from "../api";
import { useT } from "../i18n/react";
import { Trans } from "../i18n/Trans";
import { ErrorNote, SectionTitle } from "../ui";
import { RequestBudget } from "./budget/RequestBudget";
import { ConsumptionBar, money, pct } from "./finance-ui";

// "Budget e costi" nel dettaglio della richiesta (demand.md §9-10): preventivo, budget
// approvato, consuntivo e impegnato, con la barra consumo/budget. Sono aggregati, visibili
// a chi vede la richiesta; voci, persone e tariffe restano nella pagina /finanza (finance.read).

export function RequestFinance({ detail }: { detail: ElementDetail }) {
  const t = useT();
  const key = detail.element.key || detail.element.id;
  const q = useQuery({ queryKey: ["finance", "totals", key], queryFn: () => financeApi.totals(key) });
  if (q.error) return <ErrorNote error={q.error} />;
  if (!q.data) return null;
  const d = q.data;
  const cur = d.currency;
  const estimate =
    d.estimate_low_cents == null && d.estimate_high_cents == null
      ? "—"
      : d.estimate_low_cents != null && d.estimate_high_cents != null && d.estimate_low_cents !== d.estimate_high_cents
        ? `${money(d.estimate_low_cents, d.estimate_currency)} – ${money(d.estimate_high_cents, d.estimate_currency)}`
        : money(d.estimate_high_cents ?? d.estimate_low_cents, d.estimate_currency);
  const over = d.consumption_pct != null && d.consumption_pct > 100;
  const alert = d.consumption_pct != null && d.alert_thresholds.some((th) => d.consumption_pct! >= th);
  const numbers: { label: string; value: string; note?: string }[] = [
    { label: t("budget.reqfin.estimate"), value: estimate, note: t("budget.reqfin.estimateNote") },
    {
      label: t("budget.reqfin.approved"),
      value: money(d.approved_cents, cur),
      note: d.pending_cents != null ? t("budget.reqfin.pending", { amount: money(d.pending_cents, cur) }) : d.approved_cents == null ? t("budget.reqfin.notApproved") : undefined,
    },
    { label: t("budget.reqfin.actual"), value: money(d.actual_cents, cur), note: d.capex_actual_cents ? t("budget.reqfin.capex", { amount: money(d.capex_actual_cents, cur) }) : undefined },
    { label: t("budget.reqfin.committed"), value: money(d.committed_cents, cur), note: t("budget.reqfin.committedNote") },
  ];
  const empty = d.approved_cents == null && d.pending_cents == null && !d.actual_cents && !d.committed_cents;
  return (
    <section className="flex flex-col gap-3" aria-label={t("budget.reqfin.title")}>
      <SectionTitle
        aside={
          d.can_see_details ? (
            <Link to="/finanza" search={{ tab: "costi", richiesta: key }} className="text-[12px] text-accent underline">
              {t("budget.reqfin.detail")}
            </Link>
          ) : undefined
        }
      >
        {t("budget.reqfin.title")}
      </SectionTitle>
      <dl className="m-0 grid grid-cols-2 gap-x-4 gap-y-3">
        {numbers.map((n) => (
          <div key={n.label} className="flex flex-col gap-0.5 border-l-2 border-hair pl-2.5">
            <dt className="label text-muted">{n.label}</dt>
            <dd className="m-0 font-mono text-[17px] leading-tight font-semibold tabular">{n.value}</dd>
            {n.note && <dd className="m-0 text-[11.5px] text-muted">{n.note}</dd>}
          </div>
        ))}
      </dl>
      {d.approved_cents != null && d.approved_cents > 0 && (
        <div className="flex flex-col gap-1.5">
          <p className="m-0 flex flex-wrap items-baseline justify-between gap-x-3 text-[12.5px]">
            <span>
              <Trans
                k="budget.reqfin.consumption"
                slots={{ pct: <span className={`font-mono font-semibold tabular ${over ? "text-danger" : ""}`}>{pct(d.consumption_pct)}</span> }}
              />
              {alert && <span className={`ml-1.5 font-semibold ${over ? "text-danger" : ""}`}>{over ? t("budget.reqfin.over") : t("budget.reqfin.nearLimit")}</span>}
            </span>
            {d.forecast_cents != null && (
              <span className="text-muted">
                <Trans
                  k="budget.reqfin.forecast"
                  params={{ source: d.forecast_source === "eac" ? t("budget.reqfin.eac") : t("budget.reqfin.forecastWord") }}
                  slots={{
                    amount: (
                      <span className={`font-mono font-semibold text-fg tabular ${d.forecast_cents > d.approved_cents ? "!text-danger" : ""}`}>{money(d.forecast_cents, cur)}</span>
                    ),
                  }}
                />
              </span>
            )}
          </p>
          <ConsumptionBar actual={d.actual_cents} committed={d.committed_cents} budget={d.approved_cents} thresholds={d.alert_thresholds} />
          <p className="m-0 flex gap-4 text-[11.5px] text-muted">
            <span className="flex items-center gap-1.5"><span className="inline-block h-2 w-3 bg-fg" aria-hidden="true" />{t("budget.reqfin.legendActual")}</span>
            <span className="flex items-center gap-1.5"><span className="inline-block h-2 w-3 bg-muted" aria-hidden="true" />{t("budget.reqfin.legendCommitted")}</span>
            {d.ev_enabled && <span className="ml-auto">{t("budget.reqfin.evOn")}</span>}
          </p>
        </div>
      )}
      {d.by_category.length > 0 && (
        <details className="group">
          <summary className="cursor-pointer text-[12.5px] text-muted hover:text-fg">{t("budget.reqfin.byCategory", { count: d.by_category.length })}</summary>
          <div className="mt-1.5 overflow-x-auto">
            <table className="w-full border-collapse text-[12.5px] tabular">
              <thead>
                <tr className="label text-left">
                  <th scope="col" className="border-b-2 border-rule py-1 pr-2">{t("budget.reqfin.category")}</th>
                  <th scope="col" className="border-b-2 border-rule px-2 py-1 text-right">{t("budget.reqfin.budget")}</th>
                  <th scope="col" className="border-b-2 border-rule px-2 py-1 text-right">{t("budget.reqfin.actual")}</th>
                  <th scope="col" className="hidden border-b-2 border-rule py-1 pl-2 text-right sm:table-cell">{t("budget.reqfin.committed")}</th>
                </tr>
              </thead>
              <tbody>
                {d.by_category.map((c) => (
                  <tr key={c.cost_category_id} className="border-b border-hair">
                    <td className="py-1 pr-2">
                      {c.category} <span className="font-mono text-[10.5px] text-muted uppercase">{c.nature}</span>
                    </td>
                    <td className="px-2 py-1 text-right font-mono">{money(c.budget_cents, cur)}</td>
                    <td className={`px-2 py-1 text-right font-mono ${c.budget_cents > 0 && c.actual_cents > c.budget_cents ? "text-danger" : ""}`}>
                      {money(c.actual_cents, cur)}
                    </td>
                    <td className="hidden py-1 pl-2 text-right font-mono sm:table-cell">{money(c.committed_cents, cur)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </details>
      )}
      {empty && (
        <p className="m-0 text-[12.5px] text-muted">
          {t("budget.reqfin.empty")}
          {d.can_edit ? ` ${t("budget.reqfin.emptyEdit")}` : ""}
        </p>
      )}
      <RequestBudget detail={detail} />
    </section>
  );
}
