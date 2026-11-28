// Modello della continuità operativa (docs/sviluppo/continuita.md): i tipi del contratto delle
// API e le funzioni pure (minuti, proposta della BIA, frasi delle incoerenze, raggruppamenti)
// usate dalle pagine e provate da web/test/continuita.test.ts. Nessuna chiamata al server qui.
import { lookup, tr } from "./i18n/core.ts";

export type Ref = { id: string; key: string; title: string };
export type Category = { key: string; name: string; intolerable: number; levels: { value: number; label: string }[] };
export type Horizon = { key: string; name: string; minutes: number };
export type TestType = { key: string; name: string; description: string };
export type Config = {
  categories: Category[];
  horizons: Horizon[];
  test_types: TestType[];
  test_max_age_days: number;
  bia_review_months: number;
  critical_mtpd_minutes: number;
};

export type IssueCode =
  | "rto_over_required"
  | "rpo_over_required"
  | "rto_missing"
  | "dependency_slower"
  | "dependency_rto_missing"
  | "critical_not_declared"
  | "plan_missing"
  | "test_missing"
  | "test_failed"
  | "bia_missing"
  | "bia_review_due";
export const ISSUE_CODES: IssueCode[] = [
  "rto_over_required",
  "rpo_over_required",
  "rto_missing",
  "dependency_slower",
  "dependency_rto_missing",
  "critical_not_declared",
  "plan_missing",
  "test_missing",
  "test_failed",
  "bia_missing",
  "bia_review_due",
];

export type Severity = "high" | "medium";
export type ActionKind = "set_rto" | "set_rpo" | "set_critical" | "set_provider_rto" | "write_plan" | "plan_test" | "start_bia" | "review_bia" | "open_test";
export type Issue = {
  code: string;
  ref: string;
  severity: Severity;
  application: Ref | null;
  capability: Ref | null;
  other: (Ref & { kind: "application" | "provider" }) | null;
  declared: number | null;
  required: number | null;
  other_value: number | null;
  days: number | null;
  source: Ref | null;
  team: { id: string; name: string } | null;
  action: { kind: ActionKind; target: string };
};
export type Can = { write: boolean; approve: boolean; admin: boolean };

export type Settings = { ready: boolean; version: number; config: Config; updated_by: string | null; updated_at: string | null; can: Can };
export type SettingsVersion = { version: number; by: string | null; at: string; config: Config };

export type Summary = { issues: number; high: number; critical_functions: number; bia_approved: number; bia_missing: number; tests_due: number; plans_missing: number };
export type Overview = { ready: boolean; summary: Summary; issues: Issue[]; can: Can };
export type OverviewFilter = { capability?: string; team?: string; code?: string };

export type BiaBrief = {
  id: string;
  key: string;
  state: string;
  state_category: string;
  critical: boolean | null;
  mtpd_minutes: number | null;
  rto_minutes: number | null;
  rpo_minutes: number | null;
  approved_version: number | null;
  approved_at: string | null;
  review_due: string | null;
};
export type BiaListItem = {
  capability: Ref & { code: string | null; parent_id: string | null; depth: number };
  bia: BiaBrief | null;
  applications: number;
  critical_applications: number;
};

/** Impatti: categoria → orizzonte → livello. */
export type Impacts = Record<string, Record<string, number>>;
export type Proposal = { mtpd_minutes: number | null; rto_minutes: number | null; critical: boolean; category: string | null; horizon: string | null };
export type BiaVersion = { version: number; at: string; by: string | null; mtpd_minutes: number | null; rto_minutes: number | null; rpo_minutes: number | null; critical: boolean; settings_version: number };
export type BiaView = {
  id: string;
  key: string;
  title: string;
  state: string;
  state_category: string;
  capability: Ref;
  impacts: Impacts;
  critical: boolean | null;
  critical_reason: string;
  mtpd_minutes: number | null;
  rto_minutes: number | null;
  rpo_minutes: number | null;
  notes: string;
  proposal: Proposal | null;
  settings_version: number;
  config: Config;
  config_changed: boolean;
  approved: { version: number; at: string; by: string | null; mtpd_minutes: number | null; rto_minutes: number | null; rpo_minutes: number | null; critical: boolean } | null;
  versions: BiaVersion[];
  applications: (Ref & { rto_minutes: number | null; rpo_minutes: number | null; rto_ok: boolean | null; rpo_ok: boolean | null })[];
  can: { edit: boolean; approve: boolean };
};
export type BiaInput = Partial<{
  impacts: Impacts;
  critical: boolean | null;
  critical_reason: string;
  mtpd_minutes: number | null;
  rto_minutes: number | null;
  rpo_minutes: number | null;
  notes: string;
}>;

