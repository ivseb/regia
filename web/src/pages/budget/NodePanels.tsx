// Pannelli di un nodo dell'albero dei budget: storia dei movimenti, «Ripartisci» con anteprima,
// «Chiedi trasferimento», aggiunta di un figlio e dettagli (replica/budget.md §6).
import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  budgetApi,
  orgApi,
  type AllocationNode,
  type BudgetPeriod,
  type DistMode,
  type DistPreview,
  type DistTarget,
  type EndPolicy,
} from "../../api";
import { fmtDateTime, fmtNumber } from "../../i18n/format";
import { useT } from "../../i18n/react";
import { EntitySelect, OptionSelect } from "../../select";
import { ErrorNote, Loading } from "../../ui";
import { Field, fromInput, mny, signed, toInput } from "./budget-ui";

const MODES: DistMode[] = ["equal", "percent", "previous", "requests"];

/** Storia del nodo: ogni variazione è un movimento immutabile. */
export function History({ node, cur }: { node: AllocationNode; cur: string }) {
  const t = useT();
  const q = useQuery({ queryKey: ["budget", "movements", node.id], queryFn: () => budgetApi.movements(node.id) });
  if (q.isPending) return <Loading rows={2} />;
  return (
    <div className="flex flex-col gap-2">
      <ErrorNote error={q.error} />
      {q.data && q.data.items.length === 0 && <p className="m-0 text-muted">{t("budget.history.empty")}</p>}
      {q.data && q.data.items.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-[12.5px] tabular">
            <caption className="sr-only">{t("budget.history.caption", { name: node.label })}</caption>
            <thead>
              <tr className="label text-left">
                <th scope="col" className="border-b-2 border-rule py-1 pr-2">{t("budget.history.when")}</th>
                <th scope="col" className="border-b-2 border-rule px-2 py-1">{t("budget.history.kind")}</th>
                <th scope="col" className="border-b-2 border-rule px-2 py-1 text-right">{t("budget.history.amount")}</th>
                <th scope="col" className="border-b-2 border-rule px-2 py-1 text-right">{t("budget.history.balance")}</th>
                <th scope="col" className="hidden border-b-2 border-rule px-2 py-1 sm:table-cell">{t("budget.history.reason")}</th>
              </tr>
            </thead>
            <tbody>
              {[...q.data.items].reverse().map((m) => (
                <tr key={m.id} className="border-b border-hair align-top">
                  <td className="py-1 pr-2 whitespace-nowrap">
                    {fmtDateTime(m.created_at)}
                    {m.created_by && <span className="block text-[11px] text-muted">{m.created_by}</span>}
                  </td>
                  <td className="px-2 py-1">
                    {t(`budget.mov.${m.kind}`)}
                    {m.counterpart && <span className="block text-[11px] text-muted">{m.counterpart}</span>}
                  </td>
                  <td className={`px-2 py-1 text-right font-mono ${m.amount_cents < 0 ? "text-danger" : ""}`}>{signed(m.amount_cents, cur)}</td>
                  <td className="px-2 py-1 text-right font-mono">{mny(m.balance_cents, cur)}</td>
                  <td className="hidden px-2 py-1 text-muted sm:table-cell">{m.reason}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

type PctTarget = { key: string; label: string; target: DistTarget; percent: string };

/** «Ripartisci»: uguale, percentuali, come il periodo precedente, proporzionale alle richieste; sempre con anteprima. */
export function Distribute({ node, cur, onDone }: { node: AllocationNode; cur: string; onDone: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const [mode, setMode] = useState<DistMode>("equal");
  const [total, setTotal] = useState(toInput(node.allocated_cents));
  // Il pannello può aprirsi mentre l'albero si aggiorna (subito dopo una modifica in linea dello
  // stanziato): finché la persona non scrive un importo, segue lo stanziato del nodo.
  const [totalTouched, setTotalTouched] = useState(false);
  useEffect(() => {
    if (!totalTouched) setTotal(toInput(node.allocated_cents));
  }, [node.allocated_cents, totalTouched]);
  const [reason, setReason] = useState("");
  const [targets, setTargets] = useState<PctTarget[]>([]);
  const [prev, setPrev] = useState<DistPreview | null>(null);
  const [done, setDone] = useState<DistPreview | null>(null);

  const body = () => {
    const totalCents = fromInput(total);
    return {
      mode,
      total_cents: totalCents ?? undefined,
      reason: reason.trim() || undefined,
      targets: mode === "percent" ? targets.map((x) => ({ ...x.target, percent: Number(x.percent.replace(",", ".")) || 0 })) : undefined,
    };
  };
  // Cambiare qualcosa invalida l'anteprima: la conferma ricalcola, ma chi decide deve vedere ciò che conferma.
  const stale = () => {
    setPrev(null);
    setDone(null);
  };

  const seed = useMutation({
    mutationFn: () => budgetApi.distributePreview(node.id, { mode: "equal", total_cents: fromInput(total) ?? undefined }),
    onSuccess: (p) => {
      const n = Math.max(p.lines.length, 1);
      setTargets(
        p.lines.map((l, i) => ({
          key: l.allocation_id ?? l.org_unit_id ?? l.element_id ?? String(i),
          label: l.label,
          target: { allocation_id: l.allocation_id ?? undefined, org_unit_id: l.org_unit_id ?? undefined, element_id: l.element_id ?? undefined },
          percent: fmtNumber(Math.floor(10000 / n) / 100, { maximumFractionDigits: 2, useGrouping: false }),
        })),
      );
    },
  });
  useEffect(() => {
    if (mode === "percent" && targets.length === 0 && !seed.isPending && !seed.isError) seed.mutate();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mode]);

  const preview = useMutation({ mutationFn: () => budgetApi.distributePreview(node.id, body()), onSuccess: setPrev });
  const confirm = useMutation({
    mutationFn: () => budgetApi.distribute(node.id, body()),
    onSuccess: (r) => {
      setDone(r);
      setPrev(null);
      qc.invalidateQueries({ queryKey: ["budget"] });
      qc.invalidateQueries({ queryKey: ["finance"] });
      onDone();
    },
  });
  const pctSum = targets.reduce((s, x) => s + (Number(x.percent.replace(",", ".")) || 0), 0);

  return (
    <form className="flex flex-col gap-3" aria-label={t("budget.dist.heading", { name: node.label })} onSubmit={(e) => { e.preventDefault(); preview.mutate(); }}>
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-[14rem_10rem_minmax(0,1fr)] lg:items-end">
        <Field label={t("budget.dist.mode")}>
          <OptionSelect value={mode} onChange={(v) => { setMode(v as DistMode); stale(); }} options={MODES.map((m) => ({ value: m, label: t(`budget.dist.mode.${m}`) }))} />
        </Field>
        <Field label={t("budget.dist.total")}>
          <input className="field text-right tabular" inputMode="decimal" value={total} onChange={(e) => { setTotalTouched(true); setTotal(e.target.value); stale(); }} />
        </Field>
        <Field label={t("budget.dist.reason")} className="sm:col-span-2 lg:col-span-1">
          <input className="field" value={reason} onChange={(e) => { setReason(e.target.value); stale(); }} />
        </Field>
      </div>
      <p className="m-0 text-[12.5px] text-muted">{t(`budget.dist.help.${mode}`)}</p>
      {mode === "percent" && (
        <div className="flex flex-col gap-1.5" role="group" aria-label={t("budget.dist.percentages")}>
          {seed.isPending && <Loading rows={1} />}
          {targets.length === 0 && !seed.isPending && <p className="m-0 text-muted">{t("budget.dist.noTargets")}</p>}
          {targets.map((x, i) => (
            <div key={x.key} className="flex items-center gap-3">
              <label className="flex min-w-0 flex-1 items-center gap-3">
                <span className="min-w-0 flex-1 break-words">{x.label}</span>
                <input
                  className="field w-24 text-right tabular"
                  inputMode="decimal"
                  aria-label={t("budget.dist.percentOf", { name: x.label })}
                  value={x.percent}
                  onChange={(e) => { setTargets(targets.map((y, j) => (j === i ? { ...y, percent: e.target.value } : y))); stale(); }}
                />
                <span aria-hidden="true">%</span>
              </label>
            </div>
          ))}
          {targets.length > 0 && (
            <p className={`m-0 text-[12.5px] ${pctSum > 100.001 ? "font-semibold text-danger" : "text-muted"}`}>
              {t("budget.dist.percentSum", { sum: fmtNumber(pctSum, { maximumFractionDigits: 2 }) })}
            </p>
          )}
        </div>
      )}
      <div className="flex flex-wrap gap-2">
        <button className="btn" disabled={preview.isPending || (mode === "percent" && (targets.length === 0 || pctSum > 100.001))}>{t("budget.dist.preview")}</button>
        {prev && (
          <button type="button" className="btn btn-primary" disabled={!prev.valid || confirm.isPending} onClick={() => confirm.mutate()}>
            {t("budget.dist.confirm")}
          </button>
        )}
      </div>
      <ErrorNote error={preview.error ?? confirm.error} />
      {done && <p role="status" className="m-0 font-semibold text-ok">{t("budget.dist.done", { count: done.lines.length })}</p>}
      {prev && <PreviewTable p={prev} cur={cur} />}
    </form>
  );
}

function PreviewTable({ p, cur }: { p: DistPreview; cur: string }) {
  const t = useT();
  return (
    <div className="flex flex-col gap-2" aria-live="polite" data-preview={p.valid ? "valid" : "invalid"}>
      <p className={`m-0 font-semibold ${p.valid ? "" : "text-danger"}`}>
        {p.valid ? t("budget.dist.valid") : t("budget.dist.invalid", { amount: mny(p.exceeds_cents, cur) })}
      </p>
      {p.warnings.map((w, i) => (
        <p key={i} className="m-0 text-[12.5px] text-flag">{w}</p>
      ))}
      <div className="overflow-x-auto">
        <table className="w-full border-collapse text-[12.5px] tabular">
          <thead>
            <tr className="label text-left">
              <th scope="col" className="border-b-2 border-rule py-1 pr-2">{t("budget.dist.col.target")}</th>
              <th scope="col" className="border-b-2 border-rule px-2 py-1 text-right">{t("budget.dist.col.current")}</th>
              <th scope="col" className="border-b-2 border-rule px-2 py-1 text-right">{t("budget.dist.col.proposed")}</th>
              <th scope="col" className="border-b-2 border-rule px-2 py-1 text-right">{t("budget.dist.col.delta")}</th>
              <th scope="col" className="hidden border-b-2 border-rule px-2 py-1 text-right sm:table-cell">{t("budget.dist.col.committed")}</th>
            </tr>
          </thead>
          <tbody>
            {p.lines.map((l, i) => {
              const below = l.proposed_cents < l.committed_cents;
              return (
                <tr key={l.allocation_id ?? l.org_unit_id ?? l.element_id ?? i} className="border-b border-hair align-top">
                  <td className="py-1 pr-2">
                    {l.label} {l.new && <span className="label text-accent">{t("budget.dist.new")}</span>}
                    {l.warnings.map((w, j) => (
                      <span key={j} className="block text-[11.5px] text-flag">{w}</span>
                    ))}
                  </td>
                  <td className="px-2 py-1 text-right font-mono">{mny(l.current_cents, cur)}</td>
                  <td className={`px-2 py-1 text-right font-mono font-semibold ${below ? "text-danger" : ""}`}>{mny(l.proposed_cents, cur)}</td>
                  <td className={`px-2 py-1 text-right font-mono ${l.delta_cents < 0 ? "text-danger" : l.delta_cents > 0 ? "text-ok" : "text-muted"}`}>{signed(l.delta_cents, cur)}</td>
                  <td className="hidden px-2 py-1 text-right font-mono sm:table-cell">{mny(l.committed_cents, cur)}</td>
                </tr>
              );
            })}
          </tbody>
          <tfoot>
            <tr className="font-semibold">
              <th scope="row" className="py-1 pr-2 text-left">{t("budget.dist.after")}</th>
              <td className="px-2 py-1 text-right font-mono" colSpan={2}>
                {t("budget.dist.distributedAfter", { amount: mny(p.distributed_after, cur) })}
              </td>
              <td className={`px-2 py-1 text-right font-mono ${p.reserve_after < 0 ? "text-danger" : ""}`} colSpan={2}>
                {t("budget.dist.reserveAfter", { amount: mny(p.reserve_after, cur) })}
              </td>
            </tr>
          </tfoot>
        </table>
      </div>
    </div>
  );
}

/** «Chiedi trasferimento» dal nodo a un altro nodo dello stesso periodo. */
export function TransferForm({ node, items, cur, onDone }: { node: AllocationNode; items: AllocationNode[]; cur: string; onDone: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const [to, setTo] = useState("");
  const [amount, setAmount] = useState("");
  const [reason, setReason] = useState("");
  // Esclusi il nodo stesso, i suoi antenati e i suoi discendenti (il server lo rifiuta).
  const blocked = new Set<string>([node.id]);
  for (let id = node.parent_id; id; id = items.find((x) => x.id === id)?.parent_id ?? null) blocked.add(id);
  const start = items.findIndex((x) => x.id === node.id);
  for (let i = start + 1; i < items.length && items[i].depth > node.depth; i++) blocked.add(items[i].id);
  const options = items.filter((x) => !blocked.has(x.id) && x.kind !== "root" && !x.archived).map((x) => ({ value: x.id, label: x.label, detail: x.unit_code ?? undefined }));
  const available = Math.max(Math.min(node.reserve_cents, node.residual_cents), 0);
  const send = useMutation({
    mutationFn: () => budgetApi.requestTransfer({ from_allocation_id: node.id, to_allocation_id: to, amount_cents: fromInput(amount) ?? 0, reason }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["budget"] });
      qc.invalidateQueries({ queryKey: ["approvals"] });
      onDone();
    },
  });
  return (
    <form className="grid gap-3 sm:grid-cols-2 lg:grid-cols-[minmax(0,1.4fr)_10rem_minmax(0,2fr)_auto] lg:items-end" aria-label={t("budget.transfer.heading", { name: node.label })} onSubmit={(e) => { e.preventDefault(); send.mutate(); }}>
      <Field label={t("budget.transfer.to")}>
        <OptionSelect value={to} onChange={setTo} options={options} placeholder={t("budget.transfer.pick")} />
      </Field>
      <Field label={t("budget.transfer.amount")}>
        <input className="field text-right tabular" inputMode="decimal" required value={amount} onChange={(e) => setAmount(e.target.value)} />
      </Field>
      <Field label={t("budget.transfer.reason")}>
        <input className="field" required value={reason} onChange={(e) => setReason(e.target.value)} />
      </Field>
      <button className="btn btn-primary" disabled={send.isPending || !to || !reason.trim() || !amount.trim()}>{t("budget.transfer.send")}</button>
      <p className="m-0 text-[12.5px] text-muted sm:col-span-2 lg:col-span-4">{t("budget.transfer.available", { amount: mny(available, cur) })} {t("budget.transfer.help")}</p>
      <div className="sm:col-span-2 lg:col-span-4"><ErrorNote error={send.error} /></div>
    </form>
  );
}

/** Aggiunge un figlio al nodo: un'unità (o un elemento) con il suo stanziamento. */
export function AddChild({ node, items, cur, onDone }: { node: AllocationNode; items: AllocationNode[]; cur: string; onDone: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const [kind, setKind] = useState<"unit" | "element">("unit");
  const [unit, setUnit] = useState("");
  const [element, setElement] = useState("");
  const [nature, setNature] = useState("");
  const [amount, setAmount] = useState("");
  const [reason, setReason] = useState("");
  const units = useQuery({ queryKey: ["org", "units", false], queryFn: () => orgApi.units({}) });
  const taken = new Set(items.filter((x) => x.org_unit_id).map((x) => x.org_unit_id!));
  const options = (units.data?.items ?? []).filter((u) => !taken.has(u.id)).map((u) => ({ value: u.id, label: u.path, detail: u.code ?? undefined }));
  const reserve = node.reserve_cents;
  const cents = fromInput(amount) ?? 0;
  const add = useMutation({
    mutationFn: () =>
      budgetApi.createNode({
        period_id: node.period_id,
        parent_id: node.id,
        org_unit_id: kind === "unit" ? unit : undefined,
        element_id: kind === "element" ? element : undefined,
        nature: (nature || undefined) as "capex" | "opex" | undefined,
        amount_cents: cents,
        reason: reason.trim() || undefined,
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["budget"] });
      qc.invalidateQueries({ queryKey: ["finance"] });
      onDone();
    },
  });
  return (
    <form className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4 lg:items-end" aria-label={t("budget.add.heading", { name: node.label })} onSubmit={(e) => { e.preventDefault(); add.mutate(); }}>
      <Field label={t("budget.add.kind")}>
        <OptionSelect value={kind} onChange={(v) => setKind(v as "unit" | "element")} options={[{ value: "unit", label: t("budget.add.kind.unit") }, { value: "element", label: t("budget.add.kind.element") }]} />
      </Field>
      {kind === "unit" ? (
        <Field label={t("budget.add.unit")} className="lg:col-span-2">
          <OptionSelect value={unit} onChange={setUnit} options={options} placeholder={t("budget.add.pickUnit")} />
        </Field>
      ) : (
        <Field label={t("budget.add.element")} className="lg:col-span-2">
          <EntitySelect kind="element" value={element} onChange={setElement} />
        </Field>
      )}
      <Field label={t("budget.add.nature")}>
        <OptionSelect value={nature} onChange={setNature} noneLabel={t("budget.add.natureNone")} options={[{ value: "capex", label: "CAPEX" }, { value: "opex", label: "OPEX" }]} />
      </Field>
      <Field label={t("budget.add.amount")}>
        <input className="field text-right tabular" inputMode="decimal" required value={amount} onChange={(e) => setAmount(e.target.value)} />
      </Field>
      <Field label={t("budget.add.reason")} className="sm:col-span-2">
        <input className="field" value={reason} onChange={(e) => setReason(e.target.value)} />
      </Field>
      <div className="flex gap-2">
        <button className="btn btn-primary" disabled={add.isPending || (kind === "unit" ? !unit : !element) || !amount.trim()}>{t("budget.add.create")}</button>
      </div>
      <p className={`m-0 text-[12.5px] sm:col-span-2 lg:col-span-4 ${cents > reserve ? "font-semibold text-danger" : "text-muted"}`}>
        {t("budget.add.reserve", { amount: mny(reserve, cur), after: mny(reserve - cents, cur) })}
      </p>
      <div className="sm:col-span-2 lg:col-span-4"><ErrorNote error={add.error ?? units.error} /></div>
    </form>
  );
}

/** Dettagli del nodo: politica di fine periodo, nota, archiviazione. */
export function NodeDetails({ node, period, canFinance, onDone }: { node: AllocationNode; period: BudgetPeriod; canFinance: boolean; onDone: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const [policy, setPolicy] = useState<string>(node.end_policy ?? "");
  const [note, setNote] = useState(node.note ?? "");
  const save = useMutation({
    mutationFn: () => budgetApi.patchNode(node.id, { note, ...(canFinance ? { end_policy: policy as EndPolicy | "" } : {}) }),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["budget"] }); onDone(); },
  });
  const archive = useMutation({
    mutationFn: () => budgetApi.patchNode(node.id, { archived: !node.archived }),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["budget"] }); onDone(); },
  });
  return (
    <form className="grid gap-3 sm:grid-cols-2 lg:grid-cols-[14rem_minmax(0,1fr)_auto] lg:items-end" aria-label={t("budget.node.heading", { name: node.label })} onSubmit={(e) => { e.preventDefault(); save.mutate(); }}>
      {canFinance && (
        <Field label={t("budget.node.policy")}>
          <OptionSelect
            value={policy}
            onChange={setPolicy}
            noneLabel={t("budget.node.policyTenant")}
            options={(["return", "carry", "lapse"] as const).map((p) => ({ value: p, label: t(`budget.policy.${p}`) }))}
          />
        </Field>
      )}
      <Field label={t("budget.node.note")}>
        <input className="field" value={note} onChange={(e) => setNote(e.target.value)} />
      </Field>
      <div className="flex gap-2">
        <button className="btn btn-primary" disabled={save.isPending}>{t("budget.node.save")}</button>
        {node.kind !== "root" && period.status !== "closed" && (
          <button type="button" className="btn btn-danger" disabled={archive.isPending} onClick={() => archive.mutate()}>
            {node.archived ? t("budget.node.restore") : t("budget.node.archive")}
          </button>
        )}
      </div>
      <p className="m-0 text-[12px] text-muted sm:col-span-2 lg:col-span-3">{t("budget.node.archiveHelp")}</p>
      <div className="sm:col-span-2 lg:col-span-3"><ErrorNote error={save.error ?? archive.error} /></div>
    </form>
  );
}
