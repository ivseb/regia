// Schede di dettaglio di rischio, incidente e fornitore ICT nella pagina dell'elemento
// (piano, passo 20). Si caricano solo per questi tipi.
import { EntitySelect, Select } from "../select";
import { Suspense, lazy, useState, type FormEvent, type ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { ErrorNote, SectionTitle, date, Loading } from "../ui";
import {
  EVENT_KINDS,
  NOTIFICATION_STATUS,
  TREATMENTS,
  riskApi,
  type Contract,
  type ContractInput,
  type IncidentInput,
  type IncidentView,
  type Notification,
  type PhaseMove,
  type ProviderView,
  type RiskView,
} from "../riskApi";
import { RiskControlsPanel } from "./ControlLiving";
import { Countdown, Fact, Facts, Heatmap, RefList, ScoreBadge, dateTime, euroCents, fromLocalInput, toLocalInput } from "./riskUi";
import { tr } from "../i18n/core";
import { loc } from "../i18n/format";

export default function GrcDetail({ refKey, type }: { refKey: string; type: string }) {
  if (type === "risk") return <RiskDetail refKey={refKey} />;
  if (type === "incident") return <IncidentDetail refKey={refKey} />;
  if (type === "ict_provider") return <ProviderDetail refKey={refKey} />;
  return null;
}

function useRefresh(refKey: string) {
  const qc = useQueryClient();
  return () => {
    qc.invalidateQueries({ queryKey: ["risk"] });
    qc.invalidateQueries({ queryKey: ["grc", "risk-controls"] });
    qc.invalidateQueries({ queryKey: ["element", refKey] });
    qc.invalidateQueries({ queryKey: ["activity"] });
    qc.invalidateQueries({ queryKey: ["links", refKey] });
  };
}

function Edit({ on, onClick, label = tr("governance.compliancesections.modifica") }: { on: boolean; onClick: () => void; label?: string }) {
  if (!on) return null;
  return (
    <button className="text-[12px] text-accent underline" onClick={onClick}>
      {label}
    </button>
  );
}

// ---------------------------------------------------------------------
// Rischio

function RiskDetail({ refKey }: { refKey: string }) {
  const view = useQuery({ queryKey: ["risk", "risk", refKey], queryFn: () => riskApi.risk(refKey) });
  const [editing, setEditing] = useState<"" | "assessment" | "accept">("");
  const r = view.data;
  if (view.error) return <ErrorNote error={view.error} />;
  if (!r) return <Loading className="m-0 text-muted" />;
  const cells = (s: RiskView["inherent"]) =>
    r.matrix.likelihood.flatMap((l) =>
      r.matrix.impact.map((i) => {
        const score = l.value * i.value;
        const band = r.matrix.bands.find((b) => score >= b.min && score <= b.max);
        const here = s && s.likelihood === l.value && s.impact === i.value;
        return { likelihood: l.value, impact: i.value, score, band: band?.key ?? "", tone: band?.tone ?? "neutral", count: here ? 1 : 0, keys: here ? [r.key] : [] };
      }),
    );
  return (
    <>
      <section className="flex flex-col gap-2" aria-label={tr("governance.riskdetail.valutazione")}>
        <SectionTitle aside={<Edit on={r.can.treat && editing === ""} onClick={() => setEditing("assessment")} label={tr("governance.riskdetail.valuta_e_tratta")} />}>{tr("governance.riskdetail.valutazione_2", { name: r.matrix.name })}</SectionTitle>
        {editing === "assessment" ? (
          <AssessmentForm r={r} refKey={refKey} onDone={() => setEditing("")} />
        ) : (
          <Facts>
            <Fact k={tr("governance.requirements.categoria")}>{r.category || "—"}</Fact>
            <Fact k={tr("governance.riskdetail.inerente")}>
              <ScoreBadge score={r.inherent} matrix={r.matrix} />
              {r.inherent && (
                <span className="block text-[12px] text-muted">
                  {r.inherent.likelihood_name} × {r.inherent.impact_name}
                </span>
              )}
            </Fact>
            <Fact k={tr("governance.riskdetail.residuo")}>
              <ScoreBadge score={r.residual} matrix={r.matrix} />
              {r.residual && (
                <span className="block text-[12px] text-muted">
                  {r.residual.likelihood_name} × {r.residual.impact_name}
                </span>
              )}
            </Fact>
            <Fact k={tr("governance.riskdetail.trattamento")}>{r.treatment_name || "—"}</Fact>
            <Fact k={tr("governance.riskdetail.piano")}>
              <span className="whitespace-pre-wrap">{r.treatment_plan || "—"}</span>
            </Fact>
            <Fact k={tr("governance.riskdetail.scadenza_del_trattamento")}>{date(r.treatment_due)}</Fact>
            <Fact k={tr("governance.riskdetail.prossima_revisione")}>{date(r.review_due)}</Fact>
            <Fact k={tr("governance.riskdetail.titolare")}>{r.owner ?? "—"}</Fact>
            <Fact k={tr("governance.riskdetail.accettazione")}>
              {r.acceptance_name}
              {r.accepted_until && r.acceptance !== "none" && <span className="text-muted tabular">{tr("governance.riskdetail.fino_al", { date: date(r.accepted_until) })}</span>}
            </Fact>
          </Facts>
        )}
        {r.acceptance === "pending" && <p className="m-0 text-[12.5px] text-muted">{tr("governance.riskdetail.le_decisioni_stanno_nella_scheda")}</p>}
        {r.can.request_acceptance && editing === "" && (
          <button className="btn self-start" onClick={() => setEditing("accept")}>{tr("governance.riskdetail.chiedi_l_accettazione_del_residuo")}</button>
        )}
        {editing === "accept" && <AcceptForm refKey={refKey} onDone={() => setEditing("")} />}
      </section>
      <section className="grid grid-cols-1 gap-6 sm:grid-cols-2" aria-label={tr("governance.riskdetail.posizione_sulla_matrice")}>
        <Heatmap matrix={r.matrix} cells={cells(r.inherent)} label={tr("governance.riskdetail.rischio_inerente")} title={tr("governance.riskdetail.inerente")} legend={false} />
        <Heatmap matrix={r.matrix} cells={cells(r.residual)} label={tr("governance.requirements.rischio_residuo")} title={tr("governance.riskdetail.residuo")} legend={false} />
      </section>
      <RiskLinks r={r} refKey={refKey} />
    </>
  );
}

function AssessmentForm({ r, refKey, onDone }: { r: RiskView; refKey: string; onDone: () => void }) {
  const refresh = useRefresh(refKey);
  const [f, setF] = useState({
    category: r.category,
    il: r.inherent?.likelihood ?? 0,
    ii: r.inherent?.impact ?? 0,
    rl: r.residual?.likelihood ?? 0,
    ri: r.residual?.impact ?? 0,
    treatment: r.treatment ?? "",
    plan: r.treatment_plan,
    due: r.treatment_due ?? "",
    review: r.review_due ?? "",
    owner: r.owner_id ?? "",
  });
  const save = useMutation({
    mutationFn: () =>
      riskApi.updateRisk(refKey, {
        ...(r.can.edit
          ? { category: f.category, inherent_likelihood: f.il, inherent_impact: f.ii, ...(f.owner !== (r.owner_id ?? "") ? { owner_id: f.owner } : {}) }
          : {}),
        residual_likelihood: f.rl,
        residual_impact: f.ri,
        treatment: f.treatment,
        treatment_plan: f.plan,
        treatment_due: f.due,
        review_due: f.review,
      }),
    onSuccess: () => {
      refresh();
      onDone();
    },
  });
  const sel = (label: string, v: number, set: (n: number) => void, levels: { value: number; name: string }[], disabled = false) => (
    <label className="flex w-44 flex-col gap-1">
      <span className="label">{label}</span>
      <Select className="field" value={v} disabled={disabled} onChange={(e) => set(Number(e.target.value))}>
        <option value={0}>{tr("governance.riskdetail.da_valutare")}</option>
        {levels.map((x) => (
          <option key={x.value} value={x.value}>
            {x.value} · {x.name}
          </option>
        ))}
      </Select>
    </label>
  );
  return (
    <form
      className="flex flex-col gap-3"
      onSubmit={(e: FormEvent) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      <div className="flex flex-wrap gap-3">
        {sel(tr("governance.riskdetail.probabilita_inerente"), f.il, (n) => setF({ ...f, il: n }), r.matrix.likelihood, !r.can.edit)}
        {sel(tr("governance.riskdetail.impatto_inerente"), f.ii, (n) => setF({ ...f, ii: n }), r.matrix.impact, !r.can.edit)}
        {sel(tr("governance.riskdetail.probabilita_residua"), f.rl, (n) => setF({ ...f, rl: n }), r.matrix.likelihood)}
        {sel(tr("governance.riskdetail.impatto_residuo"), f.ri, (n) => setF({ ...f, ri: n }), r.matrix.impact)}
      </div>
      <div className="flex flex-wrap gap-3">
        <label className="flex w-44 flex-col gap-1">
          <span className="label">{tr("governance.riskdetail.trattamento")}</span>
          <Select className="field" value={f.treatment} onChange={(e) => setF({ ...f, treatment: e.target.value })}>
            <option value="">—</option>
            {Object.entries(TREATMENTS).map(([k, v]) => (
              <option key={k} value={k}>
                {v}
              </option>
            ))}
          </Select>
        </label>
        <label className="flex w-44 flex-col gap-1">
          <span className="label">{tr("governance.riskdetail.scadenza_del_trattamento")}</span>
          <input className="field" type="date" value={f.due} onChange={(e) => setF({ ...f, due: e.target.value })} />
        </label>
        <label className="flex w-44 flex-col gap-1">
          <span className="label">{tr("governance.riskdetail.prossima_revisione")}</span>
          <input className="field" type="date" value={f.review} onChange={(e) => setF({ ...f, review: e.target.value })} />
        </label>
        {r.can.edit && (
          <>
            <label className="flex min-w-0 flex-[1_1_160px] flex-col gap-1">
              <span className="label">{tr("governance.requirements.categoria")}</span>
              <input className="field" value={f.category} onChange={(e) => setF({ ...f, category: e.target.value })} />
            </label>
            <label className="flex w-48 flex-col gap-1">
              <span className="label">{tr("governance.riskdetail.titolare")}</span>
              <EntitySelect kind="member" noneLabel="—" className="field" value={f.owner} onChange={(v) => setF({ ...f, owner: v })} />
            </label>
          </>
        )}
      </div>
      <label className="flex flex-col gap-1">
        <span className="label">{tr("governance.riskdetail.piano_di_trattamento")}</span>
        <textarea className="field min-h-20" value={f.plan} onChange={(e) => setF({ ...f, plan: e.target.value })} placeholder={tr("governance.riskdetail.controlli_da_introdurre_chi_li")} />
      </label>
      <div className="flex gap-2">
        <button className="btn btn-primary" disabled={save.isPending}>{tr("governance.compliancesections.salva")}</button>
        <button type="button" className="btn" onClick={onDone}>{tr("governance.compliance.annulla")}</button>
      </div>
      <ErrorNote error={save.error} />
    </form>
  );
}

function AcceptForm({ refKey, onDone }: { refKey: string; onDone: () => void }) {
  const refresh = useRefresh(refKey);
  const [until, setUntil] = useState("");
  const [comment, setComment] = useState("");
  const run = useMutation({
    mutationFn: () => riskApi.requestAcceptance(refKey, until, comment),
    onSuccess: () => {
      refresh();
      onDone();
    },
  });
  return (
    <form
      className="flex flex-col gap-3 border-l-2 border-accent pl-4"
      onSubmit={(e) => {
        e.preventDefault();
        run.mutate();
      }}
    >
      <p className="m-0 text-[13px] text-muted">{tr("governance.riskdetail.l_approvazione_segue_la_politica")}</p>
      <div className="flex flex-wrap gap-3">
        <label className="flex w-48 flex-col gap-1">
          <span className="label">{tr("governance.riskdetail.accettato_fino_al")}</span>
          <input className="field" type="date" required value={until} onChange={(e) => setUntil(e.target.value)} />
        </label>
        <label className="flex min-w-0 flex-[1_1_260px] flex-col gap-1">
          <span className="label">{tr("governance.requirements.motivazione")}</span>
          <input className="field" value={comment} onChange={(e) => setComment(e.target.value)} />
        </label>
      </div>
      <div className="flex gap-2">
        <button className="btn btn-primary" disabled={run.isPending}>{tr("governance.requirements.chiedi_l_approvazione")}</button>
        <button type="button" className="btn" onClick={onDone}>{tr("governance.compliance.annulla")}</button>
      </div>
      <ErrorNote error={run.error} />
    </form>
  );
}

function LinkPicker({ placeholder, onPick, busy }: { placeholder: string; onPick: (ref: string) => void; busy?: boolean }) {
  const [v, setV] = useState("");
  return (
    <form
      className="flex flex-wrap items-end gap-2"
      onSubmit={(e) => {
        e.preventDefault();
        if (v.trim()) onPick(v.trim());
        setV("");
      }}
    >
      <input className="field max-w-xs font-mono" aria-label={placeholder} placeholder={placeholder} value={v} onChange={(e) => setV(e.target.value.toUpperCase())} />
      <button className="btn" disabled={busy || !v.trim()}>{tr("governance.riskdetail.collega")}</button>
    </form>
  );
}

function RiskLinks({ r, refKey }: { r: RiskView; refKey: string }) {
  const refresh = useRefresh(refKey);
  const link = useMutation({ mutationFn: (t: string) => riskApi.linkRisk(refKey, t), onSuccess: refresh });
  const unlink = useMutation({ mutationFn: (t: string) => riskApi.unlinkRisk(refKey, t), onSuccess: refresh });
  return (
    <section className="flex flex-col gap-3" aria-label={tr("governance.riskdetail.collegamenti_del_rischio")}>
      <SectionTitle>{tr("governance.riskdetail.esposti_al_rischio")}</SectionTitle>
      <RefList items={r.targets} empty={tr("governance.riskdetail.nessuna_applicazione_asset_o_fornitore")} onRemove={r.can.edit ? (x) => unlink.mutate(x.key) : undefined} busy={unlink.isPending} />
      {r.can.edit && <LinkPicker placeholder={tr("governance.riskdetail.chiave_es_arc_3_o")} onPick={(k) => link.mutate(k)} busy={link.isPending} />}
      <RiskControlsPanel refKey={refKey} onUnlink={r.can.edit ? (k) => unlink.mutate(k) : undefined} busy={unlink.isPending} />
      <ErrorNote error={link.error ?? unlink.error} />
    </section>
  );
}

// ---------------------------------------------------------------------
// Incidente

const LOSS: Record<string, string> = { get availability() { return tr("governance.riskdetail.disponibilita"); }, get integrity() { return tr("governance.riskdetail.integrita"); }, get confidentiality() { return tr("governance.riskdetail.riservatezza"); }, get authenticity() { return tr("governance.riskdetail.autenticita"); } };

function IncidentDetail({ refKey }: { refKey: string }) {
  const view = useQuery({ queryKey: ["risk", "incident", refKey], queryFn: () => riskApi.incident(refKey) });
  const [editing, setEditing] = useState(false);
  const [move, setMove] = useState<PhaseMove | null>(null);
  const x = view.data;
  if (view.error) return <ErrorNote error={view.error} />;
  if (!x) return <Loading className="m-0 text-muted" />;
  const manage = x.can.manage;
  return (
    <>
      {move && <PhaseMovePanel move={move} x={x} refKey={refKey} onClose={() => setMove(null)} />}
      <section className="flex flex-col gap-2" aria-label={tr("governance.riskdetail.impatto")}>
        <SectionTitle aside={<Edit on={manage && !editing} onClick={() => setEditing(true)} />}>{tr("governance.riskdetail.impatto_e_criteri")}</SectionTitle>
        {editing ? (
          <ImpactForm
            x={x}
            refKey={refKey}
            onDone={(saved) => {
              setEditing(false);
              if (saved?.phase_move) setMove(saved.phase_move);
            }}
          />
        ) : (
          <Facts>
            <Fact k={tr("governance.riskdetail.rilevato")}>{dateTime(x.detected_at)}</Fact>
            <Fact k={tr("governance.riskdetail.classificato")}>{dateTime(x.classified_at)}</Fact>
            <Fact k={tr("governance.riskdetail.contenuto")}>{dateTime(x.contained_at)}</Fact>
            <Fact k={tr("governance.riskdetail.risolto")}>{dateTime(x.resolved_at)}</Fact>
            <Fact k={tr("governance.riskdetail.durata")}>
              <span className="tabular">{tr("governance.riskdetail.ore", { duration_hours: x.duration_hours })}</span>
              {x.downtime_minutes != null && <span className="text-muted tabular">{tr("governance.riskdetail.fermo_min", { downtime_minutes: x.downtime_minutes })}</span>}
            </Fact>
            <Fact k={tr("governance.riskdetail.clienti_coinvolti")}>
              <span className="tabular">
                {x.clients_affected?.toLocaleString(loc()) ?? "—"}
                {x.clients_affected_pct != null && ` (${x.clients_affected_pct.toLocaleString(loc())}%)`}
              </span>
            </Fact>
            <Fact k={tr("governance.riskdetail.perdita_di_dati")}>{x.data_loss.length ? x.data_loss.map((d) => LOSS[d] ?? d).join(", ") : tr("governance.riskdetail.nessuna")}</Fact>
            <Fact k={tr("governance.riskdetail.impatto_economico_2")}>{euroCents(x.economic_impact_cents)}</Fact>
            <Fact k={tr("governance.riskdetail.stati_membri")}>{x.member_states ?? "—"}</Fact>
            <Fact k={tr("governance.riskdetail.reputazione")}>{x.reputational ? tr("governance.riskdetail.si") : tr("governance.riskdetail.no")}</Fact>
            <Fact k={tr("governance.riskdetail.causa")}>
              {x.cause_name || "—"}
              {x.cause_provider && (
                <>
                  {" · "}
                  <Link to="/e/$key" params={{ key: x.cause_provider.key }} className="text-accent">
                    {x.cause_provider.title}
                  </Link>
                </>
              )}
              {x.recurring && <span className="text-muted">{tr("governance.riskdetail.ricorrente")}</span>}
            </Fact>
            <Fact k={tr("governance.compliance.responsabile")}>{x.lead ?? "—"}</Fact>
          </Facts>
        )}
      </section>
      <Classification x={x} refKey={refKey} />
      <Notifications x={x} refKey={refKey} />
      <Impacted x={x} refKey={refKey} />
      <Timeline x={x} refKey={refKey} />
      <PostmortemSection x={x} refKey={refKey} />
    </>
  );
}

function num(v: string): number | undefined {
  if (v.trim() === "") return undefined;
  const n = Number(v.replace(",", "."));
  return Number.isFinite(n) ? n : undefined;
}

/**
 * Dopo aver registrato la data di risoluzione: dice se l'incidente è passato alla fase di
 * risoluzione, oppure chiede in quale fase portarlo (il processo non lo consente da lì).
 */
function PhaseMovePanel({ move, x, refKey, onClose }: { move: PhaseMove; x: IncidentView; refKey: string; onClose: () => void }) {
  const refresh = useRefresh(refKey);
  const go = useMutation({
    mutationFn: (to: string) => api.transition(x.element.id, to),
    onSuccess: () => {
      refresh();
      onClose();
    },
  });
  return (
    <section
      role="status"
      aria-label={tr("governance.riskdetail.fase_dopo_la_risoluzione")}
      className={`flex flex-col gap-2 border-l-4 px-3 py-3 ${move.status === "moved" ? "border-ok" : "border-flag"} bg-sel`}
    >
      <p className="m-0 text-[13.5px] font-semibold">{move.message}</p>
      {move.status === "choose" && move.options.length > 0 && (
        <div className="flex flex-wrap gap-2" role="group" aria-label={tr("governance.riskdetail.in_quale_fase_portare_l")}>
          {move.options.map((o) => (
            <button key={o.to} className="btn min-h-[44px] justify-center" disabled={go.isPending} onClick={() => go.mutate(o.to)}>
              {o.label} → {o.to}
            </button>
          ))}
        </div>
      )}
      <button className="self-start text-[12.5px] text-muted underline" onClick={onClose}>
        {move.status === "choose" ? tr("governance.riskdetail.lascia_la_fase_com_e") : tr("governance.compliance.chiudi")}
      </button>
      <ErrorNote error={go.error} />
    </section>
  );
}

function ImpactForm({ x, refKey, onDone }: { x: IncidentView; refKey: string; onDone: (saved?: IncidentView) => void }) {
  const refresh = useRefresh(refKey);
  const providers = useQuery({ queryKey: ["risk", "providers", ""], queryFn: () => riskApi.providers(""), staleTime: 60_000 });
  const [f, setF] = useState({
    detected: toLocalInput(x.detected_at),
    contained: toLocalInput(x.contained_at),
    resolved: toLocalInput(x.resolved_at),
    clients: x.clients_affected?.toString() ?? "",
    pct: x.clients_affected_pct?.toString() ?? "",
    downtime: x.downtime_minutes?.toString() ?? "",
    loss: x.data_loss,
    euro: x.economic_impact_cents != null ? String(x.economic_impact_cents / 100) : "",
    states: x.member_states?.toString() ?? "",
    reputational: x.reputational,
    recurring: x.recurring,
    cause: x.cause ?? "",
    provider: x.cause_provider?.key ?? "",
    lead: x.lead_id ?? "",
  });
  const save = useMutation({
    mutationFn: () => {
      const body: IncidentInput = {
        detected_at: fromLocalInput(f.detected),
        clients_affected: num(f.clients),
        clients_affected_pct: num(f.pct),
        downtime_minutes: num(f.downtime),
        data_loss: f.loss,
        economic_impact_cents: num(f.euro) != null ? Math.round(num(f.euro)! * 100) : undefined,
        member_states: num(f.states),
        reputational: f.reputational,
        recurring: f.recurring,
        cause: f.cause,
      };
      // La causa «fornitore terzo» si sceglie dall'elenco dei fornitori ICT; con un'altra causa il fornitore si toglie.
      if (f.cause === "provider") body.cause_provider = f.provider;
      if (f.contained) body.contained_at = fromLocalInput(f.contained);
      if (f.resolved) body.resolved_at = fromLocalInput(f.resolved);
      if (f.lead && f.lead !== x.lead_id) body.lead_id = f.lead;
      return riskApi.updateIncident(refKey, body);
    },
    onSuccess: (saved) => {
      refresh();
      onDone(saved);
    },
  });
  const field = (label: string, k: keyof typeof f, props: Record<string, unknown> = {}) => (
    <label className="flex w-44 flex-col gap-1">
      <span className="label">{label}</span>
      <input className="field" value={f[k] as string} onChange={(e) => setF({ ...f, [k]: e.target.value })} {...props} />
    </label>
  );
  return (
    <form
      className="flex flex-col gap-3"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      <div className="flex flex-wrap gap-3">
        {field(tr("governance.riskdetail.rilevato_il"), "detected", { type: "datetime-local", required: true, className: "field w-full" /* i18n-ok: classi CSS */ })}
        {field(tr("governance.riskdetail.contenuto_il"), "contained", { type: "datetime-local" })}
        {field(tr("governance.riskdetail.risolto_il"), "resolved", { type: "datetime-local" })}
      </div>
      <div className="flex flex-wrap gap-3">
        {field(tr("governance.riskdetail.clienti_coinvolti"), "clients", { inputMode: "numeric" })}
        {field(tr("governance.riskdetail.clienti"), "pct", { inputMode: "decimal" })}
        {field(tr("governance.riskdetail.fermo_minuti"), "downtime", { inputMode: "numeric" })}
        {field(tr("governance.riskdetail.impatto_economico"), "euro", { inputMode: "decimal" })}
        {field(tr("governance.riskdetail.stati_membri"), "states", { inputMode: "numeric" })}
      </div>
      <fieldset className="m-0 flex flex-wrap gap-x-4 gap-y-1 border-0 p-0">
        <legend className="label mb-1">{tr("governance.riskdetail.perdita_di_dati")}</legend>
        {Object.entries(LOSS).map(([k, v]) => (
          <label key={k} className="flex items-baseline gap-1.5 text-[13px]">
            <input type="checkbox" checked={f.loss.includes(k)} onChange={() => setF({ ...f, loss: f.loss.includes(k) ? f.loss.filter((y) => y !== k) : [...f.loss, k] })} />
            {v}
          </label>
        ))}
      </fieldset>
      <div className="flex flex-wrap items-end gap-3">
        <label className="flex w-52 flex-col gap-1">
          <span className="label">{tr("governance.riskdetail.causa")}</span>
          <Select className="field" value={f.cause} onChange={(e) => setF({ ...f, cause: e.target.value })}>
            <option value="">—</option>
            {Object.entries(x.options.cause).map(([k, v]) => (
              <option key={k} value={k}>
                {v}
              </option>
            ))}
          </Select>
        </label>
        {f.cause === "provider" && (
          <label className="flex w-56 flex-col gap-1">
            <span className="label">{tr("governance.riskdetail.fornitore")}</span>
            <Select className="field" value={f.provider} onChange={(e) => setF({ ...f, provider: e.target.value })}>
              <option value="">{tr("governance.riskdetail.scegli_il_fornitore")}</option>
              {providers.data?.items.map((p) => (
                <option key={p.id} value={p.key}>
                  {p.name} ({p.key})
                </option>
              ))}
            </Select>
          </label>
        )}
        <label className="flex w-48 flex-col gap-1">
          <span className="label">{tr("governance.compliance.responsabile")}</span>
          <EntitySelect kind="member" noneLabel="—" className="field" value={f.lead} onChange={(v) => setF({ ...f, lead: v })} />
        </label>
        <label className="flex items-center gap-2 pb-2 text-[13px]">
          <input type="checkbox" checked={f.reputational} onChange={(e) => setF({ ...f, reputational: e.target.checked })} />{tr("governance.riskdetail.impatto_reputazionale")}</label>
        <label className="flex items-center gap-2 pb-2 text-[13px]">
          <input type="checkbox" checked={f.recurring} onChange={(e) => setF({ ...f, recurring: e.target.checked })} />{tr("governance.riskdetail.ricorrente_2")}</label>
      </div>
      <p className="m-0 text-[12.5px] text-muted">{tr("governance.riskdetail.se_l_incidente_e_gia")}</p>
      <div className="flex gap-2">
        <button className="btn btn-primary" disabled={save.isPending}>{tr("governance.compliancesections.salva")}</button>
        <button type="button" className="btn" onClick={() => onDone()}>{tr("governance.compliance.annulla")}</button>
      </div>
      <ErrorNote error={save.error} />
    </form>
  );
}

function Classification({ x, refKey }: { x: IncidentView; refKey: string }) {
  const refresh = useRefresh(refKey);
  const preview = useMutation({ mutationFn: () => riskApi.classify(refKey, true) });
  const run = useMutation({ mutationFn: () => riskApi.classify(refKey, false), onSuccess: refresh });
  const results = preview.data?.results ?? x.classification;
  return (
    <section className="flex flex-col gap-2" aria-label={tr("governance.riskdetail.classificazione_normativa")}>
      <SectionTitle
        aside={
          x.can.manage ? (
            <span className="flex gap-3 text-[12px]">
              <button className="text-accent underline" disabled={preview.isPending} onClick={() => preview.mutate()}>{tr("governance.riskdetail.anteprima")}</button>
              {x.classified_at && (
                <button className="text-accent underline" disabled={run.isPending} onClick={() => run.mutate()}>{tr("governance.riskdetail.riclassifica")}</button>
              )}
            </span>
          ) : undefined
        }
      >{tr("governance.riskdetail.classificazione_normativa")}</SectionTitle>
      {!x.classified_at && !preview.data && (
        <p className="m-0 text-[12.5px] text-muted">{tr("governance.riskdetail.non_ancora_classificato_la_classificazione")}</p>
      )}
      {preview.data && <p className="m-0 text-[12.5px] text-muted">{tr("governance.riskdetail.anteprima_sui_dati_attuali_non")}</p>}
      {results.map((r) => (
        <div key={r.key} className="flex flex-col gap-1 border-b border-hair pb-2">
          <p className="m-0 flex flex-wrap items-baseline gap-x-3 text-[13px]">
            <span className="font-semibold">{r.name}</span>
            <span className={r.positive ? "border border-flag px-1 text-[11px] font-semibold text-flag" : "text-muted"}>{r.outcome}</span>
          </p>
          <ul className="m-0 flex list-none flex-wrap gap-x-4 gap-y-0.5 p-0 text-[12.5px]">
            {r.criteria.map((c) => (
              <li key={c.key} className={c.met ? "font-medium" : "text-muted"}>
                <span aria-hidden="true">{c.met ? "■" : "□"}</span> {c.name}
                {c.primary && <span className="text-muted">{tr("governance.riskdetail.principale")}</span>}
                <span className="sr-only">{c.met ? tr("governance.riskdetail.soddisfatto") : tr("governance.riskdetail.non_soddisfatto")}</span>
              </li>
            ))}
          </ul>
          {r.reference && <p className="m-0 text-[11.5px] text-muted">{r.reference}</p>}
        </div>
      ))}
      <ErrorNote error={preview.error ?? run.error} />
    </section>
  );
}

function Notifications({ x, refKey }: { x: IncidentView; refKey: string }) {
  if (x.notifications.length === 0) return null;
  return (
    <section id="notifiche" className="flex scroll-mt-4 flex-col gap-2" aria-label={tr("governance.riskdetail.notifiche_normative")}>
      <SectionTitle>{tr("governance.riskdetail.notifiche_normative")}</SectionTitle>
      {x.notifications.map((n) => (
        <NotificationCard key={n.id} n={n} refKey={refKey} manage={x.can.manage} />
      ))}
    </section>
  );
}

// Bozza DORA con l'assistente (AI, ondata 5): caricata solo aprendo una notifica DORA; senza AI
// mostra al più le versioni salvate (che esistono solo se l'assistente è stato usato).
const DoraNoticeAssist = lazy(() => import("./GrcAssistant").then((m) => ({ default: m.DoraNoticeAssist })));

function NotificationCard({ n, refKey, manage }: { n: Notification; refKey: string; manage: boolean }) {
  const refresh = useRefresh(refKey);
  const [open, setOpen] = useState(false);
  const [content, setContent] = useState(n.content);
  const [ref, setRef] = useState("");
  const [channel, setChannel] = useState("");
  const [at, setAt] = useState("");
  const [note, setNote] = useState("");
  const done = n.status === "submitted" || n.status === "not_required";
  const draft = useMutation({ mutationFn: () => riskApi.saveDraft(n.id, content), onSuccess: refresh });
  const submit = useMutation({
    mutationFn: () => riskApi.submit(n.id, { content, reference: ref, channel, submitted_at: fromLocalInput(at) }),
    onSuccess: () => {
      refresh();
      setOpen(false);
    },
  });
  const skip = useMutation({ mutationFn: () => riskApi.notRequired(n.id, note), onSuccess: refresh });
  return (
    <article className={`flex flex-col gap-2 border-l-2 pl-3 ${done ? "border-hair" : "border-flag"}`}>
      <p className="m-0 flex flex-wrap items-baseline gap-x-3 gap-y-0.5 text-[13px]">
        <span className="font-semibold">{n.stage_name}</span>
        <span className="text-muted">{n.scheme_name}</span>
        <span className={done ? "text-ok" : "text-muted"}>{NOTIFICATION_STATUS[n.status]}</span>
        <span className="ml-auto">
          {done ? (
            <span className="text-[12.5px] text-muted tabular">
              {n.status === "submitted" ? tr("governance.riskdetail.inviata", { dateTime: dateTime(n.submitted_at), p: n.reference ? ` · ${n.reference}` : "" }) : n.note}
            </span>
          ) : (
            <>
              {n.due_at && <span className="mr-2 text-[12.5px] text-muted tabular">{tr("governance.riskdetail.entro", { dateTime: dateTime(n.due_at) })}</span>}
              <Countdown due={n.due_at} />
            </>
          )}
        </span>
      </p>
      {n.hint && !done && <p className="m-0 text-[12.5px] text-muted">{n.hint}</p>}
      {manage && !done && !open && (
        <button className="self-start text-[12.5px] text-accent underline" onClick={() => setOpen(true)}>
          {n.status === "draft" ? tr("governance.riskdetail.riprendi_la_bozza") : tr("governance.riskdetail.prepara")}
        </button>
      )}
      {done && n.content && (
        <details className="text-[13px]">
          <summary className="cursor-pointer text-muted">{tr("governance.riskdetail.contenuto_inviato")}</summary>
          <p className="m-0 mt-1 whitespace-pre-wrap">{n.content}</p>
        </details>
      )}
      {open && (
        <div className="flex flex-col gap-2">
          {n.scheme_key === "dora" && (
            <Suspense fallback={null}>
              <DoraNoticeAssist
                notification={n.id}
                onSaved={(c) => {
                  setContent(c);
                  refresh();
                }}
              />
            </Suspense>
          )}
          <label className="flex flex-col gap-1">
            <span className="label">{tr("governance.riskdetail.contenuto")}</span>
            <textarea className="field min-h-28" value={content} onChange={(e) => setContent(e.target.value)} />
          </label>
          <div className="flex flex-wrap gap-2">
            <button className="btn" disabled={draft.isPending} onClick={() => draft.mutate()}>{tr("governance.riskdetail.salva_la_bozza")}</button>
            <button className="btn" onClick={() => setOpen(false)}>{tr("governance.compliance.chiudi")}</button>
          </div>
          <fieldset className="m-0 flex flex-wrap items-end gap-2 border-0 p-0">
            <legend className="label mb-1">{tr("governance.riskdetail.registra_l_invio_fatto_fuori")}</legend>
            <input className="field w-52" type="datetime-local" aria-label={tr("governance.riskdetail.inviata_il")} value={at} onChange={(e) => setAt(e.target.value)} />
            <input className="field w-40" placeholder={tr("governance.riskdetail.protocollo")} aria-label={tr("governance.compliance.riferimento")} value={ref} onChange={(e) => setRef(e.target.value)} />
            <input className="field w-48" placeholder={tr("governance.architecture.canale")} aria-label={tr("governance.riskdetail.canale")} value={channel} onChange={(e) => setChannel(e.target.value)} />
            <button className="btn btn-primary" disabled={submit.isPending} onClick={() => submit.mutate()}>{tr("governance.riskdetail.registra_l_invio")}</button>
          </fieldset>
          <fieldset className="m-0 flex flex-wrap items-end gap-2 border-0 p-0">
            <legend className="label mb-1">{tr("governance.riskdetail.oppure_non_dovuta")}</legend>
            <input className="field min-w-0 flex-[1_1_260px]" placeholder={tr("governance.riskdetail.perche_non_e_dovuta")} aria-label={tr("governance.requirements.motivazione")} value={note} onChange={(e) => setNote(e.target.value)} />
            <button className="btn" disabled={skip.isPending} onClick={() => skip.mutate()}>{tr("governance.riskdetail.dichiara_non_dovuta")}</button>
          </fieldset>
          <ErrorNote error={draft.error ?? submit.error ?? skip.error} />
        </div>
      )}
    </article>
  );
}

function Impacted({ x, refKey }: { x: IncidentView; refKey: string }) {
  const refresh = useRefresh(refKey);
  const link = useMutation({ mutationFn: (t: string) => riskApi.linkIncident(refKey, t), onSuccess: refresh });
  const unlink = useMutation({ mutationFn: (t: string) => riskApi.unlinkIncident(refKey, t), onSuccess: refresh });
  return (
    <section className="flex flex-col gap-2" aria-label={tr("governance.riskdetail.applicazioni_e_servizi_impattati")}>
      <SectionTitle>{tr("governance.riskdetail.applicazioni_e_servizi_impattati")}</SectionTitle>
      <RefList items={x.impacted} empty={tr("governance.riskdetail.nessuna_applicazione_o_asset_collegato")} onRemove={x.can.manage ? (r) => unlink.mutate(r.key) : undefined} busy={unlink.isPending} />
      {x.risks.length > 0 && (
        <>
          <h4 className="label m-0 mt-1 text-muted">{tr("governance.riskdetail.rischi_concretizzati")}</h4>
          <RefList items={x.risks} empty="" onRemove={x.can.manage ? (r) => unlink.mutate(r.key) : undefined} />
        </>
      )}
      {x.can.manage && <LinkPicker placeholder={tr("governance.riskdetail.applicazione_asset_o_rischio")} onPick={(k) => link.mutate(k)} busy={link.isPending} />}
      <ErrorNote error={link.error ?? unlink.error} />
    </section>
  );
}

function Timeline({ x, refKey }: { x: IncidentView; refKey: string }) {
  const refresh = useRefresh(refKey);
  const [text, setText] = useState("");
  const [kind, setKind] = useState("update");
  const [at, setAt] = useState("");
  const add = useMutation({
    mutationFn: () => riskApi.addEvent(refKey, { kind, text, at: fromLocalInput(at) }),
    onSuccess: () => {
      setText("");
      setAt("");
      refresh();
    },
  });
  return (
    <section className="flex flex-col gap-2" aria-label={tr("governance.riskdetail.timeline")}>
      <SectionTitle>{tr("governance.riskdetail.timeline")}</SectionTitle>
      <ol className="m-0 flex list-none flex-col p-0">
        {x.events.map((e) => (
          <li key={e.id} className="grid grid-cols-[110px_minmax(0,1fr)] gap-x-3 border-b border-hair py-1.5 text-[13px] sm:grid-cols-[130px_110px_minmax(0,1fr)]">
            <span className="font-mono text-[12px] text-muted tabular">{dateTime(e.at)}</span>
            <span className={`text-[12px] ${e.kind === "notification" ? "font-semibold text-flag" : "text-muted"}`}>{EVENT_KINDS[e.kind] ?? e.kind}</span>
            <span className="col-span-2 min-w-0 sm:col-span-1">
              {e.text}
              {!e.system && e.created_by && <span className="text-muted"> · {e.created_by}</span>}
            </span>
          </li>
        ))}
      </ol>
      {x.can.manage && (
        <form
          className="flex flex-wrap items-end gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            add.mutate();
          }}
        >
          <Select className="field w-40" aria-label={tr("governance.riskdetail.tipo_di_evento")} value={kind} onChange={(e) => setKind(e.target.value)}>
            {["update", "contained", "communication", "action"].map((k) => (
              <option key={k} value={k}>
                {EVENT_KINDS[k]}
              </option>
            ))}
          </Select>
          <input className="field w-52" type="datetime-local" aria-label={tr("governance.riskdetail.quando_vuoto_adesso")} value={at} onChange={(e) => setAt(e.target.value)} />
          <input className="field min-w-0 flex-[1_1_240px]" required aria-label={tr("governance.riskdetail.evento")} placeholder={tr("governance.riskdetail.cosa_e_successo")} value={text} onChange={(e) => setText(e.target.value)} />
          <button className="btn" disabled={add.isPending}>{tr("governance.compliance.aggiungi")}</button>
        </form>
      )}
      <ErrorNote error={add.error} />
    </section>
  );
}

