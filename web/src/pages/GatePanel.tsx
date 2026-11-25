// Gate di rilascio (piano, passo 21, fase 4; replica/devsecops.md §3.5):
// - GatePanel: scheda «Gate di rilascio» dell'applicazione e della richiesta, con lo stato per
//   ambiente adesso, i motivi, lo storico delle decisioni e gli override;
// - GateRecent: gli ultimi esiti, nella pagina /devsecops.
// La pipeline chiama POST /api/v1/devsecops/gate prima del deploy; qui si vede cosa ha risposto.
import { useState } from "react";
import { Link, useRouter } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { gateApi, type DecisionView, type GateCell, type GateOutcome, type GateReason, type GateResult, type OverrideView } from "../gateApi";
import { ErrorNote, SectionTitle, ago, Loading } from "../ui";
import { ProblemActions } from "./ProblemActions";
import { tr } from "../i18n/core";
import { Trans } from "../i18n/Trans";
import { loc } from "../i18n/format";

// ---------------------------------------------------------------------
// Pezzi comuni

const RESULT_TEXT: Record<GateResult, string> = { get pass() { return tr("governance.gatepanel.via_libera"); }, get warn() { return tr("governance.gatepanel.con_avvisi"); }, get fail() { return tr("governance.gatepanel.bloccato"); } };
const RESULT_MARK: Record<GateResult, string> = { pass: "✓", warn: "!", fail: "✕" };
const RESULT_TONE: Record<GateResult, string> = { pass: "border-ok text-ok", warn: "border-flag text-flag", fail: "border-danger text-danger" };
const TEXT_TONE: Record<GateResult, string> = { pass: "text-ok", warn: "text-flag", fail: "text-danger" };

export function ResultBadge({ result, overridden }: { result: GateResult; overridden?: boolean }) {
  return (
    <span className={`inline-flex items-center gap-1 self-start border px-1.5 text-[11.5px] font-semibold whitespace-nowrap ${RESULT_TONE[result]}`}>
      <span aria-hidden="true">{RESULT_MARK[result]}</span>
      {overridden ? tr("governance.gatepanel.superato_con_override") : RESULT_TEXT[result]}
    </span>
  );
}

const MODE_TEXT: Record<string, string> = { get off() { return tr("governance.gatepanel.gate_spento"); }, get warn() { return tr("governance.gatepanel.in_avviso"); }, get block() { return tr("governance.gatepanel.bloccante"); }, get none() { return tr("governance.gatepanel.senza_regole"); } };

function itLink(url: string | undefined): string | undefined {
  // I link del gate sono assoluti se il tenant ha un indirizzo pubblico: dentro l'app si usa il percorso.
  if (!url) return undefined;
  try {
    const u = new URL(url, window.location.origin);
    return u.pathname + u.search;
  } catch {
    return url;
  }
}

/**
 * Dove si chiede la deroga per un motivo di blocco: i requisiti si derogano nella scheda
 * «Requisiti» della richiesta, le vulnerabilità con un'eccezione nella loro pagina. Gli altri
 * motivi (finestra, change, controlli) non hanno una deroga: restano l'override.
 */
function derogaTarget(kind: string, requestKey?: string): { to: string; search?: Record<string, string>; label: string } | null {
  if (kind === "requirements" && requestKey) return { to: `/e/${requestKey}`, search: { scheda: "requisiti" }, label: tr("governance.gatepanel.chiedi_deroga") };
  if (kind === "vulnerabilities") return { to: "/devsecops/vulnerabilita", label: tr("governance.gatepanel.chiedi_deroga") };
  return null;
}

