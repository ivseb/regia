// Nella scheda della richiesta: unità richiedente e periodo di competenza, con lo stanziamento da cui la richiesta
// attingerebbe, proposto dall'unità (replica/budget.md §6).
import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { api, budgetApi, orgApi, type ElementDetail } from "../../api";
import { useT } from "../../i18n/react";
import { EntitySelect, OptionSelect } from "../../select";
import { useMe } from "../../shell";
import { ErrorNote } from "../../ui";
import { useRefreshElement } from "../ElementSections";
import { Field, mny, useCurrency } from "./budget-ui";

export function RequestBudget({ detail }: { detail: ElementDetail }) {
  const t = useT();
  const cur = useCurrency();
  const d = detail.demand;
  const key = detail.element.key || detail.element.id;
  const refresh = useRefreshElement(key);
  const perms = useMe().data?.active_tenant?.permissions ?? [];
  const canEdit = perms.includes("*") || perms.includes("element.write");
  const periods = useQuery({ queryKey: ["budget", "periods"], queryFn: () => budgetApi.periods(), retry: false });
  const [err, setErr] = useState<unknown>(null);
  const save = useMutation({
    mutationFn: (p: { requesting_unit_id?: string; budget_period_id?: string }) => api.updateDemand(detail.element.id, p),
    onSuccess: () => { setErr(null); refresh(); },
    onError: setErr,
  });
  const unit = d?.requesting_unit_id ?? "";
  const period = d?.budget_period_id ?? "";
  const list = (periods.data?.items ?? []).filter((p) => p.status !== "closed" || p.id === period);
  const units = useQuery({ queryKey: ["org", "units", false], queryFn: () => orgApi.units({}), enabled: !!unit && !!period, retry: false });
  const tree = useQuery({ queryKey: ["budget", "tree", period], queryFn: () => budgetApi.tree(period), enabled: !!unit && !!period, retry: false });
  if (!d) return null;

  // L'unità o la prima unità sopra di lei che ha uno stanziamento nel periodo.
  const byUnit = new Map((tree.data?.items ?? []).filter((n) => n.org_unit_id).map((n) => [n.org_unit_id!, n]));
  const parentOf = new Map((units.data?.items ?? []).map((u) => [u.id, u.parent_id]));
  let proposed = undefined as ReturnType<typeof byUnit.get>;
  for (let u: string | null | undefined = unit; u && !proposed; u = parentOf.get(u)) proposed = byUnit.get(u);

  return (
    <section className="flex flex-col gap-2" aria-label={t("budget.request.title")}>
      <h4 className="label m-0 text-muted">{t("budget.request.title")}</h4>
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label={t("budget.request.unit")}>
          {canEdit ? (
            <EntitySelect kind="unit" value={unit} onChange={(v) => save.mutate({ requesting_unit_id: v })} noneLabel={t("budget.request.none")} hints={d.requesting_unit_id && d.requesting_unit ? { [d.requesting_unit_id]: d.requesting_unit } : undefined} />
          ) : (
            <span>{d.requesting_unit ?? "—"}</span>
          )}
        </Field>
        <Field label={t("budget.request.period")}>
          {canEdit ? (
            <OptionSelect value={period} onChange={(v) => save.mutate({ budget_period_id: v })} noneLabel={t("budget.request.none")} options={list.map((p) => ({ value: p.id, label: p.fiscal_year === p.name ? p.name : `${p.fiscal_year} · ${p.name}` }))} />
          ) : (
            <span>{d.budget_period ?? "—"}</span>
          )}
        </Field>
      </div>
      {unit && period && (
        <p className="m-0 text-[12.5px] text-muted" data-proposed={proposed ? "yes" : "no"}>
          {proposed
            ? t("budget.request.proposed", { name: proposed.label, amount: mny(proposed.residual_cents, cur) })
            : tree.isPending || units.isPending
              ? ""
              : t("budget.request.noAllocation")}
        </p>
      )}
      <ErrorNote error={err} />
    </section>
  );
}
