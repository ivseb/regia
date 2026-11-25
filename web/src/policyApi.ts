// API della politica della pipeline, dell'aderenza e dell'adozione guidata (DevSecOps, fase 3;
// replica/devsecops.md §3.3 e §3.4). Modulo a parte, caricato con le pagine che lo usano.
import { ApiError, call } from "./api";
import { tr } from "./i18n/core";

const q = encodeURIComponent;

export type Mode = "off" | "warn" | "block";
export const MODES: Mode[] = ["off", "warn", "block"];
export const MODE_NAME: Record<Mode, string> = { get off() { return tr("governance.pipelinepolicy.spento"); }, get warn() { return tr("governance.pipelinepolicy.avviso"); }, get block() { return tr("governance.pipelinepolicy.blocca"); } };

export type Control = { key: string; name: string; help?: string; aliases?: string[] };
export type PolicyClass = {
  key: string;
  name: string;
  criticality?: string[];
  critical_function?: "" | "yes" | "no";
  data?: string[];
  exposure?: string[];
  modes: Record<string, Mode>;
};
export type PolicyDef = {
  format: string;
  exposure_field?: string;
  controls: Control[];
  classes: PolicyClass[];
  fallback: PolicyClass;
};
export type PolicyVersion = { version: number; note: string; created_at: string; created_by: string | null; definition?: PolicyDef };
export type PolicyState = {
  configured: boolean;
  version: number;
  definition: PolicyDef;
  versions: PolicyVersion[];
  observed: { data: string[]; exposure: string[]; checks: string[] };
  can_edit: boolean;
  fields: { key: string; name: string }[];
};
export type Change = { kind: string; text: string; stricter?: boolean };
export type Status = "aderente" | "parziale" | "fuori_politica" | "non_in_pipeline";
export const STATUS_NAME: Record<Status, string> = {
  get aderente() { return tr("governance.policyapi.aderente"); },
  get parziale() { return tr("governance.policyapi.parziale"); },
  get fuori_politica() { return tr("governance.policyapi.fuori_politica"); },
  get non_in_pipeline() { return tr("governance.policyapi.non_in_pipeline"); },
};
export type Counts = { total: number } & Record<Status, number>;
export type Impact = {
  before_configured: boolean;
  before: { apps: Counts; services: Counts };
  after: { apps: Counts; services: Counts };
  changed_apps: number;
  changed_services: number;
  transitions: { from: string; to: string; apps: number; services: number }[];
  items: { kind: "application" | "service"; id: string; key?: string; name: string; from: string; to: string; of?: string }[];
  changes: Change[];
  problems: { path: string; message: string }[];
};

export type ControlState = { key: string; name: string; required: Mode; observed?: Mode; status: "ok" | "missing" | "weaker" | "na" };
export type ServiceAdherence = {
  id: string;
  name: string;
  repository: string | null;
  status: Status;
  controls: ControlState[];
  missing: string[];
  last_run: string | null;
  commit?: string;
};
export type AppAdherence = {
  id: string;
  key: string;
  name: string;
  code: string | null;
  class: string;
  class_key: string;
  criticality: string;
  supports_critical_function: boolean;
  status: Status;
  missing: string[];
  services: ServiceAdherence[];
  required: ControlState[];
};
export type Adherence = {
  configured: boolean;
  version: number;
  apps: Counts;
  services: Counts;
  unassigned_services: number;
  gaps: { key: string; name: string; missing: number; weaker: number; ok: number }[];
  assets: { id: string; key: string; name: string; applications: Counts }[];
  applications: AppAdherence[];
  truncated: boolean;
};

export type ChecklistItem = { key: string; label: string; state: "ok" | "todo" | "info"; detail?: string; action?: string };
export type AdoptionPR = {
  id: string;
  application_id: string | null;
  repository: string;
  base_branch: string;
  branch: string;
  number: number;
  url: string;
  title: string;
  state: "open" | "merged" | "closed";
  policy_version: number | null;
  class: string | null;
  files: { path: string; status: string }[];
  last_error: string | null;
  created_at: string;
  synced_at: string | null;
  closed_at: string | null;
};
export type PipelineApp = { configured: boolean; version: number; application: AppAdherence; items: ChecklistItem[]; can_configure: boolean };
export type PipelineView = {
  subject: { id: string; key: string; title: string; type: string };
  configured: boolean;
  version: number;
  applications: PipelineApp[];
  can_configure: boolean;
};

