// Messaggi di validazione dei controlli vivi: regola di efficacia, proposta e mappatura guidata.
import type { Source } from "../types";

export default {
  "errors.validation.grc.proposal_decision_invalid": "Decisione non ammessa: conferma o non confermare.",
  "errors.validation.grc.proposal_not_available": "Il sistema non ha una proposta da decidere per questo controllo.",
  "errors.validation.grc.proposal_note_required": "Spiega perché non confermi la proposta.",
  "errors.validation.grc.rule_condition_invalid": "Condizione non valida: {detail}",
  "errors.validation.grc.rule_empty": "Scrivi almeno una condizione, «efficace se» o «a rischio se».",
  "errors.validation.grc.rule_fact_unknown": "Il dato «{fact}» non esiste per le regole di efficacia.",
  "errors.validation.grc.rule_source_invalid": "La sorgente «{source}» non è ammessa.",
} satisfies Source;
