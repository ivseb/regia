// Client di tempo e team (piano, passo 18; replica/work.md «Tempo e team»): timesheet
// settimanale, report del tempo, comandi in blocco, membri dei team con capacità e assenze.
import { call } from "./api";
import { loc } from "./i18n/format";
import { tr } from "./i18n/core";

const q = encodeURIComponent;

// ---------------------------------------------------------------------
// Timesheet

export type TSDay = { date: string; workday: boolean; holiday: boolean; absence: number; capacity: number; locked: boolean; future: boolean; total: number };
export type TSRow = {
  id: string;
  key: string;
  title: string;
  type: string;
  state: string;
  category: string;
  days: number[];
  total: number;
  estimate: number | null;
  logged: number;
  assigned: boolean;
};
export type TSSuggestion = { id: string; key: string; title: string; type: string; state: string; minutes: number };
export type TimeSettings = {
  require_approval: boolean;
  approver_mode: "team_lead" | "member" | "admin";
  approver_id: string | null;
  approver: string | null;
  locked_until: string | null;
  max_day_minutes: number;
  can_edit: boolean;
};
export type Absence = { id: string; member_id: string; member: string; from: string; to: string; kind: string; hours_per_day: number | null; note: string | null };
export type Timesheet = {
  member_id: string;
  member: string;
  mine: boolean;
  week: string;
  prev: string;
  next: string;
  days: TSDay[];
  rows: TSRow[];
  total: number;
  capacity: number;
  status: "draft" | "submitted" | "approved" | "rejected";
  comment: string | null;
  decided_by: string | null;
  submit_to: string[];
  editable: boolean;
  can_decide: boolean;
  assigned: TSSuggestion[];
  previous: TSSuggestion[];
  settings: TimeSettings;
  max_day: number;
  prev_total: number;
  weekly_hours: number;
  absences: Absence[];
};
export type PendingWeek = { member_id: string; member: string; week: string; minutes: number; submitted_at: string; capacity: number };
export type CellInput = { element: string; date: string; duration: string };

// ---------------------------------------------------------------------
// Report

export type ReportGroup = {
  key: string;
  label: string;
  minutes: number;
  estimate: number;
  capacity?: number;
  cost_cents?: number;
  children?: ReportGroup[];
};
export type TimeReport = {
  from: string;
  to: string;
  dim1: string;
  dim2: string;
  groups: ReportGroup[];
  total: ReportGroup;
  scope: "all" | "team" | "own";
  can_cost: boolean;
};
export type TimeFilter = { from: string; to: string; person?: string; team?: string; space?: string; epic?: string; request?: string; element?: string; week?: string; month?: string };
export type TimeRecord = {
  id: string;
  date: string;
  member_id: string;
  member: string;
  element_id: string;
  element_key: string;
  title: string;
  minutes: number;
  text: string | null;
  space: string;
  epic: string | null;
};
export type TimeRecords = { items: TimeRecord[]; total_minutes: number; count: number; truncated: boolean };

/** Dimensioni di raggruppamento del report: [chiave, etichetta nella lingua corrente]. */
export const dimList = (): [string, string][] => [
  ["person", tr("lavoro.tempo.dimPerson")],
  ["team", tr("lavoro.tempo.dimTeam")],
  ["space", tr("lavoro.tempo.dimSpace")],
  ["epic", tr("lavoro.tempo.dimEpic")],
  ["request", tr("lavoro.tempo.dimRequest")],
  ["element", tr("lavoro.tempo.dimElement")],
  ["week", tr("lavoro.tempo.dimWeek")],
  ["month", tr("lavoro.tempo.dimMonth")],
];

export function filterQuery(f: TimeFilter): string {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(f)) if (v) p.set(k, v);
  return p.toString();
}

// ---------------------------------------------------------------------
// Comandi in blocco

export type BulkCommand = {
  kind: "phase" | "assign" | "field" | "labels" | "sprint" | "delete";
  value?: string;
  field?: string;
  raw?: unknown;
  add?: string[];
  remove?: string[];
  comment?: string;
  label?: string;
};
export type BulkItem = { id: string; key: string; title: string; status: "ok" | "unchanged" | "blocked"; changes?: string[]; reason?: string };
export type BulkResult = { dry_run: boolean; applied: number; unchanged: number; blocked: number; items: BulkItem[]; summary: string; command_id?: string };
export type BulkContext = {
  members: { id: string; name: string }[];
  phases: string[];
  sprints: { id: string; key: string; title: string; state: string }[];
  fields: { key: string; name: string; data_type: string; multi: boolean; options?: { id: string; name: string }[] }[];
  labels: string[];
};
export type LastBulk = { id: string; summary: string; created_at: string; elements: number };
export type UndoResult = { summary: string; restored: number; skipped: BulkItem[] };

