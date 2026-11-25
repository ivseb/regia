// API delle vulnerabilità governate (DevSecOps, fase 2; replica/devsecops.md §3.2): cruscotto,
// elenco con azioni in blocco, eccezioni, politica di SLA e di escalation con l'anteprima.
// Modulo a parte, caricato con le sue pagine: il bundle iniziale non cresce.
import { call } from "./api";
import { tr } from "./i18n/core";

const q = encodeURIComponent;

export type Sev = "critical" | "high" | "medium" | "low";
export type Crit = "critical" | "high" | "medium" | "low" | "none";
export const SEVERITIES: Sev[] = ["critical", "high", "medium", "low"];
export const CRITICALITIES: Crit[] = ["critical", "high", "medium", "low", "none"];
export const SEV_NAME: Record<string, string> = { get critical() { return tr("governance.vulnapi.critica"); }, get high() { return tr("governance.vulnapi.alta"); }, get medium() { return tr("governance.vulnapi.media"); }, get low() { return tr("governance.vulnapi.bassa"); }, get info() { return tr("governance.vulnapi.info"); } };
export const CRIT_NAME: Record<Crit, string> = { get critical() { return tr("governance.vulnapi.critica"); }, get high() { return tr("governance.vulnapi.alta"); }, get medium() { return tr("governance.vulnapi.media"); }, get low() { return tr("governance.vulnapi.bassa"); }, get none() { return tr("governance.vulnapi.non_classificata"); } };
export const STAGE_NAME: Record<string, string> = {
  get open() { return tr("governance.vulnapi.aperta"); },
  get taken() { return tr("governance.vulnerabilities.presa_in_carico"); },
  get in_progress() { return tr("governance.vulnapi.in_correzione"); },
  get exception() { return tr("governance.vulnapi.eccezione"); },
  get resolved() { return tr("governance.vulnapi.risolta"); },
  get closed() { return tr("governance.vulnapi.chiusa_automaticamente"); },
  get false_positive() { return tr("governance.vulnapi.falso_positivo"); },
};

export type Esc = { name: string; days: number; to: string[] };
export type Policy = {
  matrix: Record<Sev, Record<Crit, number | null>>;
  dora_percent: number;
  exposed_percent: number;
  exposure_field: string;
  exposure_values: string[];
  soon_days: number;
  auto_assign: boolean;
  exception_notice_days: number;
  max_exception_days: number;
  escalation: Esc[];
  updated_at: string | null;
  default: boolean;
};

export type Rule = { when: { risk_lte?: number; risk_gt?: number }; steps: { approvers: { role?: string; body?: string; member?: string }[] }[] };
export type IDName = { id: string; key?: string; name: string };
export type Config = {
  policy: Policy;
  exception_policy: { id: string; rules: Rule[] };
  teams: IDName[];
  groups: IDName[];
  members: IDName[];
  roles: IDName[];
  bodies: IDName[];
  fields: IDName[];
  can_edit: boolean;
};

export type PreviewCell = { severity: Sev; criticality: Crit; open: number; overdue: number; soon: number };
export type Totals = { open: number; no_sla: number; overdue: number; soon: number };
export type Preview = { cells: PreviewCell[]; proposed: Totals; current: Totals; apps: { key: string; name: string; overdue: number; now: number }[] };

export type Filters = {
  team?: string;
  application?: string;
  severity?: string;
  stage?: string;
  due?: string;
  q?: string;
  mine?: boolean;
  sort?: string;
  limit?: number;
  offset?: number;
};

export type Item = {
  id: string;
  cve: string;
  severity: Sev | "info";
  package: string | null;
  version: string | null;
  fixed_in: string | null;
  layer: string | null;
  url: string | null;
  stage: string;
  stage_name: string;
  first_seen: string;
  due_at: string | null;
  sla_days: number | null;
  days_left: number | null;
  overdue: boolean;
  service_id: string | null;
  service: string | null;
  application_id: string | null;
  application_key: string | null;
  application: string | null;
  criticality: string | null;
  dora: boolean;
  team_id: string | null;
  team: string | null;
  team_manual: boolean;
  assignee_id: string | null;
  assignee: string | null;
  exception: string | null;
  exception_status: string | null;
  exception_until: string | null;
  closed_at: string | null;
  closed_reason: string | null;
};

