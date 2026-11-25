// Client dell'import di progetti da Jira, YouTrack e CSV (Configurazione → Import;
// docs/import.md): caricamento o connessione, analisi, mappatura, anteprima, esecuzione in
// background, report e annullamento.
import { ApiError, call, type State } from "./api";
import type { Key } from "./i18n/core";

const q = encodeURIComponent;

export type Source = "jira_csv" | "youtrack_csv" | "csv" | "jira_api" | "youtrack_api";
export type RunStatus = "validating" | "pending" | "preview" | "running" | "done" | "failed" | "rolled_back";

export type NameCount = { name: string; count: number };
export type StateInfo = NameCount & { category?: string; types: string[] };
export type UserInfo = { ref: string; name: string; email?: string; count: number };
export type FieldInfo = { name: string; count: number; distinct: number; samples: string[]; data_type: string; multi: boolean };
export type SprintRow = { name: string; count: number; open: number; state?: string; start?: string; end?: string };

export type Analysis = {
  system: "jira" | "youtrack" | "csv";
  format: "csv" | "api";
  instance?: string;
  issues: number;
  projects: NameCount[];
  types: NameCount[];
  states: StateInfo[];
  priorities: NameCount[];
  labels: NameCount[];
  labels_total: number;
  users: UserInfo[];
  fields: FieldInfo[];
  sprints: SprintRow[];
  link_types: NameCount[];
  comments: number;
  worklogs: number;
  attachments: number;
  links: number;
  with_parent: number;
  date_format: string;
  header?: string[];
  columns?: string[];
  warnings: string[];
  key_prefix?: string;
};

export type SpaceTarget = { mode: "existing" | "new"; key: string; team_name?: string; keep_numbers?: boolean };
export type UserTarget = { mode: "member" | "placeholder" | "none"; member_id?: string };
export type FieldTarget = { mode: "existing" | "new" | "ignore"; key?: string; name?: string; data_type?: string; multi?: boolean };
export type SprintTarget = { start?: string; end?: string; state: "planned" | "active" | "closed"; skip?: boolean };
export type Mapping = {
  columns?: string[];
  date_format: string;
  timezone?: string;
  space: SpaceTarget;
  types: Record<string, string>;
  states: Record<string, Record<string, string>>;
  users: Record<string, UserTarget>;
  fields: Record<string, FieldTarget>;
  priority: { field: string; values?: Record<string, string> };
  labels: FieldTarget;
  sprints: Record<string, SprintTarget>;
  links: Record<string, string>;
  include: { comments: boolean; worklogs: boolean; attachments: boolean; links: boolean; sprints: boolean };
};

export type RowError = { line?: number; key?: string; field?: string; message: string; level: "error" | "warning" };
export type RollbackReport = {
  deleted: number;
  kept: number;
  kept_modified: number;
  kept_updated: number;
  comments: number;
  worklogs: number;
  relations: number;
  fields: number;
  options: number;
  space_deleted: boolean;
  not_reverted: number;
};
export type Report = {
  dry_run: boolean;
  created: number;
  updated: number;
  skipped: number;
  errors: number;
  warnings: number;
  by_type: Record<string, number>;
  comments: number;
  worklogs: number;
  relations: number;
  hierarchy: number;
  sprints: number;
  attachments: number;
  attachment_links: number;
  fields_created: string[];
  options_created: number;
  space: string;
  space_new: boolean;
  placeholder?: string;
  messages: string[];
  trial?: string;
  duration_ms: number;
  rollback?: RollbackReport;
};
export type Progress = { phase?: string; done: number; total: number; message?: string };

export type Run = {
  id: string;
  source: Source;
  system: string;
  file_name: string;
  status: RunStatus;
  profile_id: string | null;
  profile: string | null;
  rows_total: number;
  rows_ok: number;
  rows_error: number;
  analysis?: Analysis;
  mapping?: Mapping;
  progress: Progress;
  report?: Report;
  errors: RowError[];
  space_id: string | null;
  space_key: string | null;
  created_by: string | null;
  created_at: string;
  started_at: string | null;
  finished_at: string | null;
  interrupted: boolean;
  needs_token: boolean;
};

export type Problem = { section: string; item?: string; message: string };
export type Profile = { id: string; name: string; source: string; mapping: Mapping; updated_at: string };
export type DateFormat = { key: string; name: string; example: string };
export type Catalog = {
  spaces: { key: string; name: string; team: string }[];
  types: { key: string; name: string; phases: State[] }[];
  members: { id: string; name: string; email?: string; status: string }[];
  fields: { key: string; name: string; data_type: string; multi: boolean; options?: string[] }[];
  relations: { key: string; name: string }[];
  date_formats: DateFormat[];
  roles: Record<string, string>;
  data_types: string[];
  profiles: Profile[];
  sources: { key: Source; name: string }[];
};
export type Connection = {
  source: "jira_api" | "youtrack_api";
  base_url: string;
  deployment?: "cloud" | "server";
  email?: string;
  token: string;
  project: string;
  query?: string;
  limit?: number;
};

