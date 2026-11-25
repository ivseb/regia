// Controlli GRC vivi (ondata 3, proposta 4): nella scheda del controllo il riquadro «Proposta del
// sistema» (regola di efficacia, motivo, dati, conferma o non conferma con la nota) e, nella
// scheda del rischio, i controlli di trattamento con efficacia, proposta ed evidenze, gli
// incidenti e le CVE che l'hanno concretizzato. Lo stato ufficiale cambia solo con la conferma.
import { Select } from "../select";
import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ErrorNote, SectionTitle, date, Loading } from "../ui";
import { EFFECTIVENESS } from "../grcApi";
import { livingApi, type EffRuleInput, type FactValue, type Proposal, type ProposalState } from "../grcLivingApi";
import { ConditionBuilder } from "./processEditor/ConditionBuilder";
import { phrase } from "../casa/gruppi";
import { tr } from "../i18n/core";
import type { FactInfo } from "../api";

const FACT_KEYS = [
  "vuln_critiche_scadute",
  "vuln_scadute",
  "gate_superati_90g",
  "gate_falliti_90g",
  "rilasci_approvati_90g",
  "rilasci_senza_sbom",
  "giorni_riesame_accessi",
  "evidenze_valide",
  "evidenze_scadute",
] as const;
type FactKey = (typeof FACT_KEYS)[number];

export const factName = (k: string): string => {
  switch (k as FactKey) {
    case "vuln_critiche_scadute": return tr("governance.controlli.fact_vuln_critiche_scadute");
    case "vuln_scadute": return tr("governance.controlli.fact_vuln_scadute");
    case "gate_superati_90g": return tr("governance.controlli.fact_gate_superati_90g");
    case "gate_falliti_90g": return tr("governance.controlli.fact_gate_falliti_90g");
    case "rilasci_approvati_90g": return tr("governance.controlli.fact_rilasci_approvati_90g");
    case "rilasci_senza_sbom": return tr("governance.controlli.fact_rilasci_senza_sbom");
    case "giorni_riesame_accessi": return tr("governance.controlli.fact_giorni_riesame_accessi");
    case "evidenze_valide": return tr("governance.controlli.fact_evidenze_valide");
    case "evidenze_scadute": return tr("governance.controlli.fact_evidenze_scadute");
  }
  return k;
};

/** I dati offerti al costruttore delle condizioni: quelli calcolati per un controllo. */
const CONTROL_FACTS = (): FactInfo[] =>
  FACT_KEYS.map((key) => ({ key, name: factName(key), kind: "number", example: "0", source: "modulo" }));

const stateName = (s: ProposalState): string => {
  switch (s) {
    case "effective": return tr("governance.controlli.state_effective");
    case "at_risk": return tr("governance.controlli.state_at_risk");
    case "not_assessed": return tr("governance.controlli.state_not_assessed");
  }
  return tr("governance.controlli.state_none");
};

const SOURCES = (): { key: string; label: string }[] => [
  { key: "release", label: tr("governance.controlli.src_release") },
  { key: "gate", label: tr("governance.controlli.src_gate") },
  { key: "sbom", label: tr("governance.controlli.src_sbom") },
  { key: "rule", label: tr("governance.controlli.src_rule") },
];

/** Dove si vede il dato da cui nasce il fatto. */
function FactLink({ k, children }: { k: string; children: React.ReactNode }) {
  const cls = "touch-44 inline-flex items-center text-accent underline";
  if (k.startsWith("vuln_")) return <Link to="/devsecops/vulnerabilita" className={cls}>{children}</Link>;
  if (k.startsWith("gate_") || k.startsWith("rilasci_")) return <Link to="/devsecops" className={cls}>{children}</Link>;
  return <Link to="/conformita" search={{ tab: "evidenze" }} className={cls}>{children}</Link>;
}

