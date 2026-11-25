// Budget di progetto collegato a un nodo dell'albero (replica/budget.md §2, «Collegamento con i budget di progetto»):
// il campo «Attinge da» nel budget di un elemento, con il residuo del nodo e quanto il budget ne userebbe,
// e l'elenco dei budget che attingono da un nodo nel suo pannello.
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { budgetApi, type AllocationNode, type BudgetKind, type ElementAllocation } from "../../api";
import { useT } from "../../i18n/react";
import { OptionSelect } from "../../select";
import { ErrorNote } from "../../ui";
import { ConfirmButton } from "../adminUi";
import { statusLabel, kindLabel } from "../finance-ui";
import { mny } from "./budget-ui";

/** Una riga del budget: mese «AAAA-MM» (vuoto = tutto l'esercizio) e importo in centesimi. */
export type SourceLine = { period: string; cents: number };

/** Quanto del budget cade nel periodo del nodo: le righe senza mese e quelle dei mesi del periodo. */
export function usedInPeriod(lines: SourceLine[], o: Pick<ElementAllocation, "start_date" | "end_date">): number {
  const a = o.start_date.slice(0, 7);
  const b = o.end_date.slice(0, 7);
  return lines.reduce((s, l) => (!l.period || (l.period >= a && l.period <= b) ? s + l.cents : s), 0);
}

type Props = {
  cur: string;
  /** Chi può collegare (finance.write). */
  canWrite: boolean;
  /** Le righe attuali del budget, per sapere quanto ne userebbe. */
  lines: SourceLine[];
  kind: BudgetKind;
} & (
  | { budget: { id: string; status: string }; element?: undefined; fiscalYear?: undefined; value?: undefined; onChange?: undefined }
  | { budget?: undefined; element: string; fiscalYear: string; value: string; onChange: (id: string) => void }
);

export function BudgetSource({ cur, canWrite, lines, kind, budget, element, fiscalYear, value, onChange }: Props) {
  const t = useT();
  const qc = useQueryClient();
  const edit = !!budget;
  const opts = useQuery({
    queryKey: ["budget", "element-allocations", budget?.id ?? element ?? "", fiscalYear ?? ""],
    queryFn: () => (budget ? budgetApi.elementAllocations({ budget_id: budget.id }) : budgetApi.elementAllocations({ element_id: element, fiscal_year_id: fiscalYear })),
    enabled: edit || !!element,
    retry: false,
  });
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["budget"] });
    qc.invalidateQueries({ queryKey: ["finance"] });
  };
  const link = useMutation({ mutationFn: (id: string | null) => budgetApi.linkBudget(budget!.id, id), onSuccess: refresh });
  const items = opts.data?.items ?? [];
  const selectedId = edit ? (opts.data?.linked_id ?? "") : (value ?? "");
  const sel = items.find((o) => o.id === selectedId);
  const hidden = edit && !!opts.data?.linked_hidden;
  const editable = canWrite && (!edit || budget.status !== "superseded");
  const options = items.map((o) => ({
    value: o.id,
    label: `${[...o.path, o.path.length ? "" : t("budget.link.thisElement")].filter(Boolean).join(" › ")} · ${o.fiscal_year === o.period ? o.period : `${o.fiscal_year} ${o.period}`}`,
    detail: t("budget.link.optionDetail", { amount: mny(o.available_cents, cur) }),
  }));
  const use = sel ? usedInPeriod(lines, sel) : 0;
  const counts = kind !== "forecast";
  const over = sel && counts ? use - sel.available_cents : 0;

  return (
    <section className="flex flex-col gap-2" aria-label={t("budget.link.label")} data-linked={selectedId ? "yes" : "no"}>
      <div className="flex min-w-0 flex-col gap-1">
        <span className="label text-muted" id="budget-link-label">{t("budget.link.label")}</span>
        {editable ? (
          <div className="flex flex-wrap items-center gap-2">
            <div className="min-w-0 flex-1 sm:max-w-md" aria-labelledby="budget-link-label">
              <OptionSelect
                value={selectedId}
                onChange={(v) => {
                  if (edit) {
                    if (v && v !== selectedId) link.mutate(v);
                  } else onChange!(v);
                }}
                options={options}
                noneLabel={edit ? undefined : t("budget.link.none")}
                placeholder={t("budget.link.pick")}
                aria-label={t("budget.link.label")}
                disabled={link.isPending}
              />
            </div>
            {edit && selectedId && (
              <ConfirmButton label={t("budget.link.unlink")} confirm={t("budget.link.unlinkConfirm")} title={t("budget.link.unlinkHelp")} pending={link.isPending} onConfirm={() => link.mutate(null)} />
            )}
          </div>
        ) : hidden ? (
          <span className="font-semibold">{t("budget.link.hidden")}</span>
        ) : sel ? (
          <span>
            {[...sel.path, sel.path.length ? "" : t("budget.link.thisElement")].filter(Boolean).join(" › ")} · {sel.period}
          </span>
        ) : (
          <span className="text-muted">{t("budget.link.noLink")}</span>
        )}
      </div>
      {hidden && <p className="m-0 text-[12.5px] text-muted">{t("budget.link.hiddenHelp")}</p>}
      {!editable && !hidden && canWrite === false && <p className="m-0 text-[12px] text-muted">{t("budget.link.readOnly")}</p>}
      {editable && !selectedId && !opts.isPending && (
        <p className="m-0 text-[12px] text-muted">
          {!edit && !element ? t("budget.link.needsElement") : items.length === 0 ? t("budget.link.noOptions") : t("budget.link.help")}
        </p>
      )}
      {sel && (
        <dl className="m-0 grid grid-cols-2 gap-x-4 gap-y-2 sm:grid-cols-5" data-testid="link-numbers">
          {(
            [
              ["allocated", t("budget.link.allocated"), mny(sel.allocated_cents, cur)],
              ["others", t("budget.link.others"), mny(sel.others_cents, cur)],
              ["available", t("budget.link.available"), mny(sel.available_cents, cur)],
              ["uses", t("budget.link.uses"), counts ? mny(use, cur) : "—"],
              ["after", t("budget.link.after"), counts ? mny(sel.available_cents - use, cur) : "—"],
            ] as const
          ).map(([k, label, v]) => (
            <div key={k} className="flex min-w-0 flex-col gap-0.5 border-l-2 border-hair pl-2.5">
              <dt className="label text-muted">{label}</dt>
              <dd className={`m-0 font-mono text-[14px] font-semibold tabular ${k === "after" && over > 0 ? "text-danger" : ""}`} data-k={k}>{v}</dd>
            </div>
          ))}
        </dl>
      )}
      {sel && !counts && <p className="m-0 text-[12px] text-muted">{t("budget.link.forecastNote")}</p>}
      {over > 0 && (
        <p role="alert" className="m-0 text-[13px] font-semibold text-danger" data-testid="link-exceeds">
          {t("budget.link.exceeds", { amount: mny(over, cur) })}
        </p>
      )}
      <ErrorNote error={link.error} />
    </section>
  );
}

