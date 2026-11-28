import type { Translation } from "../types";
import type it from "../it/errors-ai-questionari";

export default {
  "errors.ai.questionnaire_nothing": "This section has no unanswered questions to suggest answers for.",
  "errors.validation.reviews.section_unknown": "This section does not exist in the template.",
  "errors.validation.reviews.section_hidden": "The section is hidden by the answers given.",
  "errors.validation.reviews.ai_decisions_count": "From 1 to 200 decisions at a time.",
  "errors.validation.reviews.ai_proposal_gone": "The suggestion is gone: the question already has an answer or was suggested again.",
  "errors.validation.reviews.ai_proposal_empty": "The suggestion has no value: answer by hand.",
} satisfies Translation<typeof it>;
