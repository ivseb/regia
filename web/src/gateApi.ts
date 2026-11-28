// API del gate di rilascio (piano, passo 21, fase 4; replica/devsecops.md §3.5): regole per
// ambiente, anteprima, decisioni, override, mappatura delle evidenze GRC.
// Modulo a parte, caricato con le pagine DevSecOps: il bundle iniziale non cresce.
import { call, type FactInfo } from "./api";
import { tr } from "./i18n/core";

const q = encodeURIComponent;

export type GateResult = "pass" | "warn" | "fail";
export type RuleKind = "requirements" | "vulnerabilities" | "change_approved" | "window" | "checks" | "condition";

export type GateRule = {
  id: string;
  kind: RuleKind;
  enabled: boolean;
  level: "block" | "warn";
  label?: string;
  params?: Record<string, unknown>;
};

/**
 * Parametro servicenow della regola change_approved (connettore ServiceNow in sola lettura):
 * "" non conta; "accept" basta l'approvazione in ServiceNow e un rifiuto blocca; "require"
 * serve sempre, insieme alla fase se indicata. Con "" serve almeno una fase.
 */
export type ChangeServiceNow = "" | "accept" | "require";
export type ChangeApprovedParams = { phases?: string[]; require_change?: boolean; servicenow?: ChangeServiceNow };

export type GatePolicy = {
  id?: string;
  environment: string;
  mode: "off" | "warn" | "block";
  rules: GateRule[];
  override_enabled: boolean;
  override_hours: number;
  updated_at?: string;
};

export type GateRef = { id: string; key: string; code?: string; title: string; url: string };

export type GateReason = {
  rule: string;
  kind: string;
  label: string;
  status: GateResult;
  message: string;
  url?: string;
  overridden?: boolean;
};

export type OverrideRef = { id: string; status: string; reason: string; expires_at?: string };

export type GateOutcome = {
  result: GateResult;
  raw_result: GateResult;
  mode: "off" | "warn" | "block" | "none";
  environment: string;
  application: GateRef;
  request?: GateRef;
  service?: string;
  version?: string;
  reasons: GateReason[];
  summary: string;
  overridden?: boolean;
  override?: OverrideRef;
  decision_id?: string;
  url: string;
  checked_at: string;
};

export type OverrideView = {
  id: string;
  decision_id: string;
  application: GateRef;
  request?: GateRef;
  environment: string;
  version?: string;
  reason: string;
  hours: number;
  status: "pending" | "approved" | "rejected" | "revoked" | "expired";
  requested_by: string;
  requested_at: string;
  decided_at?: string;
  expires_at?: string;
  approval_request_id?: string;
};

export type DecisionView = {
  id: string;
  application: GateRef;
  request?: GateRef;
  service?: string;
  environment: string;
  version?: string;
  commit?: string;
  change?: string;
  result: GateResult;
  raw_result: GateResult;
  mode: string;
  overridden: boolean;
  reasons: GateReason[];
  actor: string;
  at: string;
  can_override: boolean;
  override?: OverrideView;
};

export type GateCell = { application: GateRef; environment: string; production: boolean; outcome: GateOutcome };

export type GatePanel = {
  subject: { id: string; key: string; title: string; type: string };
  cells: GateCell[];
  decisions: DecisionView[];
  overrides: OverrideView[];
  configured: boolean;
  can_configure: boolean;
};

export type GateApprover = { member?: string; role?: string; name?: string };
export type EvidenceMapItem = { source: string; control: string; control_title?: string; valid_days: number | null; production_only: boolean };
export type EvidenceConfig = {
  items: EvidenceMapItem[];
  controls: { key: string; title: string }[];
  check_names: string[];
  enabled: boolean;
};

export type GateConfig = {
  policies: GatePolicy[];
  environments: { id: string; name: string; production: boolean; ordinal: number }[];
  request_phases: { name: string; category: string }[];
  approvers: GateApprover[];
  roles: { key: string; name: string }[];
  evidence: EvidenceConfig;
  default_rules: GateRule[];
  facts: Record<string, FactInfo[]>;
  samples: Record<string, string>;
  can_edit: boolean;
};

