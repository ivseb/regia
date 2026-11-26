// Budget → Albero (replica/budget.md §6, passo 2): esercizio e periodo in testa, tabella ad albero con una barra
// per nodo e i numeri del §2, modifica in linea dell'importo, «Ripartisci» con anteprima, storia, trasferimenti
// e chiusura guidata. Sul telefono: carte con discesa nell'albero.
import { Fragment, useMemo, useState, type ReactNode } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { budgetApi, type AllocationNode, type BudgetPeriod, type BudgetTree as Tree } from "../../api";
import { fmtDate } from "../../i18n/format";
import { useT } from "../../i18n/react";
import { OptionSelect } from "../../select";
import { PageHeader, useMe } from "../../shell";
import { ErrorNote, Loading, SectionTitle } from "../../ui";
import { useMedia } from "../../views/ElementTable";
import { NodeBudgets } from "./BudgetSource";
import { AddChild, Distribute, History, NodeDetails, TransferForm } from "./NodePanels";
import { ClosePeriod, UndoClose } from "./ClosePeriod";
import { Field, NodeBar, PeriodStatusLabel, defaultPeriod, fromInput, mny, toInput, useCurrency } from "./budget-ui";

type Tab = "history" | "distribute" | "transfer" | "add" | "details" | "budgets";
type Edit = { id: string; cents: number | null };

export function BudgetTreePage({ period }: { period?: string }) {
  const t = useT();
  const navigate = useNavigate();
  const cur = useCurrency();
  const perms = useMe().data?.active_tenant?.permissions ?? [];
  const finance = perms.includes("*") || perms.includes("finance.write");
  const periods = useQuery({ queryKey: ["budget", "periods"], queryFn: () => budgetApi.periods() });
  const list = periods.data?.items ?? [];
  const current = list.find((p) => p.id === period) ?? defaultPeriod(list);
  const years = useMemo(() => {
    const seen = new Map<string, string>();
    for (const p of list) seen.set(p.fiscal_year_id, p.fiscal_year);
    return [...seen].map(([value, label]) => ({ value, label }));
  }, [list]);
  const year = current?.fiscal_year_id ?? "";
  const inYear = list.filter((p) => p.fiscal_year_id === year);
  const go = (id: string) => navigate({ to: "/budget", search: { periodo: id } });

  return (
    <>
      <PageHeader path={t("budget.tree.path")} title={t("budget.tree.title")} />
      <div className="flex flex-col gap-6 px-4 pb-10 md:px-7">
        <ErrorNote error={periods.error} />
        {periods.isPending && <Loading />}
        {periods.data && list.length === 0 && (
          <p className="m-0 max-w-[70ch] text-muted">
            {t("budget.tree.noPeriods")}{" "}
            {finance && <Link to="/finanza" className="text-accent underline">{t("budget.tree.toFinance")}</Link>}
          </p>
        )}
        {current && (
          <>
            <div className="flex flex-wrap items-end gap-x-6 gap-y-3">
              <Field label={t("budget.tree.year")} className="w-44">
                <OptionSelect value={year} onChange={(v) => { const first = list.find((p) => p.fiscal_year_id === v); if (first) go((defaultPeriod(list.filter((p) => p.fiscal_year_id === v)) ?? first).id); }} options={years} />
              </Field>
              <Field label={t("budget.tree.period")} className="w-56">
                <OptionSelect value={current.id} onChange={go} options={inYear.map((p) => ({ value: p.id, label: p.name, detail: t(`budget.period.status.${p.status}`) }))} />
              </Field>
              <PeriodFacts period={current} />
            </div>
            <PeriodBody key={current.id} period={current} cur={cur} finance={finance} />
          </>
        )}
      </div>
    </>
  );
}

function PeriodFacts({ period }: { period: BudgetPeriod }) {
  const t = useT();
  return (
    <p className="m-0 flex flex-wrap items-baseline gap-x-4 gap-y-1 text-[12.5px] text-muted">
      <PeriodStatusLabel status={period.status} />
      <span>{t("budget.tree.dates", { from: fmtDate(period.start_date), to: fmtDate(period.end_date) })}</span>
      {period.days_to_close != null && period.status !== "closed" && <span>{t("budget.tree.daysToClose", { count: period.days_to_close })}</span>}
    </p>
  );
}

