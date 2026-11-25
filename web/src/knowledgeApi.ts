// Client del modulo Knowledge (piano, passo 19; replica/knowledge.md): spazi di documentazione,
// pagine ad albero, versioni e confronto, blocco di modifica, template, documenti controllati,
// ricerca nello spazio, documenti collegati agli elementi.
import { ApiError, call, type ApprovalRequest, type Mention, type State, type TransitionView } from "./api";

const q = encodeURIComponent;

export type Rights = { read: boolean; write: boolean; manage: boolean };
export type Space = {
  id: string;
  key: string;
  name: string;
  description: string;
  access: "open" | "restricted";
  lead_id: string | null;
  lead: string;
  pages: number;
  updated_at: string | null;
  rights: Rights;
};
export type TreeNode = {
  id: string;
  key: string;
  title: string;
  parent_id: string | null;
  state?: string;
  state_category?: string;
  controlled: boolean;
  updated_at: string;
};
export type SpaceView = { space: Space; pages: TreeNode[] };
export type Crumb = { key: string; title: string };
export type Person = { id: string; name: string };
export type Lock = { member_id: string; name: string; acquired_at: string; expires_at: string; mine: boolean };
export type VersionInfo = {
  version: number;
  title: string;
  note: string;
  author_id: string | null;
  author: string;
  created_at: string;
  approved_at: string | null;
  approved_by: string;
  size: number;
  current: boolean;
  in_force: boolean;
};
export type VersionView = VersionInfo & { body: string };
export type Page = {
  id: string;
  key: string;
  title: string;
  space_key: string;
  space_name: string;
  parent_id: string | null;
  version: number;
  body: string;
  mentions: Mention[];
  state: string;
  state_category: string;
  controlled: boolean;
  review_months: number | null;
  next_review: string | null;
  approved_version: number | null;
  template_key: string;
  created_at: string;
  created_by: string;
  updated_at: string;
  updated_by: string;
  element_version: number;
};
export type LinkedElement = { id: string; key: string; title: string; type: string; type_name: string; state: string; state_category: string; relation?: string };
export type PageDetail = {
  page: Page;
  rights: Rights;
  path: Crumb[];
  children: Crumb[];
  lock: Lock | null;
  approved: VersionView | null;
  reviewers: Person[];
  approvers: Person[];
  links: LinkedElement[];
  transitions: TransitionView[];
  phases: State[];
  approvals: ApprovalRequest[];
};
export type Recent = {
  key: string;
  title: string;
  space_key: string;
  space_name: string;
  state: string;
  state_category: string;
  controlled: boolean;
  updated_at: string;
  updated_by: string;
};
export type Indicators = {
  review_overdue: number;
  review_soon: number;
  open_threads: number;
  orphan_threads: number;
  campaigns_active: number;
  campaigns_overdue: number;
  readings_missing: number;
  my_readings: number;
};
export type OpenThread = { thread_id: string; key: string; title: string; space_key: string; quote: string; excerpt: string; author: string; replies: number; orphan: boolean; updated_at: string };
export type MyReading = { key: string; title: string; space_key: string; name: string; version: number; due: string | null; overdue: boolean };
export type CampaignRow = { key: string; title: string; space_key: string; name: string; version: number; due: string | null; total: number; confirmed: number; overdue: boolean };
export type Home = {
  spaces: Space[];
  recent: Recent[];
  review_due: Recent[];
  to_review: Recent[];
  can_create: boolean;
  indicators: Indicators;
  open_threads: OpenThread[];
  my_readings: MyReading[];
  campaigns: CampaignRow[];
};

// Commenti in linea
export type ThreadComment = { id: string; author_id: string | null; author: string; body: string; mentions: Mention[]; created_at: string; edited_at: string | null; can_delete: boolean };
export type Thread = {
  id: string;
  quote: string;
  prefix: string;
  suffix: string;
  pos: number | null;
  nth: number;
  orphan: boolean;
  status: "open" | "resolved";
  version: number;
  resolved_by: string;
  resolved_at: string | null;
  created_by: string;
  created_at: string;
  comments: ThreadComment[];
};
export type ThreadList = { items: Thread[]; can_comment: boolean; can_resolve: boolean; page_version: number };
export type ThreadDraft = { quote: string; prefix: string; suffix: string; nth: number };

