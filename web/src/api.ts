// Client dell'API. Sarà sostituito dal client generato da OpenAPI; i tipi rispecchiano le risposte del backend.
import type { QDefinition, QEvaluation } from "./condition";
import type { FieldView } from "./fieldsApi";
import { errorText } from "./i18n/core";

export type Membership = { tenant_id: string; slug: string; name: string; member_id: string; member_type: string;
  /** Stato dell'organizzazione: solo «active» si può usare; «suspended» la sospende il superamministratore. */
  status?: "active" | "suspended" | "offboarding";
};

export type LanguageInfo = {
  effective: "it" | "en";
  source: "member" | "tenant" | "browser" | "default";
  member: "it" | "en" | null;
  tenant: "it" | "en" | null;
  options: ("it" | "en")[];
};

export type Me = {
  account_id: string;
  email: string;
  display_name: string;
  tenants: Membership[];
  /** Superamministratore dell'istanza: può creare organizzazioni. */
  is_platform_admin?: boolean;
  /** Lingua effettiva dell'interfaccia: la scelta del membro, il predefinito del tenant o il browser. */
  language?: LanguageInfo;
  active_tenant: {
    tenant_id: string;
    member_id: string;
    member_type: string;
    permissions: string[];
    /** Moduli accesi del tenant (chiave → acceso); un modulo assente vale acceso. */
    modules?: Record<string, boolean>;
    /** «auditor»: menu ridotto ai moduli di controllo, in sola lettura. */
    menu_profile?: string;
  } | null;
};

export type Element = {
  id: string;
  key: string;
  type: string;
  type_name: string;
  title: string;
  body_text: string;
  state: string;
  state_category: string;
  fields: Record<string, unknown>;
  version: number;
  created_at: string;
  updated_at: string;
  // Colonne della vista tabella (piano, passo 16).
  space_key?: string;
  number?: number | null;
  assignee_id?: string | null;
  reporter_id?: string | null;
  parent_id?: string | null;
  resolved_at?: string | null;
  start_at?: string | null;
  due_at?: string | null;
  /** Colonne della domanda (solo per le richieste, se la lista le ha chieste: «demand=1»). */
  demand?: DemandColumn;
  /** Etichette (tag) dell'elemento, nelle liste. */
  tags?: Tag[];
};

/** Etichetta (tag) di un elemento. */
export type Tag = { id: string; name: string; color?: string };

/** Dati della domanda mostrati come colonne nella lista delle richieste. */
export type DemandColumn = {
  unit: string | null;
  sponsor: string | null;
  estimate_low_cents: number | null;
  estimate_high_cents: number | null;
  regulatory_deadline: string | null;
  is_mandatory: boolean;
  score: number | null;
  days_in_state: number;
};

export type State = { id: string; name: string; category: string; color?: string; off_path?: boolean; labels?: Record<string, string> };
export type ProcessRef = { id: string; key: string; name: string; version_id: string; version: number };
export type TypeInfo = { key: string; name: string; states: State[]; process?: ProcessRef };

export type Stakeholder = { role: string; role_name: string; member_id: string | null; group_id: string | null; name: string };
export type GateResult = { key: string; name: string; passed: boolean; message?: string };
export type TransitionView = { to: string; label: string; comment_required: boolean; gates: GateResult[]; blocked: string[]; forward?: boolean };

export type DemandView = {
  requesting_unit_id: string | null;
  requesting_unit: string | null;
  driver: string | null;
  is_mandatory: boolean;
  regulatory_deadline: string | null;
  desired_date: string | null;
  estimate_low_cents: number | null;
  estimate_high_cents: number | null;
  estimate_effort_days: number | null;
  currency: string;
  /** Periodo di budget di competenza (budget.md §6). */
  budget_period_id?: string | null;
  budget_period?: string | null;
};

export type Step = {
  id: string;
  ordinal: number;
  approver: string;
  decision: string;
  comment: string | null;
  conditions: string | null;
  decided_by: string | null;
  decided_at: string | null;
  can_decide: boolean;
  // Per un organo vale la sua impostazione «si può decidere in linea»; negli altri casi è vero.
  inline_decision: boolean;
};
export type ApprovalRequest = { id: string; stage: string; status: string; amount_cents: number | null; requested_at: string; steps: Step[] };

export type ElementDetail = {
  element: Element;
  stakeholders: Stakeholder[];
  transitions: TransitionView[];
  demand?: DemandView;
  approvals: ApprovalRequest[];
  phases: State[];
  process: ProcessRef | null;
  impact?: Impact;
  application?: Application;
  // Campi personalizzati del tipo con valore, visibilità e obbligatorietà (fieldsApi.ts).
  custom_fields?: FieldView[];
};

// Architettura (sdlc.md §3-4): catalogo e analisi tecnica delle richieste.
export type Team = {
  id: string;
  name: string;
  lead_id: string | null;
  lead: string | null;
  applications: number;
  capacity_days_week: number | null;
};
export type Application = {
  id: string;
  key: string;
  name: string;
  description: string;
  state: string;
  state_category: string;
  code: string | null;
  criticality: string | null;
  supports_critical_function: boolean;
  data_classification: string | null;
  hosting: string | null;
  rto_minutes: number | null;
  rpo_minutes: number | null;
  team_id: string | null;
  team: string | null;
  assets: string[];
};
export type Component = { application_id: string; key: string; code: string | null; name: string; relation: string; team: string | null; criticality: string | null };
export type Asset = {
  id: string;
  key: string;
  name: string;
  description: string;
  kind: string;
  state: string;
  owner_unit_id: string | null;
  owner_unit: string | null;
  components: Component[];
};
export type Impact = {
  id: string;
  key: string;
  title: string;
  state: string;
  state_category: string;
  request_id: string;
  request_key: string;
  request_title: string;
  application_id: string;
  application_key: string;
  application: string;
  criticality: string | null;
  supports_critical_function: boolean;
  team_id: string | null;
  team: string | null;
  change_kind: string;
  technical_note: string;
  estimate_effort_days: number | null;
  estimate_cost_cents: number | null;
  depends_on: string[];
};
export type Analysis = {
  assets: { id: string; key: string; name: string }[];
  proposed: { application_id: string; key: string; name: string; via: string; relation: string; team: string | null }[];
  impacts: Impact[];
  can_edit: boolean;
};
export type ImportReport = {
  dry_run: boolean;
  created: number;
  updated: number;
  errors: number;
  teams_created: string[];
  assets_created: string[];
  rows: { line: number; code: string; name: string; outcome: string; message?: string }[];
};
// Pianificazione (Gantt, roadmap, carico dei team).
export type PlanTask = {
  id: string;
  key: string;
  application: string;
  team_id: string | null;
  team: string | null;
  state: string;
  state_category: string;
  start: string | null;
  end: string | null;
  estimate_effort_days: number | null;
  depends_on: string[];
  team_capacity: number;
};
export type RequestWindow = {
  id: string;
  key: string;
  title: string;
  state: string;
  state_category: string;
  start: string | null;
  end: string | null;
  desired_date: string | null;
  regulatory_deadline: string | null;
  is_mandatory: boolean;
  driver: string | null;
  impacts: number;
  scheduled: number;
  phase_index: number;
  phase_count: number;
};
export type Plan = { request: RequestWindow; tasks: PlanTask[]; can_edit: boolean };
export type PlanChange = { id: string; key: string; start: string; end: string; cause: string };
export type TeamLoad = { id: string; name: string; capacity_days_week: number; weeks: { week: string; load_days: number; capacity_days: number }[] };

export type ImpactInput = {
  application_id?: string;
  team_id?: string;
  change_kind?: string;
  technical_note?: string;
  estimate_effort_days?: number;
  depends_on?: string[];
};

export type Activity = {
  id: number;
  actor_name: string;
  kind: string;
  field_key: string | null;
  added: Record<string, unknown> | null;
  removed: Record<string, unknown> | null;
  created_at: string;
  relation_label?: string;
  other?: string;
};

export type InboxItem = {
  step_id: string;
  element_id: string;
  element_key: string;
  title: string;
  stage: string;
  amount_cents: number | null;
  requested_at: string;
  inline_decision: boolean;
  /** «stage_gate», «budget», … o «timesheet»: una settimana di timesheet non ha elemento (element_key vuoto). */
  approval_subject: string;
  timesheet_member: string | null;
  timesheet_week: string | null;
};

export type Member = { id: string; display_name: string; member_type: string };
export type OrgUnit = { id: string; name: string; kind: string };

export type NewRequest = {
  title: string;
  description: string;
  sponsor_id?: string;
  business_owner_id?: string;
  requesting_unit_id?: string;
  driver?: string;
  is_mandatory?: boolean;
  regulatory_deadline?: string;
  desired_date?: string;
  fields?: Record<string, unknown>;
};

