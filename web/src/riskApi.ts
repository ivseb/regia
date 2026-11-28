// API dei rischi ICT, dei fornitori e del registro delle informazioni, degli incidenti con le
// notifiche normative (piano, passo 20). Modulo a parte, caricato con le pagine di Governo →
// Rischi e incidenti, così il bundle iniziale non cresce.
import { call, type Element } from "./api";
import { tr } from "./i18n/core";

const q = encodeURIComponent;

export type Tone = "ok" | "neutral" | "accent" | "danger";
export type Level = { value: number; name: string; description?: string };
export type Band = { key: string; name: string; min: number; max: number; tone: Tone };
export type Matrix = {
  id?: string;
  key: string;
  name: string;
  description?: string;
  default: boolean;
  appetite_score: number | null;
  likelihood: Level[];
  impact: Level[];
  bands: Band[];
};

export type Score = {
  likelihood: number;
  likelihood_name: string;
  impact: number;
  impact_name: string;
  score: number;
  band: string;
  band_name: string;
  tone: Tone;
  over_appetite: boolean;
};

export type Ref = { id: string; key: string; title: string; type: string; type_name: string; state: string; state_category: string; critical?: boolean };

export type RiskItem = {
  id: string;
  key: string;
  title: string;
  phase: string;
  phase_category: string;
  category: string;
  inherent: Score | null;
  residual: Score | null;
  treatment: string;
  treatment_due: string | null;
  review_due: string | null;
  acceptance: string;
  owner: string | null;
  targets: string[];
  controls: number;
  overdue: boolean;
};
export type RiskRegister = { items: RiskItem[]; phases: string[]; categories: string[]; matrix: Matrix | null; can_create: boolean };
export type RiskFilter = { q?: string; fase?: string; livello?: string; trattamento?: string; categoria?: string; oltre_soglia?: boolean };

export type RiskView = {
  element: Element;
  key: string;
  matrix: Matrix;
  category: string;
  inherent: Score | null;
  residual: Score | null;
  treatment: string | null;
  treatment_name: string;
  treatment_plan: string;
  treatment_due: string | null;
  review_due: string | null;
  acceptance: string;
  acceptance_name: string;
  accepted_at: string | null;
  accepted_until: string | null;
  owner_id: string | null;
  owner: string | null;
  targets: Ref[];
  controls: Ref[];
  incidents: Ref[];
  can: { edit: boolean; treat: boolean; request_acceptance: boolean };
};
export type RiskInput = Partial<{
  title: string;
  description: string;
  category: string;
  matrix: string;
  inherent_likelihood: number;
  inherent_impact: number;
  residual_likelihood: number;
  residual_impact: number;
  treatment: string;
  treatment_plan: string;
  treatment_due: string;
  review_due: string;
  owner_id: string;
  targets: string[];
}>;

export type Cell = { likelihood: number; impact: number; score: number; band: string; tone: Tone; count: number; keys: string[] };
export type Heatmap = { matrix: Matrix; inherent: Cell[]; residual: Cell[] };

export type Provider = {
  id: string;
  key: string;
  name: string;
  phase: string;
  legal_name: string;
  lei: string | null;
  country: string | null;
  provider_type: string | null;
  is_intragroup: boolean;
  substitutability: string | null;
  substitutability_name: string;
  critical: boolean;
  critical_reason: string;
  critical_applications: number;
  contracts: number;
  next_contract_end: string | null;
  applications: string[];
};
export type Subcontractor = { name: string; country?: string; service?: string; lei?: string };
export type Contract = {
  id: string;
  provider_id: string;
  provider_key: string;
  provider: string;
  reference: string;
  service_type: string;
  services: string;
  functions: string;
  supports_critical_function: boolean;
  criticality: string | null;
  criticality_name: string;
  start_date: string | null;
  end_date: string | null;
  notice_days: number | null;
  annual_cost_cents: number | null;
  currency: string | null;
  data_locations: string[];
  processing_locations: string[];
  subcontractors: Subcontractor[];
  exit_strategy: string;
  exit_tested_on: string | null;
  governing_law: string | null;
  auto_renewal: boolean;
  /** Impegno contrattuale di ripristino (continuità operativa), in minuti. */
  rto_minutes: number | null;
  rpo_minutes: number | null;
  applications: Ref[];
  days_to_end: number | null;
  expiring: boolean;
  expired: boolean;
};
export type ContractInput = Partial<Omit<Contract, "id" | "provider_id" | "provider_key" | "provider" | "applications" | "days_to_end" | "expiring" | "expired" | "criticality_name">> & {
  applications?: string[];
};
export type ProviderView = Provider & {
  element: Element;
  owner_id: string | null;
  owner: string | null;
  application_refs: Ref[];
  risks: Ref[];
  contract_list: Contract[];
  can: { edit: boolean };
};
export type ProviderInput = Partial<{
  name: string;
  description: string;
  legal_name: string;
  lei: string;
  country: string;
  provider_type: string;
  is_intragroup: boolean;
  substitutability: string;
  owner_id: string;
  applications: string[];
}>;

