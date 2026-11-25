// Ambiente di prova e promuovi (replica/usabilita/configurazione.md §5): vedi internal/api/prova.go.
import { call, type Definition } from "./api";

export type ProvaRole = "none" | "principale" | "prova";
export type ProvaState = {
  role: ProvaRole;
  linked?: { tenant_id: string; slug: string; name: string };
  with_sample: boolean;
  created_at?: string;
  refreshed_at?: string;
  can_manage: boolean;
};

export type ProvaKind = "field" | "type" | "process" | "gate" | "body" | "catalog";
export type ProvaChange = { attr: string; sub?: string; detail?: string; before: unknown; after: unknown };
export type ProvaItem = {
  kind: ProvaKind;
  key: string;
  name: string;
  change: "added" | "modified" | "removed";
  conflict: boolean;
  changes: ProvaChange[];
  before_def?: Definition;
  after_def?: Definition;
};
export type ProvaDiff = { main_name: string; sandbox_name: string; items: ProvaItem[] };
export type ProvaHistoryRow = {
  id: string;
  promoted_at: string;
  promoted_by: string;
  items: { kind: ProvaKind; key: string; name: string; change: string }[];
  summary: Record<string, number>;
};

export const PROVA_KINDS: ProvaKind[] = ["field", "type", "process", "gate", "body", "catalog"];

export const provaApi = {
  state: () => call<ProvaState>("GET", "/prova"),
  create: (sample: boolean) => call<{ tenant_id: string; slug: string }>("POST", "/prova", { sample }),
  recreate: () => call<{ objects: number }>("POST", "/prova/ricrea"),
  diff: () => call<ProvaDiff>("GET", "/prova/differenze"),
  promote: (items: { kind: ProvaKind; key: string }[]) => call<{ promoted: ProvaItem[] }>("POST", "/prova/promuovi", { items }),
  history: () => call<{ items: ProvaHistoryRow[] }>("GET", "/prova/promozioni"),
};
