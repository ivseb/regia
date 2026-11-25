// Client dell'architettura enterprise (piano, passo 20; replica/architettura-ea.md):
// capability, dipendenze tipizzate, grafo e analisi d'impatto, ciclo di vita,
// razionalizzazione, diagrammi, import ed export del catalogo esteso.
import { call } from "./api";
import { tr } from "./i18n/core";

const q = encodeURIComponent;
const B = "/architecture";

export type Crit = "low" | "medium" | "high" | "critical";
export type TimeRating = "tolerate" | "invest" | "migrate" | "eliminate";

// Le etichette si leggono al momento dell'uso, nella lingua corrente.
export const TIME = {
  get tolerate() {
    return tr("altro.ea.time.tolerate");
  },
  get invest() {
    return tr("altro.ea.time.invest");
  },
  get migrate() {
    return tr("altro.ea.time.migrate");
  },
  get eliminate() {
    return tr("altro.ea.time.eliminate");
  },
} as Record<TimeRating, string>;

export type GraphNode = {
  id: string;
  key: string;
  name: string;
  type: string;
  type_name: string;
  state: string;
  state_category: string;
  code?: string;
  criticality?: Crit;
  dora?: boolean;
  time?: TimeRating;
  team_id?: string;
  team?: string;
  parent_id?: string;
  end_of_support?: string;
};
export type GraphEdge = { id: string; type: string; source: string; target: string; attrs?: Record<string, unknown> };
export type EdgeType = { key: string; outward_name: string; inward_name: string; category: string; impact_direction: "forward" | "reverse" | "none" };
export type NamedKey = { key: string; name: string };
export type Graph = { nodes: GraphNode[]; edges: GraphEdge[]; edge_types: EdgeType[]; node_types: NamedKey[]; teams: NamedKey[] };

export type ImpactItem = {
  level: number;
  id: string;
  key: string;
  name: string;
  type: string;
  type_name: string;
  code?: string;
  team?: string;
  criticality?: Crit;
  dora: boolean;
  state: string;
  via: string;
  path: string[];
};
export type ImpactResult = { root: GraphNode; direction: "dependents" | "dependencies"; depth: number; items: ImpactItem[]; cycles: number };

export type CapApp = { id: string; key: string; name: string; code: string | null; criticality: Crit | null; dora: boolean; time: TimeRating | null; team: string | null; retired: boolean };
export type Capability = {
  id: string;
  key: string;
  name: string;
  description: string;
  code: string | null;
  parent_id: string | null;
  level: number;
  path: string[];
  criticality: Crit | null;
  position: number;
  state: string;
  state_category: string;
  owner_unit_id: string | null;
  owner_unit: string | null;
  applications: CapApp[];
  assets: { id: string; key: string; name: string }[];
};
export type CapabilityInput = Partial<{ name: string; description: string; code: string; parent_id: string; criticality: string; position: number }>;

export type AttrField = { key: string; name: string; kind: "text" | "enum" | "number" | "bool"; values?: string[] };
export type RelationType = {
  id: string;
  key: string;
  outward_name: string;
  inward_name: string;
  category: "dependency" | "capability" | "composition";
  impact_direction: "forward" | "reverse" | "none";
  source_types: string[] | null;
  target_types: string[] | null;
  attrs_schema: { fields: AttrField[] } | null;
  system: boolean;
  archived: boolean;
  relations: number;
};
export type RelationTypeInput = Partial<{
  key: string;
  outward_name: string;
  inward_name: string;
  category: string;
  impact_direction: string;
  source_types: string[];
  target_types: string[];
  attrs_schema: { fields: AttrField[] };
  archived: boolean;
}>;
export type Relation = {
  id: string;
  type: string;
  type_name: string;
  category: string;
  source_id: string;
  source_key: string;
  source_name: string;
  source_type: string;
  source_code: string | null;
  target_id: string;
  target_key: string;
  target_name: string;
  target_type: string;
  target_code: string | null;
  attrs: Record<string, unknown>;
  created_at: string;
};

