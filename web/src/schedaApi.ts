// API delle sintesi di scheda (replica/usabilita.md, proposta 2): la richiesta «decisione prima» e la
// vista 360 dell'applicazione. Una sola richiesta per scheda: il server compone i numeri con le stesse
// funzioni delle pagine che li mostrano per esteso. Un blocco che manca (null) non è leggibile dalla
// persona o appartiene a un modulo spento.
import { call } from "./api";

const q = encodeURIComponent;

/** Cosa manca o richiede un'azione: il testo lo sceglie il client dal `kind`. */
export type SummaryAction = {
  kind: string;
  count?: number;
  detail?: string;
  who?: string;
  tab?: string;
  mine?: boolean;
};

export type SumDecision = {
  request_id: string;
  stage: string;
  amount_cents: number | null;
  waiting: string[];
  steps_done: number;
  steps_all: number;
  can_decide: boolean;
  inline: boolean;
};

export type SumRankArea = {
  unit_id: string;
  unit: string;
  period_id?: string;
  period?: string;
  position: number;
  total: number;
  cut: number | null;
  funded: boolean | null;
  has_allocation: boolean;
  allocated_cents: number;
  residual_cents: number;
  fits_residual: boolean | null;
};

export type SumRank = {
  in_ranking: boolean;
  position: number;
  total: number;
  cut: number | null;
  funded: boolean | null;
  pinned: boolean;
  estimate_cents: number | null;
  cumulative_cents: number;
  budget_cents: number | null;
  value_per_cost: number | null;
  mandatory: boolean;
  area: SumRankArea | null;
};

export type SumReview = {
  key: string;
  title: string;
  type: string;
  team: string;
  stage: string;
  mandatory: boolean;
  required_missing: number;
  late_days: number;
  impact?: string;
};

export type SumRisk = { key: string; title: string; phase: string; band?: string; tone?: string; over_appetite: boolean; via?: string };

export type RequestSummary = {
  key: string;
  decision: SumDecision | null;
  score: { value: number | null; version: number | null; stale: boolean; scorecard?: string } | null;
  rank: SumRank | null;
  reviews: { total: number; open: number; mandatory_open: number; items: SumReview[] } | null;
  requirements: {
    total: number;
    to_accept: number;
    open: number;
    to_verify: number;
    verified: number;
    waived: number;
    expired_exceptions: number;
    ready: boolean;
    top: { key: string; title: string; status: string; application?: string }[];
  } | null;
  risks: SumRisk[];
  impacts: number;
  days_in_stage: number | null;
  missing: SummaryAction[];
};

export type App360Node = { key: string; code: string | null; name: string; criticality: string | null; via: string };
export type App360 = {
  key: string;
  id: string;
  owners: { role: string; name: string }[];
  team: string | null;
  team_lead: string | null;
  actions: SummaryAction[];
  deps: { dependencies: App360Node[]; dependents: App360Node[] } | null;
  requests: { key: string; title: string; state: string; state_category: string; request_key: string; request_title: string; request_state: string; request_category: string }[];
  requests_open: number;
  vulns: {
    open: number;
    overdue: number;
    soon: number;
    critical_open: number;
    critical_prod: number;
    exceptions: number;
    items: { id: string; cve: string; severity: string; package: string | null; due_at: string | null; days_left: number | null; overdue: boolean }[];
  } | null;
  releases: {
    services: number;
    on_pipeline: number;
    stale: number;
    unscanned: number;
    latest: { service: string; environment: string; production: boolean; version: string; at: string; checks_status: string; request_key: string | null }[];
  } | null;
  gate: {
    configured: boolean;
    cells: { environment: string; production: boolean; result: string; mode: string; version?: string; failing_reasons: number }[];
    failing: number;
    overrides_pending: number;
  } | null;
  pipeline: { configured: boolean; status: string; class: string; missing: string[]; services: number } | null;
  compliance: {
    applicable: number;
    covered: number;
    percent: number;
    gaps: number;
    frameworks: { key: string; name: string; worst: string; percent: number; gaps: number }[];
    controls: { key: string; title: string; effectiveness: string; valid: boolean }[];
    controls_invalid: number;
  } | null;
  risks: SumRisk[];
  risks_open: number;
  incidents: { key: string; title: string; state: string; state_category: string; detected_at: string; major: boolean }[];
  incidents_open: number;
  documents: { key: string; title: string; space: string; controlled: boolean }[];
  has_risks: boolean;
  has_incidents: boolean;
  has_documents: boolean;
  has_compliance: boolean;
  has_devsecops: boolean;
  has_architecture: boolean;
};

export const schedaApi = {
  request: (ref: string) => call<RequestSummary>("GET", `/elements/${q(ref)}/summary`),
  application: (ref: string) => call<App360>("GET", `/architecture/applications/${q(ref)}/360`),
};
