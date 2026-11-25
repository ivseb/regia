// API dei campi personalizzati (Configurazione → Campi) e della pagina di dettaglio:
// configurazione, campi del tipo per i form, collegamenti, gruppi. Rispecchia
// internal/engine/fields*.go e internal/api/fields.go.
import { call, type Element, type State } from "./api";
import { tr } from "./i18n/core";

const q = encodeURIComponent;

export type DataType =
  | "string"
  | "text"
  | "integer"
  | "decimal"
  | "boolean"
  | "date"
  | "datetime"
  | "duration"
  | "money"
  | "url"
  | "enum"
  | "version"
  | "member"
  | "group"
  | "element"
  | "state";

/** Token del tema per il colore di un'opzione: niente colori fissi. */
export type OptionColor = "" | "accent" | "ok" | "flag" | "danger" | "muted" | "fg";

export type OptionView = { id: string; name: string; color?: OptionColor; archived?: boolean; labels?: Record<string, string> };
export type RefLabel = { label: string; key?: string; type?: string };
export type FieldSettings = {
  types?: string[];
  group?: string;
  groups?: string[];
  currency?: string;
  min?: number;
  max?: number;
  hours_per_day?: number;
};
export type Money = { amount: number; currency: string };

/** Campo del tipo: definizione, regole dell'associazione e (nel dettaglio) valore e stato. */
export type FieldView = {
  key: string;
  name: string;
  data_type: DataType;
  multi: boolean;
  help?: string;
  section: string;
  ordinal: number;
  required: boolean;
  required_from?: string;
  visible_when?: string;
  archived?: boolean;
  settings: FieldSettings;
  default?: unknown;
  options?: OptionView[];
  value?: unknown;
  visible: boolean;
  required_now: boolean;
  labels?: Record<string, RefLabel>;
};

export type FieldAssoc = {
  type: string;
  type_name?: string;
  required: boolean;
  required_from?: string;
  visible_when?: string;
  section?: string;
  ordinal: number;
  problem?: string;
};

export type FieldConfig = {
  id: string;
  key: string;
  name: string;
  data_type: DataType;
  multi: boolean;
  help: string;
  default: unknown;
  settings: FieldSettings;
  aliases: string[];
  archived: boolean;
  system: boolean;
  option_set_id: string | null;
  option_set?: string;
  shared_with?: string[];
  options?: OptionView[];
  types: FieldAssoc[];
  in_use: boolean;
  elements: number;
  updated_at: string;
};

export type FieldInput = {
  key?: string;
  name?: string;
  data_type?: DataType;
  multi?: boolean;
  help?: string;
  default?: unknown;
  settings?: FieldSettings;
  aliases?: string[];
  archived?: boolean;
  option_set_id?: string;
  options?: OptionInput[];
};
export type OptionInput = { id?: string; name: string; color?: OptionColor; archived?: boolean; labels?: Record<string, string> };
export type AssocInput = { required: boolean; required_from: string; visible_when: string; section: string; ordinal?: number };

export type FieldCatalog = {
  types: { key: string; name: string; module: string; phases: State[] }[];
  groups: string[];
  base_currency: string;
  data_types: DataType[];
  multi_types: DataType[];
  colors: OptionColor[];
  option_sets: { id: string; name: string; kind: string; fields: number }[];
};

export type ImportReport = { created: string[]; updated: string[]; associations: number; warnings: string[] };

export type LinkedElement = {
  id: string;
  key: string;
  title: string;
  type: string;
  type_name: string;
  state: string;
  state_category: string;
  relation?: string;
};
export type Links = { parent: LinkedElement | null; children: LinkedElement[]; relations: LinkedElement[] };
export type Group = { id: string; name: string };

const f = (ref: string) => `/fields/${q(ref)}`;

