// Viste salvate delle liste (piano, passo 16): chiamate al server e tipi.
import { call } from "../api";

export type ViewScope = "private" | "group" | "tenant";
export type ViewKind = "list" | "table" | "board";

/** Impostazioni di presentazione di una vista (JSON libero sul server). */
export type ViewSettings = {
  cols?: string[];
  widths?: Record<string, number>;
  /** Chiave del campo di raggruppamento (vedi groupFields in ElementTable). */
  group?: string;
};

export type SavedView = {
  id: string;
  page: string;
  name: string;
  kind: ViewKind;
  query: string;
  settings: ViewSettings;
  scope: ViewScope;
  group_id: string | null;
  group_name: string | null;
  owner_id: string | null;
  owner_name: string | null;
  mine: boolean;
  can_edit: boolean;
  favorite: boolean;
  default_me: boolean;
  default_tenant: boolean;
  updated_at: string;
};

export type ViewInput = { page: string; name: string; kind: ViewKind; query: string; settings: ViewSettings; scope: ViewScope; group_id?: string | null };
export type ViewPatch = Partial<Omit<ViewInput, "page">>;

export type FavoriteView = { id: string; page: string; name: string; query: string; count: number | null; more: boolean };

const q = encodeURIComponent;

export const viewsApi = {
  list: (page: string) => call<{ items: SavedView[] }>("GET", `/views?page=${q(page)}`),
  get: (id: string) => call<SavedView>("GET", `/views/${q(id)}`),
  create: (v: ViewInput) => call<SavedView>("POST", "/views", v),
  update: (id: string, v: ViewPatch) => call<SavedView>("PATCH", `/views/${q(id)}`, v),
  remove: (id: string) => call<void>("DELETE", `/views/${q(id)}`),
  duplicate: (id: string, name?: string) => call<SavedView>("POST", `/views/${q(id)}/duplicate`, { name: name ?? "" }),
  favorite: (id: string, favorite: boolean) => call<void>("PUT", `/views/${q(id)}/favorite`, { favorite }),
  setDefault: (page: string, viewId: string | null, forWhom: "me" | "tenant") => call<void>("PUT", "/views/default", { page, view_id: viewId ?? "", for: forWhom }),
  favorites: () => call<{ items: FavoriteView[] }>("GET", "/views/favorites"),
  groups: () => call<{ items: { id: string; name: string }[] }>("GET", "/views/groups"),
};