function PostmortemSection({ x, refKey }: { x: IncidentView; refKey: string }) {
  const refresh = useRefresh(refKey);
  const [editing, setEditing] = useState(false);
  const [pm, setPm] = useState(x.postmortem ?? { summary: "", root_cause: "", lessons: "" });
  const save = useMutation({
    mutationFn: () => riskApi.savePostmortem(refKey, pm),
    onSuccess: () => {
      refresh();
      setEditing(false);
    },
  });
  const [a, setA] = useState({ title: "", assignee: "", team: "", due: "" });
  const addAction = useMutation({
    mutationFn: () => riskApi.addAction(refKey, { title: a.title, assignee_id: a.assignee || undefined, team_id: a.team || undefined, due: a.due || undefined }),
    onSuccess: () => {
      setA({ title: "", assignee: "", team: "", due: "" });
      refresh();
    },
  });
  const area = (label: string, k: keyof typeof pm, rows = "min-h-16") => (
    <label className="flex flex-col gap-1">
      <span className="label">{label}</span>
      <textarea className={`field ${rows}`} value={pm[k]} onChange={(e) => setPm({ ...pm, [k]: e.target.value })} />
    </label>
  );
  const show = (label: string, v: string): ReactNode => (
    <Fact k={label}>
      <span className="whitespace-pre-wrap">{v || "—"}</span>
    </Fact>
  );
  return (
    <section className="flex flex-col gap-2" aria-label={tr("governance.riskdetail.postmortem")}>
      <SectionTitle aside={<Edit on={x.can.manage && !editing} onClick={() => setEditing(true)} label={x.postmortem ? tr("governance.compliancesections.modifica") : tr("governance.riskdetail.compila")} />}>{tr("governance.riskdetail.postmortem")}</SectionTitle>
      {editing ? (
        <form
          className="flex flex-col gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          {area(tr("governance.riskdetail.riepilogo"), "summary", "min-h-20")}
          {area(tr("governance.riskdetail.causa_principale"), "root_cause")}
          {area(tr("governance.riskdetail.lezioni_apprese"), "lessons")}
          <div className="flex gap-2">
            <button className="btn btn-primary" disabled={save.isPending}>{tr("governance.compliancesections.salva")}</button>
            <button type="button" className="btn" onClick={() => setEditing(false)}>{tr("governance.compliance.annulla")}</button>
          </div>
          <ErrorNote error={save.error} />
        </form>
      ) : x.postmortem ? (
        <Facts>
          {show(tr("governance.riskdetail.riepilogo"), x.postmortem.summary)}
          {show(tr("governance.riskdetail.causa_principale"), x.postmortem.root_cause)}
          {show(tr("governance.riskdetail.lezioni_apprese"), x.postmortem.lessons)}
          <Fact k={tr("governance.riskdetail.compilato")}>
            {dateTime(x.postmortem_at)}
            {x.postmortem_by && ` · ${x.postmortem_by}`}
          </Fact>
        </Facts>
      ) : (
        <p className="m-0 text-[12.5px] text-muted">{tr("governance.riskdetail.non_ancora_compilato")}</p>
      )}
      <h4 className="label m-0 mt-2">{tr("governance.riskdetail.azioni_correttive")}</h4>
      {x.actions.length === 0 ? (
        <p className="m-0 text-[12.5px] text-muted">{tr("governance.riskdetail.nessuna_azione_correttiva")}</p>
      ) : (
        <ul className="m-0 flex list-none flex-col p-0">
          {x.actions.map((t) => (
            <li key={t.id} className="flex flex-wrap items-baseline gap-x-3 border-b border-hair py-1.5 text-[13px]">
              <Link to="/e/$key" params={{ key: t.key }} className="font-mono text-[12px] text-accent no-underline hover:underline">
                {t.key}
              </Link>
              <span className="min-w-[11rem] flex-1">{t.title}</span>
              <span className="text-[12px] text-muted">{t.assignee ?? tr("governance.riskdetail.senza_assegnatario")}</span>
              {t.due && <span className="text-[12px] text-muted tabular">{tr("governance.riskdetail.entro_2", { date: date(t.due) })}</span>}
              <span className={`text-[12px] ${t.state_category === "done" ? "text-ok" : "text-muted"}`}>{t.state}</span>
            </li>
          ))}
        </ul>
      )}
      {x.can.manage && (
        <form
          className="flex flex-wrap items-end gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            addAction.mutate();
          }}
        >
          <input className="field min-w-0 flex-[1_1_240px]" required aria-label={tr("governance.riskdetail.azione_correttiva")} placeholder={tr("governance.riskdetail.nuova_azione_correttiva")} value={a.title} onChange={(e) => setA({ ...a, title: e.target.value })} />
          <EntitySelect kind="team" className="field w-44" aria-label={tr("governance.requirements.team")} noneLabel={tr("governance.riskdetail.spazio_incidenti")} value={a.team} onChange={(v) => setA({ ...a, team: v })} />
          <EntitySelect kind="member" noneLabel={tr("governance.riskdetail.assegnatario_2")} className="field w-44" aria-label={tr("governance.riskdetail.assegnatario")} value={a.assignee} onChange={(v) => setA({ ...a, assignee: v })} />
          <input className="field w-40" type="date" aria-label={tr("governance.riskdetail.entro_3")} value={a.due} onChange={(e) => setA({ ...a, due: e.target.value })} />
          <button className="btn" disabled={addAction.isPending}>{tr("governance.riskdetail.crea_il_task")}</button>
        </form>
      )}
      <ErrorNote error={addAction.error} />
    </section>
  );
}