// Configurazione dei processi (sdlc.md): definizione versionata di fasi, passaggi, gate e regole.
export type Phase = { name: string; category: string; color?: string; description?: string; off_path?: boolean; labels?: Record<string, string> };
export type ProcessTransition = {
  from: string[];
  to: string;
  label?: string;
  roles?: string[];
  system?: boolean;
  when?: string;
  gates?: string[];
  approval?: string;
  comment_required?: boolean;
};
export type Gate = { key: string; name: string; condition?: string; check?: string; message?: string };
export type RuleAction = { kind: string; role?: string; group?: string; member?: string; replace?: boolean; ref?: string; when?: string; params?: Record<string, string> };
export type Rule = { key: string; name: string; on: string; when?: string; actions: RuleAction[] };
export type Definition = { phases: Phase[]; initial: string; transitions: ProcessTransition[]; gates?: Gate[]; rules?: Rule[] };
export type Problem = { level: "error" | "warning"; path: string; message: string };
export type VersionInfo = {
  id: string;
  number: number;
  status: "draft" | "scheduled" | "published" | "superseded";
  notes: string;
  created_at: string;
  created_by: string | null;
  published_at: string | null;
  published_by: string | null;
  elements: number;
  /** Entrata in vigore, fine facoltativa, deprecazione e stato derivato (src/versioni.tsx). */
  effective_from: string | null;
  effective_to: string | null;
  deprecated_at: string | null;
  lifecycle: "draft" | "scheduled" | "in_force" | "deprecated" | "concluded" | "withdrawn";
};
export type TransitionRule = { default?: string; phases?: Record<string, string> };
export type TransitionGroup = { from_version: number; phase: string; count: number; mode: string; target_phase?: string; missing?: boolean };
export type PublishPreview = {
  version: number;
  immediate: boolean;
  effective_from: string;
  effective_to?: string;
  in_force: number;
  withdrawn: number;
  plan: { to_version: number; rule: TransitionRule; groups: TransitionGroup[]; total: number; auto: number; stay: number; manual: number };
  problems: Problem[];
};
export type PublishOptions = { notes?: string; effective_from?: string; effective_to?: string; transition?: TransitionRule | null };
export type Guide = { id: string; phase: string | null; kind: "document" | "link" | "knowledge"; title: string; target: string; position: number };
export type GuideInput = { phase: string | null; kind: Guide["kind"]; title: string; target: string };
export type ProcessInfo = {
  id: string;
  key: string;
  name: string;
  description: string;
  type: string;
  type_name: string;
  priority: number;
  applies_when: string;
  archived: boolean;
  updated_at: string;
  versions: VersionInfo[];
  current?: Definition;
  draft?: Definition;
  draft_problems?: Problem[];
  warnings?: string[];
};
export type MigrationGroup = { from_version: number; phase: string; count: number; target_phase: string; ok: boolean };
export type MigrationPlan = { to_version: number; groups: MigrationGroup[]; total: number; blocked: number };
export type ConditionCheck = { ok: boolean; error?: string; keys: string[]; result?: boolean; facts?: Record<string, unknown[]> };

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public field?: string,
    public problems?: Problem[],
    // Errori della query: frammento sbagliato (indici UTF-16, come le stringhe JavaScript).
    public span?: { pos: number; end: number },
    // Valori variabili del messaggio (campo `params` del Problem), per la traduzione.
    public params?: Record<string, string | number>,
  ) {
    // Fuori dall'italiano il messaggio è la traduzione del code; il detail del server resta come riserva.
    super(errorText(code, message, params));
  }
}

export async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(`/api/v1${path}`, {
    method,
    credentials: "same-origin",
    headers: body === undefined ? {} : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (res.status === 204) return undefined as T;
  const data = await res.json().catch(() => ({}));
  if (!res.ok)
    throw new ApiError(res.status, data.code ?? "unknown", data.detail ?? res.statusText, data.field, data.problems,
      typeof data.pos === "number" ? { pos: data.pos, end: data.end } : undefined, data.params);
  return data as T;
}

const q = encodeURIComponent;

export const api = {
  me: () => call<Me>("GET", "/me"),
  login: (email: string, password: string) => call<Me>("POST", "/auth/login", { email, password }),
  logout: () => call<void>("POST", "/auth/logout"),
  switchTenant: (tenant_id: string) => call<Me>("POST", "/me/tenant", { tenant_id }),

  type: (key: string) => call<TypeInfo>("GET", `/types/${q(key)}`),
  search: (query: string, cursor?: string, limit?: number, opts: { demand?: boolean } = {}) =>
    call<{ items: Element[]; next?: string; labels?: Record<string, { label: string; key?: string; type?: string }>; demand?: Record<string, DemandColumn> }>(
      "GET",
      `/elements?q=${q(query)}${cursor ? `&cursor=${q(cursor)}` : ""}${limit ? `&limit=${limit}` : ""}${opts.demand ? "&demand=1" : ""}`,
    ).then((r) => ({ ...r, items: r.demand ? r.items.map((el) => (r.demand![el.id] ? { ...el, demand: r.demand![el.id] } : el)) : r.items })),
  element: (ref: string) => call<ElementDetail>("GET", `/elements/${q(ref)}`),
  transition: (id: string, to: string, comment?: string) => call<Element>("POST", `/elements/${id}/transitions`, { to, comment }),
  activity: (id: string) => call<{ items: Activity[] }>("GET", `/elements/${id}/activity`),

  createRequest: (r: NewRequest) => call<Element>("POST", "/demand/requests", r),
  /** requesting_unit_id e budget_period_id: "" li toglie. */
  updateDemand: (id: string, d: Partial<Pick<DemandView, "estimate_low_cents" | "estimate_high_cents" | "estimate_effort_days" | "requesting_unit_id" | "budget_period_id">>) =>
    call<void>("PATCH", `/demand/requests/${id}`, d),

  inbox: () => call<{ items: InboxItem[] }>("GET", "/approvals/inbox"),
  decide: (stepId: string, decision: string, comment?: string, conditions?: string) =>
    call<void>("POST", `/approvals/steps/${stepId}/decision`, { decision, comment, conditions }),

  members: () => call<{ items: Member[] }>("GET", "/members"),

  teams: () => call<{ items: Team[] }>("GET", "/teams"),
  /** board_key vuota = proposta dal nome; board_model vuoto = una colonna per fase (scrum, kanban, supporto). */
  createTeam: (name: string, lead_id?: string, board?: { board_key?: string; board_model?: string }) =>
    call<{ id: string }>("POST", "/teams", { name, lead_id, ...board }),
  updateTeam: (id: string, name: string, lead_id?: string, capacity_days_week?: number) =>
    call<void>("PATCH", `/teams/${id}`, { name, lead_id, capacity_days_week }),
  assets: () => call<{ items: Asset[] }>("GET", "/architecture/assets"),
  createAsset: (a: { name: string; description?: string; kind?: string }) => call<{ id: string }>("POST", "/architecture/assets", a),
  addComponent: (assetId: string, application_id: string, relation: string) =>
    call<void>("POST", `/architecture/assets/${assetId}/components`, { application_id, relation }),
  removeComponent: (assetId: string, appId: string) => call<void>("DELETE", `/architecture/assets/${assetId}/components/${appId}`),
  applications: (search = "") => call<{ items: Application[] }>("GET", `/architecture/applications?q=${q(search)}`),
  createApplication: (a: Partial<Omit<Application, "id" | "key" | "state" | "state_category" | "team" | "assets">>) =>
    call<Application>("POST", "/architecture/applications", a),
  importApplications: (csv: string, dryRun: boolean) =>
    call<ImportReport>("POST", `/architecture/applications/import${dryRun ? "?dry_run=true" : ""}`, { csv }),
  analysis: (ref: string) => call<Analysis>("GET", `/demand/requests/${q(ref)}/analysis`),
  citeAsset: (ref: string, asset_id: string) => call<void>("POST", `/demand/requests/${q(ref)}/assets`, { asset_id }),
  unciteAsset: (ref: string, assetId: string) => call<void>("DELETE", `/demand/requests/${q(ref)}/assets/${assetId}`),
  createImpact: (ref: string, i: ImpactInput) => call<Impact>("POST", `/demand/requests/${q(ref)}/impacts`, i),
  updateImpact: (id: string, i: ImpactInput) => call<Impact>("PATCH", `/impacts/${id}`, i),

  plan: (ref: string) => call<Plan>("GET", `/demand/requests/${q(ref)}/plan`),
  autoPlan: (ref: string, replan: boolean) => call<{ changes: PlanChange[] }>("POST", `/demand/requests/${q(ref)}/plan/auto`, { replan }),
  scheduleImpact: (id: string, start: string, end: string) =>
    call<{ changes: PlanChange[] }>("PUT", `/impacts/${id}/schedule`, { start, end, cascade: true }),
  shiftRequest: (ref: string, start: string, end: string) =>
    call<{ changes: PlanChange[] }>("PUT", `/demand/requests/${q(ref)}/schedule`, { start, end }),
  roadmap: (closed = false) => call<{ items: RequestWindow[] }>("GET", `/demand/roadmap${closed ? "?closed=true" : ""}`),
  teamLoad: (from: string, to: string) => call<{ items: TeamLoad[] }>("GET", `/teams/load?from=${from}&to=${to}`),

  processes: () => call<{ items: ProcessInfo[]; checks: string[] }>("GET", "/processes"),
  process: (ref: string) => call<ProcessInfo>("GET", `/processes/${q(ref)}`),
  processVersion: (ref: string, n: number) => call<Definition>("GET", `/processes/${q(ref)}/versions/${n}`),
  updateProcess: (ref: string, p: { name?: string; description?: string; priority?: number; applies_when?: string; archived?: boolean }) =>
    call<ProcessInfo>("PATCH", `/processes/${q(ref)}`, p),
  saveDraft: (ref: string, definition: unknown, notes: string) => call<ProcessInfo>("PUT", `/processes/${q(ref)}/draft`, { definition, notes }),
  discardDraft: (ref: string) => call<void>("DELETE", `/processes/${q(ref)}/draft`),
  publish: (ref: string, notes: string) => call<{ process: ProcessInfo; problems: Problem[] }>("POST", `/processes/${q(ref)}/publish`, { notes }),
  publishWith: (ref: string, o: PublishOptions) => call<{ process: ProcessInfo; problems: Problem[] }>("POST", `/processes/${q(ref)}/publish`, o),
  previewPublish: (ref: string, o: PublishOptions) => call<PublishPreview>("POST", `/processes/${q(ref)}/publish/preview`, o),
  setVigore: (ref: string, n: number, body: { effective_from?: string; effective_to?: string }) =>
    call<ProcessInfo>("PATCH", `/processes/${q(ref)}/versions/${n}/vigore`, body),
  guides: (ref: string) => call<{ items: Guide[] }>("GET", `/processes/${q(ref)}/guides`),
  addGuide: (ref: string, g: GuideInput) => call<Guide>("POST", `/processes/${q(ref)}/guides`, g),
  updateGuide: (ref: string, id: string, g: GuideInput) => call<Guide>("PATCH", `/processes/${q(ref)}/guides/${q(id)}`, g),
  removeGuide: (ref: string, id: string) => call<void>("DELETE", `/processes/${q(ref)}/guides/${q(id)}`),
  importProcess: (bundle: unknown, publish: boolean) =>
    call<{ process: ProcessInfo; problems: Problem[] }>("POST", `/processes/import${publish ? "?publish=true" : ""}`, bundle),
  exportUrl: (ref: string, version?: number) => `/api/v1/processes/${q(ref)}/export${version ? `?version=${version}` : ""}`,
  previewMigration: (ref: string, phase_map: Record<string, string>) =>
    call<MigrationPlan>("POST", `/processes/${q(ref)}/migration/preview`, { phase_map }),
  migrate: (ref: string, phase_map: Record<string, string>) => call<MigrationPlan>("POST", `/processes/${q(ref)}/migration`, { phase_map }),
  checkCondition: (condition: string, element?: string) => call<ConditionCheck>("POST", "/conditions/check", { condition, element }),
  orgUnits: () => call<{ items: OrgUnit[] }>("GET", "/org-units"),
};

// ---------------------------------------------------------------------
// Collaborazione (piano, passo 3): commenti, allegati, osservatori e notifiche.
// ---------------------------------------------------------------------

export type Mention = { id: string; name: string };
export type Attachment = {
  id: string;
  element_id: string;
  comment_id: string | null;
  author_name: string;
  name: string;
  mime_type: string;
  size_bytes: number;
  sha256: string;
  width: number | null;
  height: number | null;
  preview: boolean;
  created_at: string;
  can_delete: boolean;
};
export type Comment = {
  id: string;
  element_id: string;
  author_id: string | null;
  author_name: string;
  body: string;
  mentions: Mention[];
  created_at: string;
  edited_at: string | null;
  can_edit: boolean;
  can_delete: boolean;
  attachments: Attachment[];
};
export type WatchStatus = { following: boolean; auto: boolean; followers: number };
export type NotificationKind = "mention" | "comment" | "role" | "phase" | "approval" | "referent" | "review" | "sla" | "report" | "requirement"
  // Knowledge (piano, passo 19): revisione periodica dei documenti controllati.
  | "document"
  // Rischi, fornitori ICT e incidenti (piano, passo 20).
  | "risk"
  // DevSecOps, vulnerabilità governate: scadenze, escalation, eccezioni.
  | "vulnerability"
  // Budget gerarchico: avviso prima della chiusura del periodo.
  | "budget"
  // Timesheet: settimane da approvare, esito, riapertura.
  | "timesheet";
export type Notification = {
  id: string;
  kind: NotificationKind;
  payload: Record<string, unknown>;
  element_id: string | null;
  element_key: string;
  title: string;
  type: string;
  actor_name: string;
  read_at: string | null;
  created_at: string;
};
export type NotificationPrefs = { kinds: { kind: NotificationKind; in_app: boolean; email: boolean }[]; email_available: boolean };

/** Carica un file con l'avanzamento (fetch non lo espone): onProgress riceve 0..1. */
function uploadFile(elementRef: string, file: File, commentId?: string, onProgress?: (p: number) => void): Promise<Attachment> {
  return new Promise((resolve, reject) => {
    const form = new FormData();
    if (commentId) form.append("comment_id", commentId);
    form.append("file", file, file.name);
    const xhr = new XMLHttpRequest();
    xhr.open("POST", `/api/v1/elements/${q(elementRef)}/attachments`);
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
      if (xhr.status >= 200 && xhr.status < 300) resolve(data as unknown as Attachment);
      else reject(new ApiError(xhr.status, String(data.code ?? "unknown"), String(data.detail ?? xhr.statusText)));
    };
    xhr.onerror = () => reject(new ApiError(0, "network", "Errore di rete durante il caricamento: riprova."));
    xhr.send(form);
  });
}