export type PreviewInput = {
  application: string;
  environment: string;
  request?: string;
  service?: string;
  version?: string;
  commit?: string;
  policy?: GatePolicy;
};

export const gateApi = {
  config: () => call<GateConfig>("GET", "/devsecops/gate/config"),
  panel: (ref: string) => call<GatePanel>("GET", `/devsecops/elements/${q(ref)}/gate`),
  decisions: (p: { result?: string; limit?: number } = {}) => {
    const s = new URLSearchParams();
    if (p.result) s.set("result", p.result);
    if (p.limit) s.set("limit", String(p.limit));
    return call<{ items: DecisionView[] }>("GET", `/devsecops/gate/decisions${s.size ? "?" + s : ""}`);
  },
  savePolicy: (env: string, p: Omit<GatePolicy, "environment">) => call<GatePolicy>("PUT", `/devsecops/gate/policies/${q(env)}`, p),
  deletePolicy: (env: string) => call<void>("DELETE", `/devsecops/gate/policies/${q(env)}`),
  saveApprovers: (approvers: GateApprover[]) => call<{ approvers: GateApprover[] }>("PUT", "/devsecops/gate/approvers", { approvers }),
  saveEvidenceMap: (items: EvidenceMapItem[]) => call<EvidenceConfig>("PUT", "/devsecops/gate/evidence-map", { items }),
  preview: (p: PreviewInput) => call<GateOutcome>("POST", "/devsecops/gate/preview", p),
  requestOverride: (decision: string, reason: string, hours?: number) =>
    call<OverrideView>("POST", "/devsecops/gate/overrides", { decision, reason, hours }),
  revokeOverride: (id: string) => call<void>("POST", `/devsecops/gate/overrides/${q(id)}/revoke`),
};

export const RULE_NAMES: Record<RuleKind, string> = {
  get requirements() { return tr("governance.gateapi.requisiti_di_progetto"); },
  get vulnerabilities() { return tr("governance.vulnerabilities.vulnerabilita"); },
  get change_approved() { return tr("governance.gateapi.change_approvato"); },
  get window() { return tr("governance.gateapi.finestra_di_rilascio"); },
  get checks() { return tr("governance.gateapi.controlli_della_pipeline"); },
  get condition() { return tr("governance.gateapi.condizione_libera"); },
};

export const RULE_HELP: Record<RuleKind, string> = {
  get requirements() { return tr("governance.gateapi.ogni_requisito_applicabile_all_applicazione"); },
  get vulnerabilities() { return tr("governance.gateapi.nessuna_cve_oltre_la_soglia"); },
  get change_approved() { return tr("governance.gateapi.la_richiesta_collegata_al_change"); },
  get window() { return tr("governance.gateapi.si_rilascia_solo_nei_giorni"); },
  get checks() { return tr("governance.gateapi.i_controlli_scelti_sono_superati"); },
  get condition() { return tr("governance.gateapi.una_condizione_sui_dati_dell"); },
};

/** Nome leggibile di un controllo della pipeline (gli stessi della politica); i nomi sconosciuti restano com'sono. */
const CHECK_LABELS: Record<string, string> = {
  get test() { return tr("governance.gateapi.test_automatici"); }, get tests() { return tr("governance.gateapi.test_automatici"); }, get unit() { return tr("governance.gateapi.test_automatici"); },
  get lint() { return tr("governance.gateapi.qualita_del_codice"); },
  get secrets() { return tr("governance.gateapi.segreti_nel_codice"); },
  get dependencies() { return tr("governance.gateapi.dipendenze_vulnerabili"); }, get cves() { return tr("governance.gateapi.dipendenze_vulnerabili"); }, get cve() { return tr("governance.gateapi.dipendenze_vulnerabili"); }, get sca() { return tr("governance.gateapi.dipendenze_vulnerabili"); },
  get sast() { return tr("governance.gateapi.analisi_statica_della_sicurezza"); },
  get image() { return tr("governance.gateapi.immagine_del_container"); }, get container() { return tr("governance.gateapi.immagine_del_container"); },
};
export const checkLabel = (name: string): string => CHECK_LABELS[name.toLowerCase()] ?? name;