// ---------------------------------------------------------------------
// Fornitore ICT

function ProviderDetail({ refKey }: { refKey: string }) {
  const view = useQuery({ queryKey: ["risk", "provider", refKey], queryFn: () => riskApi.provider(refKey) });
  const [editing, setEditing] = useState(false);
  const p = view.data;
  if (view.error) return <ErrorNote error={view.error} />;
  if (!p) return <Loading className="m-0 text-muted" />;
  return (
    <>
      <section className="flex flex-col gap-2" aria-label={tr("governance.riskdetail.il_fornitore")}>
        <SectionTitle aside={<Edit on={p.can.edit && !editing} onClick={() => setEditing(true)} />}>{tr("governance.riskdetail.il_fornitore")}</SectionTitle>
        {p.critical && (
          <p className="m-0 self-start border border-flag px-2 py-0.5 text-[12.5px] font-semibold text-flag">{tr("governance.riskdetail.fornitore_critico_dora", { critical_reason: p.critical_reason })}</p>
        )}
        {editing ? (
          <ProviderForm p={p} refKey={refKey} onDone={() => setEditing(false)} />
        ) : (
          <Facts>
            <Fact k={tr("governance.riskdetail.ragione_sociale")}>{p.legal_name}</Fact>
            <Fact k={tr("governance.riskdetail.codice_lei")}>
              <span className="font-mono">{p.lei ?? "—"}</span>
            </Fact>
            <Fact k={tr("governance.riskdetail.paese")}>{p.country ?? "—"}</Fact>
            <Fact k={tr("governance.compliance.tipo")}>{p.provider_type ?? "—"}</Fact>
            <Fact k={tr("governance.riskdetail.intragruppo_2")}>{p.is_intragroup ? tr("common.yes") : tr("common.no")}</Fact>
            <Fact k={tr("governance.riskdetail.sostituibilita")}>{p.substitutability_name || "—"}</Fact>
            <Fact k={tr("governance.riskdetail.referente")}>{p.owner ?? "—"}</Fact>
          </Facts>
        )}
      </section>
      <ProviderApps p={p} refKey={refKey} />
      <section className="flex flex-col gap-3" aria-label={tr("governance.riskdetail.contratti")}>
        <SectionTitle>{tr("governance.riskdetail.contratti")}</SectionTitle>
        {p.contract_list.length === 0 && <p className="m-0 text-[12.5px] text-muted">{tr("governance.riskdetail.nessun_contratto")}</p>}
        {p.contract_list.map((c) => (
          <ContractBlock key={c.id} c={c} p={p} refKey={refKey} />
        ))}
        {p.can.edit && <ContractForm p={p} refKey={refKey} />}
      </section>
      {p.risks.length > 0 && (
        <section className="flex flex-col gap-2">
          <SectionTitle>{tr("governance.riskdetail.rischi_sul_fornitore")}</SectionTitle>
          <RefList items={p.risks} empty="" />
        </section>
      )}
    </>
  );
}

