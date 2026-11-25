// Avanzamento (piano, passi 13 e 14): status report, cruscotto di portafoglio, portale del richiedente.
import { call, type Element } from "./api";
import { tr } from "./i18n/core";
import type { FieldView } from "./fieldsApi";
import type { EntryView } from "./portalCatalogApi";

export type RAG = "green" | "amber" | "red";
// Etichette lette al momento dell'uso (getter): seguono la lingua corrente.
export const RAG_LABEL: Record<RAG, string> = {
  get green() {
    return tr("lavoro.statusreports.ragGreen");
  },
  get amber() {
    return tr("lavoro.statusreports.ragAmber");
  },
  get red() {
    return tr("lavoro.statusreports.ragRed");
  },
};
export const DIMENSIONS: { key: "overall" | "schedule" | "cost" | "scope" | "risk"; label: string }[] = [
  { key: "overall", get label() { return tr("lavoro.statusreports.dimOverall"); } },
  { key: "schedule", get label() { return tr("lavoro.statusreports.dimSchedule"); } },
  { key: "cost", get label() { return tr("lavoro.statusreports.dimCost"); } },
  { key: "scope", get label() { return tr("lavoro.statusreports.dimScope"); } },
  { key: "risk", get label() { return tr("lavoro.statusreports.dimRisk"); } },
];

export type Sections = { summary: string; done: string; next: string; risks: string; decisions: string; notes: Record<string, string> };
export type TaskRef = { key: string; application: string; team: string | null; end: string | null };
export type Snapshot = {
  as_of: string;
  phase: string;
  schedule: {
    start: string | null;
    end: string | null;
    desired_date: string | null;
    regulatory_deadline: string | null;
    tasks: number;
    tasks_done: number;
    tasks_scheduled: number;
    tasks_late: number;
    planned_pct: number | null;
    actual_pct: number | null;
    late: TaskRef[];
  };
  cost: {
    currency: string;
    approved_cents: number | null;
    actual_cents: number;
    committed_cents: number;
    forecast_cents: number | null;
    consumption_pct: number | null;
    cpi: number | null;
    spi: number | null;
    ev_as_of: string | null;
  };
  reviews_late: { key: string; title: string; team: string; due: string; late_days: number; mandatory: boolean }[];
  impacts_blocked: { key: string; application: string; reason: string }[];
  suggested: { overall: RAG; schedule: RAG; cost: RAG; scope: RAG; risk: RAG; reasons: Record<string, string[]> };
};

export type StatusReport = {
  id: string;
  element_id: string;
  element_key: string;
  element_title: string;
  period_start: string | null;
  period_end: string;
  status: "draft" | "submitted";
  overall: RAG;
  schedule: RAG;
  cost: RAG;
  scope: RAG;
  risk: RAG;
  sections: Sections;
  data: Snapshot;
  author_id: string | null;
  author: string | null;
  submitted_at: string | null;
  acknowledged_by: string | null;
  acknowledged_by_name: string | null;
  acknowledged_at: string | null;
  version: number;
  created_at: string;
  updated_at: string;
  prev_id: string | null;
  can_edit: boolean;
  can_acknowledge: boolean;
};

export type Frequency = "weekly" | "biweekly" | "monthly" | "off";
export const FREQUENCIES: Record<Frequency, string> = {
  get weekly() { return tr("lavoro.statusreports.freqWeekly"); },
  get biweekly() { return tr("lavoro.statusreports.freqBiweekly"); },
  get monthly() { return tr("lavoro.statusreports.freqMonthly"); },
  get off() { return tr("lavoro.statusreports.freqOff"); },
};

export type ReportSchedule = {
  frequency: Frequency | null;
  effective_frequency: Frequency;
  next_due: string | null;
  overdue: boolean;
  expected: boolean;
  last_overall: RAG | null;
  last_period_end: string | null;
  tenant_frequency: Frequency;
  reminder_days: number;
  expected_in_phases: string[];
};
export type ReportList = { items: StatusReport[]; schedule: ReportSchedule; can_edit: boolean };
export type Change = { field: string; label: string; from: unknown; to: unknown; trend: "better" | "worse" | "same" };
export type Comparison = { from: StatusReport; to: StatusReport; changes: Change[] };
export type ReportInput = Partial<Pick<StatusReport, "overall" | "schedule" | "cost" | "scope" | "risk">> & {
  version: number;
  period_start?: string;
  period_end?: string;
  sections?: Sections;
};
export type Indicator = { label: string; query: string };
export type ReportSettings = {
  frequency: Frequency;
  reminder_days: number;
  phases: string[];
  regulatory: Indicator[];
  updated_at: string | null;
  can_edit: boolean;
};

// Cruscotto
export type Num = { count: number; query: string };
export type PhaseStat = Num & { name: string; category: string; off_path: boolean; avg_days: number | null; stays: number; wait_days: number | null };
export type Slice = Num & { key: string; label: string };
export type Dashboard = {
  as_of: string;
  filter: { unit: string; driver: string; from: string; to: string };
  base_query: string;
  total: Num;
  open: Num;
  phases: PhaseStat[];
  late_desired: Num;
  late_deadline: Num;
  deadline_soon: Num;
  mandatory: Num;
  regulatory: (Num & { label: string; more: boolean })[];
  finance: {
    currency: string;
    approved_cents: number;
    actual_cents: number;
    committed_cents: number;
    consumption_pct: number | null;
    budget_query: string;
    cost_query: string;
    with_budget: number;
    with_costs: number;
    can_see_details: boolean;
    by_request?: { key: string; title: string; approved_cents: number; actual_cents: number; committed_cents: number; consumption_pct: number | null }[];
    by_category?: { category: string; nature: string; actual_cents: number; committed_cents: number }[];
  };
  reviews_late: (Num & { team: string })[];
  reviews_late_total: Num;
  team_load: { team: string; capacity_days: number; load_days: number; peak_pct: number; over_weeks: number; weeks: number[]; query: string }[];
  load_from: string;
  load_to: string;
  by_driver: Slice[];
  by_unit: Slice[];
  rag: {
    green: Num;
    amber: Num;
    red: Num;
    overdue: Num;
    items: { key: string; title: string; overall: RAG; schedule: RAG; cost: RAG; scope: RAG; risk: RAG; period_end: string; overdue: boolean }[];
  };
  units: Slice[];
};
export type PanelPref = { key: string; label: string; visible: boolean };
export type DashFilter = { unit?: string; driver?: string; from?: string; to?: string };