function FactList({ facts }: { facts: FactValue[] }) {
  return (
    <ul className="m-0 flex list-none flex-col p-0">
      {facts.map((f) => (
        <li key={f.key} className="flex flex-wrap items-baseline justify-between gap-x-3 border-b border-hair py-0.5 text-[13px]">
          <FactLink k={f.key}>{factName(f.key)}</FactLink>
          <span className="font-mono tabular">{f.value === null ? tr("governance.controlli.fact_none") : f.value}</span>
        </li>
      ))}
    </ul>
  );
}

function StateMark({ state }: { state: ProposalState }) {
  const dot = state === "effective" ? "bg-ok" : state === "at_risk" ? "bg-danger" : "border border-muted bg-transparent";
  return (
    <span className="inline-flex items-center gap-1.5 font-semibold">
      <span className={`size-2 shrink-0 ${dot}`} aria-hidden="true" />
      {stateName(state)}
    </span>
  );
}

function useRefreshAll(refKey: string) {
  const qc = useQueryClient();
  return () => {
    qc.invalidateQueries({ queryKey: ["grc"] });
    qc.invalidateQueries({ queryKey: ["element", refKey] });
    qc.invalidateQueries({ queryKey: ["activity"] });
    qc.invalidateQueries({ queryKey: ["risk"] });
  };
}