function ProviderForm({ p, refKey, onDone }: { p: ProviderView; refKey: string; onDone: () => void }) {
  const refresh = useRefresh(refKey);
  const [f, setF] = useState({
    legal_name: p.legal_name,
    lei: p.lei ?? "",
    country: p.country ?? "",
    provider_type: p.provider_type ?? "",
    is_intragroup: p.is_intragroup,
    substitutability: p.substitutability_name,
    owner: p.owner_id ?? "",
  });
  const save = useMutation({
    mutationFn: () =>
      riskApi.updateProvider(refKey, {
        legal_name: f.legal_name,
        lei: f.lei,
        country: f.country,
        provider_type: f.provider_type,
        is_intragroup: f.is_intragroup,
        substitutability: f.substitutability,
        ...(f.owner && f.owner !== p.owner_id ? { owner_id: f.owner } : {}),
      }),
    onSuccess: () => {
      refresh();
      onDone();
    },
  });
  return (
    <form
      className="flex flex-col gap-3"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      <div className="flex flex-wrap gap-3">
        <label className="flex min-w-0 flex-[1_1_220px] flex-col gap-1">
          <span className="label">{tr("governance.riskdetail.ragione_sociale")}</span>
          <input className="field" value={f.legal_name} onChange={(e) => setF({ ...f, legal_name: e.target.value })} />
        </label>
        <label className="flex w-56 flex-col gap-1">
          <span className="label">{tr("governance.riskdetail.codice_lei")}</span>
          <input className="field font-mono" maxLength={20} value={f.lei} onChange={(e) => setF({ ...f, lei: e.target.value.toUpperCase() })} />
        </label>
        <label className="flex w-20 flex-col gap-1">
          <span className="label">{tr("governance.riskdetail.paese")}</span>
          <input className="field font-mono uppercase" maxLength={2} value={f.country} onChange={(e) => setF({ ...f, country: e.target.value })} />
        </label>
      </div>
      <div className="flex flex-wrap items-end gap-3">
        <label className="flex min-w-0 flex-[1_1_200px] flex-col gap-1">
          <span className="label">{tr("governance.compliance.tipo")}</span>
          <input className="field" value={f.provider_type} onChange={(e) => setF({ ...f, provider_type: e.target.value })} />
        </label>
        <label className="flex w-40 flex-col gap-1">
          <span className="label">{tr("governance.riskdetail.sostituibilita")}</span>
          <Select className="field" value={f.substitutability} onChange={(e) => setF({ ...f, substitutability: e.target.value })}>
            <option value="">—</option>
            {(["facile", "media", "difficile", "impossibile"] as const).map((s) => (
              <option key={s} value={s}>{{ facile: tr("governance.riskdetail.facile"), media: tr("governance.pipelinepolicy.media"), difficile: tr("governance.riskdetail.difficile"), impossibile: tr("governance.riskdetail.impossibile") }[s]}</option>
            ))}
          </Select>
        </label>
        <label className="flex w-48 flex-col gap-1">
          <span className="label">{tr("governance.riskdetail.referente")}</span>
          <EntitySelect kind="member" noneLabel="—" className="field" value={f.owner} onChange={(v) => setF({ ...f, owner: v })} />
        </label>
        <label className="flex items-center gap-2 pb-2 text-[13px]">
          <input type="checkbox" checked={f.is_intragroup} onChange={(e) => setF({ ...f, is_intragroup: e.target.checked })} />{tr("governance.riskdetail.intragruppo")}</label>
      </div>
      <div className="flex gap-2">
        <button className="btn btn-primary" disabled={save.isPending}>{tr("governance.compliancesections.salva")}</button>
        <button type="button" className="btn" onClick={onDone}>{tr("governance.compliance.annulla")}</button>
      </div>
      <ErrorNote error={save.error} />
    </form>
  );
}

