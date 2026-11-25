// Errori della Vista DPO: trattamenti, DPIA e notifica delle violazioni.
import type { Source } from "../types";

export default {
  "errors.validation.privacy.name_required": "Serve un nome per il trattamento.",
  "errors.validation.privacy.dpia_approved": "La DPIA è approvata: riaprila per modificarla.",
  "errors.validation.privacy.dpia_incomplete": "Per metterla in revisione servono la descrizione e la valutazione dei rischi.",
  "errors.validation.privacy.dpia_opinion_required": "Per approvarla serve il parere del DPO.",
  "errors.validation.privacy.dpia_transition": "Da questo stato non si può passare a quello richiesto.",
  "errors.validation.privacy.reason_required": "Indica perché la notifica non è dovuta.",
  "errors.validation.privacy.notified_future": "La notifica non può essere nel futuro.",
} satisfies Source;