export const collab = {
  comments: (ref: string) => call<{ items: Comment[] }>("GET", `/elements/${q(ref)}/comments`),
  addComment: (ref: string, body: string) => call<Comment>("POST", `/elements/${q(ref)}/comments`, { body }),
  editComment: (id: string, body: string) => call<Comment>("PATCH", `/comments/${id}`, { body }),
  deleteComment: (id: string) => call<void>("DELETE", `/comments/${id}`),

  attachments: (ref: string) => call<{ items: Attachment[]; max_bytes: number }>("GET", `/elements/${q(ref)}/attachments`),
  upload: uploadFile,
  removeAttachment: (id: string) => call<void>("DELETE", `/attachments/${id}`),
  contentUrl: (id: string, download = false) => `/api/v1/attachments/${id}/content${download ? "?download=1" : ""}`,

  watch: (ref: string) => call<WatchStatus>("GET", `/elements/${q(ref)}/watch`),
  setWatch: (ref: string, follow: boolean) => call<WatchStatus>(follow ? "PUT" : "DELETE", `/elements/${q(ref)}/watch`),

  notifications: (unread: boolean, cursor?: string) =>
    call<{ items: Notification[]; next?: string }>("GET", `/notifications?limit=50${unread ? "&unread=true" : ""}${cursor ? `&cursor=${q(cursor)}` : ""}`),
  unread: () => call<{ unread: number }>("GET", "/notifications/count"),
  markRead: (id: string) => call<void>("POST", `/notifications/${id}/read`),
  markAllRead: () => call<{ updated: number }>("POST", "/notifications/read-all"),
  prefs: () => call<NotificationPrefs>("GET", "/me/notification-prefs"),
  setPrefs: (kinds: NotificationPrefs["kinds"]) => call<NotificationPrefs>("PUT", "/me/notification-prefs", { kinds }),
};

// Accesso unico OIDC, profilo e token API personali (piano, passo 15).
export type OIDCConfig = { enabled: boolean; name: string; password_login: boolean };
export type LinkedIdentity = { issuer: string; linked_at: string; last_login_at: string | null };
export type Account = {
  account_id: string;
  email: string;
  display_name: string;
  is_platform_admin: boolean;
  method: "password" | "oidc" | "token";
  has_password: boolean;
  identities: LinkedIdentity[];
  last_login_at: string | null;
  oidc_name: string;
  password_login: boolean;
  tenant?: Membership;
};
export type APIToken = {
  id: string;
  name: string;
  hint: string;
  scopes: string[];
  expires_at: string;
  last_used_at: string | null;
  created_at: string;
};
export type TokenList = { items: APIToken[]; scopes: string[]; default_days: number; max_days: number };

export const ssoApi = {
  oidcConfig: () => call<OIDCConfig>("GET", "/auth/oidc/config"),
  oidcStartUrl: (next: string) => `/api/v1/auth/oidc/start?next=${q(next)}`,
  account: () => call<Account>("GET", "/me/account"),
  tokens: () => call<TokenList>("GET", "/me/tokens"),
  createToken: (t: { name: string; expires_in_days: number; scopes: string[] }) =>
    call<{ token: string; item: APIToken }>("POST", "/me/tokens", t),
  revokeToken: (id: string) => call<void>("DELETE", `/me/tokens/${q(id)}`),
};

// ---------------------------------------------------------------------------
// Valutazione e priorità del portafoglio, governance (piano, passi 8 e 9).
// ---------------------------------------------------------------------------

export type ScaleLevel = { value: number; label: string };
export type Criterion = {
  key: string;
  name: string;
  description?: string;
  weight: number;
  direction: "higher_better" | "lower_better";
  scale: ScaleLevel[];
  suggest?: { when: string; value: number }[];
  suggest_fact?: string;
};
export type ScorecardDefinition = { name: string; description?: string; method: string; scorer_roles: string[]; criteria: Criterion[] };
export type ScorecardVersion = {
  id: string;
  key: string;
  version: number;
  status: "draft" | "published" | "superseded";
  type: string | null;
  notes: string;
  created_at: string;
  published_at: string | null;
  published_by: string | null;
  results: number;
  definition?: ScorecardDefinition;
};
export type Scorecard = {
  key: string;
  name: string;
  type: string | null;
  current: ScorecardVersion | null;
  draft: ScorecardVersion | null;
  versions: ScorecardVersion[];
  can_edit: boolean;
};
export type Vote = { value: number; note?: string };
export type ScoreResult = {
  id: string;
  version: number;
  version_id: string;
  values: Record<string, Vote>;
  total: number;
  comment: string;
  scored_by: string | null;
  scored_at: string;
};
export type Evaluation = {
  scorecard: ScorecardVersion | null;
  can_score: boolean;
  score: number | null;
  current: ScoreResult | null;
  current_definition?: ScorecardDefinition;
  stale: boolean;
  proposals: Record<string, { value: number; reason: string }>;
  history: ScoreResult[];
};
export type PortfolioSettings = {
  mandatory_first: boolean;
  budget_cents: number | null;
  estimate_basis: "low" | "mid" | "high";
  excluded_phases: string[];
  rank_roles: string[];
  updated_at: string | null;
  updated_by: string | null;
};
export type RankItem = {
  position: number;
  id: string;
  key: string;
  title: string;
  state: string;
  state_category: string;
  is_mandatory: boolean;
  driver: string | null;
  regulatory_deadline: string | null;
  desired_date: string | null;
  estimate_low_cents: number | null;
  estimate_high_cents: number | null;
  estimate_cents: number | null;
  score: number | null;
  score_version: number | null;
  score_stale: boolean;
  value_per_cost: number | null;
  cumulative_cents: number;
  funded: boolean | null;
  pinned: boolean;
  pin_reason: string | null;
  pinned_by: string | null;
  pinned_at: string | null;
  can_move: boolean;
};
export type Ranking = {
  items: RankItem[];
  settings: PortfolioSettings;
  budget_cents: number | null;
  total_cents: number;
  funded_cents: number;
  funded_count: number;
  cut: number | null;
  mandatory: number;
  can_admin: boolean;
  scorecard: { key: string; name: string; version: number } | null;
  // Da dove viene il budget: plafond o residuo di un esercizio, impostato a mano, assente.
  /** Presente quando la graduatoria è ristretta a un'area (e a un periodo). */
  scope?: RankScope;
  budget_source: {
    kind: "fiscal_year" | "manual" | "none" | "allocation";
    fiscal_year?: string;
    basis?: "envelope" | "residual";
    suggested: { id: string; name: string; envelope_cents: number } | null;
  };
};