export type CriterionResult = { key: string; name: string; met: boolean; primary?: boolean };
export type SchemeResult = { key: string; name: string; outcome: string; positive: boolean; criteria: CriterionResult[]; reference?: string; at: string };
export type Notification = {
  id: string;
  incident_id: string;
  incident_key: string;
  incident: string;
  scheme_key: string;
  scheme_name: string;
  stage_key: string;
  stage_name: string;
  ordinal: number;
  status: "pending" | "draft" | "submitted" | "not_required";
  due_at: string | null;
  content: string;
  hint: string;
  reference: string | null;
  channel: string | null;
  note: string | null;
  submitted_at: string | null;
  submitted_by: string | null;
  seconds_left: number | null;
};
export type IncidentEvent = { id: string; at: string; kind: string; text: string; system: boolean; created_by: string | null };
export type ActionRef = Ref & { assignee: string | null; due: string | null };
export type Postmortem = { summary: string; root_cause: string; lessons: string };
/** Che cosa è successo alla fase quando si è registrata la risoluzione (solo nella risposta della modifica). */
export type PhaseMove = {
  status: "moved" | "choose";
  phase?: string;
  target?: string;
  message: string;
  options: { to: string; label: string }[];
};
export type IncidentView = {
  element: Element;
  key: string;
  detected_at: string;
  classified_at: string | null;
  contained_at: string | null;
  resolved_at: string | null;
  duration_hours: number;
  major: boolean;
  classification: SchemeResult[];
  clients_affected: number | null;
  clients_affected_pct: number | null;
  downtime_minutes: number | null;
  data_loss: string[];
  economic_impact_cents: number | null;
  member_states: number | null;
  reputational: boolean;
  recurring: boolean;
  cause: string | null;
  cause_name: string;
  cause_provider: Ref | null;
  phase_move?: PhaseMove;
  lead_id: string | null;
  lead: string | null;
  impacted: Ref[];
  risks: Ref[];
  events: IncidentEvent[];
  notifications: Notification[];
  postmortem: Postmortem | null;
  postmortem_at: string | null;
  postmortem_by: string | null;
  actions: ActionRef[];
  options: { data_loss: Record<string, string>; cause: Record<string, string> };
  can: { manage: boolean };
};
export type IncidentInput = Partial<{
  title: string;
  description: string;
  detected_at: string;
  contained_at: string;
  resolved_at: string;
  clients_affected: number;
  clients_affected_pct: number;
  downtime_minutes: number;
  data_loss: string[];
  economic_impact_cents: number;
  member_states: number;
  reputational: boolean;
  recurring: boolean;
  cause: string;
  /** Chiave del fornitore ICT quando la causa è «fornitore terzo»; vuoto lo toglie. */
  cause_provider: string;
  lead_id: string;
  impacted: string[];
}>;
export type IncidentItem = {
  id: string;
  key: string;
  title: string;
  phase: string;
  phase_category: string;
  detected_at: string;
  resolved_at: string | null;
  major: boolean;
  outcomes: string[];
  open_notifications: number;
  next_due: string | null;
  next_stage: string;
  impacted: string[];
};

export type Dashboard = {
  heatmap: Heatmap | null;
  over_threshold: RiskItem[];
  overdue: RiskItem[];
  open_risks: number;
  critical_providers: Provider[];
  providers: number;
  expiring_contracts: Contract[];
  open_incidents: IncidentItem[];
  open_notifications: Notification[];
  settings: Settings;
  can: { risk: boolean; incident: boolean; manage_incidents: boolean };
};

