// «Attinge da» nella scheda della richiesta: per ogni budget in vigore dell'elemento (non sostituito) il nodo
// dell'albero da cui attinge, con il residuo, come nel dettaglio del budget in /finanza
// (docs/sviluppo/budget-collegamento.md). Solo con finance.read; collega e scollega solo chi ha finance.write.
import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { financeApi, type Budget } from "../../api";
import { useT } from "../../i18n/react";
import { useMe } from "../../shell";
import { ErrorNote } from "../../ui";
import { kindLabel, statusLabel } from "../finance-ui";
import { BudgetSource } from "./BudgetSource";
import { useCurrency } from "./budget-ui";

export function RequestBudgetSource({ elementKey }: { elementKey: string }) {
  const t = useT();
  const cur = useCurrency();
  const perms = useMe().data?.active_tenant?.permissions ?? [];
  const canWrite = perms.includes("*") || perms.includes("finance.write");
  const list = useQuery({
    queryKey: ["finance", "budgets", "", elementKey],
    queryFn: () => financeApi.budgets({ element: elementKey }),
    retry: false,
  });
  if (list.error) return <ErrorNote error={list.error} />;
  const items = (list.data?.items ?? []).filter((b) => b.status !== "superseded");
  if (!list.data) return null;
  return (
    <section className="flex flex-col gap-3" aria-label={t("budget.reqlink.title")} data-testid="request-budget-source">
      <h4 className="label m-0 text-muted">{t("budget.reqlink.title")}</h4>
      {items.length === 0 ? (
        <p className="m-0 text-[12.5px] text-muted">{t("budget.reqlink.empty")}</p>
      ) : (
        items.map((b) => <OneBudget key={b.id} b={b} cur={cur} canWrite={canWrite} />)
      )}
    </section>
  );
}

function OneBudget({ b, cur, canWrite }: { b: Budget; cur: string; canWrite: boolean }) {
  const t = useT();
  // Le righe servono per «Questo budget ne usa»: l'elenco non le porta, il dettaglio sì.
  const d = useQuery({ queryKey: ["finance", "budget", b.id], queryFn: () => financeApi.budget(b.id), retry: false });
  const lines = (d.data?.lines ?? []).map((l) => ({ period: l.period ?? "", cents: l.amount_cents }));
  return (
    <div className="flex flex-col gap-2 border-l-2 border-hair pl-3" data-budget={b.id}>
      <p className="m-0 flex flex-wrap items-baseline gap-x-2 text-[13px]">
        <span className="font-semibold">{t("budget.reqlink.budget", { year: b.fiscal_year, version: b.version, kind: kindLabel(b.kind) })}</span>
        <span className="text-muted" data-status={b.status}>{statusLabel(b.status)}</span>
        <Link to="/finanza" search={{ richiesta: b.element_key }} className="ml-auto text-[12px] text-accent underline">
          {t("budget.reqlink.open")}
        </Link>
      </p>
      {d.data && <BudgetSource cur={cur} canWrite={canWrite} kind={b.kind} budget={{ id: b.id, status: b.status }} lines={lines} />}
      <ErrorNote error={d.error} />
    </div>
  );
}
