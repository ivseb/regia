// API dei requisiti, della matrice di tracciabilità, delle verifiche e delle deroghe
// (piano, passi 5 e 6). Sta in un modulo a parte, caricato con le pagine dei requisiti,
// così il bundle iniziale non cresce. L'inbox (numero nel menu) è in api.ts.
import { call, requirementInbox, type Element, type Problem } from "./api";
import { tr } from "./i18n/core";

const q = encodeURIComponent;

export type CellStatus = "proposed" | "open" | "to_verify" | "verified" | "waived" | "not_applicable";

export type MatrixColumn = { impact_id: string; impact_key: string; application: string; team: string; phase: string };
export type MatrixCell = {
  status: CellStatus;
  exception_key?: string;
  expires_on?: string;
  exception_expired?: boolean;
  verified_by?: string;
  verified_at?: string;
};
export type MatrixRow = {
  id: string;
  key: string;
  title: string;
  scope: "project" | "application";
  category: string;
  stage: string;
  phase: string;
  origin: "prescription" | "catalog" | "manual" | "questionnaire";
  method: "manual" | "evidence";
  impact_key?: string;
  status: CellStatus;
  cells: (MatrixCell | null)[];
};
export type MissingItem = { key: string; title: string; status: CellStatus; status_label: string; impact?: string; application?: string };
export type Coverage = {
  requisiti: number;
  requisiti_aperti: number;
  requisiti_da_verificare: number;
  requisiti_verificati: number;
  requisiti_in_deroga: number;
  deroghe_scadute: number;
  pronto_al_rilascio: boolean;
  mancanti: MissingItem[];
  /** Solo nella matrice a pagine: quanti mancano in tutto (mancanti elenca i primi). */
  mancanti_totale?: number;
};
export type Matrix = {
  request: { id: string; key: string; title: string; state: string };
  impact?: string;
  columns: MatrixColumn[];
  rows: MatrixRow[];
  summary: Coverage;
  teams: string[];
  categories: string[];
  statuses: Record<CellStatus, string>;
  /** Righe che rispettano i filtri (anche quelle non ancora caricate), da dove parte la pagina e se ce ne sono altre. */
  total_rows: number;
  offset: number;
  more: boolean;
  can: { create: boolean };
};
export type MatrixFilter = { team?: string; category?: string; status?: string };

