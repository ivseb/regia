// Messaggi di validazione dei progetti: piano, dipendenze, avanzamento, cancelli di fase (replica/progetti.md).
import type { Source } from "../types";

export default {
  "errors.validation.projects.dep_kind": "Tipo di dipendenza non ammesso: fine→inizio, inizio→inizio o fine→fine.",
  "errors.validation.projects.dep_lag": "Il ritardo va da 0 a 365 giorni lavorativi.",
  "errors.validation.projects.dep_self": "Una voce non può dipendere da sé stessa.",
  "errors.validation.projects.dep_unknown": "«{ref}» non è una voce di questo progetto.",
  "errors.validation.projects.gate_closed": "La fase «{phase}» ha un cancello da approvare prima di andare avanti.",
  "errors.validation.projects.gate_no_policy": "Nessuna politica di approvazione per i cancelli di fase: configurala in Approvazioni.",
  "errors.validation.projects.gate_off": "La fase non ha un cancello di approvazione.",
  "errors.validation.projects.gate_status": "Il cancello è già in approvazione o approvato.",
  "errors.validation.projects.mode": "Modo di consegna non ammesso: agile, a cascata o fornitore.",
  "errors.validation.projects.no_approvals": "Il motore delle approvazioni non è disponibile.",
  "errors.validation.projects.percent": "La percentuale va da 0 a 100.",
  "errors.validation.projects.progress_milestone": "La milestone non avanza: si segna come raggiunta.",
  "errors.validation.projects.request_not_approved": "La richiesta non è ancora approvata: diventa un progetto dopo l'approvazione.",
  "errors.validation.projects.template": "Modello di progetto non ammesso: agile, a cascata, misto o commessa.",
  "errors.validation.projects.weight": "Il peso deve essere maggiore di zero.",
  "errors.validation.work.epic_agile_only": "L'epic serve solo ai work package agili.",
  "errors.validation.work.wp_no_team": "Il work package non ha un team responsabile.",
} satisfies Source;