export type PlanRef = { page: Ref; target: Ref & { type: string }; approved: boolean; approved_version: number | null };
export type Outcome = "passed" | "passed_with_findings" | "failed";
export const OUTCOMES: Outcome[] = ["passed", "passed_with_findings", "failed"];
export type TestBrief = {
  id: string;
  key: string;
  title: string;
  state: string;
  state_category: string;
  type: string;
  planned_on: string | null;
  executed_on: string | null;
  outcome: Outcome | null;
};
export type Subject = Ref & { kind: "application" | "capability" };
export type TestPlanRow = { subject: Subject; critical: boolean; required_rto: number | null; tests: TestBrief[]; last_done: TestBrief | null; planned_in_year: boolean };
export type TestPlan = { year: number; rows: TestPlanRow[]; types: TestType[]; can: Can };
export type Finding = { text: string; ref: string; work: { key: string; title: string; state: string } | null };
export type TestView = TestBrief & {
  subject: Subject;
  type_name: string;
  rto_measured_minutes: number | null;
  rpo_measured_minutes: number | null;
  notes: string;
  findings: Finding[];
  required: { rto: number | null; rpo: number | null };
  declared: { rto: number | null; rpo: number | null };
  rto_ok: boolean | null;
  rpo_ok: boolean | null;
  evidence: { key: string } | null;
  evidence_note: string;
  can: { edit: boolean };
};
export type TestInput = Partial<{
  type: string;
  planned_on: string | null;
  executed_on: string | null;
  rto_measured_minutes: number | null;
  rpo_measured_minutes: number | null;
  outcome: Outcome | null;
  findings: string[];
  notes: string;
}>;

export type Dependency = Ref & { kind: "application" | "provider"; rto_minutes: number | null; slower: boolean };
export type ApplicationContinuity = {
  application: Ref;
  declared: { rto: number | null; rpo: number | null };
  required: { rto: number | null; rpo: number | null; source: Ref | null };
  critical_declared: boolean;
  critical_required: boolean;
  dependencies: Dependency[];
  plans: PlanRef[];
  last_test: TestBrief | null;
  issues: Issue[];
  can: Can;
};
export type CapabilityContinuity = {
  capability: Ref;
  bia: BiaBrief | null;
  applications: (Ref & { rto_minutes: number | null; rpo_minutes: number | null })[];
  plans: PlanRef[];
  last_test: TestBrief | null;
  issues: Issue[];
  can: Can;
};
export type ContinuityGraph = {
  nodes: Record<string, { rto_declared: number | null; rto_required: number | null; critical: boolean }>;
  edges: { dependent: string; dependency: string; incoherent: boolean }[];
};

// ---------------------------------------------------------------------------------------------
// Funzioni pure

const DAY = 1440;

/** Minuti in forma leggibile: 90 → «1 h 30 min», 1440 → «1 g», null → «—». */
export function fmtMinutes(m: number | null | undefined): string {
  if (m == null || !Number.isFinite(m)) return "—";
  let rest = Math.max(0, Math.round(m));
  if (rest === 0) return tr("continuita.fmt.min", { n: 0 });
  const d = Math.floor(rest / DAY);
  rest -= d * DAY;
  const h = Math.floor(rest / 60);
  const min = rest - h * 60;
  const parts: string[] = [];
  if (d) parts.push(tr("continuita.fmt.days", { n: d }));
  if (h) parts.push(tr("continuita.fmt.hours", { n: h }));
  if (min) parts.push(tr("continuita.fmt.min", { n: min }));
  return parts.join(" ");
}

export type DurationUnit = "min" | "h" | "d";
const UNIT_MIN: Record<DurationUnit, number> = { min: 1, h: 60, d: DAY };

/** Minuti → valore e unità più comodi da scrivere (la più grande che divide esattamente). */
export function splitMinutes(m: number | null | undefined): { value: string; unit: DurationUnit } {
  if (m == null) return { value: "", unit: "h" };
  if (m > 0 && m % DAY === 0) return { value: String(m / DAY), unit: "d" };
  if (m % 60 === 0) return { value: String(m / 60), unit: "h" };
  return { value: String(m), unit: "min" };
}

/** Valore scritto in un'unità → minuti interi (vuoto o non valido → null). Accetta la virgola decimale. */
export function toMinutes(value: string, unit: DurationUnit): number | null {
  const v = value.trim().replace(",", ".");
  if (v === "") return null;
  const n = Number(v);
  if (!Number.isFinite(n) || n < 0) return null;
  return Math.round(n * UNIT_MIN[unit]);
}