function PeriodBody({ period, cur, finance }: { period: BudgetPeriod; cur: string; finance: boolean }) {
  const t = useT();
  const qc = useQueryClient();
  const phone = useMedia("(max-width: 767px)");
  const tree = useQuery({ queryKey: ["budget", "tree", period.id], queryFn: () => budgetApi.tree(period.id) });
  const items = tree.data?.items ?? [];
  const [selected, setSelected] = useState<{ id: string; tab: Tab } | null>(null);
  const [editing, setEditing] = useState<Edit | null>(null);
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const [focus, setFocus] = useState<string | null>(null);
  const [closing, setClosing] = useState(false);
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["budget"] });
    qc.invalidateQueries({ queryKey: ["finance"] });
    qc.invalidateQueries({ queryKey: ["ranking"] });
  };
  const open = useMutation({ mutationFn: () => budgetApi.openPeriod(period.id), onSuccess: refresh });
  const byId = useMemo(() => new Map(items.map((n) => [n.id, n])), [items]);
  const root = items.find((n) => n.kind === "root");
  const visible = useMemo(() => {
    const out: AllocationNode[] = [];
    let skip = -1;
    for (const n of items) {
      if (skip >= 0 && n.depth > skip) continue;
      skip = -1;
      out.push(n);
      if (collapsed.has(n.id) && n.children > 0) skip = n.depth;
    }
    return out;
  }, [items, collapsed]);
  const writable = period.status === "planning" || period.status === "open" || (period.status === "closing" && finance);
  const sel = selected ? byId.get(selected.id) : undefined;
  const pick = (n: AllocationNode, tab: Tab) => setSelected(selected?.id === n.id && selected.tab === tab ? null : { id: n.id, tab });

  /** Il pannello del nodo scelto, sotto la sua riga (o la sua carta). */
  const panel = (n: AllocationNode): ReactNode => {
    if (!selected || selected.id !== n.id) return null;
    const close = () => setSelected(null);
    const tabs: [Tab, string, boolean][] = [
      ["history", t("budget.tab.history"), true],
      ["distribute", t("budget.tab.distribute"), n.can_allocate && n.kind !== "element" && writable],
      ["transfer", t("budget.tab.transfer"), n.kind !== "root" && period.status !== "closed"],
      ["add", t("budget.tab.add"), n.can_allocate && n.kind !== "element" && writable],
      ["budgets", t("budget.tab.budgets"), n.kind === "element"],
      ["details", t("budget.tab.details"), n.can_edit || finance],
    ];
    return (
      <div className="flex flex-col gap-3 border-l-2 border-accent bg-sel/40 py-3 pr-2 pl-4" data-panel={selected.tab}>
        <div className="flex flex-wrap items-baseline gap-x-4 gap-y-1">
          <h3 className="m-0 text-[15px] font-bold">{n.label}</h3>
          <span role="tablist" aria-label={t("budget.tab.label")} className="flex flex-wrap gap-x-4 text-[12.5px]">
            {tabs.filter((x) => x[2]).map(([k, label]) => (
              <button key={k} role="tab" aria-selected={selected.tab === k} className={`touch-44 ${selected.tab === k ? "border-b-2 border-fg font-semibold" : "text-muted hover:text-fg"}`} onClick={() => setSelected({ id: n.id, tab: k })}>
                {label}
              </button>
            ))}
          </span>
          <button className="ml-auto text-[12px] text-muted underline hover:text-fg" onClick={close}>{t("common.close")}</button>
        </div>
        {selected.tab === "history" && <History node={n} cur={cur} />}
        {selected.tab === "distribute" && <Distribute node={n} cur={cur} onDone={() => undefined} />}
        {selected.tab === "transfer" && <TransferForm node={n} items={items} cur={cur} onDone={close} />}
        {selected.tab === "add" && <AddChild node={n} items={items} cur={cur} onDone={close} />}
        {selected.tab === "budgets" && <NodeBudgets node={n} cur={cur} />}
        {selected.tab === "details" && <NodeDetails node={n} period={period} canFinance={finance} onDone={close} />}
      </div>
    );
  };

  return (
    <>
      <div className="flex flex-wrap items-center gap-3">
        {finance && period.status === "planning" && <button className="btn" disabled={open.isPending} onClick={() => open.mutate()}>{t("budget.period.open")}</button>}
        {finance && (period.status === "open" || period.status === "closing") && (
          <button className="btn" aria-expanded={closing} onClick={() => setClosing((c) => !c)}>
            {period.status === "closing" ? t("budget.period.guided") : t("budget.period.close")}
          </button>
        )}
        {finance && period.status === "closed" && <UndoClose period={period} />}
        <Link to="/finanza" search={{ tab: "struttura" }} className="ml-auto text-[12.5px] text-accent underline">{t("budget.tree.settings")}</Link>
      </div>
      <ErrorNote error={open.error} />
      {closing && finance && <ClosePeriod period={period} cur={cur} onClose={() => setClosing(false)} />}

      {tree.isPending && <Loading />}
      <ErrorNote error={tree.error} />
      {tree.data && !root && items.length === 0 && (
        finance && writable ? <RootForm period={period} cur={cur} /> : <p className="m-0 max-w-[70ch] text-muted">{t("budget.tree.empty")}</p>
      )}
      {tree.data && root && finance && root.allocated_cents === 0 && root.children === 0 && writable && <RootForm period={period} cur={cur} existing />}
      {items.length > 0 && (
        <section aria-label={t("budget.tree.title")} className="flex flex-col gap-2">
          {phone ? (
            <Cards items={items} byId={byId} focus={focus} setFocus={setFocus} cur={cur} writable={writable} editing={editing} setEditing={setEditing} period={period} panel={panel} pick={pick} selected={selected} />
          ) : (
            <TreeTable
              visible={visible}
              byId={byId}
              cur={cur}
              writable={writable}
              editing={editing}
              setEditing={setEditing}
              collapsed={collapsed}
              toggle={(id) => setCollapsed((c) => { const n = new Set(c); if (n.has(id)) n.delete(id); else n.add(id); return n; })}
              period={period}
              panel={panel}
              pick={pick}
              selected={selected}
            />
          )}
          {sel === undefined && selected && <p className="m-0 text-muted">{t("budget.tree.nodeGone")}</p>}
          <Legend />
        </section>
      )}
      {items.length > 0 && <Transfers period={period} cur={cur} />}
    </>
  );
}

