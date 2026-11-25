// Messaggi di validazione della procedura «Coinvolgi un team».
import type { Source } from "../types";

export default {
  "errors.validation.coinvolgi.nothing": "Scegli cosa fa il team: un parere, del lavoro o tutti e due.",
  "errors.validation.coinvolgi.process_type": "Scegli il processo di un tipo di richiesta.",
  "errors.validation.coinvolgi.draft_open": "Il processo ha una bozza non pubblicata: pubblicala o scartala, poi riprova.",
  "errors.validation.coinvolgi.no_version": "Il processo non ha ancora una versione pubblicata.",
  "errors.validation.coinvolgi.phase_unknown": "La fase «{phase}» non esiste nel processo.",
} satisfies Source;
