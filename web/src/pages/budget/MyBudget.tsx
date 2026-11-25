// «Il mio budget» nel cruscotto (replica/budget.md §6): per chi risponde di un'unità o di un elemento, lo
// stanziato, l'impegnato, lo speso e il residuo del suo nodo e i giorni alla chiusura del periodo.
// Compare solo se si vede almeno un nodo che non sia la radice (la finanza usa la pagina Budget).
import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { budgetApi, type BudgetPeriod } from "../../api";
import { useT } from "../../i18n/react";
import { SectionTitle } from "../../ui";
import { NodeBar, mny, useCurrency } from "./budget-ui";

/** Periodi da mostrare: quelli aperti o in chiusura e quelli non chiusi che contengono oggi. */
function currentPeriods(periods: BudgetPeriod[]): BudgetPeriod[] {
  const today = new Date().toISOString().slice(0, 10);
  return periods.filter((p) => p.status === "open" || p.status === "closing" || (p.status !== "closed" && p.start_date <= today && p.end_date >= today)).slice(0, 20);
}

export function MyBudget() {
  const periods = useQuery({ queryKey: ["budget", "periods"], queryFn: () => budgetApi.periods(), retry: false });
  return (
    <>
      {currentPeriods(periods.data?.items ?? []).map((p) => (
        <MyBudgetPeriod key={p.id} period={p} />
      ))}
    </>
  );
}

function MyBudgetPeriod({ period }: { period: BudgetPeriod }) {
  const t = useT();
  const cur = useCurrency();
  const tree = useQuery({ queryKey: ["budget", "tree", period.id], queryFn: () => budgetApi.tree(period.id), retry: false });
  const items = tree.data?.items ?? [];
  const ids = new Set(items.map((n) => n.id));
  // I nodi più in alto tra quelli visibili: il mio sottoalbero parte da lì.
  const mine = items.filter((n) => n.kind !== "root" && (!n.parent_id || !ids.has(n.parent_id)));
  if (mine.length === 0) return null;
  return (
    <section className="flex flex-col gap-3" aria-label={t("budget.mine.title")}>
      <SectionTitle
        aside={
          <Link to="/budget" search={{ periodo: period.id }} className="text-[12px] text-accent underline">
            {t("budget.mine.open")}
          </Link>
        }
      >
        {t("budget.mine.title")}
      </SectionTitle>
      <p className="m-0 text-[12.5px] text-muted">
        {period.name}
        {period.days_to_close != null && ` · ${t("budget.mine.days", { count: period.days_to_close })}`}
      </p>
      <ul className="m-0 grid list-none grid-cols-1 gap-4 p-0 md:grid-cols-2 xl:grid-cols-3">
        {mine.map((n) => (
          <li key={n.id} className="flex flex-col gap-2 border-l-2 border-hair pl-3" data-mine={n.label}>
            <span className="font-semibold">{n.label}</span>
            <NodeBar n={n} />
            <dl className="m-0 grid grid-cols-2 gap-x-4 gap-y-1 text-[12.5px]">
              {(
                [
                  ["allocated", t("budget.col.allocated"), n.allocated_cents],
                  ["committed", t("budget.col.committed"), n.committed_cents],
                  ["spent", t("budget.col.spent"), n.spent_cents],
                  ["residual", t("budget.col.residual"), n.residual_cents],
                ] as const
              ).map(([id, label, v]) => (
                <div key={id} className="flex flex-col">
                  <dt className="label text-muted">{label}</dt>
                  <dd className={`m-0 font-mono text-[15px] font-semibold tabular ${id === "residual" && v < 0 ? "text-danger" : ""}`}>{mny(v, cur)}</dd>
                </div>
              ))}
            </dl>
          </li>
        ))}
      </ul>
    </section>
  );
}
