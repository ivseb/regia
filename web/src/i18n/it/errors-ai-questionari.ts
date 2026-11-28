// Errori dei questionari precompilati dall'AI.
import type { Source } from "../types";

export default {
  "errors.ai.questionnaire_nothing": "In questa sezione non ci sono domande senza risposta da proporre.",
  "errors.validation.reviews.section_unknown": "Sezione inesistente in questo modello.",
  "errors.validation.reviews.section_hidden": "La sezione non si vede con le risposte date.",
  "errors.validation.reviews.ai_decisions_count": "Da 1 a 200 decisioni per volta.",
  "errors.validation.reviews.ai_proposal_gone": "La proposta non c'è più: la domanda ha già una risposta o è stata riproposta.",
  "errors.validation.reviews.ai_proposal_empty": "La proposta non ha un valore: rispondi a mano.",
} satisfies Source;
