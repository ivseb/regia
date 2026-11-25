// API della conformità (piano, passo 20; replica/grc.md): cataloghi normativi, controlli,
// applicabilità, evidenze, cruscotto e pacchetto per l'audit. Modulo a parte, caricato con
// le pagine della conformità, così il bundle iniziale non cresce.
import { call } from "./api";
import { tr } from "./i18n/core";

const q = encodeURIComponent;

export type CoverageStatus = "covered" | "partial" | "no_evidence" | "no_control" | "not_applicable";
export type EvidenceStatus = "valid" | "expiring" | "expired" | "draft" | "rejected" | "revoked";
export type ScopeKind = "tenant" | "legal_entity" | "application" | "process";

export type Scope = { kind: ScopeKind; id?: string; key?: string; name: string };
export type Counts = {
  applicable: number;
  covered: number;
  partial: number;
  no_evidence: number;
  no_control: number;
  not_applicable: number;
  expiring: number;
  percent: number;
  /** Copertura pesata per criticità del perimetro: le applicazioni critiche contano di più. */
  weighted_percent: number;
};
export type FrameworkSummary = Counts & {
  key: string;
  name: string;
  version: string;
  requirements: number;
  scopes: number;
  without_control: number;
  controls_without_evidence: number;
  expiring_evidence: number;
};
export type EvidenceRef = {
  id: string;
  key: string;
  title: string;
  kind: string;
  status: EvidenceStatus;
  valid_until: string | null;
  validated_by: string;
  derived: boolean;
  scope: Scope;
  controls?: string[];
};
export type ReqRef = { id: string; framework: string; ref: string; title: string; scopes: number };
export type ControlRef = { id: string; key: string; title: string; owner: string; missing: Scope[]; expiring: number };
export type ScopeFramework = Counts & { key: string; name: string; worst: CoverageStatus };
export type ScopeSummary = Scope & Counts & { frameworks: ScopeFramework[]; worst: CoverageStatus };
export type Settings = { expiry_notice_days: number; default_validity_days: number; process_types: string[]; recomputed_at?: string | null };
export type Dashboard = {
  generated_at: string;
  frameworks: FrameworkSummary[];
  tenant: Counts;
  scopes: ScopeSummary[];
  without_control: ReqRef[];
  controls_without_evidence: ControlRef[];
  expiring: EvidenceRef[];
  controls: number;
  valid_evidence: number;
  settings: Settings;
  status_names: Record<CoverageStatus, string>;
  can_edit: boolean;
};

export type PairControl = { id: string; key: string; title: string; coverage: "full" | "partial"; effectiveness: string; evidence: EvidenceRef[] };
export type PairView = { scope: Scope; status: CoverageStatus; source: string; note?: string; expiring: boolean; controls: PairControl[] };
export type CoverageItem = {
  id: string;
  ref: string;
  parent: string;
  kind: string;
  title: string;
  summary: string;
  assessable: boolean;
  status: CoverageStatus | "";
  counts: Counts;
  controls: number;
  pairs?: PairView[];
};
export type FrameworkCoverage = { framework: FrameworkSummary | null; enabled: boolean; items: CoverageItem[] };

export type MappingView = {
  id: string;
  from: string;
  from_id: string;
  from_title: string;
  to: string;
  to_id: string;
  to_title: string;
  relation: string;
  source: string;
  system: boolean;
};
export type DecisionView = { scope: Scope; status: string; rule?: string; justification: string; decided_by: string; decided_at: string };
export type ControlBrief = { id: string; key: string; title: string; phase: string; operational: boolean; coverage: string; effectiveness: string; owner: string };
export type RequirementDetail = CoverageItem & {
  framework: FrameworkSummary;
  enabled: boolean;
  mappings: MappingView[];
  decisions: DecisionView[];
  rules: string[];
  mapped_controls: ControlBrief[];
  can_edit: boolean;
};
export type ScopeItem = PairView & { id: string; ref: string; title: string };
export type ScopeCompliance = {
  scope: Scope;
  frameworks: (Counts & { key: string; name: string; worst: CoverageStatus; items: ScopeItem[] })[];
  counts: Counts;
  tenant: Counts;
};

