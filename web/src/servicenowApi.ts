// API del connettore ServiceNow (DevSecOps, sola lettura). Rispecchia internal/api/devsecops_servicenow.go.
// Regia legge incidenti P1-P2 e change request dei servizi CMDB associati alle applicazioni:
// non scrive mai su ServiceNow.
import { call } from "./api";
import type { SyncStatus } from "./githubApi";
import type { IncidentInput, IncidentView } from "./riskApi";

const q = encodeURIComponent;
const S = "/devsecops/servicenow";

export type SNAuth = "basic" | "oauth";
export type SNDoraMode = "confirm" | "auto";

export type SNConfig = {
  base_url: string;
  auth: SNAuth;
  username?: string;
  client_id?: string;
  service_class: string;
  service_filter?: string;
  code_field?: string;
  priorities: number[];
  dora_mode: SNDoraMode;
  lookback_days: number;
  interval_minutes: number;
  private_network?: boolean;
  use_proxy?: boolean;
  ca_certificate?: string;
};

export type SNIntegration = {
  id: string;
  name: string;
  active: boolean;
  config: SNConfig;
  has_password: boolean;
  has_client_secret: boolean;
  has_webhook_secret: boolean;
  webhook_path: string;
  services: { total: number; confirmed: number; suggested: number; ignored: number };
  records: { incidents: number; pending: number; changes: number };
  sync: {
    next_run_at: string | null;
    running: boolean;
    last_started_at: string | null;
    last_finished_at: string | null;
    last_status: SyncStatus | null;
    last_error: string | null;
  };
  created_at: string;
  updated_at: string;
};

/** proxy: "" quando l'operatore non ha configurato un proxy in uscita. */
export type SNIntegrationList = { items: SNIntegration[]; allow_private: boolean; dev: boolean; proxy: string; base_url: string };

export type SNIntegrationInput = {
  name: string;
  active?: boolean;
  base_url: string;
  auth: SNAuth;
  username: string;
  password?: string;
  client_id: string;
  client_secret?: string;
  service_class: string;
  service_filter: string;
  code_field: string;
  priorities: number[];
  dora_mode: SNDoraMode;
  lookback_days: number;
  interval_minutes: number;
  private_network: boolean;
  use_proxy: boolean;
  ca_certificate: string;
  webhook_secret?: string;
  remove_webhook_secret?: boolean;
};

export type SNTableTest = { table: string; label: string; readable: boolean; sample: number; error?: string };
export type SNTestResult = { ok: boolean; message: string; details: string[]; warnings: string[]; tables: SNTableTest[] };

export type SNAppRef = { id: string; key: string; name: string; code: string };
export type SNRef = { id: string; key: string; title: string };

export type SNServiceStatus = "none" | "suggested" | "confirmed" | "ignored";
export type SNService = {
  id: string;
  sys_id: string;
  name: string;
  class: string;
  code?: string;
  url: string;
  missing: boolean;
  status: SNServiceStatus;
  application: SNAppRef | null;
  suggestion: { application: SNAppRef; score: number; reason: string } | null;
  incidents: number;
  changes: number;
  last_synced_at: string | null;
  last_error: string | null;
  confirmed_at: string | null;
};

export type SNDiscoverResult = { found: number; new: number; missing: number; suggested: number };

export type SNRunStats = {
  services: number;
  discovered?: number;
  new_services?: number;
  incidents: number;
  new_incidents: number;
  changes: number;
  new_changes: number;
  known_changes?: number;
  requests: number;
  duration_ms: number;
  errors?: string[];
  warnings?: string[];
};
export type SNRun = {
  id: string;
  trigger: "schedule" | "manual" | "webhook";
  scope: string;
  status: SyncStatus;
  stats: SNRunStats;
  error: string | null;
  started_at: string;
  finished_at: string | null;
};

