// API del connettore Gitea (DevSecOps, ). Rispecchia internal/api/devsecops_gitea.go.
// I profili di pipeline sono quelli di tutti i connettori (githubApi.profiles e compagnia).
import { call } from "./api";
import type { Preview, Preset, RateLimit, Repository, SyncStatus } from "./githubApi";

const q = encodeURIComponent;
const G = "/devsecops/gitea";

export type GTConfig = {
  base_url: string;
  owners?: string[];
  repositories?: string[];
  interval_minutes: number;
  profile_id?: string;
  verdict_check?: string;
  release_environment?: string;
  private_network?: boolean;
  ca_certificate?: string;
  include_archived?: boolean;
};
export type GTIntegration = {
  id: string;
  name: string;
  active: boolean;
  config: GTConfig;
  has_token: boolean;
  has_webhook_secret: boolean;
  webhook_path: string;
  profile_name: string | null;
  profile_version: number | null;
  repos: { total: number; confirmed: number; suggested: number; ignored: number };
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
export type GTIntegrationList = { items: GTIntegration[]; allow_private: boolean; dev: boolean; storing: boolean; base_url: string };
export type GTIntegrationInput = {
  name: string;
  active?: boolean;
  base_url: string;
  owners: string[];
  repositories: string[];
  token?: string;
  webhook_secret?: string;
  remove_webhook_secret?: boolean;
  interval_minutes: number;
  profile_id: string;
  verdict_check: string;
  release_environment: string;
  private_network: boolean;
  ca_certificate: string;
  include_archived: boolean;
};
export type GTTestResult = { ok: boolean; message: string; details: string[]; warnings: string[]; server: string; scopes: string[]; rate_limit?: RateLimit };

// Un repository scoperto ha la forma del repository di GitHub.
export type GTRepo = Repository;

export type GTRunStats = {
  repos: number;
  discovered?: number;
  new_repos?: number;
  releases: number;
  sboms: number;
  vuln_reports: number;
  check_runs: number;
  attachments: number;
  requests: number;
  profile_version?: number;
  duration_ms: number;
  errors?: string[];
  warnings?: string[];
};
export type GTRun = { id: string; trigger: "schedule" | "manual" | "webhook"; scope: string; status: SyncStatus; stats: GTRunStats; error: string | null; started_at: string; finished_at: string | null };

export const giteaApi = {
  list: () => call<GTIntegrationList>("GET", `${G}/integrations`),
  create: (i: GTIntegrationInput) => call<{ id: string }>("POST", `${G}/integrations`, i),
  update: (id: string, i: GTIntegrationInput) => call<void>("PATCH", `${G}/integrations/${q(id)}`, i),
  remove: (id: string) => call<void>("DELETE", `${G}/integrations/${q(id)}`),
  test: (id: string) => call<GTTestResult>("POST", `${G}/integrations/${q(id)}/test`),
  webhookSecret: (id: string) => call<{ secret: string }>("POST", `${G}/integrations/${q(id)}/webhook-secret`),
  repos: (id: string) => call<{ items: GTRepo[] }>("GET", `${G}/integrations/${q(id)}/repos`),
  discover: (id: string) => call<{ found: number; new: number; missing: number; suggested: number }>("POST", `${G}/integrations/${q(id)}/discover`),
  mapRepo: (id: string, repo: string, body: { status: "confirmed" | "ignored" | "none"; application_id?: string; service_name?: string }) =>
    call<GTRepo>("PUT", `${G}/integrations/${q(id)}/repos/${q(repo)}`, body),
  accept: (id: string, ids: string[]) => call<{ confirmed: number }>("POST", `${G}/integrations/${q(id)}/repos/accept`, { ids }),
  sync: (id: string, full = false) => call<{ run_id: string }>("POST", `${G}/integrations/${q(id)}/sync${full ? "?full=true" : ""}`),
  runs: (id: string) => call<{ items: GTRun[] }>("GET", `${G}/integrations/${q(id)}/runs`),
  preview: (id: string, body: { repository: string; definition?: unknown; profile_id?: string; tag?: string }) =>
    call<Preview>("POST", `${G}/integrations/${q(id)}/preview`, body),
  presets: () => call<{ presets: Preset[] }>("GET", `${G}/presets`),
};