function ProviderApps({ p, refKey }: { p: ProviderView; refKey: string }) {
  const refresh = useRefresh(refKey);
  const [app, setApp] = useState("");
  const link = useMutation({ mutationFn: () => riskApi.linkApp(refKey, app), onSuccess: () => (setApp(""), refresh()) });
  const unlink = useMutation({ mutationFn: (k: string) => riskApi.unlinkApp(refKey, k), onSuccess: refresh });
  const linked = new Set(p.application_refs.map((a) => a.id));
  return (
    <section className="flex flex-col gap-2" aria-label={tr("governance.riskdetail.applicazioni_servite")}>
      <SectionTitle>{tr("governance.riskdetail.applicazioni_servite")}</SectionTitle>
      <RefList items={p.application_refs} empty={tr("governance.riskdetail.nessuna_applicazione_collegata")} onRemove={p.can.edit ? (r) => unlink.mutate(r.key) : undefined} busy={unlink.isPending} />
      {p.can.edit && (
        <form
          className="flex flex-wrap gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            link.mutate();
          }}
        >
          <EntitySelect kind="application" className="field max-w-sm" aria-label={tr("governance.requirements.applicazione")} required noneLabel={tr("governance.riskdetail.scegli_un_applicazione")} filter={(o) => !linked.has(o.value)} value={app} onChange={setApp} />
          <button className="btn" disabled={link.isPending}>{tr("governance.riskdetail.collega")}</button>
        </form>
      )}
      <ErrorNote error={link.error ?? unlink.error} />
    </section>
  );
}

