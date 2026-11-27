// Baseline e change request del progetto (replica/progetti.md §3 «Baseline e variazioni», passo 3),
// sotto il Gantt della scheda «Piano». La baseline è una versione immutabile del piano: la prima la
// fissa il PM, le successive nascono da una change request approvata. Quando una modifica porta una
// milestone o la fine oltre le tolleranze, la change request nasce da sola e va in approvazione
// (o resta da inviare, con il motivo, se l'approvazione non può partire). Passo 5: anche il budget oltre la
// tolleranza e le voci aggiunte o tolte dopo la baseline aprono la change request; quella in approvazione si
// ritira; le tolleranze del singolo progetto si cambiano qui.
import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { projectKeys, projectsApi, type Baseline, type ChangeRequest, type Deviation, type ProjectPlan } from "../../projectsApi";
import { errorText } from "../../i18n/core";
import { useT } from "../../i18n/react";
import { ErrorNote, SectionTitle, date } from "../../ui";
import { ItemLink } from "./shared";

export function BaselinePanel({ plan, refKey }: { plan: ProjectPlan; refKey: string }) {
  const t = useT();
  const qc = useQueryClient();
  const crs = useQuery({ queryKey: projectKeys.changeRequests(refKey), queryFn: () => projectsApi.changeRequests(refKey) });
  const bls = useQuery({ queryKey: projectKeys.baselines(refKey), queryFn: () => projectsApi.baselines(refKey), enabled: plan.baseline != null });
  const [err, setErr] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState("");
  const [asking, setAsking] = useState(false);

  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    setErr(null);
    try {
      await fn();
      return true;
    } catch (e) {
      setErr(e);
      return false;
    } finally {
      await Promise.all([
        qc.invalidateQueries({ queryKey: projectKeys.plan(refKey) }),
        qc.invalidateQueries({ queryKey: projectKeys.changeRequests(refKey) }),
        qc.invalidateQueries({ queryKey: projectKeys.baselines(refKey) }),
        qc.invalidateQueries({ queryKey: projectKeys.overview(refKey) }),
      ]);
      setBusy(false);
    }
  };

  const tol = plan.tolerance;
  const list = crs.data?.items ?? [];
  const open = list.some((c) => c.status === "draft" || c.status === "pending");
  const b = plan.baseline;
  return (
    <section className="flex flex-col gap-3" aria-label={t("progetti.baseline.title")}>
      <SectionTitle>{t("progetti.baseline.title")}</SectionTitle>
      <p className="m-0 text-[12.5px] text-muted">{t("progetti.baseline.tolerance", { days: tol.days, pct: tol.budget_pct })}</p>
      <ToleranceRow plan={plan} refKey={refKey} busy={busy} run={run} />
      {b ? (
        <p className="m-0" data-testid="baseline-corrente">
          {t("progetti.baseline.current", { number: b.number, date: date(b.created_at), who: b.created_by ?? t("progetti.common.none") })}
        </p>
      ) : (
        <div className="flex flex-col gap-2">
          <p className="m-0">{t("progetti.baseline.none")}</p>
          {plan.can_edit && (
            <div className="flex flex-wrap items-end gap-2">
              <label className="flex min-w-64 flex-1 flex-col gap-1">
                <span className="label">{t("progetti.baseline.noteLabel")}</span>
                <input className="field" value={note} maxLength={2000} onChange={(e) => setNote(e.target.value)} />
              </label>
              <button className="btn btn-primary touch-44" disabled={busy || plan.items.length === 0} onClick={() => run(() => projectsApi.createBaseline(refKey, note)).then((ok) => ok && setNote(""))}>
                {t("progetti.baseline.set")}
              </button>
            </div>
          )}
        </div>
      )}
      <ErrorNote error={err} />

      {list.length > 0 && (
        <ul className="m-0 flex list-none flex-col p-0" aria-label={t("progetti.baseline.changeRequests")}>
          {list.map((c) => (
            <CRRow key={c.id} cr={c} busy={busy} run={run} />
          ))}
        </ul>
      )}
      <ErrorNote error={crs.error} />

      {b && plan.can_edit && !open && (
        asking ? (
          <AskForm busy={busy} onCancel={() => setAsking(false)} onSubmit={(title, reason) => run(() => projectsApi.createChangeRequest(refKey, { title, reason })).then((ok) => ok && setAsking(false))} />
        ) : (
          <button className="btn touch-44 self-start" onClick={() => setAsking(true)}>
            {t("progetti.baseline.ask")}
          </button>
        )
      )}

      {b && bls.data && bls.data.items.length > 0 && (
        <details className="text-[13px]">
          <summary className="cursor-pointer text-muted">{t("progetti.baseline.history", { count: bls.data.items.length })}</summary>
          <ul className="m-0 mt-2 flex list-none flex-col p-0">
            {bls.data.items.map((x) => (
              <BaselineRow key={x.id} b={x} />
            ))}
          </ul>
        </details>
      )}
    </section>
  );
}

