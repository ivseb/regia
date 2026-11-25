// Messaggi di validazione del primo avvio guidato: modelli e ripristino dei processi di partenza.
import type { Source } from "../types";

export default {
  "errors.validation.avvio.model_unavailable": "Questo modello non è ancora disponibile.",
  "errors.validation.avvio.nothing_to_restore": "Il processo è già quello di partenza: non c'è niente da ripristinare.",
  "errors.validation.avvio.restore_invalid": "Il processo di partenza non è compatibile con i dati attuali: correggi prima i riferimenti mancanti.",
} satisfies Source;
