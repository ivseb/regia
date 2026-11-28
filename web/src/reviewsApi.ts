// API delle valutazioni con questionari (piano, passo 4). Sta in un modulo a parte, caricato
// con le pagine delle valutazioni, così il bundle iniziale non cresce; i tipi sono in api.ts.
import {
  call,
  reviewInbox,
  type AnswerHistory,
  type ElementReviews,
  type FactInfo,
  type TemplateVersion,
  type Problem,
  type QTemplate,
  type ReviewConfig,
  type ReviewTeam,
  type ReviewTeamInput,
  type ReviewType,
  type ReviewTypeInput,
  type ReviewView,
} from "./api";
import type { AIStatus } from "./aiApi";

/** Esito di «Proponi le risposte» (internal/api/reviews_ai.go). */
export type AIProposeResult = { section: string; proposed: number; no_source: number; remaining: number; sources: number; status: AIStatus };
export type AIDecision = { id: string; accept: boolean; value?: unknown };

const q = encodeURIComponent;
const rv = (ref: string) => `/reviews/${q(ref)}`;
const rc = "/review-config";

export const reviewsApi = {
  inbox: reviewInbox,
  forElement: (ref: string) => call<ElementReviews>("GET", `/elements/${q(ref)}/reviews`),
  open: (ref: string, type: string, impact?: string) => call<ReviewView>("POST", `/elements/${q(ref)}/reviews`, { type, impact }),
  get: (ref: string) => call<ReviewView>("GET", rv(ref)),
  saveAnswers: (ref: string, answers: Record<string, unknown>) => call<ReviewView>("PUT", `${rv(ref)}/answers`, { answers }),
  history: (ref: string, question = "") => call<{ items: AnswerHistory[] }>("GET", `${rv(ref)}/history?question=${q(question)}`),
  assign: (ref: string, section: string, member_id: string) =>
    call<ReviewView>("PUT", `${rv(ref)}/assignments/${q(section)}`, { member_id }),
  addPrescription: (ref: string, text: string, category: string) =>
    call<ReviewView>("POST", `${rv(ref)}/prescriptions`, { text, category }),
  updatePrescription: (ref: string, id: string, p: { status?: string; text?: string; category?: string }) =>
    call<ReviewView>("PATCH", `${rv(ref)}/prescriptions/${q(id)}`, p),
  removePrescription: (ref: string, id: string) => call<ReviewView>("DELETE", `${rv(ref)}/prescriptions/${q(id)}`),
  decide: (ref: string, outcome: string, note: string) => call<ReviewView>("POST", `${rv(ref)}/decision`, { outcome, note }),
  aiPropose: (ref: string, section: string) => call<{ view: ReviewView; result: AIProposeResult }>("POST", `${rv(ref)}/ai-proposals`, { section }),
  aiDecide: (ref: string, decisions: AIDecision[]) => call<{ view: ReviewView }>("POST", `${rv(ref)}/ai-proposals/decide`, { decisions }),

  config: () => call<ReviewConfig>("GET", rc),
  exportUrl: () => `/api/v1${rc}/export`,
  createTeam: (t: ReviewTeamInput) => call<ReviewTeam>("POST", `${rc}/teams`, t),
  updateTeam: (id: string, t: ReviewTeamInput) => call<ReviewTeam>("PATCH", `${rc}/teams/${q(id)}`, t),
  createType: (t: ReviewTypeInput) => call<ReviewType>("POST", `${rc}/types`, t),
  updateType: (id: string, t: ReviewTypeInput) => call<ReviewType>("PATCH", `${rc}/types/${q(id)}`, t),
  template: (key: string) => call<QTemplate>("GET", `${rc}/templates/${q(key)}`),
  templateVersion: (key: string, n: number) => call<TemplateVersion>("GET", `${rc}/templates/${q(key)}/versions/${n}`),
  templateFacts: () => call<{ request: FactInfo[]; impact: FactInfo[] }>("GET", `${rc}/template-facts`),
  validateTemplate:(definition: unknown) => call<{ problems: Problem[] }>("POST", `${rc}/templates/validate`, { definition }),
  saveTemplateDraft: (key: string, definition: unknown, notes = "") =>
    call<QTemplate>("PUT", `${rc}/templates/${q(key)}/draft`, { definition, notes }),
  discardTemplateDraft: (key: string) => call<void>("DELETE", `${rc}/templates/${q(key)}/draft`),
  publishTemplate: (key: string, notes = "") => call<QTemplate>("POST", `${rc}/templates/${q(key)}/publish`, { notes }),
};
