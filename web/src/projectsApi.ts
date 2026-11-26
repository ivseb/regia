// Progetti (replica/progetti.md, passi 1-4): client delle API /projects, /plan-items,
// /plan-phases, /plan-baselines, /change-requests, /progress-claims, /deliverables e /programs. Il progetto è un elemento di tipo «project» dello spazio PRJ; il piano ha fasi,
// work package (agile, a cascata o fornitore), attività dei work package a cascata, milestone e
// dipendenze fs/ss/ff con ritardo in giorni lavorativi. I numeri (avanzamento pesato, ritardi,
// dipendenze bloccate, budget, scostamenti dalla baseline) li calcola il server con una sola
// funzione: qui si mostrano. La baseline è immutabile: la prima la fissa il PM, le altre nascono
// da una change request approvata.
import { call, type Element } from "./api";
import { tr } from "./i18n/core";

const q = encodeURIComponent;

export type Template = "agile" | "waterfall" | "mixed" | "contract";
export type DeliveryMode = "agile" | "waterfall" | "supplier";
export type ItemKind = "work_package" | "plan_activity" | "milestone";
export type DepKind = "fs" | "ss" | "ff";
export type Signal = "green" | "amber" | "red" | "none";
export type GateStatus = "open" | "pending" | "approved" | "rejected";

export type Ref = { id: string; key: string; title: string };
export type RequestRef = Ref & { state: string; restricted?: boolean };

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
  progress_source: "epics" | "activities" | "claims" | "deliverables" | "declared" | "state" | "none";
  team_capacity: number;
  /** Baseline in vigore: la voce c'era? Date e scostamento della fine (giorni lavorativi, + = più tardi). */
  in_baseline: boolean;
  baseline_start: string | null;
  baseline_end: string | null;
  variance_days: number | null;
  /** Fornitore (passo 4): il contatto del fornitore dichiara i SAL; SAL da accettare; deliverable non annullati e accettati. */
  supplier_updates: boolean;
  claims_pending: number;
  deliverables: number;
  deliverables_accepted: number;
  /** Work package: altri progetti sulla stessa applicazione nello stesso periodo. */
  conflicts?: Conflict[];
  /** L'attore non può leggere la voce: restano date, avanzamento e dipendenze. */
  restricted?: boolean;
};

/** Un altro work package che tocca la stessa applicazione nello stesso periodo. project e
 * work_package sono null se l'attore non li legge (restricted: «un altro progetto»). */
export type Conflict = {
  item_id: string;
  application_id: string;
  application: string;
  project: Ref | null;
  work_package: Ref | null;
  start: string | null;
  end: string | null;
  restricted?: boolean;
};

export type BaselineHead = { id: string; number: number; start: string | null; end: string | null; created_at: string; created_by: string | null };
export type Tolerance = { days: number; budget_pct: number };

export type ProjectPlan = {
  project: ProjectHead;
  phases: Phase[];
  items: PlanItem[];
  can_edit: boolean;
  progress_method: "weighted_estimate" | "equal";
  /** Baseline in vigore (null se non è ancora fissata) e tolleranze del progetto. */
  baseline: BaselineHead | null;
  tolerance: Tolerance;
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
  restricted?: boolean;
};

export type Blocked = { item: ItemRef; waits_on: ItemRef; reason: "waiting" | "violated"; dep_kind: DepKind };

export type Budget = {
  currency: string;
  approved_cents: number | null;
  spent_cents: number;
  committed_cents: number;
  forecast_cents: number | null;
};

/** Codici del semaforo: milestone_late, past_target, over_tolerance, budget_over_tolerance (rosso);
 * work_package_late, blocked, conflicts, missing_roles (giallo). */
export type Reason =
  | "milestone_late"
  | "past_target"
  | "over_tolerance"
  | "budget_over_tolerance"
  | "work_package_late"
  | "blocked"
  | "conflicts"
  | "missing_roles";

