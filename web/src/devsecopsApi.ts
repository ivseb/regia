// API DevSecOps (piano, passo 21; replica/devsecops.md): pannello dell'asset e
// dell'applicazione, panoramica del portafoglio, storico di un servizio, configurazione.
// Modulo a parte, caricato con le pagine DevSecOps: il bundle iniziale non cresce.
import { call } from "./api";

const q = encodeURIComponent;

export type CheckResult = {
  name: string;
  mode: "off" | "warn" | "block";
  outcome: "passed" | "failed" | "skipped" | "not_run";
  skip?: { by: string; reason?: string };
  summary?: string;
  url?: string;
};
export type ChecksStatus = "ok" | "warn" | "exception" | "failed" | "none";

export type ChangeRef = {
  id: string;
  number: string;
  aliases: string[];
  title: string;
  request_id: string | null;
  request_key: string | null;
  request_title: string | null;
  request_state: string | null;
  linked_by: "key" | "field" | "manual" | null;
};

export type VulnSummary = { critical: number; high: number; medium: number; low: number; fixable_critical: number; fixable_high: number };

export type Release = {
  id: string;
  service_id: string;
  service: string;
  application_id: string | null;
  application: string | null;
  environment: string;
  production: boolean;
  version: string;
  commit: string | null;
  image: string | null;
  digest: string | null;
  at: string;
  requested_by: string | null;
  approved_by: string | null;
  approved_at: string | null;
  pull_request: string | null;
  release_url: string | null;
  change: ChangeRef | null;
  change_number: string | null;
  cves_at_release: VulnSummary | null;
  checks: CheckResult[];
  checks_status: ChecksStatus;
};

export type CVECounts = VulnSummary & {
  image: { critical: number; high: number };
  app: { critical: number; high: number };
  scanned_at?: string;
  scanners?: string[];
  declared?: boolean;
};

export type Runtime = { name: string; version: string; end_of_support: string | null; unsupported: boolean };
export type EnvCell = { version: string; at: string; last_status: string; last_at: string };

export type ServiceRow = {
  id: string;
  name: string;
  repository: string | null;
  provider: string | null;
  url: string | null;
  application_id: string | null;
  application_key: string | null;
  application: string | null;
  on_pipeline: boolean;
  first_seen_at: string | null;
  last_seen_at: string | null;
  prod_version: string;
  prod_since: string | null;
  release: Release | null;
  cves_today: CVECounts | null;
  cves_at_release: CVECounts | null;
  runtimes: Runtime[];
  days_since_release: number | null;
  stale: boolean;
  environments: Record<string, EnvCell>;
  releases: number;
};

export type Trend = { now: number; week_ago: number };
export type KPIs = {
  critical_cves: Trend;
  blocking_checks: Trend;
  stale: Trend;
  stale_days: number;
  services: number;
  on_pipeline: number;
  unscanned?: number;
};
export type Env = { id: string; name: string; production: boolean; ordinal: number };
export type ChangeGroup = { change: ChangeRef | null; releases: Release[]; last: string };

export type AppRef = {
  id: string;
  key: string;
  name: string;
  code: string | null;
  criticality: string | null;
  supports_critical_function: boolean;
  team: string | null;
};

export type Panel = {
  subject: { id: string; key: string; title: string; type: string };
  applications: AppRef[];
  kpis: KPIs;
  services: ServiceRow[];
  environments: Env[];
  changes: ChangeGroup[];
  can_configure: boolean;
};

export type ServiceHistory = ServiceRow & {
  declared_application: string | null;
  mapping_source: "pipeline" | "manual" | null;
  history: Release[];
};

export type AssetSummary = {
  id: string;
  key: string;
  name: string;
  applications: number;
  services: number;
  on_pipeline: number;
  critical: number;
  high: number;
  blocking: number;
  stale: number;
  unsupported_runtimes: number;
  last_release: string | null;
  dora: boolean;
};

export type VulnRow = {
  id: string;
  severity: string;
  package: string | null;
  version: string | null;
  fixed_in: string | null;
  layer: "image" | "app" | null;
  url: string | null;
  first_seen: string;
  service_id: string;
  service: string;
  prod_version: string;
  application_id: string | null;
  application_key: string | null;
  application: string | null;
  criticality: string | null;
  supports_critical_function: boolean;
};

export type Overview = {
  kpis: KPIs;
  assets: AssetSummary[];
  unassigned: AssetSummary;
  critical: VulnRow[];
  releases: Release[];
  unmapped_services: number;
};

export type RuntimeEOL = { name: string; version: string; end_of_support: string };
export type Settings = {
  production_envs: string[];
  stale_days: number;
  change_field: string;
  runtimes: RuntimeEOL[];
  updated_at: string | null;
  default: boolean;
};
export type ConfigService = {
  id: string;
  name: string;
  repository: string | null;
  provider: string | null;
  declared_application: string | null;
  application_id: string | null;
  application_key: string | null;
  application: string | null;
  mapping_source: "pipeline" | "manual" | null;
  first_seen_at: string | null;
  last_seen_at: string | null;
};
export type Config = {
  settings: Settings;
  environments: Env[];
  services: ConfigService[];
  applications: { id: string; key: string; name: string; code: string | null }[];
  request_fields: { key: string; name: string }[];
  changes: ChangeRef[];
  can_edit: boolean;
};

export const dsoApi = {
  panel: (ref: string) => call<Panel>("GET", `/devsecops/elements/${q(ref)}/panel`),
  overview: () => call<Overview>("GET", "/devsecops/overview"),
  service: (id: string) => call<ServiceHistory>("GET", `/devsecops/services/${q(id)}`),
  config: () => call<Config>("GET", "/devsecops/config"),
  saveSettings: (s: Pick<Settings, "production_envs" | "stale_days" | "change_field" | "runtimes">) => call<Settings>("PUT", "/devsecops/config/settings", s),
  updateEnv: (id: string, body: { production?: boolean; ordinal?: number }) => call<void>("PATCH", `/devsecops/environments/${q(id)}`, body),
  mapService: (id: string, application: string) => call<ServiceHistory>("PATCH", `/devsecops/services/${q(id)}`, { application }),
  createService: (name: string, application: string, repository?: string) =>
    call<ServiceHistory>("POST", "/devsecops/services", { format: "regia.devsecops/v1", name, application, repository }),
  updateChange: (id: string, body: { request?: string; number?: string }) => call<ChangeRef>("PATCH", `/devsecops/changes/${q(id)}`, body),
};