// Cabina di portafoglio (ondata 3): graduatoria, plafond, capacità dei team, roadmap e scenari in una chiamata.
export type CabinaRow = { sponsor: string; unit: string; risk: ("late" | "unplanned" | "no_estimate" | "no_score")[]; efforts: { team_id: string; days: number; weeks: number[] }[] };
export type CabinaPlafond = { id: string; name: string; envelope_cents: number; allocated_cents: number; residual_cents: number; linked: boolean; basis: "envelope" | "residual" };
export type CabinaTeam = { id: string; name: string; capacity: number[]; base: number[] };
export type PortfolioScenario = { id: string; name: string; cut: number; budget_cents: number | null; item_keys: string[]; created_by: string | null; created_at: string };
export type Cabina = {
  ranking: Ranking;
  budget_origin: "allocation" | "fiscal_year" | "plafond" | "simulation" | "manual" | "none";
  plafond: CabinaPlafond | null;
  rows: Record<string, CabinaRow>;
  windows: Record<string, RequestWindow>;
  weeks: string[];
  teams: CabinaTeam[];
  scenarios: PortfolioScenario[];
};
export type SedutaItem = {
  ordinal: number;
  element_id: string;
  key: string;
  title: string;
  state: string;
  is_mandatory: boolean;
  amount_cents: number | null;
  score: number | null;
  outcome: string | null;
  note: string | null;
  conditions: string | null;
  position: number | null;
  reviews: { type: string; stage: string; outcome: string | null; polarity: "positive" | "conditional" | "negative" | null }[];
  requirements: { total: number; verified: number };
};
export type SedutaPackage = {
  id: string;
  title: string;
  body: string;
  scheduled_at: string;
  status: string;
  location: string;
  can_manage: boolean;
  items: SedutaItem[];
  total_cents: number;
};
export type VerbaleResult = { key: string; title: string; space: string; created: boolean; linked: number };

export type RankScope = {
  unit_id: string;
  unit: string;
  period_id?: string;
  period?: string;
  allocation_id?: string;
  allocated_cents: number;
  residual_cents: number;
  has_allocation: boolean;
};

export type BodyMember = { member_id: string; name: string; voting: boolean };
export type GovBody = {
  id: string;
  name: string;
  kind: string;
  kind_name: string;
  description: string;
  chair_id: string | null;
  chair: string | null;
  secretary_id: string | null;
  secretary: string | null;
  quorum: number | null;
  quorum_required: number;
  approval_limit_cents: number | null;
  // Si può decidere dall'elenco senza aprire la richiesta.
  inline_decision: boolean;
  archived: boolean;
  members: BodyMember[];
  groups: { group_id: string; name: string; voting: boolean; members: number }[];
  effective: { member_id: string; name: string; voting: boolean; via_group: string | null }[];
  pending: number;
  next_meeting: string | null;
  can_manage: boolean;
};
export type BodyInput = {
  name: string;
  kind: string;
  description?: string;
  chair_id: string;
  secretary_id?: string;
  quorum?: number | null;
  inline_decision?: boolean;
  archived?: boolean;
  members: { member_id: string; voting?: boolean }[];
  groups?: { group_id: string; voting?: boolean }[];
};
export type MeetingStatus = "planned" | "convened" | "held" | "minuted" | "cancelled";
export type MeetingSummary = {
  id: string;
  body_id: string;
  body: string;
  body_kind: string;
  title: string;
  scheduled_at: string;
  location: string;
  link: string;
  status: MeetingStatus;
  items: number;
  decided: number;
};
export type AgendaItem = {
  ordinal: number;
  element_id: string;
  key: string;
  title: string;
  state: string;
  state_category: string;
  is_mandatory: boolean;
  amount_cents: number | null;
  score: number | null;
  proposed: boolean;
  step_active: boolean;
  outcome: string | null;
  note: string | null;
  conditions: string | null;
  decided_by: string | null;
  decided_at: string | null;
};
export type MeetingProposal = {
  element_id: string;
  key: string;
  title: string;
  state: string;
  is_mandatory: boolean;
  amount_cents: number | null;
  score: number | null;
  requested_at: string;
};
export type MeetingDetail = Omit<MeetingSummary, "items" | "decided"> & {
  convened_at: string | null;
  held_at: string | null;
  chair_id: string | null;
  chair: string | null;
  secretary_id: string | null;
  secretary: string | null;
  items: AgendaItem[];
  proposals: MeetingProposal[];
  attendance: { member_id: string; name: string; voting: boolean; present: boolean }[];
  quorum: { required: number; present: number; voting: number; reached: boolean };
  minutes: string | null;
  minutes_draft: string;
  minutes_approved_at: string | null;
  minutes_approved_by: string | null;
  minutes_hash: string | null;
  can_manage: boolean;
  can_decide: boolean;
  can_approve: boolean;
};
export type MeetingInput = { body_id: string; title?: string; scheduled_at: string; location?: string; link?: string; propose?: boolean };

const mp = (id: string) => `/governance/meetings/${q(id)}`;

export const portfolioApi = {
  scorecards: () => call<{ items: Scorecard[] }>("GET", "/scorecards"),
  scorecard: (key: string) => call<Scorecard>("GET", `/scorecards/${q(key)}`),
  saveScorecardDraft: (key: string, definition: unknown, notes = "") =>
    call<Scorecard>("PUT", `/scorecards/${q(key)}/draft`, { definition, notes }),
  discardScorecardDraft: (key: string) => call<void>("DELETE", `/scorecards/${q(key)}/draft`),
  publishScorecard: (key: string, notes = "") => call<Scorecard>("POST", `/scorecards/${q(key)}/publish`, { notes }),
  evaluation: (ref: string) => call<Evaluation>("GET", `/demand/requests/${q(ref)}/evaluation`),
  score: (ref: string, version_id: string, values: Record<string, Vote>, comment: string) =>
    call<ScoreResult>("POST", `/demand/requests/${q(ref)}/evaluation`, { version_id, values, comment }),
  ranking: () => call<Ranking>("GET", "/portfolio/ranking"),
  /** Graduatoria ristretta a un'area (e a un periodo): il budget è il residuo del suo stanziamento. */
  rankingFor: (unit?: string, period?: string) => call<Ranking>("GET", `/portfolio/ranking?${qs({ unit, period })}`),
  cabina: (unit?: string, period?: string, budgetCents?: number | null) =>
    call<Cabina>("GET", `/portfolio/cabina?${qs({ unit, period, budget_cents: budgetCents != null ? String(budgetCents) : undefined })}`),
  saveScenario: (s: { name: string; cut: number; budget_cents: number | null; item_keys: string[] }) => call<PortfolioScenario>("POST", "/portfolio/scenarios", s),
  deleteScenario: (id: string) => call<void>("DELETE", `/portfolio/scenarios/${q(id)}`),
  sedutaPackage: (id: string) => call<SedutaPackage>("GET", `/portfolio/sedute/${q(id)}/pacchetto`),
  generateVerbale: (id: string, space?: string) => call<VerbaleResult>("POST", `/portfolio/sedute/${q(id)}/verbale`, { space: space ?? "" }),
  rankingCsvUrl: (budgetCents?: number | null, unit?: string, period?: string) =>
    `/api/v1/portfolio/ranking.csv?${qs({ budget_cents: budgetCents != null ? String(budgetCents) : undefined, unit, period })}`,
  move: (ref: string, position: number | null, reason: string) => call<Ranking>("PUT", `/portfolio/ranking/${q(ref)}`, { position, reason }),
  updateSettings: (s: Partial<Omit<PortfolioSettings, "updated_at" | "updated_by">> & { clear_budget?: boolean }) =>
    call<PortfolioSettings>("PUT", "/portfolio/settings", s),

  bodies: () => call<{ items: GovBody[]; kinds: Record<string, string>; groups: { id: string; name: string; members: number }[] }>("GET", "/governance/bodies"),
  createBody: (b: BodyInput) => call<GovBody>("POST", "/governance/bodies", b),
  updateBody: (id: string, b: BodyInput) => call<GovBody>("PATCH", `/governance/bodies/${q(id)}`, b),
  meetings: (from?: string, to?: string) =>
    call<{ items: MeetingSummary[] }>("GET", `/governance/meetings?from=${from ?? ""}&to=${to ?? ""}`),
  meeting: (id: string) => call<MeetingDetail>("GET", mp(id)),
  createMeeting: (m: MeetingInput) => call<MeetingDetail>("POST", "/governance/meetings", m),
  updateMeeting: (id: string, m: MeetingInput) => call<MeetingDetail>("PATCH", mp(id), m),
  meetingStatus: (id: string, status: MeetingStatus) => call<MeetingDetail>("POST", `${mp(id)}/status`, { status }),
  addItem: (id: string, element: string) => call<MeetingDetail>("POST", `${mp(id)}/items`, { element }),
  removeItem: (id: string, element: string) => call<MeetingDetail>("DELETE", `${mp(id)}/items/${q(element)}`),
  attendance: (id: string, attendance: { member_id: string; present: boolean }[]) =>
    call<MeetingDetail>("PUT", `${mp(id)}/attendance`, { attendance }),
  decide: (id: string, element: string, decision: string, note?: string, conditions?: string) =>
    call<MeetingDetail>("POST", `${mp(id)}/items/${q(element)}/decision`, { decision, note, conditions }),
  saveMinutes: (id: string, text: string) => call<MeetingDetail>("PUT", `${mp(id)}/minutes`, { text }),
  approveMinutes: (id: string) => call<MeetingDetail>("POST", `${mp(id)}/minutes/approve`),
};

