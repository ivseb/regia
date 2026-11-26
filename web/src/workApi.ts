// Client del work management (piano, passi 10 e 18; replica/work.md): spazi dei team,
// board, backlog e sprint, ore lavorate, il mio lavoro, epic dei team.
import { call, type Element, type Tag } from "./api";
import { loc } from "./i18n/format";

const q = encodeURIComponent;

export type TeamSpace = {
  space_id: string;
  key: string;
  name: string;
  team_id: string;
  team: string;
  lead_id: string | null;
  lead: string | null;
  open: number;
  active_sprint: string | null;
  active_sprint_title: string | null;
};
export type TeamWithoutSpace = { id: string; name: string; proposed_key: string };

export type BoardColumn = { name: string; phases: string[]; wip: number; color?: string; type_phases?: Record<string, string[]> };
export type BoardSettings = {
  columns: BoardColumn[];
  swimlane: Swimlane;
  types: string[];
  done_days: number;
  custom: boolean;
  model?: string;
  no_sprint?: boolean;
  sort?: string;
};
export type PhaseInfo = { name: string; category: string; types: string[] };
export type BoardModel = { key: string; name: string; description: string; no_sprint: boolean; sort: string };
export type BoardPreview = { settings: BoardSettings; columns: ColumnView[]; unassigned: string[]; empty: string[]; hidden: number };
export type BoardConfig = {
  space: Ref;
  published: BoardSettings;
  draft: BoardSettings | null;
  phases: PhaseInfo[];
  models: BoardModel[];
  preview: BoardPreview;
  can_manage: boolean;
};
export type Swimlane = "none" | "assignee" | "epic" | "priority";
export type ColumnView = BoardColumn & { count: number; category: string; over: boolean };
export type Lane = { key: string; name: string; color?: string };
export type Ref = { key: string; title: string; state?: string };
export type Option = { id: string; name: string; color: string };
export type SprintRef = { id: string; key: string; title: string; state: string; category: string; start: string | null; end: string | null };

/** Card compatta della board (migliaia per risposta). */
export type Card = {
  id: string;
  key: string;
  title: string;
  type: string;
  state: string;
  col: number;
  lane?: string;
  assignee?: string;
  priority?: string;
  points?: number;
  estimate?: number;
  parent?: string;
  epic?: string;
  children?: number;
  children_done?: number;
  version: number;
  resolved?: boolean;
  tags?: Tag[];
};

/** Tipo che «Nuovo elemento» propone: nome, tipi ammessi come genitore e se ha il campo Punti. */
export type CreateType = { key: string; name: string; color?: string; parents: string[]; points: boolean };

export type Board = {
  space: Ref;
  team: string | null;
  settings: BoardSettings;
  columns: ColumnView[];
  lanes: Lane[];
  cards: Card[];
  hidden: number;
  members: Record<string, string>;
  epics: Record<string, Ref>;
  priorities: Option[];
  /** I tipi della board che si possono creare, nell'ordine della board. */
  create_types: CreateType[];
  sprints: SprintRef[];
  sprint: SprintRef | null;
  can_manage: boolean;
  can_work: boolean;
  truncated: boolean;
  /** Senza sprint la board è paginata per colonna: le prime card di ognuna, i totali esatti nelle colonne. */
  paged?: boolean;
  page_size?: number;
};

export type MoveInput = {
  element: string;
  column?: number;
  comment?: string;
  after?: string;
  before?: string;
  lane?: string;
  swimlane?: Swimlane;
};

export type BacklogItem = {
  id: string;
  key: string;
  title: string;
  type: string;
  state: string;
  category: string;
  assignee?: string;
  priority?: string;
  points?: number;
  epic?: string;
  sprint?: string;
  tags?: Tag[];
};
export type SprintView = SprintRef & { goal: string; points: number; points_done: number; items: number; items_done: number };
export type Backlog = {
  space: Ref;
  sprints: SprintView[];
  items: BacklogItem[];
  members: Record<string, string>;
  epics: Record<string, Ref>;
  priorities: Option[];
  can_manage: boolean;
  can_work: boolean;
  truncated: boolean;
  create_types: CreateType[];
  /** Solo con la paginazione: totale del backlog (senza sprint) e se dopo le voci caricate ce ne sono altre. */
  backlog_total?: number;
  more?: boolean;
};