// Portale
export type NamedField = { key: string; name: string };
export type PortalTypeView = { type: string; label: string; description: string; fields: FieldView[]; standard: NamedField[] };
export type PortalDraft = { id: string; entry: string; type: string; title: string; data: Record<string, unknown>; step: number; created_at: string; updated_at: string };
export type MyRequest = {
  id: string;
  key: string;
  title: string;
  type_name: string;
  phase: string;
  category: string;
  text: string;
  step: number;
  steps: number;
  comments: number;
  created_at: string;
  updated_at: string;
};
export type Portal = { welcome: string; types: PortalTypeView[]; entries: EntryView[]; units: NamedField[]; my_unit: string; drafts: PortalDraft[]; requests: MyRequest[] };
export type SimilarRequest = { key: string; title: string; phase: string; mine: boolean };
export type PortalRequest = {
  id: string;
  key: string;
  title: string;
  description: string;
  type_name: string;
  phase: string;
  category: string;
  text: string;
  path: { name: string; state: "done" | "current" | "todo" }[];
  next_step: string;
  followed_by: { role: string; name: string }[];
  data: { label: string; value: string }[];
  actions: { to: string; label: string; comment_required: boolean; blocked?: string }[];
  created_at: string;
  updated_at: string;
};
export type PortalType = { type: string; label: string; description: string; fields: string[]; standard: string[] };
export type PortalSettings = {
  welcome: string;
  types: PortalType[];
  phase_texts: Record<string, string>;
  available?: { key: string; name: string; fields: NamedField[] }[];
  standard_fields?: NamedField[];
  phases?: { name: string; description: string; default: string }[];
  updated_at: string | null;
  can_edit: boolean;
};

const q = encodeURIComponent;

export const reportApi = {
  list: (ref: string) => call<ReportList>("GET", `/demand/requests/${q(ref)}/status-reports`),
  create: (ref: string, period_end?: string) => call<StatusReport>("POST", `/demand/requests/${q(ref)}/status-reports`, { period_end: period_end ?? "" }),
  schedule: (ref: string, frequency: string, next_due?: string) =>
    call<ReportList>("PUT", `/demand/requests/${q(ref)}/status-reports/schedule`, { frequency, ...(next_due !== undefined ? { next_due } : {}) }),
  get: (id: string) => call<StatusReport>("GET", `/status-reports/${id}`),
  update: (id: string, input: ReportInput) => call<StatusReport>("PATCH", `/status-reports/${id}`, input),
  remove: (id: string) => call<void>("DELETE", `/status-reports/${id}`),
  refresh: (id: string) => call<StatusReport>("POST", `/status-reports/${id}/refresh`),
  submit: (id: string) => call<StatusReport>("POST", `/status-reports/${id}/submit`),
  acknowledge: (id: string) => call<StatusReport>("POST", `/status-reports/${id}/acknowledge`),
  compare: (id: string, other?: string) => call<Comparison>("GET", `/status-reports/${id}/compare${other ? `?with=${other}` : ""}`),
  markdownUrl: (id: string) => `/api/v1/status-reports/${id}/export.md`,
  settings: () => call<ReportSettings>("GET", "/report/settings"),
  saveSettings: (s: Partial<Pick<ReportSettings, "frequency" | "reminder_days" | "phases" | "regulatory">>) => call<ReportSettings>("PUT", "/report/settings", s),

  dashboard: (f: DashFilter) => {
    const p = new URLSearchParams();
    for (const [k, v] of Object.entries(f)) if (v) p.set(k, v);
    const s = p.toString();
    return call<Dashboard>("GET", `/dashboard/portfolio${s ? `?${s}` : ""}`);
  },
  layout: () => call<{ panels: PanelPref[] }>("GET", "/dashboard/portfolio/layout"),
  saveLayout: (panels: PanelPref[]) => call<{ panels: PanelPref[] }>("PUT", "/dashboard/portfolio/layout", { panels }),

  portal: () => call<Portal>("GET", "/portal"),
  similar: (text: string) => call<{ items: SimilarRequest[] }>("GET", `/portal/similar?q=${encodeURIComponent(text)}`),
  portalRequest: (ref: string) => call<PortalRequest>("GET", `/portal/requests/${q(ref)}`),
  saveDraft: (id: string | null, d: { entry: string; type: string; title: string; data: Record<string, unknown>; step: number }) =>
    id ? call<PortalDraft>("PUT", `/portal/drafts/${id}`, d) : call<PortalDraft>("POST", "/portal/drafts", d),
  deleteDraft: (id: string) => call<void>("DELETE", `/portal/drafts/${id}`),
  submitDraft: (id: string) => call<Element>("POST", `/portal/drafts/${id}/submit`),
  portalSettings: () => call<PortalSettings>("GET", "/portal/settings"),
  savePortalSettings: (s: Partial<Pick<PortalSettings, "welcome" | "types" | "phase_texts">>) => call<PortalSettings>("PUT", "/portal/settings", s),
};