export type VEvent = { at: string; kind: string; from: string | null; to: string | null; note: string; actor: string | null };
export type Exception = {
  id: string;
  key: string;
  application_id: string;
  application_key: string;
  application: string;
  reason: string;
  compensating: string;
  until: string;
  days_left: number;
  status: "pending" | "approved" | "rejected" | "expired" | "revoked";
  risk_score: number;
  risk_key: string | null;
  risk_title: string | null;
  findings: number;
  worst: string;
  requested_by: string | null;
  requested_at: string;
  decided_at: string | null;
  waiting: string[];
};
export type Detail = Item & { aliases: string[]; events: VEvent[]; exceptions: Exception[]; others: Item[] };

export type KPIs = {
  open: number;
  overdue: number;
  soon: number;
  no_sla: number;
  exceptions: number;
  exceptions_pending: number;
  resolved_30d: number;
  mttr_days: number | null;
  avg_age_days: number | null;
  soon_days: number;
  critical_open: number;
  critical_prod?: number;
};
export type Aging = { severity: Sev; buckets: [number, number, number, number]; overdue: number; total: number };
export type Group = {
  id: string;
  key?: string;
  name: string;
  team?: string | null;
  criticality?: string | null;
  dora?: boolean;
  open: number;
  overdue: number;
  soon: number;
  critical: number;
  exceptions: number;
  oldest_overdue_days: number;
};
export type TrendPoint = { at: string; open: number; overdue: number; opened: number; closed: number };
export type Dashboard = {
  kpis: KPIs;
  aging: Aging[];
  teams: Group[];
  applications: Group[];
  trend: TrendPoint[];
  filters: { teams: IDName[]; applications: IDName[] };
  can_act: boolean;
  can_configure: boolean;
};

export type BulkBody = { ids: string[]; action: string; team?: string; assignee?: string; note?: string };
export type BulkResult = { changed: number; skipped: number; notes: string[] };
export type ExceptionBody = { ids: string[]; reason: string; until: string; compensating?: string; risk?: string };

function qs(f: Filters): string {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(f)) {
    if (v === undefined || v === "" || v === false) continue;
    p.set(k, v === true ? "1" : String(v));
  }
  const s = p.toString();
  return s ? `?${s}` : "";
}

export const vulnApi = {
  dashboard: (f: Filters) => call<Dashboard>("GET", `/devsecops/vuln/dashboard${qs(f)}`),
  items: (f: Filters) => call<{ items: Item[]; total: number }>("GET", `/devsecops/vuln/items${qs(f)}`),
  item: (id: string) => call<Detail>("GET", `/devsecops/vuln/items/${q(id)}`),
  bulk: (b: BulkBody) => call<BulkResult>("POST", "/devsecops/vuln/bulk", b),
  exceptions: (status = "") => call<{ items: Exception[] }>("GET", `/devsecops/vuln/exceptions${status ? `?status=${q(status)}` : ""}`),
  requestException: (b: ExceptionBody) => call<{ items: Exception[] }>("POST", "/devsecops/vuln/exceptions", b),
  setRisk: (id: string, risk: string) => call<Exception>("PATCH", `/devsecops/vuln/exceptions/${q(id)}`, { risk }),
  revoke: (id: string, comment: string) => call<Exception>("POST", `/devsecops/vuln/exceptions/${q(id)}/revoke`, { comment }),
  risks: (text = "") => call<{ items: IDName[] }>("GET", `/devsecops/vuln/risks?q=${q(text)}`),
  config: () => call<Config>("GET", "/devsecops/vuln/config"),
  savePolicy: (p: Policy) => call<{ policy: Policy; recomputed: number }>("PUT", "/devsecops/vuln/config/policy", p),
  previewPolicy: (p: Policy) => call<Preview>("POST", "/devsecops/vuln/config/policy/preview", p),
  saveExceptionPolicy: (rules: Rule[]) => call<{ id: string; rules: Rule[] }>("PUT", "/devsecops/vuln/config/exception-policy", { rules }),
};

// ---------------------------------------------------------------------
// Utilità condivise dalle due pagine

/** Giorni effettivi di una casella con i fattori DORA ed esposizione (come il server: arrotondati, almeno 1). */
export function effectiveDays(base: number | null, dora: boolean, exposed: boolean, p: Pick<Policy, "dora_percent" | "exposed_percent">): number | null {
  if (base == null) return null;
  const v = base * (dora ? p.dora_percent / 100 : 1) * (exposed ? p.exposed_percent / 100 : 1);
  return Math.max(1, Math.round(v));
}
