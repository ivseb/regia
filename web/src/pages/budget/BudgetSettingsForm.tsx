// Impostazioni del budget gerarchico (replica/budget.md §7), nella configurazione della finanza:
// granularità dei periodi, politica di fine periodo, soglia dei trasferimenti, ripartizione libera, giorni di avviso.
import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { budgetApi, type BudgetGranularity, type BudgetSettings, type EndPolicy } from "../../api";
import { useT } from "../../i18n/react";
import { OptionSelect } from "../../select";
import { ErrorNote, SectionTitle } from "../../ui";
import { Field, fromInput, toInput } from "./budget-ui";

const GRAN: BudgetGranularity[] = ["year", "semester", "quarter", "month"];
const POLICIES: EndPolicy[] = ["return", "carry", "lapse"];
const BODIES: BudgetSettings["transfer_body_kind"][] = ["investment_committee", "steering", "architecture_board", "cab", "other"];

export function BudgetSettingsForm({ canWrite, currency }: { canWrite: boolean; currency: string }) {
  const t = useT();
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["budget", "settings"], queryFn: budgetApi.settings });
  const s = q.data;
  const [gran, setGran] = useState<BudgetGranularity>("year");
  const [policy, setPolicy] = useState<EndPolicy>("return");
  const [threshold, setThreshold] = useState("");
  const [body, setBody] = useState<BudgetSettings["transfer_body_kind"]>("investment_committee");
  const [free, setFree] = useState(true);
  const [warn, setWarn] = useState("30");
  useEffect(() => {
    if (!s) return;
    setGran(s.period_granularity);
    setPolicy(s.end_policy);
    setThreshold(toInput(s.transfer_threshold_cents));
    setBody(s.transfer_body_kind);
    setFree(s.free_allocation);
    setWarn(String(s.warn_days));
  }, [s]);
  const save = useMutation({
    mutationFn: () => {
      const cents = fromInput(threshold);
      return budgetApi.saveSettings({
        period_granularity: gran,
        end_policy: policy,
        transfer_body_kind: body,
        free_allocation: free,
        warn_days: Math.max(0, Math.min(365, Number(warn) || 0)),
        ...(cents == null ? { clear_threshold: true } : { transfer_threshold_cents: cents }),
      });
    },
    onSuccess: (r) => {
      qc.setQueryData(["budget", "settings"], r);
      qc.invalidateQueries({ queryKey: ["finance"] });
    },
  });
  const dirty = !!s && (gran !== s.period_granularity || policy !== s.end_policy || body !== s.transfer_body_kind || free !== s.free_allocation || warn !== String(s.warn_days) || fromInput(threshold) !== s.transfer_threshold_cents);
  return (
    <section className="flex flex-col gap-3" aria-label={t("budget.settings.title")}>
      <SectionTitle>{t("budget.settings.title")}</SectionTitle>
      <p className="m-0 max-w-[70ch] text-[12.5px] text-muted">{t("budget.settings.intro")}</p>
      <ErrorNote error={q.error} />
      {s && (
        <form className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3 lg:items-start" onSubmit={(e) => { e.preventDefault(); save.mutate(); }}>
          <Field label={t("budget.settings.granularity")}>
            <OptionSelect disabled={!canWrite} value={gran} onChange={(v) => setGran(v as BudgetGranularity)} options={GRAN.map((g) => ({ value: g, label: t(`budget.settings.gran.${g}`) }))} />
            <span className="text-[11.5px] text-muted">{t("budget.settings.granularityHelp")}</span>
          </Field>
          <Field label={t("budget.settings.endPolicy")}>
            <OptionSelect disabled={!canWrite} value={policy} onChange={(v) => setPolicy(v as EndPolicy)} options={POLICIES.map((p) => ({ value: p, label: t(`budget.policy.${p}`) }))} />
            <span className="text-[11.5px] text-muted">{t(`budget.policy.help.${policy}`)}</span>
          </Field>
          <Field label={t("budget.settings.warnDays")}>
            <input className="field w-28 text-right tabular" type="number" min={0} max={365} disabled={!canWrite} value={warn} onChange={(e) => setWarn(e.target.value)} />
            <span className="text-[11.5px] text-muted">{t("budget.settings.warnDaysHelp")}</span>
          </Field>
          <Field label={t("budget.settings.threshold", { currency })}>
            <input className="field w-40 text-right tabular" inputMode="decimal" disabled={!canWrite} placeholder={t("budget.settings.thresholdNone")} value={threshold} onChange={(e) => setThreshold(e.target.value)} />
            <span className="text-[11.5px] text-muted">{t("budget.settings.thresholdHelp")}</span>
          </Field>
          <Field label={t("budget.settings.body")}>
            <OptionSelect disabled={!canWrite} value={body} onChange={(v) => setBody(v as BudgetSettings["transfer_body_kind"])} options={BODIES.map((b) => ({ value: b, label: t(`budget.settings.bodyKind.${b}`) }))} />
          </Field>
          <label className="flex items-start gap-2 text-[13px] sm:col-span-2 lg:col-span-1">
            <input type="checkbox" className="mt-1" disabled={!canWrite} checked={free} onChange={(e) => setFree(e.target.checked)} />
            <span>
              {t("budget.settings.free")}
              <span className="block text-[11.5px] text-muted">{t("budget.settings.freeHelp")}</span>
            </span>
          </label>
          {canWrite && (
            <div className="flex items-center gap-3 sm:col-span-2 lg:col-span-3">
              <button className="btn btn-primary" disabled={save.isPending || !dirty}>{t("budget.settings.save")}</button>
              {save.isSuccess && !dirty && <span role="status" className="text-[12.5px] text-ok">{t("budget.settings.saved")}</span>}
            </div>
          )}
          <div className="sm:col-span-2 lg:col-span-3"><ErrorNote error={save.error} /></div>
        </form>
      )}
    </section>
  );
}
