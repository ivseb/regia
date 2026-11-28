// API della vista di crisi dell'incidente e del «dal problema all'azione» (replica/usabilita.md,
// proposte 3 e 5). Modulo a parte, caricato con le pagine che lo usano.
import { call } from "./api";
import type { IncidentEvent, IncidentView, Notification, Ref } from "./riskApi";

const q = encodeURIComponent;

export type CrisisRelease = { version: string; service: string; environment: string; production: boolean; at: string; suspect: boolean };
export type CrisisApp = {
  ref: Ref;
  criticality: string | null;
  rto_minutes: number | null;
  team: string | null;
  last_release: CrisisRelease | null;
  vulns: { open: number; overdue: number; critical: number };
  risks: Ref[];
  other_incidents: Ref[];
  runbooks: Ref[];
  dependents: Ref[];
};
export type CrisisProvider = { ref: Ref; cause: boolean; critical: boolean; next_end: string | null; applications: string[] };
export type NextKind = "classify" | "notify" | "contain" | "update" | "resolve" | "close" | "postmortem" | "none";
export type NextAction = { kind: NextKind; due_at: string | null; notification?: string };
export type TimelineItem = { at: string; kind: string; title: string; detail: string; ref?: string; future: boolean; system: boolean; by: string | null };
export type DraftField = { key: string; kind: "text" | "date" | "number" | "minutes" | "money" | "list"; value: string; missing: boolean; required: boolean };
export type CrisisView = {
  incident: IncidentView;
  next: NextAction;
  applications: CrisisApp[];
  providers: CrisisProvider[];
  timeline: TimelineItem[];
  decisions: IncidentEvent[];
  communications: IncidentEvent[];
  draft: { notification: Notification | null; fields: DraftField[]; missing: number };
  stale_hours: number;
};

export type ProblemKind = "finding" | "gate_decision" | "requirement" | "adherence" | "continuity";
export type ProblemLink = {
  id: string;
  action: "work_item" | "exception" | "override" | "adoption_pr";
  derived: boolean;
  ref: string;
  label: string;
  status: string;
  url?: string;
  at: string | null;
  by: string | null;
  element?: string;
};
export type ProblemContext = {
  kind: ProblemKind;
  ref: string;
  label: string;
  application: { id: string; key: string; title: string } | null;
  team: { id: string; name: string; has_space: boolean } | null;
  title: string;
  body: string;
  reason: string;
  extra: Record<string, string>;
  links: ProblemLink[];
  can: { work_item: boolean };
};
export type ProblemOrigin = {
  id: string;
  kind: ProblemKind;
  ref: string;
  label: string;
  at: string | null;
  by: string | null;
  application: { id: string; key: string; title: string } | null;
};
export type WorkBody = { kind: ProblemKind; ref: string; control?: string; title?: string; body_text?: string; team_id?: string; assignee_id?: string; due?: string };

export const crisisApi = {
  crisis: (ref: string) => call<CrisisView>("GET", `/incidents/${q(ref)}/crisis`),
};

export const problemApi = {
  context: (kind: ProblemKind, ref: string, control = "") => call<ProblemContext>("GET", `/problems/context?kind=${q(kind)}&ref=${q(ref)}${control ? `&control=${q(control)}` : ""}`),
  createWork: (b: WorkBody) => call<ProblemLink>("POST", "/problems/work-items", b),
  addLink: (b: { kind: ProblemKind; ref: string; action: "exception" | "override" | "adoption_pr"; target_ref: string; target_label?: string; note?: string }) =>
    call<ProblemLink>("POST", "/problems/links", b),
  origins: (ref: string) => call<{ items: ProblemOrigin[] }>("GET", `/elements/${q(ref)}/problems`),
};