export function ProposalBox({ refKey }: { refKey: string }) {
  const q = useQuery({ queryKey: ["grc", "proposal", refKey], queryFn: () => livingApi.proposal(refKey) });
  const refresh = useRefreshAll(refKey);
  const [mode, setMode] = useState<"" | "confirm" | "decline" | "rule">("");
  const [note, setNote] = useState("");
  const [override, setOverride] = useState("");
  const [synced, setSynced] = useState<number | null>(null);
  const done = () => {
    setMode("");
    setNote("");
    setOverride("");
    refresh();
  };
  const decide = useMutation({
    mutationFn: (decision: "confirm" | "not_confirm") =>
      livingApi.decide(refKey, { decision, note, effectiveness: decision === "not_confirm" && override ? override : undefined }),
    onSuccess: done,
  });
  const sync = useMutation({
    mutationFn: () => livingApi.sync(refKey),
    onSuccess: (r) => {
      setSynced(r.created);
      refresh();
    },
  });
  const p = q.data;
  if (q.error) return <ErrorNote error={q.error} />;
  if (!p) return <Loading className="m-0 text-muted" />;
  const reason =
    p.state === "effective" ? tr("governance.controlli.reason_effective")
    : p.state === "at_risk" ? tr("governance.controlli.reason_at_risk")
    : p.state === "not_assessed" ? tr("governance.controlli.reason_not_assessed")
    : tr("governance.controlli.reason_none");
  const official = EFFECTIVENESS[p.official] ?? p.official;
  const proposed = p.proposed ? (EFFECTIVENESS[p.proposed] ?? p.proposed) : "";
  return (
    <section className="flex flex-col gap-3" aria-label={tr("governance.controlli.box_title")}>
      <SectionTitle
        aside={
          p.can.edit && mode !== "rule" ? (
            <button className="touch-44 text-[12.5px] text-accent" onClick={() => setMode("rule")}>
              {p.rule ? tr("governance.controlli.rule_edit") : tr("governance.controlli.rule_define")}
            </button>
          ) : undefined
        }
      >{tr("governance.controlli.box_title")}</SectionTitle>
      <p className="m-0 text-[12.5px] text-muted">{tr("governance.controlli.box_intro")}</p>

      {mode === "rule" ? (
        <RuleForm refKey={refKey} p={p} onDone={done} onCancel={() => setMode("")} />
      ) : (
        <>
          <dl className="m-0 grid grid-cols-[130px_minmax(0,1fr)] text-[13.5px]">
            <dt className="border-b border-hair py-1.5 text-muted">{tr("governance.controlli.proposed")}</dt>
            <dd className="m-0 border-b border-hair py-1.5" data-testid="proposal-state">
              <StateMark state={p.state} />
              {proposed && <span className="text-muted"> · {proposed}</span>}
            </dd>
            <dt className="border-b border-hair py-1.5 text-muted">{tr("governance.controlli.official")}</dt>
            <dd className="m-0 border-b border-hair py-1.5">{official}</dd>
          </dl>
          <p className="m-0" data-testid="proposal-reason">{reason}</p>
          {p.rule && (p.rule.effective_when || p.rule.at_risk_when) && (
            <ul className="m-0 flex list-none flex-col gap-0.5 p-0 text-[12.5px] text-muted">
              {p.rule.at_risk_when && <li><span className="font-semibold">{tr("governance.controlli.rule_at_risk_when")}</span> {phrase(p.rule.at_risk_when, Object.fromEntries(FACT_KEYS.map((k) => [k, factName(k)])))}</li>}
              {p.rule.effective_when && <li><span className="font-semibold">{tr("governance.controlli.rule_effective_when")}</span> {phrase(p.rule.effective_when, Object.fromEntries(FACT_KEYS.map((k) => [k, factName(k)])))}</li>}
            </ul>
          )}
          {p.facts.length > 0 && (
            <div className="flex flex-col gap-1">
              <span className="label text-muted">{tr("governance.controlli.data_label")}</span>
              <FactList facts={p.facts} />
              <span className="text-[12px] text-muted">
                {p.everywhere ? tr("governance.controlli.perimeter_all", { count: p.perimeter }) : tr("governance.controlli.perimeter_apps", { count: p.perimeter })}
              </span>
            </div>
          )}
          {p.dismissed && <p className="m-0 text-[12.5px] text-muted">{tr("governance.controlli.dismissed")}</p>}
          {!p.pending && !p.dismissed && p.proposed && <p className="m-0 text-[12.5px] text-muted">{tr("governance.controlli.aligned")}</p>}

          {p.can.decide && p.pending && mode === "" && (
            <div className="flex flex-wrap gap-2">
              <button className="btn btn-primary" onClick={() => setMode("confirm")}>{tr("governance.controlli.conferma")}</button>
              <button className="btn" onClick={() => setMode("decline")}>{tr("governance.controlli.non_conferma")}</button>
            </div>
          )}
          {(mode === "confirm" || mode === "decline") && (
            <form
              className="flex flex-col gap-2 border-t-2 border-rule pt-3"
              onSubmit={(e) => {
                e.preventDefault();
                decide.mutate(mode === "confirm" ? "confirm" : "not_confirm");
              }}
            >
              <label className="flex flex-col gap-1">
                <span className="label text-muted">{tr("governance.controlli.note_label")}</span>
                <textarea className="field" rows={3} value={note} required={mode === "decline"} onChange={(e) => setNote(e.target.value)} aria-describedby="proposal-note-help" />
                <span id="proposal-note-help" className="text-[12px] text-muted">
                  {mode === "decline" ? tr("governance.controlli.note_required") : tr("governance.controlli.note_optional")}
                </span>
              </label>
              {mode === "decline" && (
                <label className="flex flex-col gap-1">
                  <span className="label text-muted">{tr("governance.controlli.override_label")}</span>
                  <Select className="field" value={override} onChange={(e) => setOverride(e.target.value)}>
                    <option value="">{tr("governance.controlli.override_keep")}</option>
                    <option value="effective">{EFFECTIVENESS.effective}</option>
                    <option value="partial">{EFFECTIVENESS.partial}</option>
                    <option value="ineffective">{EFFECTIVENESS.ineffective}</option>
                  </Select>
                </label>
              )}
              <ErrorNote error={decide.error} />
              <div className="flex flex-wrap gap-2">
                <button className="btn btn-primary" disabled={decide.isPending || (mode === "decline" && !note.trim())}>
                  {tr("governance.controlli.registra")}
                </button>
                <button type="button" className="btn" onClick={() => setMode("")}>{tr("governance.controlli.annulla")}</button>
              </div>
            </form>
          )}
          {p.can.edit && p.rule && p.rule.sources.length > 0 && (
            <div className="flex flex-wrap items-center gap-2">
              <button className="btn" onClick={() => sync.mutate()} disabled={sync.isPending}>{tr("governance.controlli.sync")}</button>
              {synced !== null && <span role="status" className="text-[12.5px] text-muted">{tr("governance.controlli.synced", { count: synced })}</span>}
              <ErrorNote error={sync.error} />
            </div>
          )}
          {p.decisions.length > 0 && (
            <details className="text-[12.5px]">
              <summary className="touch-44 inline-flex cursor-pointer items-center text-muted">{tr("governance.controlli.history")}</summary>
              <ul className="m-0 flex list-none flex-col gap-1 p-0 pt-1">
                {p.decisions.map((d) => (
                  <li key={d.id}>
                    {tr("governance.controlli.decision_line", {
                      proposed: stateName(d.proposed as ProposalState),
                      decision: d.decision === "confirmed" ? tr("governance.controlli.decision_confirmed") : tr("governance.controlli.decision_not_confirmed"),
                      by: d.by || "—",
                      date: date(d.at),
                    })}
                    {d.note && <span className="block text-muted">{d.note}</span>}
                  </li>
                ))}
              </ul>
            </details>
          )}
        </>
      )}
    </section>
  );
}