// ---------------------------------------------------------------------
// Team

export type TeamMember = {
  member_id: string;
  name: string;
  email: string | null;
  lead: boolean;
  role: string | null;
  allocation_pct: number;
  weekly_hours: number | null;
  person_hours: number;
  hours: number;
  absent_now: boolean;
};
export type TeamView = {
  id: string;
  name: string;
  lead_id: string | null;
  lead: string | null;
  space_key: string | null;
  members: TeamMember[];
  hours: number;
  capacity_days: number;
  can_manage: boolean;
};
export type TeamMemberInput = { member_id: string; lead?: boolean; role?: string; allocation_pct?: number; weekly_hours?: number; clear_hours?: boolean };
export type AbsenceInput = { member_id?: string; from: string; to?: string; kind?: string; hours_per_day?: number | null; note?: string };

// Etichette lette al momento dell'uso (getter): seguono la lingua corrente.
export const ABSENCE_KINDS: Record<string, string> = {
  get ferie() { return tr("lavoro.tempo.absVacation"); },
  get malattia() { return tr("lavoro.tempo.absSick"); },
  get permesso() { return tr("lavoro.tempo.absLeave"); },
  get altro() { return tr("lavoro.tempo.absOther"); },
};

export const tempoApi = {
  timesheet: (week?: string, member?: string) => {
    const p = new URLSearchParams();
    if (week) p.set("week", week);
    if (member) p.set("member", member);
    const s = p.toString();
    return call<Timesheet>("GET", `/work/timesheet${s ? "?" + s : ""}`);
  },
  setCells: (week: string, cells: CellInput[]) => call<Timesheet>("PUT", "/work/timesheet/cells", { week, cells }),
  copyWeek: (week: string) => call<{ copied: number; timesheet: Timesheet }>("POST", "/work/timesheet/copy", { week }),
  submit: (week: string) => call<Timesheet>("POST", "/work/timesheet/submit", { week }),
  withdraw: (week: string) => call<Timesheet>("POST", "/work/timesheet/withdraw", { week }),
  decide: (member: string, week: string, approve: boolean, comment?: string) =>
    call<Timesheet>("POST", "/work/timesheet/decide", { member, week, approve, comment: comment ?? "" }),
  reopen: (member: string, week: string) => call<void>("POST", "/work/timesheet/reopen", { member, week }),
  pending: () => call<{ items: PendingWeek[] }>("GET", "/work/timesheet/pending"),
  settings: () => call<TimeSettings>("GET", "/work/time/settings"),
  saveSettings: (s: Partial<{ require_approval: boolean; approver_mode: string; approver_id: string; locked_until: string; max_day_minutes: number }>) =>
    call<TimeSettings>("PUT", "/work/time/settings", s),

  report: (f: TimeFilter, group: string) => call<TimeReport>("GET", `/work/time/report?${filterQuery(f)}&group=${q(group)}`),
  reportCsvUrl: (f: TimeFilter, group: string) => `/api/v1/work/time/report?${filterQuery(f)}&group=${q(group)}&format=csv`,
  records: (f: TimeFilter) => call<TimeRecords>("GET", `/work/time/records?${filterQuery(f)}`),
  recordsCsvUrl: (f: TimeFilter) => `/api/v1/work/time/records?${filterQuery(f)}&format=csv`,

  bulkContext: (space?: string, requests?: boolean) => {
    const p = new URLSearchParams();
    if (space) p.set("space", space);
    if (requests) p.set("type", "business_request");
    const s = p.toString();
    return call<BulkContext>("GET", `/work/bulk/context${s ? "?" + s : ""}`);
  },
  bulk: (ids: string[], commands: BulkCommand[], o: { dry_run?: boolean; all_or_nothing?: boolean; text?: string }) =>
    call<BulkResult>("POST", "/work/bulk", { ids, commands, ...o }),
  lastBulk: () => call<{ last: LastBulk | null }>("GET", "/work/bulk/last"),
  undoBulk: () => call<UndoResult>("POST", "/work/bulk/undo"),

  teams: () => call<{ items: TeamView[] }>("GET", "/work/teams"),
  saveTeamMember: (team: string, m: TeamMemberInput) => call<{ items: TeamView[] }>("PUT", `/work/teams/${q(team)}/members`, m),
  removeTeamMember: (team: string, member: string) => call<{ items: TeamView[] }>("DELETE", `/work/teams/${q(team)}/members/${q(member)}`),
  absences: (o: { member?: string; from?: string; to?: string } = {}) => {
    const p = new URLSearchParams();
    for (const [k, v] of Object.entries(o)) if (v) p.set(k, v);
    const s = p.toString();
    return call<{ items: Absence[] }>("GET", `/work/absences${s ? "?" + s : ""}`);
  },
  createAbsence: (a: AbsenceInput) => call<Absence>("POST", "/work/absences", a),
  deleteAbsence: (id: string) => call<void>("DELETE", `/work/absences/${q(id)}`),
};