function Legend() {
  const t = useT();
  return (
    <p className="m-0 flex flex-wrap items-center gap-x-5 gap-y-1 text-[11.5px] text-muted">
      <span className="flex items-center gap-1.5"><span className="inline-block h-2 w-3 bg-fg" aria-hidden="true" />{t("budget.legend.spent")}</span>
      <span className="flex items-center gap-1.5"><span className="inline-block h-2 w-3 bg-muted" aria-hidden="true" />{t("budget.legend.committed")}</span>
      <span className="flex items-center gap-1.5"><span className="inline-block h-2 w-3 bg-hair" aria-hidden="true" />{t("budget.legend.free")}</span>
      <span className="flex items-center gap-1.5"><span className="inline-block h-2 w-3 bg-danger" aria-hidden="true" />{t("budget.legend.over")}</span>
    </p>
  );
}

/** Plafond del periodo: la radice dell'albero. */
function RootForm({ period, cur, existing }: { period: BudgetPeriod; cur: string; existing?: boolean }) {
  const t = useT();
  const qc = useQueryClient();
  const [amount, setAmount] = useState("");
  const save = useMutation({
    mutationFn: () => budgetApi.setRoot(period.id, fromInput(amount) ?? 0, undefined),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["budget"] }); qc.invalidateQueries({ queryKey: ["finance"] }); setAmount(""); },
  });
  return (
    <form className="flex flex-wrap items-end gap-3" aria-label={t("budget.root.heading")} onSubmit={(e) => { e.preventDefault(); save.mutate(); }}>
      <p className="m-0 basis-full text-[13px] text-muted">{existing ? t("budget.root.zero") : t("budget.root.none", { name: period.name })}</p>
      <Field label={t("budget.root.amount", { currency: cur })}>
        <input className="field w-40 text-right tabular" inputMode="decimal" required value={amount} onChange={(e) => setAmount(e.target.value)} />
      </Field>
      <button className="btn btn-primary" disabled={save.isPending || !amount.trim()}>{t("budget.root.save")}</button>
      <ErrorNote error={save.error} />
    </form>
  );
}

// ---------------------------------------------------------------------
// Modifica in linea dell'importo

