// API dello spazio dell'auditor (ondata 3, C6): incarichi, campioni, richieste di evidenza,
// commenti e rilievi. L'auditor scrive solo queste cose; il resto della conformità è in lettura.
import { call } from "./api";

export type Brief = { id: string; key: string; title: string };

export type Engagement = {
  id: string;
  title: string;
  framework: string;
  period_from: string;
  period_to: string;
  status: "open" | "closed";
  note: string;
  created_by: string;
  created_at: string;
  closed_at: string | null;
  apps: Brief[];
  controls: number;
  reviewed: number;
  open_requests: number;
  overdue_requests: number;
  open_findings: number;
  samples: number;
};

export type EngControl = Brief & {
  owner: string;
  effectiveness: string;
  conclusion: "" | "reviewed" | "to_clarify";
  samples: number;
  sample_pending: number;
  open_requests: number;
  open_findings: number;
};

export type EngagementDetail = Engagement & { control_list: EngControl[]; can: { audit: boolean } };

export type EngagementInput = {
  title: string;
  framework: string;
  period_from: string;
  period_to: string;
  note: string;
  controls: string[];
  apps: string[];
};

export type Outcome = "pending" | "compliant" | "non_compliant" | "to_clarify";
export type SampleItem = {
  position: number;
  evidence: Brief;
  kind: string;
  status: string;
  valid_from: string;
  valid_until: string;
  url: string;
  outcome: Outcome;
  note: string;
  assessed_by: string;
  assessed_at: string | null;
};
export type Sample = {
  id: string;
  engagement: string;
  control: Brief;
  seed: number;
  size: number;
  population: number;
  drawn_by: string;
  drawn_at: string;
  period_from: string;
  period_to: string;
  items: SampleItem[];
};

export type AuditRequest = {
  id: string;
  engagement: string;
  control: Brief;
  evidence: Brief | null;
  title: string;
  body: string;
  due: string;
  overdue: boolean;
  assignee_id: string;
  assignee: string;
  status: "open" | "answered" | "closed";
  response: string;
  response_url: string;
  response_evidence: Brief | null;
  requested_by: string;
  requested_at: string;
  answered_by: string;
  answered_at: string | null;
  closed_at: string | null;
  can_answer: boolean;
  can_close: boolean;
};

export type Severity = "low" | "medium" | "high" | "critical";
export type Finding = {
  id: string;
  engagement: string;
  control: Brief;
  severity: Severity;
  title: string;
  description: string;
  status: "open" | "plan_proposed" | "closed";
  action_plan: string;
  action_due: string;
  plan_by: string;
  plan_at: string | null;
  closure_note: string;
  opened_by: string;
  opened_at: string;
  closed_by: string;
  closed_at: string | null;
  can_plan: boolean;
  can_close: boolean;
};

export type AuditComment = { id: string; evidence: Brief | null; body: string; author: string; author_id: string; created_at: string };

export type AuditHome = {
  engagements: Engagement[];
  current: EngagementDetail | null;
  findings: Finding[];
  requests: AuditRequest[];
  mine: AuditRequest[];
  can_audit: boolean;
};

export type ControlAudit = {
  requests: AuditRequest[];
  comments: AuditComment[];
  findings: Finding[];
  samples: Sample[];
  engagements: Engagement[];
  can: { audit: boolean; respond: boolean };
};

const q = encodeURIComponent;
const B = "/grc/audit";

export const auditApi = {
  home: (engagement = "") => call<AuditHome>("GET", `${B}/home${engagement ? `?engagement=${q(engagement)}` : ""}`),
  create: (b: EngagementInput) => call<EngagementDetail>("POST", `${B}/engagements`, b),
  close: (id: string) => call<EngagementDetail>("POST", `${B}/engagements/${id}/close`),
  packUrl: (id: string) => `/api/v1${B}/engagements/${id}/pack`,
  review: (id: string, control: string, conclusion: string, note: string) =>
    call<EngagementDetail>("PUT", `${B}/engagements/${id}/controls/${q(control)}/review`, { conclusion, note }),
  draw: (id: string, control: string, size: number, seed?: number) =>
    call<Sample>("POST", `${B}/engagements/${id}/samples`, { control, size, ...(seed === undefined ? {} : { seed }) }),
  sample: (id: string) => call<Sample>("GET", `${B}/samples/${id}`),
  assess: (id: string, pos: number, outcome: Outcome, note: string) => call<Sample>("PUT", `${B}/samples/${id}/items/${pos}`, { outcome, note }),
  request: (id: string, b: { control: string; title: string; body: string; due: string; evidence?: string }) =>
    call<AuditRequest>("POST", `${B}/engagements/${id}/requests`, b),
  answer: (id: string, b: { response: string; url: string; evidence: string }) => call<AuditRequest>("POST", `${B}/requests/${id}/answer`, b),
  closeRequest: (id: string) => call<AuditRequest>("POST", `${B}/requests/${id}/close`),
  mine: () => call<{ items: AuditRequest[] }>("GET", `${B}/requests/mine`),
  openFinding: (id: string, b: { control: string; severity: Severity; title: string; description: string }) =>
    call<Finding>("POST", `${B}/engagements/${id}/findings`, b),
  plan: (id: string, b: { action_plan: string; action_due: string }) => call<Finding>("POST", `${B}/findings/${id}/plan`, b),
  closeFinding: (id: string, note: string) => call<Finding>("POST", `${B}/findings/${id}/close`, { note }),
  control: (ref: string) => call<ControlAudit>("GET", `/grc/controls/${q(ref)}/audit`),
  comment: (ref: string, b: { body: string; evidence?: string; engagement?: string }) =>
    call<{ items: AuditComment[] }>("POST", `/grc/controls/${q(ref)}/audit/comments`, b),
};
