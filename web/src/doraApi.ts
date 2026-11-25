// API delle metriche DORA delle quattro chiavi (DevSecOps, fase 5a; replica/devsecops.md §3.6).
// Modulo a parte, caricato con la pagina «Metriche di consegna»: il bundle iniziale non cresce.
import { call } from "./api";

const q = encodeURIComponent;

export type Period = "week" | "month" | "quarter";
export type Scope = "tenant" | "team" | "application" | "asset" | "service";
export type Level = "low" | "medium" | "high" | "elite";
export type MetricKey = "deploy_frequency" | "lead_time_days" | "change_failure_rate" | "mttr_hours";

export type Ladder = { elite: number; high: number; medium: number };
export type SeriesPt = { at: string; value: number | null; level?: Level; partial?: boolean };
export type MetricCard = {
  key: MetricKey;
  name: string;
  unit: string;
  higher_better: boolean;
  hint: string;
  value: number | null;
  prev: number | null;
  level: Level | "";
  prev_level: Level | "";
  n: number;
  series: SeriesPt[];
  ladder: Ladder;
};
export type CompareRow = {
  id: string;
  name: string;
  values: Partial<Record<MetricKey, number | null>>;
  levels: Partial<Record<MetricKey, Level>>;
  level: Level | "";
  deploys: number;
  attempts: number;
};
export type SubjectRef = { scope: Scope; id: string; name: string };
export type DoraSettings = {
  failure_window_hours: number;
  hotfix_words: string[];
  lead_time_start: "auto" | "check_run" | "change" | "request";
  thresholds: Record<MetricKey, Ladder>;
  fact_days: number;
  updated_at: string | null;
  default: boolean;
};
export type Dashboard = {
  period: Period;
  subject: SubjectRef;
  bucket: string;
  bucket_end: string;
  current: string;
  metrics: MetricCard[];
  level: Level | "";
  prev_level: Level | "";
  compare: CompareRow[];
  compare_scope: string;
  subjects: SubjectRef[];
  computed: boolean;
  open_incidents?: number;
  computed_at: string | null;
  settings: DoraSettings;
  can_configure: boolean;
};

export type FailReason = { kind: "status" | "incident" | "rollback" | "hotfix" | "linked"; text: string; ref?: string };
export type DrillDeploy = {
  id: string;
  service: string;
  application_id: string | null;
  version: string;
  at: string;
  status: string;
  commit: string | null;
  change: string | null;
  release_url: string | null;
  lead_start: string | null;
  lead_source?: "check_run" | "change" | "request";
  lead_days: number | null;
  failed: boolean;
  reasons: FailReason[];
};
export type DrillIncident = {
  id: string;
  key: string;
  title: string;
  detected_at: string;
  resolved_at: string | null;
  hours: number | null;
  applications: string[];
};
export type Drill = {
  subject: SubjectRef;
  period: Period;
  start: string;
  end: string;
  values: {
    deploys: number;
    attempts: number;
    failed: number;
    frequency: number;
    lead_time_days: number | null;
    lead_time_n: number;
    change_failure_rate: number | null;
    mttr_hours: number | null;
    mttr_n: number;
    partial: boolean;
  };
  levels: Partial<Record<MetricKey, Level>>;
  deploys: DrillDeploy[];
  incidents: DrillIncident[];
};

export type PreviewRow = {
  scope: Scope;
  id: string;
  name: string;
  current: Partial<Record<MetricKey, number | null>>;
  proposed: Partial<Record<MetricKey, number | null>>;
  current_levels: Partial<Record<MetricKey, Level>>;
  proposed_levels: Partial<Record<MetricKey, Level>>;
  current_level: Level | "";
  proposed_level: Level | "";
  changed: boolean;
};

type Query = { period: Period; scope: Scope; subject?: string; compare?: Scope | "" };

export const doraApi = {
  dashboard: ({ period, scope, subject, compare }: Query) =>
    call<Dashboard>("GET", `/devsecops/dora?period=${period}&scope=${scope}${subject ? `&subject=${q(subject)}` : ""}${compare ? `&compare=${compare}` : ""}`),
  drill: (period: Period, scope: Scope, subject: string | undefined, at: string) =>
    call<Drill>("GET", `/devsecops/dora/drilldown?period=${period}&scope=${scope}${subject ? `&subject=${q(subject)}` : ""}&at=${q(at)}`),
  settings: () => call<DoraSettings>("GET", "/devsecops/dora/settings"),
  saveSettings: (s: Omit<DoraSettings, "updated_at" | "default">) => call<DoraSettings>("PUT", "/devsecops/dora/settings", s),
  preview: (period: Period, settings: Omit<DoraSettings, "updated_at" | "default">) =>
    call<{ items: PreviewRow[] }>("POST", "/devsecops/dora/preview", { period, settings }),
  recompute: () => call<{ points: number; deploys: number; incidents: number }>("POST", "/devsecops/dora/recompute"),
};