export function ReasonList({ reasons, onOverride, requestKey }: { reasons: GateReason[]; onOverride?: (r: GateReason) => void; requestKey?: string }) {
  const router = useRouter();
  return (
    <ul className="m-0 flex list-none flex-col gap-1.5 p-0">
      {reasons.map((r, i) => {
        const to = itLink(r.url);
        const open = r.status !== "pass" && !r.overridden;
        const deroga = open ? derogaTarget(r.kind, requestKey) : null;
        return (
          <li key={r.rule + i} className="flex items-baseline gap-2 text-[13px]">
            <span aria-hidden="true" className={`w-3 shrink-0 text-center font-semibold ${TEXT_TONE[r.status]}`}>
              {RESULT_MARK[r.status]}
            </span>
            <span className="min-w-0">
              <span className="font-semibold">{r.label}</span>
              <span className="text-muted"> · {r.status === "pass" ? tr("governance.pipelineadherence.ok") : r.status === "warn" ? tr("governance.gatepanel.avviso") : tr("governance.gatepanel.non_soddisfatta")}{r.overridden ? tr("governance.gatepanel.superata_dall_override") : ""}</span>
              <br />
              <span className={r.status === "pass" ? "text-muted" : ""}>{r.message}</span>
              {to && r.status !== "pass" && (
                <>
                  {" "}
                  <a
                    href={to}
                    className="whitespace-nowrap"
                    onClick={(e) => {
                      if (e.metaKey || e.ctrlKey || e.shiftKey) return;
                      e.preventDefault();
                      router.history.push(to);
                    }}
                  >{tr("governance.gatepanel.apri")}</a>
                </>
              )}
              {open && (onOverride || deroga) && (
                <span className="mt-1 flex flex-wrap gap-2">
                  {onOverride && (
                    <button type="button" className="btn min-h-[44px] justify-center py-1 text-[12.5px] md:min-h-0" onClick={() => onOverride(r)}>{tr("governance.gatepanel.chiedi_override")}</button>
                  )}
                  {deroga && (
                    <Link to={deroga.to} search={deroga.search as never} className="btn min-h-[44px] justify-center py-1 text-[12.5px] no-underline md:min-h-0">
                      {deroga.label}
                    </Link>
                  )}
                </span>
              )}
            </span>
          </li>
        );
      })}
    </ul>
  );
}

function when(iso: string): string {
  return new Date(iso).toLocaleString(loc(), { day: "2-digit", month: "2-digit", year: "numeric", hour: "2-digit", minute: "2-digit" });
}

// ---------------------------------------------------------------------
// Scheda dell'applicazione e della richiesta

export function GatePanel({ refKey }: { refKey: string }) {
  const panel = useQuery({ queryKey: ["gate-panel", refKey], queryFn: () => gateApi.panel(refKey) });
  const p = panel.data;
  if (panel.isPending) return <Loading className="m-0 text-muted" />;
  if (!p) return <ErrorNote error={panel.error} />;
  const isRequest = p.subject.type === "business_request";
  const apps = [...new Set(p.cells.map((c) => c.application.key))];
  return (
    <div className="flex min-w-0 flex-col gap-7">
      <p className="m-0 max-w-3xl text-muted">
        <Trans
          k="governance.gatepanel.prima_del_deploy_la_pipeline_chiede"
          params={{ scope: isRequest ? tr("governance.gatepanel.per_le_applicazioni_di_questa") : "" }}
          slots={{ now: <span className="font-semibold text-fg">{tr("governance.gatepanel.adesso")}</span> }}
        />
      </p>
      {!p.configured && (
        <div className="flex flex-col gap-2 border-l-2 border-rule pl-3">
          <p className="m-0">{tr("governance.gatepanel.nessuna_regola_del_gate_e")}</p>
          {p.can_configure && <Link to="/configurazione/gate" className="self-start">{tr("governance.gatepanel.configura_le_regole_del_gate")}</Link>}
        </div>
      )}
      {p.configured && p.cells.length === 0 && (
        <p className="m-0 text-muted">{isRequest ? tr("governance.gatepanel.la_richiesta_non_ha_impatti") : tr("governance.gatepanel.nessun_ambiente_da_valutare")}</p>
      )}
      {apps.map((k) => (
        <ApplicationStatus key={k} cells={p.cells.filter((c) => c.application.key === k)} showApp={isRequest} />
      ))}
      <Decisions items={p.decisions} refKey={refKey} showApp={isRequest} />
      <Overrides items={p.overrides} refKey={refKey} canRevoke={p.can_configure} />
      {p.can_configure && p.configured && <Link to="/configurazione/gate" className="self-start text-[12.5px]">{tr("governance.gatepanel.configurazione_gate_di_rilascio")}</Link>}
    </div>
  );
}

function ApplicationStatus({ cells, showApp }: { cells: GateCell[]; showApp: boolean }) {
  const app = cells[0].application;
  return (
    <section className="flex min-w-0 flex-col gap-2" aria-label={tr("governance.gatepanel.stato_del_gate_di", { key: app.key })}>
      <SectionTitle aside={showApp ? <Link to="/e/$key" params={{ key: app.key }} search={{ scheda: "gate" }} className="text-[12px]">{app.key} · {app.title}</Link> : undefined}>{tr("governance.gatepanel.adesso_2")}{showApp ? "" : tr("governance.gatepanel.per_ambiente")}
      </SectionTitle>
      <ul className="m-0 flex list-none flex-col p-0">
        {cells.map((c) => (
          <li key={c.environment} className="border-b border-hair py-3">
            <Cell cell={c} />
          </li>
        ))}
      </ul>
    </section>
  );
}