function BaselineRow({ b }: { b: Baseline }) {
  const t = useT();
  return (
    <li className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5 border-b border-hair py-1.5">
      <span className="font-semibold">{t("progetti.baseline.number", { number: b.number })}</span>
      {b.current && <span className="text-[12px] font-semibold">{t("progetti.baseline.inForce")}</span>}
      <span className="tabular text-muted">{t("progetti.baseline.span", { from: date(b.start), to: date(b.end) })}</span>
      <span className="text-muted">{t("progetti.baseline.by", { date: date(b.created_at), who: b.created_by ?? t("progetti.common.none") })}</span>
      {b.change_request && <ItemLink itemKey={b.change_request.key} />}
      {b.note && <span className="basis-full text-muted">{b.note}</span>}
    </li>
  );
}

function CRRow({ cr, busy, run }: { cr: ChangeRequest; busy: boolean; run: (fn: () => Promise<unknown>) => Promise<boolean> }) {
  const t = useT();
  const [reason, setReason] = useState("");
  const blocked = cr.blocked ? errorText(cr.blocked.code, String(cr.blocked.params?.detail ?? ""), cr.blocked.params as Record<string, string | number>) : null;
  const tone = cr.status === "pending" || cr.status === "draft" ? "font-semibold" : "text-muted";
  return (
    <li className="flex flex-col gap-1.5 border-b border-hair py-2.5" data-testid="change-request">
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5">
        <ItemLink itemKey={cr.key} />
        <span className="font-semibold">{cr.title}</span>
        <span className={`text-[12.5px] ${tone}`}>{t(`progetti.baseline.status.${cr.status}`)}</span>
        <span className="text-[12px] text-muted">{t(`progetti.baseline.origin.${cr.origin}`)}</span>
        <span className="ml-auto text-[12px] text-muted">{date(cr.created_at)}</span>
      </div>
      {cr.reason && <p className="m-0 text-[13px]">{cr.reason}</p>}
      {cr.deviations.length > 0 && (
        <ul className="m-0 list-none p-0 text-[13px]">
          {cr.deviations.map((d) => (
            <li key={d.kind + d.id} className="flex flex-wrap gap-x-2" data-kind={d.kind}>
              <DeviationText d={d} />
            </li>
          ))}
        </ul>
      )}
      {cr.budget_pct != null && cr.budget_pct !== 0 && <p className="m-0 text-[13px] text-muted">{t("progetti.baseline.budgetVariance", { pct: cr.budget_pct > 0 ? `+${cr.budget_pct}` : `${cr.budget_pct}` })}</p>}
      {cr.status === "approved" && cr.new_baseline != null && <p className="m-0 text-[13px] text-muted">{t("progetti.baseline.became", { number: cr.new_baseline })}</p>}
      {cr.status === "draft" && blocked && (
        <p role="status" className="m-0 text-[13px] text-danger">
          {t("progetti.baseline.blocked")} {blocked}
        </p>
      )}
      {cr.can_submit && (
        <div className="flex flex-wrap items-end gap-2">
          <label className="flex min-w-64 flex-1 flex-col gap-1">
            <span className="label">{t("progetti.baseline.reasonLabel")}</span>
            <input className="field" value={reason} maxLength={4000} placeholder={cr.reason} onChange={(e) => setReason(e.target.value)} />
          </label>
          <button className="btn btn-primary touch-44" disabled={busy} onClick={() => run(() => projectsApi.submitChangeRequest(cr.id, reason))}>
            {t("progetti.baseline.submit")}
          </button>
          <button className="btn touch-44" disabled={busy} onClick={() => run(() => projectsApi.withdrawChangeRequest(cr.id))}>
            {t("progetti.baseline.withdraw")}
          </button>
        </div>
      )}
      {cr.status === "pending" && cr.can_withdraw && (
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-[12.5px] text-muted">{t("progetti.passo5.cr.pendingHelp")}</span>
          <button
            className="btn touch-44"
            disabled={busy}
            onClick={() => window.confirm(t("progetti.passo5.cr.withdrawConfirm")) && void run(() => projectsApi.withdrawChangeRequest(cr.id))}
          >
            {t("progetti.passo5.cr.withdrawPending")}
          </button>
        </div>
      )}
    </li>
  );
}

/** Uno scostamento della change request: date (milestone o fine), perimetro o budget. */
function DeviationText({ d }: { d: Deviation }) {
  const t = useT();
  switch (d.kind) {
    case "scope_added":
    case "scope_removed":
      return (
        <>
          <span className="font-medium">{d.title || d.key}</span>
          <span className="text-muted">{t(`progetti.passo5.scope.${d.kind === "scope_added" ? "added" : "removed"}`)}</span>
          {(d.kind === "scope_added" ? d.end : d.baseline_end) && <span className="tabular text-muted">{date(d.kind === "scope_added" ? d.end : d.baseline_end)}</span>}
        </>
      );
    case "budget":
      return (
        <>
          <span className="font-medium">{t("progetti.passo5.cr.budget")}</span>
          <span className="tabular font-semibold text-danger">{t("progetti.passo5.cr.budgetPct", { pct: d.pct != null && d.pct > 0 ? `+${d.pct}` : `${d.pct ?? 0}` })}</span>
        </>
      );
  }
  return (
    <>
      <span className="font-medium">{d.kind === "project" ? t("progetti.baseline.projectEnd") : d.title}</span>
      <span className="tabular text-muted">{t("progetti.baseline.deviation", { from: date(d.baseline_end), to: date(d.end) })}</span>
      <span className="tabular font-semibold text-danger">{t("progetti.baseline.shift", { sign: "+", count: d.days })}</span>
    </>
  );
}