export type Template = { key: string; name: string; description: string; uses: string; needs: string[]; controls: string[]; stack: string[]; default: boolean };
export type AdoptionParam = { key: string; label: string; default?: string; options?: string[] };
export type AdoptionSource = {
  integration_id: string | null;
  integration: string;
  profile_id: string;
  profile: string;
  templates: Template[];
  params: AdoptionParam[];
  workflow_path: string;
  config_path: string;
  can_open_pr: boolean | null;
  can_open_pr_reason: string;
};
export type AdoptionRepo = { id: string; integration_id: string; full_name: string; default_branch: string; mapped: boolean; suggested: boolean; archived: boolean };
export type AdoptionOptions = { sources: AdoptionSource[]; repositories: AdoptionRepo[]; adoptions: AdoptionPR[] };
export type AdoptionInput = {
  application_id: string;
  integration_id?: string;
  profile_id?: string;
  repository_id?: string;
  templates?: string[];
  params?: Record<string, string>;
};
export type DiffLine = { op: " " | "+" | "-"; text: string };
export type PreviewFile = { path: string; status: "new" | "changed" | "unchanged"; content: string; diff: DiffLine[]; added: number; removed: number };
export type AdoptionPreview = {
  application: string;
  class: string;
  policy_version: number;
  controls: { key: string; name: string; mode: Mode }[];
  uncovered: string[];
  profile: string;
  repository: string;
  default_branch: string;
  files: PreviewFile[];
  warnings: string[];
  can_open_pr: boolean;
  can_open_reason: string;
};

// Profilo di pipeline: solo la sezione di adozione (il resto della definizione resta com'è).
export type AdoptionSpec = {
  workflow_path?: string;
  config_path?: string;
  config_key?: string;
  workflow_name?: string;
  branches?: string[];
  params?: AdoptionParam[];
  templates: {
    key: string;
    name: string;
    description?: string;
    uses: string;
    with?: Record<string, string>;
    inherit_secrets?: boolean;
    needs?: string[];
    default?: boolean;
    controls?: string[];
    stack?: string[];
  }[];
};
export type ProfileItem = { id: string; key: string; name: string; version: number; definition: Record<string, unknown> };

/** Scarica un file prodotto da una richiesta POST (lo zip dei file di adozione). */
async function download(path: string, body: unknown): Promise<{ blob: Blob; name: string }> {
  const res = await fetch(`/api/v1${path}`, {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new ApiError(res.status, data.code ?? "unknown", data.detail ?? res.statusText, data.field, data.problems);
  }
  const cd = res.headers.get("Content-Disposition") ?? "";
  return { blob: await res.blob(), name: /filename="([^"]+)"/.exec(cd)?.[1] ?? "pipeline.zip" };
}

export const polApi = {
  policy: () => call<PolicyState>("GET", "/devsecops/policy"),
  save: (definition: PolicyDef, note: string) => call<PolicyState>("PUT", "/devsecops/policy", { definition, note }),
  impact: (definition: PolicyDef) => call<Impact>("POST", "/devsecops/policy/impact", { definition }),
  diff: (from: number, to: number) => call<{ changes: Change[] }>("GET", `/devsecops/policy/diff?from=${from}&to=${to}`),
  version: (n: number) => call<PolicyVersion>("GET", `/devsecops/policy/versions/${n}`),
  adherence: () => call<Adherence>("GET", "/devsecops/adherence"),
  pipeline: (ref: string) => call<{ pipeline: PipelineView; adoptions: Record<string, AdoptionPR[]> }>("GET", `/devsecops/elements/${q(ref)}/pipeline`),
  options: (appId: string) => call<AdoptionOptions>("GET", `/devsecops/applications/${q(appId)}/adoption`),
  syncAdoptions: (appId: string) => call<{ checked: number; adoptions: AdoptionPR[] }>("POST", `/devsecops/applications/${q(appId)}/adoption/sync`),
  repositories: (text: string) => call<{ items: AdoptionRepo[] }>("GET", `/devsecops/adoption/repositories?q=${q(text)}`),
  preview: (in_: AdoptionInput) => call<AdoptionPreview>("POST", "/devsecops/adoption/preview", in_),
  openPR: (in_: AdoptionInput) => call<AdoptionPR>("POST", "/devsecops/adoption/pull-request", in_),
  download: (in_: AdoptionInput) => download("/devsecops/adoption/download", in_),
  profiles: () => call<{ items: ProfileItem[] }>("GET", "/devsecops/pipeline-profiles"),
  profile: (id: string) => call<{ profile: ProfileItem }>("GET", `/devsecops/pipeline-profiles/${q(id)}`),
  saveProfile: (id: string, name: string, definition: unknown, note: string) =>
    call<{ version: number }>("PUT", `/devsecops/pipeline-profiles/${q(id)}`, { name, definition, note }),
};