export type RequirementInfo = {
  id: string;
  key: string;
  title: string;
  text: string;
  scope: "project" | "application";
  origin: "prescription" | "catalog" | "manual" | "questionnaire";
  category: string;
  stage: string;
  phase: string;
  request_id: string;
  request_key: string;
  request_title: string;
  impact_key: string | null;
  application: string | null;
  review_key: string | null;
  review_title: string | null;
  prescription: string | null;
  catalog_key: string | null;
  catalog_name: string | null;
  catalog_version: number | null;
  catalog_item: string | null;
  baseline_key: string | null;
  verification_method: "manual" | "evidence";
  verification_check: string | null;
  verification_criterion: string;
  verifier_team: string | null;
  verifier_team_key: string | null;
  verifier_role: string | null;
  verifier_role_name: string | null;
  created_at: string;
};
export type RequirementCell = {
  impact_id: string;
  impact_key: string;
  application: string;
  team: string;
  status: CellStatus;
  status_label: string;
  exception_key?: string;
  expires_on?: string;
  exception_expired?: boolean;
  implemented_by: string | null;
  implemented_at: string | null;
  verified_by: string | null;
  verified_at: string | null;
  can: { ready: boolean; verify: boolean; exception: boolean; why_not?: string };
};
export type Verification = {
  id: string;
  impact_key: string;
  outcome: "verified" | "rejected";
  method: "manual" | "automatic";
  collector: string | null;
  note: string;
  links: string[];
  attachments: { id: string; name: string; sha256: string }[];
  evidence_key: string | null;
  evidence_title: string | null;
  by: string | null;
  at: string;
  signature: string;
};
export type RiskLevel = { key: string; name: string; score: number };
export type RequirementSettings = { risk_levels: RiskLevel[]; max_exception_days: number; expiry_notice_days: number };
export type ExceptionSummary = {
  id: string;
  key: string;
  stage: string;
  phase: string;
  risk_level: string;
  risk_name: string;
  expires_on: string;
  impacts: string[];
  created_at: string;
};
export type RequirementView = {
  requirement: RequirementInfo;
  element: Element;
  cells: RequirementCell[];
  verifications: Verification[];
  exceptions: ExceptionSummary[];
  settings: RequirementSettings;
  can: { edit: boolean; verify: boolean; exception: boolean };
};
export type ExceptionView = {
  element: Element;
  key: string;
  requirement_key: string;
  requirement_title: string;
  impacts: string[];
  reason: string;
  risk_level: string;
  risk_name: string;
  risk_score: number;
  compensating: string;
  expires_on: string;
  days_left: number;
  stage: "draft" | "pending" | "approved" | "rejected" | "expired" | "revoked";
  phase: string;
  requested_by: string | null;
  approved_at: string | null;
  closed_at: string | null;
  settings: RequirementSettings;
  can: { edit?: boolean };
};
export type CreateRequirementInput = {
  title: string;
  text?: string;
  category?: string;
  scope: "project" | "application";
  impact?: string;
  verification_method?: "manual" | "evidence";
  verification_check?: string;
  verification_criterion?: string;
  verifier_team?: string;
  verifier_role?: string;
};
export type VerifyInput = { outcome: "verified" | "rejected"; note: string; links: string[]; attachments: string[]; evidence_element?: string };
export type ExceptionInput = {
  impacts: string[];
  reason: string;
  risk_level: string;
  expires_on: string;
  compensating: string;
};

// Configurazione
export type CatalogVerification = { method: "manual" | "evidence"; check?: string; criterion?: string; team?: string; role?: string };
export type CatalogItem = { key: string; title: string; text?: string; category?: string; verification: CatalogVerification };
export type Baseline = { key: string; name: string; description?: string; scope: "application" | "project"; when?: string; items: string[] };
export type CatalogDefinition = { name: string; description?: string; items: CatalogItem[]; baselines: Baseline[] };
export type CatalogSummary = {
  key: string;
  name: string;
  description: string;
  version: number | null;
  published_at: string | null;
  items: number;
  baselines: Baseline[];
  has_draft: boolean;
  draft_version: number | null;
};
export type CatalogVersion = {
  id: string;
  key: string;
  version: number;
  status: "draft" | "published" | "superseded";
  name: string;
  description: string;
  definition: CatalogDefinition | null;
  notes: string;
  created_at: string;
  published_at: string | null;
  published_by: string | null;
};
export type Catalog = {
  key: string;
  current: CatalogVersion | null;
  draft: CatalogVersion | null;
  draft_problems: Problem[];
  versions: CatalogVersion[];
  requirements: number;
};
export type Approver = { role?: string; body?: string; member?: string };
export type PolicyRule = { when: { risk_lte?: number; risk_gt?: number }; steps: { approvers: Approver[] }[] };
export type RequirementConfig = {
  catalogs: CatalogSummary[];
  settings: RequirementSettings;
  policy: { id: string; name: string; rules: PolicyRule[] };
  teams: { id: string; key: string; name: string; group: string; archived: boolean }[];
  roles: { key: string; name: string }[];
  statuses: Record<CellStatus, string>;
  can_edit: boolean;
};
export type PreviewItem = { id: string; key: string; code?: string; title: string; matched: boolean; facts: Record<string, string> };

const filterQuery = (f: MatrixFilter, limit?: number) => {
  const p = new URLSearchParams();
  if (limit) p.set("limit", String(limit));
  if (f.team) p.set("team", f.team);
  if (f.category) p.set("category", f.category);
  if (f.status) p.set("status", f.status);
  const s = p.toString();
  return s ? `?${s}` : "";
};
const rq = (ref: string) => `/requirements/${q(ref)}`;
const cfg = "/requirement-config";

