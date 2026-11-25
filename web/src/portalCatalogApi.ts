// Catalogo del portale: le voci con il loro modulo a passi e l'instradamento (docs/sviluppo/catalogo-portale.md).
import { call } from "./api";
import type { FieldView } from "./fieldsApi";

export type NamedKey = { key: string; name: string };

/** Campo di un passo come lo vede il richiedente (etichetta vuota per un dato della domanda: vale quella di prodotto). */
export type StepField = { key: string; std?: boolean; label: string; help?: string; required?: boolean; visible_when?: string };
export type StepView = { preset?: "context" | "details" | ""; title?: string; intro?: string; fields: StepField[] };
export type EntryView = {
  key: string;
  label: string;
  description: string;
  category: string;
  icon: string;
  type: string;
  intro?: string;
  title_label?: string;
  desc_label?: string;
  steps: StepView[];
  fields: FieldView[];
};

/** Il modulo come si modifica: le parti vuote seguono il campo del tipo. */
export type FormField = { key: string; std?: boolean; label?: string; help?: string; required?: boolean; visible_when?: string };
export type FormStep = { preset?: "context" | "details" | ""; title?: string; intro?: string; fields: FormField[] };
export type Priority = "low" | "medium" | "high";
export type Route = { when?: string; role?: string; team_id?: string; group_id?: string; priority?: Priority | ""; label?: string };
export type EntryForm = { intro?: string; title_label?: string; desc_label?: string; steps: FormStep[]; routes: Route[] };
export type Entry = {
  id: string;
  key: string;
  label: string;
  description: string;
  category: string;
  icon: string;
  type: string;
  position: number;
  active: boolean;
  group_id: string;
  form: EntryForm;
  draft: EntryForm | null;
  published_at: string | null;
};
export type CatalogConfig = {
  entries: Entry[];
  stored: boolean;
  types: { key: string; name: string; fields: NamedKey[] }[];
  standard_fields: NamedKey[];
  roles: NamedKey[];
  teams: NamedKey[];
  groups: NamedKey[];
  priorities: Priority[];
};
export type EntryPatch = Partial<Pick<Entry, "label" | "description" | "category" | "icon" | "active" | "group_id">> & { form?: EntryForm };
export type EntryPreview = { entry: EntryView; units: NamedKey[]; my_unit: string };

const q = encodeURIComponent;

export const catalogApi = {
  config: () => call<CatalogConfig>("GET", "/portal/catalog"),
  create: (e: { label: string; description?: string; category?: string; icon?: string; type: string }) => call<Entry>("POST", "/portal/catalog", e),
  update: (key: string, p: EntryPatch) => call<Entry>("PUT", `/portal/catalog/${q(key)}`, p),
  publish: (key: string) => call<Entry>("POST", `/portal/catalog/${q(key)}/publish`),
  discard: (key: string) => call<Entry>("DELETE", `/portal/catalog/${q(key)}/draft`),
  preview: (key: string, form: EntryForm) => call<EntryPreview>("POST", `/portal/catalog/${q(key)}/preview`, form),
  reorder: (keys: string[]) => call<void>("PUT", "/portal/catalog/order", { keys }),
  support: (ref: string, comment: string) => call<{ key: string }>("POST", `/portal/support/${q(ref)}`, { comment }),
};