function RuleForm({ refKey, p, onDone, onCancel }: { refKey: string; p: Proposal; onDone: () => void; onCancel: () => void }) {
  const [r, setR] = useState<EffRuleInput>({
    name: p.rule?.name ?? "",
    effective_when: p.rule?.effective_when ?? "",
    at_risk_when: p.rule?.at_risk_when ?? "",
    sources: p.rule?.sources ?? [],
    active: p.rule?.active ?? true,
  });
  const save = useMutation({ mutationFn: () => livingApi.setRule(refKey, r), onSuccess: onDone });
  const remove = useMutation({ mutationFn: () => livingApi.removeRule(refKey), onSuccess: onDone });
  const facts = CONTROL_FACTS();
  return (
    <form
      className="flex flex-col gap-3"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      <p className="m-0 text-[12.5px] text-muted">{tr("governance.controlli.rule_help")}</p>
      <label className="flex flex-col gap-1">
        <span className="label text-muted">{tr("governance.controlli.rule_name")}</span>
        <input className="field" value={r.name} onChange={(e) => setR({ ...r, name: e.target.value })} />
      </label>
      <ConditionBuilder label={tr("governance.controlli.rule_effective_when")} value={r.effective_when} onChange={(v) => setR((x) => ({ ...x, effective_when: v }))} facts={facts} empty={tr("governance.controlli.rule_never")} />
      <ConditionBuilder label={tr("governance.controlli.rule_at_risk_when")} value={r.at_risk_when} onChange={(v) => setR((x) => ({ ...x, at_risk_when: v }))} facts={facts} empty={tr("governance.controlli.rule_never")} />
      <fieldset className="m-0 flex min-w-0 flex-col gap-1 border-0 p-0">
        <legend className="label mb-1 p-0 text-muted">{tr("governance.controlli.rule_sources")}</legend>
        {SOURCES().map((s) => (
          <label key={s.key} className="flex min-h-[44px] items-center gap-2 md:min-h-0">
            <input
              type="checkbox"
              checked={r.sources.includes(s.key)}
              onChange={(e) => setR({ ...r, sources: e.target.checked ? [...r.sources, s.key] : r.sources.filter((x) => x !== s.key) })}
            />
            {s.label}
          </label>
        ))}
      </fieldset>
      <label className="flex min-h-[44px] items-center gap-2 md:min-h-0">
        <input type="checkbox" checked={r.active} onChange={(e) => setR({ ...r, active: e.target.checked })} />
        {tr("governance.controlli.rule_active")}
      </label>
      <ErrorNote error={save.error ?? remove.error} />
      <div className="flex flex-wrap gap-2">
        <button className="btn btn-primary" disabled={save.isPending}>{tr("governance.controlli.rule_save")}</button>
        <button type="button" className="btn" onClick={onCancel}>{tr("governance.controlli.annulla")}</button>
        {p.rule && (
          <button type="button" className="btn btn-danger" disabled={remove.isPending} onClick={() => remove.mutate()}>
            {tr("governance.controlli.rule_remove")}
          </button>
        )}
      </div>
    </form>
  );
}