export type Numbers = {
  progress: number;
  start: string | null;
  end: string | null;
  baseline_start: string | null;
  baseline_end: string | null;
  /** Numero della baseline in vigore; scostamento della fine in giorni lavorativi e in % della durata. */
  baseline: number | null;
  variance_days: number | null;
  variance_pct: number | null;
  tolerance: Tolerance;
  /** Milestone oltre la tolleranza (days_late è lo scostamento dalla baseline). */
  over_tolerance: ItemRef[];
  end_over_tolerance: boolean;
  /** Costo previsto rispetto alla baseline (%), solo a chi vede il budget. */
  budget_variance_pct: number | null;
  budget_over_tolerance: boolean;
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
  /** SAL dichiarati dai fornitori e da accettare; conflitti con altri progetti sulle stesse applicazioni. */
  claims_pending: number;
  conflicts: Conflict[];
  /** Change request da inviare o in approvazione. */
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
  /** Oltre queste tolleranze una modifica del piano apre una change request. */
  tolerance_days: number;
  tolerance_budget_pct: number;
  roles: ProjectRole[];
  available_roles: { key: string; name: string }[];
  can_edit: boolean;
};

export type BaselineItem = {
  id: string;
  kind: ItemKind;
  key: string;
  title: string;
  phase_id: string | null;
  phase: string | null;
  start: string | null;
  end: string | null;
  estimate_effort_days: number | null;
  restricted?: boolean;
};

export type Baseline = BaselineHead & {
  note: string;
  change_request: Ref | null;
  budget: Budget | null;
  items?: BaselineItem[];
  current: boolean;
};

export type CRStatus = "draft" | "pending" | "approved" | "rejected" | "withdrawn";

export type Deviation = {
  id: string;
  kind: "milestone" | "project";
  key: string;
  title: string;
  baseline_end: string | null;
  end: string | null;
  days: number;
  restricted?: boolean;
};

export type ChangeRequest = {
  id: string;
  key: string;
  title: string;
  state: string;
  state_category: string;
  status: CRStatus;
  origin: "tolerance" | "manual";
  reason: string;
  baseline: number | null;
  new_baseline: number | null;
  new_baseline_id: string | null;
  approval_request_id: string | null;
  /** Perché l'approvazione non è partita: codice di errore (errors.<code>) e parametri. */
  blocked: { code: string; params?: Record<string, unknown> } | null;
  deviations: Deviation[];
  budget_pct: number | null;
  tolerance: Tolerance;
  created_at: string;
  created_by: string | null;
  decided_at: string | null;
  can_submit: boolean;
};

// ---- Fornitori (passo 4): contratto, SAL e deliverable del work package in modo «fornitore».

export type ClaimStatus = "submitted" | "accepted" | "rejected" | "withdrawn";

/** SAL: percentuale complessiva raggiunta e/o importo del periodo. value è l'avanzamento che porta. */
export type Claim = {
  id: string;
  key: string;
  number: number;
  period_end: string | null;
  percent: number | null;
  /** null per chi non vede gli importi. */
  amount_cents: number | null;
  status: ClaimStatus;
  state: string;
  note: string;
  submitted_by: string | null;
  submitted_at: string;
  decided_by: string | null;
  decided_at: string | null;
  decision_note: string;
  value: number | null;
};

export type DeliverableStatus = "pending" | "delivered" | "accepted" | "rejected" | "cancelled";

export type Deliverable = {
  id: string;
  key: string;
  title: string;
  status: DeliverableStatus;
  state: string;
  due: string | null;
  milestone_id: string | null;
  milestone: string | null;
  delivered_by: string | null;
  delivered_at: string | null;
  delivery_note: string;
  decided_by: string | null;
  decided_at: string | null;
  decision_note: string;
  restricted?: boolean;
};

export type SupplierView = {
  item_id: string;
  delivery_mode: DeliveryMode;
  supplier_id: string | null;
  supplier: string | null;
  /** Il contatto del fornitore dichiara i SAL e consegna i deliverable (altrimenti solo il PM). */
  supplier_updates: boolean;
  contacts: { id: string; name: string }[];
  /** amount_cents è null per chi non vede gli importi. */
  contract: { ref: string | null; amount_cents: number | null; currency: string };
  claims: Claim[];
  deliverables: Deliverable[];
  progress: number;
  progress_source: PlanItem["progress_source"];
  /** Somma degli importi dei SAL accettati (null per chi non vede gli importi). */
  accepted_cents: number | null;
  amounts_visible: boolean;
  /** Contratto, contatto, accettazione, nuovi deliverable. */
  can_edit: boolean;
  /** Dichiarare un SAL, consegnare un deliverable. */
  can_declare: boolean;
};