export type Column = { key: string; label: string };
export type ColumnInfo = Column & { group: string; help?: string };
export type Settings = { register_columns: Column[]; contract_notice_days: number; review_notice_days: number; notification_warning_hours: number };
export type Criterion = { key: string; name: string; condition: string; primary?: boolean; help?: string };
export type Due = { from: string; hours?: number; days?: number };
export type Stage = { key: string; name: string; due: Due[]; hint?: string };
export type Scheme = {
  id?: string;
  key: string;
  name: string;
  outcome_name: string;
  reference?: string;
  description?: string;
  active: boolean;
  ordinal: number;
  criteria: Criterion[];
  when: string;
  stages: Stage[];
};
export type Approver = { role?: string; body?: string; member?: string };
export type PolicyRule = { when: { risk_lte?: number; risk_gt?: number }; steps: { approvers: Approver[] }[] };
export type RiskConfig = {
  matrices: Matrix[];
  settings: Settings;
  policy: { id: string; name: string; rules: PolicyRule[] };
  schemes: Scheme[];
  column_catalog: ColumnInfo[];
  tones: Tone[];
  can_edit: boolean;
  can_edit_schemes: boolean;
};

function qs(f: Record<string, string | boolean | undefined>): string {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(f)) {
    if (v === undefined || v === "" || v === false) continue;
    p.set(k, String(v));
  }
  const s = p.toString();
  return s ? `?${s}` : "";
}