/** Carica il CSV con l'avanzamento (fetch non lo espone): onProgress riceve 0..1. */
function upload(source: Source, file: File, delimiter: string, onProgress?: (p: number) => void): Promise<Run> {
  return new Promise((resolve, reject) => {
    const form = new FormData();
    form.append("source", source);
    if (delimiter) form.append("delimiter", delimiter);
    form.append("file", file, file.name);
    const xhr = new XMLHttpRequest();
    xhr.open("POST", "/api/v1/imports/upload");
    xhr.withCredentials = true;
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) onProgress?.(e.loaded / e.total);
    };
    xhr.onload = () => {
      let data: Record<string, unknown> = {};
      try {
        data = JSON.parse(xhr.responseText);
      } catch {
        /* risposta non JSON */
      }
      if (xhr.status >= 200 && xhr.status < 300) resolve(data as unknown as Run);
      else reject(new ApiError(xhr.status, String(data.code ?? "unknown"), String(data.detail ?? xhr.statusText), data.field as string | undefined));
    };
    xhr.onerror = () => reject(new ApiError(0, "network", "Errore di rete durante il caricamento: riprova."));
    xhr.send(form);
  });
}

export const importApi = {
  runs: () => call<{ items: Run[] }>("GET", "/imports"),
  run: (id: string) => call<Run>("GET", `/imports/${q(id)}`),
  catalog: () => call<Catalog>("GET", "/imports/catalog"),
  upload,
  connect: (c: Connection) => call<Run>("POST", "/imports/connect", c),
  saveMapping: (id: string, m: Mapping) => call<{ run: Run; problems: Problem[] }>("PUT", `/imports/${q(id)}/mapping`, m),
  suggest: (id: string) => call<Mapping>("GET", `/imports/${q(id)}/suggest`),
  applyProfile: (id: string, profile: string) => call<{ run: Run; problems: Problem[] }>("POST", `/imports/${q(id)}/profile/${q(profile)}`),
  preview: (id: string) => call<Run>("POST", `/imports/${q(id)}/preview`),
  start: (id: string, token?: { token: string; email?: string }) => call<Run>("POST", `/imports/${q(id)}/start`, token ?? {}),
  rollback: (id: string) => call<Run>("POST", `/imports/${q(id)}/rollback`),
  profiles: () => call<{ items: Profile[] }>("GET", "/import-profiles"),
  saveProfile: (p: { name: string; run_id?: string; mapping?: Mapping }, id?: string) =>
    call<Profile>(id ? "PUT" : "POST", id ? `/import-profiles/${q(id)}` : "/import-profiles", p),
  deleteProfile: (id: string) => call<void>("DELETE", `/import-profiles/${q(id)}`),
};

// Etichette: i testi stanno nel catalogo (altro.import.*); si leggono con t().
export const SOURCE_LABEL: Record<Source, Key> = {
  jira_csv: "altro.import.source.jira_csv",
  youtrack_csv: "altro.import.source.youtrack_csv",
  csv: "altro.import.source.csv",
  jira_api: "altro.import.source.jira_api",
  youtrack_api: "altro.import.source.youtrack_api",
};

export const STATUS_LABEL: Record<RunStatus, Key> = {
  validating: "altro.import.status.validating",
  pending: "altro.import.status.pending",
  preview: "altro.import.status.preview",
  running: "altro.import.status.running",
  done: "altro.import.status.done",
  failed: "altro.import.status.failed",
  rolled_back: "altro.import.status.rolled_back",
};

export const PHASE_LABEL: Record<string, Key> = {
  lettura: "altro.import.phase.lettura",
  preparazione: "altro.import.phase.preparazione",
  elementi: "altro.import.phase.elementi",
  collegamenti: "altro.import.phase.collegamenti",
  commenti: "altro.import.phase.commenti",
  ore: "altro.import.phase.ore",
  allegati: "altro.import.phase.allegati",
  completato: "altro.import.phase.completato",
  annullamento: "altro.import.phase.annullamento",
  annullato: "altro.import.phase.annullato",
};

export const DATA_TYPE_LABEL: Record<string, Key> = {
  string: "altro.import.dataType.string",
  text: "altro.import.dataType.text",
  integer: "altro.import.dataType.integer",
  decimal: "altro.import.dataType.decimal",
  boolean: "altro.import.dataType.boolean",
  date: "altro.import.dataType.date",
  datetime: "altro.import.dataType.datetime",
  duration: "altro.import.dataType.duration",
  url: "altro.import.dataType.url",
  enum: "altro.import.dataType.enum",
  member: "altro.import.dataType.member",
};
