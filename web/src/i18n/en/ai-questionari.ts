// AI-prefilled questionnaires (feature «questionari»).
import type { Translation } from "../types";
import type it from "../it/ai-questionari";

export default {
  "ai.feature.questionari": "Prefilled questionnaires",
  "ai.feature.questionari.d": "Suggested answers for the questions of a section, each with its source: completed reviews, graph facts, Knowledge pages, attachments.",
  "ai.questionari.propose": "Suggest answers",
  "ai.questionari.proposeHelp": "The AI reads the completed reviews of the same application (and of similar ones), the graph facts, the linked pages and the attachments, and suggests an answer for the questions still empty. Nothing is saved until you accept it.",
  "ai.questionari.working": "Reading the sources and preparing the suggestions…",
  "ai.questionari.result": "{proposed} suggestions with a source, {none} to fill in by hand.",
  "ai.questionari.remaining": "{n} questions left: ask again to get suggestions for them.",
  "ai.questionari.acceptSection": "Accept the section's suggestions ({n})",
  "ai.questionari.confirmTitle": "These questions change the score or generate prescriptions and requirements. Read them again before accepting them all:",
  "ai.questionari.confirmAll": "Accept all",
  "ai.questionari.cancel": "Cancel",
  "ai.questionari.boxLabel": "AI suggestion",
  "ai.questionari.accept": "Accept",
  "ai.questionari.edit": "Edit",
  "ai.questionari.acceptEdited": "Accept with the change",
  "ai.questionari.discard": "Discard",
  "ai.questionari.none": "No source: fill it in by hand.",
  "ai.questionari.confidence.alta": "high confidence",
  "ai.questionari.confidence.media": "medium confidence",
  "ai.questionari.confidence.bassa": "low confidence",
  "ai.questionari.sources": "Sources:",
  "ai.questionari.source.answer": "confirmed answer in {key}",
  "ai.questionari.source.similar": "answer for {app} (similar application) in {key}",
  "ai.questionari.source.page": "page {key} «{title}»",
  "ai.questionari.source.fact": "{key}: {detail}",
  "ai.questionari.source.attachment": "attachment «{name}» of {key}",
  "ai.questionari.careful": "Look at it carefully before accepting it.",
} satisfies Translation<typeof it>;