/** Proposta della BIA (stessa regola del backend) dagli impatti compilati. */
export function proposeBia(config: Pick<Config, "categories" | "horizons" | "critical_mtpd_minutes">, impacts: Impacts): Proposal {
  const hs = [...config.horizons].sort((a, b) => a.minutes - b.minutes);
  if (hs.length === 0) return { mtpd_minutes: null, rto_minutes: null, critical: false, category: null, horizon: null };
  for (let i = 0; i < hs.length; i++) {
    const h = hs[i];
    for (const c of config.categories) {
      const lvl = impacts[c.key]?.[h.key];
      if (lvl != null && lvl >= c.intolerable) {
        const rto = i > 0 ? hs[i - 1].minutes : Math.floor(h.minutes / 2);
        return { mtpd_minutes: h.minutes, rto_minutes: rto, critical: h.minutes <= config.critical_mtpd_minutes, category: c.key, horizon: h.key };
      }
    }
  }
  return { mtpd_minutes: null, rto_minutes: hs[hs.length - 1].minutes, critical: false, category: null, horizon: null };
}

/** La griglia è piena: ogni categoria ha un livello per ogni orizzonte. */
export function gridComplete(config: Pick<Config, "categories" | "horizons">, impacts: Impacts): boolean {
  return config.categories.every((c) => config.horizons.every((h) => impacts[c.key]?.[h.key] != null));
}

/** Incoerenze raggruppate per gravità (prima le alte), nell'ordine in cui arrivano; i gruppi vuoti non ci sono. */
export function groupBySeverity<T extends { severity: Severity }>(items: T[]): { severity: Severity; items: T[] }[] {
  const out: { severity: Severity; items: T[] }[] = [];
  for (const s of ["high", "medium"] as Severity[]) {
    const g = items.filter((i) => i.severity === s);
    if (g.length) out.push({ severity: s, items: g });
  }
  return out;
}

/** La frase di un'incoerenza, nella lingua corrente; un code sconosciuto dà il code stesso. */
export function issueText(i: Issue): string {
  const subject = i.application?.title ?? i.capability?.title ?? "";
  const p = {
    app: i.application?.title ?? subject,
    cap: i.capability?.title ?? subject,
    subject,
    other: i.other?.title ?? "",
    // Per le dipendenze conta l'RTO effettivo dell'applicazione: il dichiarato, o il richiesto se manca.
    declared: fmtMinutes(i.code.startsWith("dependency_") ? (i.declared ?? i.required) : i.declared),
    required: fmtMinutes(i.required),
    other_value: fmtMinutes(i.other_value),
    days: i.days ?? 0,
    source: i.source?.title ?? i.capability?.title ?? "",
  };
  let key = `continuita.issue.${i.code}`;
  if (i.code === "test_missing" && i.days != null) key = "continuita.issue.test_missing_days";
  if ((i.code === "rto_over_required" || i.code === "rpo_over_required" || i.code === "rto_missing") && !p.source) key += "_nosource";
  return lookup(key, p) ?? i.code;
}

export type ActionLink =
  | { to: "/e/$key"; params: { key: string }; search?: undefined }
  | { to: "/documenti"; params?: undefined; search?: undefined }
  | { to: "/architettura/continuita/test"; params?: undefined; search: { anno?: number; riga?: string } }
  | { to: "/architettura/continuita/bia"; params?: undefined; search: { capability?: string } };

/** Dove porta l'azione diretta di un'incoerenza. */
export function actionLink(a: Issue["action"], year = new Date().getFullYear()): ActionLink {
  switch (a.kind) {
    case "write_plan":
      return { to: "/documenti" };
    case "plan_test":
      return { to: "/architettura/continuita/test", search: { anno: year, riga: a.target } };
    case "start_bia":
      return { to: "/architettura/continuita/bia", search: { capability: a.target } };
    default:
      // set_rto, set_rpo, set_critical (applicazione), set_provider_rto (fornitore), review_bia, open_test.
      return { to: "/e/$key", params: { key: a.target } };
  }
}

/** Esito del confronto fra un valore e il suo limite (minore o uguale è ok). */
export function withinLimit(value: number | null | undefined, limit: number | null | undefined): boolean | null {
  if (value == null || limit == null) return null;
  return value <= limit;
}

/** Mese (0-11) in cui cade un test nell'anno: esecuzione, altrimenti pianificazione; null se fuori dall'anno. */
export function testMonth(t: Pick<TestBrief, "planned_on" | "executed_on">, year: number): number | null {
  const d = t.executed_on ?? t.planned_on;
  if (!d || !d.startsWith(`${year}-`)) return null;
  const m = Number(d.slice(5, 7));
  return m >= 1 && m <= 12 ? m - 1 : null;
}

/** Permessi della continuità dall'elenco dei permessi della persona. */
export function continuityCan(perms: string[]): Can {
  const has = (p: string) => perms.includes("*") || perms.includes(p);
  return { write: has("continuity.write"), approve: has("continuity.approve"), admin: has("continuity.admin") || has("tenant.admin") };
}
