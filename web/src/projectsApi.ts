// Progetti (replica/progetti.md, passi 1 e 2): client delle API /projects, /plan-items e
// /plan-phases. Il progetto è un elemento di tipo «project» dello spazio PRJ; il piano ha fasi,
// work package (agile, a cascata o fornitore), attività dei work package a cascata, milestone e
// dipendenze fs/ss/ff con ritardo in giorni lavorativi. I numeri (avanzamento pesato, ritardi,
// dipendenze bloccate, budget) li calcola il server con una sola funzione: qui si mostrano.
import { call, type Element } from "./api";

const q = encodeURIComponent;

export type Template = "agile" | "waterfall" | "mixed" | "contract";
export type DeliveryMode = "agile" | "waterfall" | "supplier";
export type ItemKind = "work_package" | "plan_activity" | "milestone";
export type DepKind = "fs" | "ss" | "ff";
export type Signal = "green" | "amber" | "red" | "none";
export type GateStatus = "open" | "pending" | "approved" | "rejected";

export type Ref = { id: string; key: string; title: string };
export type RequestRef = Ref & { state: string };

export type ProjectHead = {
  id: string;
  key: string;
  title: string;
  state: string;
  state_category: string;
  template: Template;
  start: string | null;
  end: string | null;
  requests: RequestRef[];
  /** Scadenza normativa (o data desiderata) più vicina delle richieste. */
  target: string | null;
  target_kind: "regulatory" | "desired" | "";
  program: Ref | null;
};

export type Phase = {
  id: string;
  name: string;
  ordinal: number;
  gate_enabled: boolean;
  gate_status: GateStatus;
  gate_request_id: string | null;
  start: string | null;
  end: string | null;
  progress: number;
};

export type Dep = { id: string; kind: DepKind; lag: number };

export type PlanItem = {
  id: string;
  key: string;
  title: string;
  kind: ItemKind;
  phase_id: string | null;
  /** Work package dell'attività (o della milestone legata a un work package). */
  parent_id: string | null;
  state: string;
  state_category: string;
  start: string | null;
  end: string | null;
  delivery_mode: DeliveryMode | null;
  team_id: string | null;
  team: string | null;
  application_id: string | null;
  application: string | null;
  supplier_id: string | null;
  supplier: string | null;
  assignee_id: string | null;
  assignee: string | null;
  estimate_effort_days: number | null;
  weight: number | null;
  contractual: boolean;
  /** Impatto della richiesta da cui nasce il work package. */
  impact: Ref | null;
  /** Epic dei team (work package agile). */
  epics: Ref[];
  deps: Dep[];
  /** 0..100. */
  progress: number;
  progress_source: "epics" | "activities" | "declared" | "state" | "none";
  team_capacity: number;
};

export type ProjectPlan = {
  project: ProjectHead;
  phases: Phase[];
  items: PlanItem[];
  can_edit: boolean;
  progress_method: "weighted_estimate" | "equal";
};

export type ItemRef = {
  id: string;
  key: string;
  title: string;
  kind: ItemKind | "risk";
  date: string | null;
  days_late: number;
  team: string | null;
  progress: number;
  state: string;
};

export type Blocked = { item: ItemRef; waits_on: ItemRef; reason: "waiting" | "violated"; dep_kind: DepKind };

export type Budget = {
  currency: string;
  approved_cents: number | null;
  spent_cents: number;
  committed_cents: number;
  forecast_cents: number | null;
};

/** Codici del semaforo: milestone_late, past_target (rosso); work_package_late, blocked, missing_roles (giallo). */
export type Reason = "milestone_late" | "past_target" | "work_package_late" | "blocked" | "missing_roles";

export type Numbers = {
  progress: number;
  start: string | null;
  end: string | null;
  /** Baseline: arriva con il passo 3 (sempre null per ora). */
  baseline_start: string | null;
  baseline_end: string | null;
  actual_start: string | null;
  actual_end: string | null;
  target: string | null;
  target_kind: "regulatory" | "desired" | "";
  /** Giorni lavorativi di margine sulla scadenza (negativo = oltre). */
  margin_days: number | null;
  work_packages: number;
  work_packages_done: number;
  milestones: number;
  milestones_done: number;
  by_mode: { mode: DeliveryMode; count: number; progress: number }[];
  next_milestone: ItemRef | null;
  late: ItemRef[];
  blocked: Blocked[];
  /** Change request aperte (passo 3: per ora sempre 0). */
  open_change_requests: number;
  risks: ItemRef[];
  missing_roles: { key: string; name: string }[];
  budget: Budget | null;
  signal: Signal;
  reasons: Reason[];
};

export type Overview = { numbers: Numbers; project: ProjectHead; phases: Phase[]; can_edit: boolean };

export type ProjectSummary = {
  id: string;
  key: string;
  title: string;
  state: string;
  state_category: string;
  template: Template;
  project_manager: string | null;
  requests: number;
  progress: number;
  signal: Signal;
  start: string | null;
  end: string | null;
  work_packages: number;
  next_milestone: ItemRef | null;
  late: number;
};

export type PlanItemView = PlanItem & {
  project: Ref;
  phase: string | null;
  can_edit: boolean;
  /** Il responsabile della voce aggiorna avanzamento e date del suo perimetro. */
  can_update: boolean;
};

export type Change = { id: string; key: string; start: string; end: string; cause: "moved" | "cascade" | "auto" };

