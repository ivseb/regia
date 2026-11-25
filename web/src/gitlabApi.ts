// API del connettore GitLab (DevSecOps, fase 5b). Rispecchia internal/api/devsecops_gitlab.go.
// I profili di pipeline sono quelli di tutti i connettori (githubApi.profiles e compagnia).
import { call } from "./api";
import type { Preview, Preset, RateLimit, Repository, SyncStatus } from "./githubApi";

const q = encodeURIComponent;
const G = "/devsecops/gitlab";

export type GLConfig = {
  base_url: string;
  groups?: string[];
  projects?: string[];
  interval_minutes: number;
  profile_id?: string;
  verdict_check?: string;
  release_environment?: string;
  artifact_jobs?: string[];
  private_network?: boolean;
  ca_certificate?: string;
  include_archived?: boolean;
};
export type GLIntegration = {
  id: string;
  name: string;
  active: boolean;
  config: GLConfig;
  has_token: boolean;
  has_webhook_secret: boolean;
  webhook_path: string;
  profile_name: string | null;
  profile_version: number | null;
  projects: { total: number; confirmed: number; suggested: number; ignored: number };
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
export type GLIntegrationList = { items: GLIntegration[]; allow_private: boolean; dev: boolean; storing: boolean; base_url: string };
export type GLIntegrationInput = {
  name: string;
  active?: boolean;
  base_url: string;
  groups: string[];
  projects: string[];
  token?: string;
  webhook_secret?: string;
  remove_webhook_secret?: boolean;
  interval_minutes: number;
  profile_id: string;
  verdict_check: string;
  release_environment: string;
  artifact_jobs: string[];
  private_network: boolean;
  ca_certificate: string;
  include_archived: boolean;
};
export type GLTestResult = { ok: boolean; message: string; details: string[]; warnings: string[]; server: string; scopes: string[]; rate_limit?: RateLimit };

// Un progetto scoperto ha la forma del repository di GitHub.
export type GLProject = Repository;

export type GLRunStats = {
  projects: number;
  discovered?: number;
  new_projects?: number;
  releases: number;
  sboms: number;
  vuln_reports: number;
  deployments: number;
  environments: number;
  check_runs: number;
  pipelines: number;
  artifacts: number;
  requests: number;
  profile_version?: number;
  duration_ms: number;
  errors?: string[];
  warnings?: string[];
};
export type GLRun = { id: string; trigger: "schedule" | "manual" | "webhook"; scope: string; status: SyncStatus; stats: GLRunStats; error: string | null; started_at: string; finished_at: string | null };

export const gitlabApi = {
  list: () => call<GLIntegrationList>("GET", `${G}/integrations`),
  create: (i: GLIntegrationInput) => call<{ id: string }>("POST", `${G}/integrations`, i),
  update: (id: string, i: GLIntegrationInput) => call<void>("PATCH", `${G}/integrations/${q(id)}`, i),
  remove: (id: string) => call<void>("DELETE", `${G}/integrations/${q(id)}`),
  test: (id: string) => call<GLTestResult>("POST", `${G}/integrations/${q(id)}/test`),
  webhookSecret: (id: string) => call<{ secret: string }>("POST", `${G}/integrations/${q(id)}/webhook-secret`),
  projects: (id: string) => call<{ items: GLProject[] }>("GET", `${G}/integrations/${q(id)}/projects`),
  discover: (id: string) => call<{ found: number; new: number; missing: number; suggested: number }>("POST", `${G}/integrations/${q(id)}/discover`),
  mapProject: (id: string, project: string, body: { status: "confirmed" | "ignored" | "none"; application_id?: string; service_name?: string }) =>
    call<GLProject>("PUT", `${G}/integrations/${q(id)}/projects/${q(project)}`, body),
  accept: (id: string, ids: string[]) => call<{ confirmed: number }>("POST", `${G}/integrations/${q(id)}/projects/accept`, { ids }),
  sync: (id: string, full = false) => call<{ run_id: string }>("POST", `${G}/integrations/${q(id)}/sync${full ? "?full=true" : ""}`),
  runs: (id: string) => call<{ items: GLRun[] }>("GET", `${G}/integrations/${q(id)}/runs`),
  preview: (id: string, body: { repository: string; definition?: unknown; profile_id?: string; tag?: string }) =>
    call<Preview>("POST", `${G}/integrations/${q(id)}/preview`, body),
  presets: () => call<{ presets: Preset[] }>("GET", `${G}/presets`),
};