export type Point = { day: string; value: number };
export type Burndown = {
  sprint: SprintRef;
  goal: string;
  unit: "points" | "items";
  start: string;
  end: string;
  committed: number;
  scope: number;
  remaining: number;
  completed: number;
  ideal: Point[];
  actual: Point[];
  started: boolean;
  closed: boolean;
};

export type TimeEntry = { id: string; element_key: string; member_id: string; member: string; date: string; minutes: number; text: string | null; mine: boolean };
export type TimeSummary = { entries: TimeEntry[]; minutes: number; total_minutes: number; estimate: number | null; can_log: boolean };
export type TimeInput = { duration?: string; hours?: number; date?: string; text?: string };

export type Progress = { done: number; total: number; percent: number; epics: number; open_epics: number };
export type EpicView = {
  id: string;
  key: string;
  title: string;
  state: string;
  state_category: string;
  space_key: string;
  team: string | null;
  impact_key: string | null;
  application: string | null;
  assignee: string | null;
  progress: Progress;
};
export type Delivery = {
  kind: "request" | "impact" | "epic" | "item" | "sprint" | "none";
  epics: EpicView[];
  progress?: Progress;
  can_create_epic: boolean;
  sprint?: Ref;
  space?: Ref;
};

export type MyItem = {
  id: string;
  key: string;
  title: string;
  type: string;
  type_name: string;
  state: string;
  category: string;
  space: string;
  priority: string | null;
  priority_color: string | null;
  points: number | null;
  due: string | null;
  sprint: string | null;
  updated_at: string;
  tags?: Tag[];
};

export type ItemInput = { type: string; title: string; parent?: string; assignee_id?: string; sprint?: string; fields?: Record<string, unknown>; top?: boolean };
export type SprintInput = { title?: string; goal?: string; start?: string; end?: string };

const sp = (key: string) => `/work/spaces/${q(key)}`;

export const workApi = {
  spaces: () => call<{ items: TeamSpace[]; teams_without_space: TeamWithoutSpace[] }>("GET", "/work/spaces"),
  createSpace: (team_id: string, key?: string, model?: string) => call<TeamSpace>("POST", "/work/spaces", { team_id, key: key ?? "", model: model ?? "" }),
  boardConfig: (key: string) => call<BoardConfig>("GET", `${sp(key)}/board/config`),
  saveDraft: (key: string, s: BoardSettings) => call<BoardConfig>("PUT", `${sp(key)}/board/draft`, s),
  discardDraft: (key: string) => call<BoardConfig>("DELETE", `${sp(key)}/board/draft`),
  previewBoard: (key: string, o: { model?: string; settings?: BoardSettings }) => call<BoardPreview>("POST", `${sp(key)}/board/preview`, o),
  publishBoard: (key: string) => call<BoardConfig>("POST", `${sp(key)}/board/publish`),
  board: (key: string, o: { q?: string; sprint?: string; swimlane?: string; col?: number; after?: string; limit?: number }) => {
    const p = new URLSearchParams();
    if (o.col != null) p.set("col", String(o.col));
    if (o.after) p.set("after", o.after);
    if (o.limit) p.set("limit", String(o.limit));
    if (o.q) p.set("q", o.q);
    if (o.sprint) p.set("sprint", o.sprint);
    if (o.swimlane) p.set("swimlane", o.swimlane);
    const s = p.toString();
    return call<Board>("GET", `${sp(key)}/board${s ? "?" + s : ""}`);
  },
  saveSettings: (key: string, s: BoardSettings) => call<BoardSettings>("PUT", `${sp(key)}/board/settings`, s),
  move: (key: string, m: MoveInput) => call<{ element: Element; rank: string; phase: string }>("POST", `${sp(key)}/board/move`, m),
  createItem: (key: string, i: ItemInput) => call<Element>("POST", `${sp(key)}/items`, i),
  /** limit: voci del backlog da caricare (gli sprint arrivano interi); senza, tutto il backlog. */
  backlog: (key: string, query?: string, limit?: number) => {
    const p = new URLSearchParams();
    if (query) p.set("q", query);
    if (limit) p.set("limit", String(limit));
    const s = p.toString();
    return call<Backlog>("GET", `${sp(key)}/backlog${s ? "?" + s : ""}`);
  },
  createSprint: (key: string, s: SprintInput) => call<Element>("POST", `${sp(key)}/sprints`, s),
  updateSprint: (ref: string, s: SprintInput) => call<Element>("PATCH", `/work/sprints/${q(ref)}`, s),
  startSprint: (ref: string) => call<Element>("POST", `/work/sprints/${q(ref)}/start`),
  closeSprint: (ref: string, move_to: string) => call<Element>("POST", `/work/sprints/${q(ref)}/close`, { move_to }),
  burndown: (ref: string) => call<Burndown>("GET", `/work/sprints/${q(ref)}/burndown`),
  rank: (ref: string, after?: string, before?: string) => call<{ rank: string }>("PUT", `/work/elements/${q(ref)}/rank`, { after: after ?? "", before: before ?? "" }),
  assign: (ref: string, member_id: string) => call<Element>("PUT", `/work/elements/${q(ref)}/assignee`, { member_id }),
  plan: (ref: string, sprint: string, after?: string, before?: string) =>
    call<Element>("PUT", `/work/elements/${q(ref)}/sprint`, { sprint, after: after ?? "", before: before ?? "" }),
  time: (ref: string) => call<TimeSummary>("GET", `/work/elements/${q(ref)}/time`),
  logTime: (ref: string, t: TimeInput) => call<{ id: string; costed: boolean; time: TimeSummary }>("POST", `/work/elements/${q(ref)}/time`, t),
  deleteTime: (id: string) => call<void>("DELETE", `/work/time/${q(id)}`),
  delivery: (ref: string) => call<Delivery>("GET", `/work/elements/${q(ref)}/delivery`),
  createEpic: (impact: string) => call<Element>("POST", `/work/impacts/${q(impact)}/epic`),
  mine: () => call<{ items: MyItem[] }>("GET", "/work/me"),
};