function ContractBlock({ c, p, refKey }: { c: Contract; p: ProviderView; refKey: string }) {
  const [editing, setEditing] = useState(false);
  const refresh = useRefresh(refKey);
  const del = useMutation({ mutationFn: () => riskApi.deleteContract(c.id), onSuccess: refresh });
  if (editing) return <ContractForm p={p} refKey={refKey} c={c} onDone={() => setEditing(false)} />;
  return (
    <article className="flex flex-col gap-1">
      <p className="m-0 flex flex-wrap items-baseline gap-x-3 border-b border-hair pb-1 text-[13px]">
        <span className="font-mono font-semibold">{c.reference}</span>
        <span className="min-w-[11rem] flex-1">{c.service_type || c.services || "—"}</span>
        {c.end_date && (
          <span className={`tabular ${c.expired ? "font-semibold text-danger" : c.expiring ? "font-semibold text-flag" : "text-muted"}`}>
            {c.expired ? tr("governance.riskdetail.scaduto_il") : tr("governance.riskdetail.scade_il")} {date(c.end_date)}
          </span>
        )}
        {p.can.edit && (
          <span className="flex gap-3 text-[12px]">
            <button className="text-accent underline" onClick={() => setEditing(true)}>{tr("governance.riskdetail.modifica")}</button>
            <button
              className="text-muted underline hover:text-danger"
              disabled={del.isPending}
              onClick={() => {
                if (window.confirm(tr("governance.riskdetail.cancellare_il_contratto_non_si", { reference: c.reference }))) del.mutate();
              }}
            >{tr("governance.riskdetail.cancella")}</button>
          </span>
        )}
      </p>
      <Facts>
        <Fact k={tr("governance.devsecops.servizi")}>{c.services || "—"}</Fact>
        <Fact k={tr("governance.riskdetail.funzioni_supportate")}>
          {c.functions || "—"}
          {c.supports_critical_function && <span className="ml-1.5 border border-flag px-1 text-[10.5px] font-semibold text-flag">{tr("governance.riskdetail.funzione_critica")}</span>}
        </Fact>
        <Fact k={tr("governance.riskdetail.criticita")}>{c.criticality_name || "—"}</Fact>
        <Fact k={tr("governance.riskdetail.durata")}>
          {date(c.start_date)} – {date(c.end_date)}
          {c.notice_days != null && <span className="text-muted">{tr("governance.riskdetail.preavviso_giorni", { notice_days: c.notice_days })}</span>}
          {c.auto_renewal && <span className="text-muted">{tr("governance.riskdetail.rinnovo_automatico")}</span>}
        </Fact>
        <Fact k={tr("governance.riskdetail.costo_annuo")}>{c.annual_cost_cents != null ? `${(c.annual_cost_cents / 100).toLocaleString(loc())} ${c.currency ?? ""}` : "—"}</Fact>
        <Fact k={tr("governance.riskdetail.dati_conservati_in")}>{c.data_locations.join(", ") || "—"}</Fact>
        <Fact k={tr("governance.riskdetail.dati_trattati_in")}>{c.processing_locations.join(", ") || "—"}</Fact>
        <Fact k={tr("governance.riskdetail.subappalti")}>{c.subcontractors.length ? c.subcontractors.map((s) => `${s.name}${s.country ? ` (${s.country})` : ""}${s.service ? `: ${s.service}` : ""}`).join("; ") : "—"}</Fact>
        <Fact k={tr("governance.riskdetail.strategia_di_uscita")}>
          <span className="whitespace-pre-wrap">{c.exit_strategy || "—"}</span>
          {c.exit_tested_on && <span className="block text-[12px] text-muted">{tr("governance.riskdetail.provata_il", { date: date(c.exit_tested_on) })}</span>}
        </Fact>
        <Fact k={tr("governance.riskdetail.legge_applicabile")}>{c.governing_law ?? "—"}</Fact>
        <Fact k={tr("governance.compliance.applicazioni")}>{c.applications.map((a) => a.title).join(", ") || "—"}</Fact>
      </Facts>
      <ErrorNote error={del.error} />
    </article>
  );
}

