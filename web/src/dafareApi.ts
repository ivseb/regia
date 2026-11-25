// Client di «Da fare» (ondata 2, proposta 1): la pagina unica con decisioni, valutazioni, verifiche,
// impatti del team, tempo e menzioni, e il numero per il campanello. Le azioni usano le API già
// esistenti (decisione del passo, passaggio di fase, stima dell'impatto, timesheet).
import { call, type InboxItem, type Notification } from "./api";

const q = encodeURIComponent;

export type TodoDecision = InboxItem & {
  subject: string;
  type: string;
  body?: string;
  is_request: boolean;
  score: number | null;
  position: number | null;
  rank_total: number;
  funded: boolean | null;
  cumulative_cents: number | null;
  budget_cents: number | null;
  mandatory: boolean;
  reviews_open: number;
  reviews_mandatory_open: number;
  requirements_to_accept: number;
};

export type TodoReview = {
  id: string;
  key: string;
  title: string;
  role: "compile" | "team";
  type_name: string;
  team: string;
  request_key: string;
  request_title: string;
  application: string | null;
  stage: string;
  mandatory: boolean;
  due_on: string | null;
  late_days: number;
  required_total: number;
  required_missing: number;
};

export type TodoVerify = {
  requirement_key: string;
  requirement_id: string;
  title: string;
  category: string;
  impact_key: string;
  application: string;
  team: string;
  request_key: string;
  request_title: string;
  implemented_by: string | null;
  since: string;
};

export type TodoTransition = { to: string; label: string; forward: boolean; comment_required: boolean; blocked: string[] };

export type TodoImpact = {
  id: string;
  key: string;
  title: string;
  state: string;
  request_key: string;
  request_title: string;
  application: string;
  change_kind: string;
  team_id: string | null;
  team: string | null;
  effort_days: number | null;
  from: string | null;
  to: string | null;
  can_estimate: boolean;
  transitions: TodoTransition[];
  capacity_days: number | null;
  load_days: number | null;
  free_days: number | null;
  weeks: number;
};

export type TodoWeek = { kind: "submit" | "approve"; week: string; minutes: number; member_id?: string; member?: string };
export type TodoReport = { id: string; key: string; title: string; due: string; late: boolean; draft: boolean };
export type TodoCounts = {
  decisions: number;
  reviews: number;
  verifications: number;
  impacts: number;
  time: number;
  reports: number;
  mentions: number;
  total: number;
  more: boolean;
};

export type Todo = {
  counts: TodoCounts;
  decisions: TodoDecision[];
  reviews: TodoReview[];
  verifications: TodoVerify[];
  impacts: TodoImpact[];
  time: TodoWeek[];
  reports: TodoReport[];
  mentions: Notification[];
  next_meeting: { at: string; body: string; title: string } | null;
  silenced: string[];
  can_time: boolean;
};

export type Decision = "approved" | "approved_with_conditions" | "deferred" | "rejected";

export const dafareApi = {
  todo: () => call<Todo>("GET", "/todo"),
  count: () => call<TodoCounts>("GET", "/todo/count"),
  decide: (stepId: string, decision: Decision, text: string) =>
    call<void>("POST", `/approvals/steps/${q(stepId)}/decision`, {
      decision,
      comment: decision === "approved_with_conditions" ? undefined : text || undefined,
      conditions: decision === "approved_with_conditions" ? text : undefined,
    }),
  silenced: () => call<{ items: string[] }>("GET", "/me/silenced"),
  silence: (ref: string, on: boolean) => call<void>(on ? "PUT" : "DELETE", `/me/silenced/${q(ref)}`),
};