function AmountEdit({ node, parent, cur, editing, setEditing, period }: { node: AllocationNode; parent?: AllocationNode; cur: string; editing: Edit | null; setEditing: (e: Edit | null) => void; period: BudgetPeriod }) {
  const t = useT();
  const qc = useQueryClient();
  const mine = editing?.id === node.id;
  const [text, setText] = useState("");
  const save = useMutation({
    mutationFn: (cents: number) => (node.kind === "root" ? budgetApi.setRoot(period.id, cents).then((n) => ({ node: n, parent: null })) : budgetApi.setAmount(node.id, cents)),
    onSuccess: (r) => {
      // La riserva del padre si aggiorna subito, senza aspettare la ricarica.
      qc.setQueryData<Tree>(["budget", "tree", period.id], (old) =>
        old ? { ...old, items: old.items.map((x) => (x.id === r.node.id ? { ...x, ...r.node } : r.parent && x.id === r.parent.id ? { ...x, ...r.parent } : x)) } : old,
      );
      setEditing(null);
      qc.invalidateQueries({ queryKey: ["budget"] });
      qc.invalidateQueries({ queryKey: ["finance"] });
    },
  });
  const canEdit = node.can_edit && (period.status === "planning" || period.status === "open" || period.status === "closing");
  if (!canEdit) return <span className="font-mono">{mny(node.allocated_cents, cur)}</span>;
  if (!mine) {
    return (
      <button
        className="touch-44 -mx-1 px-1 text-right font-mono underline decoration-dotted underline-offset-4 hover:bg-sel"
        aria-label={t("budget.edit.start", { name: node.label, amount: mny(node.allocated_cents, cur) })}
        onClick={() => { setText(toInput(node.allocated_cents)); setEditing({ id: node.id, cents: node.allocated_cents }); save.reset(); }}
      >
        {mny(node.allocated_cents, cur)}
      </button>
    );
  }
  const cents = fromInput(text);
  const reserveAfter = parent && cents != null ? parent.reserve_cents + node.allocated_cents - cents : null;
  const belowChildren = cents != null && cents < node.distributed_cents;
  return (
    <form className="flex flex-col items-end gap-1" onSubmit={(e) => { e.preventDefault(); if (cents != null) save.mutate(cents); }}>
      <input
        className="field w-32 text-right tabular"
        inputMode="decimal"
        autoFocus
        aria-label={t("budget.edit.amount", { name: node.label })}
        value={text}
        onChange={(e) => { setText(e.target.value); setEditing({ id: node.id, cents: fromInput(e.target.value) }); }}
        onKeyDown={(e) => { if (e.key === "Escape") setEditing(null); }}
      />
      {reserveAfter != null && (
        <span className={`text-[11.5px] ${reserveAfter < 0 ? "font-semibold text-danger" : "text-muted"}`}>
          {t("budget.edit.parentReserve", { name: parent!.label, amount: mny(reserveAfter, cur) })}
        </span>
      )}
      {belowChildren && <span className="text-[11.5px] font-semibold text-danger">{t("budget.edit.belowChildren", { amount: mny(node.distributed_cents, cur) })}</span>}
      <span className="flex gap-2">
        <button className="btn btn-primary py-1" disabled={save.isPending || cents == null}>{t("budget.edit.save")}</button>
        <button type="button" className="btn py-1" onClick={() => setEditing(null)}>{t("common.cancel")}</button>
      </span>
      <ErrorNote error={save.error} />
    </form>
  );
}

// ---------------------------------------------------------------------
// Tabella (desktop)

type Common = {
  cur: string;
  writable: boolean;
  editing: Edit | null;
  setEditing: (e: Edit | null) => void;
  period: BudgetPeriod;
  panel: (n: AllocationNode) => ReactNode;
  pick: (n: AllocationNode, tab: Tab) => void;
  selected: { id: string; tab: Tab } | null;
  byId: Map<string, AllocationNode>;
};