// ---------------------------------------------------------------------
// Budget e costi (piano, passi 11 e 12): /finanza per chi ha finance.read, totali
// aggregati della richiesta per tutti quelli che la vedono.
// ---------------------------------------------------------------------

export type FinSettings = {
  base_currency: string;
  labor_category_id: string | null;
  portfolio_fiscal_year_id: string | null;
  portfolio_basis: "envelope" | "residual";
  alert_thresholds: number[];
};
export type FiscalYear = {
  id: string;
  name: string;
  start_date: string;
  end_date: string;
  status: "planning" | "open" | "closed";
  envelope_cents: number | null;
  allocated_cents: number;
  requested_cents: number;
  residual_cents: number | null;
  budgets: number;
};
export type CostCategory = {
  id: string;
  name: string;
  code: string | null;
  parent_id: string | null;
  nature: "capex" | "opex";
  sourcing: "internal" | "external";
  active: boolean;
};
export type FundingSource = { id: string; name: string; code: string | null; active: boolean };
export type FinOverview = {
  settings: FinSettings;
  fiscal_years: FiscalYear[];
  categories: CostCategory[];
  funding_sources: FundingSource[];
  import_fields: Record<string, string>;
  can_write: boolean;
};
export type BudgetLine = {
  id: string;
  cost_category_id: string;
  category: string;
  nature: string;
  org_unit_id: string | null;
  org_unit: string | null;
  funding_source_id: string | null;
  funding_source: string | null;
  period: string | null;
  amount_cents: number;
  effective_cents: number;
  pending_out_cents: number;
  note: string | null;
};
export type BudgetKind = "requested" | "revised" | "forecast";
export type Budget = {
  id: string;
  element_id: string;
  element_key: string;
  element_title: string;
  fiscal_year_id: string;
  fiscal_year: string;
  kind: BudgetKind;
  version: number;
  status: "draft" | "submitted" | "approved" | "superseded";
  note: string;
  decision_note: string | null;
  based_on_id: string | null;
  total_cents: number;
  effective_cents: number;
  approval_request_id: string | null;
  approval_status: string | null;
  approved_by: string | null;
  approved_at: string | null;
  submitted_at: string | null;
  created_at: string;
  lines?: BudgetLine[];
  can_edit: boolean;
  pending_transfers: boolean;
  /** Nodo dell'albero dei budget da cui attinge (null = nessuno). */
  allocation_id?: string | null;
};
export type LineInput = {
  cost_category_id: string;
  org_unit_id?: string | null;
  funding_source_id?: string | null;
  period?: string | null;
  amount_cents: number;
  note?: string | null;
};
export type Transfer = {
  id: string;
  from_line_id: string;
  to_line_id: string;
  from_element: string;
  to_element: string;
  from_category: string;
  to_category: string;
  fiscal_year: string;
  amount_cents: number;
  reason: string;
  status: "pending" | "approved" | "rejected";
  approval_request_id: string | null;
  created_by: string | null;
  created_at: string;
  decided_by: string | null;
  decided_at: string | null;
  decision_note: string | null;
};
export type CostEntry = {
  id: string;
  element_id: string;
  element_key: string;
  element_title: string;
  cost_category_id: string;
  category: string;
  nature: string;
  org_unit: string | null;
  source: string;
  source_ref: string | null;
  entry_date: string;
  kind: "actual" | "committed";
  amount_cents: number;
  currency: string;
  amount_base_cents: number;
  fx_rate: number;
  capitalizable: boolean;
  description: string;
  supplier: string | null;
  member: string | null;
  hours: number | null;
  hourly_cents: number | null;
  import_run_id: string | null;
  created_at: string;
};
export type CostPage = {
  items: CostEntry[];
  next?: string;
  total_base_cents: number;
  count: number;
  actual_base_cents: number;
  committed_base_cents: number;
};
export type CostFilter = { element?: string; category?: string; kind?: string; source?: string; from?: string; to?: string; q?: string };
export type CostInput = {
  element: string;
  cost_category_id: string;
  source?: string;
  source_ref?: string | null;
  entry_date: string;
  kind?: string;
  amount_cents: number;
  currency?: string;
  capitalizable?: boolean;
  description?: string;
  supplier?: string | null;
};
export type Rate = {
  id: string;
  member_id: string | null;
  member: string | null;
  role_key: string | null;
  hourly_cents: number;
  currency: string;
  valid_from: string;
  valid_to: string | null;
  note: string;
  uses: number;
};
export type RateInput = {
  member_id?: string | null;
  role_key?: string | null;
  hourly_cents: number;
  currency?: string;
  valid_from: string;
  valid_to?: string | null;
  note?: string;
};
export type WorkLog = {
  id: string;
  element_id: string;
  element_key: string;
  member_id: string;
  member: string;
  work_date: string;
  hours: number;
  role_key: string | null;
  capitalizable: boolean;
  text: string | null;
  cost_base_cents: number | null;
  rate_kind: "member" | "role" | null;
  hourly_cents: number | null;
  rate_currency: string | null;
  fx_rate: number | null;
};
export type WorkLogInput = {
  element: string;
  member_id?: string;
  work_date: string;
  hours: number;
  role_key?: string | null;
  capitalizable?: boolean;
  text?: string | null;
};
export type ExchangeRate = { currency: string; valid_on: string; rate_to_base: number };
export type ImportMapping = {
  columns: Record<string, string>;
  delimiter?: string;
  date_format?: string;
  decimal?: string;
  defaults?: Record<string, string>;
};
export type ImportTarget = "cost_entries" | "work_logs";
export type ImportProfile = { id: string; name: string; target: string; mapping: ImportMapping; updated_at: string };
export type ImportRowError = { row: number; field?: string; message: string };
export type ImportPreviewRow = {
  row: number;
  element_key: string;
  entry_date: string;
  amount_cents: number;
  currency: string;
  amount_base_cents: number;
  category: string;
  kind: string;
  description: string;
  source_ref: string;
  duplicate: boolean;
  // Solo per le ore (target work_logs).
  person?: string;
  minutes?: number;
  activity_type?: string;
};
export type ImportRun = {
  id: string;
  target: ImportTarget;
  profile_id: string | null;
  profile: string | null;
  file_name: string;
  status: "preview" | "done" | "rolled_back" | "failed";
  rows_total: number;
  rows_ok: number;
  rows_error: number;
  rows_duplicate: number;
  errors: ImportRowError[];
  preview: ImportPreviewRow[];
  dry_run: boolean;
  created_by: string | null;
  created_at: string;
  finished_at: string | null;
};
export type FinTotals = {
  currency: string;
  estimate_low_cents: number | null;
  estimate_high_cents: number | null;
  estimate_currency: string;
  approved_cents: number | null;
  pending_cents: number | null;
  actual_cents: number;
  committed_cents: number;
  capex_actual_cents: number;
  forecast_cents: number | null;
  forecast_source: "forecast" | "eac" | null;
  consumption_pct: number | null;
  alert_thresholds: number[];
  by_category: { cost_category_id: string; category: string; nature: string; budget_cents: number; actual_cents: number; committed_cents: number }[];
  by_month: { month: string; actual_cents: number; committed_cents: number }[];
  budgets: { id: string; fiscal_year: string; kind: BudgetKind; version: number; status: string; total_cents: number }[];
  ev_enabled: boolean;
  can_see_details: boolean;
  can_edit: boolean;
};
export type EVSnapshot = {
  period_end: string;
  bac_cents: number;
  pv_cents: number;
  ev_cents: number;
  ac_cents: number;
  percent_complete: number;
  cpi: number | null;
  spi: number | null;
  eac_cents: number | null;
  vac_cents: number | null;
  manual: boolean;
  computed_at: string;
};
export type EVProject = {
  element_id: string;
  key: string;
  title: string;
  ev_enabled: boolean;
  ev_method: "percent_complete" | "milestones" | "work_packages" | null;
  percent_complete: number;
  baseline_budget_id: string | null;
  bac_cents: number;
  plan_start: string | null;
  plan_end: string | null;
  latest: EVSnapshot | null;
  snapshots?: EVSnapshot[];
  curve?: { date: string; pv_cents: number }[];
  can_edit: boolean;
  message?: string;
};

const qs = (o: Record<string, string | undefined>) =>
  Object.entries(o)
    .filter(([, v]) => v)
    .map(([k, v]) => `${k}=${q(v!)}`)
    .join("&");