export const requirementsApi = {
  inbox: requirementInbox,
  /** limit: righe da caricare (le prime N); senza, la matrice intera. Il riepilogo è sempre di tutta la matrice. */
  matrix: (ref: string, f: MatrixFilter, limit?: number) => call<Matrix>("GET", `/elements/${q(ref)}/requirements${filterQuery(f, limit)}`),
  csvUrl: (ref: string, f: MatrixFilter) => `/api/v1/elements/${q(ref)}/requirements.csv${filterQuery(f)}`,
  create: (ref: string, input: CreateRequirementInput) => call<RequirementView>("POST", `/elements/${q(ref)}/requirements`, input),
  applyBaseline: (ref: string, catalog: string) => call<{ created: string[] }>("POST", `/elements/${q(ref)}/requirements/baseline`, { catalog }),
  get: (ref: string) => call<RequirementView>("GET", rq(ref)),
  update: (ref: string, p: Partial<CreateRequirementInput>) => call<RequirementView>("PATCH", rq(ref), p),
  ready: (ref: string, impact: string, note: string) => call<RequirementView>("POST", `${rq(ref)}/cells/${q(impact)}/ready`, { note }),
  verify: (ref: string, impact: string, v: VerifyInput) => call<RequirementView>("POST", `${rq(ref)}/cells/${q(impact)}/verify`, v),
  requestException: (ref: string, e: ExceptionInput) => call<ExceptionView>("POST", `${rq(ref)}/exceptions`, e),
  exception: (ref: string) => call<ExceptionView>("GET", `/exceptions/${q(ref)}`),
  updateException: (ref: string, e: Partial<ExceptionInput>) => call<ExceptionView>("PATCH", `/exceptions/${q(ref)}`, e),

  config: () => call<RequirementConfig>("GET", cfg),
  exportUrl: () => `/api/v1${cfg}/export`,
  saveSettings: (s: RequirementSettings) => call<RequirementSettings>("PUT", `${cfg}/settings`, s),
  savePolicy: (rules: PolicyRule[]) => call<RequirementConfig["policy"]>("PUT", `${cfg}/exception-policy`, { rules }),
  preview: (condition: string, scope: string) => call<{ items: PreviewItem[] }>("POST", `${cfg}/preview`, { condition, scope }),
  catalog: (key: string) => call<Catalog>("GET", `${cfg}/catalogs/${q(key)}`),
  validate: (definition: unknown) => call<{ problems: Problem[] }>("POST", `${cfg}/catalogs/validate`, { definition }),
  saveDraft: (key: string, definition: unknown, notes = "") => call<Catalog>("PUT", `${cfg}/catalogs/${q(key)}/draft`, { definition, notes }),
  discardDraft: (key: string) => call<void>("DELETE", `${cfg}/catalogs/${q(key)}/draft`),
  publish: (key: string, notes = "") => call<Catalog>("POST", `${cfg}/catalogs/${q(key)}/publish`, { notes }),
};

export const STATUS_LABELS: Record<CellStatus, string> = {
  get proposed() { return tr("governance.requirementsapi.da_accettare"); },
  get open() { return tr("governance.requirementsapi.da_realizzare"); },
  get to_verify() { return tr("governance.requirements.da_verificare_2"); },
  get verified() { return tr("governance.requirements.verificato"); },
  get waived() { return tr("governance.requirementsapi.in_deroga"); },
  get not_applicable() { return tr("governance.requirementsapi.non_applicabile"); },
};

export const ORIGIN_LABELS: Record<string, string> = { get prescription() { return tr("governance.requirementsapi.prescrizione"); }, get catalog() { return tr("governance.requirementsapi.catalogo"); }, get manual() { return tr("governance.requirementsapi.manuale"); }, get questionnaire() { return tr("governance.requisitiQ.origine.questionario"); } };
