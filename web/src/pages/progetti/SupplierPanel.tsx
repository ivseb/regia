// Progetti, passo 4: la parte «fornitore» di un work package. Contratto e contatto, interruttore
// «il fornitore aggiorna da sé», SAL (dichiarati dal contatto o dal PM, accettati o respinti dal PM)
// e deliverable (creati dal PM, consegnati da chi dichiara, accettati o respinti dal PM).
// Il server calcola l'avanzamento e dice chi può fare cosa (can_edit, can_declare): qui si mostra.
import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { EntitySelect, Select } from "../../select";
import { ErrorNote, Loading, SectionTitle, date } from "../../ui";
import { fmtCurrency } from "../../i18n/format";
import { useT } from "../../i18n/react";
import { useTypeNames } from "../../typesApi";
import { projectKeys, projectsApi, type Claim, type Deliverable, type SupplierView } from "../../projectsApi";
import { useClaimName } from "./shared";

type Run = (fn: () => Promise<unknown>) => Promise<void>;

export function SupplierPanel({ itemId, projectKey }: { itemId: string; projectKey: string }) {
  const t = useT();
  const qc = useQueryClient();
  const q = useQuery({ queryKey: projectKeys.supplier(itemId), queryFn: () => projectsApi.supplier(itemId) });
  const [err, setErr] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  if (q.error) return <ErrorNote error={q.error} />;
  if (!q.data) return <Loading className="text-muted" />;
  const v = q.data;

  /** Dopo ogni mutazione: la vista del fornitore, la voce e i numeri del piano e della panoramica. */
  const run: Run = async (fn) => {
    setBusy(true);
    setErr(null);
    try {
      const next = await fn();
      if (next) qc.setQueryData(projectKeys.supplier(itemId), next);
      await Promise.all([
        qc.invalidateQueries({ queryKey: projectKeys.supplier(itemId) }),
        qc.invalidateQueries({ queryKey: projectKeys.item(itemId) }),
        qc.invalidateQueries({ queryKey: projectKeys.item(projectKey) }),
        qc.invalidateQueries({ queryKey: ["plan-item"] }),
        qc.invalidateQueries({ queryKey: ["project-plan"] }),
        qc.invalidateQueries({ queryKey: ["project-overview"] }),
        qc.invalidateQueries({ queryKey: ["element"] }),
      ]);
    } catch (e) {
      setErr(e);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex flex-col gap-8">
      <section className="flex flex-col gap-3" aria-label={t("progetti.fornitori.title")} data-testid="fornitore-contratto">
        <SectionTitle>{t("progetti.fornitori.title")}</SectionTitle>
        <Contract v={v} busy={busy} run={run} />
      </section>
      <Claims v={v} busy={busy} run={run} itemId={itemId} />
      <Deliverables v={v} busy={busy} run={run} itemId={itemId} projectKey={projectKey} />
      <ErrorNote error={err} />
    </div>
  );
}

const money = (cents: number | null, currency: string) => (cents == null ? "—" : fmtCurrency(cents / 100, currency));

function Contract({ v, busy, run }: { v: SupplierView; busy: boolean; run: Run }) {
  const t = useT();
  const edit = v.can_edit;
  // Il modulo modifica i dati scritti sul work package; quelli del contratto collegato fanno da segnaposto.
  const own = v.contract.own_amount_cents;
  const [ref, setRef] = useState(v.contract.own_ref ?? "");
  const [amount, setAmount] = useState(own == null ? "" : String(own / 100));
  const [currency, setCurrency] = useState(v.contract.currency || "EUR");
  useEffect(() => {
    setRef(v.contract.own_ref ?? "");
    setAmount(own == null ? "" : String(own / 100));
    setCurrency(v.contract.currency || "EUR");
  }, [v.contract.own_ref, own, v.contract.currency]);
  const linked = v.contract.linked;
  const n = amount.trim() === "" ? null : Number(amount.replace(",", "."));
  const amountOk = n == null || (Number.isFinite(n) && n >= 0);
  const contact = v.contacts[0] ?? null;
  const save = () => {
    const p: Parameters<typeof projectsApi.updateSupplier>[1] = { contract_ref: ref.trim() };
    if (v.amounts_visible) {
      p.contract_currency = currency.trim().toUpperCase() || "EUR";
      if (n == null) p.clear_amount = true;
      else p.contract_amount_cents = Math.round(n * 100);
    }
    void run(() => projectsApi.updateSupplier(v.item_id, p));
  };
  return (
    <div className="flex flex-col gap-3">
      <LinkedContractBlock v={v} busy={busy} run={run} />
      {edit ? (
        <form className="grid grid-cols-1 gap-2 sm:grid-cols-[minmax(0,1fr)_160px_90px_auto] sm:items-end" onSubmit={(e) => { e.preventDefault(); if (amountOk) save(); }}>
          <label className="flex flex-col gap-1">
            <span className="label">{t("progetti.fornitori.contractRef")}</span>
            <input className="field" maxLength={120} value={ref} placeholder={linked?.reference ?? ""} onChange={(e) => setRef(e.target.value)} />
          </label>
          {v.amounts_visible && (
            <>
              <label className="flex flex-col gap-1">
                <span className="label">{t("progetti.fornitori.contractAmount")}</span>
                <input type="number" inputMode="decimal" min={0} step="0.01" className="field tabular" value={amount}
                  placeholder={linked?.annual_cost_cents != null ? String(linked.annual_cost_cents / 100) : ""} onChange={(e) => setAmount(e.target.value)} />
              </label>
              <label className="flex flex-col gap-1">
                <span className="label">{t("progetti.fornitori.currency")}</span>
                <input className="field" maxLength={3} value={currency} onChange={(e) => setCurrency(e.target.value)} />
              </label>
            </>
          )}
          <button type="submit" className="btn touch-44 sm:col-start-4" disabled={busy || !amountOk}>{t("progetti.fornitori.saveContract")}</button>
        </form>
      ) : (
        <dl className="m-0 grid grid-cols-1 gap-x-6 gap-y-2 sm:grid-cols-3">
          <Fact label={t("progetti.fornitori.contractRef")}>{v.contract.ref || t("progetti.fornitori.none")}</Fact>
          <Fact label={t("progetti.fornitori.contractAmount")}>{v.amounts_visible ? money(v.contract.amount_cents, v.contract.currency || "EUR") : t("progetti.fornitori.amountsHidden")}</Fact>
        </dl>
      )}
      {edit && linked && <p className="m-0 text-[12px] text-muted">{t("progetti.passo6.contract.ownHelp")}</p>}
      {v.amounts_visible && v.accepted_cents != null && (
        <p className="tabular m-0 text-[13px] text-muted">{t("progetti.fornitori.acceptedTotal", { amount: money(v.accepted_cents, v.contract.currency || "EUR") })}</p>
      )}
      {!v.amounts_visible && edit && <p className="m-0 text-[12px] text-muted">{t("progetti.fornitori.amountsHidden")}</p>}

      <div className="flex flex-col gap-1">
        <span className="label">{t("progetti.fornitori.contact")}</span>
        {edit ? (
          v.supplier_id || contact ? (
            <EntitySelect kind="member" className="field w-full sm:w-72" aria-label={t("progetti.fornitori.contact")} noneLabel={t("progetti.fornitori.contactNone")} disabled={busy}
              hints={contact ? { [contact.id]: contact.name } : undefined} value={contact?.id ?? ""} onChange={(id) => run(() => projectsApi.updateSupplier(v.item_id, { contact_id: id }))} />
          ) : (
            <span className="text-[13px] text-muted">{t("progetti.fornitori.supplierNone")}</span>
          )
        ) : (
          <span>{contact ? contact.name : t("progetti.fornitori.contactNone")}</span>
        )}
      </div>

      <label className="flex min-h-[44px] items-start gap-2 md:min-h-0">
        <input type="checkbox" className="mt-0.5 size-5 accent-[var(--accent)]" disabled={!edit || busy} checked={v.supplier_updates}
          onChange={(e) => run(() => projectsApi.updateSupplier(v.item_id, { supplier_updates: e.target.checked }))} />
        <span className="flex flex-col">
          <span className="label">{t("progetti.fornitori.updates")}</span>
          <span className="text-[12px] text-muted">{t("progetti.fornitori.updatesHelp")}</span>
        </span>
      </label>
    </div>
  );
}

/** Contratto del registro ICT collegato (passo 6): si sceglie tra quelli del fornitore, si scollega. */
function LinkedContractBlock({ v, busy, run }: { v: SupplierView; busy: boolean; run: Run }) {
  const t = useT();
  const linked = v.contract.linked;
  const [pick, setPick] = useState("");
  const opts = useQuery({ queryKey: projectKeys.contracts(v.item_id), queryFn: () => projectsApi.contractOptions(v.item_id), enabled: v.can_edit && !linked });
  const period = (a: string | null, b: string | null) => (a || b ? `${a ? date(a) : "…"} – ${b ? date(b) : "…"}` : t("progetti.passo6.contract.noDates"));
  if (linked) {
    return (
      <div className="flex flex-col gap-2 border-l-2 border-rule pl-3" data-testid="contratto-collegato">
        <span className="label text-muted">{t("progetti.passo6.contract.linked")}</span>
        <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
          <span className="font-mono text-[12.5px] font-semibold">{linked.reference}</span>
          <span>{linked.provider}</span>
          {linked.service_type && <span className="text-[12.5px] text-muted">{linked.service_type}</span>}
        </div>
        <span className="tabular text-[12.5px]">
          {period(linked.start_date, linked.end_date)}
          {linked.expired && <span className="ml-2 font-semibold text-danger">{t("progetti.passo6.contract.expired")}</span>}
        </span>
        {v.amounts_visible && linked.annual_cost_cents != null && (
          <span className="tabular text-[12.5px]">{t("progetti.passo6.contract.annual", { amount: money(linked.annual_cost_cents, linked.currency || "EUR") })}</span>
        )}
        {v.can_edit && (
          <button type="button" className="btn touch-44 self-start" disabled={busy} onClick={() => run(() => projectsApi.updateSupplier(v.item_id, { contract_id: "" }))}>
            {t("progetti.passo6.contract.unlink")}
          </button>
        )}
      </div>
    );
  }
  if (!v.can_edit) return null;
  const items = opts.data?.items ?? [];
  return (
    <div className="flex flex-col gap-1.5" data-testid="contratto-registro">
      <span className="label">{t("progetti.passo6.contract.fromRegister")}</span>
      {opts.data && items.length === 0 ? (
        <span className="text-[12.5px] text-muted">{t("progetti.passo6.contract.noneInRegister")}</span>
      ) : (
        <div className="flex flex-wrap items-end gap-2">
          <Select className="field w-full sm:w-80" aria-label={t("progetti.passo6.contract.fromRegister")} value={pick} onChange={(e) => setPick(e.target.value)}>
            <option value="">{t("progetti.passo6.contract.choose")}</option>
            {items.map((c) => (
              <option key={c.id} value={c.id}>
                {`${c.reference} · ${c.provider}${c.expired ? ` · ${t("progetti.passo6.contract.expired")}` : ""}`}
              </option>
            ))}
          </Select>
          <button type="button" className="btn touch-44" disabled={busy || !pick} onClick={() => run(() => projectsApi.updateSupplier(v.item_id, { contract_id: pick }))}>
            {t("progetti.passo6.contract.link")}
          </button>
        </div>
      )}
      <span className="text-[12px] text-muted">{t("progetti.passo6.contract.help")}</span>
    </div>
  );
}

function Fact({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-0.5">
      <dt className="label text-muted">{label}</dt>
      <dd className="m-0">{children}</dd>
    </div>
  );
}

/** Esito con motivo: Respingi apre il campo, il motivo è obbligatorio. */
function DecisionButtons({ busy, onAccept, onReject, acceptLabel, rejectLabel }: { busy: boolean; onAccept: () => void; onReject: (note: string) => void; acceptLabel: string; rejectLabel: string }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const [note, setNote] = useState("");
  if (open) {
    return (
      <form className="flex w-full flex-col gap-2 sm:flex-row sm:items-end" onSubmit={(e) => { e.preventDefault(); if (note.trim()) { onReject(note.trim()); setOpen(false); setNote(""); } }}>
        <label className="flex min-w-0 flex-1 flex-col gap-1">
          <span className="label">{t("progetti.fornitori.reasonLabel")}</span>
          <input className="field" maxLength={500} required autoFocus value={note} onChange={(e) => setNote(e.target.value)} />
        </label>
        <button type="submit" className="btn btn-danger touch-44" disabled={busy || !note.trim()}>{t("progetti.fornitori.confirmReject")}</button>
        <button type="button" className="btn btn-quiet touch-44" onClick={() => setOpen(false)}>{t("progetti.fornitori.cancel")}</button>
      </form>
    );
  }
  return (
    <span className="flex flex-wrap gap-2">
      <button type="button" className="btn btn-primary touch-44" disabled={busy} onClick={onAccept}>{acceptLabel}</button>
      <button type="button" className="btn touch-44" disabled={busy} onClick={() => setOpen(true)}>{rejectLabel}</button>
    </span>
  );
}

function Claims({ v, busy, run, itemId }: { v: SupplierView; busy: boolean; run: Run; itemId: string }) {
  const t = useT();
  const name = useClaimName();
  const cur = v.contract.currency || "EUR";
  const hasSubmitted = v.claims.some((c) => c.status === "submitted");
  return (
    <section className="flex flex-col gap-3" aria-label={name.many} data-testid="fornitore-sal">
      <SectionTitle>{name.many}</SectionTitle>
      {v.claims.length === 0 ? (
        <p className="m-0 text-muted">{t("progetti.fornitori.claims.empty")}</p>
      ) : (
        <ul className="m-0 flex list-none flex-col p-0" aria-label={name.many}>
          {v.claims.map((c) => (
            <ClaimRow key={c.id} c={c} cur={cur} v={v} busy={busy} run={run} />
          ))}
        </ul>
      )}
      {v.can_declare && !hasSubmitted && <ClaimForm v={v} busy={busy} run={run} itemId={itemId} />}
    </section>
  );
}

function ClaimRow({ c, cur, v, busy, run }: { c: Claim; cur: string; v: SupplierView; busy: boolean; run: Run }) {
  const t = useT();
  const name = useClaimName();
  const pending = c.status === "submitted";
  const tone = c.status === "accepted" ? "text-ok" : c.status === "rejected" ? "text-danger" : pending ? "text-flag" : "text-muted"; // i18n-ok: classi CSS
  return (
    <li className="flex flex-col gap-1.5 border-b border-hair py-2.5" data-testid={`sal-${c.number}`} data-status={c.status}>
      <div className="grid grid-cols-[auto_minmax(0,1fr)] items-baseline gap-x-4 gap-y-1 sm:grid-cols-[70px_110px_80px_130px_90px_minmax(0,1fr)]">
        <span className="font-mono text-[11.5px] font-medium">{name.one} {t("progetti.fornitori.claims.number", { number: c.number })}</span>
        <span className="tabular text-muted" title={t("progetti.fornitori.claims.period")}>{c.period_end ? date(c.period_end) : "—"}</span>
        <span className="tabular font-semibold" title={t("progetti.fornitori.claims.percent")}>{c.percent == null ? "—" : `${c.percent}%`}</span>
        <span className="tabular" title={t("progetti.fornitori.claims.amount")}>{v.amounts_visible ? money(c.amount_cents, cur) : "—"}</span>
        <span className="tabular text-muted" title={t("progetti.fornitori.claims.value")}>{c.value == null ? "—" : `${Math.round(c.value)}%`}</span>
        <span className={`font-semibold ${tone}`}>{t(`progetti.fornitori.claims.status.${c.status}`)}</span>
      </div>
      <span className="text-[12px] text-muted">{t("progetti.fornitori.claims.by", { who: c.submitted_by ?? "—", when: date(c.submitted_at) })}</span>
      {c.note && <span className="break-words">{c.note}</span>}
      {c.decided_at && <span className="text-[12px] text-muted">{t("progetti.fornitori.claims.decidedBy", { who: c.decided_by ?? "—", when: date(c.decided_at) })}{c.decision_note ? ` · ${c.decision_note}` : ""}</span>}
      {pending && v.can_edit && (
        <DecisionButtons busy={busy} acceptLabel={t("progetti.fornitori.accept")} rejectLabel={t("progetti.fornitori.reject")}
          onAccept={() => run(() => projectsApi.decideClaim(c.id, { outcome: "accepted" }))}
          onReject={(note) => run(() => projectsApi.decideClaim(c.id, { outcome: "rejected", note }))} />
      )}
    </li>
  );
}

function ClaimForm({ v, busy, run, itemId }: { v: SupplierView; busy: boolean; run: Run; itemId: string }) {
  const t = useT();
  const name = useClaimName();
  const [percent, setPercent] = useState("");
  const [amount, setAmount] = useState("");
  const [period, setPeriod] = useState("");
  const [note, setNote] = useState("");
  const [accept, setAccept] = useState(false);
  const p = percent.trim() === "" ? null : Number(percent.replace(",", "."));
  const a = amount.trim() === "" ? null : Number(amount.replace(",", "."));
  const pOk = p == null || (Number.isFinite(p) && p >= 0 && p <= 100);
  const aOk = a == null || (Number.isFinite(a) && a >= 0);
  const valid = pOk && aOk && (p != null || a != null);
  const submit = () => {
    if (!valid) return;
    void run(async () => {
      const next = await projectsApi.createClaim(itemId, {
        ...(p != null ? { percent: p } : {}),
        ...(a != null ? { amount_cents: Math.round(a * 100) } : {}),
        ...(period ? { period_end: period } : {}),
        ...(note.trim() ? { note: note.trim() } : {}),
        ...(accept && v.can_edit ? { accept: true } : {}),
      });
      setPercent(""); setAmount(""); setPeriod(""); setNote(""); setAccept(false);
      return next;
    });
  };
  return (
    <form className="flex flex-col gap-2 border-t border-rule pt-3" aria-label={t("progetti.fornitori.claims.declare", { name: name.one })} data-testid="sal-form" onSubmit={(e) => { e.preventDefault(); submit(); }}>
      <h4 className="label m-0">{t("progetti.fornitori.claims.declare", { name: name.one })}</h4>
      <p className="m-0 text-[12px] text-muted">{t("progetti.fornitori.claims.declareHelp")}</p>
      <div className="grid grid-cols-1 gap-2 sm:grid-cols-[110px_150px_150px_minmax(0,1fr)]">
        <label className="flex flex-col gap-1">
          <span className="label">{t("progetti.fornitori.claims.percentLabel")}</span>
          <input type="number" inputMode="decimal" min={0} max={100} step="any" className="field tabular" data-testid="sal-percent" value={percent} onChange={(e) => setPercent(e.target.value)} />
        </label>
        {v.amounts_visible && (
          <label className="flex flex-col gap-1">
            <span className="label">{t("progetti.fornitori.claims.amountLabel")}</span>
            <input type="number" inputMode="decimal" min={0} step="0.01" className="field tabular" data-testid="sal-amount" value={amount} onChange={(e) => setAmount(e.target.value)} />
          </label>
        )}
        <label className="flex flex-col gap-1">
          <span className="label">{t("progetti.fornitori.claims.periodLabel")}</span>
          <input type="date" className="field" value={period} onChange={(e) => setPeriod(e.target.value)} />
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{t("progetti.fornitori.claims.noteLabel")}</span>
          <input className="field" maxLength={500} value={note} onChange={(e) => setNote(e.target.value)} />
        </label>
      </div>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        {v.can_edit && (
          <label className="flex min-h-[44px] items-center gap-2 md:min-h-0">
            <input type="checkbox" className="size-5 accent-[var(--accent)]" checked={accept} onChange={(e) => setAccept(e.target.checked)} />
            <span>{t("progetti.fornitori.claims.acceptNow")}</span>
          </label>
        )}
        <button type="submit" className="btn btn-primary touch-44" disabled={busy || !valid} data-testid="sal-submit">{t("progetti.fornitori.claims.submit")}</button>
        {!valid && (percent !== "" || amount !== "") && <span className="text-[12px] text-muted">{t("progetti.fornitori.claims.needValue")}</span>}
      </div>
    </form>
  );
}

function Deliverables({ v, busy, run, itemId, projectKey }: { v: SupplierView; busy: boolean; run: Run; itemId: string; projectKey: string }) {
  const t = useT();
  const tn = useTypeNames();
  const one = tn.name("deliverable", t("progetti.fornitori.name.deliverable"));
  const many = tn.plural("deliverable", t("progetti.fornitori.name.deliverables"));
  return (
    <section className="flex flex-col gap-3" aria-label={many} data-testid="fornitore-deliverable">
      <SectionTitle>{many}</SectionTitle>
      {v.deliverables.length === 0 ? (
        <p className="m-0 text-muted">{t("progetti.fornitori.deliverables.empty")}</p>
      ) : (
        <ul className="m-0 flex list-none flex-col p-0" aria-label={many}>
          {v.deliverables.map((d) => (
            <DeliverableRow key={d.id} d={d} v={v} busy={busy} run={run} />
          ))}
        </ul>
      )}
      {v.can_edit && <NewDeliverable one={one} itemId={itemId} projectKey={projectKey} busy={busy} run={run} />}
    </section>
  );
}

function DeliverableRow({ d, v, busy, run }: { d: Deliverable; v: SupplierView; busy: boolean; run: Run }) {
  const t = useT();
  const [note, setNote] = useState("");
  const [delivering, setDelivering] = useState(false);
  const tone = d.status === "accepted" ? "text-ok" : d.status === "rejected" ? "text-danger" : d.status === "delivered" ? "text-flag" : "text-muted"; // i18n-ok: classi CSS
  const canDeliver = v.can_declare && (d.status === "pending" || d.status === "rejected");
  return (
    <li className="flex flex-col gap-1.5 border-b border-hair py-2.5" data-status={d.status}>
      <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
        <span className="min-w-0 break-words font-semibold">{d.title}</span>
        <span className={`font-semibold ${tone}`}>{t(`progetti.fornitori.deliverables.status.${d.status}`)}</span>
      </div>
      <span className="flex flex-wrap gap-x-4 text-[12px] text-muted">
        {d.due && <span className="tabular">{t("progetti.fornitori.deliverables.due", { date: date(d.due) })}</span>}
        {d.milestone && <span>{t("progetti.fornitori.deliverables.milestone", { title: d.milestone })}</span>}
      </span>
      {d.delivered_at && <span className="text-[12px] text-muted">{t("progetti.fornitori.deliverables.deliveredBy", { who: d.delivered_by ?? "—", when: date(d.delivered_at) })}{d.delivery_note ? ` · ${d.delivery_note}` : ""}</span>}
      {d.decided_at && <span className="text-[12px] text-muted">{t("progetti.fornitori.deliverables.decidedBy", { who: d.decided_by ?? "—", when: date(d.decided_at) })}{d.decision_note ? ` · ${d.decision_note}` : ""}</span>}
      {canDeliver &&
        (delivering ? (
          <form className="flex flex-col gap-2 sm:flex-row sm:items-end" onSubmit={(e) => { e.preventDefault(); void run(() => projectsApi.deliver(d.id, note.trim())); setDelivering(false); setNote(""); }}>
            <label className="flex min-w-0 flex-1 flex-col gap-1">
              <span className="label">{t("progetti.fornitori.deliverables.noteLabel")}</span>
              <input className="field" maxLength={500} autoFocus value={note} onChange={(e) => setNote(e.target.value)} />
            </label>
            <button type="submit" className="btn btn-primary touch-44" disabled={busy}>{t("progetti.fornitori.deliverables.deliver")}</button>
            <button type="button" className="btn btn-quiet touch-44" onClick={() => setDelivering(false)}>{t("progetti.fornitori.cancel")}</button>
          </form>
        ) : (
          <button type="button" className="btn touch-44 self-start" disabled={busy} onClick={() => setDelivering(true)}>{t("progetti.fornitori.deliverables.deliver")}</button>
        ))}
      {d.status === "delivered" && v.can_edit && (
        <DecisionButtons busy={busy} acceptLabel={t("progetti.fornitori.accept")} rejectLabel={t("progetti.fornitori.reject")}
          onAccept={() => run(() => projectsApi.decideDeliverable(d.id, { outcome: "accepted" }))}
          onReject={(n) => run(() => projectsApi.decideDeliverable(d.id, { outcome: "rejected", note: n }))} />
      )}
    </li>
  );
}

function NewDeliverable({ one, itemId, projectKey, busy, run }: { one: string; itemId: string; projectKey: string; busy: boolean; run: Run }) {
  const t = useT();
  const plan = useQuery({ queryKey: projectKeys.plan(projectKey), queryFn: () => projectsApi.plan(projectKey) });
  const milestones = (plan.data?.items ?? []).filter((i) => i.kind === "milestone");
  const [title, setTitle] = useState("");
  const [due, setDue] = useState("");
  const [ms, setMs] = useState("");
  const label = t("progetti.fornitori.deliverables.new", { name: one });
  return (
    <form className="flex flex-col gap-2 border-t border-rule pt-3" aria-label={label} data-testid="deliverable-form"
      onSubmit={(e) => {
        e.preventDefault();
        if (!title.trim()) return;
        void run(async () => {
          const next = await projectsApi.createDeliverable(itemId, { title: title.trim(), ...(due ? { due } : {}), ...(ms ? { milestone_id: ms } : {}) });
          setTitle(""); setDue(""); setMs("");
          return next;
        });
      }}>
      <h4 className="label m-0">{label}</h4>
      <div className="grid grid-cols-1 gap-2 sm:grid-cols-[minmax(0,1fr)_150px_220px_auto] sm:items-end">
        <label className="flex flex-col gap-1">
          <span className="label">{t("progetti.fornitori.deliverables.titleLabel")}</span>
          <input className="field" maxLength={200} value={title} onChange={(e) => setTitle(e.target.value)} />
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{t("progetti.fornitori.deliverables.dueLabel")}</span>
          <input type="date" className="field" value={due} onChange={(e) => setDue(e.target.value)} />
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{t("progetti.fornitori.deliverables.milestoneLabel")}</span>
          <Select className="field" value={ms} onChange={(e) => setMs(e.target.value)}>
            <option value="">{t("progetti.fornitori.deliverables.milestoneNone")}</option>
            {milestones.map((m) => <option key={m.id} value={m.id}>{m.key} · {m.title}</option>)}
          </Select>
        </label>
        <button type="submit" className="btn touch-44" disabled={busy || !title.trim()}>{t("progetti.fornitori.deliverables.add")}</button>
      </div>
    </form>
  );
}