export const riskApi = {
  dashboard: () => call<Dashboard>("GET", "/risk/dashboard"),
  risks: (f: RiskFilter = {}) => call<RiskRegister>("GET", `/risks${qs(f)}`),
  risksCSV: (f: RiskFilter = {}) => `/api/v1/risks.csv${qs(f)}`,
  createRisk: (in_: RiskInput) => call<RiskView>("POST", "/risks", in_),
  risk: (ref: string) => call<RiskView>("GET", `/risks/${q(ref)}`),
  updateRisk: (ref: string, in_: RiskInput) => call<RiskView>("PATCH", `/risks/${q(ref)}`, in_),
  linkRisk: (ref: string, target: string) => call<RiskView>("POST", `/risks/${q(ref)}/links`, { target }),
  unlinkRisk: (ref: string, target: string) => call<RiskView>("DELETE", `/risks/${q(ref)}/links/${q(target)}`),
  requestAcceptance: (ref: string, until: string, comment: string) => call<RiskView>("POST", `/risks/${q(ref)}/acceptance`, { until, comment }),

  providers: (search = "", critical = false) => call<{ items: Provider[] }>("GET", `/ict/providers${qs({ q: search, critici: critical })}`),
  createProvider: (in_: ProviderInput) => call<ProviderView>("POST", "/ict/providers", in_),
  provider: (ref: string) => call<ProviderView>("GET", `/ict/providers/${q(ref)}`),
  updateProvider: (ref: string, in_: ProviderInput) => call<ProviderView>("PATCH", `/ict/providers/${q(ref)}`, in_),
  linkApp: (ref: string, application: string) => call<ProviderView>("POST", `/ict/providers/${q(ref)}/applications`, { application }),
  unlinkApp: (ref: string, app: string) => call<ProviderView>("DELETE", `/ict/providers/${q(ref)}/applications/${q(app)}`),
  createContract: (ref: string, in_: ContractInput) => call<Contract>("POST", `/ict/providers/${q(ref)}/contracts`, in_),
  updateContract: (id: string, in_: ContractInput) => call<Contract>("PATCH", `/ict/contracts/${q(id)}`, in_),
  deleteContract: (id: string) => call<void>("DELETE", `/ict/contracts/${q(id)}`),
  contracts: (expiring = false) => call<{ items: Contract[] }>("GET", `/ict/contracts${qs({ in_scadenza: expiring })}`),
  registerCSV: (columns: string[] = [], grain = "") => `/api/v1/ict/register.csv${qs({ columns: columns.join(","), grain })}`,

  incidents: (f: { q?: string; aperti?: boolean; gravi?: boolean } = {}) => call<{ items: IncidentItem[] }>("GET", `/incidents${qs(f)}`),
  createIncident: (in_: IncidentInput) => call<IncidentView>("POST", "/incidents", in_),
  incident: (ref: string) => call<IncidentView>("GET", `/incidents/${q(ref)}`),
  updateIncident: (ref: string, in_: IncidentInput) => call<IncidentView>("PATCH", `/incidents/${q(ref)}`, in_),
  linkIncident: (ref: string, target: string) => call<IncidentView>("POST", `/incidents/${q(ref)}/links`, { target }),
  unlinkIncident: (ref: string, target: string) => call<IncidentView>("DELETE", `/incidents/${q(ref)}/links/${q(target)}`),
  addEvent: (ref: string, body: { at?: string; kind: string; text: string }) => call<IncidentView>("POST", `/incidents/${q(ref)}/events`, body),
  classify: (ref: string, dryRun: boolean) =>
    call<{ results: SchemeResult[]; major: boolean; created: number }>("POST", `/incidents/${q(ref)}/classify${dryRun ? "?dry_run=true" : ""}`),
  savePostmortem: (ref: string, pm: Postmortem) => call<IncidentView>("PUT", `/incidents/${q(ref)}/postmortem`, pm),
  addAction: (ref: string, body: { title: string; body_text?: string; assignee_id?: string; team_id?: string; due?: string }) =>
    call<IncidentView>("POST", `/incidents/${q(ref)}/actions`, body),
  saveDraft: (id: string, content: string) => call<Notification>("PUT", `/regulatory-notifications/${q(id)}/draft`, { content }),
  submit: (id: string, body: { content?: string; submitted_at?: string; reference?: string; channel?: string; note?: string }) =>
    call<Notification>("POST", `/regulatory-notifications/${q(id)}/submit`, body),
  notRequired: (id: string, note: string) => call<Notification>("POST", `/regulatory-notifications/${q(id)}/not-required`, { note }),

  config: () => call<RiskConfig>("GET", "/risk-config"),
  saveMatrix: (m: Matrix) => call<Matrix>("PUT", `/risk-config/matrices/${q(m.key)}`, m),
  saveSettings: (s: Settings) => call<Settings>("PUT", "/risk-config/settings", s),
  savePolicy: (rules: PolicyRule[]) => call<unknown>("PUT", "/risk-config/acceptance-policy", { rules }),
  saveScheme: (s: Scheme) => call<Scheme>("PUT", `/risk-config/schemes/${q(s.key)}`, s),
  previewScheme: (scheme: Scheme, incident: string) => call<{ valid: boolean; result: SchemeResult | null }>("POST", "/risk-config/schemes/preview", { scheme, incident }),
  exportConfig: () => call<unknown>("GET", "/risk-config/export"),
  importConfig: (raw: string) =>
    fetch("/api/v1/risk-config/import", { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json" }, body: raw }).then(async (r) => {
      const data = await r.json().catch(() => ({}));
      if (!r.ok) throw new Error(data.detail ?? r.statusText);
      return data as { matrices: string[]; schemes: string[]; settings: boolean; policy: boolean };
    }),
};

export const TREATMENTS: Record<string, string> = { get mitigate() { return tr("governance.riskapi.mitigare"); }, get accept() { return tr("governance.riskapi.accettare"); }, get transfer() { return tr("governance.riskapi.trasferire"); }, get avoid() { return tr("governance.riskapi.evitare"); } };
export const EVENT_KINDS: Record<string, string> = {
  get detected() { return tr("governance.riskapi.rilevazione"); },
  get classified() { return tr("governance.riskapi.classificazione"); },
  get contained() { return tr("governance.riskapi.contenimento"); },
  get resolved() { return tr("governance.riskapi.ripristino"); },
  get update() { return tr("governance.riskapi.aggiornamento"); },
  get communication() { return tr("governance.riskapi.comunicazione"); },
  get decision() { return tr("crisi.event.decision"); },
  get notification() { return tr("governance.riskapi.notifica"); },
  get action() { return tr("governance.riskapi.azione"); },
  get closed() { return tr("governance.riskapi.chiusura"); },
};
export const NOTIFICATION_STATUS: Record<string, string> = { get pending() { return tr("governance.riskapi.da_preparare"); }, get draft() { return tr("governance.riskapi.bozza"); }, get submitted() { return tr("governance.riskapi.inviata"); }, get not_required() { return tr("governance.riskapi.non_dovuta"); } };