export type Technology = {
  id: string;
  name: string;
  version: string;
  vendor: string;
  category: string;
  end_of_support: string | null;
  end_of_life: string | null;
  notes: string;
  applications: number;
  days_left: number | null;
};
export type TechnologyInput = Partial<{ name: string; version: string; vendor: string; category: string; end_of_support: string; end_of_life: string; notes: string }>;

export type RatApp = {
  id: string;
  key: string;
  name: string;
  code: string | null;
  team: string | null;
  criticality: Crit | null;
  dora: boolean;
  time: TimeRating | null;
  time_note: string;
  state: string;
  dependents: number;
};
export type Settings = { node_types: string[]; eol_warning_days: number; duplicate_min: number };
export type Rationalization = {
  settings: Settings;
  time_counts: Record<TimeRating | "none", number>;
  applications: RatApp[];
  duplicates: { capability_id: string; capability_key: string; capability_name: string; path: string[]; applications: RatApp[] }[];
  end_of_support: { application: RatApp; technology_id: string; technology: string; end_of_support: string | null; end_of_life: string | null; days_left: number }[];
  orphans: RatApp[];
  retiring_with_dependents: RatApp[];
};

export type Profile = {
  node: GraphNode;
  relations: Relation[];
  capabilities: { id: string; key: string; name: string; path: string[] }[];
  technologies: { id: string; name: string; version: string; end_of_support: string | null; end_of_life: string | null; days_left: number | null }[];
  time_note: string;
  dependents: number;
  direct_dependents: number;
  dependencies: number;
  end_of_support_days: number | null;
  relation_types: RelationType[];
};

export type DiagramNode = { id: string; x: number; y: number };
export type DiagramNote = { id: string; x: number; y: number; w?: number; text: string };
export type C4Level = "context" | "container" | "component";
export type C4Actor = { id: string; name: string; kind: "person" | "role"; description?: string; member_id?: string; label?: string };
export type C4Spec = { level: C4Level; root_id: string; actors: C4Actor[]; hidden?: string[] };
export type C4Node = {
  id: string;
  kind: "person" | "system" | "external" | "container" | "database" | "component" | "asset";
  name: string;
  type_label: string;
  tech?: string;
  description?: string;
  element_id?: string;
  key?: string;
  criticality?: Crit;
  inside?: boolean;
};
export type C4Edge = { id: string; source: string; target: string; label: string; protocol?: string; data?: string };
export type C4Model = { level: C4Level; root_id: string; root_name: string; nodes: C4Node[]; edges: C4Edge[]; boundary?: string; missing?: boolean };
export type DiagramLayout = {
  nodes: DiagramNode[];
  notes: DiagramNote[];
  edge_types?: string[];
  c4?: C4Spec;
  pos?: Record<string, { x: number; y: number }>;
};
export type Diagram = {
  id: string;
  name: string;
  kind: string;
  description: string;
  version: number;
  layout: DiagramLayout;
  node_count: number;
  created_by: string | null;
  updated_by: string | null;
  updated_at: string;
};
export type DiagramView = Diagram & { nodes: GraphNode[]; edges: GraphEdge[]; edge_types: EdgeType[]; missing: number; c4?: C4Model };
export type DiagramInput = Partial<{ name: string; kind: string; description: string; layout: DiagramLayout; version: number }>;

export type EAImportReport = {
  dry_run: boolean;
  created: number;
  updated: number;
  errors: number;
  rows: { kind: string; line: number; name: string; outcome: "created" | "updated" | "error"; message?: string }[];
};