// ---------------------------------------------------------------------
// Scheda del rischio: controlli di trattamento, incidenti e CVE

export function RiskControlsPanel({ refKey, onUnlink, busy }: { refKey: string; onUnlink?: (key: string) => void; busy?: boolean }) {
  const q = useQuery({ queryKey: ["grc", "risk-controls", refKey], queryFn: () => livingApi.riskControls(refKey) });
  const v = q.data;
  if (q.error) return <ErrorNote error={q.error} />;
  if (!v) return <Loading className="m-0 text-muted" />;
  return (
    <section className="flex flex-col gap-2" aria-label={tr("governance.controlli.risk_title")}>
      <SectionTitle>{tr("governance.controlli.risk_title")}</SectionTitle>
      <ul className="m-0 flex list-none flex-col p-0">
        {v.controls.map((c) => (
          <li key={c.key} className="flex flex-col gap-0.5 border-b border-hair py-2">
            <span className="flex flex-wrap items-center justify-between gap-x-3">
              <Link to="/e/$key" params={{ key: c.key }} className="touch-44 inline-flex items-center gap-1.5 no-underline">
                <span className="font-mono text-[12px] text-accent">{c.key}</span>
                <span className="text-fg">{c.title}</span>
              </Link>
              {onUnlink && (
                <button className="touch-44 text-[12px] text-muted underline hover:text-danger" disabled={busy} onClick={() => onUnlink(c.key)}>
                  {tr("governance.controlli.scollega")}
                </button>
              )}
            </span>
            {c.detail && (
              <span className="flex flex-wrap gap-x-3 text-[12.5px] text-muted">
                <span>{tr("governance.controlli.risk_effectiveness")}: {EFFECTIVENESS[c.effectiveness] ?? c.effectiveness}</span>
                <span>{tr("governance.controlli.risk_evidence", { count: c.valid_evidence })}</span>
                {c.next_expiry && <span>{tr("governance.controlli.risk_expiry", { date: date(c.next_expiry) })}</span>}
                {!c.operational && <span>{tr("governance.controlli.risk_not_operational")}</span>}
                {c.proposed && <span className="text-flag">{tr("governance.controlli.risk_pending", { state: EFFECTIVENESS[c.proposed] ?? c.proposed })}</span>}
              </span>
            )}
          </li>
        ))}
        {v.controls.length === 0 && <li className="py-1.5 text-muted">{tr("governance.controlli.risk_no_controls")}</li>}
      </ul>
      {v.incidents.length > 0 && (
        <>
          <span className="label text-muted">{tr("governance.controlli.risk_incidents")}</span>
          <ul className="m-0 flex list-none flex-col p-0">
            {v.incidents.map((i) => (
              <li key={i.key} className="border-b border-hair">
                <Link to="/e/$key" params={{ key: i.key }} className="touch-44 inline-flex items-center gap-1.5 no-underline">
                  <span className="font-mono text-[12px] text-accent">{i.key}</span> <span className="text-fg">{i.title}</span>
                </Link>
              </li>
            ))}
          </ul>
        </>
      )}
      {v.cves.length > 0 && (
        <>
          <span className="label text-muted">{tr("governance.controlli.risk_cves")}</span>
          <ul className="m-0 flex list-none flex-col p-0">
            {v.cves.map((c) => (
              <li key={`${c.exception}-${c.cve}`} className="border-b border-hair py-1.5 font-mono text-[12.5px]">
                {tr("governance.controlli.risk_cve_line", { cve: c.cve, severity: c.severity, app: c.application, n: c.exception })}
              </li>
            ))}
          </ul>
        </>
      )}
    </section>
  );
}