/** Lettera del tipo sulle card (E, S, T, B). */
export const TYPE_MARK: Record<string, { letter: string; name: string }> = {
  epic: { letter: "E", name: "Epic" },
  story: { letter: "S", name: "Story" },
  task: { letter: "T", name: "Task" },
  bug: { letter: "B", name: "Bug" },
  sprint: { letter: "SP", name: "Sprint" },
};

/** Spazio scelto per ultimo (per persona e browser). */
const LAST_SPACE = "regia.delivery.spazio";
export function lastSpace(): string | undefined {
  try {
    return localStorage.getItem(LAST_SPACE) ?? undefined;
  } catch {
    return undefined;
  }
}
export function rememberSpace(key: string) {
  try {
    localStorage.setItem(LAST_SPACE, key);
  } catch {
    /* navigazione privata: pazienza */
  }
}

/** Sprint scelto per ultimo in ogni spazio: la board riparte da lì invece che da «Sprint attivo». */
const LAST_SPRINT = "regia.delivery.sprint.";
export function lastSprint(space: string | undefined): string | undefined {
  if (!space) return undefined;
  try {
    return localStorage.getItem(LAST_SPRINT + space) ?? undefined;
  } catch {
    return undefined;
  }
}
/** `sprint` vuoto = «Sprint attivo» (si dimentica la scelta). */
export function rememberSprint(space: string | undefined, sprint: string | undefined) {
  if (!space) return;
  try {
    if (sprint) localStorage.setItem(LAST_SPRINT + space, sprint);
    else localStorage.removeItem(LAST_SPRINT + space);
  } catch {
    /* navigazione privata: pazienza */
  }
}

/** Minuti in ore leggibili: 90 → "1h 30m". */
export function fmtMinutes(m: number): string {
  if (!m) return "0h";
  const h = Math.floor(m / 60);
  const r = Math.round(m % 60);
  return [h ? `${h}h` : "", r ? `${r}m` : ""].filter(Boolean).join(" ");
}

export function fmtPoints(n: number | null | undefined): string {
  if (n == null) return "";
  return Number.isInteger(n) ? String(n) : n.toLocaleString(loc(), { maximumFractionDigits: 1 });
}