function Cell({ cell }: { cell: GateCell }) {
  const o = cell.outcome;
  const [open, setOpen] = useState(o.result !== "pass");
  const bad = o.reasons.filter((r) => r.status !== "pass").length;
  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <span className="min-w-[88px] font-semibold">{cell.environment}</span>
        <ResultBadge result={o.result} overridden={o.overridden} />
        <span className="text-[12px] text-muted">
          {MODE_TEXT[o.mode]}
          {cell.production ? tr("governance.gatepanel.produzione") : ""}
          {o.version ? tr("governance.gatepanel.versione", { version: o.version }) : ""}
        </span>
        <button className="ml-auto text-[12px] text-accent underline" aria-expanded={open} onClick={() => setOpen(!open)}>
          {open ? tr("governance.gatepanel.nascondi_i_motivi") : tr("governance.gatepanel.mostra_i_motivi_n", { count: o.reasons.length, extra: bad ? tr("governance.gatepanel.da_guardare", { bad }) : "" })}
        </button>
      </div>
      <p className="m-0 text-[13px] text-muted">{o.summary}</p>
      {open && <ReasonList reasons={o.reasons} requestKey={o.request?.key} />}
    </div>
  );
}

/**
 * Un solo stato per rilascio: le decisioni sullo stesso rilascio (applicazione, ambiente,
 * versione) si raggruppano e vale l'ultima; le precedenti sono la cronologia, a parte.
 * Le decisioni senza versione non si raggruppano.
 */
export function groupReleases(items: DecisionView[]): { latest: DecisionView; older: DecisionView[] }[] {
  const sorted = [...items].sort((a, b) => b.at.localeCompare(a.at));
  const groups: { latest: DecisionView; older: DecisionView[] }[] = [];
  const byRelease = new Map<string, { latest: DecisionView; older: DecisionView[] }>();
  for (const d of sorted) {
    const k = d.version ? `${d.application.key}|${d.environment}|${d.version}` : "";
    const g = k ? byRelease.get(k) : undefined;
    if (g) {
      g.older.push(d);
      continue;
    }
    const ng = { latest: d, older: [] as DecisionView[] };
    groups.push(ng);
    if (k) byRelease.set(k, ng);
  }
  return groups;
}

function ReleaseHistory({ older, refKey, showApp }: { older: DecisionView[]; refKey?: string; showApp: boolean }) {
  if (older.length === 0) return null;
  return (
    <details className="mt-2 text-[12.5px]">
      <summary className="cursor-pointer text-muted">{tr("governance.gatepanel.cronologia_di_questo_rilascio", { length: older.length })}{" "}{older.length === 1 ? tr("governance.gatepanel.decisione_precedente") : tr("governance.gatepanel.decisioni_precedenti")})
      </summary>
      <ul className="m-0 mt-1 flex list-none flex-col border-l-2 border-hair p-0 pl-3">
        {older.map((o) => (
          <li key={o.id} className="py-2">
            <DecisionRow d={o} refKey={refKey} showApp={showApp} superseded />
          </li>
        ))}
      </ul>
    </details>
  );
}

function Decisions({ items, refKey, showApp }: { items: DecisionView[]; refKey: string; showApp: boolean }) {
  const groups = groupReleases(items);
  return (
    <section className="flex min-w-0 flex-col gap-2" aria-label={tr("governance.gatepanel.decisioni_registrate")}>
      <SectionTitle>{tr("governance.gatepanel.decisioni_della_pipeline")}</SectionTitle>
      {/* Più decisioni sullo stesso rilascio: vale l'ultima, le altre sono storia. */}
      {items.length === 0 && <p className="m-0 text-muted">{tr("governance.gatepanel.la_pipeline_non_ha_ancora")}</p>}
      <ul className="m-0 flex list-none flex-col p-0">
        {groups.map(({ latest, older }) => (
          <li key={latest.id} className="border-b border-hair py-2.5">
            <DecisionRow d={latest} refKey={refKey} showApp={showApp} />
            <ReleaseHistory older={older} refKey={refKey} showApp={showApp} />
          </li>
        ))}
      </ul>
    </section>
  );
}