/** contact_id "" toglie il contatto; clear_amount toglie l'importo del contratto. */
export type SupplierPatch = Partial<{
  supplier_updates: boolean;
  contract_ref: string;
  contract_amount_cents: number;
  clear_amount: boolean;
  contract_currency: string;
  contact_id: string;
}>;

export type NewClaim = { percent?: number; amount_cents?: number; period_end?: string; note?: string; accept?: boolean };
export type Decision = { outcome: "accepted" | "rejected"; note?: string };

// ---- Programmi (passo 4): progetti con un obiettivo comune, numeri aggregati e roadmap.

export type RoadmapMilestone = {
  id: string;
  key: string;
  title: string;
  date: string | null;
  baseline_date: string | null;
  state_category: string;
  contractual: boolean;
  days_late: number;
  restricted?: boolean;
};

export type ProgramProject = {
  id: string;
  key: string;
  title: string;
  state: string;
  state_category: string;
  project_manager: string | null;
  progress: number;
  signal: Signal;
  reasons: Reason[];
  start: string | null;
  end: string | null;
  baseline_start: string | null;
  baseline_end: string | null;
  variance_days: number | null;
  work_packages: number;
  late: number;
  conflicts: number;
  claims_pending: number;
  milestones: RoadmapMilestone[];
};

/** Pagina del programma: numbers sono quelli di computeNumbers su tutte le voci dei progetti. */
export type ProgramView = { program: Ref; state: string; numbers: Numbers; projects: ProgramProject[]; can_edit: boolean };

