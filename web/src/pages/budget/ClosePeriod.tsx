// Chiusura guidata di un periodo (replica/budget.md §3): anteprima nodo per nodo di ciò che torna al padre,
// si riporta o decade; avviso sugli impegni aperti; conferma; annullamento finché il periodo successivo non ha ripartizioni.
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { budgetApi, type BudgetPeriod, type ClosureLine } from "../../api";
import { useT } from "../../i18n/react";
import { ErrorNote, Loading } from "../../ui";
import { mny } from "./budget-ui";

export function ClosePeriod({ period, cur, onClose }: { period: BudgetPeriod; cur: string; onClose: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const [approve, setApprove] = useState(false);
  const [result, setResult] = useState<{ ret: number; carry: number; lapse: number } | null>(null);
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["budget"] });
    qc.invalidateQueries({ queryKey: ["finance"] });
    qc.invalidateQueries({ queryKey: ["ranking"] });
  };
  const preview = useQuery({ queryKey: ["budget", "close-preview", period.id, period.status], queryFn: () => budgetApi.closePreview(period.id), enabled: period.status !== "closed" });
  const start = useMutation({ mutationFn: () => budgetApi.startClosing(period.id), onSuccess: refresh });
  const close = useMutation({
    mutationFn: () => budgetApi.closePeriod(period.id, approve),
    onSuccess: (c) => {
      setResult({ ret: c.return_cents, carry: c.carry_cents, lapse: c.lapse_cents });
      refresh();
    },
  });
  const p = preview.data;
  return (
    <section className="flex flex-col gap-3 border-l-2 border-accent pl-4" aria-label={t("budget.close.heading", { name: period.name })}>
      <div className="flex items-baseline gap-3">
        <h2 className="m-0 text-[17px] font-bold">{t("budget.close.heading", { name: period.name })}</h2>
        <button className="ml-auto text-[12px] text-muted underline hover:text-fg" onClick={onClose}>{t("common.close")}</button>
      </div>
      <p className="m-0 max-w-[75ch] text-[13px] text-muted">{t("budget.close.intro")}</p>
      {preview.isPending && period.status !== "closed" && <Loading rows={2} />}
      <ErrorNote error={preview.error ?? start.error ?? close.error} />
      {result && (
        <p role="status" className="m-0 font-semibold text-ok">
          {t("budget.close.done", { ret: mny(result.ret, cur), carry: mny(result.carry, cur), lapse: mny(result.lapse, cur) })}
        </p>
      )}
      {p && !result && (
        <>
          <dl className="m-0 flex flex-wrap gap-x-8 gap-y-2">
            {[
              [t("budget.close.return"), p.return_cents],
              [t("budget.close.carry"), p.carry_cents],
              [t("budget.close.lapse"), p.lapse_cents],
            ].map(([k, v]) => (
              <div key={k as string} className="flex flex-col">
                <dt className="label text-muted">{k}</dt>
                <dd className="m-0 font-mono text-[17px] font-semibold tabular">{mny(v as number, cur)}</dd>
              </div>
            ))}
          </dl>
          {p.open_commitments > 0 && <p className="m-0 font-semibold text-flag">{t("budget.close.openCommitments", { count: p.open_commitments })}</p>}
          {p.warnings.map((w, i) => <p key={i} className="m-0 text-[12.5px] text-flag">{w}</p>)}
          {p.next_period && <p className="m-0 text-[12.5px] text-muted">{t("budget.close.next", { name: p.next_period })}</p>}
          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-[12.5px] tabular">
              <caption className="sr-only">{t("budget.close.caption")}</caption>
              <thead>
                <tr className="label text-left">
                  <th scope="col" className="border-b-2 border-rule py-1 pr-2">{t("budget.col.node")}</th>
                  <th scope="col" className="hidden border-b-2 border-rule px-2 py-1 sm:table-cell">{t("budget.close.policy")}</th>
                  <th scope="col" className="hidden border-b-2 border-rule px-2 py-1 text-right md:table-cell">{t("budget.col.allocated")}</th>
                  <th scope="col" className="hidden border-b-2 border-rule px-2 py-1 text-right md:table-cell">{t("budget.col.committed")}</th>
                  <th scope="col" className="border-b-2 border-rule px-2 py-1 text-right">{t("budget.close.remainder")}</th>
                  <th scope="col" className="border-b-2 border-rule px-2 py-1 text-right">{t("budget.close.return")}</th>
                  <th scope="col" className="border-b-2 border-rule px-2 py-1 text-right">{t("budget.close.carry")}</th>
                  <th scope="col" className="border-b-2 border-rule py-1 pl-2 text-right">{t("budget.close.lapse")}</th>
                </tr>
              </thead>
              <tbody>
                {p.lines.map((l: ClosureLine) => (
                  <tr key={l.allocation_id} className="border-b border-hair align-top">
                    <td className="py-1 pr-2" style={{ paddingLeft: `${Math.min(l.depth, 6) * 14}px` }}>
                      {l.label}
                      {l.warnings.map((w, i) => <span key={i} className="block text-[11.5px] text-flag">{w}</span>)}
                    </td>
                    <td className="hidden px-2 py-1 sm:table-cell">
                      {t(`budget.policy.${l.policy}`)}
                      {l.policy_source === "node" && <span className="label ml-1 text-accent">{t("budget.close.nodePolicy")}</span>}
                    </td>
                    <td className="hidden px-2 py-1 text-right font-mono md:table-cell">{mny(l.allocated_cents, cur)}</td>
                    <td className="hidden px-2 py-1 text-right font-mono md:table-cell">{mny(l.committed_cents, cur)}</td>
                    <td className="px-2 py-1 text-right font-mono">{mny(l.remainder_cents, cur)}</td>
                    <td className="px-2 py-1 text-right font-mono">{l.return_cents ? mny(l.return_cents, cur) : "—"}</td>
                    <td className="px-2 py-1 text-right font-mono">{l.carry_cents ? mny(l.carry_cents, cur) : "—"}</td>
                    <td className="py-1 pl-2 text-right font-mono">{l.lapse_cents ? mny(l.lapse_cents, cur) : "—"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {period.status === "open" && (
            <div className="flex flex-col gap-2">
              <p className="m-0 text-[12.5px] text-muted">{t("budget.close.startHelp")}</p>
              <div><button className="btn btn-primary" disabled={start.isPending} onClick={() => start.mutate()}>{t("budget.close.start")}</button></div>
            </div>
          )}
          {period.status === "closing" && (
            <div className="flex flex-col gap-2">
              {p.needs_approval && (
                <label className="flex items-start gap-2 text-[13px]">
                  <input type="checkbox" className="mt-1" checked={approve} onChange={(e) => setApprove(e.target.checked)} />
                  {t("budget.close.approveCarry", { amount: mny(p.carry_cents, cur) })}
                </label>
              )}
              <div className="flex gap-2">
                <button className="btn btn-primary" disabled={close.isPending || !p.can_confirm || (p.needs_approval && !approve)} onClick={() => close.mutate()}>
                  {t("budget.close.confirm")}
                </button>
                <button className="btn" onClick={onClose}>{t("common.cancel")}</button>
              </div>
              {!p.can_confirm && <p className="m-0 text-[12.5px] text-danger">{t("budget.close.cannotConfirm")}</p>}
            </div>
          )}
        </>
      )}
    </section>
  );
}

/** Annulla l'ultima chiusura di un periodo chiuso. */
export function UndoClose({ period }: { period: BudgetPeriod }) {
  const t = useT();
  const qc = useQueryClient();
  const undo = useMutation({
    mutationFn: () => budgetApi.undoClose(period.id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["budget"] });
      qc.invalidateQueries({ queryKey: ["finance"] });
    },
  });
  return (
    <span className="flex flex-col gap-1">
      <button className="btn" disabled={undo.isPending} onClick={() => undo.mutate()}>{t("budget.close.undo")}</button>
      <ErrorNote error={undo.error} />
    </span>
  );
}
