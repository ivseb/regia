// API del connettore GitHub e dei profili di pipeline (DevSecOps, fase 1).
// Rispecchia internal/api/devsecops_github.go.
import { call } from "./api";

const q = encodeURIComponent;
const G = "/devsecops/github";
const P = "/devsecops/pipeline-profiles";

export type RateLimit = { limit: number; remaining: number; used: number; reset: string; resource?: string; observed: string; waited_seconds?: number };
export type SyncStatus = "running" | "ok" | "partial" | "error" | "rate_limited";
export type GHConfig = {
  base_url: string;
  owners: string[];
  auth_mode: "app" | "token";
  app_id?: string;
  installation_id?: string;
  interval_minutes: number;
  profile_id?: string;
  verdict_check?: string;
  release_environment?: string;
  gitops_repositories?: string[];
  private_network?: boolean;
  ca_certificate?: string;
  include_archived?: boolean;
};
export type Integration = {
  id: string;
  name: string;
  active: boolean;
  config: GHConfig;
  has_token: boolean;
  has_private_key: boolean;
  has_webhook_secret: boolean;
  webhook_path: string;
  profile_name: string | null;
  profile_version: number | null;
  repositories: { total: number; confirmed: number; suggested: number; ignored: number };
  sync: {
    next_run_at: string | null;
    running: boolean;
    last_started_at: string | null;
    last_finished_at: string | null;
    last_status: SyncStatus | null;
    last_error: string | null;
    rate_limit: RateLimit | null;
  };
  created_at: string;
  updated_at: string;
};
export type IntegrationList = { items: Integration[]; allow_private: boolean; dev: boolean; storing: boolean; base_url: string };
export type IntegrationInput = {
  name: string;
  active?: boolean;
  base_url: string;
  owners: string[];
  auth_mode: "app" | "token";
  app_id: string;
  installation_id: string;
  token?: string;
  private_key?: string;
  webhook_secret?: string;
  remove_webhook_secret?: boolean;
  interval_minutes: number;
  profile_id: string;
  verdict_check: string;
  release_environment: string;
  gitops_repositories: string[];
  private_network: boolean;
  ca_certificate: string;
  include_archived: boolean;
};
export type TestResult = { ok: boolean; message: string; details: string[]; warnings: string[]; server: string; rate_limit?: RateLimit };

export type AppRef = { id: string; key: string; name: string; code: string };
export type Repository = {
  id: string;
  external_id: string;
  full_name: string;
  name: string;
  default_branch: string;
  html_url: string;
  description: string;
  archived: boolean;
  private: boolean;
  missing: boolean;
  status: "none" | "suggested" | "confirmed" | "ignored";
  application: AppRef | null;
  suggestion: { application: AppRef; score: number; reason: string } | null;
  service_name: string;
  last_synced_at: string | null;
  last_error: string | null;
  confirmed_at: string | null;
};

export type RunStats = {
  repositories: number;
  discovered?: number;
  new_repositories?: number;
  releases: number;
  sboms: number;
  vuln_reports: number;
  deployments: number;
  gitops_deployments: number;
  check_runs: number;
  requests: number;
  profile_version?: number;
  duration_ms: number;
  errors?: string[];
  warnings?: string[];
};
export type Run = { id: string; trigger: "schedule" | "manual" | "webhook"; scope: string; status: SyncStatus; stats: RunStats; error: string | null; started_at: string; finished_at: string | null };

export type Profile = {
  id: string;
  key: string;
  name: string;
  description: string;
  preset: string | null;
  version: number;
  definition: unknown;
  used_by: string[];
  created_at: string;
  updated_at: string;
};
export type Preset = { key: string; name: string; description: string; definition: unknown };
export type ProfileVersion = { version: number; note: string; created_at: string; created_by: string | null; definition?: unknown };
export type ProfileProblem = { path: string; message: string };