function TreeTable({ visible, collapsed, toggle, ...c }: Common & { visible: AllocationNode[]; collapsed: Set<string>; toggle: (id: string) => void }) {
  const t = useT();
  const th = "border-b-2 border-rule px-2 py-1.5 text-right";
  return (
    <div className="overflow-x-auto">
      <table className="w-full border-collapse text-[13px] tabular" role="treegrid">
        <caption className="sr-only">{t("budget.tree.caption")}</caption>
        <thead>
          <tr className="label">
            <th scope="col" className="border-b-2 border-rule py-1.5 pr-2 text-left">{t("budget.col.node")}</th>
            <th scope="col" className="w-32 border-b-2 border-rule px-2 py-1.5 text-left">{t("budget.col.bar")}</th>
            <th scope="col" className={th}>{t("budget.col.allocated")}</th>
            <th scope="col" className={`${th} hidden lg:table-cell`}>{t("budget.col.distributed")}</th>
            <th scope="col" className={th}>{t("budget.col.reserve")}</th>
            <th scope="col" className={th}>{t("budget.col.committed")}</th>
            <th scope="col" className={`${th} hidden lg:table-cell`}>{t("budget.col.spent")}</th>
            <th scope="col" className={th}>{t("budget.col.residual")}</th>
            <th scope="col" className={`${th} hidden xl:table-cell`}>{t("budget.col.forecast")}</th>
            <th scope="col" className="border-b-2 border-rule py-1.5 pl-2 text-right"><span className="sr-only">{t("budget.col.actions")}</span></th>
          </tr>
        </thead>
        <tbody>
          {visible.map((n) => {
            const parent = n.parent_id ? c.byId.get(n.parent_id) : undefined;
            const preview = c.editing && c.editing.id !== n.id && c.byId.get(c.editing.id)?.parent_id === n.id && c.editing.cents != null
              ? n.reserve_cents + c.byId.get(c.editing.id)!.allocated_cents - c.editing.cents
              : null;
            const reserve = preview ?? n.reserve_cents;
            const isSel = c.selected?.id === n.id;
            return (
              <Fragment key={n.id}>
                <tr data-node={n.label} className={`border-b border-hair align-middle ${isSel ? "bg-sel" : ""} ${n.archived ? "text-muted" : ""}`}>
                  <td className="py-1.5 pr-2" style={{ paddingLeft: `${Math.min(n.depth, 8) * 18}px` }}>
                    <span className="flex items-center gap-1">
                      {n.children > 0 ? (
                        <button className="touch-44 flex size-6 shrink-0 items-center justify-center text-muted hover:text-fg" aria-label={collapsed.has(n.id) ? t("budget.tree.expand", { name: n.label }) : t("budget.tree.collapse", { name: n.label })} aria-expanded={!collapsed.has(n.id)} onClick={() => toggle(n.id)}>
                          <span aria-hidden="true">{collapsed.has(n.id) ? "▸" : "▾"}</span>
                        </button>
                      ) : (
                        <span className="size-6 shrink-0" aria-hidden="true" />
                      )}
                      <span className="flex min-w-0 flex-col">
                        <span className="flex flex-wrap items-baseline gap-x-2">
                          <span className={n.kind === "root" ? "font-bold" : "font-medium"}>{n.label}</span>
                          {n.unit_code && <span className="font-mono text-[11px] text-accent">{n.unit_code}</span>}
                          {n.nature && <span className="font-mono text-[10.5px] text-muted uppercase">{n.nature}</span>}
                          {n.kind === "element" && <span className="label text-muted">{t("budget.node.element")}</span>}
                        </span>
                        {n.manager_name && <span className="text-[11.5px] text-muted">{n.manager_name}</span>}
                      </span>
                    </span>
                  </td>
                  <td className="px-2"><NodeBar n={n} /></td>
                  <td className="px-2 text-right"><AmountEdit node={n} parent={parent} cur={c.cur} editing={c.editing} setEditing={c.setEditing} period={c.period} /></td>
                  <td className="hidden px-2 text-right font-mono lg:table-cell">{mny(n.distributed_cents, c.cur)}</td>
                  <td className={`px-2 text-right font-mono ${reserve < 0 ? "font-semibold text-danger" : ""} ${preview != null ? "bg-sel" : ""}`} data-col="reserve">{n.kind === "element" ? "—" : mny(reserve, c.cur)}</td>
                  <td className="px-2 text-right font-mono">{mny(n.committed_cents, c.cur)}</td>
                  <td className="hidden px-2 text-right font-mono lg:table-cell">{mny(n.spent_cents, c.cur)}</td>
                  <td className={`px-2 text-right font-mono ${n.residual_cents < 0 ? "font-semibold text-danger" : ""}`}>{mny(n.residual_cents, c.cur)}</td>
                  <td className={`hidden px-2 text-right font-mono xl:table-cell ${n.forecast_cents > n.allocated_cents ? "text-flag" : ""}`}>{mny(n.forecast_cents, c.cur)}</td>
                  <td className="py-1 pl-2 text-right">
                    <span className="flex flex-wrap justify-end gap-x-3 text-[12.5px]">
                      <button className="touch-44 text-accent underline" aria-label={t("budget.act.historyOf", { name: n.label })} onClick={() => c.pick(n, "history")}>{t("budget.act.history")}</button>
                      {n.can_allocate && n.kind !== "element" && c.writable && (
                        <button className="touch-44 text-accent underline" aria-label={t("budget.act.distributeOf", { name: n.label })} onClick={() => c.pick(n, "distribute")}>{t("budget.act.distribute")}</button>
                      )}
                      {n.kind !== "root" && c.period.status !== "closed" && (
                        <button className="touch-44 text-accent underline" aria-label={t("budget.act.transferOf", { name: n.label })} onClick={() => c.pick(n, "transfer")}>{t("budget.act.transfer")}</button>
                      )}
                      <button className="touch-44 text-muted underline hover:text-fg" aria-label={t("budget.act.moreOf", { name: n.label })} onClick={() => c.pick(n, n.can_allocate && n.kind !== "element" && c.writable ? "add" : "details")}>{t("budget.act.more")}</button>
                    </span>
                  </td>
                </tr>
                {isSel && (
                  <tr>
                    <td colSpan={10} className="px-0 py-2">{c.panel(n)}</td>
                  </tr>
                )}
              </Fragment>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}


// ---------------------------------------------------------------------
// Carte (telefono): una per nodo, con discesa nei figli

function Cards({ items, focus, setFocus, ...c }: Omit<Common, "byId"> & { items: AllocationNode[]; byId: Map<string, AllocationNode>; focus: string | null; setFocus: (id: string | null) => void }) {
  const t = useT();
  const cur = c.cur;
  const here = focus ? c.byId.get(focus) : undefined;
  const chain: AllocationNode[] = [];
  for (let n = here; n; n = n.parent_id ? c.byId.get(n.parent_id) : undefined) chain.unshift(n);
  const shown = focus ? items.filter((n) => n.parent_id === focus) : items.filter((n) => !n.parent_id || !c.byId.has(n.parent_id));
  const card = (n: AllocationNode, head: boolean) => {
    const parent = n.parent_id ? c.byId.get(n.parent_id) : undefined;
    return (
      <li key={n.id} data-node={n.label} className={`flex flex-col gap-2 border border-hair p-3 ${head ? "border-rule" : ""} ${n.archived ? "text-muted" : ""}`}>
        <div className="flex items-baseline gap-2">
          <span className="font-semibold">{n.label}</span>
          {n.unit_code && <span className="font-mono text-[11px] text-accent">{n.unit_code}</span>}
          {n.manager_name && <span className="ml-auto min-w-0 text-[11.5px] break-words text-muted">{n.manager_name}</span>}
        </div>
        <NodeBar n={n} />
        <dl className="m-0 grid grid-cols-2 gap-x-4 gap-y-1 text-[12.5px]">
          <div className="flex justify-between gap-2"><dt className="text-muted">{t("budget.col.allocated")}</dt><dd className="m-0 font-mono"><AmountEdit node={n} parent={parent} cur={cur} editing={c.editing} setEditing={c.setEditing} period={c.period} /></dd></div>
          <div className="flex justify-between gap-2"><dt className="text-muted">{t("budget.col.committed")}</dt><dd className="m-0 font-mono">{mny(n.committed_cents, cur)}</dd></div>
          <div className="flex justify-between gap-2"><dt className="text-muted">{t("budget.col.residual")}</dt><dd className={`m-0 font-mono ${n.residual_cents < 0 ? "font-semibold text-danger" : ""}`}>{mny(n.residual_cents, cur)}</dd></div>
          <div className="flex justify-between gap-2"><dt className="text-muted">{t("budget.col.reserve")}</dt><dd className="m-0 font-mono">{n.kind === "element" ? "—" : mny(n.reserve_cents, cur)}</dd></div>
          <div className="flex justify-between gap-2"><dt className="text-muted">{t("budget.col.spent")}</dt><dd className="m-0 font-mono">{mny(n.spent_cents, cur)}</dd></div>
          <div className="flex justify-between gap-2"><dt className="text-muted">{t("budget.col.forecast")}</dt><dd className="m-0 font-mono">{mny(n.forecast_cents, cur)}</dd></div>
        </dl>
        <div className="flex flex-wrap gap-x-4 text-[13px]">
          {n.children > 0 && !head && <button className="touch-44 font-semibold text-accent underline" onClick={() => setFocus(n.id)}>{t("budget.cards.down", { count: n.children })}</button>}
          <button className="touch-44 text-accent underline" aria-label={t("budget.act.historyOf", { name: n.label })} onClick={() => c.pick(n, "history")}>{t("budget.act.history")}</button>
          {n.can_allocate && n.kind !== "element" && c.writable && (
            <button className="touch-44 text-accent underline" aria-label={t("budget.act.distributeOf", { name: n.label })} onClick={() => c.pick(n, "distribute")}>{t("budget.act.distribute")}</button>
          )}
          {n.kind !== "root" && c.period.status !== "closed" && (
            <button className="touch-44 text-accent underline" aria-label={t("budget.act.transferOf", { name: n.label })} onClick={() => c.pick(n, "transfer")}>{t("budget.act.transfer")}</button>
          )}
          <button className="touch-44 text-muted underline" aria-label={t("budget.act.moreOf", { name: n.label })} onClick={() => c.pick(n, n.can_allocate && n.kind !== "element" && c.writable ? "add" : "details")}>{t("budget.act.more")}</button>
        </div>
        {c.panel(n)}
      </li>
    );
  };
  return (
    <div className="flex flex-col gap-3">
      {here && (
        <nav aria-label={t("budget.cards.path")} className="flex flex-wrap items-center gap-x-1 text-[12.5px]">
          <button className="touch-44 text-accent underline" onClick={() => setFocus(null)}>{t("budget.cards.top")}</button>
          {chain.map((n) => (
            <span key={n.id} className="flex items-center gap-1">
              <span aria-hidden="true">›</span>
              {n.id === focus ? <span className="font-semibold">{n.label}</span> : <button className="touch-44 text-accent underline" onClick={() => setFocus(n.id)}>{n.label}</button>}
            </span>
          ))}
          <button className="touch-44 ml-auto text-accent underline" onClick={() => setFocus(here.parent_id)}>{t("budget.cards.up")}</button>
        </nav>
      )}
      <ul className="m-0 flex list-none flex-col gap-3 p-0">
        {here && card(here, true)}
        {shown.map((n) => card(n, false))}
      </ul>
    </div>
  );
}

// ---------------------------------------------------------------------
// Trasferimenti del periodo

function Transfers({ period, cur }: { period: BudgetPeriod; cur: string }) {
  const t = useT();
  const q = useQuery({ queryKey: ["budget", "transfers", period.id], queryFn: () => budgetApi.transfers(period.id) });
  const items = q.data?.items ?? [];
  if (items.length === 0 && !q.error) return null;
  return (
    <section className="flex flex-col gap-2" aria-label={t("budget.transfers.title")}>
      <SectionTitle>{t("budget.transfers.title")}</SectionTitle>
      <ErrorNote error={q.error} />
      <div className="overflow-x-auto">
        <table className="w-full border-collapse text-[12.5px] tabular">
          <thead>
            <tr className="label text-left">
              <th scope="col" className="border-b-2 border-rule py-1 pr-2">{t("budget.transfers.from")}</th>
              <th scope="col" className="border-b-2 border-rule px-2 py-1">{t("budget.transfers.to")}</th>
              <th scope="col" className="border-b-2 border-rule px-2 py-1 text-right">{t("budget.transfers.amount")}</th>
              <th scope="col" className="border-b-2 border-rule px-2 py-1">{t("budget.transfers.status")}</th>
              <th scope="col" className="hidden border-b-2 border-rule px-2 py-1 md:table-cell">{t("budget.transfers.reason")}</th>
            </tr>
          </thead>
          <tbody>
            {items.map((x) => (
              <tr key={x.id} className="border-b border-hair">
                <td className="py-1 pr-2">{x.from}</td>
                <td className="px-2 py-1">{x.to}</td>
                <td className="px-2 py-1 text-right font-mono">{mny(x.amount_cents, cur)}</td>
                <td className={`px-2 py-1 ${x.status === "pending" ? "text-accent" : x.status === "rejected" ? "text-muted" : ""}`}>
                  {t(`budget.transfers.st.${x.status}`)}
                  {x.status === "pending" && x.approval_kind !== "none" && <span className="block text-[11px] text-muted">{t(`budget.transfers.by.${x.approval_kind}`)}</span>}
                </td>
                <td className="hidden px-2 py-1 text-muted md:table-cell">{x.reason}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}
