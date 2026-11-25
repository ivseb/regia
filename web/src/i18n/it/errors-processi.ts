// Errori di validazione dei processi (Configurazione → Processi). Il code arriva dal server
// (engine.InvalidCode); in italiano si mostra il dettaglio del server, qui c'è il testo per l'inglese.
import type { Source } from "../types";

export default {
  "errors.validation.process.priority_required": "La priorità è obbligatoria quando il processo non ha una condizione: vale per tutti gli elementi del tipo.",
  "errors.validation.process.priority_negative": "La priorità è un numero intero, zero o positivo.",
} satisfies Source;