// Conferma di lettura
export type AudienceRef = { kind: "member" | "group" | "role"; id: string; name?: string };
export type Reader = { member_id: string; name: string; state: "confirmed" | "pending" | "outdated"; confirmed_version: number | null; confirmed_at: string | null };
export type Campaign = {
  id: string;
  name: string;
  version: number;
  due_date: string | null;
  status: "active" | "closed";
  reopen_on_approval: boolean;
  renew_days: number;
  remind_days: number;
  created_by: string;
  created_at: string;
  closed_at: string | null;
  reminded_at: string | null;
  audience: AudienceRef[];
  total: number;
  confirmed: number;
  overdue: boolean;
  complete: boolean;
  my_state: "" | "confirmed" | "pending" | "outdated";
  people: Reader[];
};
export type Readings = { items: Campaign[]; can_manage: boolean; version: number; approved_version: number | null };
export type CampaignInput = {
  name?: string;
  version?: number;
  due_date?: string | null;
  audience?: AudienceRef[];
  reopen_on_approval?: boolean;
  renew_days?: number;
  remind_days?: number;
  status?: "active" | "closed";
};
export type AudiencePreview = { people: Person[]; excluded: Person[] };
export type ImportItem = { path: string; title: string; level: number; chars: number; attachments: number; synthetic: boolean; key?: string };
export type ImportReport = { dry: boolean; pages: number; attachments: number; items: ImportItem[]; warnings: string[]; ignored: number };
export type Template = {
  id: string;
  space_key?: string;
  key: string;
  name: string;
  description: string;
  body: string;
  controlled: boolean;
  review_months: number | null;
  archived: boolean;
  can_edit: boolean;
};
export type DiffPart = { op: "eq" | "add" | "del"; text: string };
export type DiffLine = { op: "eq" | "add" | "del" | "mod"; a?: number; b?: number; text?: string; old?: string; parts?: DiffPart[] };
export type Comparison = {
  from: VersionInfo;
  to: VersionInfo;
  title_changed: boolean;
  lines: DiffLine[];
  stats: { added: number; removed: number; changed: number };
};
export type SearchHit = { key: string; title: string; state: string; excerpt: { t: string; hit?: boolean }[]; updated_at: string };
export type AccessEntry = {
  id: string;
  role_key: string;
  role_name: string;
  level: "write" | "read" | "none";
  member_id: string | null;
  group_id: string | null;
  name: string;
};
export type Document = {
  key: string;
  title: string;
  space_key: string;
  space_name: string;
  state: string;
  state_category: string;
  controlled: boolean;
  approved_version: number | null;
  via: "link" | "ref";
  updated_at: string;
};
export type RefInfo = { key: string; title: string; type: string; type_name: string; state: string; state_category: string };