export type VersionInfo = {
  id: string;
  version: string;
  status: "draft" | "published" | "superseded";
  effective_from: string | null;
  source_url: string;
  license_note: string;
  notes: string;
  published_at: string | null;
  items: number;
  assessable: number;
};
export type FrameworkInfo = {
  id: string;
  key: string;
  name: string;
  kind: string;
  jurisdiction: string;
  publisher: string;
  description: string;
  system: boolean;
  starter: string | null;
  status: "" | "active" | "paused" | "retired";
  current: VersionInfo | null;
  draft: VersionInfo | null;
  versions: VersionInfo[];
};
export type Starter = { key: string; name: string; description: string; version: string; items: number; imported: boolean };
export type Library = { frameworks: FrameworkInfo[]; starters: Starter[]; settings: Settings; can_edit: boolean };
export type ItemView = { id: string; ref: string; parent: string; kind: string; title: string; summary: string; assessable: boolean };
export type FrameworkDetail = FrameworkInfo & { shown: VersionInfo | null; items: ItemView[]; can_edit: boolean };

export type RuleInput = {
  key: string;
  name: string;
  description?: string;
  framework?: string;
  refs?: string[];
  controls?: string[];
  scope: "legal_entity" | "application" | "process";
  condition?: string;
  active?: boolean;
};
export type RuleView = RuleInput & { id: string; matches: number; missing: string[] };
export type Rules = { items: RuleView[]; recomputed_at: string | null; can_edit: boolean };
export type PreviewItem = Scope & { matched: boolean; facts: Record<string, string[]> };

export type ControlSummary = {
  id: string;
  key: string;
  title: string;
  phase: string;
  operational: boolean;
  owner_id: string;
  owner: string;
  frequency: string;
  kind: string;
  automation: string;
  effectiveness: string;
  assessed_at: string | null;
  frameworks: string[];
  requirements: number;
  valid_evidence: number;
  expiring_evidence: number;
  missing_scopes: number;
  next_expiry: string | null;
  next_review: string | null;
};
export type MappedReq = {
  id: string;
  ref: string;
  framework: string;
  framework_name: string;
  req_ref: string;
  title: string;
  coverage: string;
  note: string;
  current: boolean;
  enabled: boolean;
};
export type ScopeNeed = { scope: Scope; requirements: string[]; evidence: EvidenceRef[]; status: "ok" | "expiring" | "missing" };
export type ControlView = ControlSummary & {
  text: string;
  effectiveness_note: string;
  assessed_by: string;
  collectors: string[];
  requirements: MappedReq[];
  scopes: DecisionView[];
  needs: ScopeNeed[];
  evidence: EvidenceRef[];
  can: { edit: boolean; add_evidence: boolean; validate: boolean };
};
export type ControlInput = {
  title: string;
  text?: string;
  owner_id?: string;
  frequency?: string;
  kind?: string;
  automation?: string;
  collectors?: string[];
  requirements?: { ref: string; coverage?: string }[];
};
export type EvidenceInput = {
  controls: string[];
  kind: string;
  title: string;
  note?: string;
  url?: string;
  page?: string;
  scope?: string;
  target?: string;
  valid_until?: string;
};
export type EvidenceView = EvidenceRef & {
  note: string;
  url: string;
  page: { id: string; key: string; title: string } | null;
  source: string;
  collector: string;
  external_ref: string;
  valid_from: string;
  validated_at: string | null;
  signature: string;
  signature_ok: boolean;
  status_note: string;
  created_by: string;
  linked_controls: { id: string; key: string; title: string }[];
  attachments: number;
  can: { validate: boolean; reject: boolean; revoke: boolean };
};
export type LinkRef = { ref: string; framework: string; req_ref: string; id?: string; title: string; found: boolean };
export type Links = { requirement: { id: string; key: string; title: string }; manual: LinkRef[]; catalog: LinkRef[]; can_edit: boolean };