export type Service = { name: string; repository?: string; provider?: string; url?: string; application?: string };
export type CheckResult = { name: string; mode?: string; outcome: string; summary?: string; url?: string };
export type ReleaseObj = {
  service: Service;
  version: string;
  commit?: string;
  image?: string;
  digest?: string;
  environment: string;
  change_number?: string;
  change_title?: string;
  requested_by?: string;
  approved_by?: string;
  approved_at?: string;
  pull_request?: string;
  release_url?: string;
  at: string;
  cves_at_release?: { critical: number; high: number; medium: number; low: number; fixable_critical: number; fixable_high: number };
  checks?: CheckResult[];
};
export type DeploymentObj = { service: Service; environment: string; version: string; digest?: string; status: string; at: string; source?: string; external_ref?: string };
export type Preview = {
  repository: string;
  service: Service;
  associated: boolean;
  profile: string;
  release_tag?: string;
  assets: string[];
  assets_read: string[];
  release: ReleaseObj | null;
  sbom: { format: string; components: number; bytes: number } | null;
  vulnerabilities: { scanner: string; total: number; summary: NonNullable<ReleaseObj["cves_at_release"]>; sample: { id: string; package: string; version?: string; fixed_in?: string; severity: string }[] } | null;
  check_run: { commit: string; ref?: string; verdict: string; checks: CheckResult[]; at: string } | null;
  check_ready: boolean;
  deployments: DeploymentObj[];
  gitops: DeploymentObj[];
  warnings: string[];
  requests: number;
};

export const githubApi = {
  list: () => call<IntegrationList>("GET", `${G}/integrations`),
  create: (i: IntegrationInput) => call<{ id: string }>("POST", `${G}/integrations`, i),
  update: (id: string, i: IntegrationInput) => call<void>("PATCH", `${G}/integrations/${q(id)}`, i),
  remove: (id: string) => call<void>("DELETE", `${G}/integrations/${q(id)}`),
  test: (id: string) => call<TestResult>("POST", `${G}/integrations/${q(id)}/test`),
  webhookSecret: (id: string) => call<{ secret: string }>("POST", `${G}/integrations/${q(id)}/webhook-secret`),
  repos: (id: string) => call<{ items: Repository[] }>("GET", `${G}/integrations/${q(id)}/repositories`),
  discover: (id: string) => call<{ found: number; new: number; missing: number; suggested: number }>("POST", `${G}/integrations/${q(id)}/discover`),
  mapRepo: (id: string, repo: string, body: { status: "confirmed" | "ignored" | "none"; application_id?: string; service_name?: string }) =>
    call<Repository>("PUT", `${G}/integrations/${q(id)}/repositories/${q(repo)}`, body),
  accept: (id: string, ids: string[]) => call<{ confirmed: number }>("POST", `${G}/integrations/${q(id)}/repositories/accept`, { ids }),
  sync: (id: string, full = false) => call<{ run_id: string }>("POST", `${G}/integrations/${q(id)}/sync${full ? "?full=true" : ""}`),
  runs: (id: string) => call<{ items: Run[] }>("GET", `${G}/integrations/${q(id)}/runs`),
  preview: (id: string, body: { repository: string; definition?: unknown; profile_id?: string; tag?: string }) =>
    call<Preview>("POST", `${G}/integrations/${q(id)}/preview`, body),

  profiles: () =>
    call<{ items: Profile[]; presets: Preset[]; format: string; record_fields: Record<string, string>; gitops_fields: Record<string, string> }>("GET", P),
  profile: (id: string) => call<{ profile: Profile; versions: ProfileVersion[] }>("GET", `${P}/${q(id)}`),
  profileVersion: (id: string, n: number) => call<ProfileVersion>("GET", `${P}/${q(id)}/versions/${n}`),
  createProfile: (body: { preset?: string; name?: string; description?: string; definition?: unknown }) => call<{ id: string }>("POST", P, body),
  updateProfile: (id: string, body: { name: string; description: string; definition: unknown; note: string }) =>
    call<{ version: number }>("PUT", `${P}/${q(id)}`, body),
  deleteProfile: (id: string) => call<void>("DELETE", `${P}/${q(id)}`),
  validate: (definition: unknown) => call<{ valid: boolean; problems: ProfileProblem[] }>("POST", `${P}/validate`, { definition }),
  importProfile: (document: unknown) => call<{ id: string; key: string; version: number; created: boolean }>("POST", `${P}/import`, { document }),
  exportURL: (id: string) => `/api/v1${P}/${q(id)}/export`,
};
