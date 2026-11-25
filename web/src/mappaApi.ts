// Mappa d'insieme dei processi (sola lettura): internal/api/mappa.go.
import { call } from "./api";

export type MapItem = {
  kind: "review" | "approval" | "gate" | "action";
  key: string;
  name: string;
  on: "create" | "enter" | "exit" | "transition";
  when?: string;
  scope?: "request" | "impact";
  team?: string;
  mandatory?: boolean;
  rule?: string;
  steps?: string[][];
  missing?: boolean;
};
export type MapPhase = { name: string; category: string; off_path?: boolean; initial?: boolean; items: MapItem[] };
export type MapLink = { kind: "review" | "epic"; scope?: "request" | "impact"; phase: string };
export type MapProcess = {
  key: string;
  name: string;
  type_key: string;
  type_name: string;
  version: number;
  applies_when?: string;
  phases: MapPhase[];
  teams: string[];
  links: MapLink[];
};
export type ProcessMap = { processes: MapProcess[]; unpublished: { key: string; name: string; type_name: string }[] };

export const mappaApi = {
  get: () => call<ProcessMap>("GET", "/mappa-processi"),
};
