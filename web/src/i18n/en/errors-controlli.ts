import type { Translation } from "../types";
import type it from "../it/errors-controlli";

export default {
  "errors.validation.grc.proposal_decision_invalid": "Decision not allowed: confirm or do not confirm.",
  "errors.validation.grc.proposal_not_available": "The system has no proposal to decide for this control.",
  "errors.validation.grc.proposal_note_required": "Explain why you do not confirm the proposal.",
  "errors.validation.grc.rule_condition_invalid": "Invalid condition: {detail}",
  "errors.validation.grc.rule_empty": "Write at least one condition, “effective if” or “at risk if”.",
  "errors.validation.grc.rule_fact_unknown": "The data “{fact}” does not exist for effectiveness rules.",
  "errors.validation.grc.rule_source_invalid": "The source “{source}” is not allowed.",
} satisfies Translation<typeof it>;