export type SNDoraStatus = "pending" | "counted" | "excluded";
export type SNIncident = {
  id: string;
  integration_id: string;
  sys_id: string;
  number: string;
  priority: number;
  state: string;
  state_label: string;
  opened_at: string;
  resolved_at: string | null;
  short_description: string;
  url: string;
  service: string;
  application: SNRef | null;
  dora_status: SNDoraStatus;
  dora_mode: SNDoraMode;
  counts: boolean;
  regia_incident: SNRef | null;
  updated_at: string;
};

export type SNChange = {
  id: string;
  number: string;
  state: string;
  state_label: string;
  approval: string;
  approval_label: string;
  type: string;
  planned_start: string | null;
  planned_end: string | null;
  actual_start: string | null;
  actual_end: string | null;
  short_description: string;
  url: string;
  service: string;
  application: SNRef | null;
  regia_change: string | null;
  request: SNRef | null;
  updated_at: string;
};

export type SNProposal = {
  source: SNIncident;
  title: string;
  description: string;
  /** RFC 3339 */
  detected_at: string;
  resolved_at: string | null;
  /** id delle applicazioni */
  impacted: string[];
};

export type SNIncidentFilter = { application?: string; integration?: string; proposals?: boolean; limit?: number };

function query(f: Record<string, string | number | boolean | undefined>): string {
  const s = new URLSearchParams();
  for (const [k, v] of Object.entries(f)) if (v !== undefined && v !== "" && v !== false) s.set(k, String(v));
  return s.size ? `?${s}` : "";
}

export const servicenowApi = {
  list: () => call<SNIntegrationList>("GET", `${S}/integrations`),
  create: (i: SNIntegrationInput) => call<{ id: string }>("POST", `${S}/integrations`, i),
  update: (id: string, i: SNIntegrationInput) => call<void>("PATCH", `${S}/integrations/${q(id)}`, i),
  remove: (id: string) => call<void>("DELETE", `${S}/integrations/${q(id)}`),
  test: (id: string) => call<SNTestResult>("POST", `${S}/integrations/${q(id)}/test`),
  webhookSecret: (id: string) => call<{ secret: string }>("POST", `${S}/integrations/${q(id)}/webhook-secret`),
  services: (id: string) => call<{ items: SNService[] }>("GET", `${S}/integrations/${q(id)}/services`),
  mapService: (id: string, service: string, body: { status: "confirmed" | "ignored" | "none"; application_id?: string }) =>
    call<SNService>("PUT", `${S}/integrations/${q(id)}/services/${q(service)}`, body),
  accept: (id: string, ids: string[]) => call<{ confirmed: number }>("POST", `${S}/integrations/${q(id)}/services/accept`, { ids }),
  discover: (id: string) => call<SNDiscoverResult>("POST", `${S}/integrations/${q(id)}/discover`),
  sync: (id: string, full = false) => call<{ run_id: string }>("POST", `${S}/integrations/${q(id)}/sync?full=${full}`),
  runs: (id: string) => call<{ items: SNRun[] }>("GET", `${S}/integrations/${q(id)}/runs`),
  incidents: (f: SNIncidentFilter = {}) => call<{ items: SNIncident[] }>("GET", `${S}/incidents${query(f)}`),
  changes: (f: { application?: string; integration?: string; limit?: number } = {}) => call<{ items: SNChange[] }>("GET", `${S}/changes${query(f)}`),
  proposal: (id: string) => call<SNProposal>("GET", `${S}/incidents/${q(id)}/proposal`),
  /** Crea l'incidente di Regia dalla proposta e lo collega all'incidente di ServiceNow. */
  promote: (id: string, in_: IncidentInput) => call<IncidentView>("POST", `${S}/incidents/${q(id)}/promote`, in_),
  decide: (id: string, status: SNDoraStatus) => call<SNIncident>("POST", `${S}/incidents/${q(id)}/decision`, { status }),
  link: (id: string, incident: string) => call<SNIncident>("POST", `${S}/incidents/${q(id)}/link`, { incident }),
};
