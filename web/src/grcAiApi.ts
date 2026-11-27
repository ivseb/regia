// GRC assistito (AI, ondata 5). Rispecchia internal/api/ai_grc.go e internal/api/risk_drafts.go.
import { call } from "./api";
import type { AIStatus, Ref } from "./aiApi";
import type { Notification } from "./riskApi";

const q = encodeURIComponent;

export type Coverage = "full" | "partial";

export type GrcProposal = {
  evidence: Ref;
  control: Ref;
  coverage: Coverage;
  reason: string;
  requirements: string[];
  evidence_kind?: string;
  evidence_status?: string;
  can_accept: boolean;
};
export type GrcMissing = { text: string; requirement?: string; scope?: string; source: "regia" | "modello" };
export type GrcMapResult = {
  mode: "evidenza" | "controllo";
  subject: Ref;
  proposals: GrcProposal[];
  missing: GrcMissing[];
  candidates: number;
  unverified?: number;
  text?: string;
  status: AIStatus;
};

export type DocKind = "policy" | "runbook";
export type GrcSource = { kind: "control" | "risk" | "requirement"; id: string; key: string; title: string };
export type GrcDraftResult = { kind: DocKind; source: GrcSource; title: string; text: string; refs: Ref[]; unverified?: number; tools?: string[]; status: AIStatus };
export type GrcSaved = { key: string; title: string; space: string; state: string; controlled: boolean; linked: boolean };

export type FieldStatus = "compilato" | "da_verificare" | "mancante";
export type NoticeField = {
  num: string;
  name: string;
  name_en: string;
  section: number;
  when?: string;
  hint?: string;
  narrative?: boolean;
  value: string;
  status: FieldStatus;
  source?: string;
  by_assistant?: boolean;
};
export type DraftVersion = {
  version: number;
  content: string;
  fields: { num: string; name: string; value: string; status: FieldStatus; source?: string; hint?: string }[];
  source: "assistente" | "persona";
  created_by: string;
  created_at: string;
};
export type GrcNoticeResult = {
  notification: Notification;
  fields: NoticeField[];
  markdown: string;
  missing: number;
  written: number;
  unverified?: number;
  sources: string[];
  versions: DraftVersion[];
  status: AIStatus;
};

export const grcAiApi = {
  map: (subject: { evidence?: string; control?: string }) => call<GrcMapResult>("POST", "/ai/grc/map", subject),
  decide: (p: { evidence: string; control: string; coverage: Coverage; reason: string; accept: boolean }) =>
    call<{ evidence: string; control: string; already: boolean }>("POST", "/ai/grc/map/decide", p),
  draft: (source: string, kind: DocKind, hint?: string) => call<GrcDraftResult>("POST", "/ai/grc/draft", { source, kind, hint }),
  saveDraft: (p: { source: string; kind: DocKind; title: string; text: string; space: string }) => call<GrcSaved>("POST", "/ai/grc/draft/save", p),
  notice: (notification: string) => call<GrcNoticeResult>("POST", "/ai/grc/notice", { notification }),
  saveNotice: (notification: string, fields: { num: string; value: string; status: FieldStatus; source?: string }[]) =>
    call<{ notification: Notification; version: DraftVersion }>("POST", "/ai/grc/notice/save", { notification, fields }),
  versions: (notification: string) => call<{ notification: Notification; items: DraftVersion[] }>("GET", `/regulatory-notifications/${q(notification)}/drafts`),
  exportUrl: (notification: string, version: number, format: "md" | "json") =>
    `/api/v1/regulatory-notifications/${q(notification)}/drafts/${version}/export?format=${format}`,
};