/** Pannello del nodo: i budget di progetto che attingono da lì, con link all'elemento, stato e impegnato. */
export function NodeBudgets({ node, cur }: { node: AllocationNode; cur: string }) {
  const t = useT();
  const list = node.budgets ?? [];
  const committed = list.reduce((s, b) => s + b.committed_cents, 0);
  if (list.length === 0)
    return (
      <p className="m-0 max-w-[70ch] text-[13px] text-muted">
        {t("budget.link.nodeEmpty")} {t("budget.link.nodeHelp")}
      </p>
    );
  return (
    <div className="flex flex-col gap-2" data-testid="node-budgets">
      <ul className="m-0 flex list-none flex-col p-0">
        {list.map((b) => (
          <li key={b.id} className="grid grid-cols-[minmax(0,1fr)_auto] items-baseline gap-x-3 gap-y-0.5 border-b border-hair py-1.5 text-[13px]">
            <span className="min-w-0">
              <Link to="/e/$key" params={{ key: b.element_key }} className="font-mono text-accent no-underline hover:underline">{b.element_key}</Link>{" "}
              <span className="break-words">{b.element_title}</span>
            </span>
            <span className="text-right font-mono tabular" title={t("budget.link.col.total")}>{mny(b.total_cents, cur)}</span>
            <span className="text-[12px] text-muted">
              v{b.version} · {kindLabel(b.kind)} · <span data-status={b.status}>{statusLabel(b.status)}</span>
            </span>
            <span className="text-right text-[12px] text-muted">
              {t("budget.link.col.committed")} <span className="font-mono tabular">{mny(b.committed_cents, cur)}</span>
            </span>
          </li>
        ))}
      </ul>
      <p className="m-0 text-[12.5px] text-muted">{t("budget.link.nodeTotal", { amount: mny(committed, cur), allocated: mny(node.allocated_cents, cur) })}</p>
    </div>
  );
}