export function DecisionRow({ d, refKey, showApp = true, superseded = false }: { d: DecisionView; refKey?: string; showApp?: boolean; superseded?: boolean }) {
  const [open, setOpen] = useState(false);
  const [asking, setAsking] = useState(false);
  const [seed, setSeed] = useState("");
  const firstBad = d.reasons.find((r) => r.status !== "pass");
  // L'override parte dal motivo del blocco: il modulo si apre con quel motivo già citato.
  const onOverride = d.can_override
    ? (r: GateReason) => {
        setSeed(tr("governance.gatepanel.si_rilascia_lo_stesso_nonostante", { label: r.label, message: r.message }));
        setAsking(true);
      }
    : undefined;
  return (
    <div className="flex min-w-0 flex-col gap-1.5">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <ResultBadge result={d.result} overridden={d.overridden} />
        <span className="font-semibold">{d.environment}</span>
        {showApp && (
          <Link to="/e/$key" params={{ key: d.application.key }} search={{ scheda: "gate" }} className="no-underline">
            {d.application.code || d.application.key}
          </Link>
        )}
        {d.version && <span className="font-mono text-[12px]">{d.version}</span>}
        {superseded && <span className="text-[12px] text-muted" title={tr("governance.gatepanel.per_lo_stesso_rilascio_c")}>{tr("governance.gatepanel.superata")}</span>}
        {d.request && (
          <Link to="/e/$key" params={{ key: d.request.key }} className="font-mono text-[12px]">{d.request.key}</Link>
        )}
        <span className="text-[12px] text-muted" title={when(d.at)}>
          {d.actor} · {ago(d.at)}
        </span>
        <button className="ml-auto text-[12px] text-accent underline" aria-expanded={open} onClick={() => setOpen(!open)}>
          {open ? tr("governance.gatepanel.nascondi") : tr("governance.gatepanel.motivi")}
        </button>
      </div>
      {!open && firstBad && <ReasonList reasons={[firstBad]} onOverride={onOverride} requestKey={d.request?.key} />}
      {open && <ReasonList reasons={d.reasons} onOverride={onOverride} requestKey={d.request?.key} />}
      {d.override && (
        <p className="m-0 text-[12.5px] text-muted">{tr("governance.gatepanel.override", { p: OVERRIDE_TEXT[d.override.status], reason: d.override.reason, requested_by: d.override.requested_by })}</p>
      )}
      {asking && <OverrideForm decision={d.id} refKey={refKey} initialReason={seed} onDone={() => setAsking(false)} />}
      {open && d.result !== "pass" && <ProblemActions kind="gate_decision" refKey={d.id} compact />}
    </div>
  );
}

const OVERRIDE_TEXT: Record<OverrideView["status"], string> = {
  get pending() { return tr("governance.gatepanel.in_attesa_di_approvazione"); },
  get approved() { return tr("governance.gatepanel.approvato"); },
  get rejected() { return tr("governance.gatepanel.respinto"); },
  get revoked() { return tr("governance.gatepanel.ritirato"); },
  get expired() { return tr("governance.risks.scaduto"); },
};

function OverrideForm({ decision, refKey, initialReason = "", onDone }: { decision: string; refKey?: string; initialReason?: string; onDone: () => void }) {
  const qc = useQueryClient();
  const [reason, setReason] = useState(initialReason);
  const [hours, setHours] = useState("");
  const m = useMutation({
    mutationFn: () => gateApi.requestOverride(decision, reason, hours ? Number(hours) : undefined),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["gate-panel"] });
      void qc.invalidateQueries({ queryKey: ["gate-decisions"] });
      if (refKey) void qc.invalidateQueries({ queryKey: ["element", refKey] });
      onDone();
    },
  });
  return (
    <form
      className="flex max-w-xl flex-col gap-2 border-l-2 border-rule pl-3"
      onSubmit={(e) => {
        e.preventDefault();
        m.mutate();
      }}
    >
      <p className="m-0 text-[12.5px] text-muted">{tr("governance.gatepanel.un_override_supera_i_motivi")}</p>
      <label className="flex flex-col gap-1">
        <span className="label">{tr("governance.gatepanel.perche_si_rilascia_lo_stesso")}</span>
        <textarea className="field min-h-[72px]" required value={reason} onChange={(e) => setReason(e.target.value)} placeholder={tr("governance.gatepanel.es_correzione_urgente_del_pagamento")} />
      </label>
      <label className="flex w-40 flex-col gap-1">
        <span className="label">{tr("governance.gatepanel.durata_ore")}</span>
        <input className="field font-mono" inputMode="numeric" value={hours} onChange={(e) => setHours(e.target.value.replace(/\D/g, ""))} placeholder={tr("governance.gatepanel.massimo")} />
      </label>
      <span className="flex gap-2">
        <button className="btn btn-primary" disabled={m.isPending || reason.trim() === ""}>{tr("governance.requirements.chiedi_l_approvazione")}</button>
        <button type="button" className="btn" onClick={onDone}>{tr("governance.compliance.annulla")}</button>
      </span>
      <ErrorNote error={m.error} />
    </form>
  );
}