// ---------------------------------------------------------------------
// Date e durate

export const pad = (n: number) => String(n).padStart(2, "0");
export const isoDay = (d: Date) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
export const parseDay = (s: string) => new Date(s + "T00:00:00");
export const addDays = (s: string, n: number) => {
  const d = parseDay(s);
  d.setDate(d.getDate() + n);
  return isoDay(d);
};
export function mondayOf(s?: string): string {
  const d = s ? parseDay(s) : new Date();
  d.setHours(0, 0, 0, 0);
  d.setDate(d.getDate() - ((d.getDay() + 6) % 7));
  return isoDay(d);
}

/** Minuti in forma da cella: 90 → "1h30", 45 → "45m", 120 → "2h". */
export function cellText(m: number): string {
  if (!m) return "";
  const h = Math.floor(m / 60);
  const r = m % 60;
  if (!h) return `${r}m`;
  return r ? `${h}h${pad(r)}` : `${h}h`;
}

/** Totali: 450 → "7h 30m", 0 → "0h". */
export function totalText(m: number): string {
  if (!m) return "0h";
  const h = Math.floor(m / 60);
  const r = m % 60;
  return [h ? `${h}h` : "", r ? `${r}m` : ""].filter(Boolean).join(" ");
}

/** Ore decimali per i report: 90 → "1,5". */
export function hoursText(m: number): string {
  return (m / 60).toLocaleString(loc(), { maximumFractionDigits: 1 });
}

const HM = /^(\d+)\s*[h:]\s*(\d{1,2})\s*m?$/;
/** Legge una durata come il server: "1,5", "1h30", "1:30", "45m", "2h", "1g". null = non valida. */
export function parseMinutes(s: string): number | null {
  const t = s.trim().toLowerCase();
  if (t === "" || t === "0" || t === "-") return 0;
  if (/^\d+([.,]\d+)?$/.test(t)) return Math.round(parseFloat(t.replace(",", ".")) * 60);
  let m = HM.exec(t);
  if (m) return Number(m[2]) < 60 ? Number(m[1]) * 60 + Number(m[2]) : null;
  m = /^(\d+(?:[.,]\d+)?)\s*h$/.exec(t);
  if (m) return Math.round(parseFloat(m[1].replace(",", ".")) * 60);
  m = /^(\d+)\s*m(?:in)?$/.exec(t);
  if (m) return Number(m[1]);
  m = /^(\d+(?:[.,]\d+)?)\s*g$/.exec(t);
  if (m) return Math.round(parseFloat(m[1].replace(",", ".")) * 8 * 60);
  return null;
}

/** Giorno della settimana abbreviato nella lingua corrente: «Lun», «Mon». */
export function dayName(d: Date): string {
  const n = d.toLocaleDateString(loc(), { weekday: "short" }).replace(/\.$/, "");
  return n.charAt(0).toUpperCase() + n.slice(1);
}
export function dayLabel(s: string): { name: string; num: string; month: string } {
  const d = parseDay(s);
  return { name: dayName(d), num: String(d.getDate()), month: d.toLocaleDateString(loc(), { month: "short" }) };
}
export function weekLabel(week: string): string {
  const a = parseDay(week);
  const b = parseDay(addDays(week, 6));
  const f = (d: Date, y: boolean) => d.toLocaleDateString(loc(), { day: "numeric", month: "short", ...(y ? { year: "numeric" } : {}) });
  return `${f(a, false)} – ${f(b, true)}`;
}
