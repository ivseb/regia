// API della Vista DPO (ondata 3, replica/usabilita/governo.md, C8): registro dei trattamenti, DPIA,
// violazioni dei dati personali e flussi di dati. Modulo a parte, caricato con la pagina che lo usa.
import { call } from "./api";

export type AppRef = {
  id: string;
  key: string;
  name: string;
  classification: string;
  personal: boolean;
  credentials: boolean;
  financial: boolean;
};

export type Issue = "no_basis" | "no_purpose" | "no_application" | "dpia_missing";
export type DpiaStatus = "draft" | "in_review" | "approved";

export type Treatment = {
  id: string;
  name: string;
  purpose: string;
  legal_basis: string;
  legal_basis_note: string;
  data_subjects: string[];
  data_categories: string[];
  special_categories: boolean;
  large_scale: boolean;
  profiling: boolean;
  high_risk: boolean;
  recipients: string;
  transfers_extra_eu: boolean;
  transfer_details: string;
  retention: string;
  owner_id: string | null;
  owner: string | null;
  status: "active" | "archived";
  updated_at: string;
  applications: AppRef[];
  dpia_required: boolean;
  dpia: { id: string; status: DpiaStatus } | null;
  issues: Issue[];
};

export type TreatmentInput = {
  name: string;
  purpose: string;
  legal_basis: string;
  legal_basis_note: string;
  data_subjects: string[];
  data_categories: string[];
  special_categories: boolean;
  large_scale: boolean;
  profiling: boolean;
  high_risk: boolean;
  recipients: string;
  transfers_extra_eu: boolean;
  transfer_details: string;
  retention: string;
  owner_id: string;
  status: "active" | "archived";
  applications: string[];
};

export type ElRef = { id: string; key: string; title: string; state: string; detail: string };
export type ReviewRef = { id: string; key: string; request: string; type: string; stage: string; outcome: string | null; polarity: string | null };

export type Dpia = {
  id: string;
  treatment_id: string;
  treatment: string;
  status: DpiaStatus;
  description: string;
  necessity: string;
  risk_assessment: string;
  dpo_opinion: string;
  review_id: string | null;
  review: ReviewRef | null;
  submitted_at: string | null;
  approved_at: string | null;
  approved_by: string | null;
  updated_at: string;
  risks: ElRef[];
  controls: ElRef[];
  reviews: ReviewRef[];
};

export type DpiaInput = {
  description: string;
  necessity: string;
  risk_assessment: string;
  dpo_opinion: string;
  review_id: string;
  risk_ids: string[];
  control_ids: string[];
};

export type DpiaItem = { id: string; treatment_id: string; treatment: string; status: DpiaStatus; risks: number; controls: number; has_review: boolean; updated_at: string };

export type BreachStatus = "open" | "overdue" | "notified" | "notified_late" | "not_required";
export type Breach = {
  id: string;
  key: string;
  title: string;
  phase: string;
  phase_category: string;
  major: boolean;
  detected_at: string;
  resolved_at: string | null;
  personal: boolean;
  source: "manual" | "treatment" | "application" | "none";
  note: string;
  deadline_at: string;
  hours_left: number;
  status: BreachStatus;
  notified_at: string | null;
  not_required: boolean;
  not_required_reason: string;
  closed: boolean;
  applications: AppRef[];
  treatments: { id: string; name: string }[];
};

export type FlowNode = AppRef & { core: boolean; linked: boolean };
export type FlowEdge = { id: string; type: string; name: string; source: string; target: string; data: string };
export type Flows = {
  treatments: { treatment: { id: string; name: string }; nodes: FlowNode[]; edges: FlowEdge[]; exposed: number }[];
  unlinked: AppRef[];
  personal: number;
  credentials: number;
  financial: number;
};

export type Home = {
  counts: {
    treatments: number;
    no_basis: number;
    dpia_required: number;
    dpia_missing: number;
    dpia_in_review: number;
    dpia_approved: number;
    open_breaches: number;
    soon_breaches: number;
    unlinked_apps: number;
    transfers_extra_eu: number;
  };
  no_basis: Treatment[];
  dpia_missing: Treatment[];
  in_review: DpiaItem[];
  breaches: Breach[];
  unlinked: AppRef[];
};

const q = encodeURIComponent;
const base = "/privacy";

export const privacyApi = {
  home: () => call<Home>("GET", `${base}/home`),
  treatments: (archived = false) =>
    call<{ items: Treatment[]; legal_bases: { key: string; name: string }[] }>("GET", `${base}/treatments${archived ? "?archived=1" : ""}`),
  createTreatment: (b: TreatmentInput) => call<Treatment>("POST", `${base}/treatments`, b),
  updateTreatment: (id: string, b: TreatmentInput) => call<Treatment>("PUT", `${base}/treatments/${q(id)}`, b),
  deleteTreatment: (id: string) => call<void>("DELETE", `${base}/treatments/${q(id)}`),
  csvUrl: "/api/v1/privacy/treatments.csv",
  dpias: () => call<{ items: DpiaItem[] }>("GET", `${base}/dpias`),
  dpia: (treatmentId: string) => call<Dpia>("GET", `${base}/treatments/${q(treatmentId)}/dpia`),
  saveDpia: (treatmentId: string, b: DpiaInput) => call<Dpia>("PUT", `${base}/treatments/${q(treatmentId)}/dpia`, b),
  dpiaTo: (treatmentId: string, to: DpiaStatus) => call<Dpia>("POST", `${base}/treatments/${q(treatmentId)}/dpia/transition`, { to }),
  breaches: (all = false) => call<{ items: Breach[]; hours: number }>("GET", `${base}/breaches${all ? "?all=1" : ""}`),
  setBreach: (id: string, b: { personal: boolean; note: string; treatment_ids: string[] }) => call<Breach>("PUT", `${base}/breaches/${q(id)}`, b),
  notify: (id: string, b: { notified_at?: string; not_required?: boolean; reason?: string; clear?: boolean }) =>
    call<Breach>("POST", `${base}/breaches/${q(id)}/notification`, b),
  flows: () => call<Flows>("GET", `${base}/flows`),
};