export const financeApi = {
  totals: (ref: string) => call<FinTotals>("GET", `/demand/requests/${q(ref)}/finance`),
  project: (ref: string) => call<EVProject>("GET", `/demand/requests/${q(ref)}/ev`),
  saveProject: (ref: string, p: { ev_enabled?: boolean; ev_method?: string; percent_complete?: number; baseline_budget_id?: string }) =>
    call<EVProject>("PUT", `/demand/requests/${q(ref)}/ev`, p),
  snapshot: (ref: string, as_of?: string, percent_complete?: number) =>
    call<EVProject>("POST", `/demand/requests/${q(ref)}/ev/snapshots`, { as_of, percent_complete }),
  projects: () => call<{ items: EVProject[] }>("GET", "/finance/projects"),

  overview: () => call<FinOverview>("GET", "/finance/overview"),
  saveSettings: (s: Partial<FinSettings> & { clear_portfolio?: boolean }) => call<FinSettings>("PUT", "/finance/settings", s),
  createFiscalYear: (f: { name: string; start_date: string; end_date: string; envelope_cents?: number | null }) =>
    call<FiscalYear>("POST", "/finance/fiscal-years", f),
  updateFiscalYear: (id: string, f: { name?: string; status?: string; envelope_cents?: number | null; clear_envelope?: boolean }) =>
    call<FiscalYear>("PATCH", `/finance/fiscal-years/${q(id)}`, f),
  createCategory: (c: { name: string; code?: string; nature: string; sourcing: string }) => call<CostCategory>("POST", "/finance/categories", c),
  updateCategory: (id: string, c: Partial<Omit<CostCategory, "id">>) => call<CostCategory>("PATCH", `/finance/categories/${q(id)}`, c),
  createFundingSource: (name: string, code?: string) => call<FundingSource>("POST", "/finance/funding-sources", { name, code }),
  exchangeRates: () => call<{ items: ExchangeRate[] }>("GET", "/finance/exchange-rates"),
  setExchangeRate: (x: ExchangeRate) => call<ExchangeRate>("PUT", "/finance/exchange-rates", x),

  budgets: (f: { fiscal_year?: string; element?: string; all?: boolean }) =>
    call<{ items: Budget[] }>("GET", `/finance/budgets?${qs({ fiscal_year: f.fiscal_year, element: f.element, all: f.all ? "true" : undefined })}`),
  budget: (id: string) => call<Budget>("GET", `/finance/budgets/${q(id)}`),
  createBudget: (b: { element: string; fiscal_year_id: string; kind: BudgetKind; note?: string; lines?: LineInput[]; allocation_id?: string | null }) =>
    call<Budget>("POST", "/finance/budgets", b),
  saveLines: (id: string, lines: LineInput[], note?: string) => call<Budget>("PUT", `/finance/budgets/${q(id)}/lines`, { lines, note }),
  submitBudget: (id: string) => call<Budget>("POST", `/finance/budgets/${q(id)}/submit`),
  deleteBudget: (id: string) => call<void>("DELETE", `/finance/budgets/${q(id)}`),
  transfers: (fiscal_year?: string) => call<{ items: Transfer[] }>("GET", `/finance/transfers?${qs({ fiscal_year })}`),
  createTransfer: (t: { from_line_id: string; to_line_id: string; amount_cents: number; reason: string }) =>
    call<Transfer>("POST", "/finance/transfers", t),

  costs: (f: CostFilter, cursor?: string) => call<CostPage>("GET", `/finance/costs?${qs({ ...f, cursor })}`),
  createCost: (c: CostInput) => call<CostEntry>("POST", "/finance/costs", c),
  deleteCost: (id: string) => call<void>("DELETE", `/finance/costs/${q(id)}`),
  workLogs: (element?: string) => call<{ items: WorkLog[] }>("GET", `/finance/work-logs?${qs({ element })}`),
  createWorkLog: (w: WorkLogInput) => call<WorkLog>("POST", "/finance/work-logs", w),
  deleteWorkLog: (id: string) => call<void>("DELETE", `/finance/work-logs/${q(id)}`),
  recompute: () => call<{ costed: number; without_rate: number }>("POST", "/finance/work-logs/recompute"),
  rates: () => call<{ items: Rate[] }>("GET", "/finance/rates"),
  createRate: (r: RateInput) => call<Rate>("POST", "/finance/rates", r),
  updateRate: (id: string, r: RateInput) => call<Rate>("PATCH", `/finance/rates/${q(id)}`, r),
  deleteRate: (id: string) => call<void>("DELETE", `/finance/rates/${q(id)}`),

  profiles: (target: ImportTarget = "cost_entries") =>
    call<{ items: ImportProfile[]; fields: Record<string, string> }>("GET", `/finance/import-profiles?target=${target}`),
  saveProfile: (id: string | null, name: string, mapping: ImportMapping, target: ImportTarget = "cost_entries") =>
    id
      ? call<ImportProfile>("PUT", `/finance/import-profiles/${q(id)}`, { name, mapping })
      : call<ImportProfile>("POST", "/finance/import-profiles", { name, mapping, target }),
  deleteProfile: (id: string) => call<void>("DELETE", `/finance/import-profiles/${q(id)}`),
  imports: (target: ImportTarget = "cost_entries") => call<{ items: ImportRun[] }>("GET", `/finance/imports?target=${target}`),
  importRun: (id: string) => call<ImportRun>("GET", `/finance/imports/${q(id)}`),
  previewImport: (i: { profile_id?: string; mapping?: ImportMapping; file_name: string; csv: string }) =>
    call<ImportRun>("POST", "/finance/imports", i),
  confirmImport: (id: string, csv: string, skip_errors: boolean) =>
    call<ImportRun>("POST", `/finance/imports/${q(id)}/confirm`, { csv, skip_errors }),
  rollbackImport: (id: string) => call<ImportRun>("POST", `/finance/imports/${q(id)}/rollback`),
};
// Valutazioni con questionari (piano, passo 4): team valutatori, tipi, modelli
// versionati, valutazioni di richieste e impatti, compilazione ed esame.
// ---------------------------------------------------------------------

export type ReviewOutcome = { key: string; label: string; polarity: "positive" | "conditional" | "negative" };
export type ReviewTeam = {
  id: string;
  key: string;
  name: string;
  description: string;
  group_id: string;
  group: string;
  members: number;
  lead_id: string | null;
  lead: string | null;
  sla_days: number;
  reminder_days: number;
  escalation_days: number | null;
  archived: boolean;
};
export type ReviewType = {
  id: string;
  key: string;
  name: string;
  description: string;
  team_id: string;
  team: string;
  scope: "request" | "impact";
  template_key: string;
  template: string | null;
  template_version: number | null;
  mandatory: boolean;
  outcomes: ReviewOutcome[];
  deciders: "team" | "lead";
  owner_roles: string[];
  archived: boolean;
};
export type TemplateVersion = {
  id: string;
  key: string;
  version: number;
  status: "draft" | "published" | "superseded";
  name: string;
  notes: string;
  created_at: string;
  published_at: string | null;
  published_by: string | null;
  reviews: number;
  definition?: QDefinition;
};
export type QTemplate = {
  key: string;
  name: string;
  current: TemplateVersion | null;
  draft: TemplateVersion | null;
  draft_problems?: Problem[];
  versions: TemplateVersion[];
  can_edit: boolean;
};
/** Regola di un processo che apre un tipo di valutazione («Quando si apre»). */
export type ReviewTrigger = {
  review_type: string;
  process_key: string;
  process_name: string;
  item_type: string;
  rule_key: string;
  rule_name: string;
  on: string;
  when?: string;
  action_when?: string;
};
export type ReviewConfig = {
  teams: ReviewTeam[];
  types: ReviewType[];
  triggers?: ReviewTrigger[];
  templates: QTemplate[];
  groups: { id: string; name: string; members: number }[];
  outcomes_default: ReviewOutcome[];
  can_edit: boolean;
};
export type ReviewSummary = {
  id: string;
  key: string;
  title: string;
  type_key: string;
  type_name: string;
  scope: "request" | "impact";
  mandatory: boolean;
  team: string;
  stage: string;
  state: string;
  state_category: string;
  request_key: string;
  request_title: string;
  impact_key: string | null;
  application: string | null;
  due_on: string | null;
  paused: boolean;
  late_days: number;
  outcome_label: string | null;
  outcome_polarity: ReviewOutcome["polarity"] | null;
  required_total: number;
  required_missing: number;
  superseded: boolean;
  created_at: string;
  role?: "compile" | "team";
};
export type ElementReviews = {
  items: ReviewSummary[];
  types: { key: string; name: string; scope: "request" | "impact"; team: string; mandatory: boolean }[];
  impacts: { id: string; key: string; application: string }[];
  facts: Record<string, unknown>;
};
export type ReviewPrescription = {
  id: string;
  source: "rule" | "manual";
  rule_key: string | null;
  text: string;
  category: string;
  status: "proposed" | "accepted" | "rejected";
  created_by: string | null;
  created_at: string;
  decided_by: string | null;
};
/** Requisito proposto da una regola del questionario; diventa un requisito alla chiusura della valutazione. */
export type ReviewRequirementProposal = {
  rule_key: string;
  title: string;
  text: string;
  category: string;
  method: "manual" | "evidence";
  check: string | null;
  /** Chiave del requisito creato (vuota finché la valutazione non si chiude). */
  requirement: string | null;
};
export type ReviewAnswer = { value: unknown; by: string | null; at: string; source: "manual" | "prefill" };
export type ReviewView = {
  review: {
    id: string;
    key: string;
    title: string;
    type_key: string;
    type_name: string;
    scope: "request" | "impact";
    mandatory: boolean;
    team_id: string;
    team: string;
    deciders: "team" | "lead";
    stage: string;
    request: { id: string; key: string; title: string };
    impact: { id: string; key: string; title: string } | null;
    application: string | null;
    submitted_at: string | null;
    due_on: string | null;
    paused: boolean;
    late_days: number;
    outcome: string | null;
    outcome_label: string | null;
    outcome_polarity: ReviewOutcome["polarity"] | null;
    decision_note: string;
    decided_at: string | null;
    decided_by: string | null;
    superseded_by: string | null;
    opened_by_rule: string | null;
    template_key: string;
    template_version: number;
    template_current_version: number | null;
  };
  element: Element;
  transitions: TransitionView[];
  phases: State[];
  process: ProcessRef | null;
  stakeholders: Stakeholder[];
  template: QDefinition;
  answers: Record<string, ReviewAnswer>;
  facts: Record<string, unknown[]>;
  evaluation: QEvaluation;
  assignments: { section: string; member_id: string; name: string }[];
  prescriptions: ReviewPrescription[];
  requirement_proposals: ReviewRequirementProposal[];
  outcomes: ReviewOutcome[];
  attachments: { id: string; name: string; size: number }[];
  can: {
    answer_all: boolean;
    sections: string[];
    assign: boolean;
    prescribe: boolean;
    decide: boolean;
    is_team: boolean;
    is_owner: boolean;
    read_only_reason?: string;
  };
};
export type AnswerHistory = { id: number; question: string; value: unknown; source: string; by: string | null; at: string };
export type ReviewTeamInput = Partial<{
  key: string;
  name: string;
  description: string;
  group_id: string;
  lead_id: string;
  sla_days: number;
  reminder_days: number;
  escalation_days: number;
  archived: boolean;
}>;
export type ReviewTypeInput = Partial<{
  key: string;
  name: string;
  description: string;
  team_id: string;
  scope: string;
  template_key: string;
  mandatory: boolean;
  outcomes: ReviewOutcome[];
  deciders: string;
  owner_roles: string[];
  archived: boolean;
}>;