export const eaApi = {
  graph: () => call<Graph>("GET", `${B}/graph`),
  impact: (ref: string, direction: "dependents" | "dependencies", depth = 0) =>
    call<ImpactResult>("GET", `${B}/impact/${q(ref)}?direction=${direction}&depth=${depth}`),
  impactCsvUrl: (ref: string, direction: string, depth = 0) => `/api/v1${B}/impact/${q(ref)}?direction=${direction}&depth=${depth}&format=csv`,
  profile: (ref: string) => call<Profile>("GET", `${B}/profile/${q(ref)}`),

  capabilities: () => call<{ items: Capability[] }>("GET", `${B}/capabilities`),
  createCapability: (c: CapabilityInput) => call<Capability>("POST", `${B}/capabilities`, c),
  updateCapability: (ref: string, c: CapabilityInput) => call<Capability>("PATCH", `${B}/capabilities/${q(ref)}`, c),
  linkCapability: (ref: string, elementId: string) => call<void>("POST", `${B}/capabilities/${q(ref)}/links`, { element_id: elementId }),
  unlinkCapability: (ref: string, elementId: string) => call<void>("DELETE", `${B}/capabilities/${q(ref)}/links/${q(elementId)}`),

  relationTypes: () => call<{ items: RelationType[] }>("GET", `${B}/relation-types`),
  createRelationType: (t: RelationTypeInput) => call<RelationType>("POST", `${B}/relation-types`, t),
  updateRelationType: (key: string, t: RelationTypeInput) => call<RelationType>("PATCH", `${B}/relation-types/${q(key)}`, t),
  relations: (category = "", element = "") => call<{ items: Relation[] }>("GET", `${B}/relations?category=${q(category)}&element=${q(element)}`),
  createRelation: (type: string, source: string, target: string, attrs: Record<string, unknown> = {}) =>
    call<Relation>("POST", `${B}/relations`, { type, source, target, attrs }),
  updateRelation: (id: string, attrs: Record<string, unknown>) => call<Relation>("PATCH", `${B}/relations/${q(id)}`, { attrs }),
  deleteRelation: (id: string) => call<void>("DELETE", `${B}/relations/${q(id)}`),

  setTime: (ref: string, time_rating: string, note: string) => call<void>("PUT", `${B}/applications/${q(ref)}/time`, { time_rating, note }),
  setTechnologies: (ref: string, ids: string[]) => call<void>("PUT", `${B}/applications/${q(ref)}/technologies`, { technology_ids: ids }),
  technologies: () => call<{ items: Technology[] }>("GET", `${B}/technologies`),
  createTechnology: (t: TechnologyInput) => call<Technology>("POST", `${B}/technologies`, t),
  updateTechnology: (id: string, t: TechnologyInput) => call<Technology>("PATCH", `${B}/technologies/${q(id)}`, t),
  deleteTechnology: (id: string) => call<void>("DELETE", `${B}/technologies/${q(id)}`),
  rationalization: () => call<Rationalization>("GET", `${B}/rationalization`),
  settings: () => call<Settings>("GET", `${B}/settings`),
  updateSettings: (s: Settings) => call<Settings>("PUT", `${B}/settings`, s),

  diagrams: () => call<{ items: Diagram[] }>("GET", `${B}/diagrams`),
  diagram: (id: string) => call<DiagramView>("GET", `${B}/diagrams/${q(id)}`),
  generateC4: (root_id: string, level: C4Level) => call<DiagramView>("POST", `${B}/diagrams/c4`, { root_id, level }),
  createDiagram: (d: DiagramInput) => call<DiagramView>("POST", `${B}/diagrams`, d),
  updateDiagram: (id: string, d: DiagramInput) => call<DiagramView>("PUT", `${B}/diagrams/${q(id)}`, d),
  deleteDiagram: (id: string) => call<void>("DELETE", `${B}/diagrams/${q(id)}`),

  exportUrl: (format: "json" | "csv", what = "") => `/api/v1${B}/catalog/export?format=${format}${what ? `&what=${what}` : ""}`,
  importCSV: (kind: "capabilities" | "dependencies", csv: string, dryRun: boolean) =>
    call<EAImportReport>("POST", `${B}/catalog/import${dryRun ? "?dry_run=true" : ""}`, { kind, csv }),
  importJSON: (json: unknown, dryRun: boolean) => call<EAImportReport>("POST", `${B}/catalog/import${dryRun ? "?dry_run=true" : ""}`, { json }),
};

/** Permesso di modifica dell'architettura. */
export function canWriteEA(perms: string[]): boolean {
  return perms.includes("*") || perms.includes("architecture.write");
}