export const STATUS_NAMES: Record<CoverageStatus, string> = {
  get covered() { return tr("governance.compliance.coperto"); },
  get partial() { return tr("governance.compliancesections.parziale"); },
  get no_evidence() { return tr("governance.grcapi.senza_evidenza_valida"); },
  get no_control() { return tr("governance.compliance.senza_controllo_2"); },
  get not_applicable() { return tr("governance.compliance.non_applicabile_2"); },
};
export const EVIDENCE_STATUS: Record<EvidenceStatus, string> = {
  get valid() { return tr("governance.grcapi.valida"); },
  get expiring() { return tr("governance.vulnerabilities.in_scadenza"); },
  get expired() { return tr("governance.vulnerabilities.scaduta"); },
  get draft() { return tr("governance.grcapi.da_validare"); },
  get rejected() { return tr("governance.grcapi.respinta"); },
  get revoked() { return tr("governance.vulnerabilities.revocata"); },
};
export const EVIDENCE_KINDS: Record<string, string> = {
  get attachment() { return tr("governance.grcapi.allegato"); },
  get link() { return tr("governance.compliancesections.link"); },
  get knowledge_page() { return tr("governance.compliancesections.pagina_knowledge"); },
  get note() { return tr("governance.compliancesections.nota_2"); },
  get release() { return tr("governance.grcapi.rilascio"); },
  get automatic() { return tr("governance.grcapi.automatica"); },
  get requirement_verification() { return tr("governance.grcapi.verifica_di_progetto"); },
};
export const FREQUENCIES: Record<string, string> = {
  get continuous() { return tr("governance.grcapi.continua"); },
  get monthly() { return tr("governance.grcapi.mensile"); },
  get quarterly() { return tr("governance.grcapi.trimestrale"); },
  get semiannual() { return tr("governance.grcapi.semestrale"); },
  get annual() { return tr("governance.grcapi.annuale"); },
  get biennial() { return tr("governance.grcapi.biennale"); },
};
export const EFFECTIVENESS: Record<string, string> = { get not_assessed() { return tr("governance.grcapi.non_valutata"); }, get effective() { return tr("governance.compliancesections.efficace"); }, get partial() { return tr("governance.compliancesections.parziale"); }, get ineffective() { return tr("governance.compliancesections.inefficace"); } };
export const CONTROL_KINDS: Record<string, string> = { get preventive() { return tr("governance.grcapi.preventivo"); }, get detective() { return tr("governance.grcapi.di_rilevazione"); }, get corrective() { return tr("governance.grcapi.correttivo"); } };
export const AUTOMATIONS: Record<string, string> = { get manual() { return tr("governance.grcapi.manuale"); }, get semi() { return tr("governance.grcapi.semiautomatico"); }, get automatic() { return tr("governance.grcapi.automatico"); } };
export const SCOPE_NAMES: Record<ScopeKind, string> = {
  get tenant() { return tr("governance.grcui.tutta_l_organizzazione"); },
  get legal_entity() { return tr("governance.grcapi.entita_legale"); },
  get application() { return tr("governance.requirements.applicazione"); },
  get process() { return tr("governance.grcapi.processo"); },
};
export const ITEM_KINDS: Record<string, string> = {
  get chapter() { return tr("governance.grcapi.capo"); },
  get article() { return tr("governance.grcapi.articolo"); },
  get paragraph() { return tr("governance.grcapi.paragrafo"); },
  get requirement() { return tr("governance.grcapi.requisito"); },
  get control() { return tr("governance.grcapi.controllo"); },
  get annex() { return tr("governance.grcapi.allegato"); },
};
export const RELATIONS: Record<string, string> = { get equivalent() { return tr("governance.grcapi.equivalente"); }, get subset() { return tr("governance.grcapi.sottoinsieme"); }, get superset() { return tr("governance.grcapi.sovrainsieme"); }, get related() { return tr("governance.grcapi.correlato"); } };