/** Conteggio della inbox delle valutazioni per il menu; il resto dell API delle valutazioni è in reviewsApi.ts (caricato con le pagine). */
export const reviewInbox = (limit?: number) =>
  call<{ compile: ReviewSummary[]; team: ReviewSummary[]; more: boolean }>("GET", `/reviews/inbox${limit ? `?limit=${limit}` : ""}`);

// Requisiti (piano, passi 5 e 6): celle da verificare per il numero nel menu; il resto
// dell'API dei requisiti è in requirementsApi.ts (caricato con le pagine).
export type VerifyItem = {
  requirement_id: string;
  requirement_key: string;
  title: string;
  category: string;
  scope: "project" | "application";
  impact_key: string;
  application: string;
  team: string;
  request_key: string;
  request_title: string;
  implemented_by: string | null;
  since: string;
};
export const requirementInbox = (limit?: number) =>
  call<{ items: VerifyItem[]; more: boolean }>("GET", `/requirements/inbox${limit ? `?limit=${limit}` : ""}`);
// Linguaggio di query: autocompletamento e campi (piano, passo 16)
// ---------------------------------------------------------------------

export type QueryErrorInfo = { message: string; pos: number; end: number };
export type Suggestion = { kind: "field" | "value" | "keyword" | "operator" | "sort" | "direction"; label: string; insert: string; detail?: string };
export type Suggestions = {
  from: number;
  to: number;
  context: "field" | "value" | "sort" | "direction";
  field?: string;
  items: Suggestion[];
  error?: QueryErrorInfo;
};
export type QueryField = {
  key: string;
  name: string;
  label: string;
  kind: string;
  source: "system" | "module" | "custom";
  data_type?: string;
  sortable: boolean;
  multi?: boolean;
  options?: { id: string; name: string; color?: string }[];
  /** Tipi a cui il campo si applica (assente = tutti). */
  types?: string[];
};

export const queryApi = {
  suggest: (query: string, pos: number, signal?: AbortSignal) =>
    fetch(`/api/v1/query/suggest?q=${q(query)}&pos=${pos}`, { credentials: "same-origin", signal }).then(async (res) => {
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new ApiError(res.status, data.code ?? "unknown", data.detail ?? res.statusText);
      const out = data as Suggestions;
      return { ...out, items: out.items ?? [] }; // un server vecchio può mandare null
    }),
  fields: () => call<{ items: QueryField[] }>("GET", "/query/fields"),
};

// ---------------------------------------------------------------------
// Editor visuale dei processi (sdlc.md §10): metadati, catalogo, validazione dal vivo.
// ---------------------------------------------------------------------

export type FactInfo = {
  key: string;
  name: string;
  kind: "text" | "number" | "bool" | "date" | "presence";
  example?: string;
  values?: string[];
  source: "base" | "modulo" | "campo" | "ruolo" | "elemento" | "domanda";
  /** Fuori contesto (es. un ruolo che nessun elemento del tipo usa): si vede in modalità avanzata. */
  advanced?: boolean;
};
export type ActionField = { key: string; name: string; kind: string; required?: boolean; help?: string };
export type ActionSchema = { kind: string; name: string; description?: string; fields: ActionField[]; one_of?: string[] };
export type NamedKey = { key: string; name: string };
export type PolicyInfo = { name: string; stage: string; type?: string; outcomes: Record<string, string> };
export type ProcessMeta = {
  type: NamedKey;
  module: string;
  roles: NamedKey[];
  groups: NamedKey[];
  checks: string[];
  policies: PolicyInfo[];
  facts: FactInfo[];
  sample?: string;
  actions: ActionSchema[];
};
export type ProcessBundle = {
  format: string;
  key: string;
  name: string;
  description?: string;
  type: string;
  priority: number;
  applies_when?: string;
  notes?: string;
  definition: Definition;
};
export type CatalogType = { key: string; name: string; module: string; processes: number };
export type ProcessCatalog = { types: CatalogType[]; starters: ProcessBundle[] };
export type NewProcess = {
  key: string;
  name: string;
  type: string;
  description?: string;
  priority?: number;
  applies_when?: string;
  copy_from?: string;
};

export const processEditorApi = {
  meta: (ref: string) => call<ProcessMeta>("GET", `/processes/${q(ref)}/meta`),
  catalog: () => call<ProcessCatalog>("GET", "/process-catalog"),
  check: (ref: string, definition: Definition) => call<{ problems: Problem[] }>("POST", `/processes/${q(ref)}/check`, { definition }),
  create: (p: NewProcess) => call<ProcessInfo>("POST", "/processes", p),
};

// ---------------------------------------------------------------------
// Organizzazione e budget gerarchico (replica/budget.md; docs/budget-api.md). Solo tipi e chiamate:
// le pagine arrivano a parte. Gli importi sono centesimi nella valuta base.
// ---------------------------------------------------------------------
export type OrgRole = "manager" | "finance_ref" | "member";
export type OrgKind = "legal_entity" | "business_unit" | "department" | "cost_center";

export type OrgUnitNode = {
  id: string;
  name: string;
  parent_id: string | null;
  kind: OrgKind;
  code: string | null;
  manager_id: string | null;
  manager_name: string | null;
  archived: boolean;
  /** Profondità nell'albero (0 = in cima); l'elenco è già in ordine di visita. */
  depth: number;
  /** «Società › Area › Reparto». */
  path: string;
  members: number;
  children: number;
};
export type OrgUnitInput = {
  name?: string;
  parent_id?: string;
  kind?: OrgKind;
  /** Stringa vuota = toglie il codice. */
  code?: string;
  /** Stringa vuota = toglie il responsabile. */
  manager_id?: string;
  archived?: boolean;
};
export type OrgMember = { member_id: string; name: string; email: string; role: OrgRole };
export type OrgPerson = {
  member_id: string;
  name: string;
  email: string;
  units: { unit_id: string; name: string; path: string; role: OrgRole }[];
};
export type OrgImportRow = {
  line: number;
  action: "create" | "update" | "unchanged" | "error";
  code: string;
  name: string;
  parent: string;
  message?: string;
};
export type OrgImportReport = {
  sha256: string;
  rows: OrgImportRow[];
  create: number;
  update: number;
  unchanged: number;
  errors: number;
  applied: boolean;
};

export type BudgetGranularity = "year" | "semester" | "quarter" | "month";
export type EndPolicy = "return" | "carry" | "lapse";
export type BudgetSettings = {
  period_granularity: BudgetGranularity;
  end_policy: EndPolicy;
  /** null = nessuna soglia: approva sempre il responsabile del nodo comune. */
  transfer_threshold_cents: number | null;
  transfer_body_kind: "investment_committee" | "steering" | "architecture_board" | "cab" | "other";
  free_allocation: boolean;
  warn_days: number;
};
export type BudgetSettingsInput = Partial<Omit<BudgetSettings, "transfer_threshold_cents">> & {
  transfer_threshold_cents?: number;
  clear_threshold?: boolean;
};

export type PeriodStatus = "planning" | "open" | "closing" | "closed";
export type BudgetPeriod = {
  id: string;
  fiscal_year_id: string;
  fiscal_year: string;
  name: string;
  ordinal: number;
  start_date: string;
  end_date: string;
  status: PeriodStatus;
  closed_at: string | null;
  /** Giorni alla fine; null se il periodo è chiuso o già finito. */
  days_to_close: number | null;
  /** Id della radice (il plafond del periodo): solo la finanza la vede. */
  root_id: string | null;
};

export type AllocationNumbers = {
  allocated_cents: number;
  distributed_cents: number;
  reserve_cents: number;
  committed_cents: number;
  orders_cents: number;
  spent_cents: number;
  residual_cents: number;
  forecast_cents: number;
};
export type AllocationNode = AllocationNumbers & {
  id: string;
  period_id: string;
  parent_id: string | null;
  kind: "root" | "unit" | "element";
  org_unit_id: string | null;
  element_id: string | null;
  label: string;
  unit_code: string | null;
  manager_name: string | null;
  nature: "capex" | "opex" | null;
  funding_source_id: string | null;
  cost_category_id: string | null;
  end_policy: EndPolicy | null;
  note: string;
  archived: boolean;
  depth: number;
  children: number;
  can_allocate: boolean;
  can_edit: boolean;
  budgets?: NodeBudget[];
};
/** Budget di progetto che attinge da un nodo: `committed_cents` è il totale se approvato (non previsione), altrimenti 0. */
export type NodeBudget = {
  id: string;
  kind: BudgetKind;
  version: number;
  status: string;
  total_cents: number;
  element_key: string;
  element_title: string;
  committed_cents: number;
};
export type BudgetTree = { periods: BudgetPeriod[]; period_id?: string; items: AllocationNode[] };
export type AllocationNodeInput = {
  period_id: string;
  parent_id: string;
  /** Un'unità oppure un elemento. */
  org_unit_id?: string;
  element_id?: string;
  nature?: "capex" | "opex";
  funding_source_id?: string;
  cost_category_id?: string;
  amount_cents: number;
  note?: string;
  reason?: string;
};
export type AllocationMovement = {
  id: string;
  kind: "initial" | "allocate" | "transfer" | "return" | "carry" | "lapse" | "adjust";
  /** Con segno: effetto sullo stanziato del nodo. */
  amount_cents: number;
  counterpart_id: string | null;
  counterpart: string | null;
  batch_id: string;
  closure_id: string | null;
  transfer_id: string | null;
  approval_request_id: string | null;
  reason: string;
  created_by: string | null;
  created_at: string;
  /** Stanziato del nodo dopo il movimento. */
  balance_cents: number;
};

