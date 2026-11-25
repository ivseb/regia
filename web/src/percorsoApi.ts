// API del percorso di una richiesta o di un impatto (ondata «semplicità», internal/api/percorso.go):
// dove siamo, chi è coinvolto, il lavoro che ne nasce, cosa succede dopo e cosa tocca a chi guarda.
// Una sola richiesta: il server compone tutto dai dati configurati, il client sceglie i testi dai codici.
import { call } from "./api";

const q = encodeURIComponent;

export type PcPhase = { name: string; labels?: Record<string, string>; category: string; off_path?: boolean; status: "done" | "current" | "next" };
export type PcReview = { key: string; title: string; type: string; team: string; stage: string; mandatory: boolean; open: boolean; impact?: string; mine?: boolean };
export type PcApproval = { stage: string; status: string; waiting: string[]; steps_done: number; steps_all: number; can_decide: boolean };
export type PcImpact = { key: string; title: string; application: string; team: string; state: string; state_category: string; current?: boolean };
export type PcWaiting = { kind: "reviews" | "approval"; count?: number; who?: string };
export type PcNext = { to: string; label: string; forward: boolean; roles?: string[]; mine: boolean; blocked: string[]; waiting: PcWaiting[] };
export type PcTrigger = { phase: string; rule: string; kind: string; ref: string; name: string; role: string; when: string; when_met: boolean | null; action_when: string };
export type PcTodo = { kind: string; mine: boolean; key?: string; detail?: string; who?: string; tab?: string; count?: number; link?: string };
export type PcEpic = { key: string; title: string; state: string; team: string | null; application: string | null; progress: { done: number; total: number; percent: number } };
export type PcProgress = { done: number; total: number; percent: number; epics: number; open_epics: number };

export type PcGuide = { id: string; phase: string | null; kind: "document" | "link" | "knowledge"; title: string; target: string; position: number };
export type PcVersionNotice = {
  kind: "deprecated";
  process_key: string;
  process_name: string;
  version: number;
  current_version: number;
  since: string;
  ends_at?: string;
  phase_exists: boolean;
  can_migrate: boolean;
};

export type Percorso = {
  key: string;
  type: "business_request" | "impact";
  title: string;
  request_key: string;
  request_title?: string;
  current: string;
  closed: boolean;
  phases: PcPhase[];
  process?: string;
  reviews: PcReview[];
  approvals: PcApproval[];
  impacts: PcImpact[];
  delivery: { epics: PcEpic[]; progress?: PcProgress } | null;
  next: PcNext[];
  triggers: PcTrigger[];
  todo: PcTodo[];
  guides: PcGuide[];
  version_notice: PcVersionNotice | null;
};

export const percorsoApi = {
  get: (ref: string) => call<Percorso>("GET", `/elements/${q(ref)}/percorso`),
};
