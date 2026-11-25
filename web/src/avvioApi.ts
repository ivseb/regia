// Primo avvio guidato (replica/usabilita.md, proposta 7B): passi calcolati dal sistema, modelli per tipo
// di azienda con anteprima, cosa è applicato e ripristino dei processi di partenza. Vedi internal/api/avvio.go.
import { call, type Definition } from "./api";

export type StepKey = "organizzazione" | "moduli" | "persone" | "team" | "processo" | "organi" | "norme" | "richiesta";

export type AvvioStep = { key: StepKey; done: boolean; not_needed?: boolean; count: number };
export type AvvioState = { steps: AvvioStep[]; done: number; total: number; closed: boolean; model: string };

export type ModelKey = "banca_assicurazione" | "software_house" | "pmi" | "pa" | "consulente";
export type ModelRow = { key: ModelKey; available: boolean; applied: boolean };

export type ItemKind = "module" | "type" | "process" | "body" | "catalog";
export type AvvioItem = { kind: ItemKind; key: string; name: string; state: "new" | "present"; unverified?: boolean; detail?: number };
export type AvvioPreview = { key: ModelKey; available: boolean; items: AvvioItem[]; new: number; present: number; applied: boolean };

export type AppliedProcess = {
  key: string;
  name: string;
  type: string;
  type_name: string;
  version: number;
  state: "original" | "modified" | "unknown";
  archived: boolean;
  has_draft: boolean;
  restored_at: string | null;
};
export type AppliedCatalog = { key: string; name: string; starter: string; unverified: boolean; verified_at: string | null; verified_by: string | null };
export type Applied = {
  models: { key: string; applied_at: string; applied_by: string | null }[];
  processes: AppliedProcess[];
  catalogs: AppliedCatalog[];
  bodies: { key: string; name: string }[];
  modules: { key: string; name: string; enabled: boolean }[];
};
export type RestorePreview = {
  key: string;
  name: string;
  modified: boolean;
  version: number;
  has_draft: boolean;
  elements: number;
  current: Definition | null;
  original: Definition;
};

// Percorso guidato (internal/api/avvio_percorso.go): le tappe con stato calcolato e le incoerenze trovate.
export type TappaKey = "persone" | "team" | "processi" | "valutazioni" | "spazi" | "moduli";
export type IssueCode =
  | "team_senza_membri"
  | "team_senza_spazio"
  | "processo_non_pubblicato"
  | "processo_di_partenza"
  | "valutazione_team_senza_membri"
  | "valutazione_senza_regola"
  | "regola_valutazione_mancante"
  | "approvazione_senza_politica"
  | "spazio_senza_responsabile";
export type Tappa = {
  key: TappaKey;
  status: "done" | "partial" | "todo";
  optional?: boolean;
  not_needed?: boolean;
  counts: Record<string, number>;
  issues: { code: IssueCode; count: number }[];
  modules?: { key: string; name: string; enabled: boolean }[];
};
export type Percorso = { tappe: Tappa[]; done: number; total: number; fresh: boolean; model: string; closed: boolean; next: string };

export const avvioApi = {
  percorso: () => call<Percorso>("GET", "/avvio/percorso"),
  state: () => call<AvvioState>("GET", "/avvio"),
  close: () => call<AvvioState>("POST", "/avvio/chiudi"),
  reopen: () => call<AvvioState>("DELETE", "/avvio/chiudi"),
  models: () => call<{ items: ModelRow[] }>("GET", "/avvio/modelli"),
  preview: (key: string) => call<AvvioPreview>("GET", `/avvio/modelli/${encodeURIComponent(key)}`),
  apply: (key: string) => call<AvvioPreview>("POST", `/avvio/modelli/${encodeURIComponent(key)}`),
  applied: () => call<Applied>("GET", "/avvio/applicato"),
  restorePreview: (key: string) => call<RestorePreview>("GET", `/avvio/applicato/processi/${encodeURIComponent(key)}`),
  restore: (key: string, fromVersion: number) =>
    call<Applied>("POST", `/avvio/applicato/processi/${encodeURIComponent(key)}/ripristina`, { from_version: fromVersion }),
  verify: (key: string) => call<Applied>("POST", `/avvio/applicato/cataloghi/${encodeURIComponent(key)}/verifica`),
};

export const MODEL_KEYS: ModelKey[] = ["banca_assicurazione", "software_house", "pmi", "pa", "consulente"];
