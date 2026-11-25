// Messaggi di validazione dello spazio dell'auditor.
import type { Source } from "../types";

export default {
  "errors.validation.audit.conclusion_invalid": "Conclusione non ammessa: esaminato o da chiarire.",
  "errors.validation.audit.control_not_in_scope": "Il controllo non è nel perimetro dell'incarico.",
  "errors.validation.audit.engagement_closed": "L'incarico è chiuso: non si può aggiungere altro.",
  "errors.validation.audit.evidence_not_found": "Evidenza inesistente: controlla la chiave.",
  "errors.validation.audit.finding_closed": "Il rilievo è già chiuso.",
  "errors.validation.audit.framework_unknown": "La norma indicata non esiste.",
  "errors.validation.audit.no_assignee": "Il controllo non ha un responsabile: indica a chi chiedere l'evidenza.",
  "errors.validation.audit.note_required": "Scrivi una nota: serve per un esito non conforme o da chiarire e per chiudere un rilievo.",
  "errors.validation.audit.outcome_invalid": "Esito non ammesso: conforme, non conforme o da chiarire.",
  "errors.validation.audit.period_invalid": "La fine del periodo precede l'inizio.",
  "errors.validation.audit.plan_required": "Scrivi il piano d'azione.",
  "errors.validation.audit.request_not_open": "La richiesta non è più aperta.",
  "errors.validation.audit.response_required": "Scrivi la risposta o indica un link o un'evidenza.",
  "errors.validation.audit.sample_empty": "Il controllo non ha evidenze nel periodo da campionare.",
  "errors.validation.audit.sample_size_invalid": "La dimensione del campione va da 1 a 200.",
  "errors.validation.audit.severity_invalid": "Gravità non ammessa: bassa, media, alta o critica.",
} satisfies Source;