function Overrides({ items, refKey, canRevoke }: { items: OverrideView[]; refKey: string; canRevoke: boolean }) {
  const qc = useQueryClient();
  const revoke = useMutation({
    mutationFn: (id: string) => gateApi.revokeOverride(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["gate-panel", refKey] }),
  });
  if (items.length === 0) return null;
  return (
    <section className="flex min-w-0 flex-col gap-2" aria-label={tr("governance.gatepanel.override_2")}>
      <SectionTitle>{tr("governance.gatepanel.override_2")}</SectionTitle>
      <ul className="m-0 flex list-none flex-col p-0">
        {items.map((o) => (
          <li key={o.id} className="flex flex-col gap-1 border-b border-hair py-2.5">
            <span className="flex flex-wrap items-center gap-x-3 gap-y-1">
              <span className="font-semibold">{o.environment}</span>
              <span className={o.status === "approved" ? "text-ok" : o.status === "rejected" || o.status === "revoked" ? "text-danger" : "text-flag"}>{OVERRIDE_TEXT[o.status]}</span>
              {o.version && <span className="font-mono text-[12px]">{o.version}</span>}
              <span className="text-[12px] text-muted">
                {o.requested_by} · {ago(o.requested_at)}
                {o.expires_at && o.status === "approved" ? tr("governance.gatepanel.fino_al", { when: when(o.expires_at) }) : ""}
              </span>
              {canRevoke && (o.status === "pending" || o.status === "approved") && (
                <button className="ml-auto text-[12px] text-muted underline" disabled={revoke.isPending} onClick={() => revoke.mutate(o.id)}>{tr("governance.gatepanel.ritira")}</button>
              )}
            </span>
            <span className="text-[13px]">{o.reason}</span>
            {o.status === "pending" && (
              <Link to="/e/$key" params={{ key: o.application.key }} search={{ scheda: "approvazioni" }} className="self-start text-[12px]">{tr("governance.gatepanel.vedi_l_approvazione")}</Link>
            )}
          </li>
        ))}
      </ul>
      <ErrorNote error={revoke.error} />
    </section>
  );
}

// ---------------------------------------------------------------------
// Ultimi esiti nella pagina /devsecops

export function GateRecent() {
  const [onlyBad, setOnlyBad] = useState(false);
  const q = useQuery({ queryKey: ["gate-decisions", onlyBad], queryFn: () => gateApi.decisions({ limit: 12, result: onlyBad ? "fail" : undefined }) });
  const items = q.data?.items ?? [];
  const groups = groupReleases(items);
  return (
    <section className="flex min-w-0 flex-col gap-2" aria-label={tr("governance.gatepanel.gate_di_rilascio")}>
      <SectionTitle
        aside={
          <label className="flex items-center gap-1.5 text-[12px] text-muted">
            <input type="checkbox" checked={onlyBad} onChange={(e) => setOnlyBad(e.target.checked)} />{tr("governance.gatepanel.solo_i_bloccati")}</label>
        }
      >{tr("governance.gatepanel.gate_di_rilascio")}</SectionTitle>
      <ErrorNote error={q.error} />
      {q.isPending && <Loading className="m-0 text-muted" />}
      {q.data && items.length === 0 && (
        <p className="m-0 text-muted">
          {onlyBad ? tr("governance.gatepanel.nessun_rilascio_bloccato_di_recente") : tr("governance.gatepanel.nessuna_decisione_ancora_la_pipeline")}
        </p>
      )}
      <ul className="m-0 flex list-none flex-col p-0">
        {groups.map(({ latest, older }) => (
          <li key={latest.id} className="border-b border-hair py-2.5">
            <DecisionRow d={latest} />
            <ReleaseHistory older={older} showApp />
          </li>
        ))}
      </ul>
    </section>
  );
}

export type { GateOutcome };