export const fieldsApi = {
  list: () => call<{ items: FieldConfig[] }>("GET", "/fields"),
  catalog: () => call<FieldCatalog>("GET", "/fields/catalog"),
  get: (ref: string) => call<FieldConfig>("GET", f(ref)),
  create: (in_: FieldInput) => call<FieldConfig>("POST", "/fields", in_),
  update: (ref: string, in_: FieldInput) => call<FieldConfig>("PATCH", f(ref), in_),
  remove: (ref: string) => call<void>("DELETE", f(ref)),
  saveOptions: (ref: string, options: OptionInput[]) => call<FieldConfig>("PUT", `${f(ref)}/options`, { options }),
  associate: (ref: string, type: string, a: AssocInput) => call<FieldConfig>("PUT", `${f(ref)}/types/${q(type)}`, a),
  dissociate: (ref: string, type: string) => call<FieldConfig>("DELETE", `${f(ref)}/types/${q(type)}`),
  reorder: (type: string, fields: string[]) => call<void>("PUT", `/types/${q(type)}/fields/order`, { fields }),
  exportUrl: "/api/v1/fields/export",
  importBundle: (bundle: unknown, dryRun: boolean) =>
    call<{ report: ImportReport; dry_run: boolean }>("POST", `/fields/import${dryRun ? "?dry_run=true" : ""}`, bundle),

  typeFields: (type: string) => call<{ items: FieldView[] }>("GET", `/types/${q(type)}/fields`),
  links: (ref: string) => call<Links>("GET", `/elements/${q(ref)}/links`),
  groups: () => call<{ items: Group[] }>("GET", "/groups"),
  /** Modifica titolo, descrizione o campi (lock ottimistico con la versione). */
  updateElement: (id: string, version: number, patch: { title?: string; body_text?: string; fields?: Record<string, unknown> }) =>
    call<Element>("PATCH", `/elements/${id}`, { version, ...patch }),
};

export const DATA_TYPE_LABELS: Record<DataType, string> = {
  get string() { return tr("configurazione.fields.dataType.string"); },
  get text() { return tr("configurazione.fields.dataType.text"); },
  get integer() { return tr("configurazione.fields.dataType.integer"); },
  get decimal() { return tr("configurazione.fields.dataType.decimal"); },
  get boolean() { return tr("configurazione.fields.dataType.boolean"); },
  get date() { return tr("configurazione.fields.dataType.date"); },
  get datetime() { return tr("configurazione.fields.dataType.datetime"); },
  get duration() { return tr("configurazione.fields.dataType.duration"); },
  get money() { return tr("configurazione.fields.dataType.money"); },
  get url() { return tr("configurazione.fields.dataType.url"); },
  get enum() { return tr("configurazione.fields.dataType.enum"); },
  get version() { return tr("configurazione.fields.dataType.version"); },
  get member() { return tr("configurazione.fields.dataType.member"); },
  get group() { return tr("configurazione.fields.dataType.group"); },
  get element() { return tr("configurazione.fields.dataType.element"); },
  get state() { return tr("configurazione.fields.dataType.state"); },
};

/** Un esempio per ogni tipo di dato, mostrato nel modulo di creazione di un campo. */
export const DATA_TYPE_EXAMPLES: Record<DataType, string> = {
  get string() { return tr("configurazione.fields.dataTypeExample.string"); },
  get text() { return tr("configurazione.fields.dataTypeExample.text"); },
  get integer() { return tr("configurazione.fields.dataTypeExample.integer"); },
  get decimal() { return tr("configurazione.fields.dataTypeExample.decimal"); },
  get boolean() { return tr("configurazione.fields.dataTypeExample.boolean"); },
  get date() { return tr("configurazione.fields.dataTypeExample.date"); },
  get datetime() { return tr("configurazione.fields.dataTypeExample.datetime"); },
  get duration() { return tr("configurazione.fields.dataTypeExample.duration"); },
  get money() { return tr("configurazione.fields.dataTypeExample.money"); },
  get url() { return tr("configurazione.fields.dataTypeExample.url"); },
  get enum() { return tr("configurazione.fields.dataTypeExample.enum"); },
  get version() { return tr("configurazione.fields.dataTypeExample.version"); },
  get member() { return tr("configurazione.fields.dataTypeExample.member"); },
  get group() { return tr("configurazione.fields.dataTypeExample.group"); },
  get element() { return tr("configurazione.fields.dataTypeExample.element"); },
  get state() { return tr("configurazione.fields.dataTypeExample.state"); },
};

export const COLOR_LABELS: Record<OptionColor, string> = {
  get ""() { return tr("configurazione.fields.color.none"); },
  get accent() { return tr("configurazione.fields.color.accent"); },
  get ok() { return tr("configurazione.fields.color.ok"); },
  get flag() { return tr("configurazione.fields.color.flag"); },
  get danger() { return tr("configurazione.fields.color.danger"); },
  get muted() { return tr("configurazione.fields.color.muted"); },
  get fg() { return tr("configurazione.fields.color.fg"); },
};
