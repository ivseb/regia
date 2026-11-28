// API del connettore ALM/Quality Center (DevSecOps, sola lettura). Rispecchia internal/api/devsecops_alm.go.
// Regia legge test set, esecuzioni e difetti dei progetti ALM indicati e li lega ai numeri di
// change: non scrive mai su ALM.
import { call } from "./api";
import type { SyncStatus } from "./githubApi";

const q = encodeURIComponent;
const S = "/devsecops/alm";

export type ALMAuth = "apikey" | "user";
/** Dove sta il numero di change. */
export type ALMChangeSource = "test_set_field" | "test_set_name" | "cycle_field" | "release_field";
/** Come si riconosce l'applicazione. */
export type ALMAppSource = "project" | "folder" | "test_set_field";

export type ALMProject = { domain: string; project: string };

export const ALM_DEFAULT_CHANGE_FIELD = "user-01";
export const ALM_DEFAULT_CHANGE_PATTERN = "[A-Za-z]{2,10}[0-9]{4,12}";
export const ALM_DEFAULT_CLOSED = ["Closed", "Rejected"];
export const ALM_MIN_INTERVAL = 10;

export type ALMConfig = {
  base_url: string;
  auth: ALMAuth;
  username?: string;
  client_id?: string;
  projects: ALMProject[];
  change_source: ALMChangeSource;
  change_field?: string;
  change_pattern: string;
  app_source: ALMAppSource;
  app_field?: string;
  defect_closed: string[];
  keep_tester?: boolean;
  timezone: string;
  lookback_days: number;
  interval_minutes: number;
  private_network?: boolean;
  use_proxy?: boolean;
  ca_certificate?: string;
};

export type ALMIntegration = {
  id: string;
  name: string;
  active: boolean;
  config: ALMConfig;
  has_password: boolean;
  has_client_secret: boolean;
  units: { total: number; confirmed: number; suggested: number; ignored: number };
  /** linked: test set con un numero di change. */
  records: { test_sets: number; linked: number; runs: number; defects: number };
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
export type ALMIntegrationList = { items: ALMIntegration[]; allow_private: boolean; dev: boolean; proxy: string };

/** Segreti vuoti = invariati. */
export type ALMIntegrationInput = {
  name: string;
  active?: boolean;
  base_url: string;
  auth: ALMAuth;
  username: string;
  password?: string;
  client_id: string;
  client_secret?: string;
  projects: ALMProject[];
  change_source: ALMChangeSource;
  change_field: string;
  change_pattern: string;
  app_source: ALMAppSource;
  app_field: string;
  defect_closed: string[];
  keep_tester: boolean;
  timezone: string;
  lookback_days: number;
  interval_minutes: number;
  private_network: boolean;
  use_proxy: boolean;
  ca_certificate: string;
};

export type ALMCollectionTest = { collection: string; label: string; readable: boolean; sample: number; error?: string };
export type ALMProjectTest = { project: string; readable: boolean; error?: string; collections: ALMCollectionTest[] };
export type ALMTestResult = { ok: boolean; message: string; method?: string; details: string[]; warnings: string[]; projects: ALMProjectTest[] };

export type ALMAppRef = { id: string; key: string; name: string; code: string };

export type ALMUnitKind = "project" | "folder" | "value";
export type ALMUnitStatus = "none" | "suggested" | "confirmed" | "ignored";
export type ALMUnit = {
  id: string;
  kind: ALMUnitKind;
  name: string;
  path: string;
  missing: boolean;
  status: ALMUnitStatus;
  application: ALMAppRef | null;
  suggestion: { application: ALMAppRef; score: number; reason: string } | null;
  test_sets: number;
  last_synced_at: string | null;
  confirmed_at: string | null;
};

export type ALMDiscoverResult = { found: number; new: number; missing: number; suggested: number };

export type ALMRunStats = {
  projects: number;
  test_sets: number;
  new_test_sets: number;
  linked: number;
  items: number;
  runs: number;
  new_runs: number;
  defects: number;
  new_units?: number;
  requests: number;
  duration_ms: number;
  errors?: string[];
  warnings?: string[];
};
export type ALMRun = {
  id: string;
  trigger: string;
  scope: string;
  status: SyncStatus;
  stats: ALMRunStats;
  error: string | null;
  started_at: string;
  finished_at: string | null;
};

export type ALMCounts = { planned: number; passed: number; failed: number; blocked: number; not_completed: number; no_run: number; na: number };
export type ALMTestSet = {
  id: string;
  integration_id: string;
  project: string;
  external_id: string;
  name: string;
  folder: string;
  status: string;
  cycle: string;
  release: string;
  change: string;
  /** Chiave dell'applicazione, se l'unità è associata. */
  application: string | null;
  /** Indirizzo td:// che apre il test set nel client di ALM. */
  url: string;
  counts: ALMCounts;
  open_defects: number;
  updated_at: string | null;
};

export type ALMTestSetFilter = { integration?: string; change?: string; limit?: number };

function query(f: Record<string, string | number | undefined>): string {
  const s = new URLSearchParams();
  for (const [k, v] of Object.entries(f)) if (v !== undefined && v !== "") s.set(k, String(v));
  return s.size ? `?${s}` : "";
}

export const almApi = {
  list: () => call<ALMIntegrationList>("GET", `${S}/integrations`),
  get: (id: string) => call<ALMIntegration>("GET", `${S}/integrations/${q(id)}`),
  create: (i: ALMIntegrationInput) => call<{ id: string }>("POST", `${S}/integrations`, i),
  update: (id: string, i: ALMIntegrationInput) => call<void>("PATCH", `${S}/integrations/${q(id)}`, i),
  remove: (id: string) => call<void>("DELETE", `${S}/integrations/${q(id)}`),
  test: (id: string) => call<ALMTestResult>("POST", `${S}/integrations/${q(id)}/test`),
  units: (id: string) => call<{ items: ALMUnit[] }>("GET", `${S}/integrations/${q(id)}/units`),
  mapUnit: (id: string, unit: string, body: { status: "confirmed" | "ignored" | "none"; application_id?: string }) =>
    call<ALMUnit>("PUT", `${S}/integrations/${q(id)}/units/${q(unit)}`, body),
  accept: (id: string, ids: string[]) => call<{ confirmed: number }>("POST", `${S}/integrations/${q(id)}/units/accept`, { ids }),
  discover: (id: string) => call<ALMDiscoverResult>("POST", `${S}/integrations/${q(id)}/discover`),
  sync: (id: string, full = false) => call<{ run_id: string }>("POST", `${S}/integrations/${q(id)}/sync?full=${full}`),
  runs: (id: string) => call<{ items: ALMRun[] }>("GET", `${S}/integrations/${q(id)}/runs`),
  testSets: (f: ALMTestSetFilter = {}) => call<{ items: ALMTestSet[] }>("GET", `${S}/test-sets${query(f)}`),
};
