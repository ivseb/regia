// API dei controlli GRC vivi (ondata 3, proposta 4): regola e proposta di efficacia, evidenze dal
// grafo, controlli di un rischio e mappatura guidata requisito ↔ controllo.
import { call } from "./api";
import type { ControlView } from "./grcApi";

const q = encodeURIComponent;

export type ProposalState = "none" | "effective" | "at_risk" | "not_assessed";
export type FactValue = { key: string; value: number | null };
export type EffRule = {
  name: string;
  effective_when: string;
  at_risk_when: string;
  sources: string[];
  active: boolean;
  updated_by: string;
  updated_at: string | null;
};
export type EffRuleInput = { name: string; effective_when: string; at_risk_when: string; sources: string[]; active: boolean };
export type ProposalDecision = {
  id: string;
  proposed: string;
  decision: "confirmed" | "not_confirmed";
  previous: string;
  resulting: string;
  note: string;
  by: string;
  at: string;
  facts: FactValue[];
};
export type Proposal = {
  control: string;
  rule: EffRule | null;
  state: ProposalState;
  proposed: "effective" | "partial" | "";
  official: string;
  pending: boolean;
  dismissed: boolean;
  facts: FactValue[];
  all_facts: FactValue[];
  perimeter: number;
  everywhere: boolean;
  decisions: ProposalDecision[];
  can: { edit: boolean; decide: boolean };
};
export type ProposalBrief = { control: string; state: ProposalState; proposed: string; pending: boolean };

export type RiskControl = {
  key: string;
  title: string;
  owner: string;
  effectiveness: string;
  proposed: string;
  proposal_state: ProposalState;
  valid_evidence: number;
  next_expiry: string | null;
  operational: boolean;
  detail: boolean;
};
export type RiskControlsView = {
  controls: RiskControl[];
  incidents: { key: string; title: string }[];
  cves: { cve: string; severity: string; application: string; exception: number; status: string }[];
};

export type WorkReq = { id: string; ref: string; framework: string; req_ref: string; title: string; summary: string; parent: string };
export type WorkFramework = { key: string; name: string; total: number; missing: number; requirements: WorkReq[] };
export type WorkControl = {
  key: string;
  title: string;
  text: string;
  owner: string;
  frameworks: string[];
  requirements: number;
  effectiveness: string;
};
export type Workbench = { frameworks: WorkFramework[]; controls: WorkControl[]; can: { edit: boolean } };
export type Suggestion = { control: string; score: number; words: string[]; category?: "chapter" | "framework" };

export const livingApi = {
  proposal: (ref: string) => call<Proposal>("GET", `/grc/controls/${q(ref)}/proposal`),
  proposals: () => call<{ items: ProposalBrief[] }>("GET", "/grc/proposals"),
  setRule: (ref: string, r: EffRuleInput) => call<Proposal>("PUT", `/grc/controls/${q(ref)}/proposal/rule`, r),
  removeRule: (ref: string) => call<Proposal>("DELETE", `/grc/controls/${q(ref)}/proposal/rule`),
  decide: (ref: string, body: { decision: "confirm" | "not_confirm"; effectiveness?: string; note: string }) =>
    call<Proposal>("POST", `/grc/controls/${q(ref)}/proposal/decision`, body),
  sync: (control?: string) => call<{ controls: number; created: number }>("POST", "/grc/living/sync", control ? { control } : {}),
  riskControls: (ref: string) => call<RiskControlsView>("GET", `/grc/risks/${q(ref)}/controls`),
  workbench: () => call<Workbench>("GET", "/grc/mapping/workbench"),
  suggest: (requirements: string[]) => call<{ items: Suggestion[] }>("POST", "/grc/mapping/suggestions", { requirements }),
  mapBulk: (control: string, requirements: string[], coverage: "full" | "partial") =>
    call<ControlView>("POST", "/grc/mapping/bulk", { control, requirements, coverage }),
};