export const kb = {
  home: () => call<Home>("GET", "/kb/home"),
  spaces: () => call<{ items: Space[] }>("GET", "/kb/spaces"),
  createSpace: (s: { key: string; name: string; description?: string; access?: string }) => call<Space>("POST", "/kb/spaces", s),
  updateSpace: (key: string, s: { name?: string; description?: string; access?: string; lead_id?: string }) =>
    call<Space>("PATCH", `/kb/spaces/${q(key)}`, s),
  tree: (key: string) => call<SpaceView>("GET", `/kb/spaces/${q(key)}`),
  access: (key: string) => call<{ items: AccessEntry[] }>("GET", `/kb/spaces/${q(key)}/access`),
  grant: (key: string, g: { role: string; member_id?: string; group_id?: string }) =>
    call<{ items: AccessEntry[] }>("POST", `/kb/spaces/${q(key)}/access`, g),
  revoke: (key: string, id: string) => call<{ items: AccessEntry[] }>("DELETE", `/kb/spaces/${q(key)}/access/${q(id)}`),
  search: (key: string, text: string) => call<{ items: SearchHit[] }>("GET", `/kb/spaces/${q(key)}/search?q=${q(text)}`),
  templates: (key: string) => call<{ items: Template[] }>("GET", `/kb/spaces/${q(key)}/templates`),
  createTemplate: (key: string, t: Partial<Template>) => call<Template>("POST", `/kb/spaces/${q(key)}/templates`, t),
  updateTemplate: (id: string, t: Partial<Template>) => call<Template>("PATCH", `/kb/templates/${q(id)}`, t),

  createPage: (space: string, p: { title: string; parent?: string; template?: string; body?: string; after?: string; before?: string }) =>
    call<PageDetail>("POST", `/kb/spaces/${q(space)}/pages`, p),
  page: (ref: string) => call<PageDetail>("GET", `/kb/pages/${q(ref)}`),
  save: (ref: string, s: { title?: string; body: string; note?: string; base_version: number }) => call<PageDetail>("PUT", `/kb/pages/${q(ref)}`, s),
  remove: (ref: string) => call<void>("DELETE", `/kb/pages/${q(ref)}`),
  move: (ref: string, m: { parent?: string; after?: string; before?: string }) => call<PageDetail>("POST", `/kb/pages/${q(ref)}/move`, m),
  versions: (ref: string) => call<{ items: VersionInfo[] }>("GET", `/kb/pages/${q(ref)}/versions`),
  version: (ref: string, n: number) => call<VersionView>("GET", `/kb/pages/${q(ref)}/versions/${n}`),
  diff: (ref: string, from: number, to: number) => call<Comparison>("GET", `/kb/pages/${q(ref)}/diff?from=${from}&to=${to}`),
  restore: (ref: string, n: number, note?: string) => call<PageDetail>("POST", `/kb/pages/${q(ref)}/versions/${n}/restore`, { note }),
  lock: (ref: string, force = false) => call<{ lock: Lock | null; taken: boolean }>("POST", `/kb/pages/${q(ref)}/lock${force ? "?force=true" : ""}`),
  unlock: (ref: string) => call<void>("DELETE", `/kb/pages/${q(ref)}/lock`),
  control: (ref: string, c: { controlled: boolean; review_months?: number | null; reviewers: string[]; approvers: string[] }) =>
    call<PageDetail>("PUT", `/kb/pages/${q(ref)}/control`, c),
  reviewed: (ref: string, note?: string) => call<PageDetail>("POST", `/kb/pages/${q(ref)}/reviewed`, { note }),
  link: (ref: string, element: string) => call<PageDetail>("POST", `/kb/pages/${q(ref)}/links`, { element }),
  unlink: (ref: string, element: string) => call<PageDetail>("DELETE", `/kb/pages/${q(ref)}/links/${q(element)}`),
  documents: (ref: string) => call<{ items: Document[] }>("GET", `/kb/elements/${q(ref)}/documents`),
  threads: (ref: string) => call<ThreadList>("GET", `/kb/pages/${q(ref)}/threads`),
  createThread: (ref: string, t: ThreadDraft & { body: string }) => call<ThreadList>("POST", `/kb/pages/${q(ref)}/threads`, t),
  reply: (thread: string, body: string) => call<ThreadList>("POST", `/kb/threads/${q(thread)}/comments`, { body }),
  deleteThreadComment: (thread: string, comment: string) => call<ThreadList>("DELETE", `/kb/threads/${q(thread)}/comments/${q(comment)}`),
  resolve: (thread: string) => call<ThreadList>("POST", `/kb/threads/${q(thread)}/resolve`),
  reopen: (thread: string) => call<ThreadList>("POST", `/kb/threads/${q(thread)}/reopen`),

  audience: () => call<{ groups: { id: string; name: string; members: number }[]; roles: { id: string; name: string }[] }>("GET", "/kb/audience"),
  readings: (ref: string) => call<Readings>("GET", `/kb/pages/${q(ref)}/readings`),
  createReading: (ref: string, c: CampaignInput) => call<Readings>("POST", `/kb/pages/${q(ref)}/readings`, c),
  previewReading: (ref: string, audience: AudienceRef[]) => call<AudiencePreview>("POST", `/kb/pages/${q(ref)}/readings/preview`, { audience }),
  updateReading: (id: string, c: CampaignInput) => call<Readings>("PATCH", `/kb/readings/${q(id)}`, c),
  confirmReading: (id: string, version: number) => call<Readings>("POST", `/kb/readings/${q(id)}/confirm`, { version }),
  remindReading: (id: string) => call<Readings>("POST", `/kb/readings/${q(id)}/remind`),
  readingCsvUrl: (id: string) => `/api/v1/kb/readings/${q(id)}/export`,

  exportUrl: (space: string, o: { page?: string; approved?: boolean; attachments?: boolean }) => {
    const p = new URLSearchParams();
    if (o.page) p.set("pagina", o.page);
    if (o.approved) p.set("approvate", "1");
    if (o.attachments === false) p.set("allegati", "0");
    const qs = p.toString();
    return `/api/v1/kb/spaces/${q(space)}/export${qs ? "?" + qs : ""}`;
  },
  importZip: async (space: string, file: File, o: { parent?: string; dry?: boolean }): Promise<ImportReport> => {
    const form = new FormData();
    if (o.dry) form.append("dry", "1");
    if (o.parent) form.append("parent", o.parent);
    form.append("file", file, file.name);
    const res = await fetch(`/api/v1/kb/spaces/${q(space)}/import`, { method: "POST", credentials: "same-origin", body: form });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new ApiError(res.status, data.code ?? "unknown", data.detail ?? res.statusText, data.field, data.problems);
    return data as ImportReport;
  },
  refs: (keys: string[]) => call<{ items: RefInfo[] }>("GET", `/kb/refs?keys=${q(keys.join(","))}`),
};