export type DistMode = "equal" | "percent" | "previous" | "requests";
export type DistTarget = {
  allocation_id?: string;
  org_unit_id?: string;
  element_id?: string;
  nature?: "capex" | "opex";
  funding_source_id?: string;
  percent?: number;
};
export type DistInput = { mode: DistMode; total_cents?: number; targets?: DistTarget[]; reason?: string };
export type DistLine = {
  allocation_id: string | null;
  org_unit_id: string | null;
  element_id: string | null;
  label: string;
  new: boolean;
  current_cents: number;
  proposed_cents: number;
  delta_cents: number;
  committed_cents: number;
  percent: number;
  weight_cents?: number;
  warnings: string[];
};
export type DistPreview = {
  node_id: string;
  mode: DistMode;
  total_cents: number;
  lines: DistLine[];
  allocated_cents: number;
  distributed_after: number;
  reserve_after: number;
  exceeds_cents: number;
  warnings: string[];
  valid: boolean;
};

export type ClosureLine = {
  allocation_id: string;
  label: string;
  depth: number;
  kind: "root" | "unit" | "element";
  policy: EndPolicy;
  policy_source: "tenant" | "node";
  allocated_cents: number;
  committed_cents: number;
  remainder_cents: number;
  return_cents: number;
  carry_cents: number;
  lapse_cents: number;
  warnings: string[];
};
export type ClosurePreview = {
  period_id: string;
  next_period_id: string | null;
  next_period: string | null;
  default_policy: EndPolicy;
  lines: ClosureLine[];
  return_cents: number;
  carry_cents: number;
  lapse_cents: number;
  open_commitments: number;
  warnings: string[];
  /** C'è un riporto: la conferma richiede approve_carry. */
  needs_approval: boolean;
  can_confirm: boolean;
};
export type PeriodClosure = {
  id: string;
  period_id: string;
  next_period_id: string | null;
  status: "confirmed" | "undone";
  return_cents: number;
  carry_cents: number;
  lapse_cents: number;
  created_at: string;
  undone_at: string | null;
};

export type AllocationTransfer = {
  id: string;
  period_id: string;
  from_allocation_id: string;
  to_allocation_id: string;
  from: string;
  to: string;
  amount_cents: number;
  reason: string;
  status: "pending" | "approved" | "rejected";
  approval_kind: "none" | "manager" | "body";
  approver_member_id: string | null;
  approval_request_id: string | null;
  created_by: string | null;
  created_at: string;
  decided_at: string | null;
  decision_note: string | null;
};
export type ElementAllocation = {
  id: string;
  period_id: string;
  period: string;
  allocated_cents: number;
  fiscal_year: string;
  fiscal_year_id: string;
  period_status: PeriodStatus;
  start_date: string;
  end_date: string;
  /** Nodi sopra, dalla radice, tra quelli che chi chiama vede. */
  path: string[];
  others_cents: number;
  /** Stanziato meno ciò che impegnano gli altri budget approvati (può essere negativo). */
  available_cents: number;
  /** Totale del budget indicato nel periodo del nodo (0 senza budget). */
  mine_cents: number;
  linked: boolean;
};
export type ElementAllocations = { items: ElementAllocation[]; linked_id: string | null; linked_hidden: boolean };

export const orgApi = {
  units: (f: { q?: string; limit?: number; archived?: boolean } = {}) =>
    call<{ items: OrgUnitNode[] }>("GET", `/org/units?${qs({ q: f.q, limit: f.limit ? String(f.limit) : undefined, archived: f.archived ? "1" : undefined })}`),
  createUnit: (u: OrgUnitInput) => call<OrgUnitNode>("POST", "/org/units", u),
  updateUnit: (id: string, u: OrgUnitInput) => call<OrgUnitNode>("PATCH", `/org/units/${q(id)}`, u),
  /** parent_id vuoto = in cima. */
  moveUnit: (id: string, parent_id: string) => call<OrgUnitNode>("POST", `/org/units/${q(id)}/move`, { parent_id }),
  members: (id: string) => call<{ items: OrgMember[] }>("GET", `/org/units/${q(id)}/members`),
  setMember: (unit: string, member: string, role: OrgRole) =>
    call<{ items: OrgMember[] }>("PUT", `/org/units/${q(unit)}/members/${q(member)}`, { role }),
  removeMember: (unit: string, member: string) => call<void>("DELETE", `/org/units/${q(unit)}/members/${q(member)}`),
  people: (f: { q?: string; limit?: number } = {}) =>
    call<{ items: OrgPerson[] }>("GET", `/org/people?${qs({ q: f.q, limit: f.limit ? String(f.limit) : undefined })}`),
  importPreview: (csv: string, delimiter?: string) => call<OrgImportReport>("POST", "/org/import/preview", { csv, delimiter }),
  importConfirm: (csv: string, sha256: string, delimiter?: string) =>
    call<OrgImportReport>("POST", "/org/import/confirm", { csv, sha256, delimiter }),
};

export const budgetApi = {
  settings: () => call<BudgetSettings>("GET", "/budget/settings"),
  saveSettings: (s: BudgetSettingsInput) => call<BudgetSettings>("PUT", "/budget/settings", s),
  periods: (fiscal_year_id?: string) => call<{ items: BudgetPeriod[] }>("GET", `/budget/periods?${qs({ fiscal_year_id })}`),
  generatePeriods: (fiscal_year_id: string, granularity?: BudgetGranularity) =>
    call<{ items: BudgetPeriod[] }>("POST", `/budget/fiscal-years/${q(fiscal_year_id)}/periods`, granularity ? { granularity } : undefined),
  openPeriod: (id: string) => call<BudgetPeriod>("POST", `/budget/periods/${q(id)}/open`),
  setRoot: (period_id: string, amount_cents: number, reason?: string) =>
    call<AllocationNode>("POST", `/budget/periods/${q(period_id)}/root`, { amount_cents, reason }),
  closePreview: (period_id: string) => call<ClosurePreview>("GET", `/budget/periods/${q(period_id)}/close-preview`),
  startClosing: (period_id: string) => call<BudgetPeriod>("POST", `/budget/periods/${q(period_id)}/start-closing`),
  closePeriod: (period_id: string, approve_carry = false) =>
    call<PeriodClosure>("POST", `/budget/periods/${q(period_id)}/close`, { approve_carry }),
  undoClose: (period_id: string) => call<BudgetPeriod>("POST", `/budget/periods/${q(period_id)}/undo-close`),
  closures: (period_id: string) => call<{ items: PeriodClosure[] }>("GET", `/budget/periods/${q(period_id)}/closures`),

  tree: (period_id?: string) => call<BudgetTree>("GET", `/budget/tree?${qs({ period_id })}`),
  node: (id: string) => call<AllocationNode>("GET", `/budget/allocations/${q(id)}`),
  createNode: (n: AllocationNodeInput) => call<AllocationNode>("POST", "/budget/allocations", n),
  patchNode: (id: string, p: { end_policy?: EndPolicy | ""; note?: string; archived?: boolean; cost_category_id?: string }) =>
    call<AllocationNode>("PATCH", `/budget/allocations/${q(id)}`, p),
  /** Modifica in linea: restituisce il nodo e il padre con la nuova riserva (il padre può mancare se non è visibile). */
  setAmount: (id: string, amount_cents: number, reason?: string) =>
    call<{ node: AllocationNode; parent: AllocationNode | null }>("PUT", `/budget/allocations/${q(id)}/amount`, { amount_cents, reason }),
  movements: (id: string) => call<{ items: AllocationMovement[] }>("GET", `/budget/allocations/${q(id)}/movements`),
  distributePreview: (id: string, d: DistInput) => call<DistPreview>("POST", `/budget/allocations/${q(id)}/distribute/preview`, d),
  distribute: (id: string, d: DistInput) => call<DistPreview>("POST", `/budget/allocations/${q(id)}/distribute`, d),

  transfers: (period_id?: string) => call<{ items: AllocationTransfer[] }>("GET", `/budget/transfers?${qs({ period_id })}`),
  requestTransfer: (t: { from_allocation_id: string; to_allocation_id: string; amount_cents: number; reason: string }) =>
    call<AllocationTransfer>("POST", "/budget/transfers", t),
  /** Nodi compatibili: per un elemento (id o chiave) e un esercizio, oppure per un budget (`budget_id`). */
  elementAllocations: (f: { element_id?: string; fiscal_year_id?: string; budget_id?: string }) =>
    call<ElementAllocations>("GET", `/budget/element-allocations?${qs(f)}`),
  /** Collega (o scollega con null) un budget di progetto a uno stanziamento: serve finance.write. */
  linkBudget: (budget_id: string, allocation_id: string | null) =>
    call<Budget & { allocation_id: string | null }>("PUT", `/finance/budgets/${q(budget_id)}/allocation`, { allocation_id }),
};

/** Risposta di GET /finance/overview con le aggiunte del budget gerarchico. */
export type FinOverviewBudget = FinOverview & { budget_settings: BudgetSettings; periods: BudgetPeriod[] };
/** Budget di progetto con il collegamento allo stanziamento (campo presente in tutte le risposte dei budget). */
export type BudgetLinked = Budget & { allocation_id: string | null };