export const grcApi = {
  dashboard: () => call<Dashboard>("GET", "/grc/dashboard"),
  library: () => call<Library>("GET", "/grc/library"),
  settings: (s: Settings) => call<Settings>("PUT", "/grc/settings", s),
  importStarter: (key: string, enable = true) => call<{ framework: FrameworkInfo }>("POST", `/grc/starters/${q(key)}`, { enable }),
  importCatalog: (body: {
    format: "json" | "csv" | "oscal";
    content: string;
    framework?: Partial<Pick<FrameworkInfo, "key" | "name" | "kind">>;
    version?: { version: string };
    publish: boolean;
    enable: boolean;
  }) => call<FrameworkInfo>("POST", "/grc/frameworks/import", body),
  framework: (key: string, draft = false) => call<FrameworkDetail>("GET", `/grc/frameworks/${q(key)}${draft ? "?draft=1" : ""}`),
  coverage: (key: string) => call<FrameworkCoverage>("GET", `/grc/frameworks/${q(key)}/coverage`),
  exportUrl: (key: string, format: "oscal" | "csv" | "json") => `/api/v1/grc/frameworks/${q(key)}/export?format=${format}`,
  newDraft: (key: string, version: string) => call<FrameworkInfo>("POST", `/grc/frameworks/${q(key)}/draft`, { version }),
  discardDraft: (key: string) => call<void>("DELETE", `/grc/frameworks/${q(key)}/draft`),
  draftItem: (key: string, it: Omit<ItemView, "id" | "assessable">) => call<FrameworkDetail>("PUT", `/grc/frameworks/${q(key)}/draft/items`, it),
  removeDraftItem: (key: string, ref: string) => call<FrameworkDetail>("DELETE", `/grc/frameworks/${q(key)}/draft/items?ref=${q(ref)}`),
  publish: (key: string) => call<FrameworkInfo>("POST", `/grc/frameworks/${q(key)}/publish`),
  setStatus: (key: string, status: string) => call<FrameworkInfo>("PUT", `/grc/frameworks/${q(key)}/status`, { status }),
  mappings: (framework = "") => call<{ items: MappingView[] }>("GET", `/grc/mappings?framework=${q(framework)}`),
  mappingsCsvUrl: (framework = "") => `/api/v1/grc/mappings.csv?framework=${q(framework)}`,
  addMapping: (from: string, to: string, relation: string) => call<MappingView>("POST", "/grc/mappings", { from, to, relation }),
  deleteMapping: (id: string) => call<void>("DELETE", `/grc/mappings/${q(id)}`),
  requirement: (id: string) => call<RequirementDetail>("GET", `/grc/requirements/${q(id)}`),
  setApplicability: (id: string, items: { scope: string; target?: string; status: string; justification?: string }[]) =>
    call<RequirementDetail>("PUT", `/grc/requirements/${q(id)}/applicability`, { items }),
  rules: () => call<Rules>("GET", "/grc/rules"),
  saveRules: (items: RuleInput[]) => call<Rules>("PUT", "/grc/rules", { items }),
  preview: (scope: string, condition: string) => call<{ items: PreviewItem[] }>("POST", "/grc/rules/preview", { scope, condition }),
  recompute: () => call<{ rows: number; at: string }>("POST", "/grc/recompute"),
  scope: (kind: string, target: string) => call<ScopeCompliance>("GET", `/grc/scopes/${q(kind)}/${q(target)}`),
  controls: () => call<{ items: ControlSummary[] }>("GET", "/grc/controls"),
  createControl: (c: ControlInput) => call<ControlView>("POST", "/grc/controls", c),
  control: (ref: string) => call<ControlView>("GET", `/grc/controls/${q(ref)}`),
  updateControl: (ref: string, c: Partial<{ owner_id: string; frequency: string; kind: string; automation: string; collectors: string[] }>) =>
    call<ControlView>("PATCH", `/grc/controls/${q(ref)}`, c),
  assess: (ref: string, effectiveness: string, note: string) => call<ControlView>("POST", `/grc/controls/${q(ref)}/assessment`, { effectiveness, note }),
  setControlRequirements: (ref: string, items: { ref: string; coverage?: string }[]) =>
    call<ControlView>("PUT", `/grc/controls/${q(ref)}/requirements`, { items }),
  evidenceList: (status = "") => call<{ items: EvidenceRef[] }>("GET", `/grc/evidence?status=${q(status)}`),
  createEvidence: (e: EvidenceInput) => call<EvidenceView>("POST", "/grc/evidence", e),
  evidence: (ref: string) => call<EvidenceView>("GET", `/grc/evidence/${q(ref)}`),
  validate: (ref: string, valid_until = "", note = "") => call<EvidenceView>("POST", `/grc/evidence/${q(ref)}/validate`, { valid_until, note }),
  reject: (ref: string, note: string) => call<EvidenceView>("POST", `/grc/evidence/${q(ref)}/reject`, { note }),
  revoke: (ref: string, note: string) => call<EvidenceView>("POST", `/grc/evidence/${q(ref)}/revoke`, { note }),
  links: (ref: string) => call<Links>("GET", `/grc/project-requirements/${q(ref)}/links`),
  setLinks: (ref: string, refs: string[]) => call<Links>("PUT", `/grc/project-requirements/${q(ref)}/links`, { refs }),
  auditPackUrl: (framework = "") => `/api/v1/grc/audit-pack${framework ? `?framework=${q(framework)}` : ""}`,
  exportPackageUrl: () => "/api/v1/grc/export",
};