/**
 * Tolleranze del singolo progetto (passo 5): le vede chiunque legga il piano; le cambia chi governa il
 * progetto (PMO, sponsor, governo del portafoglio). Vuoto vuol dire quella del tenant.
 */
function ToleranceRow({ plan, refKey, busy, run }: { plan: ProjectPlan; refKey: string; busy: boolean; run: (fn: () => Promise<unknown>) => Promise<boolean> }) {
  const t = useT();
  const own = plan.project_tolerance ?? { days: null, budget_pct: null };
  const [editing, setEditing] = useState(false);
  const [days, setDays] = useState(own.days == null ? "" : String(own.days));
  const [pct, setPct] = useState(own.budget_pct == null ? "" : String(own.budget_pct));
  const custom = own.days != null || own.budget_pct != null;
  if (!editing) {
    return (
      <p className="m-0 flex flex-wrap items-center gap-x-3 gap-y-1 text-[12.5px] text-muted" data-testid="tolleranze-progetto">
        <span>{custom ? t("progetti.passo5.tol.own") : t("progetti.passo5.tol.tenant")}</span>
        {plan.can_edit_tolerance && (
          <button type="button" className="btn btn-quiet px-0 text-[12.5px] underline" onClick={() => setEditing(true)}>
            {t("progetti.passo5.tol.edit")}
          </button>
        )}
      </p>
    );
  }
  const num = (v: string) => (v.trim() === "" ? null : Number(v));
  return (
    <form
      className="flex flex-wrap items-end gap-3 border border-hair p-3"
      aria-label={t("progetti.passo5.tol.title")}
      onSubmit={(e) => {
        e.preventDefault();
        void run(() => projectsApi.setTolerance(refKey, { days: num(days), budget_pct: num(pct) })).then((ok) => ok && setEditing(false));
      }}
    >
      <p className="m-0 basis-full text-[12.5px] text-muted">{t("progetti.passo5.tol.help")}</p>
      <label className="flex flex-col gap-1">
        <span className="label">{t("progetti.baseline.settings.days")}</span>
        <input className="field w-32" type="number" min={0} max={365} step={1} value={days} placeholder={t("progetti.passo5.tol.inherit")} onChange={(e) => setDays(e.target.value)} />
      </label>
      <label className="flex flex-col gap-1">
        <span className="label">{t("progetti.baseline.settings.budget")}</span>
        <input className="field w-32" type="number" min={0} max={100} step={0.5} value={pct} placeholder={t("progetti.passo5.tol.inherit")} onChange={(e) => setPct(e.target.value)} />
      </label>
      <button type="submit" className="btn btn-primary touch-44" disabled={busy}>
        {t("progetti.passo5.tol.save")}
      </button>
      <button type="button" className="btn touch-44" onClick={() => setEditing(false)}>
        {t("progetti.piano.cancel")}
      </button>
    </form>
  );
}

function AskForm({ busy, onCancel, onSubmit }: { busy: boolean; onCancel: () => void; onSubmit: (title: string, reason: string) => void }) {
  const t = useT();
  const [title, setTitle] = useState("");
  const [reason, setReason] = useState("");
  return (
    <form
      className="flex flex-col gap-2 border border-hair p-3"
      aria-label={t("progetti.baseline.ask")}
      onSubmit={(e) => {
        e.preventDefault();
        if (reason.trim()) onSubmit(title.trim(), reason.trim());
      }}
    >
      <p className="m-0 text-[12.5px] text-muted">{t("progetti.baseline.askHelp")}</p>
      <label className="flex flex-col gap-1">
        <span className="label">{t("progetti.baseline.titleLabel")}</span>
        <input className="field" value={title} maxLength={500} onChange={(e) => setTitle(e.target.value)} />
      </label>
      <label className="flex flex-col gap-1">
        <span className="label">{t("progetti.baseline.reasonLabel")}</span>
        <textarea className="field min-h-20" required value={reason} maxLength={4000} onChange={(e) => setReason(e.target.value)} />
      </label>
      <div className="flex gap-2">
        <button type="submit" className="btn btn-primary touch-44" disabled={busy || !reason.trim()}>
          {t("progetti.baseline.askGo")}
        </button>
        <button type="button" className="btn touch-44" onClick={onCancel}>
          {t("progetti.piano.cancel")}
        </button>
      </div>
    </form>
  );
}
