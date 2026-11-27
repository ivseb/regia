// Errori del GRC assistito (AI, ondata 5).
import type { Source } from "../types";

export default {
  "errors.ai.grc_map_subject": "Indica un'evidenza oppure un controllo.",
  "errors.ai.grc_kind_invalid": "Tipo di documento sconosciuto: policy o runbook.",
  "errors.ai.grc_source_invalid": "La bozza nasce da un controllo, da un rischio o da un requisito normativo.",
  "errors.validation.grc.evidence_not_linkable": "Un'evidenza respinta o revocata non si collega.",
  "errors.validation.risk.notification_not_dora": "Il modello DORA vale solo per le notifiche dello schema DORA.",
} satisfies Source;