export type ProgramSummary = {
  id: string;
  key: string;
  title: string;
  state: string;
  state_category: string;
  projects: number;
  progress: number;
  signal: Signal;
  start: string | null;
  end: string | null;
  next_milestone: ItemRef | null;
  late: number;
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

// Le voci che l'attore non può leggere arrivano senza titolo: qui prendono un'etichetta.
function veil<T extends { title: string; restricted?: boolean }>(x: T): T {
  return x.restricted ? { ...x, title: tr("progetti.restricted") } : x;
}
const veilHead = (h: ProjectHead): ProjectHead => ({ ...h, requests: h.requests.map(veil) });
const veilCR = (c: ChangeRequest): ChangeRequest => ({ ...c, deviations: c.deviations.map(veil) });
const veilConflict = (c: Conflict): Conflict => (c.restricted ? { ...c, project: { id: "", key: "", title: tr("progetti.restricted") } } : c);
const veilItem = (it: PlanItem): PlanItem => ({ ...veil(it), ...(it.conflicts ? { conflicts: it.conflicts.map(veilConflict) } : {}) });
const veilSupplier = (v: SupplierView): SupplierView => ({ ...v, deliverables: v.deliverables.map(veil) });
const veilNumbers = (n: Numbers): Numbers => ({
  ...n,
  late: n.late.map(veil),
  blocked: n.blocked.map((b) => ({ ...b, item: veil(b.item), waits_on: veil(b.waits_on) })),
  next_milestone: n.next_milestone && veil(n.next_milestone),
  over_tolerance: n.over_tolerance.map(veil),
  conflicts: (n.conflicts ?? []).map(veilConflict),
});

export const projectsApi = {
  list: (closed = false) =>
    call<{ items: ProjectSummary[] }>("GET", `/projects${closed ? "?closed=true" : ""}`).then((r) => ({
      items: r.items.map((p) => ({ ...p, next_milestone: p.next_milestone && veil(p.next_milestone) })),
    })),
  create: (p: NewProject) => call<Element>("POST", "/projects", p),
  plan: (ref: string) =>
    call<ProjectPlan>("GET", `/projects/${q(ref)}/plan`).then((p) => ({ ...p, project: veilHead(p.project), items: p.items.map(veilItem) })),
  overview: (ref: string) =>
    call<Overview>("GET", `/projects/${q(ref)}/overview`).then((o) => ({ ...o, project: veilHead(o.project), numbers: veilNumbers(o.numbers) })),
  autoPlan: (ref: string, replan = false, from?: string) => call<{ changes: Change[] }>("POST", `/projects/${q(ref)}/plan/auto`, { replan, ...(from ? { from } : {}) }),
  createItem: (ref: string, it: NewItem) => call<PlanItemView>("POST", `/projects/${q(ref)}/items`, it),
  createPhase: (ref: string, p: { name: string; gate_enabled?: boolean; ordinal?: number }) => call<Phase>("POST", `/projects/${q(ref)}/phases`, p),
  attachRequest: (ref: string, request: string) => call<Element>("POST", `/projects/${q(ref)}/requests`, { request }),

  item: (id: string) => call<PlanItemView>("GET", `/plan-items/${q(id)}`).then((it) => ({ ...it, ...veilItem(it) })),
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

  baselines: (ref: string) => call<{ items: Baseline[] }>("GET", `/projects/${q(ref)}/baselines`),
  baseline: (id: string) => call<Baseline>("GET", `/plan-baselines/${q(id)}`).then((b) => ({ ...b, items: b.items?.map(veil) })),
  /** Fissa la prima baseline (le altre nascono dalle change request approvate). */
  createBaseline: (ref: string, note = "") => call<Baseline>("POST", `/projects/${q(ref)}/baselines`, { note }),
  changeRequests: (ref: string) =>
    call<{ items: ChangeRequest[] }>("GET", `/projects/${q(ref)}/change-requests`).then((r) => ({ items: r.items.map(veilCR) })),
  createChangeRequest: (ref: string, c: { title?: string; reason: string }) => call<ChangeRequest>("POST", `/projects/${q(ref)}/change-requests`, c),
  submitChangeRequest: (id: string, reason = "") => call<ChangeRequest>("POST", `/change-requests/${q(id)}/submit`, { reason }),
  withdrawChangeRequest: (id: string) => call<ChangeRequest>("POST", `/change-requests/${q(id)}/withdraw`, {}),

  // Fornitori (passo 4).
  supplier: (id: string) => call<SupplierView>("GET", `/plan-items/${q(id)}/supplier`).then(veilSupplier),
  updateSupplier: (id: string, p: SupplierPatch) => call<SupplierView>("PUT", `/plan-items/${q(id)}/supplier`, p).then(veilSupplier),
  createClaim: (id: string, c: NewClaim) => call<SupplierView>("POST", `/plan-items/${q(id)}/claims`, c).then(veilSupplier),
  decideClaim: (claimId: string, d: Decision) => call<SupplierView>("POST", `/progress-claims/${q(claimId)}/decision`, d).then(veilSupplier),
  createDeliverable: (id: string, d: { title: string; due?: string; milestone_id?: string }) =>
    call<SupplierView>("POST", `/plan-items/${q(id)}/deliverables`, d).then(veilSupplier),
  deliver: (deliverableId: string, note = "") => call<SupplierView>("POST", `/deliverables/${q(deliverableId)}/deliver`, { note }).then(veilSupplier),
  decideDeliverable: (deliverableId: string, d: Decision) => call<SupplierView>("POST", `/deliverables/${q(deliverableId)}/decision`, d).then(veilSupplier),

  // Programmi (passo 4).
  programs: (closed = false) =>
    call<{ items: ProgramSummary[] }>("GET", `/programs${closed ? "?closed=true" : ""}`).then((r) => ({
      items: r.items.map((p) => ({ ...p, next_milestone: p.next_milestone && veil(p.next_milestone) })),
    })),
  createProgram: (p: { title: string; body_text?: string }) => call<Element>("POST", "/programs", p),
  program: (ref: string) =>
    call<ProgramView>("GET", `/programs/${q(ref)}`).then((v) => ({
      ...v,
      numbers: veilNumbers(v.numbers),
      projects: v.projects.map((p) => ({ ...p, milestones: p.milestones.map(veil) })),
    })),
  /** Mette il progetto in un programma ("" lo toglie). */
  setProgram: (ref: string, program: string) => call<{ program: Ref | null }>("PUT", `/projects/${q(ref)}/program`, { program }),

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
  baselines: (ref: string) => ["project-baselines", ref] as const,
  changeRequests: (ref: string) => ["project-change-requests", ref] as const,
  ofRequest: (ref: string) => ["request-project", ref] as const,
  supplier: (id: string) => ["plan-item-supplier", id] as const,
  programs: ["programs"] as const,
  program: (ref: string) => ["program", ref] as const,
};