const list = (s: string) =>
  s
    .split(/[,\s]+/)
    .map((x) => x.trim().toUpperCase())
    .filter(Boolean);

function ContractForm({ p, refKey, c, onDone }: { p: ProviderView; refKey: string; c?: Contract; onDone?: () => void }) {
  const refresh = useRefresh(refKey);
  const [open, setOpen] = useState(!!c);
  const [f, setF] = useState({
    reference: c?.reference ?? "",
    service_type: c?.service_type ?? "",
    services: c?.services ?? "",
    functions: c?.functions ?? "",
    critical: c?.supports_critical_function ?? false,
    criticality: c?.criticality ?? "",
    start: c?.start_date ?? "",
    end: c?.end_date ?? "",
    notice: c?.notice_days?.toString() ?? "",
    cost: c?.annual_cost_cents != null ? String(c.annual_cost_cents / 100) : "",
    currency: c?.currency ?? "EUR",
    data: c?.data_locations.join(", ") ?? "",
    processing: c?.processing_locations.join(", ") ?? "",
    subs: c?.subcontractors.map((s) => [s.name, s.country ?? "", s.service ?? ""].join(" | ")).join("\n") ?? "",
    exit: c?.exit_strategy ?? "",
    tested: c?.exit_tested_on ?? "",
    law: c?.governing_law ?? "",
    renewal: c?.auto_renewal ?? false,
    apps: c?.applications.map((a) => a.id) ?? [],
  });
  const save = useMutation({
    mutationFn: () => {
      const body: ContractInput = {
        reference: f.reference,
        service_type: f.service_type,
        services: f.services,
        functions: f.functions,
        supports_critical_function: f.critical,
        criticality: f.criticality,
        start_date: f.start,
        end_date: f.end,
        notice_days: num(f.notice),
        annual_cost_cents: num(f.cost) != null ? Math.round(num(f.cost)! * 100) : undefined,
        currency: f.currency,
        data_locations: list(f.data),
        processing_locations: list(f.processing),
        subcontractors: f.subs
          .split("\n")
          .map((l) => l.split("|").map((x) => x.trim()))
          .filter((x) => x[0])
          .map(([name, country, service]) => ({ name, country, service })),
        exit_strategy: f.exit,
        exit_tested_on: f.tested,
        governing_law: f.law,
        auto_renewal: f.renewal,
        applications: f.apps,
      };
      return c ? riskApi.updateContract(c.id, body) : riskApi.createContract(refKey, body);
    },
    onSuccess: () => {
      refresh();
      if (c) onDone?.();
      else setOpen(false);
    },
  });
  if (!open)
    return (
      <button className="btn self-start" onClick={() => setOpen(true)}>{tr("governance.riskdetail.nuovo_contratto")}</button>
    );
  const input = (label: string, k: keyof typeof f, props: Record<string, unknown> = {}, w = "w-44") => (
    <label className={`flex ${w} flex-col gap-1`}>
      <span className="label">{label}</span>
      <input className="field" value={f[k] as string} onChange={(e) => setF({ ...f, [k]: e.target.value })} {...props} />
    </label>
  );
  return (
    <form
      className="flex flex-col gap-3 border-l-2 border-accent pl-4"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      <div className="flex flex-wrap gap-3">
        {input(tr("governance.compliance.riferimento"), "reference", { required: true }, "w-40")}
        {input(tr("governance.riskdetail.tipo_di_servizio_ict"), "service_type", {}, "min-w-0 flex-[1_1_200px]" /* i18n-ok: classi CSS */)}
        {input(tr("governance.riskdetail.inizio"), "start", { type: "date" })}
        {input(tr("governance.requirements.scadenza"), "end", { type: "date" })}
        {input(tr("governance.riskdetail.preavviso_giorni_2"), "notice", { inputMode: "numeric" }, "w-36")}
      </div>
      <label className="flex flex-col gap-1">
        <span className="label">{tr("governance.riskdetail.servizi_forniti")}</span>
        <textarea className="field min-h-12" value={f.services} onChange={(e) => setF({ ...f, services: e.target.value })} />
      </label>
      <div className="flex flex-wrap items-end gap-3">
        {input(tr("governance.riskdetail.funzioni_supportate"), "functions", {}, "min-w-0 flex-[1_1_260px]" /* i18n-ok: classi CSS */)}
        <label className="flex w-36 flex-col gap-1">
          <span className="label">{tr("governance.riskdetail.criticita")}</span>
          <Select className="field" value={f.criticality} onChange={(e) => setF({ ...f, criticality: e.target.value })}>
            <option value="">—</option>
            {Object.entries({ low: tr("governance.pipelinepolicy.bassa"), medium: tr("governance.pipelinepolicy.media"), high: tr("governance.pipelinepolicy.alta"), critical: tr("governance.pipelinepolicy.critica") }).map(([k, v]) => (
              <option key={k} value={k}>
                {v}
              </option>
            ))}
          </Select>
        </label>
        <label className="flex items-center gap-2 pb-2 text-[13px]">
          <input type="checkbox" checked={f.critical} onChange={(e) => setF({ ...f, critical: e.target.checked })} />{tr("governance.riskdetail.supporta_una_funzione_critica_o")}</label>
      </div>
      <div className="flex flex-wrap gap-3">
        {input(tr("governance.riskdetail.paesi_dei_dati_conservazione"), "data", { placeholder: "IT, DE" }, "w-56")}
        {input(tr("governance.riskdetail.paesi_dei_dati_trattamento"), "processing", { placeholder: "IT" }, "w-56")}
        {input(tr("governance.riskdetail.costo_annuo"), "cost", { inputMode: "decimal" }, "w-36")}
        {input(tr("governance.riskdetail.valuta"), "currency", { maxLength: 3 }, "w-20")}
        {input(tr("governance.riskdetail.legge_applicabile"), "law", {}, "w-40")}
      </div>
      <label className="flex flex-col gap-1">
        <span className="label">{tr("governance.riskdetail.subappaltatori_uno_per_riga_nome")}</span>
        <textarea className="field min-h-12 font-mono text-[12.5px]" value={f.subs} onChange={(e) => setF({ ...f, subs: e.target.value })} />
      </label>
      <div className="flex flex-wrap items-end gap-3">
        <label className="flex min-w-0 flex-[1_1_320px] flex-col gap-1">
          <span className="label">{tr("governance.riskdetail.strategia_di_uscita")}</span>
          <textarea className="field min-h-12" value={f.exit} onChange={(e) => setF({ ...f, exit: e.target.value })} />
        </label>
        {input(tr("governance.riskdetail.uscita_provata_il"), "tested", { type: "date" })}
        <label className="flex items-center gap-2 pb-2 text-[13px]">
          <input type="checkbox" checked={f.renewal} onChange={(e) => setF({ ...f, renewal: e.target.checked })} />{tr("governance.riskdetail.rinnovo_automatico_2")}</label>
      </div>
      {p.application_refs.length > 0 && (
        <fieldset className="m-0 flex flex-wrap gap-x-4 gap-y-1 border-0 p-0">
          <legend className="label mb-1">{tr("governance.riskdetail.applicazioni_coperte")}</legend>
          {p.application_refs.map((a) => (
            <label key={a.id} className="flex items-baseline gap-1.5 text-[13px]">
              <input type="checkbox" checked={f.apps.includes(a.id)} onChange={() => setF({ ...f, apps: f.apps.includes(a.id) ? f.apps.filter((x) => x !== a.id) : [...f.apps, a.id] })} />
              {a.title}
            </label>
          ))}
        </fieldset>
      )}
      <div className="flex gap-2">
        <button className="btn btn-primary" disabled={save.isPending}>
          {c ? tr("governance.compliancesections.salva") : tr("governance.riskdetail.aggiungi_il_contratto")}
        </button>
        <button type="button" className="btn" onClick={() => (c ? onDone?.() : setOpen(false))}>{tr("governance.compliance.annulla")}</button>
      </div>
      <ErrorNote error={save.error} />
    </form>
  );
}