export type ProgressEntry = { id: string; date: string; percent: number; note: string; member: string | null; created_at: string };

export type ProjectRole = { key: string; name: string; required: boolean; inherit_from_request: boolean };

export type ProjectSettings = {
  default_template: Template;
  auto_from_request: boolean;
  programs_enabled: boolean;
  progress_method: "weighted_estimate" | "equal";
  roles: ProjectRole[];
  available_roles: { key: string; name: string }[];
  can_edit: boolean;
};

export type NewProject = { title: string; body_text?: string; template?: Template; start?: string; end?: string; program_id?: string; no_phases?: boolean };

export type DepInput = { ref: string; kind?: DepKind; lag?: number };

export type NewItem = {
  kind: ItemKind;
  title: string;
  body_text?: string;
  phase_id?: string;
  /** Work package dell'attività (chiave o id). */
  parent_id?: string;
  delivery_mode?: DeliveryMode;
  team_id?: string;
  application_id?: string;
  supplier_id?: string;
  assignee_id?: string;
  estimate_effort_days?: number;
  weight?: number;
  contractual?: boolean;
  start?: string;
  end?: string;
  /** Milestone. */
  date?: string;
  depends_on?: DepInput[];
};

/** "" toglie team, applicazione, fornitore, fase o assegnatario; weight 0 torna alla stima. */
export type ItemPatch = Partial<{
  title: string;
  phase_id: string;
  delivery_mode: DeliveryMode;
  team_id: string;
  application_id: string;
  supplier_id: string;
  assignee_id: string;
  estimate_effort_days: number;
  weight: number;
  contractual: boolean;
}>;

export const projectsApi = {
  list: (closed = false) => call<{ items: ProjectSummary[] }>("GET", `/projects${closed ? "?closed=true" : ""}`),
  create: (p: NewProject) => call<Element>("POST", "/projects", p),
  plan: (ref: string) => call<ProjectPlan>("GET", `/projects/${q(ref)}/plan`),
  overview: (ref: string) => call<Overview>("GET", `/projects/${q(ref)}/overview`),
  autoPlan: (ref: string, replan = false, from?: string) => call<{ changes: Change[] }>("POST", `/projects/${q(ref)}/plan/auto`, { replan, ...(from ? { from } : {}) }),
  createItem: (ref: string, it: NewItem) => call<PlanItemView>("POST", `/projects/${q(ref)}/items`, it),
  createPhase: (ref: string, p: { name: string; gate_enabled?: boolean; ordinal?: number }) => call<Phase>("POST", `/projects/${q(ref)}/phases`, p),
  attachRequest: (ref: string, request: string) => call<Element>("POST", `/projects/${q(ref)}/requests`, { request }),

  item: (id: string) => call<PlanItemView>("GET", `/plan-items/${q(id)}`),
  updateItem: (id: string, p: ItemPatch) => call<PlanItemView>("PATCH", `/plan-items/${q(id)}`, p),
  deleteItem: (id: string) => call<void>("DELETE", `/plan-items/${q(id)}`),
  schedule: (id: string, start: string, end: string, cascade = true) => call<{ changes: Change[] }>("PUT", `/plan-items/${q(id)}/schedule`, { start, end, cascade }),
  setDeps: (id: string, deps: DepInput[]) => call<PlanItemView>("PUT", `/plan-items/${q(id)}/dependencies`, { depends_on: deps }),
  progress: (id: string) => call<{ items: ProgressEntry[] }>("GET", `/plan-items/${q(id)}/progress`),
  recordProgress: (id: string, percent: number, note = "", date?: string) =>
    call<PlanItemView>("POST", `/plan-items/${q(id)}/progress`, { percent, note, ...(date ? { date } : {}) }),
  /** Crea (se manca) l'epic del work package agile sulla board del team. */
  createEpic: (id: string) => call<Element>("POST", `/plan-items/${q(id)}/epic`),

  updatePhase: (id: string, p: { name?: string; gate_enabled?: boolean; ordinal?: number }) => call<Phase>("PATCH", `/plan-phases/${q(id)}`, p),
  deletePhase: (id: string) => call<void>("DELETE", `/plan-phases/${q(id)}`),
  /** Chiede l'approvazione del cancello della fase (motore delle approvazioni). */
  requestGate: (id: string) => call<Phase>("POST", `/plan-phases/${q(id)}/gate`),

  settings: () => call<ProjectSettings>("GET", "/projects/settings"),
  saveSettings: (s: Partial<Omit<ProjectSettings, "available_roles" | "can_edit">>) => call<ProjectSettings>("PUT", "/projects/settings", s),

  /** Il progetto della richiesta (null se non c'è) e se la richiesta è approvata. */
  ofRequest: (ref: string) => call<{ project: Ref | null; approved: boolean }>("GET", `/demand/requests/${q(ref)}/project`),
  /** «Crea il progetto»: la richiesta approvata diventa il progetto (o restituisce quello che ha già). */
  fromRequest: (ref: string) => call<{ project: Element; created: boolean }>("POST", `/demand/requests/${q(ref)}/project`),
};

/** Chiavi di React Query condivise dalle schede del progetto. */
export const projectKeys = {
  list: ["projects"] as const,
  plan: (ref: string) => ["project-plan", ref] as const,
  overview: (ref: string) => ["project-overview", ref] as const,
  item: (id: string) => ["plan-item", id] as const,
  ofRequest: (ref: string) => ["request-project", ref] as const,
};
